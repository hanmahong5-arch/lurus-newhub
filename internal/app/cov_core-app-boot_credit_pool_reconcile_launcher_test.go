package app

// cov_core-app-boot_credit_pool_reconcile_launcher_test.go — coverage for
// StartCreditPoolReconcileWithContext (0%), the only function in
// credit_pool_reconcile.go left untested by credit_pool_reconcile_test.go
// (which drives ReconcileStrandedTopups/RecordStrandedTopup/
// TryFinalizeStrandedTopup directly). Reuses setupReconcileDB/poolBalance
// from that file (same package).
//
// CREDIT_POOL_RECONCILE_INTERVAL_SECONDS only accepts whole seconds, so this
// test's minimum possible tick is 1s; the poll deadline below is generous
// (10s) to stay robust under load without adding a new sub-second-sensitive
// assertion.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestCoreAppBootStartCreditPoolReconcileWithContext_LeaderCompensatesOnTick(t *testing.T) {
	_, pool := setupReconcileDB(t, "t-strand-launcher", 10_000)
	ctx := context.Background()

	if err := RecordStrandedTopup(ctx, "evt-launcher-1", "t-strand-launcher", pool.ID, 400); err != nil {
		t.Fatalf("record stranded: %v", err)
	}

	prevEnv, hadEnv := os.LookupEnv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS")
	os.Setenv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS", "1")
	t.Cleanup(func() {
		if hadEnv {
			os.Setenv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS", prevEnv)
		} else {
			os.Unsetenv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS")
		}
	})

	prevLeader := common.IsLeader()
	common.SetLeader(true)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	runCtx, cancel := context.WithCancel(context.Background())
	StartCreditPoolReconcileWithContext(runCtx)

	deadline := time.Now().Add(10 * time.Second)
	var reconciled bool
	for time.Now().Before(deadline) {
		if poolBalance(t, "t-strand-launcher") == 400 {
			reconciled = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if !reconciled {
		t.Fatalf("expected the leader-gated ticker to compensate the stranded topup within the deadline; final balance = %d", poolBalance(t, "t-strand-launcher"))
	}
	// No further DB access happens in the goroutine after ctx.Done() fires
	// (only ticker.Stop()/SysLog/return), so a brief grace period is enough
	// before the sqlite connection can safely be torn down by t.Cleanup.
	time.Sleep(30 * time.Millisecond)
}

func TestCoreAppBootStartCreditPoolReconcileWithContext_NonLeaderDoesNotCompensate(t *testing.T) {
	_, pool := setupReconcileDB(t, "t-strand-launcher-nonleader", 10_000)
	ctx := context.Background()

	if err := RecordStrandedTopup(ctx, "evt-launcher-2", "t-strand-launcher-nonleader", pool.ID, 400); err != nil {
		t.Fatalf("record stranded: %v", err)
	}

	prevEnv, hadEnv := os.LookupEnv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS")
	os.Setenv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS", "1")
	t.Cleanup(func() {
		if hadEnv {
			os.Setenv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS", prevEnv)
		} else {
			os.Unsetenv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS")
		}
	})

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	runCtx, cancel := context.WithCancel(context.Background())
	StartCreditPoolReconcileWithContext(runCtx)

	// Generous window across >1 tick interval: a non-leader must never
	// compensate, no matter how many ticks fire.
	time.Sleep(2500 * time.Millisecond)
	cancel()

	if got := poolBalance(t, "t-strand-launcher-nonleader"); got != 0 {
		t.Errorf("pool balance = %d, want 0 — a non-leader replica must never run the reconcile sweep", got)
	}
	time.Sleep(30 * time.Millisecond)
}
