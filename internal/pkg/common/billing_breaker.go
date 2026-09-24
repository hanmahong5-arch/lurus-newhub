package common

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// Billing breaker defaults. Exported so a caller that re-tunes the breaker
// (ConfigureBillingBreaker) can put it back exactly as it was.
const (
	BillingBreakerDefaultThreshold = 3
	BillingBreakerDefaultTimeout   = 15 * time.Second
	// BillingBreakerDefaultHalfOpenTimeout bounds the half-open probe. See
	// BillingBreakerAllow for why an unbounded half-open is a wedge.
	BillingBreakerDefaultHalfOpenTimeout = 15 * time.Second
)

// Read-only state labels returned by BillingBreakerState.
const (
	BillingBreakerStateClosed   = "closed"
	BillingBreakerStateOpen     = "open"
	BillingBreakerStateHalfOpen = "half_open"
)

// billingBreaker is a simple circuit breaker for platform billing calls.
// When the platform is unresponsive, this prevents cascading 10s timeouts
// on every request by fast-failing after consecutive failures.
//
// States:
//   - Closed: normal operation, all calls pass through
//   - Open: platform assumed down, calls rejected immediately with clear error
//   - HalfOpen: one probe request allowed; success → Closed, failure → Open
var billingBreaker = &platformBreaker{
	threshold:       BillingBreakerDefaultThreshold,
	timeout:         BillingBreakerDefaultTimeout,
	halfOpenTimeout: BillingBreakerDefaultHalfOpenTimeout,
}

type platformBreaker struct {
	mu               sync.Mutex
	consecutiveFails int
	lastFailTime     time.Time
	state            int // 0=closed, 1=open, 2=halfopen
	// halfOpenSince is when the current probe slot was handed out. Only
	// meaningful while state == 2; cleared on every exit from half-open.
	halfOpenSince   time.Time
	threshold       int
	timeout         time.Duration
	halfOpenTimeout time.Duration
}

// ConfigureBillingBreaker re-tunes the breaker and resets it to closed.
// A non-positive argument keeps the current value.
//
// This is a call seam, the same convention as getChannelFn in
// adapter/handler/relay.go: hermetic tests use it to drive
// closed → open → half-open without sleeping out the production 15s cooldown.
// Production code does not call it; the defaults above are the deployed
// configuration.
func ConfigureBillingBreaker(threshold int, timeout, halfOpenTimeout time.Duration) {
	billingBreaker.mu.Lock()
	defer billingBreaker.mu.Unlock()

	if threshold > 0 {
		billingBreaker.threshold = threshold
	}
	if timeout > 0 {
		billingBreaker.timeout = timeout
	}
	if halfOpenTimeout > 0 {
		billingBreaker.halfOpenTimeout = halfOpenTimeout
	}
	billingBreaker.consecutiveFails = 0
	billingBreaker.state = 0
	billingBreaker.halfOpenSince = time.Time{}
	billingBreaker.lastFailTime = time.Time{}
	metrics.BillingCircuitBreakerState.Set(0)
}

// BillingBreakerAllow checks if the billing circuit breaker permits a call.
// Returns nil if allowed, or an error describing why the call was rejected.
//
// MUTATES STATE: an open breaker past its cooldown becomes half-open here and
// the caller walks away holding the single probe slot. Every caller therefore
// owes the breaker a BillingBreakerSuccess or BillingBreakerFailure — a caller
// that only wants to LOOK at the breaker must use BillingBreakerState /
// BillingBreakerIsOpen instead. GET /api/health used to call this every 5s
// from the readiness probe and never report back, which wedged the breaker in
// half-open (every real billing call refused) until the pod restarted.
func BillingBreakerAllow() error {
	billingBreaker.mu.Lock()
	defer billingBreaker.mu.Unlock()

	switch billingBreaker.state {
	case 0: // closed
		return nil
	case 1: // open
		if time.Since(billingBreaker.lastFailTime) >= billingBreaker.timeout {
			billingBreaker.state = 2 // transition to half-open
			billingBreaker.halfOpenSince = time.Now()
			metrics.BillingCircuitBreakerState.Set(2)
			return nil
		}
		return fmt.Errorf("billing service temporarily unavailable (circuit open, retry in %ds)",
			int(billingBreaker.timeout.Seconds()-time.Since(billingBreaker.lastFailTime).Seconds()))
	case 2: // half-open — one probe at a time
		// Past the probe's deadline we must assume it will never report:
		// nothing else leaves half-open, so without this the FIRST caller to
		// take the slot and die (or forget to report) freezes every billing
		// call for the life of the process. Re-arm and hand the slot to this
		// caller instead.
		if time.Since(billingBreaker.halfOpenSince) >= billingBreaker.halfOpenTimeout {
			billingBreaker.halfOpenSince = time.Now()
			return nil
		}
		return fmt.Errorf("billing service recovering (probe in progress)")
	}
	return nil
}

// BillingBreakerIsOpen reports whether the breaker is currently OPEN (platform
// deemed unreachable). Read-only — unlike BillingBreakerAllow it never mutates
// state (no half-open transition), so the P1-2 degrade path can consult it
// without perturbing the breaker. half-open (state 2) returns false: a probe is
// in flight, so we are not in the "platform is down" condition that degradation
// is meant to cover.
func BillingBreakerIsOpen() bool {
	billingBreaker.mu.Lock()
	defer billingBreaker.mu.Unlock()
	return billingBreaker.state == 1
}

// BillingBreakerState returns the breaker's current state as a label —
// "closed", "open" or "half_open" — for surfaces that must REPORT the breaker
// rather than use it (health checks, admin views). Read-only, like
// BillingBreakerIsOpen and unlike BillingBreakerAllow: it grants nothing and
// consumes nothing, so it is safe to poll every few seconds.
//
// Blind spot, deliberately: it reports the LAST RECORDED state. An open
// breaker whose cooldown has already elapsed still reads "open", and a
// half-open breaker whose probe deadline has elapsed still reads "half_open",
// because the transition happens inside BillingBreakerAllow — performing it
// here would be exactly the mutation this function exists to avoid.
func BillingBreakerState() string {
	billingBreaker.mu.Lock()
	defer billingBreaker.mu.Unlock()
	switch billingBreaker.state {
	case 1:
		return BillingBreakerStateOpen
	case 2:
		return BillingBreakerStateHalfOpen
	default:
		return BillingBreakerStateClosed
	}
}

// BillingBreakerSuccess records a successful billing call.
func BillingBreakerSuccess() {
	billingBreaker.mu.Lock()
	defer billingBreaker.mu.Unlock()

	if billingBreaker.state != 0 {
		slog.Info("billing circuit breaker closed — platform recovered")
	}
	billingBreaker.consecutiveFails = 0
	billingBreaker.state = 0
	billingBreaker.halfOpenSince = time.Time{}
	metrics.BillingCircuitBreakerState.Set(0)
}

// BillingBreakerFailure records a failed billing call.
func BillingBreakerFailure() {
	billingBreaker.mu.Lock()
	defer billingBreaker.mu.Unlock()

	billingBreaker.consecutiveFails++
	billingBreaker.lastFailTime = time.Now()

	switch billingBreaker.state {
	case 0: // closed
		if billingBreaker.consecutiveFails >= billingBreaker.threshold {
			billingBreaker.state = 1
			metrics.BillingCircuitBreakerState.Set(1)
			slog.Warn("billing circuit breaker OPEN — platform unreachable",
				"consecutive_failures", billingBreaker.consecutiveFails)
		}
	case 2: // half-open probe failed
		billingBreaker.state = 1
		billingBreaker.halfOpenSince = time.Time{}
		metrics.BillingCircuitBreakerState.Set(1)
		slog.Warn("billing circuit breaker re-opened — probe failed")
	}
}

// PreAuthorizeWithBreaker wraps PreAuthorizeGRPC with circuit breaker protection.
// When the breaker is open, returns immediately without making a network call.
func PreAuthorizeWithBreaker(ctx context.Context, accountID int64, amount float64,
	productID, referenceID, description string, ttlSeconds int) (*PreAuthResult, error) {

	if err := BillingBreakerAllow(); err != nil {
		return nil, err
	}

	result, err := PreAuthorizeGRPC(ctx, accountID, amount, productID, referenceID, description, ttlSeconds)
	if err != nil {
		BillingBreakerFailure()
		return nil, err
	}

	BillingBreakerSuccess()
	return result, nil
}

// SettleWithBreaker wraps SettlePreAuthGRPC with circuit breaker protection.
func SettleWithBreaker(ctx context.Context, preAuthID int64, actualAmount float64) (*SettlePreAuthResult, error) {
	if err := BillingBreakerAllow(); err != nil {
		return nil, err
	}

	result, err := SettlePreAuthGRPC(ctx, preAuthID, actualAmount)
	if err != nil {
		BillingBreakerFailure()
		return nil, err
	}

	BillingBreakerSuccess()
	return result, nil
}

// ReleaseWithBreaker wraps ReleasePreAuthGRPC with circuit breaker protection.
func ReleaseWithBreaker(ctx context.Context, preAuthID int64) error {
	if err := BillingBreakerAllow(); err != nil {
		return err
	}

	if err := ReleasePreAuthGRPC(ctx, preAuthID); err != nil {
		BillingBreakerFailure()
		return err
	}

	BillingBreakerSuccess()
	return nil
}
