package handler

// v2_analytics_rankings_test.go — GetTenantRankingsV2: the tenant-admin
// gate and per-tenant scoping of the model/vendor leaderboard. Self-
// contained hermetic SQLite router, mirroring v2_pricing_write_test.go's
// pattern (mock tenant_context + role, no TenantSlugGuard/UserAuth — those
// are proven elsewhere).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var tenantRankingsTestDBCounter atomic.Int64

type tenantRankingsCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	tenantID string
	cleanup  func()
}

func setupTenantRankingsRouter(t *testing.T) *tenantRankingsCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := tenantRankingsTestDBCounter.Add(1)
	dsn := fmt.Sprintf("file:tenantrankings%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.Log{}); err != nil {
		t.Fatalf("auto migrate entity.Log: %v", err)
	}

	prevDB, prevLog := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false

	tenantID := fmt.Sprintf("tenant-rankings-%d", n)

	router := gin.New()
	// mockAuth's context shape mirrors the real UserAuth()+TenantSlugGuard()
	// chain, which always populates tenant_context before the handler runs
	// (same rationale as setupPricingWriteRouter's mockAuth). X-Test-Role
	// downgrades the caller to exercise the 403 gate.
	mockAuth := func(c *gin.Context) {
		role := common.RoleCommonUser
		switch c.GetHeader("X-Test-Role") {
		case "admin":
			role = common.RoleAdminUser
		case "root":
			role = common.RoleRootUser
		}
		c.Set("role", role)
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID: tenantID,
			UserID:   1,
		})
		c.Next()
	}
	router.GET("/api/v2/:tenant_slug/analytics/rankings", mockAuth, GetTenantRankingsV2)

	return &tenantRankingsCtx{
		router:   router,
		db:       db,
		tenantID: tenantID,
		cleanup: func() {
			repo.DB, repo.LOG_DB = prevDB, prevLog
			common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
			if sqlDB, err := db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		},
	}
}

func doGETWithHeaders(router *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func seedTenantRankingsLog(t *testing.T, db *gorm.DB, tenantID, model string, prompt, completion, quota int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenantID,
		Type:             entity.LogTypeConsume,
		ModelName:        model,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
	}
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed tenant rankings log: %v", err)
	}
}

// TestTenantRankingsV2_ForbiddenForNormalUser: tenant-wide rankings cover
// every member's usage, so the route carries the same admin gate as
// GetAllLogStatV2 (v2_log_stat.go:87-113).
func TestTenantRankingsV2_ForbiddenForNormalUser(t *testing.T) {
	ctx := setupTenantRankingsRouter(t)
	defer ctx.cleanup()

	w := doGET(ctx.router, "/api/v2/acme/analytics/rankings")
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a normal (non-admin) caller, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestTenantRankingsV2_AdminSeesOwnTenantOnly: a tenant admin's rankings
// must reflect only their own tenant's usage, even when another tenant logs
// the same model name at a much larger volume. The mutation named in the
// plan (drop tenant_id where clause) is exercised at the repo layer by
// TestGetRankings_TenantIsolation; this test proves the same guarantee
// holds end-to-end through the HTTP handler.
func TestTenantRankingsV2_AdminSeesOwnTenantOnly(t *testing.T) {
	ctx := setupTenantRankingsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedTenantRankingsLog(t, ctx.db, ctx.tenantID, "shared-model", 100, 50, 30, now-60)
	seedTenantRankingsLog(t, ctx.db, "other-tenant", "shared-model", 100_000, 50_000, 30_000, now-60)

	w := doGETWithHeaders(ctx.router, "/api/v2/acme/analytics/rankings?by=model&hours=1",
		map[string]string{"X-Test-Role": "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	body := parseJSON(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data object: %s", w.Body.String())
	}
	if got := data["by"]; got != "model" {
		t.Errorf("data.by = %v, want model", got)
	}
	rows, ok := data["rows"].([]interface{})
	if !ok {
		t.Fatalf("missing rows array: %s", w.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row scoped to the caller's own tenant, got %d: %v", len(rows), rows)
	}
	row := rows[0].(map[string]interface{})
	if row["name"] != "shared-model" {
		t.Errorf("name = %v, want shared-model", row["name"])
	}
	if got := row["total_tokens"].(float64); got != 150 {
		t.Errorf("total_tokens = %v, want 150 (other tenant's 150000 leaked in)", got)
	}
	if got := row["quota"].(float64); got != 30 {
		t.Errorf("quota = %v, want 30 (other tenant's 30000 leaked in)", got)
	}
}

// TestTenantRankingsV2_RootRoleAlsoAdmitted: requireTenantAdmin's gate is
// >= RoleAdminUser, so a platform-root caller (who outranks tenant-admin)
// must also pass — mirrors GetAllLogStatV2's gate semantics.
func TestTenantRankingsV2_RootRoleAlsoAdmitted(t *testing.T) {
	ctx := setupTenantRankingsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedTenantRankingsLog(t, ctx.db, ctx.tenantID, "alpha", 10, 5, 3, now-60)

	w := doGETWithHeaders(ctx.router, "/api/v2/acme/analytics/rankings",
		map[string]string{"X-Test-Role": "root"})
	if w.Code != http.StatusOK {
		t.Fatalf("root caller should be admitted, got %d body=%s", w.Code, w.Body.String())
	}
}
