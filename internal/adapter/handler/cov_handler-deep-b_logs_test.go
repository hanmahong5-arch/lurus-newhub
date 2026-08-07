package handler

// cov_handler-deep-b_logs_test.go — business-acceptance coverage for the
// remaining gaps across v2_log.go, v2_log_stat.go, v2_log_cluster.go,
// v2_log_export.go and v2_log_export_admin.go: the shared
// middleware.GetTenantContext defence-in-depth 401 branch (route
// misconfiguration — never reachable through SetupV2TestRouter's mockAuth,
// which always populates tenant_context), untested filter branches, row-cap
// clamp bounds, genuine DB-query failures (dropped table), and — using the
// real TEST_POSTGRES_DSN harness like internal/adapter/repo's PG-integration
// tier — the PostgreSQL-only DATE_TRUNC/TO_CHAR bucket-expression arm of
// GetLogClusterV2, which the hermetic SQLite tests can never exercise
// (common.UsingPostgreSQL is always false there).
//
// Reuses SetupV2TestRouter / V2RequestAsUser / seedStatLog / seedClusterLog /
// clusterPath / setupLogExportRouter / doExportGet / seedExportLog from the
// package's existing *_test.go scaffolding per "先读既有测试...复用脚手架".

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ─── shared bare (no-auth) router for the tenant-ctx-missing 401 branch ────

func handlerDeepBBareLogRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v2/:tenant_slug")
	g.GET("/logs", GetLogsV2)
	g.GET("/logs/all", GetAllLogsV2)
	g.GET("/logs/cluster", GetLogClusterV2)
	g.GET("/logs/stat", GetLogStatV2)
	g.GET("/logs/export", ExportLogsV2)
	return r
}

func TestGetLogsV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareLogRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/acme/logs", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

func TestGetAllLogsV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareLogRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/acme/logs/all", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

func TestGetLogClusterV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareLogRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/acme/logs/cluster", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

func TestGetLogStatV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareLogRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/acme/logs/stat", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

func TestExportLogsV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareLogRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/acme/logs/export", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

// ─── GetLogsV2 / GetAllLogsV2: DB-query failure ────────────────────────────

// TestGetLogsV2_QueryDBError forces a genuine DB failure (logs table dropped
// from under a live connection) and confirms the handler returns 500 rather
// than a partial/garbled 200.
func TestGetLogsV2_QueryDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs", nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
}

func TestGetAllLogsV2_QueryDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/logs/all", nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
}

// ============================================================================
// v2_log_stat.go
// ============================================================================

func TestGetLogStatV2_TypeFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	seedStatLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", 1000, 100, 200, now)
	// A non-consume log type (e.g. topup=1) must be excluded once type filter is set.
	otherTypeLog := &repo.Log{
		UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Type: 1,
		ModelName: "gpt-4o", Quota: 5000, CreatedAt: now,
	}
	if err := ctx.DB.Create(otherTypeLog).Error; err != nil {
		t.Fatalf("seed type-1 log: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		fmt.Sprintf("/api/v2/test-tenant/logs/stat?type=%d", repo.LogTypeConsume), nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if got := int(data["total_requests"].(float64)); got != 1 {
		t.Errorf("expected total_requests=1 with type filter (excludes the type=1 row), got %d", got)
	}
}

func TestGetLogStatV2_TokenNameFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	now := common.GetTimestamp()
	matching := &repo.Log{
		UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Type: repo.LogTypeConsume,
		ModelName: "gpt-4o", TokenName: "prod-key", Quota: 1000,
		PromptTokens: 10, CompletionTokens: 20, CreatedAt: now,
	}
	other := &repo.Log{
		UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Type: repo.LogTypeConsume,
		ModelName: "gpt-4o", TokenName: "staging-key", Quota: 9999,
		PromptTokens: 99, CompletionTokens: 99, CreatedAt: now,
	}
	if err := ctx.DB.Create(matching).Error; err != nil {
		t.Fatalf("seed matching log: %v", err)
	}
	if err := ctx.DB.Create(other).Error; err != nil {
		t.Fatalf("seed other log: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		"/api/v2/test-tenant/logs/stat?token_name=prod-key", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	// Both totals AND the rolling rpm/tpm window apply the token_name filter.
	if got := int(data["total_requests"].(float64)); got != 1 {
		t.Errorf("total_requests = %d, want 1 (token_name filter excludes staging-key)", got)
	}
	if got := int(data["total_quota"].(float64)); got != 1000 {
		t.Errorf("total_quota = %d, want 1000", got)
	}
	if got := int(data["rpm"].(float64)); got != 1 {
		t.Errorf("rpm = %d, want 1 (token_name filter applies to the rolling window too)", got)
	}
	if got := int(data["tpm"].(float64)); got != 30 {
		t.Errorf("tpm = %d, want 30 (10+20 of the matching row only)", got)
	}
}

func TestGetLogStatV2_WindowQueryDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/logs/stat", nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
}

// ============================================================================
// v2_log_cluster.go
// ============================================================================

func TestGetLogClusterV2_InvalidBucketDefaultsToHour(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedClusterLog(t, ctx, ctx.NormalUser.Id, "gpt-4o", "", 1_700_000_000)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		clusterPath(ctx, "bucket=fortnight&from=0&to=9999999999"), nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if data["bucket"] != "hour" {
		t.Errorf("bucket = %v, want 'hour' (invalid value must fall back)", data["bucket"])
	}
}

func TestGetLogClusterV2_QueryDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, clusterPath(ctx, ""), nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
}

// ─── real-PostgreSQL tier: exercises the PG-only DATE_TRUNC/TO_CHAR bucket
// expression that the hermetic SQLite suite structurally cannot reach
// (common.UsingPostgreSQL is always false under SetupV2TestRouter). Mirrors
// the connect/migrate/cleanup shape of repo.SetupTestDB, scoped locally
// because that helper lives in package repo's _test.go and isn't importable.

var handlerDeepBPGCounter atomic.Int64

type handlerDeepBPGClusterCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	tenantID string
	userID   int
}

func handlerDeepBSetupPGClusterRouter(t *testing.T) *handlerDeepBPGClusterCtx {
	t.Helper()
	baseDSN := os.Getenv("TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping PostgreSQL bucket-expression test")
	}
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("test_handler_cluster_%d_%d", time.Now().UnixNano(), handlerDeepBPGCounter.Add(1))
	adminDB, err := gorm.Open(postgres.Open(baseDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := adminDB.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create test database %q: %v", dbName, err)
	}
	if sqlDB, err := adminDB.DB(); err == nil {
		_ = sqlDB.Close()
	}

	testDSN := handlerDeepBSwapDBName(baseDSN, dbName)
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database %q: %v", dbName, err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Tenant{}, &repo.Log{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	repo.DB, repo.LOG_DB = db, db
	common.UsingSQLite = false
	common.UsingPostgreSQL = true
	common.RedisEnabled = false
	repo.InitCol()

	tenantID := "pg-cluster-tenant"
	if err := db.Create(&repo.Tenant{
		Id: tenantID, Name: "PG Cluster Tenant", Slug: "pg-cluster-tenant",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_pg_cluster",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	user := &repo.User{
		Username: "pg-cluster-user", DisplayName: "PG Cluster User",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "pgcluster@test.local", TenantId: tenantID,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	mockAuth := func(c *gin.Context) {
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID: tenantID,
			UserID:   user.Id,
		})
		c.Next()
	}
	router := gin.New()
	router.GET("/api/v2/:tenant_slug/logs/cluster", mockAuth, GetLogClusterV2)

	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		if dropDB, err := gorm.Open(postgres.Open(baseDSN), &gorm.Config{}); err == nil {
			_ = dropDB.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error
			if sqlDB, err := dropDB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
	})

	return &handlerDeepBPGClusterCtx{router: router, db: db, tenantID: tenantID, userID: user.Id}
}

// handlerDeepBSwapDBName mirrors repo.buildTestDSN's URL-form handling —
// duplicated locally since that helper is unexported in package repo.
func handlerDeepBSwapDBName(baseDSN, dbName string) string {
	// All local/CI TEST_POSTGRES_DSN values used by this repo are URL-form
	// (postgres://...); reuse net/url via a minimal manual replace to avoid
	// pulling in the regex/kv-form path repo's helper also supports.
	if idx := lastSlashDeepB(baseDSN); idx >= 0 {
		if q := indexByteDeepB(baseDSN[idx:], '?'); q >= 0 {
			return baseDSN[:idx] + "/" + dbName + baseDSN[idx+q:]
		}
		return baseDSN[:idx] + "/" + dbName
	}
	return baseDSN
}

func lastSlashDeepB(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func indexByteDeepB(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func TestGetLogClusterV2_PostgresBucketExpression(t *testing.T) {
	pg := handlerDeepBSetupPGClusterRouter(t)

	base := time.Now().Add(-30 * time.Minute).Unix()
	for i := 0; i < 3; i++ {
		if err := pg.db.Create(&repo.Log{
			UserId: pg.userID, TenantId: pg.tenantID, Type: repo.LogTypeConsume,
			ModelName: "gpt-4o", Content: "", CreatedAt: base + int64(i)*60,
		}).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	for _, bucket := range []string{"hour", "day"} {
		t.Run(bucket, func(t *testing.T) {
			path := fmt.Sprintf("/api/v2/pg-cluster-tenant/logs/cluster?bucket=%s&from=0&to=%d", bucket, time.Now().Unix()+3600)
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			pg.router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (PG DATE_TRUNC/TO_CHAR query must not error), body=%s", w.Code, w.Body.String())
			}
			resp := ParseV2Response(t, w)
			if resp["success"] != true {
				t.Fatalf("success = %v, body=%s", resp["success"], w.Body.String())
			}
			data := resp["data"].(map[string]interface{})
			items := data["items"].([]interface{})
			if len(items) != 1 {
				t.Fatalf("items = %d, want 1 aggregated row (3 same-model/same-bucket logs), body=%s", len(items), w.Body.String())
			}
			row := items[0].(map[string]interface{})
			if row["count"].(float64) != 3 {
				t.Errorf("count = %v, want 3", row["count"])
			}
			if row["bucket"] == "" {
				t.Errorf("bucket string is empty — PG TO_CHAR formatting produced no value")
			}
		})
	}
}

// ============================================================================
// v2_log_export.go
// ============================================================================

func TestExportLogsV2_MaxRowsNonPositiveDefaults(t *testing.T) {
	ctx := setupLogExportRouter(t)

	base := int64(1_700_000_000)
	for i := 0; i < 3; i++ {
		seedExportLog(t, ctx, "gpt-4o", "tk", base+int64(i))
	}
	// max_rows=0 is non-positive → falls back to exportDefaultMaxRows, so all
	// 3 seeded rows must come back (not zero, not an error).
	w := doExportGet(ctx, ctx.tenantSlug, "max_rows=0", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 4 { // header + 3 rows
		t.Errorf("rows = %d, want 4 (header + 3), body:\n%s", len(records), w.Body.String())
	}
}

func TestExportLogsV2_MaxRowsOverHardCapClamped(t *testing.T) {
	ctx := setupLogExportRouter(t)
	seedExportLog(t, ctx, "gpt-4o", "tk", 1_700_000_000)

	// A max_rows far above exportHardMaxRows must not error and must still
	// return the (small) actual row count — proving the clamp doesn't break
	// the request, just bounds the ceiling.
	w := doExportGet(ctx, ctx.tenantSlug, "max_rows=999999999", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 2 { // header + 1 row
		t.Errorf("rows = %d, want 2 (header + 1), body:\n%s", len(records), w.Body.String())
	}
}

// TestExportLogsV2_QueryDBError drops the logs table and confirms the
// streaming handler stops cleanly (status already committed as 200 + headers
// by the time the query runs, per the handler's own streaming design — this
// locks in that a mid-stream DB failure yields header-only output rather
// than a panic or a hung response).
func TestExportLogsV2_QueryDBError(t *testing.T) {
	ctx := setupLogExportRouter(t)

	if err := ctx.db.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := doExportGet(ctx, ctx.tenantSlug, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (headers already committed), body=%s", w.Code, w.Body.String())
	}
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("rows = %d, want 1 (header only — the query failure aborts before any data row), body:\n%s", len(records), w.Body.String())
	}
}

// ============================================================================
// v2_log_export_admin.go
// ============================================================================

var handlerDeepBAdminExportDBCounter atomic.Int64

type handlerDeepBAdminExportCtx struct {
	router *gin.Engine
	db     *gorm.DB
}

func handlerDeepBSetupAdminExportRouter(t *testing.T) *handlerDeepBAdminExportCtx {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	router := gin.New()
	router.GET("/api/v2/admin/logs/export", ExportAdminLogsV2)
	return &handlerDeepBAdminExportCtx{router: router, db: ctx.DB}
}

func TestExportAdminLogsV2_CountQueryDBError(t *testing.T) {
	ctx := handlerDeepBSetupAdminExportRouter(t)

	if err := ctx.db.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/logs/export", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "Failed to query logs" {
		t.Errorf("message = %v, want 'Failed to query logs'", resp["message"])
	}
}

func TestExportAdminLogsV2_MaxRowsNonPositiveClampsToHardCap(t *testing.T) {
	ctx := handlerDeepBSetupAdminExportRouter(t)

	l := &repo.Log{TenantId: "t1", UserId: 1, Type: repo.LogTypeConsume, ModelName: "gpt-4o", CreatedAt: 1_700_000_000}
	if err := ctx.db.Create(l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/logs/export?max_rows=-1", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	// max_rows<=0 clamps to adminExportHardMaxRows, so X-Truncated must NOT be
	// set for a single-row dataset (total is nowhere near the 100k cap).
	if got := w.Header().Get("X-Truncated"); got != "" {
		t.Errorf("X-Truncated = %q, want empty (clamp went to the hard cap, not zero)", got)
	}
	if got := w.Header().Get("X-Total-Matched"); got != "1" {
		t.Errorf("X-Total-Matched = %q, want 1", got)
	}
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 2 { // header + 1 row
		t.Errorf("rows = %d, want 2 (header + 1), body:\n%s", len(records), w.Body.String())
	}
}
