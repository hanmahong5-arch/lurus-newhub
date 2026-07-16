package resilience

import (
	"sync"
	"testing"
	"time"
)

// TestStateString verifies the human-readable names used in metrics/logging,
// including the unknown fallback for out-of-range values.
func TestStateString(t *testing.T) {
	cases := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half_open"},
		{State(99), "unknown(99)"},
		{State(-1), "unknown(-1)"},
	}
	for _, c := range cases {
		if got := c.state.String(); got != c.want {
			t.Errorf("State(%d).String() = %q, want %q", int(c.state), got, c.want)
		}
	}
}

// TestDefaultConfigDefaults verifies production defaults when no env override is set.
func TestDefaultConfigDefaults(t *testing.T) {
	t.Setenv("CB_THRESHOLD", "")
	t.Setenv("CB_TIMEOUT_SEC", "")
	cfg := DefaultConfig()
	if cfg.Threshold != 5 {
		t.Errorf("default Threshold = %d, want 5", cfg.Threshold)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("default Timeout = %v, want 30s", cfg.Timeout)
	}
}

// TestDefaultConfigEnvOverride verifies valid env values override the defaults.
func TestDefaultConfigEnvOverride(t *testing.T) {
	t.Setenv("CB_THRESHOLD", "12")
	t.Setenv("CB_TIMEOUT_SEC", "7")
	cfg := DefaultConfig()
	if cfg.Threshold != 12 {
		t.Errorf("Threshold = %d, want 12", cfg.Threshold)
	}
	if cfg.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want 7s", cfg.Timeout)
	}
}

// TestDefaultConfigEnvInvalidIgnored verifies malformed / non-positive env values
// are ignored and defaults are retained (guards against a bad deploy-time env
// tripping the breaker at 0 or a negative threshold).
func TestDefaultConfigEnvInvalidIgnored(t *testing.T) {
	cases := []struct {
		threshold string
		timeout   string
	}{
		{"notanumber", "notanumber"},
		{"0", "0"},   // non-positive rejected by n > 0 guard
		{"-3", "-9"}, // negative rejected
	}
	for _, c := range cases {
		t.Setenv("CB_THRESHOLD", c.threshold)
		t.Setenv("CB_TIMEOUT_SEC", c.timeout)
		cfg := DefaultConfig()
		if cfg.Threshold != 5 {
			t.Errorf("CB_THRESHOLD=%q: Threshold = %d, want 5 (default)", c.threshold, cfg.Threshold)
		}
		if cfg.Timeout != 30*time.Second {
			t.Errorf("CB_TIMEOUT_SEC=%q: Timeout = %v, want 30s (default)", c.timeout, cfg.Timeout)
		}
	}
}

// TestRecordSuccessFiresStateChangeCallback proves that recovery (Open→Closed)
// emits exactly one state-change event so metrics/alerting see the channel heal.
func TestRecordSuccessFiresStateChangeCallback(t *testing.T) {
	var transitions []struct{ from, to State }
	r := NewRegistry(Config{
		Threshold: 1,
		Timeout:   10 * time.Millisecond,
		OnStateChange: func(channelID int, from, to State) {
			if channelID != 7 {
				t.Errorf("callback channelID = %d, want 7", channelID)
			}
			transitions = append(transitions, struct{ from, to State }{from, to})
		},
	})

	r.RecordFailure(7) // Closed→Open (1 transition)
	time.Sleep(15 * time.Millisecond)
	if !r.Allow(7) { // Open→HalfOpen (no callback: Allow doesn't fire OnStateChange)
		t.Fatal("expected probe allowed after timeout")
	}
	r.RecordSuccess(7) // HalfOpen→Closed (1 transition via callback)

	if r.GetState(7) != StateClosed {
		t.Fatalf("expected Closed after probe success, got %s", r.GetState(7))
	}
	// Two callback-firing transitions: Closed→Open and HalfOpen→Closed.
	if len(transitions) != 2 {
		t.Fatalf("expected 2 callback transitions, got %d: %+v", len(transitions), transitions)
	}
	if transitions[0].from != StateClosed || transitions[0].to != StateOpen {
		t.Errorf("transition[0] = %+v, want Closed→Open", transitions[0])
	}
	if transitions[1].from != StateHalfOpen || transitions[1].to != StateClosed {
		t.Errorf("transition[1] = %+v, want HalfOpen→Closed", transitions[1])
	}
}

// TestRecordSuccessOnAlreadyClosedNoCallback verifies success on a healthy
// (Closed) channel is a no-op transition and does not spam the callback.
func TestRecordSuccessOnAlreadyClosedNoCallback(t *testing.T) {
	fired := 0
	r := NewRegistry(Config{
		Threshold:     3,
		Timeout:       time.Minute,
		OnStateChange: func(int, State, State) { fired++ },
	})
	r.RecordSuccess(1)
	r.RecordSuccess(1)
	if fired != 0 {
		t.Fatalf("expected no callback for Closed→Closed, fired %d times", fired)
	}
	if r.GetState(1) != StateClosed {
		t.Fatalf("expected Closed, got %s", r.GetState(1))
	}
}

// TestOpenRejectsBeforeTimeout proves that while a channel is Open and the
// cooldown has NOT elapsed, requests are rejected immediately (protecting the
// failing upstream from being hammered before it can recover).
func TestOpenRejectsBeforeTimeout(t *testing.T) {
	r := NewRegistry(Config{Threshold: 1, Timeout: time.Hour})
	r.RecordFailure(1) // → Open
	for i := 0; i < 5; i++ {
		if r.Allow(1) {
			t.Fatalf("iteration %d: expected Open breaker to reject before timeout", i)
		}
	}
	if r.GetState(1) != StateOpen {
		t.Fatalf("expected still Open, got %s", r.GetState(1))
	}
}

// TestOpenFailureExtendsTimeout proves that a failure recorded while already
// Open refreshes lastFailTime, extending the cooldown window.
func TestOpenFailureExtendsTimeout(t *testing.T) {
	// Margins are deliberately wide: time.Sleep on a loaded Windows runner
	// overshoots by tens of ms (measured 25ms → 43ms), so the gap between
	// "time since last failure" and the cooldown must absorb that.
	r := NewRegistry(Config{Threshold: 1, Timeout: 500 * time.Millisecond})
	r.RecordFailure(1) // → Open
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	r.RecordFailure(1) // still Open, resets lastFailTime
	time.Sleep(100 * time.Millisecond)
	// ~200ms total elapsed but well under 500ms since the last failure →
	// still within cooldown. Guard against pathological scheduling stalls:
	// only assert while the invariant "since last failure < timeout" holds.
	if time.Since(start) < 500*time.Millisecond {
		if r.Allow(1) {
			t.Fatal("expected Open breaker to still reject: cooldown was extended by the second failure")
		}
		if r.GetState(1) != StateOpen {
			t.Fatalf("expected Open, got %s", r.GetState(1))
		}
	} else {
		t.Skip("scheduler stalled past the cooldown window; timing invariant not reproducible")
	}
}

// TestCleanupRemovesInactiveBreakers proves stale per-channel breakers are
// evicted while active ones (and their tripped state) are preserved, preventing
// unbounded memory growth as channels churn.
func TestCleanupRemovesInactiveBreakers(t *testing.T) {
	r := NewRegistry(Config{Threshold: 1, Timeout: time.Minute})

	// Create three breakers; trip channel 1 into Open.
	r.RecordFailure(1) // Open
	r.Allow(2)         // creates a Closed breaker
	r.Allow(3)         // creates a Closed breaker

	active := map[int]struct{}{1: {}, 2: {}}
	r.Cleanup(active)

	// Channel 1 must survive AND retain its Open state.
	if r.GetState(1) != StateOpen {
		t.Fatalf("expected channel 1 to survive cleanup still Open, got %s", r.GetState(1))
	}
	// Channel 3 was removed; GetState recreates it fresh (Closed).
	r.mu.RLock()
	_, has3 := r.breakers[3]
	r.mu.RUnlock()
	if has3 {
		t.Fatal("expected inactive channel 3 to be removed by Cleanup")
	}
	if r.GetState(3) != StateClosed {
		t.Fatalf("expected recreated channel 3 to be fresh Closed, got %s", r.GetState(3))
	}
}

// TestCleanupEmptyActiveRemovesAll verifies an empty active set clears everything.
func TestCleanupEmptyActiveRemovesAll(t *testing.T) {
	r := NewRegistry(Config{Threshold: 5, Timeout: time.Minute})
	r.Allow(1)
	r.Allow(2)
	r.Cleanup(map[int]struct{}{})
	r.mu.RLock()
	n := len(r.breakers)
	r.mu.RUnlock()
	if n != 0 {
		t.Fatalf("expected all breakers removed, %d remain", n)
	}
}

// TestConcurrentTripsUnderRace hammers a single channel from many goroutines
// mixing failures, successes, allows and reads to prove the state machine is
// race-free and eventually settles into a valid state. Run with -race.
func TestConcurrentTripsUnderRace(t *testing.T) {
	r := NewRegistry(Config{Threshold: 5, Timeout: time.Millisecond})

	var wg sync.WaitGroup
	const goroutines = 32
	const iters = 200
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				switch (id + i) % 4 {
				case 0:
					r.RecordFailure(1)
				case 1:
					r.Allow(1)
				case 2:
					r.RecordSuccess(1)
				case 3:
					_ = r.GetState(1)
				}
			}
		}(g)
	}
	wg.Wait()

	// After the storm settles, the state must be one of the three valid values.
	switch s := r.GetState(1); s {
	case StateClosed, StateOpen, StateHalfOpen:
		// ok
	default:
		t.Fatalf("breaker settled into invalid state %s", s)
	}
}

// TestConcurrentGetOrCreateSingleBreaker proves that concurrent first-touch of a
// new channel yields exactly one shared breaker (double-checked-lock path),
// so failures across goroutines accumulate on the same counter rather than
// racing to create duplicate breakers.
func TestConcurrentGetOrCreateSingleBreaker(t *testing.T) {
	r := NewRegistry(Config{Threshold: 1000, Timeout: time.Minute})

	var wg sync.WaitGroup
	const goroutines = 50
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.RecordFailure(42) // all target the same, previously-unseen channel
		}()
	}
	wg.Wait()

	r.mu.RLock()
	b, ok := r.breakers[42]
	n := len(r.breakers)
	r.mu.RUnlock()
	if !ok {
		t.Fatal("expected channel 42 breaker to exist")
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 breaker registered, got %d", n)
	}
	// A single shared breaker means all 50 failures landed on one counter.
	b.mu.Lock()
	fails := b.consecutiveFails
	b.mu.Unlock()
	if fails != goroutines {
		t.Fatalf("expected %d accumulated failures on the shared breaker, got %d", goroutines, fails)
	}
}

// TestAllowInvalidStateFailsOpen exercises the defensive default branch in
// allow(): if a breaker ever holds an out-of-range State (a corrupted /
// unexpected value), it must fail open (permit the request) rather than
// wedge the channel shut forever. We inject the bad state directly via the
// same-package breaker struct since no public API can produce it.
func TestAllowInvalidStateFailsOpen(t *testing.T) {
	r := NewRegistry(Config{Threshold: 1, Timeout: time.Minute})
	b := r.getOrCreate(1)

	b.mu.Lock()
	b.state = State(99) // not Closed/Open/HalfOpen
	b.mu.Unlock()

	if !r.Allow(1) {
		t.Fatal("expected allow() to fail open (permit) for an unknown/corrupted state")
	}
	// The bad state is left untouched by allow(); it only decides pass/reject.
	if got := r.GetState(1); got != State(99) {
		t.Fatalf("expected state to remain State(99), got %s", got)
	}
}

// TestGetOrCreateDoubleCheckReturnsExisting drives the double-checked-lock
// early-return branch in getOrCreate: two goroutines contend to first-touch
// the same channel; the loser must observe the winner's breaker after taking
// the write lock and return it instead of overwriting. We assert only one
// breaker is ever registered and both callers share it, then loop enough
// times to make the interleaved (loser-after-write-lock) path reliably fire.
func TestGetOrCreateDoubleCheckReturnsExisting(t *testing.T) {
	for attempt := 0; attempt < 200; attempt++ {
		r := NewRegistry(Config{Threshold: 5, Timeout: time.Minute})

		var wg sync.WaitGroup
		results := make([]*breaker, 2)
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start // release both goroutines as close together as possible
				results[idx] = r.getOrCreate(7)
			}(i)
		}
		close(start)
		wg.Wait()

		if results[0] != results[1] {
			t.Fatalf("attempt %d: concurrent getOrCreate returned distinct breakers", attempt)
		}
		r.mu.RLock()
		n := len(r.breakers)
		r.mu.RUnlock()
		if n != 1 {
			t.Fatalf("attempt %d: expected exactly 1 breaker registered, got %d", attempt, n)
		}
	}
}
