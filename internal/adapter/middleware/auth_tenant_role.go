package middleware

// auth_tenant_role.go — tenant-scoped role plumbing for the session path of
// resolveSessionIdentity (auth.go). Pure extraction: the TenantContext literal
// and its two nil-safe cache accessors used to sit inline in auth.go, which is
// under the source-size ratchet.

import "github.com/LurusTech/lurus-hub/internal/adapter/repo"

// sessionTenantContext builds the structured TenantContext for a session or
// access-token request. TenantRole (migration 046) is additive: it is never
// read as the integer role, and handlers honour it only when RoleTenantID
// equals TenantID; a failed cache lookup leaves both empty (fail closed).
func sessionTenantContext(tenantID string, userID int, email, username string, u *repo.UserBase, cacheErr error) *TenantContext {
	return &TenantContext{
		TenantID:     tenantID,
		UserID:       userID,
		Email:        email,
		Username:     username,
		Roles:        []string{},
		TenantRole:   cachedTenantRole(u, cacheErr),
		RoleTenantID: cachedRoleTenantID(u, cacheErr),
	}
}

// cachedTenantRole returns the tenant-scoped role from a resolved user cache
// entry, or "" when the lookup failed (fail closed: no tenant privilege).
func cachedTenantRole(u *repo.UserBase, err error) string {
	if err != nil || u == nil {
		return ""
	}
	return u.TenantRole
}

// cachedRoleTenantID returns the tenant the cached user row belongs to, or ""
// on a failed lookup. TenantContext.RoleTenantID is compared to TenantID before
// the tenant role is honoured.
func cachedRoleTenantID(u *repo.UserBase, err error) string {
	if err != nil || u == nil {
		return ""
	}
	return u.TenantId
}
