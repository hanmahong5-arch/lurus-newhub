package router

// l7_rankings_by_group_real_chain_test.go — REAL-CHAIN oracle for L7's
// by=group dimension (repair-round ruling R5). The handler-package tests
// (internal/adapter/handler/v2_analytics_rankings_test.go) mount
// GetTenantRankingsV2 directly on a bare gin.New() with tenant_context
// hand-seeded via c.Set — sound for the query/cache/response-shape claims
// they make, but they do not prove the route is reachable through the
// PRODUCTION UserAuth()+TenantSlugGuard() chain a real console request
// goes through (router/api-v2-router.go:213-215). This file closes that
// gap the same way channel_sensitive_write_real_chain_test.go does for L2:
// SetApiV2Router mounts the real middleware chain, identity comes from a
// seeded repo.User row via the session (not a hand-set context key), and
// the tenant is resolved by slug the way TenantSlugGuard actually does it.
//
// Mutation target: dropping the tenant_id predicate from
// getGroupUsageTotals/getTenantUsageTotals (or the tenant-scoping check
// anywhere in the chain) turns TestRankingsByGroupRealChain_ScopedToOwnTenant
// red — a leaked row from "other-tenant" appears in the response.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var rankingsRealChainDBCounter atomic.Int64

// setupRankingsRealChain wires an isolated in-memory DB with one tenant and
// a tenant-admin (role 10) user in it, and returns a request builder that
// serves through the REAL SetApiV2Router (exactly as cmd/server does).
func setupRankingsRealChain(t *testing.T) (tenantID, tenantSlug string, serve func(role int, path string) *httptest.ResponseRecorder, cleanup func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := rankingsRealChainDBCounter.Add(1)
	dbName := fmt.Sprintf("file:rankingsrealchain%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &entity.Tenant{}, &entity.Log{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB, prevRedis := repo.DB, repo.LOG_DB, common.RedisEnabled
	prevLogConsume := common.LogConsumeEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.RedisEnabled = false
	common.LogConsumeEnabled = false

	tenantID = fmt.Sprintf("rankings-rc-tenant-%d", n)
	tenantSlug = fmt.Sprintf("rankings-rc-slug-%d", n)
	tenant := &entity.Tenant{
		Id: tenantID, IDPOrgID: "rankings-rc-org-" + tenantID, Slug: tenantSlug,
		Name: "Rankings Real Chain Tenant", Status: entity.TenantStatusEnabled,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	adminRow := &repo.User{
		Username: "rc_admin", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Email: "rankings_rc_admin@local", TenantId: tenantID,
	}
	if err := db.Create(adminRow).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	cleanup = func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		common.LogConsumeEnabled = prevLogConsume
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	serve = func(role int, path string) *httptest.ResponseRecorder {
		engine := gin.New()
		engine.Use(gin.Recovery())
		store := cookie.NewStore([]byte("rankings-real-chain-secret"))
		engine.Use(sessions.Sessions("session", store))
		engine.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("username", "rc_admin")
			s.Set("role", role)
			s.Set("id", adminRow.Id)
			s.Set("status", common.UserStatusEnabled)
			_ = s.Save()
			c.Next()
		})
		SetApiV2Router(engine)

		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	return tenantID, tenantSlug, serve, cleanup
}

func seedRankingsRealChainLog(t *testing.T, db *gorm.DB, tenantID, group string, prompt, completion, quota int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId: 1, TenantId: tenantID, Type: entity.LogTypeConsume,
		ModelName: "irrelevant-model", Group: group, Quota: quota,
		PromptTokens: prompt, CompletionTokens: completion, CreatedAt: createdAt,
	}
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed rankings real-chain log: %v", err)
	}
}

// TestRankingsByGroupRealChain_ScopedToOwnTenant proves, through the
// production route table (SetApiV2Router mounts
// GET /api/v2/:tenant_slug/analytics/rankings under UserAuth()+
// TenantSlugGuard(), router/api-v2-router.go:213-215), that by=group
// returns only the caller's own tenant's groups.
func TestRankingsByGroupRealChain_ScopedToOwnTenant(t *testing.T) {
	tenantID, tenantSlug, serve, cleanup := setupRankingsRealChain(t)
	defer cleanup()

	now := time.Now().Unix()
	seedRankingsRealChainLog(t, repo.LOG_DB, tenantID, "premium", 100, 50, 30, now-60)
	seedRankingsRealChainLog(t, repo.LOG_DB, "rankings-rc-other-tenant", "premium", 100_000, 50_000, 30_000, now-60)

	path := fmt.Sprintf("/api/v2/%s/analytics/rankings?by=group&hours=1", tenantSlug)
	w := serve(common.RoleAdminUser, path)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200; body=%s", path, w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			By   string `json:"by"`
			Rows []struct {
				Name        string `json:"name"`
				TotalTokens int64  `json:"total_tokens"`
				Quota       int64  `json:"quota"`
			} `json:"rows"`
			Series []struct {
				Name   string `json:"name"`
				Tokens int64  `json:"tokens"`
			} `json:"series"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}
	if resp.Data.By != "group" {
		t.Fatalf("data.by = %q, want group", resp.Data.By)
	}
	// Both tenants seed the SAME group name ("premium") deliberately: if
	// the tenant predicate were dropped anywhere in the chain, the two
	// tenants' rows would silently MERGE into one row with a combined
	// total rather than appearing as an obviously-extra row, so the
	// oracle must assert on the row's own totals, not just its count.
	if len(resp.Data.Rows) != 1 {
		t.Fatalf("want exactly 1 row scoped to the caller's own tenant, got %d: %+v", len(resp.Data.Rows), resp.Data.Rows)
	}
	row := resp.Data.Rows[0]
	if row.Name != "premium" {
		t.Fatalf("row.name = %q, want premium", row.Name)
	}
	if row.TotalTokens != 150 {
		t.Fatalf("row.total_tokens = %d, want 150 (the other tenant's 150000 must not be merged in)", row.TotalTokens)
	}
	if row.Quota != 30 {
		t.Fatalf("row.quota = %d, want 30 (the other tenant's 30000 must not be merged in)", row.Quota)
	}
	// The trend series (cycle 16 P8) follows the same tenant scope: the
	// same group name from the other tenant must not be summed in.
	var seriesTokens int64
	for _, p := range resp.Data.Series {
		if p.Name != "premium" {
			t.Fatalf("series carries %q, not a ranked row", p.Name)
		}
		seriesTokens += p.Tokens
	}
	if seriesTokens != 150 {
		t.Fatalf("series tokens = %d, want 150 (own tenant only)", seriesTokens)
	}
}
