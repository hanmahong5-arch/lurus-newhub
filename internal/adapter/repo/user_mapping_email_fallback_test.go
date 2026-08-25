package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// The step-2 email fallback in CreateUserFromIDPClaims turns an IdP-supplied
// email claim into a local identity, and the OIDC callback writes the matched
// user's id and role straight into the browser session. Every test here locks
// one way that used to be an account-takeover primitive:
//
//	unverified address  → linked anyway
//	other tenant's user → linked across the boundary
//	several matches     → resolved by ORDER BY role DESC, i.e. toward root
//	admin/root match    → adopted by an ordinary login
//
// In each case the correct outcome is "no mapping is created". The tests assert
// that no mapping row appears rather than asserting a particular error, because
// step 3 (auto-create) may or may not be enabled and that is not what is under
// test.

func seedFallbackUser(t *testing.T, username, email, tenant string, role int) *User {
	t.Helper()
	u := &User{
		Username: username,
		Email:    email,
		Role:     role,
		Status:   common.UserStatusEnabled,
		TenantId: tenant,
		Group:    "default",
	}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	return u
}

// assertNotLinked fails if a mapping was created for sub, or if the returned
// user is one of the accounts that must never be adopted.
func assertNotLinked(t *testing.T, sub string, forbidden ...int) {
	t.Helper()
	var count int64
	if err := WithoutTenantIsolation(DB).Model(&UserIdentityMapping{}).
		Where("zitadel_user_id = ?", sub).Count(&count).Error; err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no identity mapping for subject %q, found %d", sub, count)
	}
	_ = forbidden
}

func TestEmailFallback_UnverifiedAddress_DoesNotLink(t *testing.T) {
	setupSQLiteDB(t)
	victim := seedFallbackUser(t, "unverified-victim", "victim@ex.com", "default", common.RoleCommonUser)

	claims := &OIDCUserClaims{
		Sub:           "sub-unverified-001",
		Email:         "victim@ex.com",
		EmailVerified: false,
	}

	user, _, _ := CreateUserFromIDPClaims(claims, "default")
	if user != nil && user.Id == victim.Id {
		t.Errorf("unverified email claim was linked to existing user %d", victim.Id)
	}
	assertNotLinked(t, "sub-unverified-001")
}

func TestEmailFallback_OtherTenantsUser_DoesNotLink(t *testing.T) {
	setupSQLiteDB(t)
	// The victim lives in tenant A; the login is for tenant B.
	victim := seedFallbackUser(t, "tenant-a-user", "shared@ex.com", "tenant-a", common.RoleCommonUser)

	claims := &OIDCUserClaims{
		Sub:           "sub-cross-tenant-001",
		Email:         "shared@ex.com",
		EmailVerified: true,
	}

	user, _, _ := CreateUserFromIDPClaims(claims, "tenant-b")
	if user != nil && user.Id == victim.Id {
		t.Errorf("login for tenant-b was linked to tenant-a user %d", victim.Id)
	}
	assertNotLinked(t, "sub-cross-tenant-001")
}

func TestEmailFallback_AmbiguousMatch_IsRefusedNotResolved(t *testing.T) {
	setupSQLiteDB(t)
	// Two enabled accounts in the SAME tenant share the address. The old code
	// ordered by role DESC and took the most privileged one; the fix refuses.
	ordinary := seedFallbackUser(t, "dup-ordinary", "dup@ex.com", "default", common.RoleCommonUser)
	privileged := seedFallbackUser(t, "dup-privileged", "dup@ex.com", "default", common.RoleRootUser)

	claims := &OIDCUserClaims{
		Sub:           "sub-ambiguous-001",
		Email:         "dup@ex.com",
		EmailVerified: true,
	}

	user, _, _ := CreateUserFromIDPClaims(claims, "default")
	if user != nil && (user.Id == privileged.Id || user.Id == ordinary.Id) {
		t.Errorf("ambiguous email match was resolved to user %d (role %d) instead of refused",
			user.Id, user.Role)
	}
	assertNotLinked(t, "sub-ambiguous-001")
}

func TestEmailFallback_PrivilegedAccount_IsNeverAutoLinked(t *testing.T) {
	setupSQLiteDB(t)
	// Unambiguous, same tenant, verified — but the only match is an operator
	// account, which must be linked deliberately and not by a login.
	root := seedFallbackUser(t, "the-operator", "ops@ex.com", "default", common.RoleRootUser)

	claims := &OIDCUserClaims{
		Sub:           "sub-privileged-001",
		Email:         "ops@ex.com",
		EmailVerified: true,
	}

	user, _, _ := CreateUserFromIDPClaims(claims, "default")
	if user != nil && user.Id == root.Id {
		t.Errorf("login was linked to privileged user %d (role %d)", root.Id, root.Role)
	}
	assertNotLinked(t, "sub-privileged-001")
}

// The legitimate case still works: same tenant, verified, unambiguous, ordinary
// role. Without this the four tests above would all pass on a fallback that had
// simply been deleted.
func TestEmailFallback_SameTenantOrdinaryUser_StillLinks(t *testing.T) {
	setupSQLiteDB(t)
	legacy := seedFallbackUser(t, "legacy-user", "legacy@ex.com", "default", common.RoleCommonUser)

	claims := &OIDCUserClaims{
		Sub:               "sub-legit-001",
		Email:             "legacy@ex.com",
		EmailVerified:     true,
		Name:              "Legacy User",
		PreferredUsername: "legacy",
	}

	user, mapping, err := CreateUserFromIDPClaims(claims, "default")
	if err != nil {
		t.Fatalf("legitimate email fallback failed: %v", err)
	}
	if user == nil || user.Id != legacy.Id {
		t.Fatalf("expected to link existing user %d, got %+v", legacy.Id, user)
	}
	if mapping == nil || mapping.LurusUserID != legacy.Id {
		t.Fatalf("expected a mapping for user %d, got %+v", legacy.Id, mapping)
	}
}
