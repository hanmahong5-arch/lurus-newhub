package router

// v2_admin_system_tasks_mount_test.go — L3 repair round (A-F7, REAL-CHAIN
// RULE). handler.v2_admin_system_tasks_test.go's TestSystemTasks_RootOnly
// hand-mounts middleware.RootJWTAuth on a private gin engine, mirroring
// api-v2-router.go's adminRoute wiring — it does NOT prove GET
// /api/v2/admin/system/tasks is actually gated that way on the route table
// production serves. This file enters through the real SetApiV2Router, the
// same convention v2_admin_security_wiring_test.go and
// r6d_switch_redeem_mount_test.go use for their own routes:
//   - anonymous (no session) -> 401, never reaching GetSystemTasksV2
//   - a seeded root (role 100) session -> 200 with a tasks array
//   - a seeded admin (role 10, NOT root) session -> the real refusal shape,
//     HTTP 200 {"success":false} (middleware/auth.go's authHelper — this
//     codebase's minRole convention is NOT a 403)
//
// No DB row is seeded for the session user: authHelper's DB/cache lookup
// misses (record not found) and falls back to the session's own role/status
// — the same fail-open convention
// handler.setupSystemTasksRootRouter's doc comment documents and relies on.
//
// Mutation target: removing `adminRoute.GET("/system/tasks",
// handler.GetSystemTasksV2)` from api-v2-router.go, or moving it outside
// adminRoute, turns the anonymous_401 and admin_role_refused subtests red.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var systemTasksMountDBCounter atomic.Int64

// systemTasksMountRouter builds the real SetApiV2Router engine over an
// isolated hermetic DB, with an optional session-seeding middleware ahead
// of it (nil = anonymous). Mirrors v2_admin_security_wiring_test.go's setup.
func systemTasksMountRouter(t *testing.T, role int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:systasksmount%d?mode=memory&cache=shared", systemTasksMountDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
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
	store := cookie.NewStore([]byte("system-tasks-mount-test-secret"))
	engine.Use(sessions.Sessions("session", store))
	if role >= 0 {
		engine.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("username", "systasks_mount_actor")
			s.Set("role", role)
			s.Set("id", 1)
			s.Set("status", common.UserStatusEnabled)
			_ = s.Save()
			c.Next()
		})
	}
	SetApiV2Router(engine)
	return engine
}

func doSystemTasksMountGET(engine *gin.Engine) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/system/tasks", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func TestSetApiV2Router_SystemTasks_MountedAndGated(t *testing.T) {
	t.Run("anonymous_401", func(t *testing.T) {
		engine := systemTasksMountRouter(t, -1) // -1 = no session middleware
		w := doSystemTasksMountGET(engine)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous GET /api/v2/admin/system/tasks via real router: status=%d, want 401; body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("admin_role_refused", func(t *testing.T) {
		engine := systemTasksMountRouter(t, common.RoleAdminUser)
		w := doSystemTasksMountGET(engine)
		if w.Code != http.StatusOK {
			t.Fatalf("admin (role=10) via real router: status=%d, want 200 (this codebase's minRole refusal is success:false, not an HTTP error); body=%s", w.Code, w.Body.String())
		}
		var env map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
		}
		if success, _ := env["success"].(bool); success {
			t.Fatalf("admin (role=10) session via real router: success=true, want false (root-only route); body=%s", w.Body.String())
		}
		if _, hasData := env["data"]; hasData {
			t.Errorf("admin (role=10) session must not reach GetSystemTasksV2 at all — body=%s", w.Body.String())
		}
	})

	t.Run("root_role_allowed", func(t *testing.T) {
		engine := systemTasksMountRouter(t, common.RoleRootUser)
		w := doSystemTasksMountGET(engine)
		if w.Code != http.StatusOK {
			t.Fatalf("root (role=100) via real router: status=%d, want 200; body=%s", w.Code, w.Body.String())
		}
		var env map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
		}
		if success, _ := env["success"].(bool); !success {
			t.Fatalf("root session via real router: success=false, want true; body=%s", w.Body.String())
		}
		data, ok := env["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("root session via real router: missing data object; body=%s", w.Body.String())
		}
		if _, ok := data["tasks"].([]interface{}); !ok {
			t.Errorf("root session via real router: data.tasks is not an array; body=%s", w.Body.String())
		}
	})
}
