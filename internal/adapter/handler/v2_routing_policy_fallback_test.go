package handler

// v2_routing_policy_fallback_test.go - the "fallback" strategy on the tenant
// routing-policy API: strategy selection, its validation rules, backward
// compatibility of a body without "strategy", and request-path visibility.

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app/routingdecision"

	"github.com/tidwall/gjson"
)

func fallbackBody() map[string]interface{} {
	return map[string]interface{}{
		"strategy": "fallback",
		"enabled":  true,
		"candidates": []map[string]string{
			{"id": "first", "model": "model-a"},
			{"id": "second", "model": "model-b", "criteria": "cheaper"},
		},
	}
}

func TestRoutingPolicyV2_FallbackRoundTrip(t *testing.T) {
	f := setupRoutingPolicy(t)
	w := f.do(f.AdminUser, http.MethodPut, "smart", fallbackBody(), "", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT fallback = %d: %s", w.Code, w.Body)
	}
	b := w.Body.String()
	if gjson.Get(b, "data.strategy").String() != "fallback" || gjson.Get(b, "data.candidates.#").Int() != 2 {
		t.Fatalf("view = %s", b)
	}
	// Candidate order is the policy: it must survive storage.
	if gjson.Get(b, "data.candidates.0.model").String() != "model-a" || gjson.Get(b, "data.candidates.1.model").String() != "model-b" {
		t.Fatalf("candidate order lost: %s", b)
	}
	if routingdecision.LookupFallback(f.TenantID, "smart") == nil {
		t.Fatal("enabled fallback policy not visible to the request path after PUT")
	}
	if routingdecision.Lookup(f.TenantID, "smart") != nil {
		t.Fatal("a fallback policy must not be served to the decision hook")
	}
	g := f.do(f.AdminUser, http.MethodGet, "smart", nil, "", "admin")
	if g.Code != http.StatusOK || gjson.Get(g.Body.String(), "data.strategy").String() != "fallback" {
		t.Fatalf("GET = %d: %s", g.Code, g.Body)
	}

	// Switching strategy replaces the one row in place.
	if w := f.do(f.AdminUser, http.MethodPut, "smart", policyBody(), "", "admin"); w.Code != http.StatusOK {
		t.Fatalf("PUT decision over fallback = %d: %s", w.Code, w.Body)
	}
	if f.rowCount() != 1 || routingdecision.LookupFallback(f.TenantID, "smart") != nil || routingdecision.Lookup(f.TenantID, "smart") == nil {
		t.Fatal("strategy switch did not replace the policy")
	}
}

func TestRoutingPolicyV2_OmittedStrategyStaysDecision(t *testing.T) {
	f := setupRoutingPolicy(t)
	w := f.do(f.AdminUser, http.MethodPut, "smart", policyBody(), "", "admin")
	if w.Code != http.StatusOK || gjson.Get(w.Body.String(), "data.strategy").String() != "decision" {
		t.Fatalf("a body without strategy must remain a decision policy: %d %s", w.Code, w.Body)
	}
}

func TestRoutingPolicyV2_FallbackIgnoresDecisionOnlyFields(t *testing.T) {
	f := setupRoutingPolicy(t)
	b := fallbackBody()
	b["evaluator_model"] = ""
	b["default_candidate"] = "ghost"
	b["min_confidence"] = 3
	b["instructions"] = "stored as given"
	w := f.do(f.AdminUser, http.MethodPut, "smart", b, "", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("decision-only fields must be accepted and ignored: %d %s", w.Code, w.Body)
	}
	if gjson.Get(w.Body.String(), "data.instructions").String() != "stored as given" || gjson.Get(w.Body.String(), "data.default_candidate").String() != "ghost" {
		t.Fatalf("ignored fields must still be stored as given: %s", w.Body)
	}
}

func TestRoutingPolicyV2_FallbackValidation(t *testing.T) {
	f := setupRoutingPolicy(t)
	nine := make([]map[string]string, 0, 9)
	for i := 0; i < 9; i++ {
		nine = append(nine, map[string]string{"id": "c" + strconv.Itoa(i), "model": "model-a"})
	}
	cases := []struct {
		name string
		mut  func(map[string]interface{})
		code string
	}{
		{"unknown strategy", func(b map[string]interface{}) { b["strategy"] = "cost" }, "INVALID_STRATEGY"},
		{"no candidates", func(b map[string]interface{}) { b["candidates"] = []map[string]string{} }, "INVALID_CANDIDATES"},
		{"nine candidates", func(b map[string]interface{}) { b["candidates"] = nine }, "INVALID_CANDIDATES"},
		{"candidate equals the public model", func(b map[string]interface{}) {
			b["candidates"] = []map[string]string{{"id": "a", "model": "smart"}}
		}, "INVALID_CANDIDATE_MODEL"},
		{"candidate not routable", func(b map[string]interface{}) {
			b["candidates"] = []map[string]string{{"id": "a", "model": "model-nobody-serves"}}
		}, "MODEL_NOT_ROUTABLE"},
		{"duplicate id", func(b map[string]interface{}) {
			b["candidates"] = []map[string]string{{"id": "a", "model": "model-a"}, {"id": "a", "model": "model-b"}}
		}, "DUPLICATE_CANDIDATE"},
	}
	for _, tc := range cases {
		b := fallbackBody()
		tc.mut(b)
		w := f.do(f.AdminUser, http.MethodPut, "smart", b, "", "admin")
		if w.Code != http.StatusBadRequest || dpErrCode(w) != tc.code {
			t.Errorf("%s: %d %s, want 400 %s", tc.name, w.Code, w.Body, tc.code)
		}
	}
	if f.rowCount() != 0 {
		t.Fatalf("a rejected policy left %d row(s)", f.rowCount())
	}
}
