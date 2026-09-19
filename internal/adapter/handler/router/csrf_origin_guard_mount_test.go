package router

// csrf_origin_guard_mount_test.go — cycle-12 L4 hand-off. The middleware's own
// decision table is pinned in middleware/browser_origin_guard_test.go; this
// file proves the guard is actually ON the route table production serves, and
// that it runs BEFORE auth (the refusal carries CROSS_SITE_REQUEST, not the
// auth layer's UNAUTHENTICATED/PERMISSION_DENIED).
//
// Mutation target: removing apiV2.Use(middleware.BrowserOriginGuard()) from
// api-v2-router.go turns same_site_cookie_post_refused red.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func csrfMountRequest(t *testing.T, engine *gin.Engine, method, path string, withCookie bool, fetchSite string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if withCookie {
		req.AddCookie(&http.Cookie{Name: "session", Value: "csrf-mount-test"})
	}
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func csrfMountErrorCode(w *httptest.ResponseRecorder) string {
	var env map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		return ""
	}
	code, _ := env["error_code"].(string)
	return code
}

func TestBrowserOriginGuard_MountedOnApiV2(t *testing.T) {
	engine := mountRealRouterWithSession(t, map[string]interface{}{
		"username": "csrf-mount-actor",
		"role":     common.RoleRootUser,
		"id":       1,
		"status":   common.UserStatusEnabled,
	})
	const path = "/api/v2/admin/tenants"

	t.Run("same_site_cookie_post_refused", func(t *testing.T) {
		w := csrfMountRequest(t, engine, http.MethodPost, path, true, "same-site")
		if w.Code != http.StatusForbidden || csrfMountErrorCode(w) != "CROSS_SITE_REQUEST" {
			t.Fatalf("status=%d error_code=%q, want 403 CROSS_SITE_REQUEST via the real router; body=%s", w.Code, csrfMountErrorCode(w), w.Body.String())
		}
	})

	t.Run("same_origin_not_refused_as_cross_site", func(t *testing.T) {
		w := csrfMountRequest(t, engine, http.MethodPost, path, true, "same-origin")
		if csrfMountErrorCode(w) == "CROSS_SITE_REQUEST" {
			t.Fatalf("same-origin POST refused as cross-site; body=%s", w.Body.String())
		}
	})

	t.Run("no_cookie_cross_site_not_refused", func(t *testing.T) {
		w := csrfMountRequest(t, engine, http.MethodPost, path, false, "cross-site")
		if csrfMountErrorCode(w) == "CROSS_SITE_REQUEST" {
			t.Fatalf("cookie-less cross-site POST refused — this carve-out is what keeps POST /api/v2/bridge/exchange working; body=%s", w.Body.String())
		}
	})

	t.Run("get_not_guarded", func(t *testing.T) {
		w := csrfMountRequest(t, engine, http.MethodGet, path, true, "cross-site")
		if csrfMountErrorCode(w) == "CROSS_SITE_REQUEST" {
			t.Fatalf("cross-site GET refused — the guard only covers state-changing methods; body=%s", w.Body.String())
		}
	})

	t.Run("mode_off_admits", func(t *testing.T) {
		t.Setenv("CSRF_ORIGIN_GUARD_MODE", "off")
		w := csrfMountRequest(t, engine, http.MethodPost, path, true, "same-site")
		if csrfMountErrorCode(w) == "CROSS_SITE_REQUEST" {
			t.Fatalf("CSRF_ORIGIN_GUARD_MODE=off still refused — the incident lever does not work on the real chain; body=%s", w.Body.String())
		}
	})
}
