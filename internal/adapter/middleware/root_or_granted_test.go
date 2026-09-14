package middleware

// root_or_granted_test.go — oracle tests for RootOrGranted (L4,
// auth-security-17/18, console-ux-36). Drives the real gin session
// middleware chain (cookie store + resolveSessionIdentity, same shape as
// buildAuthRouter in auth_test.go) for the session-path cases, and the real
// OIDC JWKS harness (setupIntegrationTest / oidc_auth_integration_test.go)
// for the Bearer-JWT case — no hand-built context keys standing in for
// either auth path (REAL-CHAIN RULE).

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

var grantTestDBCounter atomic.Int64

// setupGrantTestDB wires repo.DB to an isolated in-memory SQLite database
// migrated with just the tables RootOrGranted's session path touches
// (repo.User for the incidental GetUserCache re-validation read,
// entity.AdminPermissionGrant for the grant lookup itself). Mirrors
// cover_helpers_test.go's setupCoverDB pattern without sharing its global
// table list, so this lane's addition can't perturb other packages' tests
// that reuse that helper.
func setupGrantTestDB(t *testing.T) func() {
	t.Helper()
	dbName := fmt.Sprintf("file:root_or_granted_test_%d?mode=memory&cache=shared", grantTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &entity.AdminPermissionGrant{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}
	prevDB := repo.DB
	prevRedis := common.RedisEnabled
	repo.DB = db
	common.RedisEnabled = false
	return func() {
		repo.DB = prevDB
		common.RedisEnabled = prevRedis
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

// buildRootOrGrantedRouter mirrors auth_test.go's buildAuthRouter but
// mounts RootOrGranted(resource, action) instead of UserAuth() — same
// cookie-session-injector shape so the session values a real login would
// leave behind are what resolveSessionIdentity reads, not a hand-set
// context key.
func buildRootOrGrantedRouter(sessionValues map[string]interface{}, resource, action string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	store := cookie.NewStore([]byte("root-or-granted-test-secret"))
	r.Use(sessions.Sessions("session", store))

	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range sessionValues {
			s.Set(k, v)
		}
		_ = s.Save()
		c.Next()
	})

	r.GET("/audit/events", RootOrGranted(resource, action), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	// A sibling route standing in for adminRoute's RootJWTAuth gate, so a
	// test can prove a grant that unlocks /audit/events does NOT also
	// unlock this one — RootOrGranted must never leak into the rest of the
	// admin subtree it isn't mounted on.
	r.GET("/tenants/1", RootAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	return r
}

func adminSession(id int) map[string]interface{} {
	return map[string]interface{}{
		"username": "delegated-admin",
		"role":     common.RoleAdminUser,
		"id":       id,
		"status":   common.UserStatusEnabled,
	}
}

func rootSession(id int) map[string]interface{} {
	return map[string]interface{}{
		"username": "root-operator",
		"role":     common.RoleRootUser,
		"id":       id,
		"status":   common.UserStatusEnabled,
	}
}

func TestRootOrGranted_SessionRootPasses(t *testing.T) {
	defer setupGrantTestDB(t)()

	r := buildRootOrGrantedRouter(rootSession(1), "audit", "read")
	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a root session with no grant row; body=%s", w.Code, w.Body.String())
	}
}

func TestRootOrGranted_SessionAdminNoGrant403(t *testing.T) {
	defer setupGrantTestDB(t)()

	r := buildRootOrGrantedRouter(adminSession(2), "audit", "read")
	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-root admin with no grant row; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if resp["error_code"] != "PERMISSION_DENIED" {
		t.Errorf("error_code = %v, want PERMISSION_DENIED", resp["error_code"])
	}
}

func TestRootOrGranted_SessionAdminWithGrant200(t *testing.T) {
	defer setupGrantTestDB(t)()

	if _, err := repo.CreatePermissionGrant(2, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	r := buildRootOrGrantedRouter(adminSession(2), "audit", "read")
	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a non-root admin with an active audit:read grant; body=%s", w.Code, w.Body.String())
	}

	// Negative: the SAME session must NOT reach the RootAuth-gated sibling
	// route — the grant unlocks exactly the audit feed, nothing else on the
	// admin subtree.
	req2 := httptest.NewRequest(http.MethodGet, "/tenants/1", nil)
	// Reuse the same cookie jar behaviour: build a fresh router instance
	// with the identical session values (cookie store is stateless per
	// request in this harness — see buildAuthRouter's own convention).
	r2 := buildRootOrGrantedRouter(adminSession(2), "audit", "read")
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, req2)
	// RootAuth's own insufficient-role rejection answers HTTP 200 with
	// success:false (authHelper's established, if surprising, convention —
	// see auth.go's roleVal < minRole branch) rather than a non-2xx status,
	// so the body's success field — not the status code — is the oracle
	// here.
	var resp2 map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("unmarshal /tenants/1 body: %v", err)
	}
	if resp2["success"] == true {
		t.Fatalf("body = %s, want success:false — an audit:read grant must not unlock RootAuth-gated /tenants/1", w2.Body.String())
	}
}

func TestRootOrGranted_GrantForOtherActionStill403(t *testing.T) {
	defer setupGrantTestDB(t)()

	if _, err := repo.CreatePermissionGrant(2, "audit", "write", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	r := buildRootOrGrantedRouter(adminSession(2), "audit", "read")
	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a grant for a DIFFERENT action must not satisfy this route's requirement; body=%s",
			w.Code, w.Body.String())
	}
}

func TestRootOrGranted_RevokedGrant403(t *testing.T) {
	defer setupGrantTestDB(t)()

	grant, err := repo.CreatePermissionGrant(2, "audit", "read", 1)
	if err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	if err := repo.RevokePermissionGrant(grant.Id); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}

	r := buildRootOrGrantedRouter(adminSession(2), "audit", "read")
	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a revoked grant; body=%s", w.Code, w.Body.String())
	}
}

// TestRootOrGranted_SessionCommonUserWithGrantRejected is the A-F3 oracle
// (cycle-8 L4 repair round): RootOrGranted's own RoleAdminUser floor is a
// second, independent line of defense behind CreateGrantV2's own grantee-
// role check (B-F4) — this seeds the grant directly through the repo layer
// (bypassing the handler entirely) so a role < RoleAdminUser session can
// never reach the grant lookup at all, regardless of how the row got there.
func TestRootOrGranted_SessionCommonUserWithGrantRejected(t *testing.T) {
	defer setupGrantTestDB(t)()

	if _, err := repo.CreatePermissionGrant(3, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	commonSession := map[string]interface{}{
		"username": "common-user",
		"role":     common.RoleCommonUser,
		"id":       3,
		"status":   common.UserStatusEnabled,
	}
	r := buildRootOrGrantedRouter(commonSession, "audit", "read")
	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if resp["success"] == true {
		t.Fatalf("body = %s, want success:false — a role < RoleAdminUser session must never reach the grant check", w.Body.String())
	}
}

// TestRootOrGranted_DisabledUserWithGrantRejected is the A-F4 oracle
// (cycle-8 L4 repair round) — the oracle for the operator decision
// "RootOrGranted must never bypass the per-request status/role
// re-validation". The session cookie itself still claims Enabled; only the
// DB row (read via resolveSessionIdentity's repo.GetUserCache
// re-validation, auth.go, PR #167) says Disabled. An active grant must not
// let a disabled user through.
func TestRootOrGranted_DisabledUserWithGrantRejected(t *testing.T) {
	defer setupGrantTestDB(t)()

	if err := repo.DB.Create(&repo.User{Id: 4, Username: "delegated-admin", Role: common.RoleAdminUser, Status: common.UserStatusDisabled}).Error; err != nil {
		t.Fatalf("seed disabled user: %v", err)
	}
	if _, err := repo.CreatePermissionGrant(4, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	r := buildRootOrGrantedRouter(adminSession(4), "audit", "read")
	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if resp["success"] == true {
		t.Fatalf("body = %s, want success:false — a disabled user must be rejected even holding an active grant", w.Body.String())
	}
	if resp["message"] != "用户已被封禁" {
		t.Errorf("message = %v, want 用户已被封禁 (the per-request DB re-validation rejection)", resp["message"])
	}
}

// TestRootOrGranted_OIDCOffBearerRequiresRootSession is the A-F5 oracle
// (cycle-8 L4 repair round): with OIDC off (or no JWKS manager), a Bearer
// header cannot be validated as a JWT, so RootOrGranted's own comment says
// it "mirrors RootJWTAuth's own fallback" — falls back to the session path
// but requires root OUTRIGHT, never consulting a grant. Proves both halves:
// a non-root admin session with an active grant is still refused, and a
// root session passes, in each case despite the Authorization header being
// present.
func TestRootOrGranted_OIDCOffBearerRequiresRootSession(t *testing.T) {
	defer setupGrantTestDB(t)()

	prevEnabled := oidcEnabled
	oidcEnabled = false
	defer func() { oidcEnabled = prevEnabled }()

	if _, err := repo.CreatePermissionGrant(2, "audit", "read", 1); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	r := buildRootOrGrantedRouter(adminSession(2), "audit", "read")
	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	req.Header.Set("Authorization", "Bearer some-opaque-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if resp["success"] == true {
		t.Fatalf("body = %s, want success:false — OIDC-off Bearer branch must require root, never honour a grant", w.Body.String())
	}

	r2 := buildRootOrGrantedRouter(rootSession(1), "audit", "read")
	req2 := httptest.NewRequest(http.MethodGet, "/audit/events", nil)
	req2.Header.Set("Authorization", "Bearer some-opaque-token")
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("root session + OIDC-off Bearer header: status = %d, want 200; body=%s", w2.Code, w2.Body.String())
	}
}

// TestRootOrGranted_BearerJWTNonRoot403 documents the JWT-branch scope: a
// genuinely signed, correctly issued admin (non-root) JWT gets the same 403
// a non-root JWT always got on this subtree — RootOrGranted's Bearer path
// never populates an "id" to look a grant up against (mirrors
// RootJWTAuth's JWT branch, admin_jwt_auth.go), so delegated grants are a
// session-path-only feature this cycle. Reuses the real JWKS harness
// (setupIntegrationTest/ctx.createJWT) so the token is genuinely validated,
// not a hand-built claims struct.
func TestRootOrGranted_BearerJWTNonRoot403(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	token := ctx.createJWT(t, adminClaims(ctx.Issuer, map[string]interface{}{"admin": map[string]interface{}{}}))
	r := mountJWT(RootOrGranted("audit", "read"))

	w := doBearer(r, token)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a valid but non-root Bearer JWT; body=%s", w.Code, w.Body.String())
	}
}
