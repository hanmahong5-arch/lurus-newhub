package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockTask is a test helper for simulating background tasks.
type mockTask struct {
	name       string
	runCount   atomic.Int32
	started    chan struct{}
	shouldFail bool
	slowdown   time.Duration
}

func newMockTask(name string) *mockTask {
	return &mockTask{
		name:    name,
		started: make(chan struct{}),
	}
}

func (m *mockTask) Name() string {
	return m.name
}

func (m *mockTask) Run(ctx context.Context) error {
	close(m.started)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
			m.runCount.Add(1)
			if m.slowdown > 0 {
				time.Sleep(m.slowdown)
			}
			if m.shouldFail {
				return errors.New("mock task failed")
			}
		}
	}
}

func TestManager_BasicLifecycle(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	task := newMockTask("test-task")
	mgr.Register(task)

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)

	// Wait for task to start
	select {
	case <-task.started:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Fatal("task did not start within timeout")
	}

	// Let it run a few cycles
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Wait for shutdown
	select {
	case <-mgr.Done():
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Fatal("manager did not complete shutdown within timeout")
	}

	if task.runCount.Load() == 0 {
		t.Error("task should have run at least once")
	}
}

func TestManager_MultipleTasksShutdown(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	const numTasks = 5
	tasks := make([]*mockTask, numTasks)

	for i := 0; i < numTasks; i++ {
		tasks[i] = newMockTask("task-" + string(rune('A'+i)))
		mgr.Register(tasks[i])
	}

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)

	// Wait for all tasks to start
	for i, task := range tasks {
		select {
		case <-task.started:
			// OK
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("task %d did not start within timeout", i)
		}
	}

	// Let them run: wait until every task has ticked at least once instead of
	// sleeping a fixed interval — under full-suite CPU load a fixed 30ms can
	// elapse before a single 10ms ticker fires, failing all five "should have
	// run" assertions below at once (observed 2026-08-29 on a loaded
	// `go test ./...` run; passes in isolation every time).
	deadline := time.Now().Add(2 * time.Second)
	for _, task := range tasks {
		for task.runCount.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Shutdown
	cancel()

	select {
	case <-mgr.Done():
		// OK
	case <-time.After(200 * time.Millisecond):
		t.Fatal("manager did not complete shutdown within timeout")
	}

	// Verify all tasks ran
	for i, task := range tasks {
		if task.runCount.Load() == 0 {
			t.Errorf("task %d should have run at least once", i)
		}
	}
}

func TestManager_ShutdownTimeout(t *testing.T) {
	t.Parallel()

	mgr := NewManager()

	// Create a slow task that ignores context
	slowTask := &slowIgnoreContextTask{
		name:    "slow-task",
		started: make(chan struct{}),
	}
	mgr.Register(slowTask)

	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)

	select {
	case <-slowTask.started:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Fatal("task did not start")
	}

	cancel()

	// Shutdown with short timeout should fail
	err := mgr.Shutdown(10 * time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

// slowIgnoreContextTask simulates a task that doesn't respond to context cancellation.
type slowIgnoreContextTask struct {
	name    string
	started chan struct{}
}

func (s *slowIgnoreContextTask) Name() string {
	return s.name
}

func (s *slowIgnoreContextTask) Run(ctx context.Context) error {
	close(s.started)
	// Ignore context, sleep for a long time
	time.Sleep(5 * time.Second)
	return nil
}

func TestManager_EmptyManager(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)

	cancel()

	// Should complete immediately
	select {
	case <-mgr.Done():
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Fatal("empty manager should complete immediately")
	}
}

func TestManager_TaskError(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	task := newMockTask("failing-task")
	task.shouldFail = true
	mgr.Register(task)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)

	select {
	case <-task.started:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Fatal("task did not start")
	}

	// Task should fail quickly
	select {
	case <-mgr.Done():
		// OK, task exited
	case <-time.After(200 * time.Millisecond):
		t.Fatal("failing task should have exited")
	}
}

func TestManager_ConcurrentRegister(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	var wg sync.WaitGroup
	const numGoroutines = 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			task := newMockTask("concurrent-task")
			mgr.Register(task)
		}(i)
	}

	wg.Wait()

	// Should have registered all tasks without panic
	mgr.mu.Lock()
	count := len(mgr.tasks)
	mgr.mu.Unlock()

	if count != numGoroutines {
		t.Errorf("expected %d tasks, got %d", numGoroutines, count)
	}
}

func TestTickerTask_BasicOperation(t *testing.T) {
	t.Parallel()

	var counter atomic.Int32
	task := NewTickerTask("counter-task", 10*time.Millisecond, func(ctx context.Context) error {
		counter.Add(1)
		return nil
	})

	if task.Name() != "counter-task" {
		t.Errorf("expected name 'counter-task', got %q", task.Name())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := task.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}

	count := counter.Load()
	if count < 5 || count > 15 {
		t.Errorf("expected counter between 5-15, got %d", count)
	}
}

func TestTickerTask_ContextCancellation(t *testing.T) {
	t.Parallel()

	var counter atomic.Int32
	task := NewTickerTask("cancel-task", 5*time.Millisecond, func(ctx context.Context) error {
		counter.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		done <- task.Run(ctx)
	}()

	// Let it run a bit
	time.Sleep(30 * time.Millisecond)

	// Cancel
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("task did not respond to cancellation")
	}

	if counter.Load() == 0 {
		t.Error("task should have run at least once before cancellation")
	}
}

func TestTickerTask_ErrorInFunction(t *testing.T) {
	t.Parallel()

	var errorCount atomic.Int32
	task := NewTickerTask("error-task", 5*time.Millisecond, func(ctx context.Context) error {
		errorCount.Add(1)
		return errors.New("intentional error")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := task.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded (task should continue despite errors), got %v", err)
	}

	// Task should have continued running despite errors
	if errorCount.Load() < 3 {
		t.Errorf("expected at least 3 error runs, got %d", errorCount.Load())
	}
}

func TestTickerTask_ZeroInterval(t *testing.T) {
	t.Parallel()

	// Zero interval should panic or behave predictably
	defer func() {
		if r := recover(); r != nil {
			// Expected - ticker panics on zero duration
		}
	}()

	var counter atomic.Int32
	task := NewTickerTask("zero-task", 0, func(ctx context.Context) error {
		counter.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_ = task.Run(ctx)
	// If we get here without panic, that's also acceptable
}

// Benchmark tests
func BenchmarkManager_StartStop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		mgr := NewManager()
		task := newMockTask("bench-task")
		mgr.Register(task)

		ctx, cancel := context.WithCancel(context.Background())
		mgr.Start(ctx)

		<-task.started
		cancel()
		<-mgr.Done()
	}
}

func BenchmarkTickerTask_Run(b *testing.B) {
	var counter atomic.Int32
	task := NewTickerTask("bench-ticker", time.Microsecond, func(ctx context.Context) error {
		counter.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(time.Duration(b.N) * time.Microsecond * 2)
		cancel()
	}()

	_ = task.Run(ctx)
}
