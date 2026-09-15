// Package authz holds the static delegated-permission catalogue consulted
// by middleware.RootOrGranted and the grant-management handlers
// (v2_admin_authz.go). It is a read-only registry, not a role editor:
// upstream New API ships two fixed roles plus a resource registry, and this
// cycle's parity target (auth-security-17/18, console-ux-36) mirrors that
// shape rather than building a general permission-table UI.
package authz

// CatalogEntry is one resource and the actions a grant may name against it.
type CatalogEntry struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

// catalog is the full set of resource/action pairs a grant may target this
// cycle. Extending it (e.g. cycle 9's channel "sensitive_write", plan §5)
// is a code change here, not a migration — the grants table itself is
// resource/action agnostic.
var catalog = map[string][]string{
	"audit": {"read"},
	// channel:sensitive_write (cycle 9, L2) — lets a non-root admin holding
	// this grant swap a channel's key/base_url/param_override/
	// header_override/proxy setting; enforced in-handler by
	// internal/adapter/handler/channel_sensitive_write.go, not by a router
	// group like audit:read.
	"channel": {"sensitive_write"},
}

// Catalog returns the static catalogue as an ordered slice (stable output
// for the GET /api/v2/admin/authz/catalog response).
func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{Resource: "audit", Actions: []string{"read"}},
		{Resource: "channel", Actions: []string{"sensitive_write"}},
	}
}

// IsValid reports whether (resource, action) is a grantable pair this
// cycle — the 400 GRANT_INVALID gate in v2_admin_authz.go's create handler.
func IsValid(resource, action string) bool {
	actions, ok := catalog[resource]
	if !ok {
		return false
	}
	for _, a := range actions {
		if a == action {
			return true
		}
	}
	return false
}

// Role is one of the two fixed permission tiers the catalogue endpoint
// surfaces alongside the resource/action list — mirrors
// common.RoleAdminUser (10) / common.RoleRootUser (100), duplicated here as
// plain ints (not an import of internal/pkg/common) to keep this package
// dependency-free; a change to those constants without an update here is
// caught by TestCatalog_RolesMatchCommonConstants.
type Role struct {
	Name    string `json:"name"`
	MinRole int    `json:"min_role"`
}

// Roles returns the two fixed roles the catalogue endpoint surfaces. This
// cycle ships no per-user role editor — grants ADD a narrow permission on
// top of these two tiers, they do not replace them.
func Roles() []Role {
	return []Role{
		{Name: "tenant-admin", MinRole: 10},
		{Name: "root", MinRole: 100},
	}
}
