package common

import (
	"runtime"
	"testing"
	"time"
)

// TestInMemoryRateLimiter_CleanerStops is the oracle for InMemoryRateLimiter's
// stop channel: Init spawns the expiry sweeper, and before this there was no
// way to end it — the goroutine outlived every test that called Init and kept
// taking the limiter's mutex for the rest of the binary. Stop must end it.
//
// The assertion is on goroutine count rather than on an exported flag because
// the sweeper has no observable output; the count is taken before Init so the
// comparison is against this test's own baseline, not an absolute number that
// other packages' background goroutines would make flaky.
func TestInMemoryRateLimiter_CleanerStops(t *testing.T) {
	// A short expiry keeps the sweeper's loop iteration short, so a stop
	// signalled mid-sleep is observed quickly.
	const expiry = 20 * time.Millisecond

	before := runtime.NumGoroutine()

	l := &InMemoryRateLimiter{}
	l.Init(expiry)
	if !l.Request("stop-key", 5, 60) {
		t.Fatal("first request should be allowed")
	}

	// The sweeper is running: wait until the count actually rises, so a
	// scheduler that has not started the goroutine yet cannot make the
	// "it went back down" half of this test vacuous.
	if !rateLimitWaitFor(2*time.Second, func() bool { return runtime.NumGoroutine() > before }) {
		t.Fatalf("goroutine count never rose above the pre-Init baseline %d — Init did not start the sweeper, so this test cannot prove Stop ends it", before)
	}

	l.Stop()

	if !rateLimitWaitFor(time.Second, func() bool { return runtime.NumGoroutine() <= before }) {
		t.Fatalf("goroutine count still %d one second after Stop(), baseline was %d — the sweeper did not exit", runtime.NumGoroutine(), before)
	}
}

// TestInMemoryRateLimiter_StopIsIdempotentAndPreInitSafe covers the two calls
// production code could make by accident: stopping twice, and stopping a
// limiter that was never Init'd. Both must be no-ops rather than a panic on a
// closed or nil channel.
func TestInMemoryRateLimiter_StopIsIdempotentAndPreInitSafe(t *testing.T) {
	var never InMemoryRateLimiter
	never.Stop() // never Init'd
	never.Stop()

	l := &InMemoryRateLimiter{}
	l.Init(20 * time.Millisecond)
	l.Stop()
	l.Stop()

	// Stopping the sweeper must not break the limiter's decisions — the
	// sweeper only evicts idle keys, it is not part of the allow/deny path.
	if !l.Request("after-stop", 1, 60) {
		t.Error("first request after Stop should still be allowed")
	}
	if l.Request("after-stop", 1, 60) {
		t.Error("second request after Stop should still be rejected by the window")
	}
}

// rateLimitWaitFor polls cond until it is true or the timeout elapses.
func rateLimitWaitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
