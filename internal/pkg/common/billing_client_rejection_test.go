package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// billing_client_rejection_test.go — cycle-14 L9 defect (2).
//
// PreAuthorize used to answer EVERY platform 400 with the insufficient-balance
// sentinel, including the arm that had already parsed and logged a different
// reason. The customer was therefore told they were out of money whenever the
// platform tightened a validation rule — a statement they cannot act on and
// their operator cannot diagnose. These tests pin the split: the balance
// sentinel for one reason, a *PlatformRejectedError carrying the platform's
// own words for everything else.
//
// WHAT THIS FILE DELIBERATELY DOES NOT TEST. The split feeds a second consumer,
// degradeAdmissible, and the brief asked for the non-balance class to be let
// through it. It is not, and the reversal is deliberate: the degrade path
// exists for the platform being UNREACHABLE, and a 400 is the platform
// answering. Admitting it would serve unsecured relays to accounts the
// platform refused under any spelling other than the one literal below
// (wallet_frozen, account_suspended, credit_limit_exceeded …). That policy and
// its mutation-verified table live in billing_degrade_test.go
// (TestDegradeAdmissible_PlatformAnsweredNeverDegrades). Nothing here should
// be read as evidence about it — an earlier draft of this file did make that
// claim, and the assertion carrying it passed identically under both policies.

// preAuthAgainst400 serves one 400 with the given JSON body and returns the
// error PreAuthorize produced.
func preAuthAgainst400(t *testing.T, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prev })

	_, err := PreAuthorize(context.Background(), 42, 2.5, "prod", "ref-l9", "desc", 60)
	if err == nil {
		t.Fatal("a 400 from the platform must produce an error")
	}
	return err
}

// TestPreAuthorize_NonBalance400_IsNotRelabelledAsOutOfMoney is the oracle for
// defect (2), first half: a 400 the platform explains with anything other than
// insufficient_balance must not come back wearing the balance label.
func TestPreAuthorize_NonBalance400_IsNotRelabelledAsOutOfMoney(t *testing.T) {
	err := preAuthAgainst400(t, `{"error":"idempotency_key_required"}`)

	if err.Error() == "insufficient_balance" {
		t.Fatalf("platform reason idempotency_key_required was relabelled as %q — "+
			"the customer is told they are out of money for a platform validation change", err)
	}
	if errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("non-balance rejection matches the balance sentinel by identity: %v", err)
	}

	// The platform's own words have to survive the trip, or the operator
	// reading the 402 still cannot tell what the platform objected to.
	var rejected *PlatformRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("want a *PlatformRejectedError, got %T (%v)", err, err)
	}
	if rejected.Reason != "idempotency_key_required" {
		t.Errorf("Reason = %q, want idempotency_key_required", rejected.Reason)
	}
	if rejected.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", rejected.Status)
	}
	if rejected.Op != "pre-authorize" {
		t.Errorf("Op = %q, want pre-authorize", rejected.Op)
	}
}

// TestPreAuthorize_Unparseable400_StillNotBalance pins the arm that has no
// reason to report: a 400 with a body we cannot parse is still not a
// statement about the customer's balance.
func TestPreAuthorize_Unparseable400_StillNotBalance(t *testing.T) {
	err := preAuthAgainst400(t, `<html>gateway says no</html>`)

	if errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("an unparseable 400 was labelled out-of-money: %v", err)
	}
	var rejected *PlatformRejectedError
	if !errors.As(err, &rejected) || rejected.Reason != "unknown" {
		t.Errorf("want a *PlatformRejectedError with Reason=unknown, got %v (%T)", err, err)
	}
}

// TestPreAuthorize_NonBalance400_IsCarriedAsThePlatformRejectedType is the
// SEAM oracle between this file and the degrade policy: it is not enough that
// the classification table refuses a *PlatformRejectedError — the real 400 has
// to arrive at that table wearing that type, through a real HTTP round trip.
// Assert on the type, and then on the verdict the shared policy gives it, so
// this breaks if either side drifts.
func TestPreAuthorize_NonBalance400_IsCarriedAsThePlatformRejectedType(t *testing.T) {
	err := preAuthAgainst400(t, `{"error":"idempotency_key_required"}`)

	if !platformAnswered(err) {
		t.Fatalf("a real 400 round trip produced %T (%v), which the degrade policy "+
			"does not recognise as the platform having answered — it would serve "+
			"this request unsecured", err, err)
	}
	// Pure policy (no Redis, no breaker mutation): breaker OPEN, fresh cache,
	// 100 LB against a 1 LB estimate — every other guard holds, so the verdict
	// turns entirely on how this error is classified.
	if degradeAdmissible(true, true, 100, 1, err) {
		t.Fatalf("a platform 400 (%v) was admitted for an unsecured relay; the "+
			"degrade path is for the platform being unreachable, not for the "+
			"platform refusing", err)
	}
}

// TestPreAuthorize_RealInsufficientBalance_StillFailsClosed is the
// do-not-regress half: a genuine out-of-money rejection must keep its exact
// wire contract AND must still be refused by the degrade path. Widening the
// non-balance arm must not open a door for free spend.
func TestPreAuthorize_RealInsufficientBalance_StillFailsClosed(t *testing.T) {
	err := preAuthAgainst400(t, `{"error":"insufficient_balance"}`)

	if err.Error() != "insufficient_balance" {
		t.Errorf("real out-of-money rejection changed shape: %q", err.Error())
	}
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Errorf("real out-of-money rejection is not the sentinel: %v", err)
	}
	if degradeAdmissible(true, true, 100, 1, err) {
		t.Fatal("a genuine insufficient_balance must never degrade — that hands out free spend")
	}
	// NB: the wrapped/flattened/renamed-carrier variants used to be asserted
	// here with a comment claiming they distinguished identity matching from
	// the old substring match. They did not — fmt.Errorf("...%w", sentinel)
	// flattens to a message that still CONTAINS the literal, so the old match
	// caught it too and the assertion passed under both. The variants that
	// actually separate the two policies are in
	// TestDegradeAdmissible_PlatformAnsweredNeverDegrades, each one verified
	// to fail when its own arm of platformAnswered is deleted.
}

// TestPreAuthorize_Non400Statuses_Unchanged pins that only the 400 arm moved:
// a 500 still reads as the service being unavailable.
func TestPreAuthorize_Non400Statuses_Unchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prev })

	_, err := PreAuthorize(context.Background(), 42, 2.5, "prod", "ref-l9", "desc", 60)
	if err == nil || err.Error() != "billing service unavailable" {
		t.Errorf("500 should stay 'billing service unavailable', got %v", err)
	}
}
