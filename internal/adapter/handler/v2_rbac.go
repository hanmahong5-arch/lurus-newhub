package handler

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// requireTenantAdmin reports whether the caller holds tenant-admin privilege via
// EITHER auth path: a session/access-token user whose integer role is >= RoleAdminUser,
// OR a OIDC-JWT user carrying the "admin"/"root" string role. It mirrors
// middleware.AdminJWTAuth's dual gate so that session-authenticated admins — whose
// TenantContext.Roles is always empty (see middleware.authHelper) — are not locked out
// when a v2 route is mounted under UserAuth() rather than a JWT admin middleware.
//
// Reusable across v2 handlers that must enforce tenant-admin inside the handler body.
func requireTenantAdmin(c *gin.Context, tc *middleware.TenantContext) bool {
	if c.GetInt("role") >= common.RoleAdminUser { // session / access-token path
		return true
	}
	// OIDC-JWT path: roles arrive as strings extracted from the token claims.
	return tc != nil && (hasRole(tc.Roles, "admin") || hasRole(tc.Roles, "root"))
}
