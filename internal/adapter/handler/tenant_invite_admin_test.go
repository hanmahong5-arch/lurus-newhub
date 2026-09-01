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
		tenantMgmt.DELETE("/:id/invites/:invite_id", RevokeTenantInvite)
	}

	cases := []struct{ method, path string }{
		{http.MethodPost, "/api/v2/admin/tenants/some-tenant/invites"},
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
