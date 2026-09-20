package lifecycle

// drain_test.go — oracle for cycle 13 L11 (graceful shutdown). Reuses
// fixShutdownListen/fixShutdownWaitReady from fix_shutdown_rebind_test.go:
// same package, same "hold the listener, poll real connectivity instead of
// sleeping" discipline as graceful_shutdown_test.go.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// TestDrainerShutdown_CutsInFlightOnTimeout is the primary oracle: a slow
// handler is still in flight when the shutdown budget expires. Shutdown must
// (a) mark the Drainer draining before it even starts waiting — proven from
// inside the inflight callback, which only runs on the timeout branch — (b)
// return the in-flight count reported by the caller-supplied inflight probe,
// and (c) return a nil error: a timed-out drain is the expected outcome of a
// budget, not a process failure (do-not-regress: no more FatalLog/exit 1).
func TestDrainerShutdown_CutsInFlightOnTimeout(t *testing.T) {
	t.Parallel()

	listener := fixShutdownListen(t)
	addr := listener.Addr().String()

	handlerEntered := make(chan struct{})
	releaseHandler := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(handlerEntered)
		<-releaseHandler
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: addr, Handler: mux}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()
	fixShutdownWaitReady(t, addr)

	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-handlerEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow handler never started")
	}

	d := NewDrainer()
	if d.IsDraining() {
		t.Fatal("fresh Drainer must not report draining before Shutdown is called")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	inflightCalls := 0
	start := time.Now()
	cut, err := d.Shutdown(shutdownCtx, srv, func() int {
		inflightCalls++
		if !d.IsDraining() {
			t.Error("Drainer must already be marked draining by the time inflight() is consulted")
		}
		return 1
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Shutdown returned error %v, want nil (a budget timeout must not surface as an error)", err)
	}
	if cut != 1 {
		t.Fatalf("cut = %d, want 1", cut)
	}
	if inflightCalls != 1 {
		t.Fatalf("inflight callback called %d times, want exactly 1", inflightCalls)
	}
	if !d.IsDraining() {
		t.Error("Shutdown must leave the Drainer marked draining")
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("Shutdown returned after only %v, want to have waited out the ~200ms budget", elapsed)
	}

	// Let the handler finish so Serve can actually return, then drain it.
	close(releaseHandler)
	select {
	case serr := <-serveErr:
		if !errors.Is(serr, http.ErrServerClosed) {
			t.Errorf("Serve returned %v, want http.ErrServerClosed", serr)
		}
	case <-time.After(2 * time.Second):
		t.Error("serve loop did not stop after Shutdown")
	}
}

// TestDrainerShutdown_NoInFlightReturnsEarlyWithZeroCut is the counterpart:
// nothing is in flight, so http.Server.Shutdown itself returns nil well
// before the budget elapses. cut must be 0 and the inflight probe must not
// even be consulted — there is nothing to count.
func TestDrainerShutdown_NoInFlightReturnsEarlyWithZeroCut(t *testing.T) {
	t.Parallel()

	listener := fixShutdownListen(t)
	addr := listener.Addr().String()
	srv := &http.Server{Addr: addr, Handler: http.NewServeMux()}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()
	fixShutdownWaitReady(t, addr)

	d := NewDrainer()

	const budget = 2 * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	inflightCalled := false
	start := time.Now()
	cut, err := d.Shutdown(shutdownCtx, srv, func() int {
		inflightCalled = true
		return 99
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Shutdown returned error %v, want nil", err)
	}
	if cut != 0 {
		t.Fatalf("cut = %d, want 0 (nothing was in flight)", cut)
	}
	if inflightCalled {
		t.Error("inflight callback must not be consulted when Shutdown completes within budget")
	}
	if !d.IsDraining() {
		t.Error("Shutdown must mark the Drainer draining even when nothing was in flight")
	}
	if elapsed >= budget {
		t.Errorf("Shutdown took %v, want well under the %v budget (idle server)", elapsed, budget)
	}

	select {
	case serr := <-serveErr:
		if !errors.Is(serr, http.ErrServerClosed) {
			t.Errorf("Serve returned %v, want http.ErrServerClosed", serr)
		}
	case <-time.After(2 * time.Second):
		t.Error("serve loop did not stop after Shutdown")
	}
}

// TestDrainer_MarkDrainingIsIdempotentAndObservable covers the two standalone
// accessors directly, independent of Shutdown.
func TestDrainer_MarkDrainingIsIdempotentAndObservable(t *testing.T) {
	t.Parallel()

	d := NewDrainer()
	if d.IsDraining() {
		t.Fatal("fresh Drainer must start not-draining")
	}
	d.MarkDraining()
	if !d.IsDraining() {
		t.Fatal("IsDraining must report true after MarkDraining")
	}
	// Calling it again must not panic or flip anything back.
	d.MarkDraining()
	if !d.IsDraining() {
		t.Fatal("IsDraining must stay true after a second MarkDraining call")
	}
}

// TestDrainer_ResetForTest exists to prove the test-only escape hatch that
// health_draining_test.go (package handler) relies on via Default() actually
// restores the not-draining state, so that test can undo its use of the
// process-wide singleton instead of leaking it into every later test in the
// same binary.
func TestDrainer_ResetForTest(t *testing.T) {
	t.Parallel()

	d := NewDrainer()
	d.MarkDraining()
	if !d.IsDraining() {
		t.Fatal("setup: MarkDraining did not take effect")
	}
	d.ResetForTest()
	if d.IsDraining() {
		t.Fatal("ResetForTest must restore the not-draining state")
	}
}

// TestDefaultDrainer_IsProcessWideSingleton locks the wiring health.go and
// (per the L11 hand-off) misc.go's GetStatus depend on: Default() must
// always return the same instance, and the package-level IsDraining/
// MarkDraining free functions must observe it.
func TestDefaultDrainer_IsProcessWideSingleton(t *testing.T) {
	t.Cleanup(func() { Default().ResetForTest() })

	if Default() != Default() {
		t.Fatal("Default() must return the same instance on every call")
	}
	if IsDraining() {
		t.Fatal("test isolation broken: the process-wide Drainer was already draining at test start")
	}
	MarkDraining()
	if !Default().IsDraining() {
		t.Fatal("package-level MarkDraining must mark Default()'s Drainer")
	}
	if !IsDraining() {
		t.Fatal("package-level IsDraining must observe MarkDraining's effect")
	}
}
