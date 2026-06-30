package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Test infrastructure for the Provisioning LIST endpoint (Tier 1.1).
//
// The existing SetupIntegrationRouter doesn't wire the provisioning route
// group or seed the (api_key, tenant) whitelist that
// repo.InternalKeyAllowedForTenant queries, so this file owns its own setup.
// SQLite in-memory keeps tests hermetic (no TEST_POSTGRES_DSN dependency).

var provTestDBCounter atomic.Int64

// provTestCtx bundles the test-scoped state callers need.
type provTestCtx struct {
	router          *gin.Engine
	db              *gorm.DB
	cleanup         func()
	tenantAlpha     *repo.Tenant
	tenantBeta      *repo.Tenant
	resellerUserID  int // CreatedBy on the narrow + scope-all api keys below
	otherResellerID int // CreatedBy on the parallel narrow key for isolation test
	narrowKeyRaw    string
	narrowKeyOther  string
	scopeAllRaw     string
}

func setupProvTestRouter(t *testing.T) *provTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:prov%d?mode=memory&cache=shared", provTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{},
		&repo.Token{},
		&repo.Tenant{},
		&repo.InternalApiKey{},
		&repo.Option{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}
	// internal_api_key_tenants is created by migration 013 outside GORM —
	// mirror its schema here so InternalKeyAllowedForTenant can query it.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS internal_api_key_tenants (
		api_key_id INTEGER NOT NULL,
		tenant_id  VARCHAR(36) NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (api_key_id, tenant_id)
	)`).Error; err != nil {
		t.Fatalf("create whitelist table: %v", err)
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	// Seed Reseller users — CreatedBy on the api keys below.
	reseller := &repo.User{
		Username: "reseller-a", DisplayName: "Reseller A",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled,
		Email: "reseller-a@test.local",
	}
	other := &repo.User{
		Username: "reseller-b", DisplayName: "Reseller B",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled,
		Email: "reseller-b@test.local",
	}
	if err := db.Create(reseller).Error; err != nil {
		t.Fatalf("seed reseller-a: %v", err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("seed reseller-b: %v", err)
	}

	alpha := &repo.Tenant{
		Id: "tenant-alpha", Name: "Alpha Org", Slug: "alpha",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_alpha",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	beta := &repo.Tenant{
		Id: "tenant-beta", Name: "Beta Org", Slug: "beta",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_beta",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(alpha).Error; err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	if err := db.Create(beta).Error; err != nil {
		t.Fatalf("seed beta: %v", err)
	}

	provScopesJSON, _ := json.Marshal([]string{repo.ScopeProvisioning})
	allScopesJSON, _ := json.Marshal([]string{repo.ScopeAll})

	narrowRaw := "lurus_ik_narrow_" + common.GetRandomString(24)
	narrowOtherRaw := "lurus_ik_narrowB_" + common.GetRandomString(24)
	scopeAllRaw := "lurus_ik_admin_" + common.GetRandomString(24)

	mkKey := func(name, raw string, scopes string, createdBy int) *repo.InternalApiKey {
		k := &repo.InternalApiKey{
			Name: name, KeyHash: hashTestKey(raw), KeyPrefix: raw[:16],
			Scopes: scopes, CreatedBy: createdBy, Enabled: true,
		}
		if err := db.Create(k).Error; err != nil {
			t.Fatalf("seed key %s: %v", name, err)
		}
		return k
	}
	narrowKey := mkKey("reseller-a-narrow", narrowRaw, string(provScopesJSON), reseller.Id)
	mkKey("reseller-b-narrow", narrowOtherRaw, string(provScopesJSON), other.Id)
	mkKey("platform-admin", scopeAllRaw, string(allScopesJSON), reseller.Id)

	// Authorise narrowKey for tenant-alpha ONLY — exercises both the
	// happy-path allow and the cross-tenant deny in one fixture.
	if err := db.Exec(
		`INSERT INTO internal_api_key_tenants (api_key_id, tenant_id) VALUES (?, ?)`,
		narrowKey.Id, alpha.Id,
	).Error; err != nil {
		t.Fatalf("seed whitelist row: %v", err)
	}

	router := gin.New()
	provGroup := router.Group("/internal/v1/provisioning")
	provGroup.Use(middleware.InternalApiAuth())
	provGroup.Use(middleware.RequireScope(repo.ScopeProvisioning))
	{
		provGroup.POST("/tenants/:slug/keys", CreateProvisionedKey)
		provGroup.GET("/tenants/:slug/keys", ListProvisionedKeys)
		provGroup.DELETE("/tenants/:slug/keys/:key_id", RevokeProvisionedKey)
	}

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(cleanup)

	return &provTestCtx{
		router:          router,
		db:              db,
		cleanup:         cleanup,
		tenantAlpha:     alpha,
		tenantBeta:      beta,
		resellerUserID:  reseller.Id,
		otherResellerID: other.Id,
		narrowKeyRaw:    narrowRaw,
		narrowKeyOther:  narrowOtherRaw,
		scopeAllRaw:     scopeAllRaw,
	}
}

// seedProvisionedToken inserts a Token directly, mimicking CreateProvisionedKey
// without exercising the HTTP layer. Returns the persisted row.
func seedProvisionedToken(t *testing.T, ctx *provTestCtx, tenantID, name string, creatorUserID int) *repo.Token {
	t.Helper()
	tok := &repo.Token{
		UserId: 0, TenantId: tenantID, Name: name,
		Key:           "sk-" + common.GetRandomString(40),
		Status:        common.TokenStatusEnabled,
		CreatedTime:   common.GetTimestamp(),
		AccessedTime:  common.GetTimestamp(),
		ExpiredTime:   -1,
		CreatorUserId: creatorUserID,
		Group:         "default",
		// model_limits intentionally left non-empty to assert it never leaks.
		ModelLimitsEnabled: true,
		ModelLimits:        "gpt-4,gpt-3.5-turbo",
	}
	if err := ctx.db.Create(tok).Error; err != nil {
		t.Fatalf("seed token %q: %v", name, err)
	}
	return tok
}

func doListRequest(ctx *provTestCtx, slug, apiKey, query string) *httptest.ResponseRecorder {
	url := fmt.Sprintf("/internal/v1/provisioning/tenants/%s/keys", slug)
	if query != "" {
		url = url + "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func parseList(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// ─── Tests ──────────────────────────────────────────────────────────────────

// 1. Empty tenant returns 200 + empty items + total=0 (not 404).
func TestListProvisionedKeys_EmptyTenant(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := doListRequest(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	data := resp["data"].(map[string]interface{})
	if data["total"].(float64) != 0 {
		t.Errorf("total = %v, want 0", data["total"])
	}
	if items, _ := data["items"].([]interface{}); len(items) != 0 {
		t.Errorf("items len = %d, want 0", len(items))
	}
}

// 2. Cross-tenant isolation: reseller-a's narrow key cannot list reseller-b's
//    tenant even when both happen to have tokens under it.
func TestListProvisionedKeys_CrossTenantDenied(t *testing.T) {
	ctx := setupProvTestRouter(t)
	// reseller-a has whitelist for alpha only — request beta → 403.
	w := doListRequest(ctx, ctx.tenantBeta.Slug, ctx.narrowKeyRaw, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	if resp["error_code"] != "TENANT_NOT_AUTHORIZED" {
		t.Errorf("error_code = %v, want TENANT_NOT_AUTHORIZED", resp["error_code"])
	}
}

// 3. Narrow scope without any whitelist row → 403. Uses reseller-b's key
//    which has no row in internal_api_key_tenants (only reseller-a was
//    seeded). Same code path as test 2 but the failure source is different
//    (empty whitelist vs wrong tenant).
func TestListProvisionedKeys_NarrowNoWhitelistDenied(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := doListRequest(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyOther, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	if resp["error_code"] != "TENANT_NOT_AUTHORIZED" {
		t.Errorf("error_code = %v, want TENANT_NOT_AUTHORIZED", resp["error_code"])
	}
}

// 4. ScopeAll key bypasses the whitelist for any tenant.
func TestListProvisionedKeys_ScopeAllBypass(t *testing.T) {
	ctx := setupProvTestRouter(t)
	// Seed two tokens under tenant-beta created by the same reseller (the
	// admin's CreatedBy is reseller.Id in this fixture).
	seedProvisionedToken(t, ctx, ctx.tenantBeta.Id, "beta-key-1", ctx.resellerUserID)
	seedProvisionedToken(t, ctx, ctx.tenantBeta.Id, "beta-key-2", ctx.resellerUserID)

	w := doListRequest(ctx, ctx.tenantBeta.Slug, ctx.scopeAllRaw, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	data := resp["data"].(map[string]interface{})
	if data["total"].(float64) != 2 {
		t.Errorf("total = %v, want 2", data["total"])
	}
}

// 5. Pagination — seed 5 tokens, request limit=2, offset=2 → exactly 2 items
//    from the middle of the window; total remains 5.
func TestListProvisionedKeys_PaginationBoundary(t *testing.T) {
	ctx := setupProvTestRouter(t)
	for i := 0; i < 5; i++ {
		seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id,
			fmt.Sprintf("alpha-key-%d", i), ctx.resellerUserID)
	}

	w := doListRequest(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, "limit=2&offset=2")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	data := resp["data"].(map[string]interface{})
	if data["total"].(float64) != 5 {
		t.Errorf("total = %v, want 5", data["total"])
	}
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Errorf("len(items) = %d, want 2 (limit clamp)", len(items))
	}
}

// 6. Response JSON whitelist — key / key_hash / model_limits must never
//    appear in the serialised output. This is the security guarantee for
//    a public-facing LIST endpoint.
func TestListProvisionedKeys_ResponseFieldWhitelist(t *testing.T) {
	ctx := setupProvTestRouter(t)
	seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id, "leaky-check", ctx.resellerUserID)

	w := doListRequest(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, banned := range []string{
		`"key":"sk-`, `"key_hash"`, `"model_limits"`, `"model_limits_enabled"`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("response leaks %q — got body: %s", banned, body)
		}
	}
	// And sanity-check that allowed fields are present.
	for _, expected := range []string{
		`"id"`, `"name"`, `"status"`, `"unlimited_quota"`, `"remain_quota"`,
		`"used_quota"`, `"group"`, `"expires_at"`, `"created_at"`,
		`"last_used_at"`, `"created_by_user_id"`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("response missing %q — got body: %s", expected, body)
		}
	}
}

// 7. limit > 100 is silently clamped to 100 (no error). We seed 150 tokens
//    and request limit=500 — response carries 100 items, total=150.
func TestListProvisionedKeys_LimitClamp(t *testing.T) {
	ctx := setupProvTestRouter(t)
	for i := 0; i < 150; i++ {
		seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id,
			fmt.Sprintf("bulk-%03d", i), ctx.resellerUserID)
	}
	w := doListRequest(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, "limit=500")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	data := resp["data"].(map[string]interface{})
	if data["limit"].(float64) != 100 {
		t.Errorf("limit echoed = %v, want clamped to 100", data["limit"])
	}
	if items := data["items"].([]interface{}); len(items) != 100 {
		t.Errorf("len(items) = %d, want 100", len(items))
	}
	if data["total"].(float64) != 150 {
		t.Errorf("total = %v, want 150 (unclamped)", data["total"])
	}
}

// 8. limit <= 0 is a hard 400 rather than a silent clamp. Without this the
//    client gets an empty page for ?limit=0 and silently assumes no keys
//    exist — exactly the bug Tier 1.1 is trying to prevent.
func TestListProvisionedKeys_RejectLimitZero(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := doListRequest(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, "limit=0")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	if resp["error_code"] != "INVALID_PAGINATION" {
		t.Errorf("error_code = %v, want INVALID_PAGINATION", resp["error_code"])
	}
}
