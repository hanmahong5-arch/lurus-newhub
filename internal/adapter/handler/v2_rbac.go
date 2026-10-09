package handler

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// isPlatformStaff reports whether the caller is one of OUR people: a session /
// access-token user whose integer role is >= RoleAdminUser, or an OIDC-JWT user
// carrying the "admin"/"root" string role. It deliberately ignores the
// tenant-scoped users.tenant_role — a customer's admin is never platform staff,
// so supply-chain detail (channel names, TierInternal log keys) stays hidden
// from them.
//
// On the OIDC-JWT path only "root" is staff. "admin" is the tenant-scoped role
// (as in requirePlatformRoot): an IdP that issues it to a customer organisation
// must not hand that organisation channel names and supply-chain endpoints.
// requireTenantAdmin still accepts it as a tenant admin.
func isPlatformStaff(c *gin.Context, tc *middleware.TenantContext) bool {
	if c.GetInt("role") >= common.RoleAdminUser { // session / access-token path
		return true
	}
	// OIDC-JWT path: roles arrive as strings extracted from the token claims.
	return tc != nil && hasRole(tc.Roles, "root")
}

// tenantRoleOf returns the caller's tenant-scoped role, honoured only when the
// user row belongs to the tenant the request is scoped to. The shared bootstrap
// tenant "default" hosts every unassigned user, so a role stamped there is never
// honoured: tenant-scoped admin has no meaning outside a real customer tenant.
func tenantRoleOf(tc *middleware.TenantContext) string {
	if tc == nil || tc.RoleTenantID == "" || tc.RoleTenantID != tc.TenantID || tc.TenantID == "default" {
		return entity.TenantRoleNone
	}
	return tc.TenantRole
}

// requireTenantAdmin reports whether the caller holds tenant-admin privilege:
// platform staff (see isPlatformStaff — this mirrors middleware.AdminJWTAuth's
// dual gate so session-authenticated admins, whose TenantContext.Roles is always
// empty, are not locked out when a v2 route is mounted under UserAuth()), OR a
// user whose tenant_role is "admin" inside their own tenant. The tenant_role arm
// grants nothing outside v2 handlers that call this function: it never touches
// the integer role, so v1 AdminAuth routes stay closed to customer admins.
//
// Reusable across v2 handlers that must enforce tenant-admin inside the handler body.
func requireTenantAdmin(c *gin.Context, tc *middleware.TenantContext) bool {
	if isPlatformStaff(c, tc) {
		return true
	}
	if tc != nil && hasRole(tc.Roles, "admin") { // OIDC tenant-scoped admin
		return true
	}
	return tenantRoleOf(tc) == entity.TenantRoleAdmin
}

// allowedProjectIDs resolves the caller's project data scope. all=true means
// unrestricted inside the tenant (platform staff and tenant admins). Otherwise
// ids lists the projects a dept_lead belongs to; any other caller gets
// (false, nil) and keeps the plain per-user view. A membership lookup failure
// also yields an empty scope (fail closed). Callers MUST read (false, nil) as
// "own rows only" — never as "no project filter"; plain members, dept_leads
// with zero projects and lookup failures are deliberately indistinguishable.
func allowedProjectIDs(c *gin.Context, tc *middleware.TenantContext) (all bool, ids []int) {
	if requireTenantAdmin(c, tc) {
		return true, nil
	}
	if tenantRoleOf(tc) != entity.TenantRoleDeptLead {
		return false, nil
	}
	got, err := repo.ListProjectIDsForUser(tc.TenantID, tc.UserID)
	if err != nil {
		common.SysError("allowedProjectIDs: membership lookup failed: " + err.Error())
		return false, nil
	}
	return false, got
}

// requirePlatformRoot is requireTenantAdmin's stricter sibling for v2 routes that
// are mounted under /:tenant_slug but whose write lands on PLATFORM-GLOBAL state
// (the process-wide ratio maps, the single-row option table, the tenant-less model
// catalogue). The tenant in the URL is route decoration there, not a blast radius:
// letting one tenant's admin through would let them reprice or delete models for
// every tenant. v1 puts the same writes behind middleware.RootAuth() — this keeps
// v2 at that level.
//
// Unlike requireTenantAdmin, the OIDC-JWT path accepts "root" ONLY: "admin" is the
// tenant-scoped role and must not reach a global write.
func requirePlatformRoot(c *gin.Context, tc *middleware.TenantContext) bool {
	if c.GetInt("role") >= common.RoleRootUser { // session / access-token path
		return true
	}
	return tc != nil && hasRole(tc.Roles, "root")
}
