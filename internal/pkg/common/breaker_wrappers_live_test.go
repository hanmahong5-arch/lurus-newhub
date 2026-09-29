package common

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
)

// TestWithBreaker_SuccessPath drives PreAuthorize/Settle/ReleaseWithBreaker down
// their happy path (the wrapped call returns nil error) and asserts each records
// success and leaves the breaker CLOSED.
func TestWithBreaker_SuccessPath(t *testing.T) {
	resetBillingBreaker(t)

	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/wallet/pre-authorize"):
			_ = json.NewEncoder(w).Encode(map[string]any{"preauth_id": 77, "status": "held", "amount": 2.0})
		case strings.Contains(r.URL.Path, "/settle"):
			_ = json.NewEncoder(w).Encode(map[string]any{"preauth_id": 77, "status": "settled"})
		case strings.Contains(r.URL.Path, "/release"):
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	ctx := context.Background()

	res, err := PreAuthorizeWithBreaker(ctx, 1, 2.0, "prod", "ref-1", "desc", 60)
	if err != nil || res == nil || res.PreAuthID != 77 {
		t.Fatalf("PreAuthorizeWithBreaker success: %+v err=%v", res, err)
	}
	if BillingBreakerIsOpen() {
		t.Error("breaker must stay closed after a successful pre-auth")
	}

	sres, err := SettleWithBreaker(ctx, 77, 1.5)
	if err != nil || sres == nil || sres.Status != "settled" {
		t.Fatalf("SettleWithBreaker success: %+v err=%v", sres, err)
	}
	if err := ReleaseWithBreaker(ctx, 77); err != nil {
		t.Fatalf("ReleaseWithBreaker success: %v", err)
	}
	if BillingBreakerIsOpen() {
		t.Error("breaker must stay closed after successful settle/release")
	}
}

// TestWithBreaker_FailurePath_OpensCircuit points billing at a dead endpoint so
// each wrapped call errors, and asserts the wrappers record failures that trip
// the circuit to OPEN once the consecutive-failure threshold is crossed — the
// state-machine transition (closed -> open) driven end-to-end through the
// wrappers, not by poking the breaker directly.
func TestWithBreaker_FailurePath_OpensCircuit(t *testing.T) {
	resetBillingBreaker(t)

	// Dead URL: bind a port then close it so every HTTP fallback is refused fast.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadURL := "http://" + ln.Addr().String()
	_ = ln.Close()
	prev := IdentityServiceURL
	IdentityServiceURL = deadURL
	t.Cleanup(func() { IdentityServiceURL = prev })

	ctx := context.Background()

	// First failure via SettleWithBreaker, then Release, then PreAuthorize — a mix
	// of all three wrappers' failure branches. threshold=3 consecutive failures
	// opens the circuit.
	if _, err := SettleWithBreaker(ctx, 1, 1.0); err == nil {
		t.Error("SettleWithBreaker must error against a dead endpoint")
	}
	if BillingBreakerIsOpen() {
		t.Error("one failure must not open the breaker (threshold is 3)")
	}
	if err := ReleaseWithBreaker(ctx, 1); err == nil {
		t.Error("ReleaseWithBreaker must error against a dead endpoint")
	}
	if _, err := PreAuthorizeWithBreaker(ctx, 1, 1.0, "p", "r", "d", 60); err == nil {
		t.Error("PreAuthorizeWithBreaker must error against a dead endpoint")
	}
	if !BillingBreakerIsOpen() {
		t.Fatal("breaker must be OPEN after threshold consecutive wrapper failures")
	}

	// With the circuit now OPEN, the next wrapper call fails fast without ever
	// reaching the network (this is the fail-open protection).
	if _, err := PreAuthorizeWithBreaker(ctx, 1, 1.0, "p", "r", "d", 60); err == nil {
		t.Error("open circuit must fast-fail the next wrapper call")
	}
}
