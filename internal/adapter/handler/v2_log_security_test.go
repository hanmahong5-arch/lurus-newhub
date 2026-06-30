package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// TestSecurityLogsAllRequiresAdmin locks the contract that GET /logs/all — which
// returns every tenant member's consumption logs (GetTenantLogsWithParams, no
// user_id filter) — is reachable only by a tenant admin. Before this guard any
// authenticated tenant member could read the whole tenant's logs despite the
// handler's "admin only" doc comment.
func TestSecurityLogsAllRequiresAdmin(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	path := "/api/v2/" + ctx.TenantID + "/logs/all"

	// Non-admin (common user, no string roles) must be rejected.
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, path, nil, nil)
	AssertV2Status(t, w, http.StatusForbidden)

	// Session-integer admin role (TenantContext.Roles empty) must pass.
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, nil)
	AssertV2Status(t, w, http.StatusOK)

	// OIDC-JWT string "admin" role must pass even for a common-role user.
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, path, nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
}

// TestSecurityLogsClusterStaysUserScoped documents the deliberate decision NOT to
// gate GET /logs/cluster behind tenant-admin: GetLogClusterV2 filters on the caller's
// own user_id, so a normal member viewing their own analytics must keep working.
// Gating it would be a regression with no tenant-isolation benefit. This test fails
// if someone later adds a requireTenantAdmin guard to the cluster endpoint.
func TestSecurityLogsClusterStaysUserScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	path := "/api/v2/" + ctx.TenantID + "/logs/cluster"
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, path, nil, nil)
	AssertV2Status(t, w, http.StatusOK)
}

// TestSecurityRequireTenantAdmin exercises requireTenantAdmin's dual gate directly,
// covering the session-integer-role branch that the HTTP harness cannot vary
// independently of the string-role branch.
func TestSecurityRequireTenantAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name  string
		role  int
		roles []string
		tcNil bool
		want  bool
	}{
		{"session admin role", common.RoleAdminUser, nil, false, true},
		{"session root role", common.RoleRootUser, nil, false, true},
		{"session common user", common.RoleCommonUser, nil, false, false},
		{"jwt admin string role", common.RoleCommonUser, []string{"admin"}, false, true},
		{"jwt root string role", common.RoleCommonUser, []string{"root"}, false, true},
		{"jwt role unset admin string", 0, []string{"admin"}, false, true},
		{"common user empty roles", common.RoleCommonUser, []string{}, false, false},
		{"nil tenant context common role", common.RoleCommonUser, nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("role", tc.role)
			var tctx *middleware.TenantContext
			if !tc.tcNil {
				tctx = &middleware.TenantContext{Roles: tc.roles}
			}
			if got := requireTenantAdmin(c, tctx); got != tc.want {
				t.Errorf("requireTenantAdmin(role=%d, roles=%v, tcNil=%v) = %v, want %v",
					tc.role, tc.roles, tc.tcNil, got, tc.want)
			}
		})
	}
}
