package common

import (
	"testing"
	"time"
)

// tuneBillingBreaker points the shared breaker at millisecond-scale windows
// for the duration of one test and puts the production defaults back after.
func tuneBillingBreaker(t *testing.T, timeout, halfOpenTimeout time.Duration) {
	t.Helper()
	ConfigureBillingBreaker(1, timeout, halfOpenTimeout)
	t.Cleanup(func() {
		ConfigureBillingBreaker(BillingBreakerDefaultThreshold,
			BillingBreakerDefaultTimeout, BillingBreakerDefaultHalfOpenTimeout)
	})
}

// TestBillingBreakerHalfOpen_CannotOutliveItsBound is the state-machine table
// for cycle 14 L1-B. Half-open admits one probe and refuses everyone else, and
// only BillingBreakerSuccess/Failure leave that state — so a caller that takes
// the slot and never reports (the readiness probe did exactly this every 5s)
// froze every billing call until the process restarted. Past the probe's
// deadline the breaker must hand the slot to the next caller instead.
func TestBillingBreakerHalfOpen_CannotOutliveItsBound(t *testing.T) {
	cases := []struct {
		name            string
		halfOpenTimeout time.Duration
		waitAfterProbe  time.Duration
		wantAllowed     bool
		reason          string
	}{
		{
			name:            "inside_the_bound_one_probe_at_a_time",
			halfOpenTimeout: time.Hour,
			waitAfterProbe:  0,
			wantAllowed:     false,
			reason:          "a second caller must not join an in-flight probe",
		},
		{
			name:            "past_the_bound_the_slot_is_handed_on",
			halfOpenTimeout: 20 * time.Millisecond,
			waitAfterProbe:  40 * time.Millisecond,
			wantAllowed:     true,
			reason:          "the probe never reported; half-open must not be permanent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tuneBillingBreaker(t, 5*time.Millisecond, tc.halfOpenTimeout)
			BillingBreakerFailure() // threshold 1 → open
			if !BillingBreakerIsOpen() {
				t.Fatal("setup: breaker must be open")
			}
			time.Sleep(10 * time.Millisecond)
			if err := BillingBreakerAllow(); err != nil {
				t.Fatalf("setup: open breaker past its cooldown must admit a probe: %v", err)
			}
			if got := BillingBreakerState(); got != BillingBreakerStateHalfOpen {
				t.Fatalf("setup: state = %q, want half_open", got)
			}

			time.Sleep(tc.waitAfterProbe)
			err := BillingBreakerAllow()
			if allowed := err == nil; allowed != tc.wantAllowed {
				t.Fatalf("allowed = %v (err=%v), want %v — %s", allowed, err, tc.wantAllowed, tc.reason)
			}
		})
	}
}

// TestBillingBreakerHalfOpen_ReArmsRepeatedlyWithoutAnyReport is the same
// defect over time: no number of elapsed windows unwedges a half-open breaker
// whose probe never reports, because nothing but a report leaves that state.
func TestBillingBreakerHalfOpen_ReArmsRepeatedlyWithoutAnyReport(t *testing.T) {
	tuneBillingBreaker(t, 5*time.Millisecond, 15*time.Millisecond)
	BillingBreakerFailure()
	time.Sleep(10 * time.Millisecond)
	if err := BillingBreakerAllow(); err != nil {
		t.Fatalf("setup: first probe must be admitted: %v", err)
	}

	for i := 1; i <= 3; i++ {
		time.Sleep(25 * time.Millisecond)
		if err := BillingBreakerAllow(); err != nil {
			t.Fatalf("window %d: billing calls are still refused (%v) long after the "+
				"probe's deadline — every wallet/pre-auth call fails until the pod restarts", i, err)
		}
	}
}

// TestBillingBreakerState_IsReadOnly pins the accessor the health check must
// use: reading the state, repeatedly, must never consume the probe slot.
func TestBillingBreakerState_IsReadOnly(t *testing.T) {
	tuneBillingBreaker(t, 5*time.Millisecond, time.Hour)
	if got := BillingBreakerState(); got != BillingBreakerStateClosed {
		t.Fatalf("fresh breaker state = %q, want closed", got)
	}

	BillingBreakerFailure() // threshold 1 → open
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if got := BillingBreakerState(); got != BillingBreakerStateOpen {
			t.Fatalf("read %d: state = %q, want open — reading must not advance the machine", i, got)
		}
	}
	// The probe slot survived the reads and is still there for a real caller.
	if err := BillingBreakerAllow(); err != nil {
		t.Fatalf("the probe slot was consumed by status reads: %v", err)
	}
	if got := BillingBreakerState(); got != BillingBreakerStateHalfOpen {
		t.Errorf("state = %q, want half_open once a real caller took the probe", got)
	}
}
