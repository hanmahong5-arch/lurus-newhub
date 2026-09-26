package common

import (
	"context"
	"errors"
	"testing"
)

// TestRecordBillingOutcome_OnlyUnreachabilityTripsTheBreaker: the breaker is
// global, so what it counts decides whether one tenant can take billing down
// for all of them. Three answers the platform GAVE (an empty wallet, a 4xx
// refusal) must leave it closed; three calls our caller abandoned must too;
// three transport failures must open it.
func TestRecordBillingOutcome_OnlyUnreachabilityTripsTheBreaker(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	live := context.Background()

	cases := []struct {
		name     string
		ctx      context.Context
		err      error
		wantOpen bool
	}{
		{"insufficient balance is an answer", live, ErrInsufficientBalance, false},
		{"wrapped insufficient balance", live, errors.Join(errors.New("preauth"), ErrInsufficientBalance), false},
		{"platform 4xx refusal is an answer", live, &PlatformRejectedError{Op: "pre-authorize", Status: 400, Reason: "wallet_frozen"}, false},
		{"our caller gave up", canceled, context.Canceled, false},
		{"platform unreachable", live, errors.New("billing service unavailable"), true},
		{"platform timed out", live, context.DeadlineExceeded, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetBillingBreaker(t)
			for i := 0; i < billingBreaker.threshold; i++ {
				recordBillingOutcome(tc.ctx, tc.err)
			}
			if got := BillingBreakerIsOpen(); got != tc.wantOpen {
				t.Fatalf("after %d × %v: breaker open = %v, want %v", billingBreaker.threshold, tc.err, got, tc.wantOpen)
			}
		})
	}
}

// An answer from the platform also proves it is reachable, so it closes a
// breaker that a real outage opened (the half-open probe got a verdict).
func TestRecordBillingOutcome_AnswerClosesAnOpenBreaker(t *testing.T) {
	resetBillingBreaker(t)
	openBillingBreaker(t)
	recordBillingOutcome(context.Background(), ErrInsufficientBalance)
	if BillingBreakerIsOpen() {
		t.Fatal("a platform verdict must count as the platform being reachable")
	}
}
