package handler

// α5: cross-tenant isolation matrix — endpoints not yet covered by a
// TestXxx_CrossTenantIsolation test.
//
// Covered:
//   - GET  /api/v2/:tenant_slug/pricing             (unknown slug → 404)
//   - POST /api/v2/:tenant_slug/pricing             (unknown slug → 404)
//   - GET  /api/v2/:tenant_slug/logs/cluster        (same-DB two-tenant filter)
//   - GET  /api/v2/:tenant_slug/playground/presets  (own only, other-tenant preset invisible)
//   - DELETE /api/v2/:tenant_slug/playground/presets/:id (other-tenant preset → 403)
//   - GET  /api/v2/:tenant_slug/billing/invoices    (same-DB two-tenant filter)
//   - GET  /api/v2/:tenant_slug/user/me             (own tenant_id in response)
//   - PUT  /api/v2/:tenant_slug/user/me             (only own record mutated)
//   - GET  /api/v2/:tenant_slug/sessions            (tenantB tokens/logs invisible)

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// ── Pricing ──────────────────────────────────────────────────────────────────

// TestGetPricingV2_CrossTenantIsolation: requesting pricing for an unknown
// tenant slug must return 404 — the handler resolves the slug against the DB
// before returning any pricing data.
func TestGetPricingV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Register the pricing route on the shared router.
	ctx.Router.GET("/api/v2/:tenant_slug/pricing",
		func(c *gin.Context) {
			tenantID := c.GetHeader("X-Test-Tenant-ID")
			if tenantID == "" {
				tenantID = ctx.TenantID
			}
			c.Set("tenant_context", &middleware.TenantContext{
				TenantID: tenantID,
				UserID:   ctx.NormalUser.Id,
			})
			c.Next()
		},
		GetPricingV2,
	)

	// Request pricing for a slug that does not exist in the DB.
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		"/api/v2/no-such-tenant-pricing/pricing", nil, nil)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown tenant slug, got %d — body: %s", w.Code, w.Body.String())
	}
}

// TestUpdatePricingV2_CrossTenantIsolation: pricing POST for an unknown
// tenant slug must be rejected with 404 before any write occurs.
func TestUpdatePricingV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ctx.Router.POST("/api/v2/:tenant_slug/pricing",
		func(c *gin.Context) {
			tenantID := c.GetHeader("X-Test-Tenant-ID")
			if tenantID == "" {
				tenantID = ctx.TenantID
			}
			c.Set("tenant_context", &middleware.TenantContext{
				TenantID: tenantID,
				UserID:   ctx.AdminUser.Id,
			})
			c.Next()
		},
		UpdatePricingV2,
	)

	body := map[string]interface{}{
		"updates": []interface{}{},
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost,
		"/api/v2/ghost-tenant-pricing/pricing", body, []string{"admin"})

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown tenant slug on pricing POST, got %d — body: %s", w.Code, w.Body.String())
	}
}

// ── Log Cluster ──────────────────────────────────────────────────────────────

// TestGetLogClusterV2_CrossTenantIsolation: the endpoint reports ERROR
// clusters (repo.LogTypeError rows only). This seeds error logs for two
// tenants in the same SQLite DB, plus a same-tenant LogTypeConsume row that
// must be excluded — a user authenticated as tenant-A must see only
// tenant-A's *error* logs: neither tenant-B's errors nor tenant-A's own
// successful (consume) calls.
func TestGetLogClusterV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenantA := ctx.TenantID
	tenantB := "cross-tenant-cluster-other"
	now := time.Now().Unix()

	// 3 error logs for tenant-A.
	for i := 0; i < 3; i++ {
		ctx.DB.Create(&repo.Log{
			UserId:    ctx.NormalUser.Id,
			TenantId:  tenantA,
			Type:      repo.LogTypeError,
			ModelName: "gpt-4o",
			Content:   "upstream timeout",
			CreatedAt: now - int64(i*60),
		})
	}

	// 1 successful (consume) log for tenant-A — must NOT appear: this is the
	// row that proves the `type = ?` predicate is really applied. If it were
	// dropped, this row would join the count below and total would be 4, not 3.
	ctx.DB.Create(&repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  tenantA,
		Type:      repo.LogTypeConsume,
		ModelName: "gpt-4o",
		Content:   "",
		CreatedAt: now,
	})

	// 5 error logs for tenant-B — must not appear in tenant-A's cluster view.
	for i := 0; i < 5; i++ {
		ctx.DB.Create(&repo.Log{
			UserId:    ctx.AdminUser.Id,
			TenantId:  tenantB,
			Type:      repo.LogTypeError,
			ModelName: "gpt-4o",
			Content:   "upstream timeout",
			CreatedAt: now - int64(i*60),
		})
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		"/api/v2/test-tenant/logs/cluster?bucket=hour&from=0&to=9999999999", nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})

	// Tenant-A has 3 error logs for gpt-4o (single hour bucket). Total count
	// across all returned items must be 3 — not 4 (own consume row leaked,
	// i.e. the type filter is missing) and not 8 (tenant-B leaked).
	var totalCount float64
	for _, it := range items {
		row := it.(map[string]interface{})
		totalCount += row["count"].(float64)
	}
	if totalCount != 3 {
		t.Errorf("expected total count=3 (tenant-A errors only), got %.0f — tenant-B logs and/or non-error logs may have leaked", totalCount)
	}
}

// ── Playground Presets ───────────────────────────────────────────────────────

// TestListPresetsV2_CrossTenantIsolation: user-A's presets must not appear
// when a different (tenant_id, user_id) pair's preset is in the same DB.
// Isolation is enforced by (tenant_id, user_id) filter in ListPlaygroundPresets.
func TestListPresetsV2_CrossTenantIsolation(t *testing.T) {
	ctx := setupPresetTestRouter(t)
	defer ctx.Cleanup()

	// Create a preset as normalUser (tenant A).
	createBody := map[string]interface{}{
		"name":   "tenant-a-private-preset",
		"prompt": "tenant-a secret",
	}
	cw := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost,
		"/api/v2/test-slug/playground/presets", createBody, nil)
	AssertV2Status(t, cw, http.StatusCreated)

	// Directly insert a preset for a different tenant/user into the DB.
	otherPreset := &repo.PlaygroundPreset{
		TenantID: "other-preset-tenant-999",
		UserID:   ctx.AdminUser.Id + 9999, // must not match normalUser
		Name:     "other-tenant-preset",
		Prompt:   "should not appear",
	}
	if err := repo.CreatePlaygroundPreset(otherPreset); err != nil {
		t.Fatalf("seed other-tenant preset: %v", err)
	}

	// List presets as normalUser — should only see their own preset.
	lw := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		"/api/v2/test-slug/playground/presets", nil, nil)
	AssertV2Status(t, lw, http.StatusOK)
	resp := AssertV2Success(t, lw)

	items, _ := resp["data"].([]interface{})
	if len(items) != 1 {
		t.Errorf("expected 1 preset (own tenant/user), got %d — cross-tenant preset may have leaked", len(items))
		return
	}

	first := items[0].(map[string]interface{})
	if name, _ := first["name"].(string); name != "tenant-a-private-preset" {
		t.Errorf("name = %q, want %q", name, "tenant-a-private-preset")
	}

	// Internal fields must not appear in the view.
	for _, forbidden := range []string{"tenant_id", "TenantID", "user_id", "UserID"} {
		if _, ok := first[forbidden]; ok {
			t.Errorf("forbidden field %q leaked through playgroundPresetView", forbidden)
		}
	}
}

// TestDeletePresetV2_CrossTenantIsolation: a user cannot delete a preset that
// belongs to a different (tenant_id, user_id) pair — must receive 403.
func TestDeletePresetV2_CrossTenantIsolation(t *testing.T) {
	ctx := setupPresetTestRouter(t)
	defer ctx.Cleanup()

	// Seed a preset for a different tenant directly in the DB.
	otherPreset := &repo.PlaygroundPreset{
		TenantID: "alien-preset-tenant-7777",
		UserID:   ctx.AdminUser.Id + 7777,
		Name:     "alien-preset",
		Prompt:   "cannot touch this",
	}
	if err := repo.CreatePlaygroundPreset(otherPreset); err != nil {
		t.Fatalf("seed alien preset: %v", err)
	}

	path := fmt.Sprintf("/api/v2/test-slug/playground/presets/%d", otherPreset.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodDelete, path, nil, nil)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 deleting another tenant's preset, got %d — body: %s", w.Code, w.Body.String())
	}
}

// ── Billing Invoices ─────────────────────────────────────────────────────────

// TestListInvoicesV2_CrossTenantIsolation seeds LogTypeConsume rows for two
// tenants in the same DB.  The authenticated user (tenant-A context) must
// receive only their own month buckets.
func TestListInvoicesV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenantA := ctx.TenantID
	tenantB := "invoice-cross-tenant-888"
	jan2026 := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC).Unix()

	// 2 consume logs for tenant-A (quota 1000 each).
	for i := 0; i < 2; i++ {
		ctx.DB.Create(&repo.Log{
			UserId:    ctx.NormalUser.Id,
			TenantId:  tenantA,
			Type:      repo.LogTypeConsume,
			Quota:     1000,
			CreatedAt: jan2026 + int64(i*60),
		})
	}

	// 3 consume logs for tenant-B (quota 9999 each) — must not appear.
	for i := 0; i < 3; i++ {
		ctx.DB.Create(&repo.Log{
			UserId:    ctx.AdminUser.Id,
			TenantId:  tenantB,
			Type:      repo.LogTypeConsume,
			Quota:     9999,
			CreatedAt: jan2026 + int64(i*60),
		})
	}

	// Build a minimal router for the invoices endpoint with explicit auth context.
	r := gin.New()
	r.GET("/api/v2/:tenant_slug/billing/invoices",
		func(c *gin.Context) {
			c.Set("tenant_context", &middleware.TenantContext{
				TenantID: tenantA,
				UserID:   ctx.NormalUser.Id,
			})
			c.Next()
		},
		ListInvoicesV2,
	)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/test-tenant/billing/invoices?from=2026-01&to=2026-01", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	items, _ := data["items"].([]interface{})

	if len(items) != 1 {
		t.Errorf("expected 1 bucket (tenant-A only), got %d — tenant-B rows may have leaked", len(items))
		return
	}
	bucket := items[0].(map[string]interface{})
	if quota := bucket["quota"].(float64); quota != 2000 {
		t.Errorf("expected quota=2000 (2×1000 for tenant-A), got %.0f — tenant-B rows may be included", quota)
	}
}

// ── User (GET/PUT /user/me) ───────────────────────────────────────────────────

// TestGetSelfV2_CrossTenantIsolation: the tenant_id in the response must match
// the auth context, not a foreign value; sensitive fields must be absent.
func TestGetSelfV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// /user/me is registered by SetupV2TestRouter.
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		"/api/v2/test-tenant/user/me", nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	// tenant_id must reflect the authenticated user's tenant.
	if tid, _ := data["tenant_id"].(string); tid != ctx.TenantID {
		t.Errorf("tenant_id = %q, want %q — wrong tenant served in /user/me", tid, ctx.TenantID)
	}

	// Sensitive credential fields must never appear.
	for _, forbidden := range []string{"password", "password_md5", "access_token"} {
		if _, ok := data[forbidden]; ok {
			t.Errorf("forbidden field %q present in /user/me response", forbidden)
		}
	}
}

// TestUpdateSelfV2_CrossTenantIsolation: PUT /user/me must only mutate the
// authenticated user's record, not any other user's record.
func TestUpdateSelfV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Capture admin user's display name before normal user makes a change.
	var beforeAdmin repo.User
	ctx.DB.First(&beforeAdmin, ctx.AdminUser.Id)

	body := map[string]interface{}{
		"display_name": "cross-tenant-update-verify",
	}

	// Normal user updates their own profile.
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut,
		"/api/v2/test-tenant/user/me", body, nil)
	AssertV2Status(t, w, http.StatusOK)

	// Admin user's display name must be unchanged.
	var afterAdmin repo.User
	ctx.DB.First(&afterAdmin, ctx.AdminUser.Id)
	if afterAdmin.DisplayName != beforeAdmin.DisplayName {
		t.Errorf("admin display_name changed from %q to %q — cross-user write occurred",
			beforeAdmin.DisplayName, afterAdmin.DisplayName)
	}

	// Normal user's record should carry the new display name.
	var afterNormal repo.User
	ctx.DB.First(&afterNormal, ctx.NormalUser.Id)
	if afterNormal.DisplayName != "cross-tenant-update-verify" {
		t.Errorf("normal user display_name = %q, want %q", afterNormal.DisplayName, "cross-tenant-update-verify")
	}
}

// ── Sessions ─────────────────────────────────────────────────────────────────

// TestListSessionsV2_CrossTenantIsolation: session counters (active_tokens,
// request_count) must be scoped to (tenant_id, user_id).  Tokens and logs
// belonging to a second tenant in the same DB must not inflate the requesting
// user's session summary.
func TestListSessionsV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tenantA := ctx.TenantID
	tenantB := "sessions-cross-tenant-other"
	now := time.Now().Unix()
	n := v2TestDBCounter.Load()
	slug := fmt.Sprintf("test-tenant-%d", n)

	// Register the sessions route with explicit tenant-A auth context.
	ctx.Router.GET("/api/v2/:tenant_slug/sessions",
		func(c *gin.Context) {
			c.Set("id", ctx.NormalUser.Id)
			c.Set("tenant_context", &middleware.TenantContext{
				TenantID: tenantA,
				UserID:   ctx.NormalUser.Id,
			})
			c.Next()
		},
		ListSessionsV2,
	)

	// Token for tenant-B (must NOT count in tenant-A's active_tokens).
	ctx.DB.Create(&repo.Token{
		UserId:   ctx.NormalUser.Id,
		TenantId: tenantB,
		Key:      "sk-tenantb-cross" + common.GetRandomString(20),
		Status:   common.TokenStatusEnabled,
		Name:     "tenantB-token",
	})

	// Log for tenant-B (must NOT count in tenant-A's request_count).
	ctx.DB.Create(&repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  tenantB,
		CreatedAt: now,
		Type:      repo.LogTypeConsume,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/"+slug+"/sessions", nil)
	w := httptest.NewRecorder()
	ctx.Router.ServeHTTP(w, req)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 session item, got %d", len(items))
	}
	item := items[0].(map[string]interface{})

	if tok := item["active_tokens"].(float64); tok != 0 {
		t.Errorf("active_tokens = %.0f, want 0 — tenant-B token must not cross into tenant-A", tok)
	}
	if rc := item["request_count"].(float64); rc != 0 {
		t.Errorf("request_count = %.0f, want 0 — tenant-B log must not cross into tenant-A", rc)
	}
}
