package handler

// v2_billing_checkout_error_test.go — pins CreateBillingCheckout's error
// mapping (N4): a platform-rejected checkout (400 — bad amount / payment
// method not configured) must surface as 400 with the platform's real
// message, not the same generic 503 used for an actual platform outage. Before
// this, common.CreateCheckout discarded the platform's HTTP status beyond a
// substring match on "insufficient", so every other 400 (e.g. "method not
// available") was indistinguishable on the wire from a dead platform.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestCreateBillingCheckout_MethodNotAvailable_Returns400(t *testing.T) {
	const accountID = int64(42424)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "method_not_available",
			"message": "payment method not available: provider not configured",
		})
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })

	c, w := r2chanNewCtx(http.MethodPost, "/api/v2/user/billing/checkout", map[string]any{
		"amount_cny":     100.0,
		"payment_method": "wechat",
	})
	c.Set("identity_account_id", accountID)

	CreateBillingCheckout(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["success"] != false {
		t.Errorf("expected success=false, got %v", resp["success"])
	}
	msg, _ := resp["message"].(string)
	if msg != "payment method not available: provider not configured" {
		t.Errorf("expected the platform's real message to pass through, got %q", msg)
	}
	if msg == "checkout service unavailable" {
		t.Error("a platform 400 must not be collapsed into the generic 503 message")
	}
}

func TestCreateBillingCheckout_PlatformDown_Returns503(t *testing.T) {
	const accountID = int64(42425)

	// A closed server address simulates a network-level failure (platform
	// unreachable), not a platform-issued rejection — this must stay 503.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = deadURL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })

	c, w := r2chanNewCtx(http.MethodPost, "/api/v2/user/billing/checkout", map[string]any{
		"amount_cny":     100.0,
		"payment_method": "alipay",
	})
	c.Set("identity_account_id", accountID)

	CreateBillingCheckout(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if msg, _ := resp["message"].(string); msg != "checkout service unavailable" {
		t.Errorf("expected generic unavailable message for a network failure, got %q", msg)
	}
}

func TestCreateBillingCheckout_PlatformDown_5xx_Returns503(t *testing.T) {
	const accountID = int64(42426)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })

	c, w := r2chanNewCtx(http.MethodPost, "/api/v2/user/billing/checkout", map[string]any{
		"amount_cny":     100.0,
		"payment_method": "alipay",
	})
	c.Set("identity_account_id", accountID)

	CreateBillingCheckout(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for a platform 5xx, got code=%d body=%s", w.Code, w.Body.String())
	}
}

// TestCreateBillingCheckout_NoPlatformAccount pins the pre-existing 503 guard
// (unrelated to the error-mapping change) so this file also documents the
// "account not linked" branch alongside the new status mapping.
func TestCreateBillingCheckout_NoPlatformAccount(t *testing.T) {
	c, w := r2chanNewCtx(http.MethodPost, "/api/v2/user/billing/checkout", map[string]any{
		"amount_cny":     100.0,
		"payment_method": "alipay",
	})
	// identity_account_id intentionally not set.

	CreateBillingCheckout(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got code=%d body=%s", w.Code, w.Body.String())
	}
}
