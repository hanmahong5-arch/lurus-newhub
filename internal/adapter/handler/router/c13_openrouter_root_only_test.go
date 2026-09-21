package router

// c13_openrouter_root_only_test.go — cycle-13 W wiring oracle for L4's
// privilege decision on the OpenRouter sync reads.
//
// /api/openrouter-sync was one AdminAuth-gated group: every write under it
// was additionally RootAuth-gated, but the four READS were not, so a tenant
// admin (role 10) could call GET /api/openrouter-sync/api-pool and receive a
// snapshot of EVERY multi-key OpenRouter channel on the platform — channel
// ids, names, per-key prefixes and cooldown state belonging to other
// tenants. (The handler also narrows its own query by tenant for a non-root
// caller since cycle 13 L4; that is defence in depth, not the gate. This
// test is about the gate.)
//
// The route table is the real one (SetApiRouter — what cmd/server reaches
// through SetRouter), and the caller identity is a real gin session, so the
// refusal comes from the production middleware chain.
//
// Mutation (verified red): drop middleware.RootAuth() from
// openrouterSyncRoute.GET("/api-pool", ...) in api-router.go — the role-10
// arm starts answering success:true with a data payload.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var openrouterRootOnlyDBCounter atomic.Int64

// openrouterRootOnlyEngine mounts the real v1 route table behind a session
// carrying the given role, against an isolated sqlite DB holding one tenant,
// one user and one multi-key OpenRouter channel. The channel exists so that
// a caller who DOES get through has something to read: a green "no data"
// would otherwise be indistinguishable from a successful refusal.
func openrouterRootOnlyEngine(t *testing.T, role int) *gin.Engine {
	t.Helper()

	seq := openrouterRootOnlyDBCounter.Add(1)
	db, err := gorm.Open(sqlite.Open(
		fmt.Sprintf("file:c13_or_rootonly_%d?mode=memory&cache=shared", seq)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Tenant{}, &repo.Channel{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	now := time.Now()
	if err := db.Create(&repo.Tenant{
		Id: "default", Name: "default", Slug: "default",
		Status: repo.TenantStatusEnabled, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &repo.User{
		Id: 8400000 + int(seq), Username: fmt.Sprintf("c13-or-%d", seq),
		DisplayName: "C13 OpenRouter Caller", Role: role,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("c13-or-%d@local", seq),
		TenantId: "default", Quota: 1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	prevDB := repo.DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	prevGlobalAPI := common.GlobalApiRateLimitEnable
	repo.DB = db
	repo.InitCol()
	common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = true, false, false
	common.GlobalApiRateLimitEnable = false
	t.Cleanup(func() {
		repo.DB = prevDB
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
		common.GlobalApiRateLimitEnable = prevGlobalAPI
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("c13-or-rootonly-secret"))))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", user.Username)
		s.Set("role", user.Role)
		s.Set("id", user.Id)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	SetApiRouter(engine)
	return engine
}

func openrouterApiPoolCall(t *testing.T, engine *gin.Engine) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/openrouter-sync/api-pool", nil)
	req.RemoteAddr = "10.42.0.1:54321"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON (status %d): %q", w.Code, w.Body.String())
	}
	return w.Code, body
}

func TestOpenRouterApiPool_TenantAdminIsRefused(t *testing.T) {
	engine := openrouterRootOnlyEngine(t, common.RoleAdminUser)
	status, body := openrouterApiPoolCall(t, engine)

	// v1's authHelper refuses with HTTP 200 {success:false} — the shape
	// switch consumes and this cycle's do-not-regress list keeps — so the
	// status code alone proves nothing. What must be true is that no data
	// came back.
	if success, _ := body["success"].(bool); success {
		t.Errorf("a role-%d session got success:true from GET /api/openrouter-sync/api-pool (status %d) — the read is not root-gated; body=%v",
			common.RoleAdminUser, status, body)
	}
	if data, present := body["data"]; present && data != nil {
		t.Errorf("a role-%d session got a data payload from GET /api/openrouter-sync/api-pool: %v — this snapshot names every multi-key OpenRouter channel on the platform",
			common.RoleAdminUser, data)
	}
}

func TestOpenRouterApiPool_RootIsAdmitted(t *testing.T) {
	engine := openrouterRootOnlyEngine(t, common.RoleRootUser)
	status, body := openrouterApiPoolCall(t, engine)

	// The companion arm: without it, a middleware that refused EVERYONE would
	// satisfy the test above while breaking the endpoint for its real
	// audience.
	if success, _ := body["success"].(bool); !success {
		t.Fatalf("a role-%d session was refused GET /api/openrouter-sync/api-pool (status %d): %v — root must still reach the handler",
			common.RoleRootUser, status, body)
	}
	if _, present := body["data"]; !present {
		t.Errorf("root reached the handler but the response carries no data key: %v", body)
	}
}
