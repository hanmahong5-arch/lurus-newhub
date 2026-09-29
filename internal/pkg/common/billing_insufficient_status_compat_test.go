package common

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// billing_insufficient_status_compat_test.go — the platform answers
// insufficient_balance with 400, 402 or 409 depending on path and version,
// converging on 402. The body `error` code is the contract; the status is not.
// These tests pin: code=insufficient_balance under any of the three statuses
// is the balance sentinel, for both PreAuthorize and DebitWallet; any other
// code is not.

func serveStatusBody(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prev })
}

var insufficientStatuses = []int{http.StatusBadRequest, http.StatusPaymentRequired, http.StatusConflict}

func TestPreAuthorize_InsufficientBalance_AnyCompatStatus(t *testing.T) {
	for _, status := range insufficientStatuses {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			serveStatusBody(t, status, `{"error":"insufficient_balance","message":"x"}`)
			_, err := PreAuthorize(context.Background(), 42, 2.5, "prod", "ref", "desc", 60)
			if !errors.Is(err, ErrInsufficientBalance) {
				t.Fatalf("status %d + insufficient_balance: want ErrInsufficientBalance, got %v", status, err)
			}
		})
	}
}

func TestPreAuthorize_400InvalidRequest_IsNotInsufficient(t *testing.T) {
	serveStatusBody(t, http.StatusBadRequest, `{"error":"invalid_request"}`)
	_, err := PreAuthorize(context.Background(), 42, 2.5, "prod", "ref", "desc", 60)
	if errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("400 invalid_request must not be the balance sentinel: %v", err)
	}
	var rejected *PlatformRejectedError
	if !errors.As(err, &rejected) || rejected.Reason != "invalid_request" || rejected.Status != http.StatusBadRequest {
		t.Fatalf("want *PlatformRejectedError{400, invalid_request}, got %T %v", err, err)
	}
}

func TestPreAuthorize_402Or409OtherCode_KeepsUnavailableBehaviour(t *testing.T) {
	for _, status := range []int{http.StatusPaymentRequired, http.StatusConflict} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			serveStatusBody(t, status, `{"error":"something_else"}`)
			_, err := PreAuthorize(context.Background(), 42, 2.5, "prod", "ref", "desc", 60)
			if errors.Is(err, ErrInsufficientBalance) {
				t.Fatalf("status %d + something_else must not be the balance sentinel", status)
			}
			var rejected *PlatformRejectedError
			if errors.As(err, &rejected) {
				t.Fatalf("status %d: behaviour changed — got PlatformRejectedError, want plain unavailable error", status)
			}
			if err == nil || err.Error() != "billing service unavailable" {
				t.Fatalf("status %d: want 'billing service unavailable', got %v", status, err)
			}
		})
	}
}

func TestDebitWallet_InsufficientBalance_AnyCompatStatus(t *testing.T) {
	for _, status := range insufficientStatuses {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			serveStatusBody(t, status, `{"error":"insufficient_balance"}`)
			_, err := DebitWallet(context.Background(), 42, 1.0, "debit", "d", "prod", "idem")
			if !errors.Is(err, ErrInsufficientBalance) {
				t.Fatalf("status %d + insufficient_balance: want ErrInsufficientBalance, got %v", status, err)
			}
		})
	}
}

func TestDebitWallet_NonBalanceCodes_AreNotInsufficient(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{
		{http.StatusBadRequest, `{"error":"invalid_request"}`},
		{http.StatusPaymentRequired, `{"error":"something_else"}`},
		{http.StatusConflict, `{"error":"something_else"}`},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.status), func(t *testing.T) {
			serveStatusBody(t, c.status, c.body)
			_, err := DebitWallet(context.Background(), 42, 1.0, "debit", "d", "prod", "idem")
			if errors.Is(err, ErrInsufficientBalance) {
				t.Fatalf("%d %s must not be the balance sentinel", c.status, c.body)
			}
			want := fmt.Sprintf("debit failed: status %d", c.status)
			if err == nil || err.Error() != want {
				t.Fatalf("want %q, got %v", want, err)
			}
		})
	}
}
