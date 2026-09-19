package common

import (
	"testing"
	"time"
)

// rateLimitHasKey reports whether the limiter still holds a bucket for key.
// It takes the limiter's own mutex, so it is safe to call while the sweeper
// is running.
func rateLimitHasKey(l *InMemoryRateLimiter, key string) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	_, ok := l.store[key]
	return ok
}

// TestInMemoryRateLimiter_CleanerStops is the oracle for InMemoryRateLimiter's
// stop channel: Init spawns the expiry sweeper, and before this there was no
// way to end it — the goroutine outlived every test that called Init and kept
// taking the limiter's mutex for the rest of the binary. Stop must end it.
//
// The assertion is on the sweeper's only observable effect — eviction of an
// idle bucket — not on runtime.NumGoroutine(). The first version of this test
// compared goroutine counts against a baseline taken before Init, and under
// `go test -shuffle=on` it failed roughly one run in three: the count is
// process-wide, so an unrelated goroutine from an earlier test exiting between
// the two samples cancels the sweeper's +1 and the liveness half of the test
// reports "Init did not start the sweeper". A test that fails on other tests'
// scheduling is not a gate, and this one guards the CI race job.
func TestInMemoryRateLimiter_CleanerStops(t *testing.T) {
	// Eviction compares whole Unix SECONDS (clearExpiredItems:
	// now-lastSeen > int64(expirationDuration.Seconds())), so any sub-second
	// expiry evicts on the first tick after the second rolls over. A short
	// ticker keeps that first tick close.
	const expiry = 20 * time.Millisecond

	l := &InMemoryRateLimiter{}
	l.Init(expiry)
	if !l.Request("evicted-key", 5, 60) {
		t.Fatal("first request should be allowed")
	}

	// Liveness: the sweeper must actually evict, or the second half of this
	// test would pass vacuously against a sweeper that never ran.
	if !rateLimitWaitFor(3*time.Second, func() bool { return !rateLimitHasKey(l, "evicted-key") }) {
		t.Fatal("the sweeper never evicted an idle bucket within 3s — Init did not start it, so this test cannot prove Stop ends it")
	}

	l.Stop()

	if !l.Request("survivor-key", 5, 60) {
		t.Fatal("request after Stop should still be allowed")
	}
	// A live sweeper evicts within ~1s (see the seconds granularity above);
	// wait comfortably past that.
	time.Sleep(1500 * time.Millisecond)
	if !rateLimitHasKey(l, "survivor-key") {
		t.Fatal("an idle bucket was evicted 1.5s after Stop() — the sweeper is still running, i.e. the stop branch in clearExpiredItems is not being taken")
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
