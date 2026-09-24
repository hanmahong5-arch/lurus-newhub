package common

import (
	"errors"
	"fmt"
	"testing"
)

// resetBillingBreaker forces the package-global breaker back to closed so each
// test starts from a known state (the breaker is a shared singleton).
func resetBillingBreaker(t *testing.T) {
	t.Helper()
	BillingBreakerSuccess()
	t.Cleanup(BillingBreakerSuccess)
}

// openBillingBreaker drives the breaker to OPEN by recording threshold failures.
func openBillingBreaker(t *testing.T) {
	t.Helper()
	for i := 0; i < billingBreaker.threshold; i++ {
		BillingBreakerFailure()
	}
	if !BillingBreakerIsOpen() {
		t.Fatalf("expected breaker OPEN after %d failures", billingBreaker.threshold)
	}
}

// TestBillingBreakerIsOpen_ReadOnly verifies IsOpen reflects state and, unlike
// BillingBreakerAllow, does not mutate it (no half-open transition side effect).
func TestBillingBreakerIsOpen_ReadOnly(t *testing.T) {
	resetBillingBreaker(t)

	if BillingBreakerIsOpen() {
		t.Fatal("breaker should start closed")
	}
	openBillingBreaker(t)
	// Repeated reads must keep returning true and not flip the breaker to half-open.
	for i := 0; i < 3; i++ {
		if !BillingBreakerIsOpen() {
			t.Fatalf("IsOpen read %d unexpectedly returned false", i)
		}
	}
}

// TestTryDegradedPreAuth_BreakerClosed_Denies: the degrade path must never
// engage while the platform is healthy — that would skip pre-auth on a working
// breaker and hand out unsecured spend.
func TestTryDegradedPreAuth_BreakerClosed_Denies(t *testing.T) {
	resetBillingBreaker(t)
	RedisEnabled = false

	if TryDegradedPreAuth("tenantX", 123, 0.5, errors.New("billing service error")) {
		t.Fatal("degrade must be denied when breaker is closed")
	}
}

// TestTryDegradedPreAuth_InsufficientBalance_Denies: even with the breaker open,
// a genuine insufficient_balance rejection must fail closed (402) — degrading it
// would let a broke user spend for free.
func TestTryDegradedPreAuth_InsufficientBalance_Denies(t *testing.T) {
	resetBillingBreaker(t)
	openBillingBreaker(t)
	RedisEnabled = false

	if TryDegradedPreAuth("tenantX", 123, 0.5, ErrInsufficientBalance) {
		t.Fatal("insufficient_balance must never degrade, even with breaker open")
	}
}

// TestTryDegradedPreAuth_NoRedis_FailsClosed: with the breaker open but no Redis,
// neither the cached balance nor the spend cap can be consulted — the safe
// default is to deny (no unbounded unsecured spend).
func TestTryDegradedPreAuth_NoRedis_FailsClosed(t *testing.T) {
	resetBillingBreaker(t)
	openBillingBreaker(t)
	RedisEnabled = false

	if TryDegradedPreAuth("tenantX", 123, 0.5, errors.New("billing service error")) {
		t.Fatal("degrade must fail closed without Redis (cannot bound spend)")
	}
}

// TestDegradeAdmissible_Policy exhaustively covers the PURE degrade policy — the
// admit path included, which the I/O-wrapped TryDegradedPreAuth can't reach in a
// Redis-less unit test. walletTrustThreshold is 10.0 LB.
func TestDegradeAdmissible_Policy(t *testing.T) {
	errOther := errors.New("billing service error")
	// The sentinel itself, not a string-identical lookalike: classification is
	// by identity now (cycle-13 L9), so a hand-built errors.New with the same
	// text would no longer exercise this guard.
	errInsufficient := ErrInsufficientBalance
	cases := []struct {
		name           string
		breakerOpen    bool
		haveFreshCache bool
		balance        float64
		estimatedLB    float64
		err            error
		want           bool
	}{
		{"admit: open + fresh + ample balance", true, true, 100, 1, errOther, true},
		{"deny: breaker closed (platform healthy)", false, true, 100, 1, errOther, false},
		{"deny: insufficient_balance never degrades", true, true, 100, 1, errInsufficient, false},
		{"deny: no fresh cache", true, false, 100, 1, errOther, false},
		{"deny: balance below trust floor", true, true, 9, 1, errOther, false},
		{"deny: estimate too close to balance (3x margin)", true, true, 12, 5, errOther, false},
		{"admit: exactly clears 3x margin and floor", true, true, 30.01, 10, errOther, true},
		{"deny: balance equals floor (strict >)", true, true, 10, 1, errOther, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := degradeAdmissible(tc.breakerOpen, tc.haveFreshCache, tc.balance, tc.estimatedLB, tc.err)
			if got != tc.want {
				t.Fatalf("degradeAdmissible(open=%v,cache=%v,bal=%.2f,est=%.2f,err=%v) = %v, want %v",
					tc.breakerOpen, tc.haveFreshCache, tc.balance, tc.estimatedLB, tc.err, got, tc.want)
			}
		})
	}
}

// TestReserveDegradedSpend_NoRedis_FailsClosed asserts the cap reservation is
// fail-closed when Redis is unavailable.
func TestReserveDegradedSpend_NoRedis_FailsClosed(t *testing.T) {
	RedisEnabled = false
	if reserveDegradedSpend("tenantX", 1.0) {
		t.Fatal("reserveDegradedSpend must return false without Redis")
	}
}

// errWrappingSentinel wraps ErrInsufficientBalance without repeating its text.
// It is what makes the identity arm of platformAnswered load-bearing: the
// substring arm cannot see a sentinel whose carrier renamed the message.
type errWrappingSentinel struct{ inner error }

func (e errWrappingSentinel) Error() string { return "billing: request refused" }
func (e errWrappingSentinel) Unwrap() error { return e.inner }

// TestDegradeAdmissible_PlatformAnsweredNeverDegrades is the money-path lock on
// the rule cycle-14 L9 made explicit: the degrade path exists for the platform
// NOT ANSWERING. Every error that is the platform's own verdict on the request
// must fail closed, whatever word the platform used for it.
//
// This matters because splitting PreAuthorize's 400 arm (defect 2) introduced a
// brand-new error class into this decision. Before that split every 400 arrived
// as ErrInsufficientBalance and was refused; afterwards a 400 arrives as
// *PlatformRejectedError, and a policy keyed on the single literal
// "insufficient_balance" would serve, for free, any account the platform
// refused under some other spelling. The reachability is not theoretical:
// PreAuthorizeWithBreaker (billing_breaker.go) records the failure BEFORE
// returning, so the very request that opens the breaker is handed to
// TryDegradedPreAuth with BillingBreakerIsOpen() already true.
//
// Every row below holds the other four guards OPEN (breaker open, fresh cache,
// 100 LB against a 1 LB estimate), so the verdict turns entirely on how the
// error is classified.
//
// BLIND SPOT: this covers errors that either wrap the sentinel, ARE a
// *PlatformRejectedError, or name the sentinel's phrase in their text. An error
// that does none of the three — a future caller that re-describes a platform
// 4xx in words of its own, or a new platform-rejection type — still reads as an
// outage here and may degrade. The structural half of that guard is
// PreAuthorize returning *PlatformRejectedError for every non-balance 4xx
// (billing_client_rejection_test.go), not anything in this table.
func TestDegradeAdmissible_PlatformAnsweredNeverDegrades(t *testing.T) {
	rejected := func(reason string) error {
		return &PlatformRejectedError{Op: "pre-authorize", Status: 400, Reason: reason}
	}
	cases := []struct {
		name string
		err  error
		want bool // want = degradeAdmissible verdict
	}{
		// Account-state refusals. None of these spell "insufficient_balance",
		// and every one of them means this account must not spend.
		{"wallet frozen", rejected("wallet_frozen"), false},
		{"account suspended", rejected("account_suspended"), false},
		{"credit limit exceeded", rejected("credit_limit_exceeded"), false},
		{"insufficient funds (a synonym)", rejected("insufficient_funds"), false},
		// A request-shape refusal. Serving it unsecured would be defensible as
		// an availability trade, but it is an operator policy decision and not
		// something a defect fix gets to smuggle in — the platform ANSWERED.
		{"idempotency key required", rejected("idempotency_key_required"), false},
		// An unparseable 400: the least-understood refusal there is.
		{"unparseable body", rejected("unknown"), false},
		// Contains the phrase but is not the sentinel — old substring policy
		// got this one right by accident, identity-only policy gets it wrong.
		{"reason contains the phrase", rejected("upstream_insufficient_balance"), false},
		// The sentinel, straight and wrapped three ways.
		{"the sentinel itself", ErrInsufficientBalance, false},
		{"sentinel wrapped with %w", fmt.Errorf("preauth leg: %w", ErrInsufficientBalance), false},
		{"sentinel FLATTENED with %v (identity is lost)", fmt.Errorf("preauth: %v", ErrInsufficientBalance), false}, //nolint:errorlint // the flattening IS the case under test
		{"sentinel wrapped by a type that renames it", errWrappingSentinel{inner: ErrInsufficientBalance}, false},
		// The outage the path actually exists for: the platform did not answer.
		{"transport failure", errors.New("billing service unavailable"), true},
		{"breaker refused to call", errors.New("billing service circuit breaker open"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := degradeAdmissible(true, true, 100, 1, tc.err); got != tc.want {
				verb := "REFUSED to degrade"
				if got {
					verb = "ADMITTED an unsecured relay"
				}
				t.Fatalf("degradeAdmissible(err=%v) %s (=%v), want %v", tc.err, verb, got, tc.want)
			}
		})
	}
}
