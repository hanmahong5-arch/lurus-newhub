package metrics

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRecordLeaderTaskSuccess_RefreshesAgeForEveryRegisteredTask is the
// frozen-clock oracle for LeaderTaskAgeSeconds. It registers two distinct
// tasks in taskreg (this package's test binary never calls taskreg.Register
// on its own, so these are guaranteed new entries — see taskreg's own
// no-dedupe doc comment), freezes nowUnix, and checks that a SECOND task's
// success also refreshes the FIRST task's age from its own frozen
// last-success reading — the "companion refresh" behavior LeaderTaskAgeSeconds's
// doc comment describes, and the reason there is no dedicated ticker for
// it: whichever task ticks most often keeps every other registered task's
// age reading fresh too. A design that only ever set the CALLING task's own
// age to 0 (i.e. dropped the taskreg.Snapshot() loop) would leave the first
// task's gauge frozen at 0 instead of advancing to 90 in the third
// assertion below — that is the mutation this test is built to catch.
func TestRecordLeaderTaskSuccess_RefreshesAgeForEveryRegisteredTask(t *testing.T) {
	const taskA = "leader-task-age-test-a"
	const taskB = "leader-task-age-test-b"
	taskreg.Register(taskA, func() time.Duration { return time.Minute }, true, nil)
	taskreg.Register(taskB, func() time.Duration { return time.Minute }, true, nil)

	origNow := nowUnix
	t.Cleanup(func() { nowUnix = origNow })

	const t0 = int64(1_700_000_000)
	nowUnix = func() int64 { return t0 }

	// taskA succeeds at t0: its own age must read 0 immediately.
	RecordLeaderTaskSuccess(taskA)
	if got := testutil.ToFloat64(LeaderTaskAgeSeconds.WithLabelValues(taskA)); got != 0 {
		t.Fatalf("LeaderTaskAgeSeconds{task=%s} right after its own success = %v, want 0", taskA, got)
	}

	// Advance the frozen clock by 90s and record a DIFFERENT task's success
	// (never taskA's again). If the companion refresh only touched the
	// calling task, taskA's gauge would stay frozen at 0.
	nowUnix = func() int64 { return t0 + 90 }
	RecordLeaderTaskSuccess(taskB)

	if got := testutil.ToFloat64(LeaderTaskAgeSeconds.WithLabelValues(taskA)); got != 90 {
		t.Errorf("LeaderTaskAgeSeconds{task=%s} after taskB's success 90s later = %v, want 90 — "+
			"a companion refresh must recompute EVERY registered task's age, not just the "+
			"caller's own", taskA, got)
	}
	if got := testutil.ToFloat64(LeaderTaskAgeSeconds.WithLabelValues(taskB)); got != 0 {
		t.Errorf("LeaderTaskAgeSeconds{task=%s} right after its own success = %v, want 0", taskB, got)
	}
}

// TestRecordLeaderTaskSuccess_AgeAdvancesAcrossOwnTicks pins the simpler,
// single-task case with a frozen clock: two successive successes of the
// SAME task 45s apart must read age=0 right after each, not accumulate or
// carry the previous gap forward.
func TestRecordLeaderTaskSuccess_AgeAdvancesAcrossOwnTicks(t *testing.T) {
	const task = "leader-task-age-test-c"
	taskreg.Register(task, func() time.Duration { return time.Minute }, true, nil)

	origNow := nowUnix
	t.Cleanup(func() { nowUnix = origNow })

	const t0 = int64(1_800_000_000)
	nowUnix = func() int64 { return t0 }
	RecordLeaderTaskSuccess(task)
	if got := testutil.ToFloat64(LeaderTaskAgeSeconds.WithLabelValues(task)); got != 0 {
		t.Fatalf("age after first success = %v, want 0", got)
	}

	nowUnix = func() int64 { return t0 + 45 }
	RecordLeaderTaskSuccess(task)
	if got := testutil.ToFloat64(LeaderTaskAgeSeconds.WithLabelValues(task)); got != 0 {
		t.Errorf("age immediately after a fresh success = %v, want 0", got)
	}
}
