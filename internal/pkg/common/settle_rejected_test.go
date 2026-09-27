package common

// settle_rejected_test.go — a 400 on settle is the platform answering
// ("invalid_request", an expired or already-settled hold), not the platform
// missing. SettlePreAuth used to flatten it into a bare "settle failed: …"
// error, so every outbox retry of a hold the platform had already decided on
// was fed to the breaker as an unreachable platform (recordBillingOutcome),
// and three such retries in a row opened the breaker for every tenant.
// PreAuthorize already returns *PlatformRejectedError for the same class of
// answer; this pins settle to the same shape.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestSettlePreAuth_400_IsPlatformRejectedAndCountsAsAnswered(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_request"})
	})
	_, err := SettlePreAuth(context.Background(), 55, 1.0)
	if err == nil {
		t.Fatal("expected an error from a 400 settle")
	}
	var rejected *PlatformRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v (%T), want *PlatformRejectedError", err, err)
	}
	if rejected.Op != "settle" || rejected.Status != http.StatusBadRequest || rejected.Reason != "invalid_request" {
		t.Errorf("rejected = %+v, want Op=settle Status=400 Reason=invalid_request", rejected)
	}
	if !platformAnswered(err) {
		t.Errorf("platformAnswered = false — the breaker would count a platform verdict as an outage")
	}
}

// Every other non-200 stays what it was: an untyped failure the outbox
// retries and the breaker counts.
func TestSettlePreAuth_Non400Failure_StaysUntyped(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "upstream_down"})
	})
	_, err := SettlePreAuth(context.Background(), 55, 1.0)
	if err == nil {
		t.Fatal("expected an error from a 502 settle")
	}
	var rejected *PlatformRejectedError
	if errors.As(err, &rejected) {
		t.Errorf("a 502 is not a platform verdict, got %+v", rejected)
	}
	if platformAnswered(err) {
		t.Errorf("platformAnswered = true for a 502")
	}
}
