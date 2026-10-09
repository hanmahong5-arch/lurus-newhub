package middleware

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func TestSessionTenantContext_CarriesRoleAndFailsClosed(t *testing.T) {
	u := &repo.UserBase{TenantId: "t1", TenantRole: "dept_lead"}

	tc := sessionTenantContext("t1", 7, "a@b.c", "bob", u, nil)
	if tc.TenantRole != "dept_lead" || tc.RoleTenantID != "t1" || tc.TenantID != "t1" || tc.UserID != 7 {
		t.Fatalf("hit: got %+v", tc)
	}

	for name, c := range map[string]*TenantContext{
		"lookup error": sessionTenantContext("t1", 7, "", "", u, errors.New("boom")),
		"nil entry":    sessionTenantContext("t1", 7, "", "", nil, nil),
	} {
		if c.TenantRole != "" || c.RoleTenantID != "" {
			t.Errorf("%s: role=%q roleTenant=%q, want both empty (fail closed)", name, c.TenantRole, c.RoleTenantID)
		}
	}
}
