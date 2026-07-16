package common

// usage_report_test.go — pins the Track A mirror-metering client contract
// (ReportUsageEvent → POST /internal/v1/usage/events) and the deploy-order
// safety of the local_ledger_advisory poll field: a platform that does not
// yet serve the field must NOT reset an operator-set flag.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReportUsageEvent_PostsContractShape(t *testing.T) {
	var got map[string]any
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	prevURL, prevKey := IdentityServiceURL, IdentityServiceInternalKey
	IdentityServiceURL = srv.URL
	IdentityServiceInternalKey = "test-key"
	t.Cleanup(func() { IdentityServiceURL = prevURL; IdentityServiceInternalKey = prevKey })

	err := ReportUsageEvent(context.Background(), 42, "llm-api", "llm_relay", 1234,
		"llm-relay:settle:99", map[string]any{"amount_cny": 0.5, "model": "gpt-4"})
	if err != nil {
		t.Fatalf("ReportUsageEvent: %v", err)
	}
	if gotPath != usageEventsPath {
		t.Errorf("path = %s, want %s", gotPath, usageEventsPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("auth = %q, want internal bearer", gotAuth)
	}
	if got["account_id"].(float64) != 42 || got["product_id"] != "llm-api" ||
		got["metric"] != "llm_relay" || got["quantity"].(float64) != 1234 ||
		got["idempotency_key"] != "llm-relay:settle:99" {
		t.Errorf("body drifted from the platform contract: %v", got)
	}
	if md, ok := got["metadata"].(map[string]any); !ok || md["model"] != "gpt-4" {
		t.Errorf("metadata not passed through: %v", got["metadata"])
	}
	if _, ok := got["occurred_at"]; !ok {
		t.Error("occurred_at missing")
	}
}

func TestReportUsageEvent_ErrorPaths(t *testing.T) {
	prevURL := IdentityServiceURL

	IdentityServiceURL = ""
	if err := ReportUsageEvent(context.Background(), 1, "p", "m", 1, "k", nil); err == nil {
		t.Error("unconfigured URL must error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prevURL })
	if err := ReportUsageEvent(context.Background(), 1, "p", "m", 1, "k", nil); err == nil {
		t.Error("non-200 must error")
	}
}

// TestFetchAndApplyBillingConfig_LocalLedgerAdvisory — present field applies;
// ABSENT field keeps the current value (deploy-order safety: an old platform
// must not force-reset an env-enabled advisory flag every 30s).
func TestFetchAndApplyBillingConfig_LocalLedgerAdvisory(t *testing.T) {
	origUnified := BillingUnifiedEnabled()
	origAdvisory := LocalLedgerAdvisory()
	t.Cleanup(func() {
		SetBillingUnifiedEnabled(origUnified)
		SetLocalLedgerAdvisory(origAdvisory)
	})

	// 1. Platform serves the field → poll applies it.
	srvOn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"unified_billing_enabled": true,
			"local_ledger_advisory":   true,
		})
	}))
	defer srvOn.Close()
	prev := IdentityServiceURL
	IdentityServiceURL = srvOn.URL
	t.Cleanup(func() { IdentityServiceURL = prev })

	SetLocalLedgerAdvisory(false)
	fetchAndApplyBillingConfig(context.Background())
	if !LocalLedgerAdvisory() {
		t.Error("poll should have applied local_ledger_advisory=true")
	}

	// 2. Platform predates the field → current value survives the poll.
	srvOld := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"unified_billing_enabled": true})
	}))
	defer srvOld.Close()
	IdentityServiceURL = srvOld.URL

	SetLocalLedgerAdvisory(true)
	fetchAndApplyBillingConfig(context.Background())
	if !LocalLedgerAdvisory() {
		t.Error("absent field must keep the current value, not reset to false")
	}

	// 3. And an explicit false still lands (operator dial-down works).
	srvOff := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"unified_billing_enabled": true,
			"local_ledger_advisory":   false,
		})
	}))
	defer srvOff.Close()
	IdentityServiceURL = srvOff.URL
	fetchAndApplyBillingConfig(context.Background())
	if LocalLedgerAdvisory() {
		t.Error("explicit false must dial the flag back down")
	}
}
