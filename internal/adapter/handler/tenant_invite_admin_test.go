package handler

// tenant_invite_admin_test.go — coverage for the root-admin issue/revoke
// endpoints (N2): IssueTenantInvite / RevokeTenantInvite (tenant_invite.go).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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

var adminInviteTestDBCounter atomic.Int64

type adminInviteCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	tenantID string
	actorID  int
}

// setupAdminInviteRouter wires IssueTenantInvite/RevokeTenantInvite against
// an isolated in-memory SQLite DB with a "id" injected into context
// (mimicking RootJWTAuth's session-based actor injection — same pattern as
// setupAdminPoolRouter).
func setupAdminInviteRouter(t *testing.T) *adminInviteCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:admininvite%d?mode=memory&cache=shared", adminInviteTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.Tenant{}, &repo.TenantInvite{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
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

	now := time.Now()
	tenantID := "tenant-inviteadmin"
	if err := db.Create(&repo.Tenant{
		Id: tenantID, Name: "InviteAdmin", Slug: "inviteadmin",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_ia",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	const actorID = 7001
	router := gin.New()
	mockAuth := func(c *gin.Context) {
		c.Set("id", actorID)
		c.Next()
	}
	g := router.Group("/api/v2/admin/tenants/:id/invites", mockAuth)
	g.POST("", IssueTenantInvite)
	g.GET("", ListTenantInvites)
	g.DELETE("/:invite_id", RevokeTenantInvite)

	t.Cleanup(func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	return &adminInviteCtx{router: router, db: db, tenantID: tenantID, actorID: actorID}
}

func (ctx *adminInviteCtx) do(method, path string, body interface{}) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// TestIssueTenantInvite_HappyPath: 201, a real 32-char code, tenant-bound,
// and persisted pending.
func TestIssueTenantInvite_HappyPath(t *testing.T) {
	ctx := setupAdminInviteRouter(t)

	w := ctx.do(http.MethodPost, "/api/v2/admin/tenants/"+ctx.tenantID+"/invites",
		map[string]any{"ttl_hours": 24})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T body=%s", resp["data"], w.Body.String())
	}
	code, _ := data["code"].(string)
	if len(code) != 32 {
		t.Errorf("code = %q, want a 32-char code", code)
	}

	var persisted repo.TenantInvite
	if err := ctx.db.Where("code = ?", code).First(&persisted).Error; err != nil {
		t.Fatalf("readback: %v", err)
	}
	if persisted.TenantId != ctx.tenantID {
		t.Errorf("TenantId = %q, want %q", persisted.TenantId, ctx.tenantID)
	}
	if persisted.Status != repo.TenantInviteStatusPending {
		t.Errorf("Status = %d, want pending(%d)", persisted.Status, repo.TenantInviteStatusPending)
	}
	if persisted.CreatedByUserId != ctx.actorID {
		t.Errorf("CreatedByUserId = %d, want %d", persisted.CreatedByUserId, ctx.actorID)
	}
}

// TestIssueTenantInvite_UnknownTenant_404 covers the tenant-existence guard.
func TestIssueTenantInvite_UnknownTenant_404(t *testing.T) {
	ctx := setupAdminInviteRouter(t)

	w := ctx.do(http.MethodPost, "/api/v2/admin/tenants/no-such-tenant/invites", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestRevokeTenantInvite_HappyPath_MakesCodeUnusable revokes a pending
// invite and proves the revoked code can no longer be consumed.
func TestRevokeTenantInvite_HappyPath_MakesCodeUnusable(t *testing.T) {
	ctx := setupAdminInviteRouter(t)

	invite, err := repo.CreateTenantInvite(ctx.tenantID, ctx.actorID, 0)
	if err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	w := ctx.do(http.MethodDelete,
		"/api/v2/admin/tenants/"+ctx.tenantID+"/invites/"+strconv.Itoa(invite.Id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	if _, err := repo.ConsumeTenantInvite(invite.Code, 1); err == nil {
		t.Error("revoked invite must not be consumable")
	}
}

// TestRevokeTenantInvite_UnknownID_404 covers the not-found path (also
// exercised for cross-tenant ids at the repo layer).
func TestRevokeTenantInvite_UnknownID_404(t *testing.T) {
	ctx := setupAdminInviteRouter(t)

	w := ctx.do(http.MethodDelete,
		"/api/v2/admin/tenants/"+ctx.tenantID+"/invites/999999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestListTenantInvites_NeverReturnsFullCode is the L6 oracle: the issue
// response carries the real code, but the list body must not — no "code"
// key anywhere in it, the raw code string absent from the body, and
// code_prefix exactly the code's first 8 characters. Projecting inv.Code
// instead of a prefix in toTenantInviteView makes this go red.
func TestListTenantInvites_NeverReturnsFullCode(t *testing.T) {
	ctx := setupAdminInviteRouter(t)

	issueW := ctx.do(http.MethodPost, "/api/v2/admin/tenants/"+ctx.tenantID+"/invites",
		map[string]any{"ttl_hours": 24})
	if issueW.Code != http.StatusCreated {
		t.Fatalf("issue status = %d, want 201, body=%s", issueW.Code, issueW.Body.String())
	}
	issueResp := ParseV2Response(t, issueW)
	issueData, _ := issueResp["data"].(map[string]interface{})
	code, _ := issueData["code"].(string)
	if len(code) != 32 {
		t.Fatalf("issued code = %q, want a 32-char code", code)
	}

	listW := ctx.do(http.MethodGet, "/api/v2/admin/tenants/"+ctx.tenantID+"/invites", nil)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200, body=%s", listW.Code, listW.Body.String())
	}
	body := listW.Body.String()
	if strings.Contains(body, code) {
		t.Fatalf("list body contains the full code %q: %s", code, body)
	}
	if strings.Contains(body, `"code"`) {
		t.Fatalf("list body has a \"code\" key: %s", body)
	}

	listResp := ParseV2Response(t, listW)
	listData, ok := listResp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T body=%s", listResp["data"], body)
	}
	invites, ok := listData["invites"].([]interface{})
	if !ok || len(invites) != 1 {
		t.Fatalf("invites = %v, want exactly 1 row", listData["invites"])
	}
	row, ok := invites[0].(map[string]interface{})
	if !ok {
		t.Fatalf("row is not a map: %T", invites[0])
	}
	prefix, _ := row["code_prefix"].(string)
	if prefix != code[:8] {
		t.Errorf("code_prefix = %q, want %q", prefix, code[:8])
	}
}

// TestListTenantInvites_ShowsStatusTransitions: a revoked invite lists
// status 3; consuming a second, separately-issued invite lists status 2
// with the consuming account id recorded.
func TestListTenantInvites_ShowsStatusTransitions(t *testing.T) {
	ctx := setupAdminInviteRouter(t)

	revoked, err := repo.CreateTenantInvite(ctx.tenantID, ctx.actorID, 0)
	if err != nil {
		t.Fatalf("seed revoked invite: %v", err)
	}
	if err := repo.RevokeTenantInvite(revoked.Id, ctx.tenantID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	consumed, err := repo.CreateTenantInvite(ctx.tenantID, ctx.actorID, 0)
	if err != nil {
		t.Fatalf("seed consumed invite: %v", err)
	}
	if _, err := repo.ConsumeTenantInvite(consumed.Code, 4242); err != nil {
		t.Fatalf("consume: %v", err)
	}

	listW := ctx.do(http.MethodGet, "/api/v2/admin/tenants/"+ctx.tenantID+"/invites", nil)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200, body=%s", listW.Code, listW.Body.String())
	}
	listResp := ParseV2Response(t, listW)
	listData := listResp["data"].(map[string]interface{})
	invites := listData["invites"].([]interface{})
	if len(invites) != 2 {
		t.Fatalf("invites = %d rows, want 2", len(invites))
	}

	byID := map[float64]map[string]interface{}{}
	for _, raw := range invites {
		row := raw.(map[string]interface{})
		byID[row["id"].(float64)] = row
	}

	revokedRow, ok := byID[float64(revoked.Id)]
	if !ok {
		t.Fatalf("revoked invite id %d missing from list", revoked.Id)
	}
	if status := revokedRow["status"].(float64); status != float64(repo.TenantInviteStatusRevoked) {
		t.Errorf("revoked row status = %v, want %d", status, repo.TenantInviteStatusRevoked)
	}

	consumedRow, ok := byID[float64(consumed.Id)]
	if !ok {
		t.Fatalf("consumed invite id %d missing from list", consumed.Id)
	}
	if status := consumedRow["status"].(float64); status != float64(repo.TenantInviteStatusConsumed) {
		t.Errorf("consumed row status = %v, want %d", status, repo.TenantInviteStatusConsumed)
	}
	if consumer := consumedRow["consumed_by_account_id"].(float64); consumer != 4242 {
		t.Errorf("consumed row consumed_by_account_id = %v, want 4242", consumer)
	}
}

// TestListTenantInvites_UnknownTenant_404 covers the tenant-existence guard
// (same shape as IssueTenantInvite's).
func TestListTenantInvites_UnknownTenant_404(t *testing.T) {
	ctx := setupAdminInviteRouter(t)

	w := ctx.do(http.MethodGet, "/api/v2/admin/tenants/no-such-tenant/invites", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestListTenantInvites_PageSizeClamped pins the pagination bound: an
// oversized page_size is clamped to 20 and a sub-1 page is clamped to 1.
// Deleting the clamp block in ListTenantInvites left the package green
// before this test existed (measured) — a caller could otherwise ask for
// page_size=100000 with no test noticing.
func TestListTenantInvites_PageSizeClamped(t *testing.T) {
	ctx := setupAdminInviteRouter(t)

	w := ctx.do(http.MethodGet,
		"/api/v2/admin/tenants/"+ctx.tenantID+"/invites?page_size=100000&page=0", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T body=%s", resp["data"], w.Body.String())
	}
	if pageSize := data["page_size"].(float64); pageSize != 20 {
		t.Errorf("page_size = %v, want clamped to 20", pageSize)
	}
	if page := data["page"].(float64); page != 1 {
		t.Errorf("page = %v, want clamped to 1", page)
	}
}

// TestTenantInviteAdmin_UnauthenticatedRejected mounts the PRODUCTION
// middleware.RootJWTAuth (same wiring as api-v2-router.go's adminRoute) and
// verifies an anonymous request never reaches either handler — the admin
// authz gate on the issue/revoke endpoints.
func TestTenantInviteAdmin_UnauthenticatedRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	store := cookie.NewStore([]byte("tenant-invite-admin-auth-test-secret"))
	router.Use(sessions.Sessions("session", store))
	admin := router.Group("/api/v2/admin")
	admin.Use(middleware.RootJWTAuth())
	tenantMgmt := admin.Group("/tenants")
	{
		tenantMgmt.POST("/:id/invites", IssueTenantInvite)
		tenantMgmt.GET("/:id/invites", ListTenantInvites)
		tenantMgmt.DELETE("/:id/invites/:invite_id", RevokeTenantInvite)
	}

	cases := []struct{ method, path string }{
		{http.MethodPost, "/api/v2/admin/tenants/some-tenant/invites"},
		{http.MethodGet, "/api/v2/admin/tenants/some-tenant/invites"},
		{http.MethodDelete, "/api/v2/admin/tenants/some-tenant/invites/1"},
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
