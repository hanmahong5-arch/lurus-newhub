package common

import (
	"testing"
	"time"
)

// covSnapshotVerificationState saves the package-level verification map/knobs
// so this file's direct manipulation of unexported state cannot leak into any
// other test in the package (all verification.go tests share one process-wide
// map + mutex).
func covSnapshotVerificationState(t *testing.T) {
	t.Helper()
	verificationMutex.Lock()
	prevMap := make(map[string]verificationValue, len(verificationMap))
	for k, v := range verificationMap {
		prevMap[k] = v
	}
	verificationMutex.Unlock()
	prevValidMinutes := VerificationValidMinutes

	t.Cleanup(func() {
		verificationMutex.Lock()
		verificationMap = prevMap
		verificationMutex.Unlock()
		VerificationValidMinutes = prevValidMinutes
	})
}

// TestRemoveExpiredPairs_DeletesOnlyExpiredEntries is the direct unit test for
// removeExpiredPairs: it must delete entries whose age has crossed the
// VerificationValidMinutes*60 threshold and leave fresh entries untouched. A
// stub that deleted everything (or nothing) must fail this.
func TestRemoveExpiredPairs_DeletesOnlyExpiredEntries(t *testing.T) {
	covSnapshotVerificationState(t)
	VerificationValidMinutes = 10 // 600s window

	verificationMutex.Lock()
	verificationMap = map[string]verificationValue{
		"fresh-key":   {code: "111111", time: time.Now()},
		"expired-key": {code: "222222", time: time.Now().Add(-700 * time.Second)},
		"boundary-key": {
			code: "333333",
			// int(700s truncation) — must land safely on the expired side.
			time: time.Now().Add(-601 * time.Second),
		},
	}
	removeExpiredPairs()
	_, freshStillThere := verificationMap["fresh-key"]
	_, expiredStillThere := verificationMap["expired-key"]
	_, boundaryStillThere := verificationMap["boundary-key"]
	verificationMutex.Unlock()

	if !freshStillThere {
		t.Error("fresh entry must survive removeExpiredPairs")
	}
	if expiredStillThere {
		t.Error("expired entry must be removed by removeExpiredPairs")
	}
	if boundaryStillThere {
		t.Error("entry past the exact expiry boundary must be removed")
	}
}

// TestRegisterVerificationCodeWithKey_TriggersPruneOverMaxSize covers the
// call site that invokes removeExpiredPairs indirectly: once the map exceeds
// verificationMapMaxSize, registering one more code must prune any already-
// expired entries in the same call so the map doesn't grow unbounded from
// abandoned verification attempts.
func TestRegisterVerificationCodeWithKey_TriggersPruneOverMaxSize(t *testing.T) {
	covSnapshotVerificationState(t)
	VerificationValidMinutes = 10

	verificationMutex.Lock()
	verificationMap = make(map[string]verificationValue, verificationMapMaxSize+2)
	// Fill past the max size with already-expired entries.
	for i := 0; i < verificationMapMaxSize+1; i++ {
		key := EmailVerificationPurpose + "stale-" + string(rune('a'+i))
		verificationMap[key] = verificationValue{code: "x", time: time.Now().Add(-1 * time.Hour)}
	}
	verificationMutex.Unlock()

	RegisterVerificationCodeWithKey("fresh-trigger", "999999", EmailVerificationPurpose)

	verificationMutex.Lock()
	remaining := len(verificationMap)
	_, freshPresent := verificationMap[EmailVerificationPurpose+"fresh-trigger"]
	verificationMutex.Unlock()

	if !freshPresent {
		t.Fatal("the just-registered code must be present after registration")
	}
	// All the stale pre-seeded entries should have been pruned, leaving only
	// the one freshly-registered entry.
	if remaining != 1 {
		t.Errorf("map size after prune-triggering register = %d, want 1 (all stale entries pruned)", remaining)
	}
}

// TestConsumeCodeWithKey_OneTimeUseUnderConcurrency asserts the documented
// atomicity guarantee: of N concurrent callers racing to consume the same
// valid code, exactly one must succeed. A regression to check-then-delete
// (releasing the lock between verify and delete) would let more than one
// goroutine through.
func TestConsumeCodeWithKey_OneTimeUseUnderConcurrency(t *testing.T) {
	covSnapshotVerificationState(t)
	VerificationValidMinutes = 10

	verificationMutex.Lock()
	verificationMap = map[string]verificationValue{
		EmailVerificationPurpose + "race-key": {code: "424242", time: time.Now()},
	}
	verificationMutex.Unlock()

	const n = 20
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func() {
			results <- ConsumeCodeWithKey("race-key", "424242", EmailVerificationPurpose)
		}()
	}
	successCount := 0
	for i := 0; i < n; i++ {
		if <-results {
			successCount++
		}
	}
	if successCount != 1 {
		t.Errorf("exactly 1 of %d concurrent consumers should succeed, got %d", n, successCount)
	}

	// The code must now be gone for good — a later attempt must fail.
	if ConsumeCodeWithKey("race-key", "424242", EmailVerificationPurpose) {
		t.Error("code must not be consumable a second time after being used")
	}
}

// TestConsumeCodeWithKey_WrongCodeDoesNotDelete ensures a mismatched code is
// rejected without deleting the still-valid stored entry, so a guesser can't
// burn the legitimate holder's one-time code by submitting a wrong guess.
func TestConsumeCodeWithKey_WrongCodeDoesNotDelete(t *testing.T) {
	covSnapshotVerificationState(t)
	VerificationValidMinutes = 10

	verificationMutex.Lock()
	verificationMap = map[string]verificationValue{
		EmailVerificationPurpose + "guard-key": {code: "555555", time: time.Now()},
	}
	verificationMutex.Unlock()

	if ConsumeCodeWithKey("guard-key", "wrong-guess", EmailVerificationPurpose) {
		t.Fatal("a wrong code must not be accepted")
	}
	if !ConsumeCodeWithKey("guard-key", "555555", EmailVerificationPurpose) {
		t.Error("the real code must still be consumable after a failed wrong-code attempt")
	}
}
