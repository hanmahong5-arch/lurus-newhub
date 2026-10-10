package handler

// v2_routing_policy_test.go - tenant-admin decision-routing policy API
// (migration 054): RBAC, tenant confinement, all-or-nothing validation,
// defaults, audit and request-path cache invalidation.
//
// Test names referenced by router/v2_completeness_test.go's swept map:
//   TestRoutingPolicyV2_CrossTenantIs404

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/routingdecision"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type rpFixture struct {
	*V2TestContext
	engine *gin.Engine
	audit  *tenantReassignAuditCapture
}

func setupRoutingPolicy(t *testing.T) *rpFixture {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	if err := ctx.DB.AutoMigrate(&entity.RoutingPolicy{}); err != nil {
		t.Fatal(err)
	}
	// Routable models: one tenant-owned channel serving model-a and model-b.
	ch := SeedV2Channel(t, ctx, "routable")
	for _, m := range []string{"model-a", "model-b"} {
		if err := ctx.DB.Create(&repo.Ability{Group: "default", Model: m, ChannelId: ch.Id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	auth := func(c *gin.Context) {
		tenantID := c.GetHeader("X-Test-Tenant-ID")
		if tenantID == "" {
			tenantID = ctx.TenantID
		}
		uid, _ := strconv.Atoi(c.GetHeader("X-Test-User-ID"))
		var roles []string
		if h := c.GetHeader("X-Test-Roles"); h != "" {
			roles = strings.Split(h, ",")
		}
		c.Set("tenant_context", &middleware.TenantContext{TenantID: tenantID, UserID: uid, Roles: roles})
		c.Set("tenant_id", tenantID)
		c.Set("user_id", uid)
		c.Set("id", uid)
		if u, _ := repo.GetUserById(uid, false); u != nil {
			c.Set("role", u.Role)
		}
		c.Next()
	}
	e := gin.New()
	g := e.Group("/api/v2/:tenant_slug/routing-policies")
	g.Use(auth)
	g.GET("/:model", GetRoutingPolicyV2)
	g.PUT("/:model", PutRoutingPolicyV2)
	g.DELETE("/:model", DeleteRoutingPolicyV2)

	f := &rpFixture{V2TestContext: ctx, engine: e, audit: captureChannelAudit(t)}
	routingdecision.Invalidate()
	t.Cleanup(func() { routingdecision.Invalidate(); ctx.Cleanup() })
	return f
}

func (f *rpFixture) do(user *repo.User, method, model string, body interface{}, tenant string, roles ...string) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/api/v2/"+f.TenantID+"/routing-policies/"+model, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User-ID", strconv.Itoa(user.Id))
	if tenant != "" {
		req.Header.Set("X-Test-Tenant-ID", tenant)
	}
	if len(roles) > 0 {
		req.Header.Set("X-Test-Roles", strings.Join(roles, ","))
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func policyBody() map[string]interface{} {
	return map[string]interface{}{
		"enabled":           true,
		"evaluator_model":   "evaluator-a",
		"instructions":      "route by difficulty",
		"min_confidence":    0.7,
		"default_candidate": "cheap",
		"candidates": []map[string]string{
			{"id": "cheap", "model": "model-a", "criteria": "simple questions"},
			{"id": "strong", "model": "model-b", "criteria": "hard problems"},
		},
	}
}

func (f *rpFixture) rowCount() int64 {
	var n int64
	f.DB.Model(&entity.RoutingPolicy{}).Count(&n)
	return n
}

func TestRoutingPolicyV2_RBAC(t *testing.T) {
	f := setupRoutingPolicy(t)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		var body interface{}
		if method == http.MethodPut {
			body = policyBody()
		}
		if w := f.do(f.NormalUser, method, "smart", body, ""); w.Code != http.StatusForbidden {
			t.Errorf("member %s = %d, want 403: %s", method, w.Code, w.Body)
		}
	}
	if f.rowCount() != 0 {
		t.Fatal("a member's PUT wrote a row")
	}
	w := f.do(f.AdminUser, http.MethodPut, "smart", policyBody(), "", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("tenant admin PUT = %d: %s", w.Code, w.Body)
	}
	if w := f.do(f.NormalUser, http.MethodGet, "smart", nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("member read of an existing policy = %d, want 403", w.Code)
	}
}

func TestRoutingPolicyV2_CRUDRoundTripAndRequestPathCache(t *testing.T) {
	f := setupRoutingPolicy(t)
	w := f.do(f.AdminUser, http.MethodPut, "smart", policyBody(), "", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body)
	}
	if gjson.Get(w.Body.String(), "data.candidates.#").Int() != 2 || gjson.Get(w.Body.String(), "data.min_confidence").Float() != 0.7 {
		t.Fatalf("view = %s", w.Body)
	}
	// The request path sees it immediately on this replica (index invalidated).
	if routingdecision.Lookup(f.TenantID, "smart") == nil {
		t.Fatal("enabled policy not visible to the request path after PUT")
	}

	g := f.do(f.AdminUser, http.MethodGet, "smart", nil, "", "admin")
	if g.Code != http.StatusOK || gjson.Get(g.Body.String(), "data.evaluator_model").String() != "evaluator-a" {
		t.Fatalf("GET = %d: %s", g.Code, g.Body)
	}

	// Replace in place: still one row, now disabled.
	b := policyBody()
	b["enabled"] = false
	if w := f.do(f.AdminUser, http.MethodPut, "smart", b, "", "admin"); w.Code != http.StatusOK {
		t.Fatalf("second PUT = %d: %s", w.Code, w.Body)
	}
	if f.rowCount() != 1 {
		t.Fatalf("rows = %d, want 1", f.rowCount())
	}
	if routingdecision.Lookup(f.TenantID, "smart") != nil {
		t.Fatal("disabled policy still served to the request path")
	}

	if w := f.do(f.AdminUser, http.MethodDelete, "smart", nil, "", "admin"); w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", w.Code, w.Body)
	}
	if w := f.do(f.AdminUser, http.MethodGet, "smart", nil, "", "admin"); w.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE = %d", w.Code)
	}
	if w := f.do(f.AdminUser, http.MethodDelete, "smart", nil, "", "admin"); w.Code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d, want 404", w.Code)
	}
}

func TestRoutingPolicyV2_CrossTenantIs404(t *testing.T) {
	f := setupRoutingPolicy(t)
	foreign := &entity.RoutingPolicy{
		TenantID: "other-tenant", PublicModel: "smart", Strategy: entity.RoutingStrategyDecision, Enabled: true,
		EvaluatorModel: "evaluator-a", MinConfidence: 0.9, DefaultCandidate: "x",
		Candidates: entity.RoutingCandidates{{ID: "x", Model: "model-a", Criteria: "c"}},
	}
	if err := repo.UpsertRoutingPolicy(foreign); err != nil {
		t.Fatal(err)
	}
	if w := f.do(f.AdminUser, http.MethodGet, "smart", nil, "", "admin"); w.Code != http.StatusNotFound {
		t.Fatalf("another tenant's policy readable: %d %s", w.Code, w.Body)
	}
	if w := f.do(f.AdminUser, http.MethodDelete, "smart", nil, "", "admin"); w.Code != http.StatusNotFound {
		t.Fatalf("another tenant's policy deletable: %d", w.Code)
	}
	// A PUT creates the caller's OWN policy; the foreign row is untouched.
	if w := f.do(f.AdminUser, http.MethodPut, "smart", policyBody(), "", "admin"); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body)
	}
	got, err := repo.GetRoutingPolicy("other-tenant", "smart")
	if err != nil || got.MinConfidence != 0.9 || got.EvaluatorModel != "evaluator-a" || len(got.Candidates) != 1 {
		t.Fatalf("foreign policy modified: %+v %v", got, err)
	}
	// A body-supplied tenant is ignored.
	b := policyBody()
	b["tenant_id"] = "other-tenant"
	f.do(f.AdminUser, http.MethodPut, "other-model", b, "", "admin")
	if p, _ := repo.GetRoutingPolicy("other-tenant", "other-model"); p != nil {
		t.Fatal("the body chose the tenant")
	}
}

func TestRoutingPolicyV2_ValidationIsAllOrNothing(t *testing.T) {
	f := setupRoutingPolicy(t)
	many := func(n int) []map[string]string {
		out := make([]map[string]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, map[string]string{"id": "c" + strconv.Itoa(i), "model": "model-a", "criteria": "x"})
		}
		return out
	}
	cases := []struct {
		name string
		mut  func(map[string]interface{})
		code string
	}{
		{"no candidates", func(b map[string]interface{}) { b["candidates"] = []map[string]string{} }, "INVALID_CANDIDATES"},
		{"33 candidates", func(b map[string]interface{}) { b["candidates"] = many(33); b["default_candidate"] = "c0" }, "INVALID_CANDIDATES"},
		{"criteria over 2048 bytes", func(b map[string]interface{}) {
			b["candidates"] = []map[string]string{{"id": "cheap", "model": "model-a", "criteria": strings.Repeat("a", 2049)}}
		}, "CRITERIA_TOO_LONG"},
		{"instructions over 4096 bytes", func(b map[string]interface{}) { b["instructions"] = strings.Repeat("a", 4097) }, "INSTRUCTIONS_TOO_LONG"},
		{"min_confidence above 1", func(b map[string]interface{}) { b["min_confidence"] = 1.5 }, "INVALID_MIN_CONFIDENCE"},
		{"min_confidence negative", func(b map[string]interface{}) { b["min_confidence"] = -0.1 }, "INVALID_MIN_CONFIDENCE"},
		{"candidate model not routable for the tenant", func(b map[string]interface{}) {
			b["candidates"] = []map[string]string{{"id": "cheap", "model": "model-nobody-serves", "criteria": "x"}}
		}, "MODEL_NOT_ROUTABLE"},
		{"default is not a candidate id", func(b map[string]interface{}) { b["default_candidate"] = "ghost" }, "INVALID_DEFAULT_CANDIDATE"},
		{"enabled without evaluator", func(b map[string]interface{}) { b["evaluator_model"] = "" }, "EVALUATOR_REQUIRED"},
	}
	for _, tc := range cases {
		b := policyBody()
		tc.mut(b)
		w := f.do(f.AdminUser, http.MethodPut, "smart", b, "", "admin")
		if w.Code != http.StatusBadRequest || dpErrCode(w) != tc.code {
			t.Errorf("%s: %d %s, want 400 %s", tc.name, w.Code, w.Body, tc.code)
		}
	}
	if f.rowCount() != 0 {
		t.Fatalf("a rejected policy left %d row(s)", f.rowCount())
	}
	if w := f.do(f.AdminUser, http.MethodPut, "smart", map[string]interface{}{"candidates": "not-a-list"}, "", "admin"); w.Code != http.StatusBadRequest {
		t.Errorf("malformed body = %d", w.Code)
	}
}

func TestRoutingPolicyV2_DefaultsAndExplicitZero(t *testing.T) {
	f := setupRoutingPolicy(t)
	b := policyBody()
	delete(b, "min_confidence")
	delete(b, "enabled")
	w := f.do(f.AdminUser, http.MethodPut, "smart", b, "", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body)
	}
	if gjson.Get(w.Body.String(), "data.min_confidence").Float() != 0.65 || gjson.Get(w.Body.String(), "data.enabled").Bool() {
		t.Fatalf("omitted fields must take the documented defaults (0.65 / disabled): %s", w.Body)
	}
	b["min_confidence"] = 0
	w = f.do(f.AdminUser, http.MethodPut, "smart", b, "", "admin")
	if w.Code != http.StatusOK || gjson.Get(w.Body.String(), "data.min_confidence").Float() != 0 {
		t.Fatalf("an explicit 0 must stay 0: %d %s", w.Code, w.Body)
	}
}

func TestRoutingPolicyV2_AuditsWritesWithoutProse(t *testing.T) {
	f := setupRoutingPolicy(t)
	b := policyBody()
	b["instructions"] = "internal playbook: escalate refunds"
	if w := f.do(f.AdminUser, http.MethodPut, "smart", b, "", "admin"); w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body)
	}
	if w := f.do(f.AdminUser, http.MethodDelete, "smart", nil, "", "admin"); w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", w.Code, w.Body)
	}
	var set, del *entity.AuditEvent
	for _, e := range f.audit.snapshot() {
		switch e.Action {
		case governance.ActionRoutingPolicySet:
			set = e
		case governance.ActionRoutingPolicyDeleted:
			del = e
		}
	}
	if set == nil || del == nil {
		t.Fatalf("missing audit events: set=%v delete=%v", set != nil, del != nil)
	}
	if strings.Contains(set.Details, "playbook") || strings.Contains(set.Details, "simple questions") {
		t.Errorf("free text copied into the audit chain: %s", set.Details)
	}
	if gjson.Get(set.Details, "public_model").String() != "smart" || gjson.Get(set.Details, "candidates").Int() != 2 || set.TenantID != f.TenantID {
		t.Errorf("audit details = %s tenant=%s", set.Details, set.TenantID)
	}
}
