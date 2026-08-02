package lifecycle

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// listenLocal binds an ephemeral loopback port and hands back the LIVE
// listener plus its address.
//
// The listener is deliberately NOT closed here, and callers must drive their
// server with server.Serve(listener) rather than server.ListenAndServe().
// The obvious-looking alternative — Listen, read Addr, Close, then let the
// server re-bind the same port — is a race with two distinct failure modes,
// both of which only surface under a loaded parallel `go test ./...` and pass
// in isolation:
//
//  1. Close() returns the port to the OS. Anything on the machine (including
//     another test in this same run) can take it before the server re-binds,
//     turning ListenAndServe into "bind: address already in use". Tests that
//     forward that error to an assertion channel then fail on a real error
//     that has nothing to do with the behaviour under test.
//  2. Nothing tells the caller when the re-bind finished, so the sleep that
//     usually follows is a guess. When the machine is busy the requests race
//     ahead of the bind, every one of them is refused, and the "requests were
//     handled" assertion fails with a count of zero.
//
// Holding the listener open removes both: the socket is already bound and
// accepting before the test body runs, so no sleep is needed either.
func listenLocal(t *testing.T) (net.Listener, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind an ephemeral loopback port: %v", err)
	}
	// Shutdown/Close on the server also closes the listener; this is only the
	// safety net for the early-return paths.
	t.Cleanup(func() { _ = listener.Close() })
	return listener, listener.Addr().String()
}

// TestGracefulHTTPShutdown tests the graceful HTTP server shutdown pattern.
func TestGracefulHTTPShutdown(t *testing.T) {
	t.Parallel()

	listener, addr := listenLocal(t)

	// Create HTTP server
	mux := http.NewServeMux()
	var requestCount atomic.Int32
	// entered is the synchronisation point that replaces the old sleep: it
	// reports that a request is genuinely inside the handler, which is the
	// precondition Shutdown's "wait for active connections" path needs.
	entered := make(chan struct{}, 5)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		time.Sleep(10 * time.Millisecond) // Simulate work
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{Handler: mux}

	// Start server on the already-bound listener
	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Make some requests
	for i := 0; i < 5; i++ {
		go func() {
			resp, err := http.Get("http://" + addr + "/")
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
	}

	// Wait for a request to actually reach the handler
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no request reached the handler")
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	// Verify server exited cleanly
	select {
	case err := <-serverErr:
		if err != nil {
			t.Errorf("server error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("server did not exit within timeout")
	}

	// Verify requests were handled
	if count := requestCount.Load(); count == 0 {
		t.Error("no requests were handled")
	}
}

// TestGracefulShutdownWithBackground tests HTTP server with background tasks.
func TestGracefulShutdownWithBackground(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)

	var backgroundTaskRuns atomic.Int32
	var httpRequestCount atomic.Int32

	// Background task
	g.Go(func() error {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				backgroundTaskRuns.Add(1)
			}
		}
	})

	listener, addr := listenLocal(t)

	// HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpRequestCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{Handler: mux}

	g.Go(func() error {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		// A fresh context on purpose: ctx is already cancelled at this point,
		// so inheriting it would make Shutdown abandon in-flight requests
		// immediately — the opposite of what a graceful drain must do.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx) //nolint:contextcheck // deliberate detachment, see above
	})

	// Make request — no startup sleep needed, the listener is already accepting
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	// Let the background task tick at least once
	deadline := time.Now().Add(10 * time.Second)
	for backgroundTaskRuns.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// Trigger shutdown
	cancel()

	// Wait for all goroutines
	if err := g.Wait(); err != nil {
		t.Errorf("errgroup error: %v", err)
	}

	// Verify both ran
	if backgroundTaskRuns.Load() == 0 {
		t.Error("background task did not run")
	}
	if httpRequestCount.Load() == 0 {
		t.Error("HTTP request not handled")
	}
}

// TestShutdownTimeout tests behavior when shutdown exceeds timeout.
func TestShutdownTimeout(t *testing.T) {
	t.Parallel()

	listener, addr := listenLocal(t)

	// release keeps the slow handler parked for exactly as long as the test
	// needs it, instead of burning a fixed 5s of wall clock. The handler must
	// still be running when Shutdown is called — that is the whole premise of
	// the assertion below — so entered reports when it truly is.
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{Handler: mux}

	go func() { _ = server.Serve(listener) }()

	// Start a slow request (don't wait for response)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("slow request never reached the handler")
	}

	// Shutdown with short timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := server.Shutdown(shutdownCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

// TestMultipleSignals simulates receiving multiple shutdown signals.
func TestMultipleSignals(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	var shutdownCount atomic.Int32
	var mu sync.Mutex
	shutdownStarted := false

	// Simulate the shutdown handler
	go func() {
		<-ctx.Done()
		mu.Lock()
		if shutdownStarted {
			mu.Unlock()
			return // Already shutting down
		}
		shutdownStarted = true
		mu.Unlock()

		shutdownCount.Add(1)
		time.Sleep(50 * time.Millisecond) // Simulate shutdown work
	}()

	// Cancel multiple times (simulating multiple signals)
	cancel()
	cancel()
	cancel()

	time.Sleep(100 * time.Millisecond)

	// Should only have processed shutdown once
	if count := shutdownCount.Load(); count != 1 {
		t.Errorf("expected 1 shutdown, got %d", count)
	}
}

// TestErrGroupPropagation tests that errgroup propagates errors correctly.
func TestErrGroupPropagation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g, ctx := errgroup.WithContext(ctx)

	expectedErr := errors.New("task failed")

	// Task that will fail
	g.Go(func() error {
		time.Sleep(10 * time.Millisecond)
		return expectedErr
	})

	// Task that waits for context
	var task2Cancelled atomic.Bool
	g.Go(func() error {
		<-ctx.Done()
		task2Cancelled.Store(true)
		return nil
	})

	err := g.Wait()
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}

	// Task 2 should have been cancelled
	if !task2Cancelled.Load() {
		t.Error("task 2 should have been cancelled when task 1 failed")
	}
}

// TestCleanShutdownOrder tests that resources are cleaned up in correct order.
func TestCleanShutdownOrder(t *testing.T) {
	t.Parallel()

	var shutdownOrder []string
	var mu sync.Mutex

	appendOrder := func(s string) {
		mu.Lock()
		shutdownOrder = append(shutdownOrder, s)
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)

	// HTTP server (should shutdown first)
	g.Go(func() error {
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		appendOrder("http")
		return nil
	})

	// Background task (should shutdown after HTTP)
	g.Go(func() error {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		appendOrder("background")
		return nil
	})

	// Database connection (should shutdown last)
	defer func() {
		appendOrder("database")
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	_ = g.Wait()

	// Wait for defer
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(shutdownOrder) < 2 {
		t.Fatalf("expected at least 2 shutdown events, got %d", len(shutdownOrder))
	}

	// HTTP should come before background
	httpIdx := -1
	bgIdx := -1
	for i, s := range shutdownOrder {
		if s == "http" {
			httpIdx = i
		}
		if s == "background" {
			bgIdx = i
		}
	}

	if httpIdx > bgIdx {
		t.Errorf("HTTP should shutdown before background: %v", shutdownOrder)
	}
}

// Benchmark errgroup context overhead
func BenchmarkErrGroupContext(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		g, ctx := errgroup.WithContext(ctx)

		g.Go(func() error {
			<-ctx.Done()
			return nil
		})

		cancel()
		_ = g.Wait()
	}
}

func BenchmarkHTTPGracefulShutdown(b *testing.B) {
	for i := 0; i < b.N; i++ {
		// Same live-listener discipline as listenLocal (see its doc comment):
		// closing and re-binding races other tests for the ephemeral port.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			b.Fatalf("failed to bind an ephemeral loopback port: %v", err)
		}

		server := &http.Server{Handler: http.NewServeMux()}

		go func() { _ = server.Serve(listener) }()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := server.Shutdown(ctx); err != nil {
			b.Fatalf("shutdown failed: %v", err)
		}
		cancel()
	}
}
