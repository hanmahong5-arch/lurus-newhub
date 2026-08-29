package lifecycle

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestLeaderTask_CatchUpRunOnAcquire pins the failover catch-up: with a long
// work interval (1h here, 24h for secret rotation in production), fn must run
// promptly when this process first becomes leader instead of waiting a full
// interval. Before this fix Run was ticker-only, so on a cluster that
// redeploys more often than the interval the task could NEVER fire — this
// test fails on that implementation (zero runs within the wait window).
func TestLeaderTask_CatchUpRunOnAcquire(t *testing.T) {
	var runs atomic.Int64
	var leader atomic.Bool

	task := NewLeaderTask("catchup-task", time.Hour, func(ctx context.Context) error {
		runs.Add(1)
		return nil
	})
	task.isLeader = leader.Load
	task.poll = 5 * time.Millisecond // production: min(interval, 30s)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = task.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Not leader: no catch-up run.
	time.Sleep(30 * time.Millisecond)
	if got := runs.Load(); got != 0 {
		t.Fatalf("expected 0 runs while not leader, got %d", got)
	}

	// Become leader: fn must run within a few poll ticks, hours before the
	// interval elapses.
	leader.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runs.Load(); got == 0 {
		t.Fatal("expected a catch-up run promptly after acquiring leadership, got 0 runs")
	}
}

// TestLeaderTask_NoRerunOnLeadershipFlap pins the other half of the contract:
// the catch-up run happens once per process, not once per acquisition. Losing
// and regaining leadership inside one interval must NOT re-run fn — lastRun
// survives the flap, so flapping leadership cannot multiply rotation passes.
func TestLeaderTask_NoRerunOnLeadershipFlap(t *testing.T) {
	var runs atomic.Int64
	var leader atomic.Bool
	leader.Store(true)

	task := NewLeaderTask("flap-task", time.Hour, func(ctx context.Context) error {
		runs.Add(1)
		return nil
	})
	task.isLeader = leader.Load
	task.poll = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = task.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Wait for the initial catch-up run.
	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if runs.Load() == 0 {
		t.Fatal("expected the initial catch-up run, got 0")
	}

	// Flap leadership: off, then on again — well within the 1h interval.
	leader.Store(false)
	time.Sleep(30 * time.Millisecond)
	leader.Store(true)
	time.Sleep(100 * time.Millisecond)

	if got := runs.Load(); got != 1 {
		t.Fatalf("expected exactly 1 run after a leadership flap within the interval, got %d", got)
	}
}
