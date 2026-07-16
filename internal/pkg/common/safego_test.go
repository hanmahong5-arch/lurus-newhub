package common

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hookPanicLog installs a test sink for the panic-recovery log lines and
// returns the channel they arrive on. Waiting on the channel guarantees the
// detached goroutine has finished its logging before the test returns, so no
// leaked SysError call can race later tests that reset the global slog logger
// or read their private log buffers.
func hookPanicLog(t *testing.T) <-chan string {
	t.Helper()
	ch := make(chan string, 16)
	hook := func(msg string) { ch <- msg }
	panicLogSink.Store(&hook)
	t.Cleanup(func() { panicLogSink.Store(nil) })
	return ch
}

// waitFor receives one value from ch or fails the test after a generous timeout.
func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func TestSafeGo_NormalExecution(t *testing.T) {
	done := make(chan struct{})

	SafeGo(func() {
		close(done)
	})

	waitFor(t, done, "SafeGo function to execute")
}

func TestSafeGo_PanicRecovery(t *testing.T) {
	logged := hookPanicLog(t)
	completed := make(chan struct{})

	// This should not crash the test
	SafeGo(func() {
		defer close(completed)
		panic("test panic")
	})

	waitFor(t, completed, "SafeGo function defer to run")
	msg := waitFor(t, logged, "SafeGo panic to be logged")
	if !strings.Contains(msg, "panic in SafeGo") || !strings.Contains(msg, "test panic") {
		t.Errorf("unexpected panic log: %q", msg)
	}
}

func TestSafeGoWithContext_NormalExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	SafeGoWithContext(ctx, func(c context.Context) {
		close(done)
	})

	waitFor(t, done, "SafeGoWithContext function to execute")
}

func TestSafeGoWithContext_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	stopped := make(chan struct{})

	SafeGoWithContext(ctx, func(c context.Context) {
		close(started)
		<-c.Done()
		close(stopped)
	})

	waitFor(t, started, "function to start")
	cancel()
	waitFor(t, stopped, "function to respond to context cancellation")
}

func TestSafeGoWithContext_PanicRecovery(t *testing.T) {
	logged := hookPanicLog(t)
	completed := make(chan struct{})

	SafeGoWithContext(context.Background(), func(c context.Context) {
		defer close(completed)
		panic("test panic")
	})

	waitFor(t, completed, "SafeGoWithContext function defer to run")
	msg := waitFor(t, logged, "SafeGoWithContext panic to be logged")
	if !strings.Contains(msg, "panic in SafeGoWithContext") {
		t.Errorf("unexpected panic log: %q", msg)
	}
}

func TestSafeGoNamed_NormalExecution(t *testing.T) {
	done := make(chan struct{})

	SafeGoNamed("test-task", func() {
		close(done)
	})

	waitFor(t, done, "SafeGoNamed function to execute")
}

func TestSafeGoNamed_PanicRecovery(t *testing.T) {
	logged := hookPanicLog(t)
	completed := make(chan struct{})

	SafeGoNamed("panic-task", func() {
		defer close(completed)
		panic("test panic")
	})

	waitFor(t, completed, "SafeGoNamed function defer to run")
	msg := waitFor(t, logged, "SafeGoNamed panic to be logged")
	if !strings.Contains(msg, "panic in SafeGo[panic-task]") {
		t.Errorf("unexpected panic log: %q", msg)
	}
}

func TestMustGo_NormalExecution(t *testing.T) {
	done := make(chan struct{})

	MustGo(func() {
		close(done)
	}, 3)

	waitFor(t, done, "MustGo function to execute")
}

func TestMustGo_PanicRecovery(t *testing.T) {
	logged := hookPanicLog(t)
	completed := make(chan struct{})

	MustGo(func() {
		defer close(completed)
		panic("test panic")
	}, 3)

	waitFor(t, completed, "MustGo function defer to run")
	msg := waitFor(t, logged, "MustGo panic to be logged")
	if !strings.Contains(msg, "panic in MustGo (retry 1)") {
		t.Errorf("unexpected panic log: %q", msg)
	}
}

// Concurrent stress test
func TestSafeGo_ConcurrentExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const numGoroutines = 100
	var completed atomic.Int32
	done := make(chan struct{}, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		SafeGo(func() {
			completed.Add(1)
			done <- struct{}{}
		})
	}

	for i := 0; i < numGoroutines; i++ {
		waitFor(t, done, "concurrent SafeGo goroutine to complete")
	}

	if completed.Load() != numGoroutines {
		t.Errorf("expected %d completions, got %d", numGoroutines, completed.Load())
	}
}

// Benchmark tests
func BenchmarkSafeGo(b *testing.B) {
	for i := 0; i < b.N; i++ {
		done := make(chan struct{})
		SafeGo(func() {
			close(done)
		})
		<-done
	}
}

func BenchmarkSafeGoWithContext(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		done := make(chan struct{})
		SafeGoWithContext(ctx, func(c context.Context) {
			close(done)
		})
		<-done
	}
}

func BenchmarkRawGoroutine(b *testing.B) {
	for i := 0; i < b.N; i++ {
		done := make(chan struct{})
		go func() {
			close(done)
		}()
		<-done
	}
}
