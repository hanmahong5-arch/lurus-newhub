package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestLeaderTask_RunsOnlyWhenLeader verifies the per-tick leadership gate:
// fn must not run while this node is not the leader, and must run once
// leadership is acquired. The acquire/renew semantics themselves are covered
// deterministically by the repo TryAcquireOrRenew tests; here we exercise only
// the gating with an injected leadership flag.
//
// The last-success-gauge assertions are synchronized through an unbuffered
// "call" channel rather than sleeps, and — this is the part that matters —
// every gauge check waits for the *next* invocation's call signal before
// reading the gauge for the *previous* invocation's outcome, never checking
// right after unblocking the invocation it's testing. Run() is a single
// goroutine processing one tick at a time, so invocation N+1's call signal
// cannot be sent until invocation N's entire body — including whatever the
// gauge-write guard decided — has finished executing. That is a real
// happens-before relationship. Checking immediately after sending an
// invocation's result (an earlier draft of this test did that) is NOT
// synchronized with the metric write on the mutated ("always record")
// version of the guard — it becomes a bare goroutine-scheduling race that
// only fails sometimes, reproducing exactly the "green in 4 of 5 runs"
// flakiness this lock exists to eliminate.
func TestLeaderTask_RunsOnlyWhenLeader(t *testing.T) {
	const taskName = "leader-task-runs-only-when-leader" // unique label; not reused elsewhere in this package
	var leader atomic.Bool
	var runs atomic.Int64
	call := make(chan struct{})   // fn signals "I was invoked" here, unbuffered
	result := make(chan error, 1) // test tells the blocked fn what to return

	ctx, cancel := context.WithCancel(context.Background())
	// Both blocking channel ops are also gated on ctx.Done(): if an
	// assertion below fails via t.Fatalf while fn is blocked (e.g. waiting
	// on `result` that nothing will ever send to once this goroutine has
	// exited via runtime.Goexit), the deferred cancel() must still be able
	// to unblock fn so task.Run returns and <-done below doesn't hang the
	// whole test binary.
	task := NewLeaderTask(taskName, 5*time.Millisecond, func(ctx context.Context) error {
		runs.Add(1)
		select {
		case call <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	task.isLeader = leader.Load // inject deterministic leadership

	done := make(chan struct{})
	go func() {
		_ = task.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	lastSuccess := func() float64 {
		return testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues(taskName))
	}
	waitCall := func() {
		t.Helper()
		select {
		case <-call:
		case <-time.After(time.Second):
			t.Fatal("fn did not run in time")
		}
	}

	// Phase 1: not leader — fn must not run, and the gauge must stay
	// unstamped (item 9: the gauge is initialised to 0 at registration, not
	// left absent, but 0 either way here).
	select {
	case <-call:
		t.Fatal("fn ran while not leader")
	case <-time.After(50 * time.Millisecond):
	}
	if got := runs.Load(); got != 0 {
		t.Fatalf("expected 0 runs while not leader, got %d", got)
	}
	if got := lastSuccess(); got != 0 {
		t.Fatalf("expected no last-success stamp before any run, got %v", got)
	}

	// Phase 2: become leader. Invocation #1 fails.
	leader.Store(true)
	waitCall() // invocation #1 started
	result <- errors.New("boom")

	// Invocation #2's call signal cannot be sent until Run() has finished
	// invocation #1's entire body (including the conditional metric write),
	// because Run() is single-goroutine. Waiting for it before reading the
	// gauge is what makes the next assertion deterministic.
	waitCall() // invocation #2 started; invocation #1's body is now fully done
	if got := lastSuccess(); got != 0 {
		t.Fatalf("last-success gauge stamped after a FAILING run: %v", got)
	}

	// Phase 3: invocation #2 succeeds.
	result <- nil
	waitCall() // invocation #3 started; invocation #2's body is now fully done
	if got := lastSuccess(); got == 0 {
		t.Fatal("expected the last-success gauge to be stamped after a successful run")
	}
	if got := runs.Load(); got < 3 {
		t.Fatalf("expected at least 3 runs (2 to complete + 1 in flight), got %d", got)
	}

	// Phase 4: lose leadership while invocation #3 is in flight; let it
	// finish (its outcome doesn't matter here), then confirm no further
	// invocation happens.
	leader.Store(false)
	result <- errors.New("in-flight tick, discarded")
	select {
	case <-call:
		t.Fatal("fn ran again after losing leadership")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestNewLeaderTask_InitialisesLastSuccessGaugeToZero locks L3 finding #9: a
// GaugeVec exports no series at all for a label it has never been set with,
// so a task that has never once succeeded would be invisible to any
// `time() - last_success > X` alert rather than showing up as "stuck at 0".
// Registration alone (no Run, no leadership) must make the series exist.
func TestNewLeaderTask_InitialisesLastSuccessGaugeToZero(t *testing.T) {
	const taskName = "leader-task-never-run"
	NewLeaderTask(taskName, time.Hour, func(ctx context.Context) error { return nil })

	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	found := false
	for _, mf := range metricFamilies {
		if mf.GetName() != "lurus_gateway_leader_task_last_success_timestamp_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "task" && lp.GetValue() == taskName {
					found = true
					if got := m.GetGauge().GetValue(); got != 0 {
						t.Errorf("last-success gauge for a never-run task = %v, want 0", got)
					}
				}
			}
		}
	}
	if !found {
		t.Fatalf("lurus_gateway_leader_task_last_success_timestamp_seconds{task=%q} series does not exist after registration", taskName)
	}
}

// TestLeaderTask_StopsOnContextCancel verifies the task returns promptly when
// its context is cancelled.
func TestLeaderTask_StopsOnContextCancel(t *testing.T) {
	var leader atomic.Bool
	leader.Store(true)

	task := NewLeaderTask("stop-task", 10*time.Millisecond, func(ctx context.Context) error { return nil })
	task.isLeader = leader.Load

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = task.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("LeaderTask.Run did not return after context cancel")
	}
}
