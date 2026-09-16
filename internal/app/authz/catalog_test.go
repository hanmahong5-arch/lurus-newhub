package authz

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestCatalog_IsValid(t *testing.T) {
	cases := []struct {
		resource, action string
		want             bool
	}{
		{"audit", "read", true},
		{"audit", "write", false},
		{"channel", "sensitive_write", true},
		{"channel", "read", false},
		{"", "", false},
	}
	for _, tc := range cases {
		if got := IsValid(tc.resource, tc.action); got != tc.want {
			t.Errorf("IsValid(%q,%q) = %v, want %v", tc.resource, tc.action, got, tc.want)
		}
	}
}

func TestCatalog_ContainsAuditRead(t *testing.T) {
	entries := Catalog()
	found := false
	for _, e := range entries {
		if e.Resource != "audit" {
			continue
		}
		for _, a := range e.Actions {
			if a == "read" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("Catalog() = %+v, want an audit:read entry", entries)
	}
}

// TestCatalog_ContainsChannelSensitiveWrite pins the cycle-9 L2 addition —
// the catalogue is the only place a grant for channel:sensitive_write can
// come from, so a regression here would make the grant-management endpoint
// (v2_admin_authz.go's 400 GRANT_INVALID gate) reject every attempt to
// grant it, even though catalog.go's own IsValid would silently agree.
func TestCatalog_ContainsChannelSensitiveWrite(t *testing.T) {
	entries := Catalog()
	found := false
	for _, e := range entries {
		if e.Resource != "channel" {
			continue
		}
		for _, a := range e.Actions {
			if a == "sensitive_write" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("Catalog() = %+v, want a channel:sensitive_write entry", entries)
	}
}

// TestCatalog_SliceMatchesMap (R9, repair-round ruling / A-5) pins the two
// hand-maintained copies of the grantable set — the `catalog` map IsValid
// reads and the Catalog() slice GET /api/v2/admin/authz/catalog returns and
// web/src/pages/v2/Admin/Authz.jsx renders — to each other in both
// directions. Without this, a future entry added to only one copy yields
// either an action the console offers but POST /grants rejects with 400
// GRANT_INVALID, or a grantable action no admin can even see to request.
func TestCatalog_SliceMatchesMap(t *testing.T) {
	sliceActions := map[string]map[string]bool{}
	for _, e := range Catalog() {
		if sliceActions[e.Resource] == nil {
			sliceActions[e.Resource] = map[string]bool{}
		}
		for _, a := range e.Actions {
			sliceActions[e.Resource][a] = true
		}
	}

	// Every (resource, action) the slice advertises must be IsValid.
	for resource, actions := range sliceActions {
		for action := range actions {
			if !IsValid(resource, action) {
				t.Errorf("Catalog() advertises %s:%s but IsValid(%q,%q) = false — the console would offer a grant POST /grants rejects with GRANT_INVALID", resource, action, resource, action)
			}
		}
	}

	// Every (resource, action) IsValid accepts must appear in the slice —
	// walk the private map directly (same package) rather than guessing at
	// its contents from outside.
	for resource, actions := range catalog {
		for _, action := range actions {
			if !sliceActions[resource][action] {
				t.Errorf("IsValid(%q,%q) = true but Catalog() does not list it — an admin could never discover this grantable pair through the API the console reads", resource, action)
			}
		}
	}
}

// TestCatalog_RolesMatchCommonConstants pins Roles()'s duplicated ints
// against the real common.RoleAdminUser/RoleRootUser constants — the two
// copies have zero shared source, so this is the only thing that catches
// one drifting from the other.
func TestCatalog_RolesMatchCommonConstants(t *testing.T) {
	roles := Roles()
	if len(roles) != 2 {
		t.Fatalf("Roles() returned %d entries, want 2", len(roles))
	}
	byName := map[string]int{}
	for _, r := range roles {
		byName[r.Name] = r.MinRole
	}
	if byName["tenant-admin"] != common.RoleAdminUser {
		t.Errorf("tenant-admin min_role = %d, want common.RoleAdminUser = %d", byName["tenant-admin"], common.RoleAdminUser)
	}
	if byName["root"] != common.RoleRootUser {
		t.Errorf("root min_role = %d, want common.RoleRootUser = %d", byName["root"], common.RoleRootUser)
	}
}
