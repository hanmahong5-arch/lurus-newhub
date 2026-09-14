package middleware

import (
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// RootOrGranted gates a route to root OR a session-authenticated admin who
// holds an ACTIVE, GLOBAL delegated permission grant naming
// (resource, action) — L4 (auth-security-17/18, console-ux-36). Confined to
// the four audit-feed GETs this cycle (auditRoute in api-v2-router.go); a
// deliberate exception to the "adminRoute is RootJWTAuth, full stop"
// convention every other admin route follows.
//
// Session path (no Authorization header): resolveSessionIdentity re-runs
// the same per-request status/role re-validation authHelper performs
// (disabled user, demoted role, revoked session, disabled tenant — see
// auth.go, PR #167) — RootOrGranted never bypasses it. Root (role >= RoleRootUser)
// passes unconditionally; a lower role needs a live row in
// admin_permission_grants (repo.HasActivePermissionGrant, direct DB read,
// no cache — small table, admin QPS) or gets 403 PERMISSION_DENIED.
//
// Bearer-JWT path: behaves EXACTLY like RootJWTAuth's JWT branch (root
// only, admin_jwt_auth.go). RootJWTAuth's JWT branch never populates the
// "id" context key a grant lookup needs (cycle-7 §8 L2 finding), so
// delegated grants are a SESSION-PATH-ONLY feature this cycle — a caller
// presenting a valid admin (non-root) JWT gets the same 403 a non-root JWT
// always got on this subtree, never a grant check. See
// TestRootOrGranted_BearerJWTNonRoot403.
func RootOrGranted(resource, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			if !resolveSessionIdentity(c, common.RoleAdminUser) {
				return
			}
			rootOrGrantedDecision(c, resource, action)
			return
		}

		// A Bearer token is present. Mirror RootJWTAuth's own fallback: when
		// OIDC/JWKS isn't configured, a bearer header cannot be validated as
		// a JWT, so fall back to the session path but require root outright
		// (no grant check — same reasoning as the JWT branch below).
		if !oidcEnabled || jwksManager == nil {
			if !resolveSessionIdentity(c, common.RoleRootUser) {
				return
			}
			c.Next()
			return
		}

		claims, err := validateJWT(c)
		if err != nil {
			common.SysError(fmt.Sprintf("RootOrGranted: token validation failed: %v", err))
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "unauthorized",
			})
			c.Abort()
			return
		}

		roles := extractRoles(claims.Roles)
		if !hasRole(roles, "root") {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "Root role required",
			})
			c.Abort()
			return
		}

		c.Set("admin_sub", claims.Subject)
		c.Set("admin_email", claims.Email)
		c.Set("admin_roles", roles)
		c.Next()
	}
}

// rootOrGrantedDecision runs the root-or-grant branch shared by the session
// path above — split out so the "grant lookup fails closed" logic has one
// call site to mutate under the lane's mutation test.
func rootOrGrantedDecision(c *gin.Context, resource, action string) {
	if c.GetInt("role") >= common.RoleRootUser {
		c.Next()
		return
	}
	granted, err := repo.HasActivePermissionGrant(c.GetInt("id"), resource, action)
	if err != nil || !granted {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "insufficient permission",
			"error_code": "PERMISSION_DENIED",
		})
		c.Abort()
		return
	}
	c.Next()
}
