package handler

// ============================================================================
// user.go edge cases beyond cover_r2_auth_test.go's happy paths and
// v1_cross_tenant_idor_test.go's GetUser/UpdateUser/GetAllUsers IDOR sweep:
//   - SearchUsers cross-tenant scope leak (the search codepath is a SEPARATE
//     query — repo.SearchUsersByTenant — from GetAllUsers's repo.GetUsersByTenant,
//     so tenant scoping there is a distinct risk surface, not implied by the
//     GetAllUsers sweep).
//   - UpdateUserSetting's gotify/bark/email validation branches that
//     cover_r2_auth_test.go didn't reach.
//   - UpdateSelf's malformed-payload and duplicate-username collision paths.
//   - GetUserModels' id-param-invalid fallback to the session id.
// Reuses r2authSession/r2authReq/r2authAssertSuccess (cover_r2_auth_test.go)
// and v1Ctx/v1Body (v1_cross_tenant_idor_test.go) — same package.
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TestHandlerIdentityLog_SearchUsers_TenantScopeLeak proves the *search*
// endpoint (a distinct query path from the plain list) still converges a
// non-root tenant admin to their own tenant, and that root keeps global
// visibility. This is the highest-risk edge in the identity surface: a
// mistake here leaks another company's user roster through a keyword search.
func TestHandlerIdentityLog_SearchUsers_TenantScopeLeak(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := &repo.User{
		Username: "search-victim", DisplayName: "Search Victim",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "victim@other-tenant.local", TenantId: "other-tenant-search-xyz", Quota: 123,
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	containsID := func(items []interface{}, id int) bool {
		for _, it := range items {
			if m, ok := it.(map[string]interface{}); ok {
				if idf, ok := m["id"].(float64); ok && int(idf) == id {
					return true
				}
			}
		}
		return false
	}

	// Non-root tenant admin searching with no keyword filter must never see
	// the other tenant's user, only its own tenant's users.
	c, w := v1Ctx(http.MethodGet, "/api/user/search?keyword=&status=0", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	SearchUsers(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	items := v1Items(t, w)
	if containsID(items, victim.Id) {
		t.Fatalf("SearchUsers leaked cross-tenant victim id=%d to a non-root caller", victim.Id)
	}
	if !containsID(items, ctx.NormalUser.Id) {
		t.Errorf("SearchUsers (non-root) missing own-tenant user in results")
	}

	// Root retains global visibility through the same endpoint.
	c, w = v1Ctx(http.MethodGet, "/api/user/search?keyword=&status=0", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	SearchUsers(c)
	if !containsID(v1Items(t, w), victim.Id) {
		t.Errorf("SearchUsers (root) must see every tenant's users, missing victim id=%d", victim.Id)
	}
}

// TestHandlerIdentityLog_SearchUsers_KeywordScopedToOwnTenant proves a
// tenant admin searching BY the victim's exact username still gets nothing —
// the tenant filter applies even when the keyword would otherwise match.
func TestHandlerIdentityLog_SearchUsers_KeywordScopedToOwnTenant(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := &repo.User{
		Username: "unique-victim-handle", DisplayName: "Victim",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "v2@other.local", TenantId: "other-tenant-kw-xyz", Quota: 1,
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	c, w := v1Ctx(http.MethodGet, "/api/user/search?keyword=unique-victim-handle", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	SearchUsers(c)
	items := v1Items(t, w)
	for _, it := range items {
		if m, ok := it.(map[string]interface{}); ok {
			if idf, ok := m["id"].(float64); ok && int(idf) == victim.Id {
				t.Fatalf("keyword search crossed tenant boundary and returned victim id=%d", victim.Id)
			}
		}
	}
}

// TestHandlerIdentityLog_UpdateUserSetting_GotifyBranches exercises the
// gotify validation ladder cover_r2_auth_test.go's "gotify_missing_token"
// case didn't reach: missing server URL, a URL that fails ParseRequestURI,
// a URL with a disallowed scheme, and an in-range explicit priority.
func TestHandlerIdentityLog_UpdateUserSetting_GotifyBranches(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	call := func(body interface{}) *httptest.ResponseRecorder {
		c, w := r2authSession(http.MethodPut, "/api/user/setting", body, ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateUserSetting(c)
		return w
	}

	t.Run("missing_server_url", func(t *testing.T) {
		w := call(map[string]interface{}{"notify_type": "gotify", "quota_warning_threshold": 1.0, "gotify_token": "tok"})
		body := r2authBody(t, w)
		if success, _ := body["success"].(bool); success {
			t.Fatalf("expected rejection for missing gotify url, body=%s", w.Body.String())
		}
		if body["message"] != "Gotify服务器地址不能为空" {
			t.Errorf("message = %v", body["message"])
		}
	})

	t.Run("malformed_url", func(t *testing.T) {
		w := call(map[string]interface{}{"notify_type": "gotify", "quota_warning_threshold": 1.0, "gotify_url": "://bad", "gotify_token": "tok"})
		body := r2authBody(t, w)
		if body["message"] != "无效的Gotify服务器地址" {
			t.Errorf("message = %v, body=%s", body["message"], w.Body.String())
		}
	})

	t.Run("disallowed_scheme", func(t *testing.T) {
		w := call(map[string]interface{}{"notify_type": "gotify", "quota_warning_threshold": 1.0, "gotify_url": "ftp://g.example.com", "gotify_token": "tok"})
		body := r2authBody(t, w)
		if body["message"] != "Gotify服务器地址必须以http://或https://开头" {
			t.Errorf("message = %v", body["message"])
		}
	})

	t.Run("in_range_priority_persisted_verbatim", func(t *testing.T) {
		w := call(map[string]interface{}{
			"notify_type": "gotify", "quota_warning_threshold": 1.0,
			"gotify_url": "https://g.example.com", "gotify_token": "tok", "gotify_priority": 3,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		body := r2authBody(t, w)
		if success, _ := body["success"].(bool); !success {
			t.Fatalf("expected success, body=%s", w.Body.String())
		}
		var u repo.User
		if err := ctx.DB.First(&u, ctx.NormalUser.Id).Error; err != nil {
			t.Fatalf("reload: %v", err)
		}
		s := u.GetSetting()
		if s.GotifyPriority != 3 {
			t.Errorf("in-range priority not persisted verbatim: got %d, want 3", s.GotifyPriority)
		}
	})
}

// TestHandlerIdentityLog_UpdateUserSetting_EmailAndBarkValidation covers the
// invalid-email and bad-Bark-URL branches that cover_r2_auth_test.go's
// "bark_bad_scheme" case (a syntactically valid ftp:// URL) never reached.
func TestHandlerIdentityLog_UpdateUserSetting_EmailAndBarkValidation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	call := func(body interface{}) map[string]interface{} {
		c, w := r2authSession(http.MethodPut, "/api/user/setting", body, ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateUserSetting(c)
		return r2authBody(t, w)
	}

	t.Run("invalid_email_format", func(t *testing.T) {
		body := call(map[string]interface{}{"notify_type": "email", "quota_warning_threshold": 1.0, "notification_email": "not-an-email"})
		if success, _ := body["success"].(bool); success {
			t.Fatal("expected rejection for malformed notification email")
		}
		if body["message"] != "无效的邮箱地址" {
			t.Errorf("message = %v", body["message"])
		}
	})

	t.Run("bark_url_fails_parse", func(t *testing.T) {
		body := call(map[string]interface{}{"notify_type": "bark", "quota_warning_threshold": 1.0, "bark_url": "://not-a-uri"})
		if body["message"] != "无效的Bark推送URL" {
			t.Errorf("message = %v", body["message"])
		}
	})
}

// TestHandlerIdentityLog_UpdateSelf_TypeMismatchRejected proves a payload
// whose "quota" value cannot fit the target struct field (string where an
// int is expected) is rejected as invalid input rather than silently
// dropping the field or crashing.
func TestHandlerIdentityLog_UpdateSelf_TypeMismatchRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authSession(http.MethodPut, "/api/user/self",
		map[string]interface{}{"username": "ok-name", "quota": "not-a-number"},
		ctx.NormalUser.Id, common.RoleCommonUser)
	UpdateSelf(c)
	body := r2authBody(t, w)
	if success, _ := body["success"].(bool); success {
		t.Fatalf("expected rejection for type-mismatched quota field, body=%s", w.Body.String())
	}
	if body["message"] != "无效的参数" {
		t.Errorf("message = %v", body["message"])
	}
}

// TestHandlerIdentityLog_UpdateSelf_ValidationFailure proves a username over
// the 20-char limit is rejected by validator, not silently truncated/saved.
func TestHandlerIdentityLog_UpdateSelf_ValidationFailure(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tooLong := "this-username-is-definitely-over-twenty-chars"
	c, w := r2authSession(http.MethodPut, "/api/user/self",
		map[string]interface{}{"username": tooLong},
		ctx.NormalUser.Id, common.RoleCommonUser)
	UpdateSelf(c)
	body := r2authBody(t, w)
	if success, _ := body["success"].(bool); success {
		t.Fatalf("expected validation rejection for over-long username, body=%s", w.Body.String())
	}

	var reloaded repo.User
	if err := ctx.DB.First(&reloaded, ctx.NormalUser.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Username == tooLong {
		t.Error("over-long username must not be persisted")
	}
}

// TestHandlerIdentityLog_UpdateSelf_DuplicateUsernameCollision proves the
// same-tenant unique-username constraint is enforced on the self-service
// rename path too (not just admin create/edit): renaming yourself to an
// existing peer's username in the same tenant is rejected, not silently
// creating an ambiguous duplicate.
func TestHandlerIdentityLog_UpdateSelf_DuplicateUsernameCollision(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	taken := ctx.AdminUser.Username
	c, w := r2authSession(http.MethodPut, "/api/user/self",
		map[string]interface{}{"username": taken},
		ctx.NormalUser.Id, common.RoleCommonUser)
	UpdateSelf(c)
	if w.Code != http.StatusOK {
		t.Fatalf("handler always responds 200 on this path, got %d", w.Code)
	}
	body := r2authBody(t, w)
	if success, _ := body["success"].(bool); success {
		t.Fatalf("renaming to an in-tenant peer's username must be rejected, body=%s", w.Body.String())
	}

	var reloaded repo.User
	if err := ctx.DB.First(&reloaded, ctx.NormalUser.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Username == taken {
		t.Error("duplicate username must not be persisted — would make two users share one login handle")
	}
}

// TestHandlerIdentityLog_UpdateSelf_SidebarPath_UnknownUser proves the
// sidebar_modules branch fails closed via ApiError when the session id
// doesn't resolve to a real user row.
func TestHandlerIdentityLog_UpdateSelf_SidebarPath_UnknownUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authSession(http.MethodPut, "/api/user/self",
		map[string]interface{}{"sidebar_modules": `{"chat":true}`},
		999999, common.RoleCommonUser)
	UpdateSelf(c)
	body := r2authBody(t, w)
	if success, _ := body["success"].(bool); success {
		t.Fatalf("unknown-user sidebar update must fail, body=%s", w.Body.String())
	}
}

// TestHandlerIdentityLog_GetUserModels_InvalidParamFallsBackToSessionUser
// proves a non-numeric :id path param doesn't 400 — it falls back to the
// authenticated caller's own id (the route is also mounted without :id for
// "my models").
func TestHandlerIdentityLog_GetUserModels_InvalidParamFallsBackToSessionUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authSession(http.MethodGet, "/api/user/models", nil, ctx.NormalUser.Id, common.RoleCommonUser)
	c.Params = gin.Params{{Key: "id", Value: "not-a-number"}}
	GetUserModels(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	r2authAssertSuccess(t, w, true)
}
