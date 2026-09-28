package common

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// The platform answers a settle/release it will never accept with 404 (no
// such hold) or 409 (already settled / released / settled for another
// amount). Both are verdicts, like the 400 already typed: the breaker must
// read them as the platform answering, and the outbox must be able to see
// the status to stop retrying.

func TestSettlePreAuth_409_IsPlatformRejected(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "pre-auth is already settled or released"})
	})
	_, err := SettlePreAuth(context.Background(), 55, 1.0)
	var rejected *PlatformRejectedError
	if !errors.As(err, &rejected) || rejected.Status != http.StatusConflict || rejected.Op != "settle" {
		t.Fatalf("err = %v (%T), want *PlatformRejectedError{Op: settle, Status: 409}", err, err)
	}
	if !platformAnswered(err) {
		t.Error("platformAnswered = false for a 409 verdict")
	}
}

func TestSettlePreAuth_404_IsPlatformRejected(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "Pre-auth not found"})
	})
	_, err := SettlePreAuth(context.Background(), 55, 1.0)
	var rejected *PlatformRejectedError
	if !errors.As(err, &rejected) || rejected.Status != http.StatusNotFound {
		t.Fatalf("err = %v (%T), want *PlatformRejectedError{Status: 404}", err, err)
	}
}

func TestReleasePreAuth_409_IsPlatformRejected(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "pre-auth is already settled or released"})
	})
	err := ReleasePreAuth(context.Background(), 55)
	var rejected *PlatformRejectedError
	if !errors.As(err, &rejected) || rejected.Status != http.StatusConflict || rejected.Op != "release" {
		t.Fatalf("err = %v (%T), want *PlatformRejectedError{Op: release, Status: 409}", err, err)
	}
	if !platformAnswered(err) {
		t.Error("platformAnswered = false for a 409 verdict")
	}
}

func TestReleasePreAuth_502_StaysUntyped(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	err := ReleasePreAuth(context.Background(), 55)
	var rejected *PlatformRejectedError
	if err == nil || errors.As(err, &rejected) {
		t.Fatalf("a 502 is not a platform verdict, got %v", err)
	}
}
