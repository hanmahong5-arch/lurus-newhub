package entity

// AdminPermissionGrant is one delegated permission: an admin-role user
// (role >= RoleAdminUser, < RoleRootUser) who holds an ACTIVE row naming
// (their own UserId, Resource, Action) may pass middleware.RootOrGranted
// for that resource/action without holding root. Root always passes
// RootOrGranted regardless of any row here.
//
// Grants are GLOBAL this cycle (auth-security-17/18, console-ux-36,
// cycle-8 plan §8 L4 / O5 amendment): TenantId is accepted only as NULL by
// the write handler (v2_admin_authz.go) — the column exists so a future
// cycle's tenant-scoped grants need no second migration, but nothing this
// cycle ever writes a non-NULL value, and repo.HasActivePermissionGrant
// only ever looks up the global (NULL tenant) row.
//
// Schema is managed by migration 034 (admin_permission_grants) plus
// AutoMigrate (repo.migrateDB) for the bare table + the plain user_id
// index. The PARTIAL unique index (user_id, COALESCE(tenant_id, empty-string),
// resource, action) WHERE revoked_at IS NULL — "one active grant per
// user+resource+action" — is created ONLY by the SQL migration: GORM
// cannot express a partial index via struct tags, and a full-table unique
// tag here would create a CONFLICTING plain unique index on every boot
// (the 031 lesson — struct tag and SQL index shape must agree). Do not add
// a `uniqueIndex` tag to UserId/TenantId/Resource/Action below.
type AdminPermissionGrant struct {
	Id     int `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId int `json:"user_id" gorm:"not null;index:idx_admin_permission_grants_user"`
	// TenantId is a pointer so an absent value serializes as JSON null and
	// round-trips through GORM as SQL NULL, matching the partial index's
	// COALESCE(tenant_id,'') semantics — an empty string and NULL must stay
	// distinguishable at the SQL layer even though this cycle only ever
	// writes NULL.
	TenantId  *string `json:"tenant_id" gorm:"type:varchar(36)"`
	Resource  string  `json:"resource" gorm:"type:varchar(64);not null"`
	Action    string  `json:"action" gorm:"type:varchar(32);not null"`
	GrantedBy int     `json:"granted_by" gorm:"not null"`
	CreatedAt int64   `json:"created_at" gorm:"not null"`
	RevokedAt *int64  `json:"revoked_at"`
}

// TableName overrides the default GORM table name.
func (AdminPermissionGrant) TableName() string {
	return "admin_permission_grants"
}
