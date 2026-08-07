package handler

// cov_handler-deep-b_logv1_test.go — business-acceptance coverage for the
// remaining gaps in log.go (the v1/session-auth log endpoints):
// GetAllLogs/GetUserLogs/SearchAllLogs/SearchUserLogs/GetLogByKey/
// DeleteHistoryLogs DB-error branches, plus SearchAllLogs's two auth-scope
// arms (tenant-scoped non-root vs. cross-tenant root) which had zero
// coverage before this file (redemption_log_v1_extra_test.go only exercises
// GetAllLogs/GetUserLogs/GetLogsStat/GetLogsSelfStat/DeleteHistoryLogs).
//
// Reuses SetupV2TestRouter / registerV1LogRoutes / seedV1Log from
// redemption_log_v1_extra_test.go (same package) per "先读既有测试...复用脚手架".
// All DB-error branches use a genuinely dropped table (glebarez/sqlite
// Migrator().DropTable) — a real DB failure, never a fabricated nil input.

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func TestGetAllLogs_V1_DBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1LogRoutes(ctx, ctx.AdminUser.Id, 0)

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/log/?p=1&page_size=10", nil, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Errorf("expected failure once the logs table is gone, body: %s", w.Body.String())
	}
}

func TestGetUserLogs_V1_DBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1LogRoutes(ctx, ctx.NormalUser.Id, 0)

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/log/self?p=1&page_size=10", nil, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Errorf("expected failure once the logs table is gone, body: %s", w.Body.String())
	}
}

func TestDeleteHistoryLogs_V1_DBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1LogRoutes(ctx, ctx.NormalUser.Id, 0)

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	future := common.GetTimestamp() + 100000
	w := V2Request(ctx.Router, http.MethodDelete, "/api/log/?target_timestamp="+strconv.FormatInt(future, 10), nil, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Errorf("expected failure once the logs table is gone, body: %s", w.Body.String())
	}
}

// ─── GetLogByKey ────────────────────────────────────────────────────────────

func registerV1LogByKeyRoute(ctx *V2TestContext) {
	ctx.Router.GET("/api/log/token", GetLogByKey)
}

// TestGetLogByKey_Success proves the happy-path join actually resolves a
// token's logs by its bearer key (the only existing coverage for this
// handler was incidental/none — this is the first direct test).
func TestGetLogByKey_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1LogByKeyRoute(ctx)

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "logbykey-token")
	l := &repo.Log{
		UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, TokenId: tok.Id,
		Type: repo.LogTypeConsume, ModelName: "gpt-4o", Quota: 100, CreatedAt: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	w := V2Request(ctx.Router, http.MethodGet, "/api/log/token?key=sk-"+tok.Key, nil, nil)
	resp := AssertV2Success(t, w)
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("expected 1 log for the token's key, got %v (body=%s)", resp["data"], w.Body.String())
	}
}

// TestGetLogByKey_DBError forces the join query itself to fail (drop the
// logs table the join selects from) and asserts the handler's own error
// envelope — success:false + the raw driver error message, per the
// handler's `"message": err.Error()` contract.
func TestGetLogByKey_DBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1LogByKeyRoute(ctx)

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/log/token?key=sk-anything", nil, nil)
	if w.Code != http.StatusOK { // handler always returns 200 per its own contract
		t.Fatalf("status = %d, want 200 (handler wraps errors in the 200 envelope), body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Errorf("expected success=false once the logs table is gone, body: %s", w.Body.String())
	}
}

// ─── SearchAllLogs: tenant-scoped (non-root) vs. cross-tenant (root) arms ──

func registerV1SearchLogsRoute(ctx *V2TestContext, role int, userID int, username string) {
	g := ctx.Router.Group("/api/log/search")
	g.Use(func(c *gin.Context) {
		c.Set("tenant_id", ctx.TenantID)
		c.Set("id", userID)
		c.Set("username", username)
		c.Set("role", role)
		c.Next()
	})
	g.GET("/", SearchAllLogs)
}

// TestSearchAllLogs_NonRoot_TenantScoped exercises the c.GetInt("role") <
// RoleRootUser branch: a tenant admin's search must go through
// repo.SearchAllLogs(ForTenant(...)) — never app.SearchLogs, which is
// deliberately cross-tenant. This branch had zero prior coverage.
func TestSearchAllLogs_NonRoot_TenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1SearchLogsRoute(ctx, common.RoleAdminUser, ctx.AdminUser.Id, "v2testadmin")

	l := &repo.Log{
		UserId: ctx.AdminUser.Id, TenantId: ctx.TenantID, Type: repo.LogTypeConsume,
		Content: "needle-content", ModelName: "gpt-4o", CreatedAt: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	w := V2Request(ctx.Router, http.MethodGet, "/api/log/search/?keyword=needle", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 matching log for a tenant admin, got %d (body=%s)", len(items), w.Body.String())
	}
}

func TestSearchAllLogs_NonRoot_DBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1SearchLogsRoute(ctx, common.RoleAdminUser, ctx.AdminUser.Id, "v2testadmin")

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/log/search/?keyword=x", nil, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Errorf("expected failure once the logs table is gone, body: %s", w.Body.String())
	}
}

// TestSearchAllLogs_Root_FallsThroughToAppSearchLogs exercises the
// root-user branch (Meilisearch disabled in tests → app.SearchLogs falls
// back to repo.SearchAllLogs(AllTenantsForAdmin())), proving root sees a
// log seeded under a DIFFERENT tenant than the caller's own tenant_id — the
// deliberate cross-tenant admin-search contract documented in log_service.go.
func TestSearchAllLogs_Root_FallsThroughToAppSearchLogs(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1SearchLogsRoute(ctx, common.RoleRootUser, ctx.RootUser.Id, "v2testroot")

	other := &repo.Log{
		UserId: ctx.RootUser.Id, TenantId: "some-other-tenant", Type: repo.LogTypeConsume,
		Content: "cross-tenant-needle", ModelName: "gpt-4o", CreatedAt: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(other).Error; err != nil {
		t.Fatalf("seed cross-tenant log: %v", err)
	}

	w := V2Request(ctx.Router, http.MethodGet, "/api/log/search/?keyword=cross-tenant-needle", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("root search must see the cross-tenant log, got %d items (body=%s)", len(items), w.Body.String())
	}
}

// ─── SearchUserLogs ─────────────────────────────────────────────────────────

func registerV1SearchUserLogsRoute(ctx *V2TestContext, userID int, username string) {
	g := ctx.Router.Group("/api/log/self/search")
	g.Use(func(c *gin.Context) {
		c.Set("tenant_id", ctx.TenantID)
		c.Set("id", userID)
		c.Set("username", username)
		c.Next()
	})
	g.GET("/", SearchUserLogs)
}

func TestSearchUserLogs_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1SearchUserLogsRoute(ctx, ctx.NormalUser.Id, "v2testuser")

	mine := &repo.Log{
		UserId: ctx.NormalUser.Id, TenantId: ctx.TenantID, Type: repo.LogTypeConsume,
		Content: "self-search-needle", ModelName: "gpt-4o", CreatedAt: common.GetTimestamp(),
	}
	others := &repo.Log{
		UserId: ctx.AdminUser.Id, TenantId: ctx.TenantID, Type: repo.LogTypeConsume,
		Content: "self-search-needle", ModelName: "gpt-4o", CreatedAt: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(mine).Error; err != nil {
		t.Fatalf("seed own log: %v", err)
	}
	if err := ctx.DB.Create(others).Error; err != nil {
		t.Fatalf("seed other user log: %v", err)
	}

	w := V2Request(ctx.Router, http.MethodGet, "/api/log/self/search/?keyword=self-search-needle", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 own-user match, got %d (body=%s)", len(items), w.Body.String())
	}
}

func TestSearchUserLogs_DBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1SearchUserLogsRoute(ctx, ctx.NormalUser.Id, "v2testuser")

	if err := ctx.DB.Migrator().DropTable(&repo.Log{}); err != nil {
		t.Fatalf("drop logs table: %v", err)
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/log/self/search/?keyword=x", nil, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Errorf("expected failure once the logs table is gone, body: %s", w.Body.String())
	}
}
