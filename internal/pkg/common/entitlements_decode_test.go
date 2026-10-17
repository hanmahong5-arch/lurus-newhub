package common

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestEntitlementsUnmarshal_NonStringValues pins that a non-string JSON value
// is stringified rather than failing the whole decode. A decode failure makes
// GetEntitlements degrade to {"plan_code":"free"}, which silently drops every
// other key — e.g. a numeric "quota_remaining": 0 would fail the gate OPEN.
func TestEntitlementsUnmarshal_NonStringValues(t *testing.T) {
	var e Entitlements
	raw := `{"plan_code":"pro","quota_remaining":0,"daily_quota":1000,"ratio":1.5,
		"beta":true,"legacy":false,"gone":null,"tags":["a"],"meta":{"k":"v"}}`
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := e.GetInt("quota_remaining", -1); got != 0 {
		t.Errorf("quota_remaining = %d, want 0", got)
	}
	if got := e.GetInt("daily_quota", -1); got != 1000 {
		t.Errorf("daily_quota = %d, want 1000", got)
	}
	if got := e.GetString("plan_code", ""); got != "pro" {
		t.Errorf("plan_code = %q", got)
	}
	if got := e.GetString("ratio", ""); got != "1.5" {
		t.Errorf("ratio = %q, want 1.5", got)
	}
	if !e.GetBool("beta", false) || e.GetBool("legacy", true) {
		t.Errorf("bools not decoded: %+v", e)
	}
	if _, ok := e["gone"]; ok {
		t.Error("null value should be dropped")
	}
	if e["tags"] != `["a"]` || e["meta"] != `{"k":"v"}` {
		t.Errorf("composite values should keep raw JSON: %+v", e)
	}
}

func TestEntitlementsUnmarshal_StringMapUnchanged(t *testing.T) {
	var e Entitlements
	if err := json.Unmarshal([]byte(`{"plan_code":"free","quota_remaining":"0"}`), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.GetString("plan_code", "") != "free" || e.GetInt("quota_remaining", -1) != 0 {
		t.Errorf("string map decode changed: %+v", e)
	}
	if err := json.Unmarshal([]byte(`["not","an","object"]`), &e); err == nil {
		t.Error("non-object body must still be a decode error")
	}
}

func TestGetEntitlements_NumericValueNotDropped(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"plan_code":"pro","quota_remaining":0}`))
	})
	e, err := GetEntitlements(context.Background(), 1, "llm-api")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if e.GetString("plan_code", "") != "pro" || e.GetInt("quota_remaining", -1) != 0 {
		t.Fatalf("numeric value made the decode fall back to free: %+v", e)
	}
}
