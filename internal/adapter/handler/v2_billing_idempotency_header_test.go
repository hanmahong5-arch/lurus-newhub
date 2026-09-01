package handler

// Pins that the outgoing platform checkout request carries the
// Idempotency-Key HEADER. The platform 400s any checkout without it, and
// newhub only ever sent the key as a body field — so checkout had NEVER
// completed end-to-end, a fact disguised by the old blanket-503 error
// mapping until the honest 400 passthrough exposed the platform's real
// message live (2026-09-01: "This endpoint requires an Idempotency-Key
// header").

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestCreateBillingCheckout_SendsIdempotencyKeyHeader(t *testing.T) {
	const accountID = int64(42425)

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"order_id": "ord_test",
			"pay_url":  "https://pay.example/x",
		})
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })

	c, w := r2chanNewCtx(http.MethodPost, "/api/v2/user/billing/checkout", map[string]any{
		"amount_cny":     50.0,
		"payment_method": "alipay",
	})
	c.Set("identity_account_id", accountID)

	CreateBillingCheckout(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got code=%d body=%s", w.Code, w.Body.String())
	}
	if gotHeader == "" {
		t.Fatal("platform request carried no Idempotency-Key header — the platform rejects such checkouts with 400")
	}
}
