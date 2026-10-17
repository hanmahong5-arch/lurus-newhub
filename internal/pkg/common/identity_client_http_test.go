package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newIdentityServer spins up an httptest server routing the platform internal
// endpoints the identity client calls, and points IdentityServiceURL at it for
// the duration of the test.
func newIdentityServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prev })
}

// ---------- Entitlements pure helpers ----------

func TestEntitlementsAccessors(t *testing.T) {
	e := Entitlements{"plan_code": "pro", "seats": "5", "beta": "true", "legacy": "false"}
	if e.GetString("plan_code", "free") != "pro" {
		t.Error("GetString existing")
	}
	if e.GetString("missing", "free") != "free" {
		t.Error("GetString default")
	}
	if e.GetInt("seats", 1) != 5 {
		t.Error("GetInt existing")
	}
	if e.GetInt("missing", 9) != 9 {
		t.Error("GetInt default")
	}
	if e.GetInt("plan_code", 3) != 3 {
		t.Error("GetInt non-numeric falls back")
	}
	if !e.GetBool("beta", false) {
		t.Error("GetBool true")
	}
	if e.GetBool("legacy", true) {
		t.Error("GetBool false")
	}
	if !e.GetBool("missing", true) {
		t.Error("GetBool default")
	}
}

func TestIdentityMappingUnmarshalJSON(t *testing.T) {
	// Canonical idp_subject wins.
	var m IdentityMapping
	if err := json.Unmarshal([]byte(`{"id":1,"idp_subject":"idp-abc","email":"a@b.com"}`), &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if m.ZitadelSub != "idp-abc" || m.ID != 1 {
		t.Errorf("canonical decode mismatch: %+v", m)
	}
	// Legacy zitadel_sub honored when idp_subject absent.
	var m2 IdentityMapping
	if err := json.Unmarshal([]byte(`{"id":2,"zitadel_sub":"legacy-xyz"}`), &m2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if m2.ZitadelSub != "legacy-xyz" {
		t.Errorf("legacy subject not honored: %+v", m2)
	}
}

// ---------- identity_client HTTP funcs: not-configured guards ----------

func TestIdentityClient_NotConfigured(t *testing.T) {
	prev := IdentityServiceURL
	IdentityServiceURL = ""
	t.Cleanup(func() { IdentityServiceURL = prev })
	ctx := context.Background()

	if m, err := GetAccountByZitadelSub(ctx, "s"); m != nil || err != nil {
		t.Error("GetAccountByZitadelSub unconfigured")
	}
	if m, err := UpsertAccount(ctx, "s", "e", "n", "a"); m != nil || err != nil {
		t.Error("UpsertAccount unconfigured")
	}
	// GetEntitlements returns a free plan default even unconfigured.
	if ent, err := GetEntitlements(ctx, 1, "prod"); err != nil || ent["plan_code"] != "free" {
		t.Error("GetEntitlements unconfigured should default to free")
	}
	if ov, err := GetAccountOverview(ctx, 1, ""); ov != nil || err != nil {
		t.Error("GetAccountOverview unconfigured")
	}
	if wb, err := GetWalletBalance(ctx, 1); wb != nil || err != nil {
		t.Error("GetWalletBalance unconfigured")
	}
	if m, err := GetAccountByEmail(ctx, "e"); m != nil || err != nil {
		t.Error("GetAccountByEmail unconfigured")
	}
	if pm, err := GetPaymentMethods(ctx); pm != nil || err != nil {
		t.Error("GetPaymentMethods unconfigured")
	}
	if bs, err := GetBillingSummary(ctx, 1); bs != nil || err != nil {
		t.Error("GetBillingSummary unconfigured")
	}
	if _, err := CreateCheckout(ctx, 1, 10, "alipay", "svc", "idem", "ret"); err == nil {
		t.Error("CreateCheckout unconfigured should error")
	}
	if _, err := GetCheckoutStatus(ctx, "ord"); err == nil {
		t.Error("GetCheckoutStatus unconfigured should error")
	}
	// ReportLLMUsage is fire-and-forget; just ensure no panic.
	ReportLLMUsage(ctx, 1, 1.0)
}

// ---------- identity_client HTTP funcs: happy paths against a fake server ----------

func TestGetAccountByZitadelSub_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/internal/v1/accounts/by-idp-sub/sub-123") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "idp_subject": "sub-123", "email": "u@x.com"})
	})
	m, err := GetAccountByZitadelSub(context.Background(), "sub-123")
	if err != nil || m == nil || m.ID != 7 || m.ZitadelSub != "sub-123" {
		t.Fatalf("unexpected result: %+v err=%v", m, err)
	}
}

func TestGetAccountByZitadelSub_NotFound(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	m, err := GetAccountByZitadelSub(context.Background(), "missing")
	if err != nil || m != nil {
		t.Errorf("404 should yield (nil,nil), got (%+v,%v)", m, err)
	}
}

func TestUpsertAccount_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/accounts/upsert") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["idp_subject"] != "sub-1" || body["email"] != "e@x.com" {
			t.Errorf("unexpected body %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 3, "idp_subject": "sub-1", "email": "e@x.com"})
	})
	m, err := UpsertAccount(context.Background(), "sub-1", "e@x.com", "Name", "url")
	if err != nil || m == nil || m.ID != 3 {
		t.Fatalf("unexpected: %+v err=%v", m, err)
	}
}

func TestGetEntitlements_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/accounts/42/entitlements/prod-x") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"plan_code": "pro", "seats": "10"})
	})
	ent, err := GetEntitlements(context.Background(), 42, "prod-x")
	if err != nil || ent.GetString("plan_code", "") != "pro" || ent.GetInt("seats", 0) != 10 {
		t.Fatalf("unexpected entitlements: %+v err=%v", ent, err)
	}
}

func TestGetAccountOverview_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("product_id") != "p1" {
			t.Errorf("missing product_id query: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"account": map[string]any{"id": 9, "display_name": "Nine"},
			"wallet":  map[string]any{"balance": 123.4, "frozen": 5.0},
		})
	})
	ov, err := GetAccountOverview(context.Background(), 9, "p1")
	if err != nil || ov == nil || ov.Account.ID != 9 || ov.Wallet.Balance != 123.4 {
		t.Fatalf("unexpected overview: %+v err=%v", ov, err)
	}
}

func TestGetWalletBalance_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"balance": 88.0, "frozen": 2.0})
	})
	wb, err := GetWalletBalance(context.Background(), 5)
	if err != nil || wb == nil || wb.Balance != 88.0 || wb.Frozen != 2.0 {
		t.Fatalf("unexpected: %+v err=%v", wb, err)
	}
}

func TestGetWalletBalance_ServerError(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	wb, err := GetWalletBalance(context.Background(), 5)
	if err != nil || wb != nil {
		t.Errorf("5xx should degrade to (nil,nil), got (%+v,%v)", wb, err)
	}
}

func TestGetAccountByEmail_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/accounts/by-email/u@x.com") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 11, "email": "u@x.com"})
	})
	m, err := GetAccountByEmail(context.Background(), "u@x.com")
	if err != nil || m == nil || m.ID != 11 {
		t.Fatalf("unexpected: %+v err=%v", m, err)
	}
}

// TestGetAccountByEmail_PathEscaped pins that reserved path characters in the
// email are escaped into ONE path segment instead of altering the route
// ("/" adding a segment, "?" / "#" truncating it into a query / fragment).
func TestGetAccountByEmail_PathEscaped(t *testing.T) {
	const email = "a/b?c#d@x.com"
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("email leaked into query: %q", r.URL.RawQuery)
		}
		if got, want := r.URL.EscapedPath(), "/internal/v1/accounts/by-email/a%2Fb%3Fc%23d@x.com"; got != want {
			t.Errorf("escaped path = %q, want %q", got, want)
		}
		if got := r.URL.Path; got != "/internal/v1/accounts/by-email/"+email {
			t.Errorf("decoded path = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 12, "email": email})
	})
	m, err := GetAccountByEmail(context.Background(), email)
	if err != nil || m == nil || m.ID != 12 {
		t.Fatalf("unexpected: %+v err=%v", m, err)
	}
}

// platformAccountByIDBody mirrors what platform GET /internal/v1/accounts/by-id/:id
// actually returns: the full entity.Account, whose MarshalJSON adds the
// canonical idp_subject next to the deprecated zitadel_sub.
func platformAccountByIDBody(id int64, idpSubject, legacySub string) map[string]any {
	body := map[string]any{
		"id": id, "lurus_id": "LU0000021", "display_name": "Zed",
		"avatar_url": "https://a/x.png", "email": "z@x.com", "email_verified": true,
		"status": 1, "is_platform_admin": false, "aff_code": "aff",
	}
	if legacySub != "" {
		body["zitadel_sub"] = legacySub
	}
	if idpSubject != "" {
		body["idp_subject"] = idpSubject
	}
	return body
}

func TestGetAccountByAccountID_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		// Must be the by-id route: /accounts/:id/overview's AccountSummary has
		// no subject/email/status, which left the session-token auth inert.
		if r.URL.Path != "/internal/v1/accounts/by-id/21" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(platformAccountByIDBody(21, "idp-sub", "idp-sub"))
	})
	m, err := GetAccountByZitadelSub_ByAccountID(context.Background(), 21)
	if err != nil || m == nil {
		t.Fatalf("unexpected: %+v err=%v", m, err)
	}
	if m.ID != 21 || m.ZitadelSub != "idp-sub" || m.Email != "z@x.com" || m.Status != 1 ||
		m.LurusID != "LU0000021" || m.DisplayName != "Zed" {
		t.Fatalf("fields not mapped: %+v", m)
	}
}

func TestGetAccountByAccountID_LegacySubjectOnly(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(platformAccountByIDBody(21, "", "legacy-sub"))
	})
	m, err := GetAccountByZitadelSub_ByAccountID(context.Background(), 21)
	if err != nil || m == nil || m.ZitadelSub != "legacy-sub" {
		t.Fatalf("unexpected: %+v err=%v", m, err)
	}
}

func TestGetAccountByAccountID_NotFound(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	m, err := GetAccountByZitadelSub_ByAccountID(context.Background(), 404)
	if err != nil || m != nil {
		t.Fatalf("404 should degrade to (nil,nil), got (%+v,%v)", m, err)
	}
}

func TestGetPaymentMethods_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"payment_methods": []map[string]any{
				{"id": "alipay", "name": "Alipay", "provider": "alipay", "type": "qr"},
			},
		})
	})
	pm, err := GetPaymentMethods(context.Background())
	if err != nil || len(pm) != 1 || pm[0].ID != "alipay" {
		t.Fatalf("unexpected: %+v err=%v", pm, err)
	}
}

func TestGetBillingSummary_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"balance": 100.0, "available": 90.0, "frozen": 10.0})
	})
	bs, err := GetBillingSummary(context.Background(), 3)
	if err != nil || bs == nil || bs.Balance != 100.0 || bs.Available != 90.0 {
		t.Fatalf("unexpected: %+v err=%v", bs, err)
	}
}

func TestCreateCheckout_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/checkout/create") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"order_no": "ORD1", "pay_url": "http://pay", "status": "pending"})
	})
	res, err := CreateCheckout(context.Background(), 1, 50, "alipay", "lurus-api", "idem-1", "http://ret")
	if err != nil || res == nil || res.OrderNo != "ORD1" || res.PayURL != "http://pay" {
		t.Fatalf("unexpected: %+v err=%v", res, err)
	}
}

func TestCreateCheckout_Error(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "bad_amount"})
	})
	if _, err := CreateCheckout(context.Background(), 1, 0, "alipay", "svc", "i", "r"); err == nil ||
		!strings.Contains(err.Error(), "bad_amount") {
		t.Errorf("expected bad_amount error, got %v", err)
	}
}

func TestGetCheckoutStatus_HTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/checkout/ORD9/status") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"order_no": "ORD9", "status": "paid", "amount_cny": 20.0})
	})
	st, err := GetCheckoutStatus(context.Background(), "ORD9")
	if err != nil || st == nil || st.Status != "paid" || st.AmountCNY != 20.0 {
		t.Fatalf("unexpected: %+v err=%v", st, err)
	}
}

func TestReportLLMUsage_HTTP(t *testing.T) {
	hit := make(chan struct{}, 1)
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/usage/report") {
			hit <- struct{}{}
		}
		w.WriteHeader(http.StatusOK)
	})
	ReportLLMUsage(context.Background(), 1, 3.5)
	select {
	case <-hit:
	default:
		t.Error("usage report endpoint was not called")
	}
}
