package router

// task_router_test.go — wiring lock for cycle-8 L8: proves SetTaskRouter (the
// function cmd/server/main.go reaches via SetRouter) actually registers
// POST /v1/tasks/:platform and GET /v1/tasks/:platform/:task_id, and that
// TokenAuth + TaskPlatformGuard are really mounted on both — not merely
// present in task_generic.go — by driving requests through the REAL router
// (REAL-CHAIN RULE), mirroring r6a_rate_limit_mount_test.go's
// seed-a-real-token-then-hit-SetXRouter convention.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var taskRouterMountDBCounter atomic.Int64

// taskRouterSeedToken mirrors r6a_rate_limit_mount_test.go's
// r6aSeedRateLimitToken: an isolated in-memory SQLite DB with one enabled
// user + unlimited-quota token, wired into repo.DB.
func taskRouterSeedToken(t *testing.T) (key string, cleanup func()) {
	t.Helper()
	seq := taskRouterMountDBCounter.Add(1)

	dbName := fmt.Sprintf("file:task_router_mount_%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Token{}, &repo.Task{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	user := &repo.User{
		Id:       7710031 + int(seq),
		Username: fmt.Sprintf("task-router-user-%d", seq), DisplayName: "Task Router User",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: fmt.Sprintf("task-router-%d@local", seq), TenantId: "default", Quota: 1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tokenKey := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: tokenKey, Status: common.TokenStatusEnabled,
		Name: "task-router-token", CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, UnlimitedQuota: true,
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	prevMemCache := common.MemoryCacheEnabled
	repo.DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false

	return tokenKey, func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		common.MemoryCacheEnabled = prevMemCache
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

func taskRouterEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetTaskRouter(engine)
	return engine
}

// TestTaskRouter_GenericRoutesMountedWithTaskChain is the oracle: routes
// exist in the real table, and unauthenticated + unknown-platform requests
// are rejected by the REAL router, not a hand-copy of it.
func TestTaskRouter_GenericRoutesMountedWithTaskChain(t *testing.T) {
	key, cleanup := taskRouterSeedToken(t)
	defer cleanup()

	engine := taskRouterEngine()

	routes := engine.Routes()
	if len(routes) == 0 {
		t.Fatal("SetTaskRouter registered nothing — this test would pass vacuously")
	}
	want := map[string]bool{
		"POST /v1/tasks/:platform":         false,
		"GET /v1/tasks/:platform/:task_id": false,
	}
	for _, rt := range routes {
		routeKey := rt.Method + " " + rt.Path
		if _, ok := want[routeKey]; ok {
			want[routeKey] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("route %q not registered; got routes: %+v", k, routes)
		}
	}

	// No Authorization header at all: TokenAuth must reject before
	// TaskPlatformGuard, Distribute or the handler ever run.
	unauthedPost := httptest.NewRequest(http.MethodPost, "/v1/tasks/suno", nil)
	wPost := httptest.NewRecorder()
	engine.ServeHTTP(wPost, unauthedPost)
	if wPost.Code != http.StatusUnauthorized {
		t.Errorf("POST unauthenticated status = %d, want 401; body=%s", wPost.Code, wPost.Body.String())
	}

	unauthedGet := httptest.NewRequest(http.MethodGet, "/v1/tasks/suno/some-task-id", nil)
	wGet := httptest.NewRecorder()
	engine.ServeHTTP(wGet, unauthedGet)
	if wGet.Code != http.StatusUnauthorized {
		t.Errorf("GET unauthenticated status = %d, want 401; body=%s", wGet.Code, wGet.Body.String())
	}

	// Authenticated but with an unknown platform: TaskPlatformGuard must
	// reject with the new error code, proving it runs (after TokenAuth,
	// before any billing-side middleware) on the real chain.
	unknownReq := httptest.NewRequest(http.MethodGet, "/v1/tasks/not-a-real-platform/xyz", nil)
	unknownReq.Header.Set("Authorization", "Bearer "+key)
	wUnknown := httptest.NewRecorder()
	engine.ServeHTTP(wUnknown, unknownReq)
	if wUnknown.Code != http.StatusNotFound {
		t.Errorf("authenticated unknown-platform status = %d, want 404; body=%s", wUnknown.Code, wUnknown.Body.String())
	}
	if !strings.Contains(wUnknown.Body.String(), "task_platform_unknown") {
		t.Errorf("body = %s, want it to contain task_platform_unknown", wUnknown.Body.String())
	}

	// Authenticated, known platform, GET for an absent task: reaches
	// GetTaskGeneric (past TokenAuth + TaskPlatformGuard + the spend-side
	// middlewares, all of which fail open with no pool/rate-limit config)
	// and answers the fail-closed 404 body, not a 401/403/500.
	knownReq := httptest.NewRequest(http.MethodGet, "/v1/tasks/suno/does-not-exist", nil)
	knownReq.Header.Set("Authorization", "Bearer "+key)
	wKnown := httptest.NewRecorder()
	engine.ServeHTTP(wKnown, knownReq)
	if wKnown.Code != http.StatusNotFound {
		t.Errorf("authenticated known-platform absent-task status = %d, want 404; body=%s", wKnown.Code, wKnown.Body.String())
	}
	if !strings.Contains(wKnown.Body.String(), "Task not found") {
		t.Errorf("body = %s, want the generic Task not found body (not task_platform_unknown)", wKnown.Body.String())
	}
}

// TestTaskRouter_ArtifactRoutesMountedWithTaskChain is cycle-8 L9's wiring
// lock, appended to L8's file: proves SetTaskRouter really registers the
// two artefact routes (not merely handler.ListTaskArtifacts/
// GetTaskArtifactContent existing as functions) and that they sit on the
// SAME TokenAuth-gated chain as the status route above — an unauthenticated
// request 401s before either handler runs, and an authenticated request for
// an absent task reaches the fail-closed 404 (not a 401/500), on both
// routes.
func TestTaskRouter_ArtifactRoutesMountedWithTaskChain(t *testing.T) {
	key, cleanup := taskRouterSeedToken(t)
	defer cleanup()

	engine := taskRouterEngine()

	routes := engine.Routes()
	want := map[string]bool{
		"GET /v1/tasks/:platform/:task_id/artifacts":              false,
		"GET /v1/tasks/:platform/:task_id/artifacts/:key/content": false,
	}
	for _, rt := range routes {
		routeKey := rt.Method + " " + rt.Path
		if _, ok := want[routeKey]; ok {
			want[routeKey] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("route %q not registered; got routes: %+v", k, routes)
		}
	}

	unauthedList := httptest.NewRequest(http.MethodGet, "/v1/tasks/suno/some-task-id/artifacts", nil)
	wList := httptest.NewRecorder()
	engine.ServeHTTP(wList, unauthedList)
	if wList.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated list status = %d, want 401; body=%s", wList.Code, wList.Body.String())
	}

	unauthedContent := httptest.NewRequest(http.MethodGet, "/v1/tasks/suno/some-task-id/artifacts/key/content", nil)
	wContent := httptest.NewRecorder()
	engine.ServeHTTP(wContent, unauthedContent)
	if wContent.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated content status = %d, want 401; body=%s", wContent.Code, wContent.Body.String())
	}

	knownList := httptest.NewRequest(http.MethodGet, "/v1/tasks/suno/does-not-exist/artifacts", nil)
	knownList.Header.Set("Authorization", "Bearer "+key)
	wKnownList := httptest.NewRecorder()
	engine.ServeHTTP(wKnownList, knownList)
	if wKnownList.Code != http.StatusNotFound {
		t.Errorf("authenticated absent-task list status = %d, want 404; body=%s", wKnownList.Code, wKnownList.Body.String())
	}

	knownContent := httptest.NewRequest(http.MethodGet, "/v1/tasks/suno/does-not-exist/artifacts/key/content", nil)
	knownContent.Header.Set("Authorization", "Bearer "+key)
	wKnownContent := httptest.NewRecorder()
	engine.ServeHTTP(wKnownContent, knownContent)
	if wKnownContent.Code != http.StatusNotFound {
		t.Errorf("authenticated absent-task content status = %d, want 404; body=%s", wKnownContent.Code, wKnownContent.Body.String())
	}
}

// TestTaskRouter_ChannelPlatformMismatchRejectedOnRealRouter is the mount lock
// for handler.TaskChannelPlatformGuard. The guard's whole purpose is that the
// URL's :platform cannot dispatch a vendor adaptor against a channel of some
// other vendor, and that property lives in the router registration: deleting
// the guard from the POST leg leaves every handler-package test green. This
// drives the REAL SetTaskRouter chain with a seeded channel whose type does
// not serve the declared platform and asserts the typed rejection.
func TestTaskRouter_ChannelPlatformMismatchRejectedOnRealRouter(t *testing.T) {
	key, cleanup := taskRouterSeedToken(t)
	defer cleanup()

	// A Suno-typed channel serving model "mismatch-probe-model".
	if err := repo.DB.AutoMigrate(&repo.Channel{}, &repo.Ability{}); err != nil {
		t.Fatalf("automigrate channel/ability: %v", err)
	}
	ch := &repo.Channel{
		Type: constant.ChannelTypeSunoAPI, Key: "sk-suno-probe", Status: common.ChannelStatusEnabled,
		Name: "suno-probe", Group: "default", Models: "mismatch-probe-model", TenantId: "default",
	}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := repo.DB.Create(&repo.Ability{
		Group: "default", Model: "mismatch-probe-model", ChannelId: ch.Id, Enabled: true,
		Priority: &[]int64{0}[0], Weight: 1,
	}).Error; err != nil {
		t.Fatalf("create ability: %v", err)
	}

	// Production reads this from the environment at boot; the test binary
	// leaves it at 0, which the body-size guard treats as "reject everything".
	prevMaxBody := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 2
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMaxBody })

	engine := taskRouterEngine()
	body := `{"model":"mismatch-probe-model","prompt":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/kling", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /v1/tasks/kling with a Suno-typed channel: status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), string(types.ErrorCodeTaskPlatformUnknown)) {
		t.Errorf("rejection body = %s, want the typed task-platform code", w.Body.String())
	}
}
