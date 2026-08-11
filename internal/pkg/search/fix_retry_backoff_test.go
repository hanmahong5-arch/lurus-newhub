package search

import (
	"errors"
	"strings"
	"testing"
)

// retryBackoffSetConfig swaps the package retry knobs and restores them on cleanup.
func retryBackoffSetConfig(t *testing.T, count, delay int) {
	t.Helper()
	origCount, origDelay := RetryCount, RetryDelay
	t.Cleanup(func() {
		RetryCount, RetryDelay = origCount, origDelay
	})
	RetryCount, RetryDelay = count, delay
}

// TestRetryWithBackoff_NonPositiveRetryCountStillRunsOnce guards the operator
// setting MEILISEARCH_RETRY_COUNT=0 (or a negative value): the loop bound used
// to be RetryCount verbatim, so fn was never invoked and the caller got a
// wrapped nil error.
func TestRetryWithBackoff_NonPositiveRetryCountStillRunsOnce(t *testing.T) {
	for _, count := range []int{0, -3} {
		retryBackoffSetConfig(t, count, 0)

		calls := 0
		if err := RetryWithBackoff(func() error {
			calls++
			return nil
		}); err != nil {
			t.Fatalf("RetryCount=%d: unexpected error: %v", count, err)
		}
		if calls != 1 {
			t.Errorf("RetryCount=%d: fn called %d times, want 1", count, calls)
		}
	}
}

// TestRetryWithBackoff_NonPositiveRetryCountWrapsRealError checks the failure
// path: exactly one attempt, and the returned error wraps the real one instead
// of a nil (which rendered as a fmt verb artifact in logs).
func TestRetryWithBackoff_NonPositiveRetryCountWrapsRealError(t *testing.T) {
	retryBackoffSetConfig(t, 0, 0)

	sentinel := errors.New("index unavailable")
	calls := 0
	err := RetryWithBackoff(func() error {
		calls++
		return sentinel
	})
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error %q does not wrap the underlying failure", err)
	}
	if strings.Contains(err.Error(), "%!") {
		t.Errorf("error message contains a fmt verb artifact: %q", err)
	}
	if !strings.Contains(err.Error(), "failed after 1 retries") {
		t.Errorf("error message = %q, want the normalized attempt count", err)
	}
}

// TestRetryWithBackoff_PositiveRetryCountUnchanged pins the existing behaviour
// for a normal configuration.
func TestRetryWithBackoff_PositiveRetryCountUnchanged(t *testing.T) {
	retryBackoffSetConfig(t, 3, 0)

	calls := 0
	err := RetryWithBackoff(func() error {
		calls++
		return errors.New("boom")
	})
	if calls != 3 {
		t.Errorf("fn called %d times, want 3", calls)
	}
	if err == nil || !strings.Contains(err.Error(), "failed after 3 retries") {
		t.Errorf("error = %v, want 'failed after 3 retries'", err)
	}

	calls = 0
	if err := RetryWithBackoff(func() error {
		calls++
		if calls < 2 {
			return errors.New("transient")
		}
		return nil
	}); err != nil {
		t.Errorf("unexpected error after recovery: %v", err)
	}
	if calls != 2 {
		t.Errorf("fn called %d times, want 2 (fail then succeed)", calls)
	}
}
