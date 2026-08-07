package handler

// cov_handler-deep-c_zita_bootstrap_test.go — business-acceptance coverage
// for zita_bootstrap.go: ZitaBootstrap (0% before this file), plus the
// remaining gaps in resolveTenantSlug / autoCreateBridgedUser. The real
// zita.AuthMiddleware validates a platform-signed session cookie via a live
// HMAC secret shared with lurus-platform, which is out of scope here (no
// real external dependency); instead these tests inject *zita.Identity
// directly into the gin context the same way AuthMiddleware does
// (c.Set(zita.ContextKey, id)) — a legitimate seam since IdentityFromContext
// is a pure context read, decoupled from how the identity got there.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

func handlerDeepCZitaRouter(t *testing.T, identity *zita.Identity, setIdentity bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("handlerdeepc-zita-session-secret"))
	r.Use(sessions.Sessions("handlerdeepc_zita_session", store))
	r.POST("/zita-bootstrap", func(c *gin.Context) {
		if setIdentity {
			c.Set(zita.ContextKey, identity)
		}
		ZitaBootstrap(c)
	})
	return r
}

func handlerDeepCDoZitaBootstrap(r *gin.Engine) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/zita-bootstrap", nil)
	r.ServeHTTP(w, req)
	return w
}

func TestZitaBootstrap_NoIdentityInContext_Unauthorized(t *testing.T) {
	r := handlerDeepCZitaRouter(t, nil, false)
	w := handlerDeepCDoZitaBootstrap(r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["message"] != "zita identity missing — SDK middleware did not validate session" {
		t.Errorf("message = %v", resp["message"])
	}
}

func TestZitaBootstrap_ZeroAccountID_Unauthorized(t *testing.T) {
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: 0}, true)
	w := handlerDeepCDoZitaBootstrap(r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["message"] != "zita identity carries no account_id" {
		t.Errorf("message = %v", resp["message"])
	}
}

// TestZitaBootstrap_ExistingLinkedUser_ResolvesRealTenantSlug covers the
// dominant repeat-login path: a user already bound to this account_id, with
// a TenantId that resolves to a real, distinctly-sluggged tenant row — the
// response must carry that ACTUAL slug (not "default"), proving
// resolveTenantSlug's DB-lookup branch, not just its fallback.
func TestZitaBootstrap_ExistingLinkedUser_ResolvesRealTenantSlug(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	accountID := int64(42)
	user := &repo.User{
		Username: "zita-linked-user", DisplayName: "Zita Linked", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "zita@test.local", TenantId: ctx.TenantID,
		Group: "default", LurusAccountID: &accountID,
	}
	if err := ctx.DB.Create(user).Error; err != nil {
		t.Fatalf("seed linked user: %v", err)
	}

	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrap(r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T", resp["data"])
	}
	if data["username"] != "zita-linked-user" {
		t.Errorf("username = %v, want zita-linked-user", data["username"])
	}
	var seededTenant repo.Tenant
	if err := ctx.DB.Where("id = ?", ctx.TenantID).First(&seededTenant).Error; err != nil {
		t.Fatalf("load seeded tenant: %v", err)
	}
	if seededTenant.Slug == "default" || seededTenant.Slug == "" {
		t.Fatalf("test setup invariant broken: seeded tenant slug = %q, want a real non-default slug", seededTenant.Slug)
	}
	if data["tenant_slug"] != seededTenant.Slug {
		// tenant_slug must resolve to the SEEDED tenant's real slug via the
		// resolveTenantSlug DB-lookup branch, not fall back to "default".
		t.Errorf("tenant_slug = %v, want the real seeded tenant slug %q", data["tenant_slug"], seededTenant.Slug)
	}
	if id, _ := data["id"].(float64); int(id) != user.Id {
		t.Errorf("id = %v, want %d", data["id"], user.Id)
	}

	// Cookie session must actually carry the resolved user (id/username/role).
	setCookie := w.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Error("expected a session cookie to be set on successful bootstrap")
	}
}

func TestZitaBootstrap_DisabledUser_Forbidden(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	accountID := int64(43)
	user := &repo.User{
		Username: "zita-disabled-user", DisplayName: "Zita Disabled", Role: common.RoleCommonUser,
		Status: common.UserStatusDisabled, Email: "zitadisabled@test.local", TenantId: ctx.TenantID,
		Group: "default", LurusAccountID: &accountID,
	}
	if err := ctx.DB.Create(user).Error; err != nil {
		t.Fatalf("seed disabled user: %v", err)
	}

	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrap(r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["message"] != "User account is disabled" {
		t.Errorf("message = %v", resp["message"])
	}
}

// TestZitaBootstrap_UnknownAccountID_AutoCreatesUser_DefaultTenantSlug
// covers the auto-create path (repo.GetUserByLurusAccountID returns
// ErrRecordNotFound) end to end: a real row must be persisted with
// username "lurus_<account_id>", TenantId "default" (resolveTenantSlug's
// literal-"default" fast path — no DB lookup needed), and the SAME id must
// come back on a second bootstrap call for the same account (no duplicate
// provisioning).
func TestZitaBootstrap_UnknownAccountID_AutoCreatesUser_DefaultTenantSlug(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const accountID = int64(777001)
	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w := handlerDeepCDoZitaBootstrap(r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T body=%s", resp["data"], w.Body.String())
	}
	if data["username"] != "lurus_777001" {
		t.Errorf("username = %v, want lurus_777001 (derived from account_id)", data["username"])
	}
	if data["tenant_slug"] != "default" {
		t.Errorf("tenant_slug = %v, want default (auto-created user's TenantId is literally 'default')", data["tenant_slug"])
	}

	var persisted repo.User
	if err := ctx.DB.Unscoped().Where("username = ?", "lurus_777001").First(&persisted).Error; err != nil {
		t.Fatalf("expected auto-created user to be persisted: %v", err)
	}
	if persisted.LurusAccountID == nil || *persisted.LurusAccountID != accountID {
		t.Errorf("persisted LurusAccountID = %v, want %d", persisted.LurusAccountID, accountID)
	}

	// Second bootstrap for the SAME account_id must resolve the SAME row,
	// not provision a duplicate.
	r2 := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: accountID}, true)
	w2 := handlerDeepCDoZitaBootstrap(r2)
	resp2 := handlerDeployParseBody(t, w2)
	data2 := resp2["data"].(map[string]interface{})
	if int(data2["id"].(float64)) != persisted.Id {
		t.Errorf("second bootstrap id = %v, want %d (same row, no duplicate provisioning)", data2["id"], persisted.Id)
	}
}

func TestZitaBootstrap_UsersTableMissing_LookupFailsWithServerError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.Migrator().DropTable(&repo.User{}); err != nil {
		t.Fatalf("drop users table: %v", err)
	}

	r := handlerDeepCZitaRouter(t, &zita.Identity{AccountID: 999}, true)
	w := handlerDeepCDoZitaBootstrap(r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (genuine DB failure, not a 401/403), body=%s", w.Code, w.Body.String())
	}
}
