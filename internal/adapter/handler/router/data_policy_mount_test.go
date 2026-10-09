package router

// data_policy_mount_test.go - migration 050 REAL-CHAIN authorization: the
// platform content-rule / override-template routes are root-only and the
// tenant data-policy routes need an authenticated session, proven through the
// real SetApiV2Router route table (not a hand-mounted copy of the middleware).

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func dpMountDo(engine *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func TestDataPolicyRoutes_PlatformRoutesRootOnly(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v2/admin/content-rules"},
		{http.MethodPost, "/api/v2/admin/content-rules"},
		{http.MethodPut, "/api/v2/admin/content-rules/1"},
		{http.MethodDelete, "/api/v2/admin/content-rules/1"},
		{http.MethodGet, "/api/v2/admin/channel-templates"},
		{http.MethodPost, "/api/v2/admin/channel-templates"},
		{http.MethodPut, "/api/v2/admin/channel-templates/1"},
		{http.MethodDelete, "/api/v2/admin/channel-templates/1"},
		{http.MethodPost, "/api/v2/admin/channel-templates/1/apply"},
	}
	t.Run("anonymous_401", func(t *testing.T) {
		engine := systemTasksMountRouter(t, -1)
		for _, r := range routes {
			if w := dpMountDo(engine, r.method, r.path); w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s anonymous: status=%d, want 401; body=%s", r.method, r.path, w.Code, w.Body)
			}
		}
	})
	t.Run("tenant_admin_role_refused", func(t *testing.T) {
		engine := systemTasksMountRouter(t, common.RoleAdminUser)
		for _, r := range routes {
			w := dpMountDo(engine, r.method, r.path)
			if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), `"data"`) {
				t.Errorf("%s %s admin (non-root): status=%d, want 403 and no data; body=%s", r.method, r.path, w.Code, w.Body)
			}
		}
	})
}

func TestDataPolicyRoutes_TenantRoutesNeedAuthentication(t *testing.T) {
	engine := systemTasksMountRouter(t, -1)
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v2/some-tenant/data-policy/retention"},
		{http.MethodPut, "/api/v2/some-tenant/data-policy/retention"},
		{http.MethodPut, "/api/v2/some-tenant/data-policy/tokens/1/retention"},
		{http.MethodGet, "/api/v2/some-tenant/data-policy/rules"},
		{http.MethodPost, "/api/v2/some-tenant/data-policy/rules"},
		{http.MethodPut, "/api/v2/some-tenant/data-policy/rules/1"},
		{http.MethodDelete, "/api/v2/some-tenant/data-policy/rules/1"},
	}
	for _, r := range routes {
		w := dpMountDo(engine, r.method, r.path)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Errorf("%s %s anonymous: status=%d, want 401/403 (route missing or unauthenticated); body=%s", r.method, r.path, w.Code, w.Body)
		}
	}
}
