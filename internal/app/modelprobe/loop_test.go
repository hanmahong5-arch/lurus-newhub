package modelprobe

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestLoop_SkipsTicksWhenNotLeader(t *testing.T) {
	setupRunOnceDB(t)
	setModelProbeSetting(t, true, "free", 24, 3, 40, 0)

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	var calls atomic.Int64
	probe := func(ctx context.Context, ch *repo.Channel, model string) Result {
		calls.Add(1)
		return Result{OK: true}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, probe, 20*time.Millisecond)
		close(done)
	}()
	<-done

	if calls.Load() != 0 {
		t.Errorf("calls = %d, want 0 — a follower must never run a probe pass", calls.Load())
	}
}

func TestLoop_RunsOnTickWhenLeader(t *testing.T) {
	setupRunOnceDB(t)
	setModelProbeSetting(t, true, "free", 24, 3, 40, 0)
	seedProbeChannel(t, "free", "m1")

	prevLeader := common.IsLeader()
	common.SetLeader(true)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	ran := make(chan struct{}, 1)
	probe := func(ctx context.Context, ch *repo.Channel, model string) Result {
		select {
		case ran <- struct{}{}:
		default:
		}
		return Result{OK: true}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Loop(ctx, probe, 10*time.Millisecond)

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop never ran a probe pass on a fast tick while leader")
	}
}

func TestLoop_ReturnsWhenContextDone(t *testing.T) {
	setupRunOnceDB(t)
	setModelProbeSetting(t, true, "free", 24, 3, 40, 0)

	probe := func(ctx context.Context, ch *repo.Channel, model string) Result {
		return Result{OK: true}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, probe, time.Hour) // long tick: only ctx.Done() can end this
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not return promptly after context cancellation")
	}
}
