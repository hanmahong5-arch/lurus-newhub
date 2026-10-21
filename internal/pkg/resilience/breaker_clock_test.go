package resilience

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is an injectable, manually advanced clock for the breaker.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func clockedRegistry(clk *fakeClock, cfg Config) *Registry {
	cfg.Now = clk.Now
	return NewRegistry(cfg)
}

// Open stays closed to traffic for exactly the cooldown, then admits a probe.
func TestBreakerClock_OpenAdmitsProbeOnlyAfterCooldown(t *testing.T) {
	clk := newFakeClock()
	r := clockedRegistry(clk, Config{Threshold: 2, Timeout: 30 * time.Second, HalfOpenTimeout: 60 * time.Second})
	r.RecordFailure(1)
	r.RecordFailure(1)
	if got := r.GetState(1); got != StateOpen {
		t.Fatalf("state = %s, want open", got)
	}

	clk.Advance(29 * time.Second)
	if r.Allow(1) {
		t.Fatal("admitted a request 1s before the cooldown ended")
	}
	if got := r.GetState(1); got != StateOpen {
		t.Fatalf("refusal must not change state, got %s", got)
	}

	clk.Advance(1 * time.Second)
	if !r.Allow(1) {
		t.Fatal("cooldown elapsed but no probe was admitted")
	}
	if got := r.GetState(1); got != StateHalfOpen {
		t.Fatalf("state = %s, want half_open", got)
	}
}

// Exactly one probe is admitted when many callers race the cooldown boundary.
func TestBreakerClock_ExactlyOneProbeUnderContention(t *testing.T) {
	clk := newFakeClock()
	r := clockedRegistry(clk, Config{Threshold: 1, Timeout: 10 * time.Second, HalfOpenTimeout: time.Hour})
	r.RecordFailure(9)
	clk.Advance(10 * time.Second)

	const callers = 200
	var admitted atomic.Int32
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(callers)
	done.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			if r.Allow(9) {
				admitted.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	if n := admitted.Load(); n != 1 {
		t.Fatalf("%d probes admitted, want exactly 1", n)
	}
}

// A probe success closes the breaker and traffic flows again; a probe failure
// re-opens it with a FRESH cooldown (not the original one).
func TestBreakerClock_ProbeOutcomes(t *testing.T) {
	clk := newFakeClock()
	r := clockedRegistry(clk, Config{Threshold: 1, Timeout: 10 * time.Second, HalfOpenTimeout: time.Hour})

	r.RecordFailure(3)
	clk.Advance(10 * time.Second)
	if !r.Allow(3) {
		t.Fatal("probe not admitted")
	}
	r.RecordSuccess(3)
	if got := r.GetState(3); got != StateClosed {
		t.Fatalf("probe success left state %s, want closed", got)
	}
	for i := 0; i < 2; i++ {
		if !r.Allow(3) {
			t.Fatalf("closed breaker refused admission %d; it must admit everyone", i+1)
		}
	}

	r.RecordFailure(3) // threshold 1 -> open
	clk.Advance(10 * time.Second)
	if !r.Allow(3) {
		t.Fatal("second probe not admitted")
	}
	r.RecordFailure(3) // probe fails -> open again, cooldown restarts now
	if got := r.GetState(3); got != StateOpen {
		t.Fatalf("probe failure left state %s, want open", got)
	}
	clk.Advance(9 * time.Second)
	if r.Allow(3) {
		t.Fatal("cooldown after a failed probe must restart from the failure")
	}
	clk.Advance(1 * time.Second)
	if !r.Allow(3) {
		t.Fatal("restarted cooldown elapsed but no probe admitted")
	}
}

// The trip rule is CONSECUTIVE failures: a success resets the run, and an
// inconclusive outcome (a caller hang-up, a user 4xx) neither adds to it nor
// clears it. A batch caller hanging up many times therefore cannot open it.
func TestBreakerClock_ConsecutiveSemantics(t *testing.T) {
	clk := newFakeClock()
	r := clockedRegistry(clk, Config{Threshold: 3, Timeout: time.Minute})

	for i := 0; i < 500; i++ {
		r.Allow(5)
		r.RecordInconclusive(5)
	}
	if got := r.GetState(5); got != StateClosed {
		t.Fatalf("500 inconclusive outcomes tripped the breaker: %s", got)
	}

	r.RecordFailure(5)
	r.RecordFailure(5)
	r.RecordInconclusive(5) // neither counts nor resets
	r.RecordSuccess(5)      // resets
	r.RecordFailure(5)
	r.RecordFailure(5)
	if got := r.GetState(5); got != StateClosed {
		t.Fatalf("a success between failures must reset the run, state %s", got)
	}
	r.RecordFailure(5)
	if got := r.GetState(5); got != StateOpen {
		t.Fatalf("3 consecutive failures must trip, state %s", got)
	}
}

// Without a FailureWindow the run never ages (historical behaviour, kept so
// the default is unchanged); with one, a stale failure no longer counts.
func TestBreakerClock_FailureWindow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		window    time.Duration
		wantState State
	}{
		{"off_keeps_historical_behaviour", 0, StateOpen},
		{"on_ages_out_stale_failures", 10 * time.Minute, StateClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := newFakeClock()
			r := clockedRegistry(clk, Config{Threshold: 5, Timeout: time.Minute, FailureWindow: tc.window})
			for i := 0; i < 4; i++ {
				r.RecordFailure(2)
			}
			clk.Advance(3 * time.Hour)
			r.RecordFailure(2)
			if got := r.GetState(2); got != tc.wantState {
				t.Fatalf("state = %s, want %s", got, tc.wantState)
			}
		})
	}

	// Inside the window failures still accumulate.
	clk := newFakeClock()
	r := clockedRegistry(clk, Config{Threshold: 3, Timeout: time.Minute, FailureWindow: time.Minute})
	for i := 0; i < 3; i++ {
		clk.Advance(20 * time.Second)
		r.RecordFailure(4)
	}
	if got := r.GetState(4); got != StateOpen {
		t.Fatalf("failures inside the window must trip, state %s", got)
	}
}

func TestRegistry_AllOpen(t *testing.T) {
	clk := newFakeClock()
	r := clockedRegistry(clk, Config{Threshold: 1, Timeout: 30 * time.Second, HalfOpenTimeout: 60 * time.Second})

	if open, d := r.AllOpen(nil); open || d != 0 {
		t.Fatalf("empty list = (%v,%v), want (false,0)", open, d)
	}
	if open, _ := r.AllOpen([]int{1}); open {
		t.Fatal("unknown channel must not read as open")
	}
	if got := len(r.Snapshot()); got != 0 {
		t.Fatalf("AllOpen created %d breakers; it must be read-only", got)
	}

	r.RecordFailure(1) // open at t0
	clk.Advance(10 * time.Second)
	r.RecordFailure(2) // open at t0+10s
	r.RecordSuccess(3) // closed

	open, d := r.AllOpen([]int{1, 2})
	if !open || d != 20*time.Second {
		t.Fatalf("AllOpen(1,2) = (%v,%v), want (true,20s): channel 1 admits first", open, d)
	}
	if open, d := r.AllOpen([]int{1, 2, 3}); open || d != 0 {
		t.Fatalf("a closed member must make it false: (%v,%v)", open, d)
	}
	if open, d := r.AllOpen([]int{2, 99}); open || d != 0 {
		t.Fatalf("an unknown member must make it false: (%v,%v)", open, d)
	}

	// Read-only: asking never advanced the state machine or took the probe.
	clk.Advance(20 * time.Second) // channel 1 cooldown ends
	if open, d := r.AllOpen([]int{1, 2}); open || d != 0 {
		t.Fatalf("channel 1 would now admit a probe: (%v,%v)", open, d)
	}
	if got := r.GetState(1); got != StateOpen {
		t.Fatalf("AllOpen advanced channel 1 to %s", got)
	}
	if !r.Allow(1) {
		t.Fatal("AllOpen consumed the probe slot")
	}

	// HalfOpen with the probe still in flight is refusing too, until its bound.
	if open, d := r.AllOpen([]int{1}); !open || d != 60*time.Second {
		t.Fatalf("half-open in-flight = (%v,%v), want (true,60s)", open, d)
	}
	clk.Advance(60 * time.Second)
	if open, _ := r.AllOpen([]int{1}); open {
		t.Fatal("probe bound elapsed: a fresh probe is admissible, not all-open")
	}
}
