package taskreg

import (
	"testing"
	"time"
)

// TestTaskreg_RegisterSnapshot drives the real Register/Snapshot pair (no
// hand-built Task struct standing in for the registry). It registers two
// tasks with distinct names and asserts Snapshot returns both, in
// registration order, with every field — including a live call through the
// Interval func — intact. Mutation: swapping the leaderOnly bool the
// registry stores would go undetected by any test that only checks Name.
func TestTaskreg_RegisterSnapshot(t *testing.T) {
	before := len(Snapshot())

	Register("taskreg-test-a", func() time.Duration { return 5 * time.Second }, true, nil)
	Register("taskreg-test-b", func() time.Duration { return 30 * time.Minute }, false, nil)

	got := Snapshot()
	if len(got) != before+2 {
		t.Fatalf("Snapshot() len = %d, want %d (before=%d + 2 new registrations)", len(got), before+2, before)
	}

	a, b := got[before], got[before+1]

	if a.Name != "taskreg-test-a" {
		t.Errorf("got[%d].Name = %q, want %q", before, a.Name, "taskreg-test-a")
	}
	if !a.LeaderOnly {
		t.Errorf("taskreg-test-a.LeaderOnly = false, want true")
	}
	if d := a.Interval(); d != 5*time.Second {
		t.Errorf("taskreg-test-a.Interval() = %s, want 5s", d)
	}
	if a.Active != nil {
		t.Errorf("taskreg-test-a.Active = non-nil, want nil (Register's active arg was nil)")
	}

	if b.Name != "taskreg-test-b" {
		t.Errorf("got[%d].Name = %q, want %q", before+1, b.Name, "taskreg-test-b")
	}
	if b.LeaderOnly {
		t.Errorf("taskreg-test-b.LeaderOnly = true, want false")
	}
	if d := b.Interval(); d != 30*time.Minute {
		t.Errorf("taskreg-test-b.Interval() = %s, want 30m", d)
	}
}

// TestTaskreg_ActiveDefaultsToAlwaysActiveWhenNil pins the nil-means-active
// convention documented on Task.Active: a task registered with a nil active
// func must not have Snapshot silently invent a non-nil stand-in — callers
// (GetSystemTasksV2) are the ones that treat a nil Active as "always
// active", and they can only do that if Snapshot hands the nil straight
// through.
func TestTaskreg_ActiveDefaultsToAlwaysActiveWhenNil(t *testing.T) {
	Register("taskreg-test-nil-active", func() time.Duration { return time.Minute }, false, nil)

	got := Snapshot()
	last := got[len(got)-1]
	if last.Name != "taskreg-test-nil-active" {
		t.Fatalf("last registered task = %q, want taskreg-test-nil-active", last.Name)
	}
	if last.Active != nil {
		t.Errorf("Active = non-nil, want nil")
	}
}

// TestTaskreg_ActiveFuncRoundTrips proves a non-nil active func passed to
// Register is the exact func Snapshot hands back — not a copy that drops
// its closed-over state. Mutation target: a Register that stores
// `func() bool { return true }` instead of the caller's func would pass
// every other test in this file (Name/LeaderOnly/Interval all still match)
// but flip this to red.
func TestTaskreg_ActiveFuncRoundTrips(t *testing.T) {
	toggle := false
	Register("taskreg-test-active-roundtrip", func() time.Duration { return time.Minute }, false, func() bool { return toggle })

	got := Snapshot()
	last := got[len(got)-1]
	if last.Active == nil {
		t.Fatalf("Active = nil, want the registered func")
	}
	if last.Active() != false {
		t.Errorf("Active() = true, want false (toggle starts false)")
	}
	toggle = true
	if last.Active() != true {
		t.Errorf("Active() = false after toggle flipped to true, want true — Snapshot must hand back the same closure, not a value copy of its result")
	}
}

// TestTaskreg_SnapshotIsACopy proves a caller mutating the returned slice
// cannot corrupt the registry — Snapshot's whole purpose (Read once, use
// once) requires it to hand back an independent copy each call.
func TestTaskreg_SnapshotIsACopy(t *testing.T) {
	Register("taskreg-test-copy", func() time.Duration { return time.Second }, false, nil)

	first := Snapshot()
	for i := range first {
		first[i].Name = "corrupted"
	}

	second := Snapshot()
	for i, task := range second {
		if task.Name == "corrupted" {
			t.Fatalf("Snapshot()[%d].Name observed a mutation made to a previous Snapshot() call's slice — Snapshot is not returning an independent copy", i)
		}
	}
}
