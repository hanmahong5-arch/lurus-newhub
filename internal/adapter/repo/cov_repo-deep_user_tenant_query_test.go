package repo

// cov_repo-deep_user_tenant_query_test.go — coverage for the tenant-scoped
// admin-console user queries in user.go (GetUsersByTenant, SearchUsersByTenant)
// and the root-role gate (IsRoot). The central business property under test:
// a tenant-admin listing/search must never leak another tenant's users.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func repoDeepSeedTenantUser(t *testing.T, tenantID, username, email, group string, role int) *User {
	t.Helper()
	u := &User{
		Username: username, DisplayName: username, Email: email,
		Role: role, Status: common.UserStatusEnabled,
		TenantId: tenantID, Group: group,
	}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("seed tenant user %q: %v", username, err)
	}
	return u
}

func TestGetUsersByTenant_ScopesToOneTenant(t *testing.T) {
	SetupTestDB(t)
	a1 := repoDeepSeedTenantUser(t, "tenant-a", "a1", "a1@x.com", "default", common.RoleCommonUser)
	a2 := repoDeepSeedTenantUser(t, "tenant-a", "a2", "a2@x.com", "default", common.RoleCommonUser)
	repoDeepSeedTenantUser(t, "tenant-b", "b1", "b1@x.com", "default", common.RoleCommonUser)

	users, total, err := GetUsersByTenant("tenant-a", &common.PageInfo{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("GetUsersByTenant: %v", err)
	}
	if total != 2 {
		t.Fatalf("tenant-a total must be 2 (isolated from tenant-b), got %d", total)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 users returned, got %d", len(users))
	}
	seen := map[int]bool{}
	for _, u := range users {
		seen[u.Id] = true
		if u.TenantId != "tenant-a" {
			t.Fatalf("leaked a non-tenant-a user: %+v", u)
		}
	}
	if !seen[a1.Id] || !seen[a2.Id] {
		t.Fatalf("both tenant-a users must be present, got %+v", users)
	}

	// id DESC ordering.
	if len(users) == 2 && users[0].Id < users[1].Id {
		t.Fatalf("expected id DESC ordering, got %d then %d", users[0].Id, users[1].Id)
	}
}

func TestGetUsersByTenant_Pagination(t *testing.T) {
	SetupTestDB(t)
	for i := 0; i < 5; i++ {
		repoDeepSeedTenantUser(t, "tenant-page", "p"+string(rune('a'+i)), string(rune('a'+i))+"@x.com", "default", common.RoleCommonUser)
	}

	page1, total, err := GetUsersByTenant("tenant-page", &common.PageInfo{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total != 5 {
		t.Fatalf("total must reflect all 5 rows regardless of page size, got %d", total)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 must have 2 rows, got %d", len(page1))
	}

	page3, _, err := GetUsersByTenant("tenant-page", &common.PageInfo{Page: 3, PageSize: 2})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 (offset 4, size 2, total 5) must have exactly the remaining 1 row, got %d", len(page3))
	}
}

func TestGetUsersByTenant_EmptyTenantReturnsEmpty(t *testing.T) {
	SetupTestDB(t)
	repoDeepSeedTenantUser(t, "tenant-real", "u1", "u1@x.com", "default", common.RoleCommonUser)

	users, total, err := GetUsersByTenant("no-such-tenant", &common.PageInfo{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("GetUsersByTenant unknown tenant: %v", err)
	}
	if total != 0 || len(users) != 0 {
		t.Fatalf("unknown tenant must yield zero rows, got total=%d len=%d", total, len(users))
	}
}

func TestSearchUsersByTenant_KeywordAndGroupScoped(t *testing.T) {
	SetupTestDB(t)
	target := repoDeepSeedTenantUser(t, "tenant-search", "findme", "findme@x.com", "vip", common.RoleCommonUser)
	repoDeepSeedTenantUser(t, "tenant-search", "other", "other@x.com", "vip", common.RoleCommonUser)
	// Same username substring but a DIFFERENT tenant — must not leak in.
	repoDeepSeedTenantUser(t, "tenant-other", "findme-imposter", "findme2@x.com", "vip", common.RoleCommonUser)

	users, total, err := SearchUsersByTenant("tenant-search", "findme", "", 0, 10)
	if err != nil {
		t.Fatalf("SearchUsersByTenant: %v", err)
	}
	if total != 1 {
		t.Fatalf("search must match only the in-tenant row, got total=%d", total)
	}
	if len(users) != 1 || users[0].Id != target.Id {
		t.Fatalf("wrong result set: %+v", users)
	}

	// Group filter narrows further.
	usersG, totalG, err := SearchUsersByTenant("tenant-search", "", "vip", 0, 10)
	if err != nil {
		t.Fatalf("SearchUsersByTenant group filter: %v", err)
	}
	if totalG != 2 {
		t.Fatalf("group filter must match both tenant-search/vip users, got %d", totalG)
	}
	_ = usersG

	usersNoMatch, totalNoMatch, err := SearchUsersByTenant("tenant-search", "", "no-such-group", 0, 10)
	if err != nil {
		t.Fatalf("SearchUsersByTenant no-match group: %v", err)
	}
	if totalNoMatch != 0 || len(usersNoMatch) != 0 {
		t.Fatalf("non-existent group must match nothing, got total=%d", totalNoMatch)
	}
}

// The keyword-is-numeric branch also matches by literal id, still scoped to
// the tenant.
func TestSearchUsersByTenant_NumericKeywordMatchesId(t *testing.T) {
	SetupTestDB(t)
	target := repoDeepSeedTenantUser(t, "tenant-numeric", "numuser", "num@x.com", "default", common.RoleCommonUser)
	repoDeepSeedTenantUser(t, "tenant-other", "othernum", "othernum@x.com", "default", common.RoleCommonUser)

	users, total, err := SearchUsersByTenant("tenant-numeric", intToStr(target.Id), "", 0, 10)
	if err != nil {
		t.Fatalf("SearchUsersByTenant numeric keyword: %v", err)
	}
	if total != 1 || len(users) != 1 || users[0].Id != target.Id {
		t.Fatalf("numeric-keyword id match failed: total=%d users=%+v", total, users)
	}
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestIsRoot_TrueOnlyForRootRole(t *testing.T) {
	SetupTestDB(t)
	root, normal, _ := SeedTestUsers(t)

	if !IsRoot(root.Id) {
		t.Fatal("root-role user must be IsRoot")
	}
	if IsRoot(normal.Id) {
		t.Fatal("common-role user must not be IsRoot")
	}
}

func TestIsRoot_ZeroAndUnknownUserAreFalse(t *testing.T) {
	SetupTestDB(t)

	if IsRoot(0) {
		t.Fatal("userId=0 must be treated as not-root without a DB lookup")
	}
	if IsRoot(999999) {
		t.Fatal("unknown userId must not be root")
	}
}

// An admin-role (but not root-role) user must NOT satisfy IsRoot — the
// root check is strictly >= RoleRootUser, not >= RoleAdminUser.
func TestIsRoot_AdminRoleIsNotRoot(t *testing.T) {
	SetupTestDB(t)
	admin := repoDeepSeedTenantUser(t, "default", "adminonly", "admin@x.com", "default", common.RoleAdminUser)

	if IsRoot(admin.Id) {
		t.Fatal("admin-role (RoleAdminUser) must not satisfy the root gate")
	}
}
