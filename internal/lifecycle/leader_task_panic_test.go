package lifecycle

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A panic inside fn used to propagate out of LeaderTask.Run; the only
// protection was a one-shot recover around the whole Run, so the task died for
// the pod's lifetime while every other replica-local signal still looked
// healthy. Each tick must recover on its own and the next tick must still run.
func TestLeaderTask_PanicInFnDoesNotStopTicking(t *testing.T) {
	var runs atomic.Int64

	task := NewLeaderTask("panic-task", time.Millisecond, func(ctx context.Context) error {
		if runs.Add(1) == 1 {
			panic("first tick dies")
		}
		return nil
	})
	task.isLeader = func() bool { return true }
	task.poll = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	escaped := make(chan any, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				escaped <- r
			}
		}()
		_ = task.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() < 3 && len(escaped) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	select {
	case r := <-escaped:
		t.Fatalf("panic escaped LeaderTask.Run: %v", r)
	default:
	}
	if got := runs.Load(); got < 3 {
		t.Fatalf("fn ran %d times, want >=3 (ticking must continue after the panic)", got)
	}
}
