package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Wallet call seams — package-level vars so hermetic tests can inject
// "debit succeeded / revert failed" without a live platform gRPC endpoint
// (same seam convention as app.AsyncGo). Production always uses the real
// common.* clients.
var (
	debitWallet  = common.DebitWalletGRPC
	creditWallet = common.CreditWalletGRPC
)

// poolTopupFundSourceAdmin is the credit_pool_fund_events.source value the
// admin topup endpoint stamps on its idempotency rows. It must stay distinct
// from the three topup_* values app/credit_pool_reconcile.go uses as the
// stranded-debit state machine ("topup_stranded", "topup_reconciled",
// "topup_manually_closed"): the background sweep selects on source =
// "topup_stranded", and app.TryFinalizeStrandedTopup only matches those three,
// so a successful admin topup's row is invisible to both — which is what keeps
// a settled credit from being "compensated" a second time.
const poolTopupFundSourceAdmin = "admin_topup"

// poolTopupFundSourceReverted is the terminal credit_pool_fund_events.source
// written when the pool credit failed and the wallet revert SUCCEEDED.
//
// That outcome — the ordinary ceiling rejection — used to persist nothing at
// all: the credit transaction rolled back, so the (tenant_id, key) slot stayed
// free. Re-submitting the same key after the ceiling was raised then found no
// record and credited the pool, while the platform deduped the debit against
// one that had already been refunded: net wallet movement zero against a real
// pool credit. The console drawer only re-mints its key after a SUCCESS, so a
// 409 keeps the key in hand and makes that the likely path.
//
// Like poolTopupFundSourceAdmin it must not be any of the three values the
// stranded state machine uses (app.FundEventSourceStranded / Reconciled /
// ManuallyClosed), or the reconcile sweep would "compensate" an attempt whose
// money is already back in the wallet.
const poolTopupFundSourceReverted = "topup_reverted"

// poolTopupKeyVerdict is how this handler reads the credit_pool_fund_events
// row that already occupies a request's (tenant_id, Idempotency-Key) slot.
//
// "A row exists" is NOT the same question as "my credit already landed". The
// key is chosen by a Reseller-authenticated caller, and the table is shared
// with the platform BillingOutbox funding path and with the stranded-debit
// state machine — migration 031's header says outright that this event_id
// space is also used for hand-typed runbook keys like "smoke-1", and describes
// the symptom of getting the question wrong: 200 success=true replayed=true
// with nothing credited.
type poolTopupKeyVerdict int

const (
	// poolTopupKeySettled — this endpoint's own committed credit, same pool,
	// same amount: a genuine replay.
	poolTopupKeySettled poolTopupKeyVerdict = iota
	// poolTopupKeyAmountMismatch — this endpoint's own credit, but for a
	// different amount. An operator who corrected a typo and resubmitted is
	// asking about an intent that never settled.
	poolTopupKeyAmountMismatch
	// poolTopupKeyReverted — an attempt on this key failed and the debit was
	// refunded. Terminal: nothing is owed, so crediting now would be free.
	poolTopupKeyReverted
	// poolTopupKeyStranded — the stranded-debit state machine owns this key
	// (app.TryFinalizeStrandedTopup / ReconcileStrandedTopups settle it).
	poolTopupKeyStranded
	// poolTopupKeyForeign — somebody else's row. Not this request's replay at
	// any price: treating it as one debits the wallet and credits nothing.
	poolTopupKeyForeign
)

// classifyPoolTopupKey decides what an existing fund-event row means for THIS
// request. Provenance first (source), then identity (pool, amount).
//
// BLIND SPOT: provenance here is a string column, not a foreign key or an
// actor id. Any other writer that stamps source = "admin_topup" on this
// tenant's rows would be read as this endpoint's own credit. Today nothing
// else writes that value (it is defined in this file and used nowhere else);
// nothing structurally prevents a future one.
func classifyPoolTopupKey(evt *repo.CreditPoolFundEvent, poolID, amount int64) poolTopupKeyVerdict {
	switch evt.Source {
	case poolTopupFundSourceAdmin:
		if evt.PoolID != poolID {
			return poolTopupKeyForeign
		}
		if evt.Amount != amount {
			return poolTopupKeyAmountMismatch
		}
		return poolTopupKeySettled
	case poolTopupFundSourceReverted:
		return poolTopupKeyReverted
	case app.FundEventSourceStranded, app.FundEventSourceReconciled, app.FundEventSourceManuallyClosed:
		return poolTopupKeyStranded
	default:
		return poolTopupKeyForeign
	}
}

// poolTopupIdempotencyKey resolves the key for one topup intent and reports
// whether the CALLER supplied it. Both header spellings are accepted and both
// are trimmed — the checkout handler trims (v2_billing.go
// checkoutIdempotencyKey) and a key that dedupes on one endpoint but not the
// other is worth a second pool credit here.
//
// No header means a fresh UUID: see the "what this does NOT guarantee" note on
// TopupCreditPool.
func poolTopupIdempotencyKey(c *gin.Context) (string, bool) {
	if k := strings.TrimSpace(c.GetHeader("Idempotency-Key")); k != "" {
		return k, true
	}
	if k := strings.TrimSpace(c.GetHeader("X-Idempotency-Key")); k != "" {
		return k, true
	}
	return "pool-topup:" + uuid.NewString(), false
}

// poolTopupCollisionEventID derives the event_id under which a stranded debit
// is recorded when the caller's key is already held by a foreign row.
//
// It cannot be the caller's key — that slot is exactly what collided, and
// app.RecordStrandedTopup treats a unique violation as "already recorded", so
// recording under the taken key would silently drop the stranded debit. It is
// a digest rather than a truncation so that two different long keys cannot
// derive the same id, and so the result always fits
// repo.PoolFundEventIDMaxLen. Deterministic, so a retry of the same colliding
// key re-derives it and records nothing new.
func poolTopupCollisionEventID(idemKey string) string {
	sum := sha256.Sum256([]byte(idemKey))
	return "pool-topup-collision:" + hex.EncodeToString(sum[:16])
}

// respondPoolTopupReplay answers a settled replay: 200 with the balance THAT
// credit produced, not a fresh read — the caller is asking what its own topup
// did, and later draws may have moved the live balance since. No metrics write
// for the same reason: event.NewBalance is a historical value and the gauge
// tracks the current balance.
func respondPoolTopupReplay(c *gin.Context, actorID int, tenantID string, pool *repo.TenantCreditPool, amount int64, event *repo.CreditPoolFundEvent) {
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionCreditPoolToppedUp, governance.ResourceCreditPool, int(pool.ID),
		fmt.Sprintf(`{"tenant_id":%q,"amount":%d,"new_balance":%d,"replayed":true}`, tenantID, amount, event.NewBalance)))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenant_id":   tenantID,
			"new_balance": event.NewBalance,
			"max_balance": pool.MaxBalance,
			"replayed":    true,
		},
	})
}

// respondPoolTopupKeyConflict refuses a key whose slot is held by something
// that is not this request's settled credit. Always success:false — the
// console renders data.new_balance whenever success is true, so answering 200
// here is how "nothing was credited" gets reported as "Topup successful".
func respondPoolTopupKeyConflict(c *gin.Context, code, message string) {
	c.JSON(http.StatusConflict, gin.H{
		"success":    false,
		"message":    message,
		"error_code": code,
	})
}

// Reseller-facing admin handlers for tenant credit pools.
// Routes registered under /api/v2/admin/tenants/:id/credit-pool* in
// api-v2-router.go. Auth: middleware.RootJWTAuth (Platform admin / Reseller).
//
// Canonical: ADR 2026-05-18 (tenant-credit-pool) §4.1.

// CreateCreditPool initialises a credit pool row for a tenant.
// Route: POST /api/v2/admin/tenants/:id/credit-pool
// Body:  { max_balance, reset_period, alert_threshold_pct }
//
// 201 with pool body, or 409 if a pool already exists for the tenant.
// `max_balance == -1` means unlimited (relay gate becomes a no-op).
func CreateCreditPool(c *gin.Context) {
	tenantID := c.Param("id")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tenant id required"})
		return
	}
	if _, err := repo.GetTenantByID(tenantID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Tenant not found"})
		return
	}

	if existing, err := repo.GetTenantCreditPool(tenantID); err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"message":    "Credit pool already exists for tenant",
			"error_code": "POOL_ALREADY_EXISTS",
		})
		return
	}

	var req struct {
		MaxBalance        int64  `json:"max_balance"`
		ResetPeriod       string `json:"reset_period"`
		AlertThresholdPct int    `json:"alert_threshold_pct"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request: " + err.Error()})
		return
	}
	if req.MaxBalance < 0 && req.MaxBalance != repo.PoolMaxBalanceUnlimited {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "max_balance must be >= 0 or -1 (unlimited)",
		})
		return
	}

	actorID := c.GetInt("id")
	pool, err := repo.CreateTenantCreditPool(tenantID, actorID, req.MaxBalance, req.ResetPeriod, req.AlertThresholdPct)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create pool: " + err.Error()})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionCreditPoolCreated, governance.ResourceCreditPool, int(pool.ID),
		fmt.Sprintf(`{"tenant_id":%q,"max_balance":%d,"reset_period":%q}`, tenantID, req.MaxBalance, req.ResetPeriod)))

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": pool})
}

// GetCreditPool returns the pool row for a tenant.
// Route: GET /api/v2/admin/tenants/:id/credit-pool
// Returns 404 with ErrPoolNotFound — distinct from the relay-gate semantics
// where absence means "unlimited bypass".
func GetCreditPool(c *gin.Context) {
	tenantID := c.Param("id")
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		if errors.Is(err, repo.ErrPoolNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"success":    false,
				"message":    "Credit pool not configured for tenant",
				"error_code": "POOL_NOT_FOUND",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": pool})
}

// endUserPoolView is the read-only projection returned to EndUsers from
// GET /api/v2/:tenant_slug/credit-pool/me. Strictly whitelisted — never
// uses the full TenantCreditPool struct so admin-only fields (ceiling
// policy, alert thresholds, owner, reset schedule) cannot leak even if
// new fields land on the entity.
//
// Pointers let null serialise for "unlimited" pools (no ceiling = no
// meaningful current/max — the relay gate is a no-op for these).
type endUserPoolView struct {
	CurrentBalance *int64 `json:"current_balance"`
	MaxBalance     *int64 `json:"max_balance"`
	Health         string `json:"health"`
}

// poolHealthForEndUser maps a pool balance to one of four enum values
// safe to render in the EndUser dashboard. Thresholds match the
// front-end CreditPoolDrawer.poolHealth helper so admin + EndUser
// surfaces agree on the same band names.
//
//   - "green"     balance >= 20% of max
//   - "yellow"    balance > 0 but < 20% of max
//   - "red"       balance == 0 (gate is now blocking)
//   - "unlimited" no ceiling configured (relay gate skipped)
func poolHealthForEndUser(pool *repo.TenantCreditPool) string {
	if pool == nil || pool.MaxBalance == repo.PoolMaxBalanceUnlimited {
		return "unlimited"
	}
	if pool.CurrentBalance <= 0 {
		return "red"
	}
	// 20% threshold via integer arithmetic — avoids float drift on
	// tiny ceilings (e.g. max=10 should still report "yellow" at 1).
	if pool.CurrentBalance*5 < pool.MaxBalance {
		return "yellow"
	}
	return "green"
}

// GetCreditPoolForEndUser returns the pool summary for the authenticated
// EndUser. Route: GET /api/v2/:tenant_slug/credit-pool/me.
// Auth:  OIDCAuth — tenantCtx.TenantID is derived from the JWT.
//
// Tier 1.2 (2026-05-19): EndUsers were learning their pool was exhausted
// only by hitting HTTP 402 from the relay gate. This endpoint lets them
// poll proactively so the dashboard can warn before traffic breaks.
//
// Security:
//   - URL tenant_slug MUST resolve to the same tenant as the JWT's
//     org claim — 403 TENANT_MISMATCH otherwise. Without this guard
//     an EndUser with a valid JWT for tenant A could read tenant B's
//     pool by typing the slug.
//   - Response struct (endUserPoolView) is a strict whitelist — admin
//     fields (ceiling policy, owner_user_id, alert_threshold_pct,
//     reset_period, last_topup_at, draws) never serialise.
//   - ErrPoolNotFound returns 200 with the "unlimited" projection, not
//     404 — matches the relay gate semantic where absence = unlimited.
func GetCreditPoolForEndUser(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil || tenantCtx == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Tenant context not found",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	tenant, terr := repo.GetTenantBySlug(c.Param("tenant_slug"))
	if terr != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	if tenant.Id != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "Authenticated tenant does not match URL slug",
			"error_code": "TENANT_MISMATCH",
		})
		return
	}

	pool, perr := repo.GetTenantCreditPool(tenant.Id)
	if perr != nil {
		if errors.Is(perr, repo.ErrPoolNotFound) {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": endUserPoolView{
					CurrentBalance: nil,
					MaxBalance:     nil,
					Health:         "unlimited",
				},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to read credit pool: " + perr.Error(),
			"error_code": "POOL_READ_FAILED",
		})
		return
	}

	view := endUserPoolView{Health: poolHealthForEndUser(pool)}
	// Always expose current_balance — informative even for unlimited.
	cb := pool.CurrentBalance
	view.CurrentBalance = &cb
	if pool.MaxBalance != repo.PoolMaxBalanceUnlimited {
		mb := pool.MaxBalance
		view.MaxBalance = &mb
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// TopupCreditPool funds the pool from the actor's platform wallet.
// Route: POST /api/v2/admin/tenants/:id/credit-pool/topup
// Body:  { amount, reason? }
//
// Flow per ADR §9 Q4 (wallet-debit only — no admin-grant path):
//  1. Lookup pool (must exist).
//  2. Resolve and validate the Idempotency-Key, then read the
//     credit_pool_fund_events row that already holds (tenant_id, key) and
//     classify it (classifyPoolTopupKey). Everything answerable from that row
//     — a settled replay, a key reused for a different amount, a key whose
//     attempt was already refunded, a key colliding with an unrelated row —
//     is answered HERE, before any money moves.
//  3. DebitWalletGRPC(amount) — if it fails, return 402.
//  4. FundPoolIdempotentWithReason(amount) keyed on the same Idempotency-Key
//     the debit used, writing the fund-event row under
//     UNIQUE(tenant_id, event_id) (migration 031) in the same transaction as
//     the balance change. Its ErrFundEventExists arm runs the SAME
//     classification, because that is where the race step 2 cannot see lands
//     — and there the wallet has already been debited: a foreign row means
//     the debit is ours and has to be given back, while a row this endpoint
//     owns means the platform deduped our debit into that intent's.
//     If the credit fails (e.g. ErrPoolWouldExceedCeiling), CreditWalletGRPC
//     reverts. A SUCCESSFUL revert writes a terminal "topup_reverted" row so
//     the key cannot be reused for a free credit; a FAILED revert strands the
//     debit as a "topup_stranded" row the background sweep compensates
//     (app.ReconcileStrandedTopups), with the STRANDED log line as operator
//     context.
//
// What this does NOT guarantee: a caller that sends no Idempotency-Key at all
// gets a fresh UUID per request, so a double-click without the header is two
// intents — two wallet debits and two pool credits. Nothing on the server can
// distinguish that from an operator deliberately funding the same tenant the
// same amount twice, and this endpoint’s caller is a human with the balance
// in front of them, so the ambiguity is resolved by requiring the header
// rather than by guessing (the checkout path, whose caller is a paying
// customer, takes the opposite trade-off — see checkoutIdempotencyKey). The
// console must send a key; see doc/coord/contracts.md.
//
// The two-step is necessarily non-atomic across services, so we accept a
// narrow stranded-debit window — but every stranded debit is durable, metered
// (newhub_credit_pool_stranded_*) and auto-compensated, not log-only. A
// retried request with the same Idempotency-Key settles its own stranded
// event via the claim protocol instead of double-crediting (see
// app.TryFinalizeStrandedTopup).
func TopupCreditPool(c *gin.Context) {
	tenantID := c.Param("id")

	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false, "message": "Credit pool not found", "error_code": "POOL_NOT_FOUND",
		})
		return
	}

	var req struct {
		Amount int64  `json:"amount" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "amount must be a positive integer"})
		return
	}

	actorID := c.GetInt("id")
	actor, err := repo.GetUserById(actorID, false)
	if err != nil || actor == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Actor not found"})
		return
	}
	if actor.LurusAccountID == nil || *actor.LurusAccountID <= 0 {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"success": false, "message": "Actor has no platform wallet (lurus_account_id unset)",
		})
		return
	}
	accountID := *actor.LurusAccountID

	// 1 LB (CNY 1) = currency.LucToLut() quota (QuotaPerUnit/USDExchangeRate:
	// quota is USD-priced; never hardcode 500000 —
	// see currency.go:36), same conversion as quota.go:985 / the v2 transfer /
	// internal topup. The wallet keeps 4 decimals (numeric(14,4)), so a debit
	// is exact only for multiples of LucToLut()/10000 quota; the sub-0.0001
	// remainder of any other amount is lost to rounding, and anything under
	// half that granularity rounds to a 0.0000 debit — free pool credit — so
	// those requests are rejected outright.
	walletAmount := float64(req.Amount) / currency.LucToLut()
	if walletAmount < 0.00005 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "amount " + strconv.FormatInt(req.Amount, 10) + " quota would round to a 0.0000 LB wallet debit (minimum " +
				strconv.FormatInt(int64(currency.LucToLut())/20000, 10) + " quota)",
		})
		return
	}
	// Idempotency key per topup intent (contracts.md S1 / ADR D4 "deterministic
	// business key, never random"). The same key flows to the gRPC debit, to its
	// HTTP twin, and — since the credit was routed through the idempotent
	// primitive — into credit_pool_fund_events.event_id. The revert uses a
	// distinct key so it is never deduped against the debit.
	idemKey, callerSuppliedKey := poolTopupIdempotencyKey(c)

	if callerSuppliedKey {
		// The caller now chooses a value that is PERSISTED, in a VARCHAR(128),
		// inside a transaction that opens after the wallet debit. Validate it
		// here, where a rejection costs nothing; the same value reaching the
		// INSERT would cost a 22001 behind a debit that already happened.
		if verr := repo.ValidateFundEventID(idemKey); verr != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "Idempotency-Key rejected: " + verr.Error(),
				"error_code": "POOL_TOPUP_KEY_INVALID",
			})
			return
		}
		// Read the slot BEFORE debiting. Every verdict except "the stranded
		// state machine owns this key" is answerable without moving money, and
		// answering them here means a colliding or spent key never produces a
		// debit that has to be reverted.
		prior, found, lerr := repo.LookupFundEvent(c.Request.Context(), tenantID, idemKey)
		if lerr != nil {
			// Unknown, not fresh: proceeding would debit the wallet on a key
			// whose history we could not read.
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success":    false,
				"message":    "could not verify the Idempotency-Key against previous topups: " + lerr.Error(),
				"error_code": "POOL_TOPUP_KEY_UNVERIFIABLE",
			})
			return
		}
		if found {
			switch classifyPoolTopupKey(prior, pool.ID, req.Amount) {
			case poolTopupKeySettled:
				respondPoolTopupReplay(c, actorID, tenantID, pool, req.Amount, prior)
				return
			case poolTopupKeyAmountMismatch:
				respondPoolTopupKeyConflict(c, "POOL_TOPUP_KEY_AMOUNT_MISMATCH", fmt.Sprintf(
					"Idempotency-Key %q already settled a topup of %d quota; %d is a different intent and needs a new key",
					idemKey, prior.Amount, req.Amount))
				return
			case poolTopupKeyReverted:
				respondPoolTopupKeyConflict(c, "POOL_TOPUP_KEY_REVERTED", fmt.Sprintf(
					"Idempotency-Key %q was already used by a topup that failed and was refunded; retry with a NEW key",
					idemKey))
				return
			case poolTopupKeyForeign:
				respondPoolTopupKeyConflict(c, "POOL_TOPUP_KEY_COLLISION", fmt.Sprintf(
					"Idempotency-Key %q is already held by an unrelated fund event for this tenant (source %q); retry with a NEW key",
					idemKey, prior.Source))
				return
			case poolTopupKeyStranded:
				// Fall through: settling a stranded intent needs the deduped
				// debit to have happened first (see the branch below).
			}
		}
	}

	debit, derr := debitWallet(
		c.Request.Context(), accountID, walletAmount,
		"pool_topup", "Credit pool topup for tenant "+tenantID, "newhub", idemKey,
	)
	if derr != nil || debit == nil || !debit.Success {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"success":    false,
			"message":    "Wallet debit failed; topup aborted",
			"error_code": "WALLET_DEBIT_FAILED",
		})
		return
	}

	// Retry of a previously-stranded intent (same Idempotency-Key): the debit
	// above was deduped upstream, so the money was only taken once. Settle the
	// stranded event via its claim protocol instead of running a fresh
	// TopupPool — a blind second credit here plus the background reconcile
	// sweep would double-credit a single debit.
	if evt, handled, ferr := app.TryFinalizeStrandedTopup(c.Request.Context(), idemKey, tenantID); handled {
		if ferr != nil {
			status := http.StatusInternalServerError
			code := "POOL_TOPUP_FAILED"
			if errors.Is(ferr, repo.ErrPoolWouldExceedCeiling) {
				status = http.StatusConflict
				code = "POOL_CEILING_EXCEEDED"
			}
			if errors.Is(ferr, app.ErrStrandedTopupManuallyClosed) {
				// The operator already settled this intent by hand (refund or
				// manual credit). The wallet debit for this key was deduped
				// upstream, so crediting the pool now would be free money.
				status = http.StatusConflict
				code = "POOL_TOPUP_INTENT_CLOSED"
			}
			c.JSON(status, gin.H{"success": false, "message": ferr.Error(), "error_code": code})
			return
		}
		metrics.CreditPoolBalance.WithLabelValues(tenantID).Set(float64(evt.NewBalance))
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
			governance.ActionCreditPoolToppedUp, governance.ResourceCreditPool, int(pool.ID),
			fmt.Sprintf(`{"tenant_id":%q,"amount":%d,"new_balance":%d,"reconciled":true}`, tenantID, req.Amount, evt.NewBalance)))
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"tenant_id":   tenantID,
				"new_balance": evt.NewBalance,
				"max_balance": pool.MaxBalance,
				"reconciled":  true,
			},
		})
		return
	}

	// Pool credit, made idempotent on the SAME key the wallet debit was
	// deduped with: repo.FundPoolIdempotentWithReason writes a
	// credit_pool_fund_events row under the UNIQUE(tenant_id, event_id) index
	// from migration 031, in the same transaction as the balance change.
	//
	// Before this the admin path called repo.TopupPool directly, so a retry of
	// an already-SUCCESSFUL topup was deduped by the platform on the debit side
	// and then credited the pool a SECOND time: same key, one debit, two
	// credits. The stranded branch above owns "debit taken, credit never
	// landed"; this owns "credit already landed". Both key off idemKey and both
	// live in credit_pool_fund_events, so exactly one of them can apply to a
	// given (tenant, key) — the stranded branch runs first and returns, and it
	// only matches the three topup_* state values this path never writes.
	event, terr := repo.FundPoolIdempotentWithReason(
		c.Request.Context(), pool.ID, tenantID, req.Amount,
		idemKey, poolTopupFundSourceAdmin, actorID, req.Reason,
	)
	if errors.Is(terr, repo.ErrFundEventExists) {
		if event == nil {
			// Unreachable through repo today (both replay arms carry the row),
			// but this is a money path: dereferencing nil would panic mid
			// request, and falling into the error branch below would revert a
			// wallet debit whose credit DID land.
			c.JSON(http.StatusInternalServerError, gin.H{
				"success":    false,
				"message":    "topup replay detected but the original fund event could not be read; reconcile by hand before retrying",
				"error_code": "POOL_TOPUP_REPLAY_UNREADABLE",
			})
			return
		}
		// The slot was filled between the pre-debit lookup and this credit, so
		// the same classification decides — but now the wallet HAS been debited.
		switch classifyPoolTopupKey(event, pool.ID, req.Amount) {
		case poolTopupKeySettled:
			// A concurrent twin of this request credited first; the platform
			// deduped both debits into one. Answer with that credit.
			respondPoolTopupReplay(c, actorID, tenantID, pool, req.Amount, event)
		case poolTopupKeyForeign:
			// Nothing this endpoint wrote owns the key, so the debit is this
			// request's own money and nothing will ever be credited for it.
			// Give it back; a failed revert strands under a derived id because
			// the caller's slot is exactly what is taken.
			revertPoolTopupDebit(c, accountID, walletAmount, tenantID, pool.ID, req.Amount,
				idemKey, poolTopupCollisionEventID(idemKey), "key slot taken by source "+event.Source)
			respondPoolTopupKeyConflict(c, "POOL_TOPUP_KEY_COLLISION", fmt.Sprintf(
				"Idempotency-Key %q was taken by an unrelated fund event (source %q) while this topup was in flight; nothing was credited — retry with a NEW key",
				idemKey, event.Source))
		default:
			// Amount mismatch, reverted, or stranded: an intent on this same
			// key that THIS endpoint owns got there first. The platform deduped
			// this debit into that intent's, so there is no money of ours to
			// give back — reverting here would refund a debit whose credit (or
			// pending compensation) is someone else's.
			respondPoolTopupKeyConflict(c, "POOL_TOPUP_KEY_IN_USE", fmt.Sprintf(
				"Idempotency-Key %q was claimed by another topup (source %q) while this one was in flight; nothing was credited — retry with a NEW key",
				idemKey, event.Source))
		}
		return
	}
	if terr != nil {
		if revertPoolTopupDebit(c, accountID, walletAmount, tenantID, pool.ID, req.Amount,
			idemKey, idemKey, "pool_err="+terr.Error()) {
			// The money is back, so this key is spent: without a terminal row
			// the (tenant, key) slot stays free, and re-submitting it after the
			// ceiling is raised would credit the pool against a debit the
			// platform dedupes into the one just refunded.
			if merr := repo.RecordTerminalFundEvent(c.Request.Context(), pool.ID, tenantID,
				idemKey, req.Amount, poolTopupFundSourceReverted); merr != nil {
				common.SysError("reverted topup left no terminal fund event — its key can credit the pool for free: " +
					"event_id=" + idemKey + " tenant=" + tenantID + " err=" + merr.Error())
			}
		}
		status := http.StatusInternalServerError
		code := "POOL_TOPUP_FAILED"
		if errors.Is(terr, repo.ErrPoolWouldExceedCeiling) {
			status = http.StatusConflict
			code = "POOL_CEILING_EXCEEDED"
		}
		c.JSON(status, gin.H{"success": false, "message": terr.Error(), "error_code": code})
		return
	}

	newBalance := event.NewBalance
	metrics.CreditPoolBalance.WithLabelValues(tenantID).Set(float64(newBalance))

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionCreditPoolToppedUp, governance.ResourceCreditPool, int(pool.ID),
		fmt.Sprintf(`{"tenant_id":%q,"amount":%d,"new_balance":%d}`, tenantID, req.Amount, newBalance)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenant_id":   tenantID,
			"new_balance": newBalance,
			"max_balance": pool.MaxBalance,
		},
	})
}

// ListCreditPoolUsage returns paginated draw history.
// Route: GET /api/v2/admin/tenants/:id/credit-pool/usage?offset=&limit=
func ListCreditPoolUsage(c *gin.Context) {
	tenantID := c.Param("id")
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false, "message": "Credit pool not found", "error_code": "POOL_NOT_FOUND",
		})
		return
	}

	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	draws, total, err := repo.ListPoolDraws(pool.ID, offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"draws":  draws,
			"total":  total,
			"offset": offset,
			"limit":  limit,
		},
	})
}

// DeleteCreditPool soft-drains the pool: sets max_balance to zero so the
// relay gate blocks new debits, and records a final adjustment draw.
// Route: DELETE /api/v2/admin/tenants/:id/credit-pool
//
// We do NOT hard-delete: the audit ledger references pool_id; keeping the
// row preserves draw history. A future "recreate" path can reset the row.
func DeleteCreditPool(c *gin.Context) {
	tenantID := c.Param("id")
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false, "message": "Credit pool not found", "error_code": "POOL_NOT_FOUND",
		})
		return
	}

	if err := repo.DB.Model(&repo.TenantCreditPool{}).
		Where("id = ?", pool.ID).
		Update("max_balance", 0).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	drained := &repo.TenantCreditPoolDraw{
		PoolID:      pool.ID,
		TenantID:    tenantID,
		Direction:   repo.PoolDrawDirectionDebit,
		Amount:      pool.CurrentBalance,
		Reason:      repo.PoolDrawReasonAdjustment,
		ActorUserID: c.GetInt("id"),
	}
	if err := repo.DB.Create(drained).Error; err != nil {
		common.SysError("DeleteCreditPool: drained-draw insert failed: " + err.Error())
	}

	metrics.CreditPoolBalance.WithLabelValues(tenantID).Set(0)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionCreditPoolDeleted, governance.ResourceCreditPool, int(pool.ID),
		fmt.Sprintf(`{"tenant_id":%q,"drained_balance":%d}`, tenantID, pool.CurrentBalance)))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Credit pool drained"})
}
