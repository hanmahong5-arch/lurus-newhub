package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// buildRoleRouter is buildAuthRouter with a configurable minimum role so both
// UserAuth and AdminAuth branches of authHelper can be exercised.
func buildRoleRouter(sessionValues map[string]interface{}, mw func() func(c *gin.Context)) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("role-test-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range sessionValues {
			s.Set(k, v)
		}
		_ = s.Save()
		c.Next()
	})
	r.GET("/test", mw(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func serveRole(r *gin.Engine, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthHelper_StatusDisabled(t *testing.T) {
	r := buildRoleRouter(map[string]interface{}{
		"username": "u", "role": common.RoleCommonUser, "id": 0, "status": common.UserStatusDisabled,
	}, UserAuth)
	w := serveRole(r, nil)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("disabled user reached handler; body=%s", w.Body.String())
	}
}

func TestAuthHelper_RoleInsufficient(t *testing.T) {
	// Common-role session hitting an AdminAuth route → permission denied.
	r := buildRoleRouter(map[string]interface{}{
		"username": "u", "role": common.RoleCommonUser, "id": 0, "status": common.UserStatusEnabled,
	}, AdminAuth)
	w := serveRole(r, nil)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("under-privileged user reached admin handler; body=%s", w.Body.String())
	}
}

func TestAuthHelper_InvalidStatusType(t *testing.T) {
	r := buildRoleRouter(map[string]interface{}{
		"username": "u", "role": common.RoleCommonUser, "id": 0, "status": "not-an-int",
	}, UserAuth)
	w := serveRole(r, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for non-int session status", w.Code)
	}
}

func TestAuthHelper_InvalidRoleType(t *testing.T) {
	r := buildRoleRouter(map[string]interface{}{
		"username": "u", "role": "not-an-int", "id": 0, "status": common.UserStatusEnabled,
	}, UserAuth)
	w := serveRole(r, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for non-int session role", w.Code)
	}
}

func TestAuthHelper_UsernameEmpty_Invalid(t *testing.T) {
	r := buildRoleRouter(map[string]interface{}{
		"username": "", "role": common.RoleCommonUser, "id": 0, "status": common.UserStatusEnabled,
	}, UserAuth)
	w := serveRole(r, nil)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("empty username reached handler; body=%s", w.Body.String())
	}
}

func TestAuthHelper_NoSessionNoToken_401(t *testing.T) {
	r := buildRoleRouter(map[string]interface{}{}, UserAuth) // empty session, no Authorization
	w := serveRole(r, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for anonymous", w.Code)
	}
}

func TestAuthHelper_InvalidAccessToken(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// No session username → access-token path. A bogus bearer is neither a
	// valid identity-session token nor an access token → rejected.
	r := buildRoleRouter(map[string]interface{}{}, UserAuth)
	w := serveRole(r, map[string]string{"Authorization": "Bearer bogus-access-token"})
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("invalid access token reached handler; body=%s", w.Body.String())
	}
}

func TestAuthHelper_TenantDisabled_403(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// User belongs to a disabled tenant → authHelper's TI-5 lockout (403).
	tn := &entity.Tenant{Id: "tenantD", Slug: "tenantD", Name: "n", Status: entity.TenantStatusDisabled, PlanType: entity.TenantPlanFree, MaxUsers: 10, MaxQuota: 1000}
	if err := repo.DB.Create(tn).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &repo.User{Username: "tenantuser", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "tu@local", TenantId: "tenantD"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	r := buildRoleRouter(map[string]interface{}{
		"username": user.Username, "role": user.Role, "id": user.Id, "status": user.Status,
	}, UserAuth)
	w := serveRole(r, nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for member of disabled tenant; body=%s", w.Code, w.Body.String())
	}
}

// authHelper re-validates status against the DB (via the user cache) on
// every request. Without that, a long-lived session/cookie keeps a disabled
// user admitted until the session expires or is reissued — an admin's
// "disable this user" action would be cosmetic. The session in this test
// never changes; only the DB does.
func TestAuthHelper_SessionStatusRevalidated_DisabledInDB(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	user := &repo.User{
		Username: "disableme", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "disableme@local", TenantId: "default",
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// buildRoleRouter re-seeds these exact session values before every
	// request, so status stays "Enabled" for the lifetime of r regardless of
	// what happens in the DB — exactly like a real cookie that isn't reissued.
	r := buildRoleRouter(map[string]interface{}{
		"username": user.Username, "role": user.Role, "id": user.Id, "status": common.UserStatusEnabled,
	}, UserAuth)

	// Baseline: DB and session agree — request succeeds.
	if w := serveRole(r, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("baseline (pre-disable) request failed; status=%d body=%s", w.Code, w.Body.String())
	}

	if err := repo.DisableUserById(user.Id); err != nil {
		t.Fatalf("DisableUserById: %v", err)
	}

	// Same session, no logout, no wait — the very next request must observe
	// the disable.
	w := serveRole(r, nil)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("disabled user's existing session still reached the handler; body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "封禁") {
		t.Errorf("body = %s, want it to contain the ban message '封禁'", w.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Success {
		t.Errorf("success = true, want false for a disabled user")
	}
}

// Twin of the status test: authHelper must also re-validate role, so an
// admin demotion takes effect on the demoted user's very next
// AdminAuth-gated request rather than waiting for their session to refresh.
func TestAuthHelper_SessionRoleRevalidated_DemotedInDB(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	user := &repo.User{
		Username: "demoteme", Role: common.RoleAdminUser, Status: common.UserStatusEnabled,
		Email: "demoteme@local", TenantId: "default", Group: "default",
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	r := buildRoleRouter(map[string]interface{}{
		"username": user.Username, "role": common.RoleAdminUser, "id": user.Id, "status": user.Status,
	}, AdminAuth)

	if w := serveRole(r, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("baseline (pre-demotion) admin request failed; status=%d body=%s", w.Code, w.Body.String())
	}

	if _, err := repo.AdminUpdateUser(user.Id, common.RoleCommonUser, user.Status, user.Quota, user.Group); err != nil {
		t.Fatalf("AdminUpdateUser demote: %v", err)
	}

	w := serveRole(r, nil)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("demoted user's existing admin session still reached the handler; body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "权限不足") {
		t.Errorf("body = %s, want it to contain '权限不足'", w.Body.String())
	}
}

// Negative control: when the cache/DB lookup itself fails (here, the session
// carries an id that was never persisted), revalidation must fail OPEN and
// preserve the session-supplied status/role — mirroring the pre-existing
// tenant-disabled check's fail-open policy on lookup error. A transient
// Redis/DB hiccup must not turn into a hard rejection for every request.
func TestAuthHelper_SessionRevalidation_FailsOpenOnLookupError(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := buildRoleRouter(map[string]interface{}{
		"username": "ghost", "role": common.RoleCommonUser, "id": 424242, "status": common.UserStatusEnabled,
	}, UserAuth)
	w := serveRole(r, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("status=%d body=%s, want 200 ok:true when the cache/DB lookup fails (fail open)", w.Code, w.Body.String())
	}
}

func TestAuthHelper_HappyPath_SetsContext(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	user := &repo.User{Username: "okuser", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "ok@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	r := buildRoleRouter(map[string]interface{}{
		"username": user.Username, "role": user.Role, "id": user.Id, "status": user.Status,
	}, UserAuth)
	w := serveRole(r, nil)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for valid default-tenant user; body=%s", w.Code, w.Body.String())
	}
}
