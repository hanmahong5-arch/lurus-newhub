package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// A panic in one reconcile pass used to end the ticker goroutine (the only
// recovery was a one-shot SafeGoWithContext around the whole loop), so stranded
// wallet debits stopped being compensated until the pod restarted. The next
// tick must still run.
func TestCreditPoolReconcile_PanicInTickDoesNotKillLoop(t *testing.T) {
	setupReconcileDB(t, "t-panic-loop", 10_000)
	t.Setenv("CREDIT_POOL_RECONCILE_INTERVAL_SECONDS", "1")

	prevLeader := common.IsLeader()
	common.SetLeader(true)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	var calls atomic.Int32
	prevSeam := resetDuePoolsSeam
	resetDuePoolsSeam = func(ctx context.Context) ([]repo.PoolResetResult, error) {
		if calls.Add(1) == 1 {
			panic("first reset sweep dies")
		}
		return nil, nil
	}
	t.Cleanup(func() { resetDuePoolsSeam = prevSeam })

	ctx, cancel := context.WithCancel(context.Background())
	StartCreditPoolReconcileWithContext(ctx)
	deadline := time.Now().Add(4 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got < 2 {
		t.Fatalf("reset sweep ran %d time(s), want >=2 — the loop died after the first panic", got)
	}
}
