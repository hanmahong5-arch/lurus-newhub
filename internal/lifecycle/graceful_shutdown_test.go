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

// TestGracefulHTTPShutdown tests the graceful HTTP server shutdown pattern.
func TestGracefulHTTPShutdown(t *testing.T) {
	t.Parallel()

	// Hold the listener for the whole test: closing it to "free the port" and
	// re-binding by address is a TOCTOU race (another process can take the port,
	// or the re-bind can lag) that makes this test fail spuriously.
	listener := fixShutdownListen(t)
	port := listener.Addr().String()

	// Create HTTP server
	mux := http.NewServeMux()
	var requestCount atomic.Int32
	handlerEntered := make(chan struct{}, 5)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		select {
		case handlerEntered <- struct{}{}:
		default:
		}
		time.Sleep(10 * time.Millisecond) // Simulate work
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    port,
		Handler: mux,
	}

	// Start server on the listener we already own
	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Wait for the server to actually accept connections
	fixShutdownWaitReady(t, port)

	// Make some requests
	var requestsSent sync.WaitGroup
	for i := 0; i < 5; i++ {
		requestsSent.Add(1)
		go func() {
			defer requestsSent.Done()
			resp, err := http.Get("http://" + port + "/")
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
	}

	// Wait until a request is really in-flight instead of guessing with a sleep
	select {
	case <-handlerEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("no request reached the handler before shutdown")
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	requestsSent.Wait()

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

	// Bind the port and keep the listener (see fixShutdownListen: closing it
	// first and re-binding by address is a race)
	listener := fixShutdownListen(t)
	port := listener.Addr().String()

	// HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpRequestCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    port,
		Handler: mux,
	}

	g.Go(func() error {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	})

	// Wait for the server to accept connections
	fixShutdownWaitReady(t, port)

	// Make request
	if resp, err := http.Get("http://" + port + "/"); err != nil {
		t.Errorf("request failed: %v", err)
	} else {
		_ = resp.Body.Close()
	}

	// Let background task run
	time.Sleep(50 * time.Millisecond)

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

	// Keep the listener instead of closing it and re-binding by address
	listener := fixShutdownListen(t)
	port := listener.Addr().String()

	slowRequestStarted := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(slowRequestStarted)
		time.Sleep(5 * time.Second) // Very slow handler
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    port,
		Handler: mux,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	fixShutdownWaitReady(t, port)

	// Start a slow request (don't wait for response)
	go func() {
		if resp, err := http.Get("http://" + port + "/slow"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	// Only shut down once the slow handler is really running, otherwise
	// Shutdown has nothing to wait for and returns nil
	select {
	case <-slowRequestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("slow request never reached the handler")
	}

	// Shutdown with short timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := server.Shutdown(shutdownCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
	// The serve loop must have stopped for the documented reason, not a bind error
	select {
	case serr := <-serveErr:
		if !errors.Is(serr, http.ErrServerClosed) {
			t.Errorf("serve ended with %v, want http.ErrServerClosed", serr)
		}
	case <-time.After(2 * time.Second):
		t.Error("serve loop did not stop after shutdown")
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
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			b.Fatalf("listen: %v", err)
		}

		server := &http.Server{
			Addr:    listener.Addr().String(),
			Handler: http.NewServeMux(),
		}

		go func() { _ = server.Serve(listener) }()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		server.Shutdown(ctx)
		cancel()
	}
}
