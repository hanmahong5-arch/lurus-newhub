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
		{"channel", "sensitive_write", false},
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
