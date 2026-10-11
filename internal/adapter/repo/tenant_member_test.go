package repo

// tenant_member_test.go — writers for users.tenant_role / project membership
// (enterprise-hub §2.4): default-tenant refusal, user-cache invalidation,
// grant-on-add / revoke-on-last-remove, atomic invite grant, batch rollback.

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func seedMemberUser(t *testing.T, tenant, name, role string) int {
	t.Helper()
	u := &User{Username: name, TenantId: tenant, TenantRole: role, Status: 1, Role: 1}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.Id
}

func migrateMemberTables(t *testing.T) {
	t.Helper()
	if err := DB.AutoMigrate(&User{}, &Token{}, &entity.ProjectMember{}, &entity.Project{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func roleOf(t *testing.T, id int) string {
	t.Helper()
	var u User
	if err := DB.First(&u, id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return u.TenantRole
}

func TestTenantRoleWriters_RefuseDefaultTenant(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	uid := seedMemberUser(t, "default", "d", "")
	err := ApplyInviteGrant("default", uid, entity.TenantRoleAdmin, 0)
	if !errors.Is(err, ErrTenantRoleInDefault) {
		t.Fatalf("err = %v, want ErrTenantRoleInDefault", err)
	}
	if got := roleOf(t, uid); got != "" {
		t.Fatalf("role written despite refusal: %q", got)
	}
	if err := AddProjectMemberGranting("default", 1, uid); !errors.Is(err, ErrTenantRoleInDefault) {
		t.Fatalf("AddProjectMemberGranting default err = %v", err)
	}
}

func TestApplyInviteGrant_RejectsForeignUserAndBadRole(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	uid := seedMemberUser(t, "t1", "a", "")
	if err := ApplyInviteGrant("t2", uid, entity.TenantRoleAdmin, 0); !errors.Is(err, ErrMemberNotInTenant) {
		t.Fatalf("foreign err = %v, want ErrMemberNotInTenant", err)
	}
	if err := ApplyInviteGrant("t1", uid, "root", 0); !errors.Is(err, ErrInvalidTenantRole) {
		t.Fatalf("bad role err = %v, want ErrInvalidTenantRole", err)
	}
	if got := roleOf(t, uid); got != "" {
		t.Fatalf("role = %q after rejected writes", got)
	}
}

// primeUserCache loads the user into the Redis user cache and fails the test
// unless the cached copy carries wantRole.
func primeUserCache(t *testing.T, uid int, wantRole string) {
	t.Helper()
	var u User
	if err := DB.First(&u, uid).Error; err != nil {
		t.Fatal(err)
	}
	if err := updateUserCache(u); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	if got, err := cacheGetUserBase(uid); err != nil || got.TenantRole != wantRole {
		t.Fatalf("cache not primed with %q: %+v %v", wantRole, got, err)
	}
}

// A revoked dept_lead must lose privilege on the next request: the writer has to
// drop the Redis user cache entry rather than leave it to expire.
func TestRemoveProjectMemberRevoking_InvalidatesUserCache(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	repoWithMiniRedis(t)
	p := seedProject(t, "t1", "p")
	uid := seedMemberUser(t, "t1", "lead", "")
	if err := AddProjectMemberGranting("t1", p, uid); err != nil {
		t.Fatal(err)
	}
	primeUserCache(t, uid, entity.TenantRoleDeptLead)

	if revoked, err := RemoveProjectMemberRevoking("t1", p, uid); err != nil || !revoked {
		t.Fatalf("revoke = %v, %v", revoked, err)
	}
	got, err := GetUserCache(uid)
	if err != nil || got.TenantRole != "" {
		t.Fatalf("GetUserCache role = %q (%v), want empty", got.TenantRole, err)
	}
}

func TestAddProjectMemberGranting_InvalidatesUserCache(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	repoWithMiniRedis(t)
	p := seedProject(t, "t1", "p")
	uid := seedMemberUser(t, "t1", "lead", "")
	primeUserCache(t, uid, "")

	if err := AddProjectMemberGranting("t1", p, uid); err != nil {
		t.Fatal(err)
	}
	got, err := GetUserCache(uid)
	if err != nil || got.TenantRole != entity.TenantRoleDeptLead {
		t.Fatalf("GetUserCache role = %q (%v), want dept_lead", got.TenantRole, err)
	}
}

func TestApplyInviteGrant_InvalidatesUserCache(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	repoWithMiniRedis(t)
	p := seedProject(t, "t1", "p")
	uid := seedMemberUser(t, "t1", "new", "")
	primeUserCache(t, uid, "")

	if err := ApplyInviteGrant("t1", uid, entity.TenantRoleAdmin, int64(p)); err != nil {
		t.Fatal(err)
	}
	got, err := GetUserCache(uid)
	if err != nil || got.TenantRole != entity.TenantRoleAdmin {
		t.Fatalf("GetUserCache role = %q (%v), want admin", got.TenantRole, err)
	}
}

func TestAddProjectMemberGranting_PromotesAndRemoveDemotes(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	repoWithMiniRedis(t)
	p1, p2 := seedProject(t, "t1", "p1"), seedProject(t, "t1", "p2")
	uid := seedMemberUser(t, "t1", "lead", "")
	admin := seedMemberUser(t, "t1", "adm", entity.TenantRoleAdmin)

	for _, p := range []int{p1, p2} {
		if err := AddProjectMemberGranting("t1", p, uid); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if got := roleOf(t, uid); got != entity.TenantRoleDeptLead {
		t.Fatalf("role = %q after add, want dept_lead", got)
	}
	// An existing admin keeps the admin role when added to a project.
	if err := AddProjectMemberGranting("t1", p1, admin); err != nil {
		t.Fatal(err)
	}
	if got := roleOf(t, admin); got != entity.TenantRoleAdmin {
		t.Fatalf("admin role downgraded to %q", got)
	}

	if _, err := RemoveProjectMemberRevoking("t1", p1, uid); err != nil {
		t.Fatal(err)
	}
	if got := roleOf(t, uid); got != entity.TenantRoleDeptLead {
		t.Fatalf("role = %q while a project remains, want dept_lead", got)
	}
	if _, err := RemoveProjectMemberRevoking("t1", p2, uid); err != nil {
		t.Fatal(err)
	}
	if got := roleOf(t, uid); got != "" {
		t.Fatalf("role = %q after last project removed, want empty", got)
	}
	// Removing the admin's last project must NOT strip admin.
	if _, err := RemoveProjectMemberRevoking("t1", p1, admin); err != nil {
		t.Fatal(err)
	}
	if got := roleOf(t, admin); got != entity.TenantRoleAdmin {
		t.Fatalf("admin role stripped to %q by member removal", got)
	}
}

func TestAddProjectMemberGranting_CrossTenantRefused(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	foreignProject := seedProject(t, "t2", "theirs")
	mine := seedProject(t, "t1", "mine")
	uid := seedMemberUser(t, "t1", "u", "")
	other := seedMemberUser(t, "t2", "o", "")

	if err := AddProjectMemberGranting("t1", foreignProject, uid); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("foreign project err = %v", err)
	}
	if err := AddProjectMemberGranting("t1", mine, other); !errors.Is(err, ErrMemberNotInTenant) {
		t.Fatalf("foreign user err = %v", err)
	}
	if got := roleOf(t, uid); got != "" {
		t.Fatalf("role = %q after refused add", got)
	}
}

func TestApplyInviteGrant_RoleAndMembershipAtomic(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	repoWithMiniRedis(t)
	p := seedProject(t, "t1", "dept")
	uid := seedMemberUser(t, "t1", "new", "")

	// Project only: role defaults to dept_lead.
	if err := ApplyInviteGrant("t1", uid, "", int64(p)); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if got := roleOf(t, uid); got != entity.TenantRoleDeptLead {
		t.Fatalf("role = %q, want dept_lead", got)
	}
	if ids, _ := ListProjectIDsForUser("t1", uid); len(ids) != 1 || ids[0] != p {
		t.Fatalf("membership = %v, want [%d]", ids, p)
	}

	// A dead project must roll the role back too (one transaction).
	u2 := seedMemberUser(t, "t1", "new2", "")
	if err := ApplyInviteGrant("t1", u2, entity.TenantRoleDeptLead, 99999); err == nil {
		t.Fatal("grant to a missing project must fail")
	}
	if got := roleOf(t, u2); got != "" {
		t.Fatalf("role = %q leaked from a failed grant", got)
	}
	// Default tenant never takes a grant.
	d := seedMemberUser(t, "default", "dd", "")
	if err := ApplyInviteGrant("default", d, entity.TenantRoleAdmin, 0); !errors.Is(err, ErrTenantRoleInDefault) {
		t.Fatalf("default grant err = %v", err)
	}
}

func TestInviteGrant_RoundTripsThroughConsume(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&Tenant{}, &TenantInvite{}); err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&Tenant{Id: "t1", Name: "T", Slug: "t1", Status: TenantStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	inv, err := CreateTenantInviteWithGrant("t1", 1, 0, entity.TenantRoleDeptLead, 7)
	if err != nil {
		t.Fatal(err)
	}
	tenant, grant, err := ConsumeTenantInviteGrant(inv.Code, 555)
	if err != nil || tenant.Id != "t1" {
		t.Fatalf("consume: %v %v", tenant, err)
	}
	if grant.MemberRole != entity.TenantRoleDeptLead || grant.ProjectID != 7 {
		t.Fatalf("grant = %+v, want dept_lead/7", grant)
	}
	if _, _, err := ConsumeTenantInviteGrant(inv.Code, 556); err == nil {
		t.Fatal("an invite must be single-use")
	}
}

func TestCreateTokensBatch_AllOrNothing(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	mk := func(key, ref string) *Token {
		return &Token{UserId: 1, TenantId: "t1", Name: ref, Key: key, EmployeeRef: ref, Status: common.TokenStatusEnabled}
	}
	// Second row collides on the unique Key: the first must roll back.
	err := CreateTokensBatch("t1", []*Token{mk("k-one", "e1"), mk("k-one", "e2")})
	if err == nil {
		t.Fatal("duplicate key must fail the batch")
	}
	var n int64
	DB.Model(&Token{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d rows survived a failed batch, want 0", n)
	}
	if err := CreateTokensBatch("t1", []*Token{mk("k-a", "e1"), mk("k-b", "e2")}); err != nil {
		t.Fatalf("clean batch: %v", err)
	}
	got, err := ExistingEmployeeRefs("t1", []string{"e1", "e2", "e3"})
	if err != nil || len(got) != 2 {
		t.Fatalf("ExistingEmployeeRefs = %v, %v; want e1,e2", got, err)
	}
	if taken, _ := TokenEmployeeRefTaken("t1", "e1", 0); !taken {
		t.Fatal("e1 must be taken")
	}
	if taken, _ := TokenEmployeeRefTaken("t2", "e1", 0); taken {
		t.Fatal("employee_ref uniqueness must be per tenant")
	}
	if taken, _ := TokenEmployeeRefTaken("t1", "e1", got["e1"].Id); taken {
		t.Fatal("a key must not conflict with itself")
	}
}

func TestProjectExternalCode_UniqueAndHardDelete(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	a, b := seedProject(t, "t1", "a"), seedProject(t, "t1", "b")
	if _, err := SetProjectExternalCode("t1", a, "FIN-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetProjectExternalCode("t1", b, "FIN-01"); !errors.Is(err, ErrProjectExternalCodeExists) {
		t.Fatalf("dup code err = %v", err)
	}
	if taken, _ := ProjectExternalCodeTaken("t2", "FIN-01", 0); taken {
		t.Fatal("codes are per tenant")
	}
	if err := HardDeleteProject("t1", a); err != nil {
		t.Fatal(err)
	}
	var n int64
	DB.Unscoped().Model(&entity.Project{}).Where("id = ?", a).Count(&n)
	if n != 0 {
		t.Fatal("HardDeleteProject left the row behind")
	}
}

func TestRevokeDeptLeadsWithoutProjects(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	repoWithMiniRedis(t)
	p1, p2 := seedProject(t, "t1", "p1"), seedProject(t, "t1", "p2")
	only := seedMemberUser(t, "t1", "only", "")
	both := seedMemberUser(t, "t1", "both", "")
	admin := seedMemberUser(t, "t1", "adm", entity.TenantRoleAdmin)
	for _, m := range [][2]int{{p1, only}, {p1, both}, {p2, both}, {p1, admin}} {
		if err := AddProjectMemberGranting("t1", m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	primeUserCache(t, only, entity.TenantRoleDeptLead)

	if _, err := SoftDeleteProject("t1", p1); err != nil {
		t.Fatal(err)
	}
	revoked, err := RevokeDeptLeadsWithoutProjects("t1", []int64{int64(only), int64(both), int64(admin)})
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked) != 1 || revoked[0] != only {
		t.Fatalf("revoked = %v, want [%d]", revoked, only)
	}
	if got := roleOf(t, only); got != "" {
		t.Fatalf("only-project lead role = %q, want revoked", got)
	}
	if got := roleOf(t, both); got != entity.TenantRoleDeptLead {
		t.Fatalf("lead with a live project lost role: %q", got)
	}
	if got := roleOf(t, admin); got != entity.TenantRoleAdmin {
		t.Fatalf("admin role touched: %q", got)
	}
	if c, err := GetUserCache(only); err != nil || c.TenantRole != "" {
		t.Fatalf("stale cached role %q (%v)", c.TenantRole, err)
	}
}

func TestRetiredProjectExternalCodeTaken(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	a, b := seedProject(t, "t1", "a"), seedProject(t, "t1", "b")
	if _, err := SetProjectExternalCode("t1", a, "FIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := SoftDeleteProject("t1", a); err != nil {
		t.Fatal(err)
	}
	if _, err := SetProjectExternalCode("t1", b, "FIN"); err != nil {
		t.Fatal(err)
	}
	if taken, err := RetiredProjectExternalCodeTaken("t1", a); err != nil || !taken {
		t.Fatalf("taken = %v, %v; want true", taken, err)
	}
	if taken, _ := RetiredProjectExternalCodeTaken("t1", b); taken {
		t.Fatal("a live project is never 'blocked from restore'")
	}
}
