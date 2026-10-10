package router

// enterprise_tenant_role_redline_test.go — red line for users.tenant_role
// (migration 046): a customer's tenant admin (tenant_role="admin", global
// role 1) must be refused by every v1 AdminAuth route, while the SAME session
// is admitted to the tenant-admin-only v2 route. If tenant_role ever leaks
// into the integer role, the first assertion goes red.

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

var tenantRoleRedlineDBCounter atomic.Int64

func setupTenantRoleRedlineEngine(t *testing.T) *gin.Engine {
	t.Helper()
	return setupTenantRoleRedlineEngineIn(t, "acme")
}

// setupTenantRoleRedlineEngineIn seeds the tenant admin inside tenantID. The
// bootstrap tenant "default" never honours tenant_role, so the positive case
// must use a real customer tenant.
func setupTenantRoleRedlineEngineIn(t *testing.T, tenantID string) *gin.Engine {
	t.Helper()

	seq := tenantRoleRedlineDBCounter.Add(1)
	db, err := gorm.Open(sqlite.Open(
		fmt.Sprintf("file:ent_tr_redline_%d?mode=memory&cache=shared", seq)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Tenant{}, &repo.Channel{}, &repo.Log{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	now := time.Now()
	tenantIDs := []string{"default"}
	if tenantID != "default" {
		tenantIDs = append(tenantIDs, tenantID)
	}
	for _, id := range tenantIDs {
		if err := db.Create(&repo.Tenant{
			Id: id, Name: id, Slug: id, IDPOrgID: "org-" + id,
			Status: repo.TenantStatusEnabled, CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("create tenant %s: %v", id, err)
		}
	}
	admin := &repo.User{
		Id: 8600000 + int(seq), Username: fmt.Sprintf("ent-tr-%d", seq),
		DisplayName: "Tenant Admin", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("ent-tr-%d@local", seq),
		TenantId: tenantID, Quota: 1_000_000, TenantRole: entity.TenantRoleAdmin,
	}
	if err := db.Create(admin).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	prevGlobalAPI, prevGlobalV2 := common.GlobalApiRateLimitEnable, common.GlobalV2RateLimitEnable
	repo.DB, repo.LOG_DB = db, db
	repo.InitCol()
	common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = true, false, false
	common.GlobalApiRateLimitEnable, common.GlobalV2RateLimitEnable = false, false
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
		common.GlobalApiRateLimitEnable, common.GlobalV2RateLimitEnable = prevGlobalAPI, prevGlobalV2
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("ent-tr-redline-secret"))))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", admin.Username)
		s.Set("role", admin.Role)
		s.Set("id", admin.Id)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	SetApiRouter(engine)
	SetApiV2Router(engine)
	return engine
}

func tenantRoleRedlineCall(t *testing.T, engine *gin.Engine, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "10.42.0.1:54321"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: not JSON (status %d): %q", path, w.Code, w.Body.String())
	}
	return w.Code, body
}

func TestTenantRoleAdmin_RefusedByV1AdminAuth(t *testing.T) {
	engine := setupTenantRoleRedlineEngine(t)
	for _, path := range []string{"/api/channel/", "/api/user/"} {
		status, body := tenantRoleRedlineCall(t, engine, path)
		if success, _ := body["success"].(bool); success {
			t.Errorf("GET %s admitted a tenant_role=admin, role=1 session (status %d, body %v) — tenant_role leaked into the global role",
				path, status, body)
		}
	}
}

func TestTenantRoleAdmin_AdmittedToTenantAdminV2Route(t *testing.T) {
	engine := setupTenantRoleRedlineEngine(t)
	status, body := tenantRoleRedlineCall(t, engine, "/api/v2/acme/logs/all")
	if status != http.StatusOK {
		t.Fatalf("GET /logs/all for the tenant admin = %d (%v), want 200 — the tenant_role never reached TenantContext", status, body)
	}
	if success, _ := body["success"].(bool); !success {
		t.Fatalf("success=false: %v", body)
	}
}

// A role stamped on a user of the bootstrap "default" tenant is never honoured.
func TestTenantRoleAdmin_InDefaultTenantRefusedByV2Route(t *testing.T) {
	engine := setupTenantRoleRedlineEngineIn(t, "default")
	status, body := tenantRoleRedlineCall(t, engine, "/api/v2/default/logs/all")
	if status != http.StatusForbidden {
		t.Fatalf("GET /logs/all for a default-tenant tenant_role=admin = %d (%v), want 403", status, body)
	}
}
