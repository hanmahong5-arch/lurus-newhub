package app

// credit_pool_reconcile_test.go — money-integrity coverage for the stranded
// topup reconcile loop. Every test hand-computes the expected pool balance:
// a stranded event of amount A must credit the pool by exactly A exactly once,
// no matter how many sweeps, replicas, or client retries touch it.
//
// Hermetic sqlite tier (-short green): DB is glebarez in-memory via
// setupServiceTestDB; the package TestMain (async_seam_test.go) already forces
// AsyncGo inline so no goroutines race the restored globals.

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

// setupReconcileDB builds the hermetic DB with pool + fund-event tables and
// one tenant pool (ceiling maxBalance, starting balance 0). Returns the pool.
func setupReconcileDB(t *testing.T, tenantID string, maxBalance int64) (*gorm.DB, *repo.TenantCreditPool) {
	t.Helper()
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	if err := db.AutoMigrate(&entity.CreditPoolFundEvent{}); err != nil {
		t.Fatalf("migrate fund events: %v", err)
	}
	pool, err := repo.CreateTenantCreditPool(tenantID, 1, maxBalance, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return db, pool
}

func poolBalance(t *testing.T, tenantID string) int64 {
	t.Helper()
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	return pool.CurrentBalance
}

func fundEvent(t *testing.T, db *gorm.DB, eventID string) *repo.CreditPoolFundEvent {
	t.Helper()
	var evt repo.CreditPoolFundEvent
	if err := db.Where("event_id = ?", eventID).First(&evt).Error; err != nil {
		t.Fatalf("read fund event %s: %v", eventID, err)
	}
	return &evt
}

// TestRecordStrandedTopup_PersistsOpenEventIdempotently proves the stranded
// record is durable and that re-recording the same event_id (a client that
// strands twice on the same Idempotency-Key) does not create a second row or
// re-count the stranded metric.
func TestRecordStrandedTopup_PersistsOpenEventIdempotently(t *testing.T) {
	db, pool := setupReconcileDB(t, "t-strand-rec", 10_000)
	ctx := context.Background()

	before := testutil.ToFloat64(metrics.CreditPoolStrandedTotal)

	if err := RecordStrandedTopup(ctx, "evt-strand-1", "t-strand-rec", pool.ID, 500); err != nil {
		t.Fatalf("record stranded: %v", err)
	}
	evt := fundEvent(t, db, "evt-strand-1")
	if evt.Source != FundEventSourceStranded {
		t.Errorf("source = %q, want %q", evt.Source, FundEventSourceStranded)
	}
	if evt.Amount != 500 || evt.NewBalance != 0 {
		t.Errorf("amount/new_balance = %d/%d, want 500/0 (not yet credited)", evt.Amount, evt.NewBalance)
	}

	// Duplicate record: no error, no second row, no metric re-count.
	if err := RecordStrandedTopup(ctx, "evt-strand-1", "t-strand-rec", pool.ID, 500); err != nil {
		t.Fatalf("duplicate record must be a no-op, got: %v", err)
	}
	var count int64
	db.Model(&repo.CreditPoolFundEvent{}).Where("event_id = ?", "evt-strand-1").Count(&count)
	if count != 1 {
		t.Errorf("fund event rows = %d, want 1 (UNIQUE event_id)", count)
	}
	if got := testutil.ToFloat64(metrics.CreditPoolStrandedTotal) - before; got != 1 {
		t.Errorf("stranded_total delta = %v, want 1 (dup not re-counted)", got)
	}

	// Invalid input is rejected, not silently stored.
	if err := RecordStrandedTopup(ctx, "", "t", pool.ID, 100); err == nil {
		t.Error("empty event_id must error")
	}
	if err := RecordStrandedTopup(ctx, "evt-neg", "t", pool.ID, 0); err == nil {
		t.Error("non-positive amount must error")
	}
}

// TestReconcileStrandedTopups_CompensatesOnceOnly is the core money assertion:
// one sweep credits the pool by exactly the stranded amount, closes the event
// and appends a ledger draw; a second sweep changes nothing (no double credit).
func TestReconcileStrandedTopups_CompensatesOnceOnly(t *testing.T) {
	db, pool := setupReconcileDB(t, "t-strand-rc", 10_000)
	ctx := context.Background()

	if err := RecordStrandedTopup(ctx, "evt-rc-1", "t-strand-rc", pool.ID, 700); err != nil {
		t.Fatalf("record stranded: %v", err)
	}

	beforeRec := testutil.ToFloat64(metrics.CreditPoolReconciledTotal)

	reconciled, failed, err := ReconcileStrandedTopups(ctx)
	if err != nil {
		t.Fatalf("reconcile round 1: %v", err)
	}
	if reconciled != 1 || failed != 0 {
		t.Errorf("round 1 = (%d reconciled, %d failed), want (1, 0)", reconciled, failed)
	}
	if got := poolBalance(t, "t-strand-rc"); got != 700 {
		t.Errorf("pool balance = %d, want 700 (0 + 700 compensated)", got)
	}
	evt := fundEvent(t, db, "evt-rc-1")
	if evt.Source != FundEventSourceReconciled {
		t.Errorf("source = %q, want %q", evt.Source, FundEventSourceReconciled)
	}
	if evt.NewBalance != 700 {
		t.Errorf("event new_balance = %d, want 700", evt.NewBalance)
	}
	// Conservation law: the compensation must appear in the draws ledger.
	draws, _, derr := repo.ListPoolDraws(pool.ID, 0, 50)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	foundCredit := false
	for _, d := range draws {
		if d.Direction == repo.PoolDrawDirectionCredit && d.Amount == 700 {
			foundCredit = true
		}
	}
	if !foundCredit {
		t.Error("no credit draw row for the compensation — ledger conservation broken")
	}

	// Round 2: idempotency. Nothing left to do, balance must not move.
	reconciled2, failed2, err := ReconcileStrandedTopups(ctx)
	if err != nil {
		t.Fatalf("reconcile round 2: %v", err)
	}
	if reconciled2 != 0 || failed2 != 0 {
		t.Errorf("round 2 = (%d, %d), want (0, 0)", reconciled2, failed2)
	}
	if got := poolBalance(t, "t-strand-rc"); got != 700 {
		t.Errorf("double credit detected: balance = %d, want 700", got)
	}
	if got := testutil.ToFloat64(metrics.CreditPoolReconciledTotal) - beforeRec; got != 1 {
		t.Errorf("reconciled_total delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.CreditPoolStrandedOpen); got != 0 {
		t.Errorf("stranded_open gauge = %v, want 0 after full drain", got)
	}
}

// TestReconcileStrandedTopups_CeilingFailureKeepsStranded proves the
// "keep state on persistent failure" branch: when the compensating credit
// cannot land (pool ceiling), the event stays stranded — including the claim
// rollback — and a later sweep succeeds once the ceiling is raised.
func TestReconcileStrandedTopups_CeilingFailureKeepsStranded(t *testing.T) {
	db, pool := setupReconcileDB(t, "t-strand-ceil", 100) // ceiling 100 < amount 500
	ctx := context.Background()

	if err := RecordStrandedTopup(ctx, "evt-ceil-1", "t-strand-ceil", pool.ID, 500); err != nil {
		t.Fatalf("record stranded: %v", err)
	}

	reconciled, failed, err := ReconcileStrandedTopups(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if reconciled != 0 || failed != 1 {
		t.Errorf("= (%d reconciled, %d failed), want (0, 1)", reconciled, failed)
	}
	if got := poolBalance(t, "t-strand-ceil"); got != 0 {
		t.Errorf("balance = %d, want 0 (credit must not land past ceiling)", got)
	}
	// The claim must have rolled back with the failed credit.
	evt := fundEvent(t, db, "evt-ceil-1")
	if evt.Source != FundEventSourceStranded {
		t.Errorf("source = %q, want still %q (claim rolled back)", evt.Source, FundEventSourceStranded)
	}
	if got := testutil.ToFloat64(metrics.CreditPoolStrandedOpen); got != 1 {
		t.Errorf("stranded_open gauge = %v, want 1 while unresolved", got)
	}

	// Operator raises the ceiling → next sweep settles it.
	if err := db.Model(&repo.TenantCreditPool{}).Where("id = ?", pool.ID).
		Update("max_balance", 1000).Error; err != nil {
		t.Fatalf("raise ceiling: %v", err)
	}
	reconciled2, failed2, err := ReconcileStrandedTopups(ctx)
	if err != nil {
		t.Fatalf("reconcile after raise: %v", err)
	}
	if reconciled2 != 1 || failed2 != 0 {
		t.Errorf("after raise = (%d, %d), want (1, 0)", reconciled2, failed2)
	}
	if got := poolBalance(t, "t-strand-ceil"); got != 500 {
		t.Errorf("balance = %d, want 500", got)
	}
}

// TestTryFinalizeStrandedTopup covers the online-retry claim protocol:
// unknown key falls through to the normal path; a stranded key settles the
// event; a second call is a replay that must not credit again; and a key
// belonging to another tenant is invisible (no cross-tenant settlement).
func TestTryFinalizeStrandedTopup(t *testing.T) {
	_, pool := setupReconcileDB(t, "t-strand-fin", 10_000)
	ctx := context.Background()

	// Unknown key → not handled, caller proceeds with normal topup.
	if _, handled, err := TryFinalizeStrandedTopup(ctx, "no-such-key", "t-strand-fin"); handled || err != nil {
		t.Fatalf("unknown key: handled=%v err=%v, want (false, nil)", handled, err)
	}

	if err := RecordStrandedTopup(ctx, "evt-fin-1", "t-strand-fin", pool.ID, 300); err != nil {
		t.Fatalf("record stranded: %v", err)
	}

	// Wrong tenant → invisible: the retry must not settle another tenant's event.
	if _, handled, _ := TryFinalizeStrandedTopup(ctx, "evt-fin-1", "t-other"); handled {
		t.Error("cross-tenant event must not be handled")
	}

	// First retry settles: pool credited exactly once.
	evt, handled, err := TryFinalizeStrandedTopup(ctx, "evt-fin-1", "t-strand-fin")
	if !handled || err != nil {
		t.Fatalf("settle: handled=%v err=%v, want (true, nil)", handled, err)
	}
	if evt.NewBalance != 300 {
		t.Errorf("new_balance = %d, want 300", evt.NewBalance)
	}
	if got := poolBalance(t, "t-strand-fin"); got != 300 {
		t.Errorf("balance = %d, want 300", got)
	}

	// Second retry is a pure replay — no additional credit.
	evt2, handled2, err := TryFinalizeStrandedTopup(ctx, "evt-fin-1", "t-strand-fin")
	if !handled2 || err != nil {
		t.Fatalf("replay: handled=%v err=%v, want (true, nil)", handled2, err)
	}
	if evt2.NewBalance != 300 {
		t.Errorf("replay new_balance = %d, want 300", evt2.NewBalance)
	}
	if got := poolBalance(t, "t-strand-fin"); got != 300 {
		t.Errorf("replay double-credited: balance = %d, want 300", got)
	}

	// A subsequent background sweep must also find nothing to do.
	reconciled, failed, err := ReconcileStrandedTopups(ctx)
	if err != nil || reconciled != 0 || failed != 0 {
		t.Errorf("sweep after online settle = (%d, %d, %v), want (0, 0, nil)", reconciled, failed, err)
	}
	if got := poolBalance(t, "t-strand-fin"); got != 300 {
		t.Errorf("sweep double-credited: balance = %d, want 300", got)
	}
}
