package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// A loop that panics must be restarted by goSupervised, not take the process
// down with it (bare g.Go does not recover) and not stay dead.
func TestGoSupervised_RestartsPanickingLoop(t *testing.T) {
	g, ctx := errgroup.WithContext(context.Background())
	var runs atomic.Int32
	goSupervised(g, ctx, "loop-a", func(ctx context.Context) {
		if runs.Add(1) == 1 {
			panic("first run dies")
		}
	})

	done := make(chan error, 1)
	go func() { done <- g.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("errgroup returned %v, want nil after a clean second run", err)
		}
	case <-time.After(loopRestartBackoff + 5*time.Second):
		t.Fatal("supervised loop never restarted after its panic")
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("loop ran %d times, want 2 (panic, restart, clean return)", got)
	}
}

// The outbox loop recovers per tick: after a panicking pass the next tick must
// still run.
func TestRunBillingOutboxLoop_PanicInTickDoesNotEndLoop(t *testing.T) {
	var calls atomic.Int32
	prev := billingOutboxTickFn
	billingOutboxTickFn = func(ctx context.Context) {
		if calls.Add(1) == 1 {
			panic("first drain dies")
		}
	}
	t.Cleanup(func() { billingOutboxTickFn = prev })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		_ = runBillingOutboxLoop(ctx, 5*time.Millisecond)
	}()

	if err := waitFor(2*time.Second, func() bool { return calls.Load() >= 3 }); err != nil {
		cancel()
		t.Fatalf("loop stopped ticking after the panic: calls=%d", calls.Load())
	}
	cancel()
	select {
	case r := <-done:
		if r != nil {
			t.Fatalf("panic escaped runBillingOutboxLoop: %v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runBillingOutboxLoop did not return after ctx cancel")
	}
}
