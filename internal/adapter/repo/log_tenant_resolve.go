package repo

// log_tenant_resolve.go — pure move out of log.go (2026-09-28, source-size
// ratchet): the tenant stamp every consume/error log row carries.

// resolveLogTenantID picks the tenant to stamp on a log row being written:
// the gin context's tenant_id when present (v1 session auth and the v2
// tenant-slug middleware both set it), otherwise the owning user's tenant.
// The fallback matters on the plain /v1 relay path: TokenAuth injects no
// tenant context, so before this fallback every such row was silently
// stamped 'default' even when the token belonged to another tenant —
// polluting the default tenant's log views and hiding the rows from the
// owning tenant's. System rows (userId 0) keep the 'default' stamp.
func resolveLogTenantID(ginTenantID string, userId int) string {
	if ginTenantID != "" {
		return ginTenantID
	}
	if userId > 0 {
		if uc, err := GetUserCache(userId); err == nil && uc.TenantId != "" {
			return uc.TenantId
		}
	}
	return "default"
}
