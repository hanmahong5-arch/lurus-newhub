package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// R2Auth shared scaffolding
//
// These tests raise coverage of the auth/user/token/option/misc HTTP handlers
// (oauth.go, user.go, token.go, v2_token.go, misc.go, option.go). They reuse
// the package's SetupV2TestRouter harness (in-memory SQLite DB + seeded
// root/admin/normal users + one enabled tenant; it swaps repo.DB and the
// relevant common.* flags and restores them on Cleanup). Handlers that read
// their identity from the gin context ("id"/"role"/"tenant_id"/"tenant_context")
// are invoked directly against a synthetic gin.Context; session-backed OAuth
// handlers run behind a cookie session middleware.
// ============================================================================

// r2authReq builds a synthetic gin.Context carrying an HTTP request. Query
// params are parsed from target; a non-nil body is JSON-encoded.
func r2authReq(method, target string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var req *http.Request
	if body != nil {
		switch b := body.(type) {
		case string:
			req = httptest.NewRequest(method, target, bytes.NewReader([]byte(b)))
		case []byte:
			req = httptest.NewRequest(method, target, bytes.NewReader(b))
		default:
			data, _ := json.Marshal(body)
			req = httptest.NewRequest(method, target, bytes.NewReader(data))
		}
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

// r2authSession builds a context carrying the caller identity (v1 session keys).
func r2authSession(method, target string, body interface{}, id, role int) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := r2authReq(method, target, body)
	c.Set("id", id)
	c.Set("role", role)
	return c, w
}

// r2authTenantCtx attaches a middleware.TenantContext so GetTenantContext works.
func r2authTenantCtx(c *gin.Context, userID int, tenantID string) {
	c.Set("tenant_context", &middleware.TenantContext{
		TenantID: tenantID,
		UserID:   userID,
		Email:    "r2auth@test.local",
		Username: "r2authuser",
	})
	c.Set("tenant_id", tenantID)
	c.Set("user_id", userID)
	c.Set("id", userID)
}

func r2authBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	return m
}

func r2authAssertSuccess(t *testing.T, w *httptest.ResponseRecorder, want bool) map[string]interface{} {
	t.Helper()
	m := r2authBody(t, w)
	got, _ := m["success"].(bool)
	if got != want {
		t.Fatalf("success = %v, want %v (code=%d body=%s)", got, want, w.Code, w.Body.String())
	}
	return m
}

// r2authSessionRouter wires a cookie-session-backed engine and lets the caller
// register routes.
func r2authSessionRouter(register func(r *gin.Engine)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("r2auth-session-secret"))
	r.Use(sessions.Sessions("r2auth_session", store))
	register(r)
	return r
}

// ---------------------------------------------------------------------------
// misc.go
// ---------------------------------------------------------------------------

func TestR2Auth_TestStatus_OK(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authReq(http.MethodGet, "/api/status/test", nil)
	TestStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	m := r2authAssertSuccess(t, w, true)
	if m["message"] != "Server is running" {
		t.Errorf("message = %v, want 'Server is running'", m["message"])
	}
}

func TestR2Auth_GetStatus_ReturnsConfig(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authReq(http.MethodGet, "/api/status", nil)
	GetStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	m := r2authAssertSuccess(t, w, true)
	data, ok := m["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body=%s", w.Body.String())
	}
	// The status payload must advertise the login-method and registration config.
	if _, ok := data["login_methods"]; !ok {
		t.Errorf("data.login_methods missing")
	}
	if _, ok := data["registration"]; !ok {
		t.Errorf("data.registration missing")
	}
}

func TestR2Auth_SimpleOptionEndpoints(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	common.OptionMapRWMutex.Lock()
	common.OptionMap["Notice"] = "notice-body"
	common.OptionMap["About"] = "about-body"
	common.OptionMap["Midjourney"] = "mj-body"
	common.OptionMap["HomePageContent"] = "home-body"
	common.OptionMapRWMutex.Unlock()

	cases := []struct {
		name    string
		handler gin.HandlerFunc
		want    string
	}{
		{"notice", GetNotice, "notice-body"},
		{"about", GetAbout, "about-body"},
		{"midjourney", GetMidjourney, "mj-body"},
		{"homepage", GetHomePageContent, "home-body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := r2authReq(http.MethodGet, "/api/"+tc.name, nil)
			tc.handler(c)
			if w.Code != http.StatusOK {
				t.Fatalf("code = %d, want 200", w.Code)
			}
			m := r2authAssertSuccess(t, w, true)
			if m["data"] != tc.want {
				t.Errorf("data = %v, want %q", m["data"], tc.want)
			}
		})
	}
}

func TestR2Auth_LegalEndpoints(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"agreement", GetUserAgreement},
		{"privacy", GetPrivacyPolicy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w := r2authReq(http.MethodGet, "/api/"+tc.name, nil)
			tc.handler(c)
			if w.Code != http.StatusOK {
				t.Fatalf("code = %d, want 200", w.Code)
			}
			m := r2authAssertSuccess(t, w, true)
			if _, ok := m["data"]; !ok {
				t.Errorf("data key missing")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// user.go
// ---------------------------------------------------------------------------

func TestR2Auth_GetAllUsers(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authSession(http.MethodGet, "/api/user/?p=1&size=10", nil, ctx.RootUser.Id, common.RoleRootUser)
	GetAllUsers(c)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	m := r2authAssertSuccess(t, w, true)
	if _, ok := m["data"]; !ok {
		t.Errorf("data (page) missing")
	}
}

func TestR2Auth_SearchUsers(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authSession(http.MethodGet, "/api/user/search?keyword=v2test&status=1", nil, ctx.RootUser.Id, common.RoleRootUser)
	SearchUsers(c)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	r2authAssertSuccess(t, w, true)
}

func TestR2Auth_GetUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	t.Run("success_root_reads_normal", func(t *testing.T) {
		c, w := r2authSession(http.MethodGet, "/api/user/"+strconv.Itoa(ctx.NormalUser.Id), nil, ctx.RootUser.Id, common.RoleRootUser)
		c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ctx.NormalUser.Id)}}
		GetUser(c)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", w.Code)
		}
		m := r2authAssertSuccess(t, w, true)
		data, _ := m["data"].(map[string]interface{})
		if data == nil || int(data["id"].(float64)) != ctx.NormalUser.Id {
			t.Errorf("data.id mismatch, body=%s", w.Body.String())
		}
	})

	t.Run("permission_denied", func(t *testing.T) {
		// A common user may not read a root user's record.
		c, w := r2authSession(http.MethodGet, "/api/user/x", nil, ctx.NormalUser.Id, common.RoleCommonUser)
		c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ctx.RootUser.Id)}}
		GetUser(c)
		m := r2authAssertSuccess(t, w, false)
		if m["message"] == "" {
			t.Errorf("expected denial message")
		}
	})

	t.Run("invalid_id", func(t *testing.T) {
		c, w := r2authSession(http.MethodGet, "/api/user/abc", nil, ctx.RootUser.Id, common.RoleRootUser)
		c.Params = gin.Params{{Key: "id", Value: "not-a-number"}}
		GetUser(c)
		r2authAssertSuccess(t, w, false)
	})

	t.Run("not_found", func(t *testing.T) {
		c, w := r2authSession(http.MethodGet, "/api/user/999999", nil, ctx.RootUser.Id, common.RoleRootUser)
		c.Params = gin.Params{{Key: "id", Value: "999999"}}
		GetUser(c)
		r2authAssertSuccess(t, w, false)
	})
}

func TestR2Auth_GenerateAccessToken(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authSession(http.MethodGet, "/api/user/token", nil, ctx.NormalUser.Id, common.RoleCommonUser)
	GenerateAccessToken(c)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	m := r2authAssertSuccess(t, w, true)
	tok, _ := m["data"].(string)
	if tok == "" {
		t.Errorf("expected non-empty access token, body=%s", w.Body.String())
	}
}

func TestR2Auth_GetSelf(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	roles := []int{common.RoleRootUser, common.RoleAdminUser, common.RoleCommonUser}
	users := []int{ctx.RootUser.Id, ctx.AdminUser.Id, ctx.NormalUser.Id}
	for i := range roles {
		c, w := r2authSession(http.MethodGet, "/api/user/self", nil, users[i], roles[i])
		GetSelf(c)
		if w.Code != http.StatusOK {
			t.Fatalf("role %d: code = %d, want 200", roles[i], w.Code)
		}
		m := r2authAssertSuccess(t, w, true)
		data, _ := m["data"].(map[string]interface{})
		if data == nil || int(data["id"].(float64)) != users[i] {
			t.Errorf("role %d: data.id mismatch body=%s", roles[i], w.Body.String())
		}
		if _, ok := data["permissions"]; !ok {
			t.Errorf("role %d: permissions missing", roles[i])
		}
	}
}

func TestR2Auth_CalculateUserPermissionsAndSidebar(t *testing.T) {
	for _, role := range []int{common.RoleRootUser, common.RoleAdminUser, common.RoleCommonUser} {
		perms := calculateUserPermissions(role)
		if _, ok := perms["sidebar_settings"]; !ok {
			t.Errorf("role %d: sidebar_settings missing", role)
		}
		cfg := generateDefaultSidebarConfig(role)
		if cfg == "" {
			t.Errorf("role %d: empty sidebar config", role)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(cfg), &parsed); err != nil {
			t.Errorf("role %d: sidebar config not valid JSON: %v", role, err)
		}
		if _, ok := parsed["chat"]; !ok {
			t.Errorf("role %d: chat section missing", role)
		}
	}
}

func TestR2Auth_GetUserModels(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authSession(http.MethodGet, "/api/user/models", nil, ctx.NormalUser.Id, common.RoleCommonUser)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ctx.NormalUser.Id)}}
	GetUserModels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	r2authAssertSuccess(t, w, true)
}

func TestR2Auth_UpdateSelf(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	t.Run("sidebar_modules_path", func(t *testing.T) {
		c, w := r2authSession(http.MethodPut, "/api/user/self",
			map[string]interface{}{"sidebar_modules": `{"chat":{"enabled":true}}`},
			ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateSelf(c)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
	})

	t.Run("display_name_path", func(t *testing.T) {
		c, w := r2authSession(http.MethodPut, "/api/user/self",
			map[string]interface{}{"username": "renamed01", "display_name": "Renamed"},
			ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateSelf(c)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
	})

	t.Run("invalid_json", func(t *testing.T) {
		c, w := r2authSession(http.MethodPut, "/api/user/self", "{not-json", ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateSelf(c)
		r2authAssertSuccess(t, w, false)
	})
}

func TestR2Auth_UpdateUserSetting(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	call := func(body interface{}) *httptest.ResponseRecorder {
		c, w := r2authSession(http.MethodPut, "/api/user/setting", body, ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateUserSetting(c)
		return w
	}

	t.Run("invalid_json", func(t *testing.T) {
		r2authAssertSuccess(t, call("{bad"), false)
	})
	t.Run("invalid_notify_type", func(t *testing.T) {
		r2authAssertSuccess(t, call(map[string]interface{}{"notify_type": "carrier-pigeon", "quota_warning_threshold": 1.0}), false)
	})
	t.Run("threshold_not_positive", func(t *testing.T) {
		r2authAssertSuccess(t, call(map[string]interface{}{"notify_type": "email", "quota_warning_threshold": 0}), false)
	})
	t.Run("webhook_missing_url", func(t *testing.T) {
		r2authAssertSuccess(t, call(map[string]interface{}{"notify_type": "webhook", "quota_warning_threshold": 1.0}), false)
	})
	t.Run("webhook_invalid_url", func(t *testing.T) {
		r2authAssertSuccess(t, call(map[string]interface{}{"notify_type": "webhook", "quota_warning_threshold": 1.0, "webhook_url": "://bad"}), false)
	})
	t.Run("bark_missing_url", func(t *testing.T) {
		r2authAssertSuccess(t, call(map[string]interface{}{"notify_type": "bark", "quota_warning_threshold": 1.0}), false)
	})
	t.Run("bark_bad_scheme", func(t *testing.T) {
		r2authAssertSuccess(t, call(map[string]interface{}{"notify_type": "bark", "quota_warning_threshold": 1.0, "bark_url": "ftp://example.com"}), false)
	})
	t.Run("gotify_missing_token", func(t *testing.T) {
		r2authAssertSuccess(t, call(map[string]interface{}{"notify_type": "gotify", "quota_warning_threshold": 1.0, "gotify_url": "https://g.example.com"}), false)
	})
	t.Run("email_success", func(t *testing.T) {
		w := call(map[string]interface{}{"notify_type": "email", "quota_warning_threshold": 5.0, "notification_email": "alert@example.com"})
		if w.Code != http.StatusOK {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
	})
	t.Run("webhook_success", func(t *testing.T) {
		w := call(map[string]interface{}{"notify_type": "webhook", "quota_warning_threshold": 5.0, "webhook_url": "https://hook.example.com/x", "webhook_secret": "s3cr3t"})
		r2authAssertSuccess(t, w, true)
	})
	t.Run("gotify_success", func(t *testing.T) {
		w := call(map[string]interface{}{"notify_type": "gotify", "quota_warning_threshold": 5.0, "gotify_url": "https://g.example.com", "gotify_token": "tok", "gotify_priority": 99})
		r2authAssertSuccess(t, w, true)
	})
}

func TestR2Auth_Logout(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authSession(http.MethodPost, "/api/user/logout", nil, ctx.NormalUser.Id, common.RoleCommonUser)
	Logout(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	r2authAssertSuccess(t, w, true)
}

// ---------------------------------------------------------------------------
// token.go
// ---------------------------------------------------------------------------

func TestR2Auth_GetTokenStatus(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "status-token")

	c, w := r2authReq(http.MethodGet, "/api/token/status", nil)
	c.Set("id", ctx.NormalUser.Id)
	c.Set("token_id", tok.Id)
	GetTokenStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	m := r2authBody(t, w)
	if m["object"] != "credit_summary" {
		t.Errorf("object = %v, want credit_summary", m["object"])
	}
	// ExpiredTime -1 (never) must be normalised to 0 in the response.
	if m["expires_at"].(float64) != 0 {
		t.Errorf("expires_at = %v, want 0", m["expires_at"])
	}
}

func TestR2Auth_GetTokenUsage(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "usage-token")

	t.Run("missing_auth_header", func(t *testing.T) {
		c, w := r2authReq(http.MethodGet, "/dashboard/billing/usage", nil)
		GetTokenUsage(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", w.Code)
		}
		r2authAssertSuccess(t, w, false)
	})

	t.Run("malformed_bearer", func(t *testing.T) {
		c, w := r2authReq(http.MethodGet, "/dashboard/billing/usage", nil)
		c.Request.Header.Set("Authorization", "Basic xyz")
		GetTokenUsage(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", w.Code)
		}
	})

	t.Run("unknown_key", func(t *testing.T) {
		c, w := r2authReq(http.MethodGet, "/dashboard/billing/usage", nil)
		c.Request.Header.Set("Authorization", "Bearer sk-nonexistentkey0000000000000000")
		GetTokenUsage(c)
		r2authAssertSuccess(t, w, false)
	})

	t.Run("valid_key", func(t *testing.T) {
		c, w := r2authReq(http.MethodGet, "/dashboard/billing/usage", nil)
		c.Request.Header.Set("Authorization", "Bearer sk-"+tok.Key)
		GetTokenUsage(c)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		m := r2authBody(t, w)
		if m["code"] != true {
			t.Errorf("code = %v, want true (body=%s)", m["code"], w.Body.String())
		}
	})
}

func TestR2Auth_AddToken(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	add := func(body interface{}) *httptest.ResponseRecorder {
		c, w := r2authSession(http.MethodPost, "/api/token/", body, ctx.NormalUser.Id, common.RoleCommonUser)
		c.Set("tenant_id", ctx.TenantID)
		AddToken(c)
		return w
	}

	t.Run("success", func(t *testing.T) {
		w := add(map[string]interface{}{"name": "tok-ok", "unlimited_quota": true, "expired_time": -1})
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
	})

	t.Run("name_too_long", func(t *testing.T) {
		w := add(map[string]interface{}{"name": strings.Repeat("x", 60), "unlimited_quota": true, "expired_time": -1})
		r2authAssertSuccess(t, w, false)
	})

	t.Run("negative_quota", func(t *testing.T) {
		w := add(map[string]interface{}{"name": "tok-neg", "unlimited_quota": false, "remain_quota": -5, "expired_time": -1})
		r2authAssertSuccess(t, w, false)
	})

	t.Run("expiry_out_of_range", func(t *testing.T) {
		w := add(map[string]interface{}{"name": "tok-exp", "unlimited_quota": true, "expired_time": int64(999999999999999)})
		r2authAssertSuccess(t, w, false)
	})

	t.Run("invalid_json", func(t *testing.T) {
		w := add("{bad-json")
		r2authAssertSuccess(t, w, false)
	})
}

func TestR2Auth_UpdateAndDeleteToken(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	t.Run("update_success", func(t *testing.T) {
		tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "upd-ok")
		c, w := r2authSession(http.MethodPut, "/api/token/",
			map[string]interface{}{"id": tok.Id, "name": "renamed", "unlimited_quota": true, "expired_time": -1, "status": common.TokenStatusEnabled},
			ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateToken(c)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
	})

	t.Run("update_status_only", func(t *testing.T) {
		tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "upd-status")
		c, w := r2authSession(http.MethodPut, "/api/token/?status_only=1",
			map[string]interface{}{"id": tok.Id, "name": "upd-status", "status": common.TokenStatusDisabled, "unlimited_quota": true, "expired_time": -1},
			ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateToken(c)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
	})

	t.Run("update_name_too_long", func(t *testing.T) {
		c, w := r2authSession(http.MethodPut, "/api/token/",
			map[string]interface{}{"id": 1, "name": strings.Repeat("y", 60), "unlimited_quota": true, "expired_time": -1},
			ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateToken(c)
		r2authAssertSuccess(t, w, false)
	})

	t.Run("update_not_found", func(t *testing.T) {
		c, w := r2authSession(http.MethodPut, "/api/token/",
			map[string]interface{}{"id": 999999, "name": "ghost", "unlimited_quota": true, "expired_time": -1},
			ctx.NormalUser.Id, common.RoleCommonUser)
		UpdateToken(c)
		r2authAssertSuccess(t, w, false)
	})

	t.Run("delete_success", func(t *testing.T) {
		tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "del-ok")
		c, w := r2authSession(http.MethodDelete, "/api/token/"+strconv.Itoa(tok.Id), nil, ctx.NormalUser.Id, common.RoleCommonUser)
		c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tok.Id)}}
		DeleteToken(c)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
	})
}

// ---------------------------------------------------------------------------
// v2_token.go — RotateTokenV2
// ---------------------------------------------------------------------------

func TestR2Auth_RotateTokenV2(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	t.Run("no_tenant_context", func(t *testing.T) {
		c, w := r2authReq(http.MethodPost, "/api/v2/t/tokens/1/rotate", nil)
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		RotateTokenV2(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", w.Code)
		}
	})

	t.Run("invalid_id", func(t *testing.T) {
		c, w := r2authReq(http.MethodPost, "/api/v2/t/tokens/x/rotate", nil)
		r2authTenantCtx(c, ctx.NormalUser.Id, ctx.TenantID)
		c.Params = gin.Params{{Key: "id", Value: "not-a-number"}}
		RotateTokenV2(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", w.Code)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		c, w := r2authReq(http.MethodPost, "/api/v2/t/tokens/999999/rotate", nil)
		r2authTenantCtx(c, ctx.NormalUser.Id, ctx.TenantID)
		c.Params = gin.Params{{Key: "id", Value: "999999"}}
		RotateTokenV2(c)
		if w.Code != http.StatusNotFound {
			t.Fatalf("code = %d, want 404", w.Code)
		}
	})

	t.Run("tenant_mismatch_forbidden", func(t *testing.T) {
		tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "rotate-mismatch")
		c, w := r2authReq(http.MethodPost, "/api/v2/t/tokens/id/rotate", nil)
		// Same user, but a different tenant in context → ownership check fails.
		r2authTenantCtx(c, ctx.NormalUser.Id, "some-other-tenant")
		c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tok.Id)}}
		RotateTokenV2(c)
		if w.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "rotate-ok")
		oldKey := tok.Key
		c, w := r2authReq(http.MethodPost, "/api/v2/t/tokens/id/rotate", nil)
		r2authTenantCtx(c, ctx.NormalUser.Id, ctx.TenantID)
		c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tok.Id)}}
		RotateTokenV2(c)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		m := r2authAssertSuccess(t, w, true)
		data, _ := m["data"].(map[string]interface{})
		newKey, _ := data["key"].(string)
		if newKey == "" || newKey == "sk-"+oldKey {
			t.Errorf("rotated key not changed: %q", newKey)
		}
		// Verify the persisted key actually changed.
		reloaded, err := repo.GetTokenByIds(tok.Id, ctx.NormalUser.Id)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if reloaded.Key == oldKey {
			t.Errorf("persisted key unchanged after rotate")
		}
	})
}

// ---------------------------------------------------------------------------
// option.go — UpdateOption
// ---------------------------------------------------------------------------

func TestR2Auth_UpdateOption(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	call := func(body interface{}) *httptest.ResponseRecorder {
		c, w := r2authSession(http.MethodPut, "/api/option/", body, ctx.RootUser.Id, common.RoleRootUser)
		UpdateOption(c)
		return w
	}

	t.Run("invalid_json", func(t *testing.T) {
		w := call("{not-json")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", w.Code)
		}
		r2authAssertSuccess(t, w, false)
	})

	t.Run("null_value_rejected", func(t *testing.T) {
		w := call(map[string]interface{}{"key": "Notice", "value": nil})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", w.Code)
		}
		r2authAssertSuccess(t, w, false)
	})

	t.Run("github_oauth_without_client_id", func(t *testing.T) {
		w := call(map[string]interface{}{"key": "GitHubOAuthEnabled", "value": "true"})
		r2authAssertSuccess(t, w, false)
	})

	t.Run("group_ratio_invalid", func(t *testing.T) {
		w := call(map[string]interface{}{"key": "GroupRatio", "value": "not-valid-json"})
		r2authAssertSuccess(t, w, false)
	})

	t.Run("string_success", func(t *testing.T) {
		w := call(map[string]interface{}{"key": "SystemName", "value": "R2AuthHub"})
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
		common.OptionMapRWMutex.RLock()
		got := common.OptionMap["SystemName"]
		common.OptionMapRWMutex.RUnlock()
		if got != "R2AuthHub" {
			t.Errorf("OptionMap[SystemName] = %q, want R2AuthHub", got)
		}
	})

	t.Run("bool_value_success", func(t *testing.T) {
		w := call(map[string]interface{}{"key": "DataExportEnabled", "value": true})
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", w.Code)
		}
		r2authAssertSuccess(t, w, true)
	})

	t.Run("float_value_success", func(t *testing.T) {
		w := call(map[string]interface{}{"key": "R2AuthNumericKey", "value": 3.5})
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", w.Code)
		}
		r2authAssertSuccess(t, w, true)
	})
}

// TestR2Auth_UpdateOption_GuardBranches exercises the per-key validation guards
// that reject enabling a login provider / ratio while its prerequisite config is
// missing. In the test harness all provider client IDs are empty, so every
// "enable" toggle must be refused with success=false.
func TestR2Auth_UpdateOption_GuardBranches(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	call := func(key string, value interface{}) *httptest.ResponseRecorder {
		c, w := r2authSession(http.MethodPut, "/api/option/",
			map[string]interface{}{"key": key, "value": value}, ctx.RootUser.Id, common.RoleRootUser)
		UpdateOption(c)
		return w
	}

	guardKeys := []struct {
		key   string
		value interface{}
	}{
		{"discord.enabled", "true"},
		{"oidc.enabled", "true"},
		{"LinuxDOOAuthEnabled", "true"},
		{"WeChatAuthEnabled", "true"},
		{"TurnstileCheckEnabled", "true"},
		{"TelegramOAuthEnabled", "true"},
		{"ImageRatio", "not-json"},
		{"AudioRatio", "not-json"},
		{"AudioCompletionRatio", "not-json"},
	}
	for _, gk := range guardKeys {
		t.Run(gk.key, func(t *testing.T) {
			w := call(gk.key, gk.value)
			// Guarded rejections respond 200 with success=false (business error).
			if w.Code != http.StatusOK {
				t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
			}
			r2authAssertSuccess(t, w, false)
		})
	}
}

// ---------------------------------------------------------------------------
// oauth.go — session-backed handlers
// ---------------------------------------------------------------------------

func TestR2Auth_OIDCLoginRedirect(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Slug of the enabled tenant seeded by SetupV2TestRouter.
	enabledSlug := ctx.RootUser.TenantId // tenant id == slug in the harness
	tenant, err := repo.GetTenantByID(ctx.TenantID)
	if err == nil {
		enabledSlug = tenant.Slug
	}

	// Seed a disabled tenant to exercise the 403 branch.
	disabled := &repo.Tenant{
		Id:     "r2auth-disabled",
		Name:   "Disabled",
		Slug:   "r2authdisabled",
		Status: repo.TenantStatusDisabled,
	}
	if err := ctx.DB.Create(disabled).Error; err != nil {
		t.Fatalf("seed disabled tenant: %v", err)
	}

	r := r2authSessionRouter(func(r *gin.Engine) {
		r.GET("/api/v2/:tenant_slug/auth/login", OIDCLoginRedirect)
	})

	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("invalid_slug_format", func(t *testing.T) {
		w := do("/api/v2/bad.slug!/auth/login")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("tenant_not_found", func(t *testing.T) {
		w := do("/api/v2/nosuchtenant/auth/login")
		if w.Code != http.StatusNotFound {
			t.Fatalf("code = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("tenant_disabled", func(t *testing.T) {
		w := do("/api/v2/r2authdisabled/auth/login")
		if w.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("invalid_redirect_url", func(t *testing.T) {
		w := do("/api/v2/" + enabledSlug + "/auth/login?redirect_url=//evil.example.com")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("success_redirects", func(t *testing.T) {
		w := do("/api/v2/" + enabledSlug + "/auth/login?register=true")
		if w.Code != http.StatusFound {
			t.Fatalf("code = %d, want 302 (body=%s)", w.Code, w.Body.String())
		}
		if w.Header().Get("Location") == "" {
			t.Errorf("expected Location header on redirect")
		}
	})
}

func TestR2Auth_GetSessionInfo(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := r2authSessionRouter(func(r *gin.Engine) {
		r.GET("/noauth", GetSessionInfo)
		r.GET("/authed", func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("id", ctx.NormalUser.Id)
			s.Set("tenant_slug", "r2auth-slug")
			_ = s.Save()
			GetSessionInfo(c)
		})
		r.GET("/badtype", func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("id", "not-an-int")
			_ = s.Save()
			GetSessionInfo(c)
		})
	})

	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("not_logged_in", func(t *testing.T) {
		w := do("/noauth")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", w.Code)
		}
	})

	t.Run("invalid_session_type", func(t *testing.T) {
		w := do("/badtype")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", w.Code)
		}
	})

	t.Run("authed", func(t *testing.T) {
		w := do("/authed")
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		m := r2authAssertSuccess(t, w, true)
		data, _ := m["data"].(map[string]interface{})
		if data == nil || int(data["id"].(float64)) != ctx.NormalUser.Id {
			t.Errorf("data.id mismatch, body=%s", w.Body.String())
		}
	})
}

func TestR2Auth_OIDCLogout(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := r2authSessionRouter(func(r *gin.Engine) {
		r.POST("/logout-plain", func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("id", ctx.NormalUser.Id)
			_ = s.Save()
			OIDCLogout(c)
		})
		r.POST("/logout-idp", func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("id", ctx.NormalUser.Id)
			s.Set("oauth_id_token", "fake.id.token")
			_ = s.Save()
			OIDCLogout(c)
		})
	})

	t.Run("no_id_token_returns_success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/logout-plain", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		r2authAssertSuccess(t, w, true)
	})

	t.Run("with_id_token_redirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/logout-idp", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusFound {
			t.Fatalf("code = %d, want 302 (body=%s)", w.Code, w.Body.String())
		}
	})
}

func TestR2Auth_ValidateIDToken_Error(t *testing.T) {
	// With no JWKS manager initialised, signature verification must fail —
	// exercises the error return of validateIDToken.
	if _, err := validateIDToken("not-a-real-jwt", ""); err == nil {
		t.Errorf("expected error validating a bogus ID token")
	}
}
