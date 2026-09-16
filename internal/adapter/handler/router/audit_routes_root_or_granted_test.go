package router

// audit_routes_root_or_granted_test.go — L4 (auth-security-17/18,
// console-ux-36). Proves the REAL production route table
// (SetApiV2Router), not a hand-mounted mimic, gates ALL FOUR audit-feed
// GETs (events, actions, export, chain-verify — cycle-8 L4 repair round,
// A-F1/B-F10: the original version of this test drove /events only, so a
// mutation moving just /export back under RootJWTAuth stayed undetected)
// behind middleware.RootOrGranted instead of RootJWTAuth: a
// session-authenticated admin holding an active audit:read grant reaches
// all four, and — the negative that proves the grant does NOT leak into
// the rest of the admin subtree — the SAME session is still refused on GET
// /api/v2/admin/tenants/:id (RootJWTAuth-gated, unrelated to any grant).
// Mutation target (lane spec): registering the four audit GETs back under
// adminRoute turns this red.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var auditRootOrGrantedDBCounter atomic.Int64

func mountRealRouterWithSession(t *testing.T, sessionValues map[string]interface{}) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:auditrootorgranted%d?mode=memory&cache=shared", auditRootOrGrantedDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &entity.AdminPermissionGrant{}, &entity.AuditEvent{}, &entity.Tenant{}} {
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
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		common.LogConsumeEnabled = prevLogConsume
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	engine.Use(gin.Recovery())
	store := cookie.NewStore([]byte("audit-root-or-granted-test-secret"))
	engine.Use(sessions.Sessions("session", store))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range sessionValues {
			s.Set(k, v)
		}
		_ = s.Save()
		c.Next()
	})
	SetApiV2Router(engine)
	return engine
}

// auditFeedGETs is all four routes moved onto auditRoute by this lane. Every
// one of them must be independently proven — the original single-route
// version of this test left /actions, /export and /chain-verify able to be
// silently re-registered under adminRoute (RootJWTAuth) without any test
// going red (A-F1/B-F10).
var auditFeedGETs = []string{
	"/api/v2/admin/audit/events",
	"/api/v2/admin/audit/actions",
	"/api/v2/admin/audit/export?format=json",
	"/api/v2/admin/audit/chain-verify",
}

func TestAuditRoutes_MountedUnderRootOrGranted(t *testing.T) {
	adminID := 5
	sessionValues := map[string]interface{}{
		"username": "delegated-admin",
		"role":     common.RoleAdminUser,
		"id":       adminID,
		"status":   common.UserStatusEnabled,
	}
	engine := mountRealRouterWithSession(t, sessionValues)

	// No grant yet: the real router rejects every one of the four with
	// PERMISSION_DENIED.
	for _, path := range auditFeedGETs {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("GET %s with no grant: status=%d, want 403; body=%s", path, w.Code, w.Body.String())
		}
		var resp struct {
			Success   bool   `json:"success"`
			ErrorCode string `json:"error_code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("GET %s with no grant: unmarshal body: %v", path, err)
		}
		if resp.Success || resp.ErrorCode != "PERMISSION_DENIED" {
			t.Fatalf("GET %s with no grant: body=%s, want success:false error_code:PERMISSION_DENIED", path, w.Body.String())
		}
	}

	if _, _, err := repo.CreatePermissionGrant(adminID, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	// With the grant, the real router reaches all four handlers.
	for _, path := range auditFeedGETs {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("GET %s with an active audit:read grant: status=%d, want 2xx; body=%s", path, w.Code, w.Body.String())
		}
		var resp struct {
			Success bool `json:"success"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("GET %s with an active audit:read grant: unmarshal body: %v", path, err)
		}
		if !resp.Success {
			t.Fatalf("GET %s with an active audit:read grant: body=%s, want success:true", path, w.Body.String())
		}
	}

	// The negative that proves this is the load-bearing property: the SAME
	// session, same grant, must NOT unlock an unrelated RootJWTAuth-gated
	// route (adminRoute's /tenants/:id) — a grant scoped to auditRoute must
	// never leak into the rest of the admin subtree.
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/tenants/some-tenant", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	var tenantsResp struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tenantsResp)
	if tenantsResp.Success {
		t.Fatalf("GET /admin/tenants/:id with only an audit:read grant: body=%s, want success:false (no leak)", w.Body.String())
	}
}

// TestAuditRoutes_ExpiredGrantRejected is the REAL-CHAIN oracle for the
// cycle-9 L1 repair ruling (R3, A-4/B-3): an expired grant does not
// authorise the route it was granted for, proved through the REAL
// production route table (SetApiV2Router), not a hand-mounted router. The
// row is seeded with revoked_at NULL / expires_at in the past — never
// explicitly revoked — so the only thing standing between this session and
// the audit feed is the expiry predicate in
// repo.HasActivePermissionGrant's WHERE clause.
func TestAuditRoutes_ExpiredGrantRejected(t *testing.T) {
	adminID := 6
	sessionValues := map[string]interface{}{
		"username": "delegated-admin-expired",
		"role":     common.RoleAdminUser,
		"id":       adminID,
		"status":   common.UserStatusEnabled,
	}
	engine := mountRealRouterWithSession(t, sessionValues)

	past := common.GetTimestamp() - 60
	if err := repo.DB.Create(&entity.AdminPermissionGrant{
		UserId: adminID, Resource: "audit", Action: "read", GrantedBy: 1,
		CreatedAt: common.GetTimestamp() - 120, ExpiresAt: &past,
	}).Error; err != nil {
		t.Fatalf("seed expired grant: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit/events", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("GET /api/v2/admin/audit/events with an expired (never revoked) grant: status=%d, want 403; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if resp.Success || resp.ErrorCode != "PERMISSION_DENIED" {
		t.Fatalf("body=%s, want success:false error_code:PERMISSION_DENIED", w.Body.String())
	}
}
