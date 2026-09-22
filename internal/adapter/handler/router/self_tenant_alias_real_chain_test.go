package router

// self_tenant_alias_real_chain_test.go — REAL-CHAIN oracle for the reserved
// :tenant_slug alias middleware.SelfTenantSlug ("~" = the caller's own
// tenant). The console builds every tenant-scoped path with it, so these are
// the properties the whole v2 console now stands on:
//
//   - the alias reaches the caller's own tenant through the production
//     UserAuth()+TenantSlugGuard() chain;
//   - handlers that read c.Param("tenant_slug") themselves see the REAL slug
//     (ListModelsV2 re-resolves it and 404s on anything unknown, so an
//     un-rewritten "~" answers TENANT_NOT_FOUND);
//   - the alias does not get a disabled tenant past the chain;
//   - the alias never needs a session to name a tenant — without one it is
//     401, not a tenant;
//   - the explicit-slug behaviour is unchanged (own slug 200, foreign slug
//     403 TENANT_MISMATCH).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var selfTenantAliasDBCounter atomic.Int64

type selfTenantAliasFixture struct {
	db          *gorm.DB
	ownTenantID string
	ownSlug     string
	otherSlug   string
	// serve runs one request through SetApiV2Router; signedIn=false omits
	// the session identity entirely.
	serve func(signedIn bool, method, path string) *httptest.ResponseRecorder
}

func setupSelfTenantAlias(t *testing.T) *selfTenantAliasFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := selfTenantAliasDBCounter.Add(1)
	dbName := fmt.Sprintf("file:selftenantalias%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &entity.Tenant{}, &entity.ChatSession{}, &entity.ChatMessage{},
		&entity.Model{}, &entity.Vendor{}, &repo.Token{},
	} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB, prevRedis := repo.DB, repo.LOG_DB, common.RedisEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.RedisEnabled = false
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	})

	ownID := fmt.Sprintf("alias-own-%d", n)
	ownSlug := fmt.Sprintf("alias-own-slug-%d", n)
	otherID := fmt.Sprintf("alias-other-%d", n)
	otherSlug := fmt.Sprintf("alias-other-slug-%d", n)
	for _, tn := range []*entity.Tenant{
		{Id: ownID, IDPOrgID: "org-" + ownID, Slug: ownSlug, Name: "Own", Status: entity.TenantStatusEnabled},
		{Id: otherID, IDPOrgID: "org-" + otherID, Slug: otherSlug, Name: "Other", Status: entity.TenantStatusEnabled},
	} {
		if err := db.Create(tn).Error; err != nil {
			t.Fatalf("create tenant: %v", err)
		}
	}
	user := &repo.User{
		Username: fmt.Sprintf("alias_user_%d", n), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("alias-%d@local", n), TenantId: ownID,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	serve := func(signedIn bool, method, path string) *httptest.ResponseRecorder {
		engine := gin.New()
		engine.Use(gin.Recovery())
		engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("self-tenant-alias-secret"))))
		if signedIn {
			engine.Use(func(c *gin.Context) {
				s := sessions.Default(c)
				s.Set("username", user.Username)
				s.Set("role", user.Role)
				s.Set("id", user.Id)
				s.Set("status", common.UserStatusEnabled)
				_ = s.Save()
				c.Next()
			})
		}
		SetApiV2Router(engine)
		req := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	return &selfTenantAliasFixture{
		db: db, ownTenantID: ownID, ownSlug: ownSlug, otherSlug: otherSlug, serve: serve,
	}
}

func selfTenantAliasErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.ErrorCode
}

func aliasPath(rest string) string {
	return "/api/v2/" + middleware.SelfTenantSlug + rest
}

// The alias reaches the caller's own tenant: chat sessions (tenant taken
// from the context) and the model catalogue (tenant re-read from
// c.Param("tenant_slug") by the handler itself).
func TestSelfTenantAlias_ReachesOwnTenant(t *testing.T) {
	fx := setupSelfTenantAlias(t)

	if w := fx.serve(true, http.MethodGet, aliasPath("/chat/sessions")); w.Code != http.StatusOK {
		t.Fatalf("GET ~/chat/sessions = %d %s, want 200", w.Code, w.Body.String())
	}

	// ListModelsV2 does repo.GetTenantBySlug(c.Param("tenant_slug")); it
	// only succeeds if the guard handed it the real slug.
	w := fx.serve(true, http.MethodGet, aliasPath("/models"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET ~/models = %d %s (error_code %q), want 200 — the handler did not see the real slug",
			w.Code, w.Body.String(), selfTenantAliasErrorCode(t, w))
	}
}

// The console's only source of its tenant's name: GET ~/user/me reports the
// real slug, not the alias it was asked with.
func TestSelfTenantAlias_UserMeReportsRealSlug(t *testing.T) {
	fx := setupSelfTenantAlias(t)
	w := fx.serve(true, http.MethodGet, aliasPath("/user/me"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET ~/user/me = %d %s, want 200", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			TenantSlug string `json:"tenant_slug"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.TenantSlug != fx.ownSlug {
		t.Fatalf("user/me tenant_slug = %q, want %q", body.Data.TenantSlug, fx.ownSlug)
	}
}

// The alias must not become a way around the disabled-tenant check.
//
// This is a property of the whole chain, not an oracle for the guard alone:
// UserAuth's repo.TenantGate (middleware/auth.go) refuses a disabled tenant
// before the guard runs, so deleting only the guard's IsDisabled check for
// the alias keeps this green (measured). It goes red if the alias ever
// reaches a handler for a disabled tenant by any route.
func TestSelfTenantAlias_DisabledTenantStillRefused(t *testing.T) {
	fx := setupSelfTenantAlias(t)
	if err := fx.db.Model(&entity.Tenant{}).Where("id = ?", fx.ownTenantID).
		Update("status", entity.TenantStatusDisabled).Error; err != nil {
		t.Fatalf("disable tenant: %v", err)
	}

	w := fx.serve(true, http.MethodGet, aliasPath("/chat/sessions"))
	if w.Code != http.StatusForbidden || selfTenantAliasErrorCode(t, w) != "TENANT_DISABLED" {
		t.Fatalf("GET ~/chat/sessions on a disabled tenant = %d %s, want 403 TENANT_DISABLED", w.Code, w.Body.String())
	}
}

// Without a session the alias names no tenant.
func TestSelfTenantAlias_WithoutSessionIsUnauthorized(t *testing.T) {
	fx := setupSelfTenantAlias(t)
	w := fx.serve(false, http.MethodGet, aliasPath("/chat/sessions"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET ~/chat/sessions = %d %s, want 401", w.Code, w.Body.String())
	}
}

// The explicit-slug contract is unchanged by the alias.
func TestSelfTenantAlias_ExplicitSlugsUnchanged(t *testing.T) {
	fx := setupSelfTenantAlias(t)

	if w := fx.serve(true, http.MethodGet, "/api/v2/"+fx.ownSlug+"/chat/sessions"); w.Code != http.StatusOK {
		t.Fatalf("own slug = %d %s, want 200", w.Code, w.Body.String())
	}
	w := fx.serve(true, http.MethodGet, "/api/v2/"+fx.otherSlug+"/chat/sessions")
	if w.Code != http.StatusForbidden || selfTenantAliasErrorCode(t, w) != "TENANT_MISMATCH" {
		t.Fatalf("foreign slug = %d %s, want 403 TENANT_MISMATCH", w.Code, w.Body.String())
	}
	w = fx.serve(true, http.MethodGet, "/api/v2/no-such-tenant/chat/sessions")
	if w.Code != http.StatusNotFound || selfTenantAliasErrorCode(t, w) != "TENANT_NOT_FOUND" {
		t.Fatalf("unknown slug = %d %s, want 404 TENANT_NOT_FOUND", w.Code, w.Body.String())
	}
}
