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

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// internalKeyAdminFixture wires the v2 internal-key admin handlers
// (ListInternalApiKeysV2 / ListInternalApiKeyTenantsV2 / GrantInternalApiKeyTenantV2 /
// RevokeInternalApiKeyTenantV2) against an isolated in-memory DB, seeding one
// internal API key (narrow-scoped, not ScopeAll) and two tenants.
type internalKeyAdminFixture struct {
	Router  *gin.Engine
	KeyID   int
	KeyHash string
	TenantA *repo.Tenant
	TenantB *repo.Tenant
	Cleanup func()
}

func setupInternalKeyAdminRouter(t *testing.T) *internalKeyAdminFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := "file:ikadmin" + formatInt64(testDBCounter.Add(1)) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.InternalApiKey{}, &repo.Tenant{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("AutoMigrate %T: %v", tbl, err)
		}
	}
	// internal_api_key_tenants 由 migration 013 建，不走 GORM —— 照抄
	// credit_pool_fund_money_path_test.go 的 schema。
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS internal_api_key_tenants (
		api_key_id INTEGER NOT NULL,
		tenant_id  VARCHAR(36) NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (api_key_id, tenant_id)
	)`).Error; err != nil {
		t.Fatalf("create whitelist table: %v", err)
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	now := time.Now()
	mkTenant := func(id, slug string) *repo.Tenant {
		tn := &repo.Tenant{
			Id: id, Name: id, Slug: slug, Status: repo.TenantStatusEnabled,
			IDPOrgID: "org_" + id, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(tn).Error; err != nil {
			t.Fatalf("create tenant %s: %v", id, err)
		}
		return tn
	}
	tenantA := mkTenant("ik-tenant-a", "ik-a")
	tenantB := mkTenant("ik-tenant-b", "ik-b")

	keyHash := hashTestKey("lurus_ik_admintest0000000000000000")
	scopes, _ := json.Marshal([]string{repo.ScopeProvisioning, repo.ScopeBalanceWrite})
	key := &repo.InternalApiKey{
		Name:      "ik-admin-test-key",
		KeyHash:   keyHash,
		KeyPrefix: "lurus_ik_admint",
		Scopes:    string(scopes),
		Enabled:   true,
	}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("create internal api key: %v", err)
	}

	router := gin.New()
	admin := router.Group("/api/v2/admin", func(c *gin.Context) {
		c.Set("id", 1) // mimics RootJWTAuth's admin actor injection
		c.Next()
	})
	ik := admin.Group("/internal-keys")
	{
		ik.GET("", ListInternalApiKeysV2)
		ik.GET("/:id/tenants", ListInternalApiKeyTenantsV2)
		ik.POST("/:id/tenants", GrantInternalApiKeyTenantV2)
		ik.DELETE("/:id/tenants/:tenant_id", RevokeInternalApiKeyTenantV2)
	}

	return &internalKeyAdminFixture{
		Router: router, KeyID: key.Id, KeyHash: keyHash,
		TenantA: tenantA, TenantB: tenantB,
		Cleanup: func() {
			repo.DB, repo.LOG_DB = prevDB, prevLogDB
			common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
			if sqlDB, _ := db.DB(); sqlDB != nil {
				_ = sqlDB.Close()
			}
		},
	}
}

// TestListInternalApiKeysV2_NoHashLeak is the load-bearing security assertion
// for the listing endpoint: the response must carry enough metadata to
// identify a key (id/name/scopes/scope_all/key_prefix/created_at) but never
// the hash or any material that could reconstruct/replay the key.
func TestListInternalApiKeysV2_NoHashLeak(t *testing.T) {
	f := setupInternalKeyAdminRouter(t)
	t.Cleanup(f.Cleanup)

	w := internalRequest(f.Router, "GET", "/api/v2/admin/internal-keys", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	assertSuccess(t, resp)

	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("expected exactly 1 key in data, got %s", mustJSON(resp))
	}
	item, ok := data[0].(map[string]interface{})
	if !ok {
		t.Fatalf("key entry is not an object: %s", mustJSON(resp))
	}

	if item["name"] != "ik-admin-test-key" {
		t.Errorf("name = %v, want ik-admin-test-key", item["name"])
	}
	if item["key_prefix"] != "lurus_ik_admint" {
		t.Errorf("key_prefix = %v, want lurus_ik_admint", item["key_prefix"])
	}
	if scopeAll, _ := item["scope_all"].(bool); scopeAll {
		t.Error("scope_all should be false for a provisioning+balance:write key")
	}
	scopes, _ := item["scopes"].([]interface{})
	if len(scopes) != 2 {
		t.Errorf("scopes = %v, want 2 entries", scopes)
	}
	if _, has := item["created_at"]; !has {
		t.Error("missing created_at field")
	}
	if _, has := item["id"]; !has {
		t.Error("missing id field")
	}

	// No field name that could carry key material.
	for _, forbidden := range []string{"key_hash", "keyhash", "hash", "key"} {
		if _, has := item[forbidden]; has {
			t.Errorf("response leaked forbidden field %q", forbidden)
		}
	}
	// Belt-and-suspenders: the raw hash value itself must never appear
	// anywhere in the response body, whatever field name it might hide under.
	if strings.Contains(w.Body.String(), f.KeyHash) {
		t.Error("response body contains the raw key_hash value")
	}
}

// TestInternalKeyTenantsV2_GrantListRevokeLifecycle covers the full whitelist
// lifecycle: empty → grant → idempotent re-grant (no duplicate row) → grant a
// second tenant → revoke → idempotent re-revoke.
func TestInternalKeyTenantsV2_GrantListRevokeLifecycle(t *testing.T) {
	f := setupInternalKeyAdminRouter(t)
	t.Cleanup(f.Cleanup)
	base := fmt.Sprintf("/api/v2/admin/internal-keys/%d/tenants", f.KeyID)

	// Initially empty.
	w := internalRequest(f.Router, "GET", base, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list(empty) status=%d body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if data, _ := resp["data"].([]interface{}); len(data) != 0 {
		t.Fatalf("expected empty whitelist, got %s", mustJSON(resp))
	}

	// Grant tenant A.
	w = internalRequest(f.Router, "POST", base, map[string]interface{}{"tenant_id": f.TenantA.Id}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("grant A status=%d body=%s", w.Code, w.Body.String())
	}
	assertSuccess(t, parseResponse(t, w))

	// Idempotent re-grant: still 200, no error.
	w = internalRequest(f.Router, "POST", base, map[string]interface{}{"tenant_id": f.TenantA.Id}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("re-grant A status=%d body=%s", w.Code, w.Body.String())
	}

	// List → exactly one row (duplicate grant did not insert a second row).
	w = internalRequest(f.Router, "GET", base, nil, nil)
	resp = parseResponse(t, w)
	data, _ := resp["data"].([]interface{})
	if len(data) != 1 {
		t.Fatalf("expected exactly 1 grant after duplicate POST, got %s", mustJSON(resp))
	}
	row, _ := data[0].(map[string]interface{})
	if row["tenant_id"] != f.TenantA.Id {
		t.Errorf("tenant_id = %v, want %s", row["tenant_id"], f.TenantA.Id)
	}

	// Grant tenant B too.
	w = internalRequest(f.Router, "POST", base, map[string]interface{}{"tenant_id": f.TenantB.Id}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("grant B status=%d body=%s", w.Code, w.Body.String())
	}
	w = internalRequest(f.Router, "GET", base, nil, nil)
	resp = parseResponse(t, w)
	data, _ = resp["data"].([]interface{})
	if len(data) != 2 {
		t.Fatalf("expected 2 grants, got %s", mustJSON(resp))
	}

	// Revoke A.
	w = internalRequest(f.Router, "DELETE", base+"/"+f.TenantA.Id, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke A status=%d body=%s", w.Code, w.Body.String())
	}
	w = internalRequest(f.Router, "GET", base, nil, nil)
	resp = parseResponse(t, w)
	data, _ = resp["data"].([]interface{})
	if len(data) != 1 {
		t.Fatalf("expected 1 grant after revoking A, got %s", mustJSON(resp))
	}
	row, _ = data[0].(map[string]interface{})
	if row["tenant_id"] != f.TenantB.Id {
		t.Errorf("remaining tenant_id = %v, want %s", row["tenant_id"], f.TenantB.Id)
	}

	// Idempotent re-revoke: A is already gone, still 200.
	w = internalRequest(f.Router, "DELETE", base+"/"+f.TenantA.Id, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("re-revoke A status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestInternalKeyTenantsV2_KeyNotFound covers the 404 path shared by all
// three tenant-whitelist endpoints when :id does not resolve to a key.
func TestInternalKeyTenantsV2_KeyNotFound(t *testing.T) {
	f := setupInternalKeyAdminRouter(t)
	t.Cleanup(f.Cleanup)
	const missing = "/api/v2/admin/internal-keys/999999/tenants"

	cases := []struct {
		method string
		path   string
		body   interface{}
	}{
		{http.MethodGet, missing, nil},
		{http.MethodPost, missing, map[string]interface{}{"tenant_id": "ik-tenant-a"}},
		{http.MethodDelete, missing + "/ik-tenant-a", nil},
	}
	for _, tc := range cases {
		w := internalRequest(f.Router, tc.method, tc.path, tc.body, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: status=%d, want 404 body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
		assertErrorCode(t, parseResponse(t, w), "KEY_NOT_FOUND")
	}
}

// TestGrantInternalApiKeyTenantV2_TenantNotFound: granting a nonexistent
// tenant_id must 404, not silently insert an orphan whitelist row.
func TestGrantInternalApiKeyTenantV2_TenantNotFound(t *testing.T) {
	f := setupInternalKeyAdminRouter(t)
	t.Cleanup(f.Cleanup)

	w := internalRequest(f.Router, "POST",
		fmt.Sprintf("/api/v2/admin/internal-keys/%d/tenants", f.KeyID),
		map[string]interface{}{"tenant_id": "no-such-tenant"}, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	assertErrorCode(t, parseResponse(t, w), "TENANT_NOT_FOUND")

	// Belt-and-suspenders: the rejected grant must not have landed anyway.
	w = internalRequest(f.Router, "GET", fmt.Sprintf("/api/v2/admin/internal-keys/%d/tenants", f.KeyID), nil, nil)
	resp := parseResponse(t, w)
	if data, _ := resp["data"].([]interface{}); len(data) != 0 {
		t.Fatalf("rejected grant leaked a whitelist row: %s", mustJSON(resp))
	}
}

// TestInternalKeysV2_CriticalRateLimit_EnforcesThenBlocks proves that
// middleware.CriticalRateLimit(), mounted on this group in production
// (api-v2-router.go:329-336), genuinely rejects requests once armed — not
// just that it no-ops when disabled. Every existing test against this router
// (including setupInternalKeyAdminRouter's shared fixture, which omits the
// middleware entirely) runs with common.CriticalRateLimitEnable at its test
// binary zero value (false), so the limiter has never actually been proven to
// limit. Mirrors the enable/save/restore pattern in
// internal/adapter/middleware/final2_cover_test.go
// TestRateLimitConstructors_EnabledFactory: since common.RedisEnabled is
// false in this fixture's DB setup, CriticalRateLimit() resolves to the
// in-memory limiter (rate-limit.go's rateLimitFactory), so no live Redis is
// needed.
func TestInternalKeysV2_CriticalRateLimit_EnforcesThenBlocks(t *testing.T) {
	f := setupInternalKeyAdminRouter(t)
	t.Cleanup(f.Cleanup)

	prevEnable := common.CriticalRateLimitEnable
	prevNum := common.CriticalRateLimitNum
	prevDuration := common.CriticalRateLimitDuration
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = 2
	common.CriticalRateLimitDuration = 3600
	defer func() {
		common.CriticalRateLimitEnable = prevEnable
		common.CriticalRateLimitNum = prevNum
		common.CriticalRateLimitDuration = prevDuration
	}()

	// setupInternalKeyAdminRouter's shared fixture builds its /internal-keys
	// group without middleware.CriticalRateLimit() (every other test in this
	// file needs the limiter OFF). This test instead builds a router that adds
	// the one piece of production wiring under test — CriticalRateLimit() must
	// be constructed here, after the globals above are armed, since
	// rateLimitFactory freezes maxRequestNum/duration/RedisEnabled at
	// middleware-construction time, not per-request.
	router := gin.New()
	admin := router.Group("/api/v2/admin", func(c *gin.Context) {
		c.Set("id", 1) // mimics RootJWTAuth's admin actor injection
		c.Next()
	})
	ik := admin.Group("/internal-keys")
	ik.Use(middleware.CriticalRateLimit())
	ik.GET("", ListInternalApiKeysV2)

	// The in-memory limiter's bucket key is mark+ClientIP and lives in a
	// package-level store that outlives this test (middleware/rate-limit.go),
	// with a key TTL far longer than a test binary. internalRequest never sets
	// RemoteAddr, so every request would land in httptest's default
	// 192.0.2.1 bucket — the second -count run then starts at an exhausted
	// window (429 before threshold) and any future limiter-mounted test is
	// poisoned too. Same reason final2_cover_test.go hands each case its own
	// IP; here a process-unique IP per invocation keeps every run fresh.
	seq := rateLimitTestIPSeq.Add(1)
	remoteAddr := fmt.Sprintf("10.99.%d.%d:1", (seq/250)%250, seq%250)
	const path = "/api/v2/admin/internal-keys"
	doGet := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	for i := 0; i < common.CriticalRateLimitNum; i++ {
		w := doGet()
		if w.Code != http.StatusOK {
			t.Fatalf("request %d/%d before threshold: status=%d, want 200, body=%s",
				i+1, common.CriticalRateLimitNum, w.Code, w.Body.String())
		}
	}
	// The (N+1)th request in the same window must be rejected by the limiter
	// itself (429), not by auth or a handler error — auth is the same stub
	// that returned 200 on every prior request.
	w := doGet()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("request past threshold: status=%d, want 429, body=%s", w.Code, w.Body.String())
	}
}

// rateLimitTestIPSeq hands each limiter-armed test invocation (including
// -count reps within one binary) a distinct client IP, because the in-memory
// limiter's buckets are package-global and effectively never expire in-test.
var rateLimitTestIPSeq atomic.Int64

// TestInternalKeysV2_UnauthenticatedRejected mounts the production
// middleware.RootJWTAuth (same wiring as api-v2-router.go's adminRoute) and
// verifies an anonymous request never reaches any of the four handlers.
func TestInternalKeysV2_UnauthenticatedRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	store := cookie.NewStore([]byte("internal-keys-admin-auth-test-secret"))
	router.Use(sessions.Sessions("session", store))
	admin := router.Group("/api/v2/admin")
	admin.Use(middleware.RootJWTAuth())
	ik := admin.Group("/internal-keys")
	{
		ik.GET("", ListInternalApiKeysV2)
		ik.GET("/:id/tenants", ListInternalApiKeyTenantsV2)
		ik.POST("/:id/tenants", GrantInternalApiKeyTenantV2)
		ik.DELETE("/:id/tenants/:tenant_id", RevokeInternalApiKeyTenantV2)
	}

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v2/admin/internal-keys"},
		{http.MethodGet, "/api/v2/admin/internal-keys/1/tenants"},
		{http.MethodPost, "/api/v2/admin/internal-keys/1/tenants"},
		{http.MethodDelete, "/api/v2/admin/internal-keys/1/tenants/some-tenant"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401 for anonymous request, got %d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
