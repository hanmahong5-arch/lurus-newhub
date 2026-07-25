package resilience

import (
	"testing"
	"time"
)

func TestRegistrySnapshot_EmptyRegistry(t *testing.T) {
	r := NewRegistry(DefaultConfig())
	if got := r.Snapshot(); len(got) != 0 {
		t.Errorf("fresh registry must report nothing, got %+v", got)
	}
}

// Snapshot must never be the thing that changes routing: reading status cannot
// create breakers or advance state machines.
func TestRegistrySnapshot_IsSideEffectFree(t *testing.T) {
	r := NewRegistry(Config{Threshold: 2, Timeout: 50 * time.Millisecond})

	r.RecordFailure(1)
	r.RecordFailure(1) // trips: consecutive fails reach the threshold

	before := r.Snapshot()
	if len(before) != 1 || before[0].State != "open" {
		t.Fatalf("expected one open breaker, got %+v", before)
	}

	// Let the probe deadline pass, then read status repeatedly.
	time.Sleep(60 * time.Millisecond)
	for i := 0; i < 3; i++ {
		snaps := r.Snapshot()
		if snaps[0].State != "open" {
			t.Fatalf("Snapshot advanced the state machine to %q — the half-open "+
				"probe slot must only be consumed by a real request", snaps[0].State)
		}
	}

	// The probe is still available for the next real admission check.
	if !r.Allow(1) {
		t.Error("half-open probe was consumed by status reads")
	}
}

func TestRegistrySnapshot_ReportsFailureProgressAndDeadline(t *testing.T) {
	r := NewRegistry(Config{Threshold: 5, Timeout: 30 * time.Second})

	r.RecordFailure(42)
	r.RecordFailure(42)

	snaps := r.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snaps))
	}
	s := snaps[0]
	if s.ChannelID != 42 {
		t.Errorf("channel id = %d, want 42", s.ChannelID)
	}
	if s.State != "closed" {
		t.Errorf("state = %q, want closed (below threshold)", s.State)
	}
	// "2/5" is the useful operator signal: this channel is degrading but has
	// not tripped yet.
	if s.ConsecutiveFails != 2 || s.Threshold != 5 {
		t.Errorf("failure progress = %d/%d, want 2/5", s.ConsecutiveFails, s.Threshold)
	}
	if s.LastFailUnix == 0 {
		t.Error("last_fail_unix must be set once a failure was recorded")
	}
	// A closed breaker has no probe deadline to report.
	if s.ProbeEligibleUnix != 0 {
		t.Errorf("probe_eligible_unix = %d, want 0 while closed", s.ProbeEligibleUnix)
	}
}

func TestRegistrySnapshot_OpenBreakerCarriesProbeDeadline(t *testing.T) {
	r := NewRegistry(Config{Threshold: 1, Timeout: 30 * time.Second})
	r.RecordFailure(9)

	s := r.Snapshot()[0]
	if s.State != "open" {
		t.Fatalf("state = %q, want open", s.State)
	}
	if s.ProbeEligibleUnix != s.LastFailUnix+30 {
		t.Errorf("probe_eligible_unix = %d, want last_fail + timeout = %d",
			s.ProbeEligibleUnix, s.LastFailUnix+30)
	}
}

func TestRegistrySnapshot_SuccessClearsFailureRun(t *testing.T) {
	r := NewRegistry(Config{Threshold: 3, Timeout: time.Second})
	r.RecordFailure(4)
	r.RecordFailure(4)
	r.RecordSuccess(4)

	s := r.Snapshot()[0]
	if s.ConsecutiveFails != 0 || s.State != "closed" {
		t.Errorf("recovery not reflected: %+v", s)
	}
}

func TestRegistrySnapshot_SortedByChannelID(t *testing.T) {
	r := NewRegistry(DefaultConfig())
	for _, id := range []int{30, 10, 20} {
		r.RecordFailure(id)
	}
	snaps := r.Snapshot()
	if len(snaps) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(snaps))
	}
	// Stable ordering keeps a polling console from reshuffling rows every tick.
	for i := 1; i < len(snaps); i++ {
		if snaps[i-1].ChannelID > snaps[i].ChannelID {
			t.Fatalf("snapshot not sorted by channel id: %+v", snaps)
		}
	}
}
