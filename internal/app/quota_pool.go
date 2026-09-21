package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	hubnats "github.com/LurusTech/lurus-hub/internal/pkg/nats"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// quota_pool.go — the tenant credit-pool leg of a charge: take from the
// pool, hand back to the pool, and warn when what is left crosses a
// threshold. Split out of quota.go by the cycle-13 wiring pass as a pure
// move (all three functions are byte-identical to the ones that stood in
// quota.go); internal/pkg/gates' source-size ratchet holds quota.go at its
// measured line count, so this cycle's additions to that file are paid for
// by a move rather than by raising the ceiling.
//
// The three belong together and apart from the rest of quota.go: they are
// the only code that touches a tenant's pool balance, and debitTenantPool's
// bool return is what the refund path keys its compensation off — a pool
// that was never debited (no tenant, no pool row, unlimited) must never be
// "handed back".
//
// What stays in quota.go: PostWssConsumeQuota, which
// quota_conservation_c13_test.go parses quota.go by path to prove still
// calls SettleConsume.

// debitTenantPool records the post-consume debit against the token's tenant
// credit pool. Three outcomes (P0-3, ADR 2026-06-10 pool-overdraft):
//
//  1. Normal: DebitPool succeeds — relay_debit draw row.
//  2. Exhausted: the gate admitted a request that out-raced the balance.
//     The user quota and upstream tokens are already spent, so we record the
//     debt via OverdraftDebitPool (balance goes negative, relay_overdraft
//     draw row) instead of dropping the debit. The negative balance keeps
//     the relay gate closed until a topup repays it.
//  3. Hard DB error: the debit is lost — CRITICAL structured log +
//     CreditPoolDebitLostTotal counter (honest residual gap, needs manual
//     reconciliation).
//
// Tokens without a tenant, tenants without a pool row, and unlimited pools
// all skip the debit (pool gate semantics: no pool = unlimited).
//
// Returns whether a debit was actually RECORDED (outcome 1 or 2). The caller
// needs that to decide whether there is anything to hand back when a later
// phase fails: crediting a pool that was skipped — or whose debit was lost to
// a DB error — would invent balance the tenant never spent.
func debitTenantPool(relayInfo *relaycommon.RelayInfo, quota int) bool {
	tok, terr := repo.GetTokenById(relayInfo.TokenId)
	if terr != nil || tok == nil || tok.TenantId == "" {
		return false
	}
	pool, perr := repo.GetTenantCreditPool(tok.TenantId)
	if perr != nil {
		if errors.Is(perr, repo.ErrPoolNotFound) {
			// Distinct from IsUnlimited(): this tenant has NO pool row at all,
			// which is either a legitimate "no pool configured" tenant or a
			// tenant-id/pool drift (orphaned token pointing at a tenant that
			// never got a pool row). Surface it — a silent return here would
			// hide the same class of bug the tenant-id drift incident found.
			metrics.CreditPoolLookupMissTotal.WithLabelValues(tok.TenantId, "no_pool").Inc()
			common.SysLog(fmt.Sprintf(
				`{"event":"pool_lookup_miss","who":"tenant:%s","what":"post-consume debit %d skipped: no credit pool row (token %d)","result":"treated as unlimited, verify tenant-id is not drifted"}`,
				tok.TenantId, quota, relayInfo.TokenId))
			return false
		}
		// Hard DB error resolving the pool row — same severity as a lost debit,
		// since we can't even tell whether this tenant is pool-gated.
		metrics.CreditPoolLookupMissTotal.WithLabelValues(tok.TenantId, "lookup_error").Inc()
		common.SysError(fmt.Sprintf(
			`{"event":"pool_lookup_error","who":"tenant:%s","what":"post-consume debit %d: credit pool lookup failed (token %d)","result":"debit skipped, conservation broken: %s"}`,
			tok.TenantId, quota, relayInfo.TokenId, perr.Error()))
		return false
	}
	if pool == nil || pool.IsUnlimited() {
		return false
	}

	derr := repo.DebitPool(pool.ID, tok.TenantId, int64(quota), relayInfo.TokenId, 0)
	if derr == nil {
		metrics.CreditPoolDebitTotal.WithLabelValues(tok.TenantId).Inc()
		after := pool.CurrentBalance - int64(quota)
		metrics.CreditPoolBalance.WithLabelValues(tok.TenantId).Set(float64(after))
		governance.RecordAuditEvent(governance.NewDetachedAuditEvent(
			tok.TenantId, governance.ActorSystem, relayInfo.UserId,
			governance.ActionBillingDebit, governance.ResourceTenant, int(pool.ID),
			fmt.Sprintf(`{"quota":%d,"pool_id":%d,"token_id":%d}`, quota, pool.ID, relayInfo.TokenId)))
		maybeAlertPoolThreshold(pool, after)
		return true
	}

	if errors.Is(derr, repo.ErrPoolExhausted) {
		newBalance, oerr := repo.OverdraftDebitPool(pool.ID, tok.TenantId, int64(quota), relayInfo.TokenId, 0)
		if oerr == nil {
			metrics.CreditPoolOverdraftTotal.WithLabelValues(tok.TenantId).Inc()
			metrics.CreditPoolBalance.WithLabelValues(tok.TenantId).Set(float64(newBalance))
			common.SysLog(fmt.Sprintf(
				`{"event":"pool_overdraft","who":"tenant:%s","what":"post-consume debit %d on exhausted pool %d (token %d)","result":"recorded as relay_overdraft, new_balance=%d"}`,
				tok.TenantId, quota, pool.ID, relayInfo.TokenId, newBalance))
			maybeAlertPoolThreshold(pool, newBalance)
			return true
		}
		derr = oerr
	}

	// Hard DB error on either path: the debit is dropped. This is the honest
	// residual gap — surface it loudly instead of a quiet info log.
	metrics.CreditPoolDebitLostTotal.Inc()
	common.SysError(fmt.Sprintf(
		`{"event":"pool_debit_lost","who":"tenant:%s","what":"post-consume debit %d on pool %d (token %d) failed","result":"debit NOT recorded, conservation broken: %s"}`,
		tok.TenantId, quota, pool.ID, relayInfo.TokenId, derr.Error()))
	return false
}

// creditTenantPool hands a recorded pool debit back, for the one caller that
// can end up having debited a pool for a request that is then charged to
// nobody: PostConsumeQuota's Phase 3 compensation. It resolves the pool the
// same way debitTenantPool does — through the token's tenant — so the credit
// lands on exactly the pool the debit came from.
//
// Best-effort in the same sense the debit is: it never fails the caller. A
// failed hand-back is a lost credit, so it is logged at the same severity as
// a lost debit rather than dropped quietly.
func creditTenantPool(relayInfo *relaycommon.RelayInfo, quota int, reason string) {
	tok, terr := repo.GetTokenById(relayInfo.TokenId)
	if terr != nil || tok == nil || tok.TenantId == "" {
		common.SysError(fmt.Sprintf(
			`{"event":"pool_credit_unresolved","who":"token:%d","what":"hand-back of %d (%s) could not resolve a tenant","result":"credit NOT recorded, conservation broken"}`,
			relayInfo.TokenId, quota, reason))
		return
	}
	if err := repo.CreditPoolAdjustment(tok.TenantId, quota, reason); err != nil {
		common.SysError(fmt.Sprintf(
			`{"event":"pool_credit_lost","who":"tenant:%s","what":"hand-back of %d (%s) on token %d failed","result":"credit NOT recorded, conservation broken: %s"}`,
			tok.TenantId, quota, reason, relayInfo.TokenId, err.Error()))
	}
}

// maybeAlertPoolThreshold fires the pool-threshold publisher (best-effort)
// when a just-applied debit crosses the pool's alert_threshold_pct and the
// schema dedup window has elapsed since the last fire. It never alters the
// debit result: publisher errors are logged + counted, not returned or
// retried here — hubnats.PublishPoolThreshold's own schema+Redis dedup
// (internal/pkg/nats/pool_threshold.go) remains authoritative; this precheck
// only avoids a DB read on every below-threshold debit.
//
// pool is the pre-debit snapshot read earlier in debitTenantPool; newBalance
// is the post-debit balance from the branch that just committed (normal
// DebitPool: pool.CurrentBalance-quota; overdraft: OverdraftDebitPool's
// returned balance).
func maybeAlertPoolThreshold(pool *repo.TenantCreditPool, newBalance int64) {
	after := *pool
	after.CurrentBalance = newBalance
	if !after.ShouldAlert() {
		return
	}
	if pool.AlertFiredAt != nil && time.Since(*pool.AlertFiredAt) < hubnats.SchemaDedupWindow() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), creditPoolAlertHookTimeout)
	defer cancel()
	fired, delivery, err := publishPoolThresholdSeam(ctx, pool.TenantID, pool.ID, after.CurrentBalance, pool.MaxBalance, pool.AlertThresholdPct)
	if err != nil {
		metrics.CreditPoolAlertHookErrorTotal.Inc()
		common.SysError(fmt.Sprintf(
			`{"event":"pool_threshold_alert_failed","who":"tenant:%s","what":"pool %d threshold publish failed","result":"debit unaffected, alert not delivered: %s"}`,
			pool.TenantID, pool.ID, err.Error()))
	}
	// fired==true means the schema mark (durable dedup) already landed even
	// when err != nil (e.g. the NATS publish itself failed after marking —
	// see hubnats.publishPoolThreshold step 3 vs 4). The dedup window is
	// already consumed either way, so this crossing must leave a durable
	// audit trace now — returning early here would suppress the pool for
	// the whole window with nothing but a log line to show for it.
	if !fired {
		return
	}
	details := fmt.Sprintf(`{"pool_id":%d,"balance":%d,"max_balance":%d,"threshold_pct":%d,"delivery":%q}`,
		pool.ID, after.CurrentBalance, pool.MaxBalance, pool.AlertThresholdPct, delivery)
	if err != nil {
		details = fmt.Sprintf(`{"pool_id":%d,"balance":%d,"max_balance":%d,"threshold_pct":%d,"delivery":%q,"error":%q}`,
			pool.ID, after.CurrentBalance, pool.MaxBalance, pool.AlertThresholdPct, delivery, err.Error())
	}
	governance.RecordAuditEvent(governance.NewDetachedAuditEvent(
		pool.TenantID, governance.ActorSystem, 0,
		governance.ActionBillingPoolThreshold, governance.ResourceTenant, int(pool.ID),
		details))
}
