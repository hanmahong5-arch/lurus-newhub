package metrics

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// leaderTaskAgeSeriesTasks collects the "task" label values
// LeaderTaskAgeSeconds currently exports, by walking the GaugeVec's own
// Collect() output. Reading it this way — instead of
// testutil.ToFloat64(LeaderTaskAgeSeconds.WithLabelValues(name)) — is what
// makes an ABSENCE assertion possible at all: WithLabelValues CREATES the
// child series it is asked for, so a test that reached for a task's gauge
// to prove the gauge does not exist would manufacture the very series it
// then measured (same reason method_label_test.go walks Collect()).
func leaderTaskAgeSeriesTasks(t *testing.T) map[string]float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 4096)
	LeaderTaskAgeSeconds.Collect(ch)
	close(ch)

	out := map[string]float64{}
	for m := range ch {
		var pm dto.Metric
		if err := m.Write(&pm); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		for _, lp := range pm.GetLabel() {
			if lp.GetName() == "task" {
				out[lp.GetValue()] = pm.GetGauge().GetValue()
			}
		}
	}
	return out
}

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

// TestRecordLeaderTaskSuccess_NeverStampedOrInactiveTaskHasNoAgeSeries is the
// negative oracle for the companion refresh (cycle-13 L10 repair, D-L10-1).
//
// The refresh's first cut recomputed an age for EVERY task taskreg knew
// about, reading each one's last-success stamp through
// WithLabelValues — which returns 0 for a task that has never stamped one.
// now - 0 is ~1.79e9 seconds, so the first success of ANY task published a
// ~57-year age for every registered task that had not yet succeeded, and
// production has exactly such a task by default: channel-health-test is
// registered on every master-capable replica but only stamps while
// AutoTestChannelEnabled is on, whose code default is false
// (internal/pkg/setting/operation_setting/monitor_setting.go), while the
// OpenRouter pool reaper stamps every 30s. newhub_task_stalled
// (deploy/r6-host-netdata/health.d/newhub.conf) would therefore have been
// WARNING from the first reaper tick on a healthy leader.
//
// Two skips prevent that, and this test pins both: a task whose last-success
// reading is 0 (never stamped in THIS process) and a task whose taskreg
// Active() is false (administratively off, so it cannot be late for a
// schedule it is not on — the same semantics GetSystemTasksV2 reports as
// standby) get no age series at all. Absence is the honest answer: the
// "never succeeded" signal stays visible on lurus_gateway_leader_task_last_success_timestamp_seconds
// itself, which that task's Start*WithContext entry point sets to 0 at boot.
func TestRecordLeaderTaskSuccess_NeverStampedOrInactiveTaskHasNoAgeSeries(t *testing.T) {
	const taskNever = "leader-task-age-test-never-stamped"
	const taskInactive = "leader-task-age-test-inactive"
	const taskDriver = "leader-task-age-test-driver"

	taskreg.Register(taskNever, func() time.Duration { return time.Minute }, true, nil)
	taskreg.Register(taskInactive, func() time.Duration { return time.Minute }, false, func() bool { return false })
	taskreg.Register(taskDriver, func() time.Duration { return 30 * time.Second }, true, nil)

	origNow := nowUnix
	t.Cleanup(func() { nowUnix = origNow })

	const t0 = int64(1_900_000_000)
	// The inactive task HAS a real last-success stamp (this is the same
	// write its own tick body would make through RecordLeaderTaskSuccess),
	// so the only reason it may be skipped below is Active() == false — not
	// "it never succeeded either".
	LeaderTaskLastSuccess.WithLabelValues(taskInactive).Set(float64(t0))

	nowUnix = func() int64 { return t0 + 3600 }
	RecordLeaderTaskSuccess(taskDriver)

	ages := leaderTaskAgeSeriesTasks(t)

	if _, ok := ages[taskDriver]; !ok {
		t.Fatalf("LeaderTaskAgeSeconds has no series for %q after its own success — the refresh "+
			"is not running at all, so this test would prove nothing about the two absences below "+
			"(series present: %v)", taskDriver, ages)
	}
	if got, ok := ages[taskNever]; ok {
		t.Errorf("LeaderTaskAgeSeconds exports task=%q = %v after another task's success, want no "+
			"series: that task has never stamped a success in this process, so its age would be "+
			"now-0 ≈ %v seconds and newhub_task_stalled would warn on a healthy replica",
			taskNever, got, float64(t0+3600))
	}
	if got, ok := ages[taskInactive]; ok {
		t.Errorf("LeaderTaskAgeSeconds exports task=%q = %v, want no series: taskreg reports this "+
			"task Active() == false (operator-disabled), and a job that is not on a schedule "+
			"cannot be late for one", taskInactive, got)
	}
}
