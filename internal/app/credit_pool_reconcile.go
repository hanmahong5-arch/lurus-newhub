// Package app — stranded credit-pool topup reconciliation.
//
// TopupCreditPool is necessarily a two-step cross-service flow: (1) platform
// wallet debit, (2) local pool credit. When (2) fails the handler reverts the
// wallet; when the revert ALSO fails the money has left the wallet without
// reaching the pool — a "stranded debit". Historically that case was log-only
// (grep-and-pray). This file closes the loop:
//
//   - RecordStrandedTopup persists the stranded intent as a
//     credit_pool_fund_events row with Source = "topup_stranded". The table's
//     UNIQUE(tenant_id, event_id) makes recording idempotent per tenant.
//   - ReconcileStrandedTopups (background sweep, leader-gated) retries the
//     pool credit per event and flips Source to "topup_reconciled" on success.
//   - TryFinalizeStrandedTopup lets an online client retry (same
//     Idempotency-Key) settle its own stranded event instead of blindly
//     crediting again — the wallet debit was deduped upstream, so a blind
//     TopupPool plus a later sweep would double-credit one debit.
//
// State machine (encoded in the existing Source varchar — the fund-events
// table has no dedicated status column and migrations 023/024 are reserved by
// other work; a first-class status column can land in a later migration and
// backfill from these values):
//
//	"topup_stranded"   → open: wallet debited, pool not credited, NewBalance=0
//	"topup_reconciled" → closed: pool credited, NewBalance = post-credit balance
//
// Every transition is a conditional UPDATE guarded by `source =
// 'topup_stranded'` inside the same DB transaction as the pool credit, so at
// most one actor (sweep tick, replica, or online retry) ever applies the
// compensating credit for a given (tenant_id, event_id).
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"gorm.io/gorm"
)

// Fund-event Source values used as the stranded-topup state machine.
// Regular fund events (platform BillingOutbox path) carry caller-supplied
// sources such as "platform-billing-outbox" and never collide with these.
const (
	FundEventSourceStranded   = "topup_stranded"
	FundEventSourceReconciled = "topup_reconciled"
)

// creditPoolReconcileDefaultInterval is the sweep period when
// CREDIT_POOL_RECONCILE_INTERVAL_SECONDS is unset. Minutes-scale is right:
// stranded events are rare, and the compensating credit is idempotent so a
// missed tick only delays (never loses) money.
const creditPoolReconcileDefaultInterval = 5 * time.Minute

// creditPoolReconcileBatchSize bounds one sweep so a pathological backlog
// cannot hold a long transaction chain; the next tick continues the drain.
const creditPoolReconcileBatchSize = 100

// errFundEventNotStranded is returned by finalizeStrandedTopup when the
// conditional claim matched zero rows — another actor already closed the
// event (or it never was stranded). Callers treat it as "nothing to do".
var errFundEventNotStranded = errors.New("fund event is not in stranded state")

// strandedRetryFailures tracks consecutive compensation failures per
// (tenant_id, event_id) across sweep ticks so the log line carries an
// escalation signal. In-memory by design: the durable state is the stranded
// row itself, the counter only enriches operator logs and resets on restart.
// Keyed by the composite because event_id alone is no longer unique across
// tenants (migration 031) — a plain event_id key would conflate two
// different tenants' unrelated failure streaks.
var strandedRetryFailures = struct {
	sync.Mutex
	counts map[string]int
}{counts: map[string]int{}}

// strandedFailureKey builds the strandedRetryFailures map key for one
// stranded event. Exported-free helper (lowercase) — this counter is purely
// an operator-log aid, not part of the durable idempotency state.
func strandedFailureKey(tenantID, eventID string) string {
	return tenantID + "|" + eventID
}

// RecordStrandedTopup persists a stranded wallet debit as an open fund event.
// Called by the topup handler when the pool credit AND the wallet revert both
// failed. Idempotent via UNIQUE(tenant_id, event_id): a duplicate record
// attempt for this tenant (e.g. a client retry that strands again on the same
// Idempotency-Key) is a no-op.
//
// NewBalance is 0 while stranded — the pool was NOT credited; the field is
// filled with the real post-credit balance when the event is reconciled.
func RecordStrandedTopup(ctx context.Context, eventID, tenantID string, poolID, amount int64) error {
	if eventID == "" || amount <= 0 {
		return fmt.Errorf("record stranded topup: event_id required and amount must be positive (got event_id=%q amount=%d)", eventID, amount)
	}
	evt := &repo.CreditPoolFundEvent{
		EventID:    eventID,
		TenantID:   tenantID,
		PoolID:     poolID,
		Amount:     amount,
		NewBalance: 0,
		Source:     FundEventSourceStranded,
		CreatedAt:  time.Now(),
	}
	if err := repo.DB.WithContext(ctx).Create(evt).Error; err != nil {
		if isUniqueViolationErr(err) {
			// Already recorded (or already reconciled) — nothing new to track.
			return nil
		}
		return fmt.Errorf("record stranded topup event: %w", err)
	}
	metrics.CreditPoolStrandedTotal.Inc()
	metrics.CreditPoolStrandedOpen.Inc()
	return nil
}

// TryFinalizeStrandedTopup is the online-retry entry point. The topup handler
// calls it after a successful wallet debit: if this Idempotency-Key has a
// stranded event for the same tenant, the debit was already taken once (the
// retry's debit was deduped upstream), so the correct action is to settle the
// stranded credit — NOT to run a fresh TopupPool, which would double-credit
// once the background sweep also fires.
//
// Returns:
//   - (nil, false, nil) — no stranded/reconciled event for this key+tenant;
//     the caller proceeds with the normal topup path.
//   - (evt, true, nil)  — the event is now (or already was) reconciled;
//     evt.NewBalance is the settled balance. Caller responds 200.
//   - (evt, true, err)  — the event exists but compensation failed again
//     (e.g. pool ceiling). It remains stranded; caller maps err to a status.
func TryFinalizeStrandedTopup(ctx context.Context, eventID, tenantID string) (*repo.CreditPoolFundEvent, bool, error) {
	var evt repo.CreditPoolFundEvent
	err := repo.DB.WithContext(ctx).
		Where("event_id = ? AND tenant_id = ? AND source IN ?", eventID, tenantID,
			[]string{FundEventSourceStranded, FundEventSourceReconciled}).
		First(&evt).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lookup stranded topup event: %w", err)
	}

	if evt.Source == FundEventSourceReconciled {
		return &evt, true, nil // idempotent replay — already settled
	}

	settled, ferr := finalizeStrandedTopup(ctx, eventID, tenantID)
	if ferr == nil {
		return settled, true, nil
	}
	if errors.Is(ferr, errFundEventNotStranded) {
		// Lost the race to a concurrent sweep tick — re-read the closed row.
		// Scoped by tenant_id: event_id alone is no longer unique across
		// tenants (migration 031), so an unscoped re-read could return a
		// different tenant's row that happens to share this event_id.
		var closed repo.CreditPoolFundEvent
		if rerr := repo.DB.WithContext(ctx).Where("event_id = ? AND tenant_id = ?", eventID, tenantID).First(&closed).Error; rerr != nil {
			return nil, true, fmt.Errorf("stranded event closed concurrently but re-read failed: %w", rerr)
		}
		return &closed, true, nil
	}
	return &evt, true, ferr
}

// finalizeStrandedTopup applies the compensating pool credit for one stranded
// event and closes it, all inside a single DB transaction. tenantID scopes
// every query on credit_pool_fund_events: event_id alone is no longer unique
// across tenants (migration 031 — idempotency is per (tenant_id, event_id)),
// so an unscoped WHERE could claim/credit/rewrite a DIFFERENT tenant's
// stranded row that happens to share this event_id.
//
//  1. Conditional claim: UPDATE ... SET source='topup_reconciled'
//     WHERE event_id=? AND tenant_id=? AND source='topup_stranded'. Zero
//     rows ⇒ another actor already owns/closed it ⇒ errFundEventNotStranded,
//     no credit.
//  2. Pool credit with the same ceiling guard as TopupPool. Failure rolls
//     the whole transaction back — including the claim — so the event
//     stays stranded for the next sweep.
//  3. Record the post-credit balance on the event + append a credit draw
//     row so the pool ledger conservation law (seed − Σdraws == balance)
//     keeps holding.
//
// Concurrency: the claim UPDATE takes the row lock (PostgreSQL) for the rest
// of the transaction; a concurrent claimant blocks, then sees source ≠
// 'topup_stranded' and backs off. SQLite (hermetic test tier) serialises
// writers entirely. Either way at most one credit per (tenant_id, event_id).
func finalizeStrandedTopup(ctx context.Context, eventID, tenantID string) (*repo.CreditPoolFundEvent, error) {
	var settled repo.CreditPoolFundEvent
	txErr := repo.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := tx.Model(&repo.CreditPoolFundEvent{}).
			Where("event_id = ? AND tenant_id = ? AND source = ?", eventID, tenantID, FundEventSourceStranded).
			Update("source", FundEventSourceReconciled)
		if claim.Error != nil {
			return fmt.Errorf("claim stranded event: %w", claim.Error)
		}
		if claim.RowsAffected == 0 {
			return errFundEventNotStranded
		}

		var evt repo.CreditPoolFundEvent
		if err := tx.Where("event_id = ? AND tenant_id = ?", eventID, tenantID).First(&evt).Error; err != nil {
			return fmt.Errorf("load claimed stranded event: %w", err)
		}

		// Compensating credit — same conditional ceiling guard as TopupPool.
		credit := tx.Model(&repo.TenantCreditPool{}).
			Where(
				"id = ? AND (max_balance = ? OR current_balance + ? <= max_balance)",
				evt.PoolID, repo.PoolMaxBalanceUnlimited, evt.Amount,
			).
			Updates(map[string]interface{}{
				"current_balance": gorm.Expr("current_balance + ?", evt.Amount),
				"updated_at":      time.Now(),
			})
		if credit.Error != nil {
			return fmt.Errorf("stranded compensation credit: %w", credit.Error)
		}
		if credit.RowsAffected == 0 {
			// Pool missing or ceiling exceeded — roll back the claim too.
			return repo.ErrPoolWouldExceedCeiling
		}

		var pool repo.TenantCreditPool
		if err := tx.Select("current_balance").Where("id = ?", evt.PoolID).First(&pool).Error; err != nil {
			return fmt.Errorf("stranded compensation readback: %w", err)
		}

		if err := tx.Model(&repo.CreditPoolFundEvent{}).
			Where("event_id = ? AND tenant_id = ?", eventID, tenantID).
			Update("new_balance", pool.CurrentBalance).Error; err != nil {
			return fmt.Errorf("record reconciled balance: %w", err)
		}

		draw := &repo.TenantCreditPoolDraw{
			PoolID:    evt.PoolID,
			TenantID:  evt.TenantID,
			Direction: repo.PoolDrawDirectionCredit,
			Amount:    evt.Amount,
			Reason:    repo.PoolDrawReasonTopup,
			CreatedAt: time.Now(),
		}
		if err := tx.Create(draw).Error; err != nil {
			return fmt.Errorf("stranded compensation draw insert: %w", err)
		}

		evt.Source = FundEventSourceReconciled
		evt.NewBalance = pool.CurrentBalance
		settled = evt
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}

	metrics.CreditPoolReconciledTotal.Inc()
	metrics.CreditPoolBalance.WithLabelValues(settled.TenantID).Set(float64(settled.NewBalance))
	strandedRetryFailures.Lock()
	delete(strandedRetryFailures.counts, strandedFailureKey(tenantID, eventID))
	strandedRetryFailures.Unlock()
	return &settled, nil
}

// ReconcileStrandedTopups runs one sweep: load open stranded events (bounded
// batch) and attempt the compensating credit for each. Returns the number of
// events reconciled and the number that failed again this pass. Events whose
// compensation keeps failing stay stranded with a rising consecutive-failure
// count in the logs; the newhub_credit_pool_stranded_open gauge stays > 0 so
// the alert keeps firing until a human intervenes (e.g. raises the ceiling).
func ReconcileStrandedTopups(ctx context.Context) (reconciled int, failed int, err error) {
	// event_id alone no longer identifies a unique row (migration 031), so the
	// sweep must carry tenant_id alongside it into finalizeStrandedTopup —
	// otherwise two tenants' stranded events sharing an event_id would race
	// each other's claim/credit inside the same call.
	var refs []struct {
		EventID  string
		TenantID string
	}
	if lerr := repo.DB.WithContext(ctx).Model(&repo.CreditPoolFundEvent{}).
		Where("source = ?", FundEventSourceStranded).
		Order("id ASC").
		Limit(creditPoolReconcileBatchSize).
		Select("event_id, tenant_id").
		Find(&refs).Error; lerr != nil {
		return 0, 0, fmt.Errorf("list stranded topup events: %w", lerr)
	}

	for _, ref := range refs {
		id, tenantID := ref.EventID, ref.TenantID
		settled, ferr := finalizeStrandedTopup(ctx, id, tenantID)
		switch {
		case ferr == nil:
			reconciled++
			common.SysLog(fmt.Sprintf(
				"credit-pool reconcile: settled stranded topup event_id=%s tenant=%s amount=%d new_balance=%d",
				id, settled.TenantID, settled.Amount, settled.NewBalance))
		case errors.Is(ferr, errFundEventNotStranded):
			// Closed by a concurrent actor between listing and claiming — fine.
		default:
			failed++
			key := strandedFailureKey(tenantID, id)
			strandedRetryFailures.Lock()
			strandedRetryFailures.counts[key]++
			attempts := strandedRetryFailures.counts[key]
			strandedRetryFailures.Unlock()
			common.SysError(fmt.Sprintf(
				"credit-pool reconcile: stranded topup still failing event_id=%s tenant=%s consecutive_failures=%d err=%v",
				id, tenantID, attempts, ferr))
		}
	}

	refreshStrandedOpenGauge(ctx)
	return reconciled, failed, nil
}

// refreshStrandedOpenGauge recomputes newhub_credit_pool_stranded_open from
// the table so the gauge is authoritative after every sweep (and self-heals
// any drift from the handler-side Inc, e.g. across process restarts).
func refreshStrandedOpenGauge(ctx context.Context) {
	var open int64
	if err := repo.DB.WithContext(ctx).Model(&repo.CreditPoolFundEvent{}).
		Where("source = ?", FundEventSourceStranded).
		Count(&open).Error; err != nil {
		common.SysError("credit-pool reconcile: count open stranded events: " + err.Error())
		return
	}
	metrics.CreditPoolStrandedOpen.Set(float64(open))
}

// StartCreditPoolReconcileWithContext launches the periodic stranded-topup
// sweep. Registered from cmd/server on master-capable nodes (matching the
// audit-cleanup / secret-rotation convention); each tick is additionally
// leader-gated so exactly one replica compensates — correctness does not
// depend on that gate (the per-event conditional claim does), it just avoids
// redundant DB writes. Interval override:
// CREDIT_POOL_RECONCILE_INTERVAL_SECONDS (default 300s).
func StartCreditPoolReconcileWithContext(ctx context.Context) {
	interval := creditPoolReconcileDefaultInterval
	if raw := os.Getenv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS"); raw != "" {
		if secs, perr := strconv.Atoi(raw); perr == nil && secs > 0 {
			interval = time.Duration(secs) * time.Second
		}
	}
	common.SysLog(fmt.Sprintf("credit-pool stranded reconcile started, interval=%s", interval))

	ticker := time.NewTicker(interval)
	common.SafeGoWithContext(ctx, func(c context.Context) {
		defer ticker.Stop()
		for {
			select {
			case <-c.Done():
				common.SysLog("credit-pool stranded reconcile stopped")
				return
			case <-ticker.C:
				if !common.IsLeader() {
					continue
				}
				if _, _, err := ReconcileStrandedTopups(c); err != nil {
					common.SysError("credit-pool reconcile sweep: " + err.Error())
				}
			}
		}
	})
}

// isUniqueViolationErr mirrors the repo-internal unique-violation check for
// PostgreSQL (23505 / duplicate key) and SQLite (hermetic test tier).
func isUniqueViolationErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "duplicate key value") ||
		strings.Contains(s, "UNIQUE constraint failed") ||
		strings.Contains(s, "23505")
}
