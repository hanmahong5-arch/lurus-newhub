package handler

// ============================================================================
// v2_sessions.go (ListSessionsV2) gaps beyond v2_sessions_test.go's happy
// path / tenant-isolation / whitelist tests: unknown tenant slug,
// unauthenticated caller, and the auth_method context override. Reuses
// setupSessionsRouter (v2_sessions_test.go, same package).
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestHandlerIdentityLog_ListSessionsV2_UnknownTenantSlug proves a slug that
// doesn't resolve to any tenant 404s instead of falling through to a
// synthetic session for an unscoped/default tenant.
func TestHandlerIdentityLog_ListSessionsV2_UnknownTenantSlug(t *testing.T) {
	ctx := setupSessionsRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/no-such-tenant-slug/sessions", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	body := parseSessions(t, w)
	if success, _ := body["success"].(bool); success {
		t.Errorf("success = true, want false")
	}
	if body["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error_code = %v, want TENANT_NOT_FOUND", body["error_code"])
	}
}

// TestHandlerIdentityLog_ListSessionsV2_Unauthenticated proves a request that
// resolved a tenant but never resolved a caller id is refused rather than
// leaking a synthetic session with active_tokens/request_count counted
// against user id 0.
func TestHandlerIdentityLog_ListSessionsV2_Unauthenticated(t *testing.T) {
	ctx := setupSessionsRouter(t)

	// A second router mounted without the id-setting mock auth: the handler
	// itself must be the one rejecting id==0, not the middleware.
	r := gin.New()
	r.GET("/api/v2/:tenant_slug/sessions", ListSessionsV2)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/"+ctx.slug+"/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
	}
	body := parseSessions(t, w)
	if body["error_code"] != "UNAUTHENTICATED" {
		t.Errorf("error_code = %v, want UNAUTHENTICATED", body["error_code"])
	}
}

// TestHandlerIdentityLog_ListSessionsV2_AuthMethodOverride proves that when
// upstream OIDC middleware stamped an auth_method on the context, that value
// is surfaced verbatim instead of the "session" fallback used by cookie auth.
func TestHandlerIdentityLog_ListSessionsV2_AuthMethodOverride(t *testing.T) {
	ctx := setupSessionsRouter(t)

	r := gin.New()
	r.GET("/api/v2/:tenant_slug/sessions", func(c *gin.Context) {
		c.Set("id", ctx.userID)
		c.Set("auth_method", "oidc_jwt")
		c.Next()
	}, ListSessionsV2)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/"+ctx.slug+"/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	body := parseSessions(t, w)
	items := body["data"].(map[string]interface{})["items"].([]interface{})
	item := items[0].(map[string]interface{})
	if item["auth_method"] != "oidc_jwt" {
		t.Errorf("auth_method = %v, want oidc_jwt (context override ignored)", item["auth_method"])
	}
}
