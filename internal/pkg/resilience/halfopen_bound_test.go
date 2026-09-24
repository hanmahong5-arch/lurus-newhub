package resilience

import (
	"testing"
	"time"
)

// TestBreakerHalfOpen_CannotOutliveItsBound is the state-machine table for the
// defect that wedged channels permanently (cycle 14 L1-A): HalfOpen admits one
// probe and then refuses everyone, and NOTHING but a reported outcome used to
// leave that state — the timeout check lived only in the Open branch. A probe
// that never reported (before this cycle: any probe ending in a user 4xx, a
// 402 or a cancellation, because relay.go only called RecordFailure for
// upstream failures) removed the channel from routing until the process
// restarted.
//
// Each row drives a real breaker through Open → HalfOpen and then asks what
// allow() does at a given point in the probe's lifetime.
func TestBreakerHalfOpen_CannotOutliveItsBound(t *testing.T) {
	cases := []struct {
		name            string
		halfOpenTimeout time.Duration
		waitAfterProbe  time.Duration
		wantAllow       bool
		reason          string
	}{
		{
			name:            "inside_the_bound_only_one_probe_runs",
			halfOpenTimeout: time.Hour,
			waitAfterProbe:  0,
			wantAllow:       false,
			reason:          "a second caller must not join an in-flight probe",
		},
		{
			name:            "inside_the_bound_still_one_probe_after_a_pause",
			halfOpenTimeout: time.Hour,
			waitAfterProbe:  20 * time.Millisecond,
			wantAllow:       false,
			reason:          "the probe's deadline has not passed yet",
		},
		{
			name:            "past_the_bound_admits_a_fresh_probe",
			halfOpenTimeout: 20 * time.Millisecond,
			waitAfterProbe:  40 * time.Millisecond,
			wantAllow:       true,
			reason:          "the admitted probe never reported; HalfOpen must not be a life sentence",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(Config{Threshold: 1, Timeout: 5 * time.Millisecond, HalfOpenTimeout: tc.halfOpenTimeout})
			r.RecordFailure(7) // → Open
			time.Sleep(10 * time.Millisecond)
			if !r.Allow(7) {
				t.Fatalf("setup: Open breaker past its cooldown must admit the probe")
			}
			if got := r.GetState(7); got != StateHalfOpen {
				t.Fatalf("setup: state = %s, want half_open", got)
			}

			time.Sleep(tc.waitAfterProbe)
			if got := r.Allow(7); got != tc.wantAllow {
				t.Fatalf("allow() = %v, want %v — %s", got, tc.wantAllow, tc.reason)
			}
		})
	}
}

// TestBreakerHalfOpen_ReArmsRepeatedlyWithoutAnyReport is the same defect seen
// over time rather than at one instant: a probe that never reports must not
// cost the channel more than one probe window, no matter how many windows
// pass. Before the fix the first Allow after the cooldown was the LAST true
// this breaker ever returned.
func TestBreakerHalfOpen_ReArmsRepeatedlyWithoutAnyReport(t *testing.T) {
	r := NewRegistry(Config{Threshold: 1, Timeout: 5 * time.Millisecond, HalfOpenTimeout: 15 * time.Millisecond})
	r.RecordFailure(3)
	time.Sleep(10 * time.Millisecond)
	if !r.Allow(3) {
		t.Fatal("setup: first probe must be admitted after the cooldown")
	}

	// Deliberately report NOTHING, as a wedged caller would.
	for i := 1; i <= 3; i++ {
		time.Sleep(25 * time.Millisecond)
		if !r.Allow(3) {
			t.Fatalf("probe window %d: channel is still excluded %v after the last admission — "+
				"HalfOpen has no escape, so this channel is dead until the process restarts",
				i, 25*time.Millisecond)
		}
	}
}

// TestBreakerRecordInconclusive_ReleasesTheProbeWithoutCountingAFailure pins
// the third outcome the relay needs: "the probe told us nothing". It must free
// the probe slot (so the channel is routable again) yet leave the failure run
// untouched (so a user's own 4xx cannot trip a healthy channel).
func TestBreakerRecordInconclusive_ReleasesTheProbeWithoutCountingAFailure(t *testing.T) {
	t.Run("half_open_returns_to_open_and_restarts_the_cooldown", func(t *testing.T) {
		r := NewRegistry(Config{Threshold: 2, Timeout: 10 * time.Millisecond, HalfOpenTimeout: time.Hour})
		r.RecordFailure(1)
		r.RecordFailure(1) // → Open at threshold 2
		time.Sleep(15 * time.Millisecond)
		if !r.Allow(1) {
			t.Fatal("setup: probe must be admitted")
		}

		r.RecordInconclusive(1)
		if got := r.GetState(1); got != StateOpen {
			t.Fatalf("state = %s, want open — an inconclusive probe hands the slot back", got)
		}
		// The failure run must not have grown: 2 failures tripped it, a third
		// would be visible in the snapshot.
		if got := r.Snapshot()[0].ConsecutiveFails; got != 2 {
			t.Errorf("consecutive fails = %d, want 2 — an inconclusive outcome is not a failure", got)
		}
		// Cooldown restarted from now, so the next probe is one timeout away.
		if r.Allow(1) {
			t.Error("a fresh probe was admitted immediately; the cooldown must restart")
		}
		time.Sleep(15 * time.Millisecond)
		if !r.Allow(1) {
			t.Error("after the cooldown the channel must get another probe")
		}
	})

	t.Run("closed_breaker_is_untouched", func(t *testing.T) {
		r := NewRegistry(Config{Threshold: 1, Timeout: time.Minute, HalfOpenTimeout: time.Minute})
		r.Allow(2)
		r.RecordInconclusive(2)
		if got := r.GetState(2); got != StateClosed {
			t.Fatalf("state = %s, want closed — a user 4xx must never trip a healthy channel", got)
		}
		if got := r.Snapshot()[0].ConsecutiveFails; got != 0 {
			t.Errorf("consecutive fails = %d, want 0", got)
		}
		if !r.Allow(2) {
			t.Error("closed breaker must keep admitting traffic")
		}
	})

	t.Run("open_breaker_is_untouched", func(t *testing.T) {
		r := NewRegistry(Config{Threshold: 1, Timeout: time.Minute, HalfOpenTimeout: time.Minute})
		r.RecordFailure(4) // → Open
		r.RecordInconclusive(4)
		if got := r.GetState(4); got != StateOpen {
			t.Fatalf("state = %s, want open", got)
		}
		if got := r.Snapshot()[0].ConsecutiveFails; got != 1 {
			t.Errorf("consecutive fails = %d, want 1 — no failure was recorded", got)
		}
	})
}

// TestBreakerHalfOpenTimeout_FallsBackWhenUnset guards the embedder-built
// Config (every existing test here, plus anything outside this package that
// composes a Config by hand): a zero HalfOpenTimeout must never mean "admit
// everyone while half-open", which would turn the breaker into a no-op exactly
// when an upstream is failing.
func TestBreakerHalfOpenTimeout_FallsBackWhenUnset(t *testing.T) {
	r := NewRegistry(Config{Threshold: 1, Timeout: time.Hour}) // no HalfOpenTimeout
	r.RecordFailure(5)
	b := r.getOrCreate(5)
	if b.halfOpenTimeout != time.Hour {
		t.Errorf("halfOpenTimeout = %v, want the configured Timeout (1h) as fallback", b.halfOpenTimeout)
	}

	r2 := NewRegistry(Config{Threshold: 1}) // neither knob set
	r2.RecordFailure(6)
	if got := r2.getOrCreate(6).halfOpenTimeout; got != defaultHalfOpenTimeout {
		t.Errorf("halfOpenTimeout = %v, want the package default %v", got, defaultHalfOpenTimeout)
	}
}

// TestDefaultConfig_HalfOpenTimeoutEnv covers the new deploy-time knob
// alongside the two that already existed.
func TestDefaultConfig_HalfOpenTimeoutEnv(t *testing.T) {
	t.Setenv("CB_HALF_OPEN_TIMEOUT_SEC", "")
	if got := DefaultConfig().HalfOpenTimeout; got != defaultHalfOpenTimeout {
		t.Errorf("default HalfOpenTimeout = %v, want %v", got, defaultHalfOpenTimeout)
	}
	t.Setenv("CB_HALF_OPEN_TIMEOUT_SEC", "9")
	if got := DefaultConfig().HalfOpenTimeout; got != 9*time.Second {
		t.Errorf("HalfOpenTimeout = %v, want 9s", got)
	}
	t.Setenv("CB_HALF_OPEN_TIMEOUT_SEC", "not-a-number")
	if got := DefaultConfig().HalfOpenTimeout; got != defaultHalfOpenTimeout {
		t.Errorf("malformed env must be ignored, got %v", got)
	}
	t.Setenv("CB_HALF_OPEN_TIMEOUT_SEC", "0")
	if got := DefaultConfig().HalfOpenTimeout; got != defaultHalfOpenTimeout {
		t.Errorf("0 must be ignored (it would disable the probe bound), got %v", got)
	}
}

// TestSnapshot_HalfOpenCarriesItsProbeDeadline keeps the operator surface
// honest about the new bound: /api/v2/admin/gateway shows "half_open" with the
// instant a fresh probe is admitted, instead of a state with no visible way out.
func TestSnapshot_HalfOpenCarriesItsProbeDeadline(t *testing.T) {
	r := NewRegistry(Config{Threshold: 1, Timeout: 5 * time.Millisecond, HalfOpenTimeout: time.Minute})
	r.RecordFailure(11)
	time.Sleep(10 * time.Millisecond)
	if !r.Allow(11) {
		t.Fatal("setup: probe must be admitted")
	}
	s := r.Snapshot()[0]
	if s.State != "half_open" {
		t.Fatalf("state = %q, want half_open", s.State)
	}
	if s.ProbeEligibleUnix == 0 {
		t.Fatal("half_open must publish when the next probe becomes eligible")
	}
	if delta := s.ProbeEligibleUnix - time.Now().Unix(); delta < 50 || delta > 61 {
		t.Errorf("next probe in %ds, want ~60s (the configured HalfOpenTimeout)", delta)
	}
}
