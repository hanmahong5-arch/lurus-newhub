package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// permissionDenied is the exact message authHelper emits when a session's role
// is below the route's required minimum (auth.go). The seal is proven by which
// routes produce it for a role-10 (tenant admin) session and which do not.
const permissionDenied = "权限不足"

var sealDBCounter atomic.Int64

// sealUser is a session identity (matches a real repo.User row so authHelper's
// GetUserCache/tenant-resolution path runs against the same queries production
// uses, not a mock).
type sealUser struct {
	id       int
	username string
	role     int
}

// setupSealRouter wires an isolated in-memory DB with a tenant-admin (role 10)
// and an operator (root, role 100) user, then returns a builder that serves a
// request through the REAL SetApiRouter with the chosen identity injected into
// the gin session — so the assertions run against the production middleware
// chain (group AdminAuth + inline RootAuth), not a reconstructed mimic.
func setupSealRouter(t *testing.T) (admin, root sealUser, serve func(u sealUser, method, path string) *httptest.ResponseRecorder, cleanup func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:seal_test_%d?mode=memory&cache=shared", sealDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &entity.Tenant{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevRedis := common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.RedisEnabled = false

	adminRow := &repo.User{Username: "seal_admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Email: "seal_admin@local", TenantId: "default"}
	rootRow := &repo.User{Username: "seal_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Email: "seal_root@local", TenantId: "default"}
	if err := db.Create(adminRow).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := db.Create(rootRow).Error; err != nil {
		t.Fatalf("create root: %v", err)
	}
	admin = sealUser{id: adminRow.Id, username: adminRow.Username, role: adminRow.Role}
	root = sealUser{id: rootRow.Id, username: rootRow.Username, role: rootRow.Role}

	serve = func(u sealUser, method, path string) *httptest.ResponseRecorder {
		engine := gin.New()
		engine.Use(gin.Recovery())
		store := cookie.NewStore([]byte("seal-test-secret"))
		engine.Use(sessions.Sessions("session", store))
		engine.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("username", u.username)
			s.Set("role", u.role)
			s.Set("id", u.id)
			s.Set("status", common.UserStatusEnabled)
			_ = s.Save()
			c.Next()
		})
		SetApiRouter(engine)

		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	cleanup = func() {
		repo.DB = prevDB
		common.RedisEnabled = prevRedis
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
	return admin, root, serve, cleanup
}

// TestRootSeal_GlobalConfigWritesRequireRoot is the regression that closes the
// v1 privilege-overload class: a role-10 tenant admin must NOT be able to mutate
// GLOBAL (cross-tenant) config through the legacy /api console. Each sealed
// route rejects the tenant admin with "权限不足" while catalog READS stay open
// to admins (tenant consoles read them to populate pickers), and the operator
// (root) sails past the gate on every route.
func TestRootSeal_GlobalConfigWritesRequireRoot(t *testing.T) {
	admin, root, serve, cleanup := setupSealRouter(t)
	defer cleanup()

	// Global-config MUTATIONS + the whole api-keys surface → operator only.
	sealed := []struct{ method, path string }{
		{http.MethodPost, "/api/vendors/"},
		{http.MethodPut, "/api/vendors/"},
		{http.MethodDelete, "/api/vendors/1"},
		{http.MethodPost, "/api/models/"},
		{http.MethodPut, "/api/models/"},
		{http.MethodDelete, "/api/models/1"},
		{http.MethodPost, "/api/models/sync_upstream"},
		{http.MethodPost, "/api/models/sync_channels"},
		{http.MethodPost, "/api/deployments/"},
		{http.MethodPut, "/api/deployments/1"},
		{http.MethodDelete, "/api/deployments/1"},
		{http.MethodPost, "/api/openrouter-sync/jobs"},
		{http.MethodPut, "/api/openrouter-sync/jobs/1"},
		{http.MethodDelete, "/api/openrouter-sync/jobs/1"},
		{http.MethodPost, "/api/openrouter-sync/run-all"},
		{http.MethodPost, "/api/prefill_group/"},
		{http.MethodPut, "/api/prefill_group/"},
		{http.MethodDelete, "/api/prefill_group/1"},
		{http.MethodGet, "/api/api-keys/"},    // whole api-keys group is root-only
		{http.MethodPost, "/api/api-keys/"},
	}
	for _, r := range sealed {
		wAdmin := serve(admin, r.method, r.path)
		if !strings.Contains(wAdmin.Body.String(), permissionDenied) {
			t.Errorf("SEAL LEAK: role-10 admin was NOT blocked on %s %s (body=%q)", r.method, r.path, wAdmin.Body.String())
		}
		wRoot := serve(root, r.method, r.path)
		if strings.Contains(wRoot.Body.String(), permissionDenied) {
			t.Errorf("root operator wrongly blocked on %s %s (body=%q)", r.method, r.path, wRoot.Body.String())
		}
	}

	// Catalog READS stay admin-accessible — a role-10 tenant admin must still be
	// able to enumerate vendors/models to configure their own channels/pricing.
	// (These hit real handlers; we only assert the auth layer did NOT deny.)
	catalogReads := []struct{ method, path string }{
		{http.MethodGet, "/api/vendors/"},
		{http.MethodGet, "/api/models/"},
		{http.MethodGet, "/api/deployments/"},
		{http.MethodGet, "/api/prefill_group/"},
	}
	for _, r := range catalogReads {
		w := serve(admin, r.method, r.path)
		if strings.Contains(w.Body.String(), permissionDenied) {
			t.Errorf("REGRESSION: role-10 admin blocked from catalog read %s %s (body=%q)", r.method, r.path, w.Body.String())
		}
	}
}
