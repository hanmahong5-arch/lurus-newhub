package common

// Tests for identity_calls.go: the budgeted HTTP identity/wallet wrappers.
//
// The oracle for the budget is total elapsed wall-clock time of one call, not
// "a deadline is attached": cycle 12 found one logical identity call costing
// 10s while the caller believed it had handed out a 5s deadline.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// billingDebitSampleCount reads the cumulative sample_count for one
// (product, op) series of billing_debit_amount_cny.
func billingDebitSampleCount(t *testing.T, product, op string) int {
	t.Helper()
	metric, ok := metrics.BillingDebitAmountCNY.WithLabelValues(product, op).(prometheus.Metric)
	if !ok {
		t.Fatalf("BillingDebitAmountCNY observer does not implement prometheus.Metric")
	}
	var m dto.Metric
	if err := metric.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	hist := m.GetHistogram()
	if hist == nil {
		t.Fatalf("metric is not a histogram")
	}
	return int(hist.GetSampleCount())
}

// TestIdentityCalls_UseHTTP verifies every budgeted wrapper reaches its HTTP
// endpoint and maps the response, and that a confirmed debit is observed on
// billing_debit_amount_cny exactly once.
func TestIdentityCalls_UseHTTP(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/wallet/pre-authorize"):
			_ = json.NewEncoder(w).Encode(map[string]any{"preauth_id": 11, "status": "held", "amount": 2.0})
		case strings.Contains(r.URL.Path, "/settle"):
			_ = json.NewEncoder(w).Encode(map[string]any{"preauth_id": 11, "status": "settled"})
		case strings.Contains(r.URL.Path, "/release"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/debit"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "balance_after": 7.0})
		case strings.Contains(r.URL.Path, "/credit"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/entitlements/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"plan_code": "pro"})
		case strings.Contains(r.URL.Path, "/overview"):
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{"id": 21}})
		case strings.Contains(r.URL.Path, "/upsert"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 22})
		case strings.Contains(r.URL.Path, "/by-idp-sub/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 23, "idp_subject": "s"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	ctx := context.Background()

	if m, err := GetAccountByZitadelSubGRPC(ctx, "s"); err != nil || m == nil || m.ID != 23 {
		t.Errorf("GetAccountByZitadelSubGRPC: %+v err=%v", m, err)
	}
	if m, err := UpsertAccountGRPC(ctx, "s", "e", "n", "a"); err != nil || m == nil || m.ID != 22 {
		t.Errorf("UpsertAccountGRPC: %+v err=%v", m, err)
	}
	if ent, err := GetEntitlementsGRPC(ctx, 1, "prod"); err != nil || ent.GetString("plan_code", "") != "pro" {
		t.Errorf("GetEntitlementsGRPC: %+v err=%v", ent, err)
	}
	if ov, err := GetAccountOverviewGRPC(ctx, 21, ""); err != nil || ov == nil || ov.Account.ID != 21 {
		t.Errorf("GetAccountOverviewGRPC: %+v err=%v", ov, err)
	}
	ReportLLMUsageGRPC(ctx, 1, 1.0) // fire-and-forget; must not panic

	if res, err := PreAuthorizeGRPC(ctx, 1, 2.0, "prod", "ref", "desc", 60); err != nil || res == nil || res.PreAuthID != 11 {
		t.Errorf("PreAuthorizeGRPC: %+v err=%v", res, err)
	}
	if res, err := SettlePreAuthGRPC(ctx, 11, 1.5); err != nil || res == nil || res.Status != "settled" {
		t.Errorf("SettlePreAuthGRPC: %+v err=%v", res, err)
	}
	if err := ReleasePreAuthGRPC(ctx, 11); err != nil {
		t.Errorf("ReleasePreAuthGRPC: %v", err)
	}
	debitBefore := billingDebitSampleCount(t, "prod", "debit")
	if res, err := DebitWalletGRPC(ctx, 1, 3.0, "spend", "d", "prod", "idem-d"); err != nil || res == nil || !res.Success {
		t.Errorf("DebitWalletGRPC: %+v err=%v", res, err)
	}
	if got := billingDebitSampleCount(t, "prod", "debit") - debitBefore; got != 1 {
		t.Errorf("billing_debit_amount_cny{product=prod,op=debit} delta = %d, want 1", got)
	}
	if err := CreditWalletGRPC(ctx, 1, 3.0, "refund", "d", "prod", "idem-c"); err != nil {
		t.Errorf("CreditWalletGRPC: %v", err)
	}
}

// hungIdentityHTTPServer points IdentityServiceURL at a server that never
// answers within the test's horizon. The handler also watches the request
// context so a client-side cancellation ends it immediately, which keeps
// srv.Close() from blocking on an in-flight request.
func hungIdentityHTTPServer(t *testing.T) {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	prevURL := IdentityServiceURL
	IdentityServiceURL = srv.URL
	// Registered in this order so cleanup runs LIFO: release the handler first,
	// then close the server.
	t.Cleanup(func() {
		IdentityServiceURL = prevURL
		srv.Close()
	})
	t.Cleanup(func() { close(release) })
}

// TestIdentityCall_TotalBudgetBoundsTheCall is the timing oracle: one identity
// call against a hung platform must respect IDENTITY_TIMEOUT_MS rather than the
// HTTP client's own 5s timeout.
func TestIdentityCall_TotalBudgetBoundsTheCall(t *testing.T) {
	t.Setenv("IDENTITY_TIMEOUT_MS", "300")
	hungIdentityHTTPServer(t)

	start := time.Now()
	_, _ = UpsertAccountGRPC(context.Background(), "sub-l7", "l7@example.invalid", "L7", "")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("UpsertAccountGRPC took %v with a 300ms total budget", elapsed)
	}

	start = time.Now()
	_, _ = DebitWalletGRPC(context.Background(), 1, 1.0, "spend", "d", "prod", "idem")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("DebitWalletGRPC took %v with a 300ms total budget", elapsed)
	}
}

// TestIdentityBudgetDefaults pins the number cycle 12 decided on for a
// deployment that does not set IDENTITY_TIMEOUT_MS.
func TestIdentityBudgetDefaults(t *testing.T) {
	if v, ok := os.LookupEnv("IDENTITY_TIMEOUT_MS"); ok {
		t.Skipf("IDENTITY_TIMEOUT_MS is set to %q in the ambient env; default case not applicable", v)
	}
	if got := identityTotalBudget(); got != 5*time.Second {
		t.Errorf("identityTotalBudget() = %v, want 5s", got)
	}
}

// TestWithIdentityBudgetNeverExtendsCallerDeadline: a caller that arrives with
// less time than the budget keeps its own, shorter deadline.
func TestWithIdentityBudgetNeverExtendsCallerDeadline(t *testing.T) {
	t.Setenv("IDENTITY_TIMEOUT_MS", "5000")
	parent, pcancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer pcancel()

	bctx, cancel := withIdentityBudget(parent)
	defer cancel()
	deadline, ok := bctx.Deadline()
	if !ok {
		t.Fatal("withIdentityBudget must attach a deadline")
	}
	if remaining := time.Until(deadline); remaining > 300*time.Millisecond {
		t.Errorf("budgeted deadline is %v away, but the caller only had 150ms", remaining)
	}
}

// TestAccountLookupFailureNeverWritesBreakerState guards the MONEY path: if the
// account/entitlement lookups recorded failures, an identity-side blip would
// fast-fail wallet operations, and on a deployment with unified billing off
// nothing would ever close the breaker again because no money leg is there to
// probe it.
func TestAccountLookupFailureNeverWritesBreakerState(t *testing.T) {
	t.Setenv("IDENTITY_TIMEOUT_MS", "300")
	newIdentityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	BillingBreakerSuccess()
	t.Cleanup(BillingBreakerSuccess)

	for i := 0; i < 5; i++ {
		_, _ = GetAccountByZitadelSubGRPC(context.Background(), "s")
		_, _ = GetEntitlementsGRPC(context.Background(), 1, "prod")
	}
	if BillingBreakerIsOpen() {
		t.Error("ten failed account/entitlement lookups opened the billing breaker: the lookup " +
			"path is writing breaker state, which would fast-fail the money path on an " +
			"identity-side blip")
	}
}
