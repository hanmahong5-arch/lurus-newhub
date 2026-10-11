package repo

import (
	"errors"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm"
)

func TestAccountKeyBinding_GetAndList(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&AccountKeyBinding{}); err != nil {
		t.Fatal(err)
	}

	b, err := GetLiveAccountKeyBinding(1, "chat")
	if b != nil || err != nil {
		t.Fatalf("absent binding must be (nil,nil), got %v %v", b, err)
	}

	DB.Create(&AccountKeyBinding{IdentityAccountID: 1, Product: "chat", TokenId: 10})
	DB.Create(&AccountKeyBinding{IdentityAccountID: 1, Product: "app", TokenId: 11})
	DB.Create(&AccountKeyBinding{IdentityAccountID: 2, Product: "chat", TokenId: 12})
	dead := &AccountKeyBinding{IdentityAccountID: 1, Product: "gone", TokenId: 13}
	DB.Create(dead)
	DB.Delete(dead)

	b, err = GetLiveAccountKeyBinding(1, "chat")
	if err != nil || b == nil || b.TokenId != 10 {
		t.Fatalf("got %+v %v, want token 10", b, err)
	}
	if b, _ = GetLiveAccountKeyBinding(1, "gone"); b != nil {
		t.Fatal("soft-deleted binding must not resolve")
	}

	list, err := ListLiveAccountKeyBindings(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Product != "app" || list[1].Product != "chat" {
		t.Fatalf("want [app chat] ordered, got %+v", list)
	}
	if list, _ = ListLiveAccountKeyBindings(404); len(list) != 0 {
		t.Fatalf("unknown account listed %d", len(list))
	}
}

func TestGetTokenUnscopedTenant(t *testing.T) {
	defer setupSQLiteDB(t)()
	tok := &Token{UserId: 1, Name: "k", Key: "sk-unscoped", TenantId: "tz", Status: 1}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatal(err)
	}
	got, err := GetTokenUnscopedTenant(int64(tok.Id))
	if err != nil || got.Id != tok.Id || got.TenantId != "tz" {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := GetTokenUnscopedTenant(987654); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func seedRosterUser(t *testing.T, tenant, name, display, email, role string) *User {
	t.Helper()
	u := &User{Username: name, DisplayName: display, Email: email, TenantId: tenant, TenantRole: role, Status: 1, Role: 1}
	if err := DB.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func TestEscapeLike(t *testing.T) {
	if got := escapeLike(`a%b_c\d`); got != `a\%b\_c\d` {
		t.Fatalf("got %q", got)
	}
}

func TestListTenantMembers(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	adm := seedRosterUser(t, "ta", "alice", "Alice", "alice@x.io", entity.TenantRoleAdmin)
	lead := seedRosterUser(t, "ta", "bob", "", "bob@x.io", entity.TenantRoleDeptLead)
	plain := seedRosterUser(t, "ta", "100%_user", "", "p@x.io", "")
	seedRosterUser(t, "tb", "alice-b", "", "ab@x.io", entity.TenantRoleAdmin)

	p1 := &entity.Project{TenantId: "ta", Name: "eng"}
	p2 := &entity.Project{TenantId: "ta", Name: "ops"}
	DB.Create(p1)
	DB.Create(p2)
	DB.Create(&entity.ProjectMember{TenantId: "ta", ProjectId: int64(p1.Id), UserId: int64(lead.Id), CreatedAt: 1})
	DB.Create(&entity.ProjectMember{TenantId: "ta", ProjectId: int64(p2.Id), UserId: int64(lead.Id), CreatedAt: 1})
	DB.Create(&entity.ProjectMember{TenantId: "ta", ProjectId: int64(p1.Id), UserId: int64(adm.Id), CreatedAt: 1})
	// membership recorded under another tenant must not show up
	DB.Create(&entity.ProjectMember{TenantId: "tb", ProjectId: int64(p1.Id), UserId: int64(plain.Id), CreatedAt: 1})

	rows, total, err := ListTenantMembers("ta", TenantMemberFilter{}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(rows) != 3 || rows[0].User.Id != adm.Id {
		t.Fatalf("total=%d rows=%d, want 3 ta users id-asc", total, len(rows))
	}
	if len(rows[0].Departments) != 1 || rows[0].Departments[0].IsLead {
		t.Fatalf("admin dept: %+v (admin is listed but not lead)", rows[0].Departments)
	}
	if len(rows[1].Departments) != 2 || !rows[1].Departments[0].IsLead || rows[1].Departments[0].Name != "eng" || rows[1].Departments[1].Name != "ops" {
		t.Fatalf("lead depts: %+v", rows[1].Departments)
	}
	if rows[2].Departments == nil || len(rows[2].Departments) != 0 {
		t.Fatalf("plain member must have empty non-nil depts: %+v", rows[2].Departments)
	}

	rows, total, _ = ListTenantMembers("ta", TenantMemberFilter{Role: "member"}, 0, 50)
	if total != 1 || rows[0].User.Id != plain.Id {
		t.Fatalf("member filter: total=%d", total)
	}
	_, total, _ = ListTenantMembers("ta", TenantMemberFilter{Role: entity.TenantRoleAdmin}, 0, 50)
	if total != 1 {
		t.Fatalf("admin filter total=%d", total)
	}
	rows, total, _ = ListTenantMembers("ta", TenantMemberFilter{Keyword: " 100% "}, 0, 50)
	if total != 1 || rows[0].User.Id != plain.Id {
		t.Fatalf("keyword literal: total=%d", total)
	}
	_, total, _ = ListTenantMembers("ta", TenantMemberFilter{Keyword: "%"}, 0, 50)
	if total != 1 {
		t.Fatalf("bare percent matched %d, want only the literal one", total)
	}
	_, total, _ = ListTenantMembers("ta", TenantMemberFilter{Keyword: "ALICE"}, 0, 50)
	if total != 1 {
		t.Fatalf("keyword alice total=%d (tb alice-b must not leak)", total)
	}

	rows, total, _ = ListTenantMembers("ta", TenantMemberFilter{}, 1, 1)
	if total != 3 || len(rows) != 1 || rows[0].User.Id != lead.Id {
		t.Fatalf("paging: total=%d rows=%d", total, len(rows))
	}
	rows, total, err = ListTenantMembers("nobody", TenantMemberFilter{}, 0, 10)
	if err != nil || total != 0 || len(rows) != 0 || rows == nil {
		t.Fatalf("empty tenant: %v %d %v", rows, total, err)
	}
}

func TestListTokensByTenant(t *testing.T) {
	defer setupSQLiteDB(t)()
	mk := func(tenant, name, ref string, user, project, status int) *Token {
		tok := &Token{UserId: user, Name: name, Key: "sk-" + name, TenantId: tenant, ProjectId: project, EmployeeRef: ref, Status: status}
		if err := DB.Create(tok).Error; err != nil {
			t.Fatal(err)
		}
		return tok
	}
	t1 := mk("ta", "alpha", "emp-1", 1, 5, 1)
	t2 := mk("ta", "beta", "emp_2", 2, 6, 2)
	t3 := mk("ta", "gamma", "", 1, 6, 1)
	mk("tb", "alpha-b", "", 1, 5, 1)

	got, total, err := ListTokensByTenant("ta", TenantTokenFilter{}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(got) != 3 || got[0].Id != t3.Id || got[2].Id != t1.Id {
		t.Fatalf("total=%d, want 3 id-desc, tenant tb excluded", total)
	}
	cases := []struct {
		name string
		f    TenantTokenFilter
		want int
		id   int
	}{
		{"user", TenantTokenFilter{UserId: 2}, 1, t2.Id},
		{"project", TenantTokenFilter{ProjectId: 5}, 1, t1.Id},
		{"status", TenantTokenFilter{Status: 2}, 1, t2.Id},
		{"name kw", TenantTokenFilter{Keyword: "amm"}, 1, t3.Id},
		{"employee kw", TenantTokenFilter{Keyword: "emp-1"}, 1, t1.Id},
		{"underscore literal", TenantTokenFilter{Keyword: "emp_2"}, 1, t2.Id},
		{"combined miss", TenantTokenFilter{UserId: 2, ProjectId: 5}, 0, 0},
	}
	for _, c := range cases {
		g, n, err := ListTokensByTenant("ta", c.f, 0, 10)
		if err != nil || int(n) != c.want || len(g) != c.want || (c.want == 1 && g[0].Id != c.id) {
			t.Errorf("%s: n=%d len=%d err=%v", c.name, n, len(g), err)
		}
	}
	g, n, _ := ListTokensByTenant("ta", TenantTokenFilter{}, 1, 1)
	if n != 3 || len(g) != 1 || g[0].Id != t2.Id {
		t.Fatalf("paging n=%d", n)
	}
}

func TestUserNamesByID(t *testing.T) {
	defer setupSQLiteDB(t)()
	a := seedRosterUser(t, "ta", "login-a", "Display A", "a@x", "")
	b := seedRosterUser(t, "ta", "login-b", "", "b@x", "")
	c := seedRosterUser(t, "tb", "login-c", "C", "c@x", "")

	m, err := UserNamesByID("ta", []int{a.Id, b.Id, c.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[a.Id] != "Display A" || m[b.Id] != "login-b" {
		t.Fatalf("got %v (display name wins, username fallback, foreign tenant excluded)", m)
	}
	if m, err = UserNamesByID("ta", nil); err != nil || m == nil || len(m) != 0 {
		t.Fatalf("empty ids: %v %v", m, err)
	}
}

// ---- tenant_payer.go ----

func seedPayerTenant(t *testing.T, id string, payer int64) {
	t.Helper()
	ten := seedTenant(t, id, "slug-"+id, id)
	if payer != 0 {
		DB.Model(ten).Update("payer_user_id", payer)
	}
}

func TestTenantPayerID_And_Resolve(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	u := seedRosterUser(t, "ta", "payer", "", "p@x", "")
	seedPayerTenant(t, "ta", int64(u.Id))
	seedPayerTenant(t, "tb", int64(u.Id)) // payer recorded but user is not a tb member
	seedPayerTenant(t, "tc", 0)

	if id, err := TenantPayerID("ta"); err != nil || id != u.Id {
		t.Fatalf("payer id = %d %v", id, err)
	}
	if _, err := TenantPayerID("missing"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing tenant err = %v", err)
	}

	got, err := ResolveTenantPayer("ta")
	if err != nil || got == nil || got.Id != u.Id {
		t.Fatalf("resolve = %+v %v", got, err)
	}
	if got, err = ResolveTenantPayer("tc"); got != nil || err != nil {
		t.Fatalf("unset payer must be (nil,nil), got %+v %v", got, err)
	}
	if got, err = ResolveTenantPayer("tb"); got != nil || err != nil {
		t.Fatalf("payer outside tenant must be (nil,nil), got %+v %v", got, err)
	}
	if _, err = ResolveTenantPayer("missing"); err == nil {
		t.Fatal("missing tenant must surface the lookup error")
	}
}

func TestIsTenantPayer(t *testing.T) {
	defer setupSQLiteDB(t)()
	seedPayerTenant(t, "ta", 42)
	if !IsTenantPayer("ta", 42) {
		t.Fatal("recorded payer must be true")
	}
	if IsTenantPayer("ta", 43) || IsTenantPayer("", 42) || IsTenantPayer("ta", 0) || IsTenantPayer("ta", -1) || IsTenantPayer("zz", 42) {
		t.Fatal("non-payer / bad input must be false")
	}
}

func payerOf(t *testing.T, tenant string) int64 {
	t.Helper()
	var ten Tenant
	if err := DB.Where("id = ?", tenant).Take(&ten).Error; err != nil {
		t.Fatal(err)
	}
	return ten.PayerUserId
}

func TestSetTenantMemberRole(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	seedPayerTenant(t, "ta", 0)
	adm := seedRosterUser(t, "ta", "adm", "", "a@x", entity.TenantRoleAdmin)
	adm2 := seedRosterUser(t, "ta", "adm2", "", "a2@x", entity.TenantRoleAdmin)
	mem := seedRosterUser(t, "ta", "mem", "", "m@x", "")
	other := seedRosterUser(t, "tb", "other", "", "o@x", "")
	yes, no := true, false

	if err := SetTenantMemberRole("default", mem.Id, entity.TenantRoleAdmin, nil, false); !errors.Is(err, ErrTenantRoleInDefault) {
		t.Fatalf("default err = %v", err)
	}
	if err := SetTenantMemberRole("", mem.Id, entity.TenantRoleAdmin, nil, false); !errors.Is(err, ErrTenantRoleInDefault) {
		t.Fatalf("empty tenant err = %v", err)
	}
	if err := SetTenantMemberRole("ta", mem.Id, "root", nil, false); !errors.Is(err, ErrInvalidTenantRole) {
		t.Fatalf("bad role err = %v", err)
	}
	if err := SetTenantMemberRole("ta", other.Id, entity.TenantRoleAdmin, nil, false); !errors.Is(err, ErrMemberNotInTenant) {
		t.Fatalf("foreign user err = %v", err)
	}
	if got := roleOf(t, other.Id); got != "" {
		t.Fatalf("foreign user role written: %q", got)
	}

	// promote + set payer in one go
	if err := SetTenantMemberRole("ta", mem.Id, entity.TenantRoleDeptLead, &yes, true); err != nil {
		t.Fatal(err)
	}
	if roleOf(t, mem.Id) != entity.TenantRoleDeptLead || payerOf(t, "ta") != int64(mem.Id) {
		t.Fatalf("role=%q payer=%d", roleOf(t, mem.Id), payerOf(t, "ta"))
	}
	// clearing for a user who is NOT the payer leaves the payer alone
	if err := SetTenantMemberRole("ta", adm2.Id, entity.TenantRoleAdmin, &no, true); err != nil {
		t.Fatal(err)
	}
	if payerOf(t, "ta") != int64(mem.Id) {
		t.Fatalf("payer cleared by a non-payer: %d", payerOf(t, "ta"))
	}
	// nil payer leaves it alone; false for the payer clears it
	if err := SetTenantMemberRole("ta", mem.Id, entity.TenantRoleNone, nil, true); err != nil || payerOf(t, "ta") != int64(mem.Id) {
		t.Fatalf("nil payer touched it: %v %d", err, payerOf(t, "ta"))
	}
	if err := SetTenantMemberRole("ta", mem.Id, entity.TenantRoleNone, &no, true); err != nil || payerOf(t, "ta") != 0 {
		t.Fatalf("payer not cleared: %v %d", err, payerOf(t, "ta"))
	}

	// keepAdmin: demoting adm2 is fine (adm remains) ...
	if err := SetTenantMemberRole("ta", adm2.Id, entity.TenantRoleNone, nil, true); err != nil {
		t.Fatalf("demote with another admin: %v", err)
	}
	// ... demoting the last one is refused and nothing changes
	if err := SetTenantMemberRole("ta", adm.Id, entity.TenantRoleNone, nil, true); !errors.Is(err, ErrLastTenantAdmin) {
		t.Fatalf("last admin err = %v", err)
	}
	if roleOf(t, adm.Id) != entity.TenantRoleAdmin {
		t.Fatal("last admin was demoted despite refusal")
	}
	// keepAdmin=false permits it (root path)
	if err := SetTenantMemberRole("ta", adm.Id, entity.TenantRoleNone, nil, false); err != nil || roleOf(t, adm.Id) != "" {
		t.Fatalf("root demote: %v", err)
	}
}

func inviteFor(t *testing.T, tenant, role string, project int64) string {
	t.Helper()
	inv, err := CreateTenantInviteWithGrant(tenant, 1, time.Hour, role, project)
	if err != nil {
		t.Fatal(err)
	}
	return inv.Code
}

func TestRedeemInviteForExistingUser(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	if err := DB.AutoMigrate(&TenantInvite{}); err != nil {
		t.Fatal(err)
	}
	u := seedRosterUser(t, "ta", "u", "", "u@x", "")
	adm := seedRosterUser(t, "ta", "adm", "", "adm@x", entity.TenantRoleAdmin)
	p := &entity.Project{TenantId: "ta", Name: "eng"}
	DB.Create(p)

	if _, err := RedeemInviteForExistingUser("", u.Id); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("empty code err = %v", err)
	}
	if _, err := RedeemInviteForExistingUser("nope", u.Id); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("unknown code err = %v", err)
	}
	if _, err := RedeemInviteForExistingUser(inviteFor(t, "ta", "", 0), 99999); !errors.Is(err, ErrMemberNotInTenant) {
		t.Fatalf("unknown user err = %v", err)
	}

	// foreign-tenant code: refused and left unspent
	foreign := inviteFor(t, "tb", "", 0)
	if _, err := RedeemInviteForExistingUser(foreign, u.Id); !errors.Is(err, ErrInviteWrongTenant) {
		t.Fatalf("foreign err = %v", err)
	}
	var fi TenantInvite
	DB.Where("code = ?", foreign).Take(&fi)
	if fi.Status != TenantInviteStatusPending {
		t.Fatalf("foreign code spent: status %d", fi.Status)
	}

	// plain code: consumed, no grant, second use refused
	plain := inviteFor(t, "ta", "", 0)
	g, err := RedeemInviteForExistingUser(plain, u.Id)
	if err != nil || g.MemberRole != "" || g.ProjectID != 0 || roleOf(t, u.Id) != "" {
		t.Fatalf("plain: %+v %v role=%q", g, err, roleOf(t, u.Id))
	}
	if _, err := RedeemInviteForExistingUser(plain, u.Id); !errors.Is(err, ErrInviteAlreadyConsumed) {
		t.Fatalf("reuse err = %v", err)
	}

	// project grant: no explicit role -> dept_lead + membership row
	pg := inviteFor(t, "ta", "", int64(p.Id))
	g, err = RedeemInviteForExistingUser(pg, u.Id)
	if err != nil || g.ProjectID != int64(p.Id) {
		t.Fatalf("project grant: %+v %v", g, err)
	}
	if roleOf(t, u.Id) != entity.TenantRoleDeptLead {
		t.Fatalf("role = %q, want dept_lead", roleOf(t, u.Id))
	}
	var n int64
	DB.Model(&entity.ProjectMember{}).Where("tenant_id = ? AND project_id = ? AND user_id = ?", "ta", p.Id, u.Id).Count(&n)
	if n != 1 {
		t.Fatalf("membership rows = %d", n)
	}

	// an admin is never demoted by a lower-role code
	g, err = RedeemInviteForExistingUser(inviteFor(t, "ta", entity.TenantRoleDeptLead, 0), adm.Id)
	if err != nil || g.MemberRole != entity.TenantRoleDeptLead {
		t.Fatalf("admin redeem: %+v %v", g, err)
	}
	if roleOf(t, adm.Id) != entity.TenantRoleAdmin {
		t.Fatalf("admin demoted to %q", roleOf(t, adm.Id))
	}

	// grant naming a project that no longer exists: rolled back, code unspent
	ghost := inviteFor(t, "ta", "", 424242)
	if _, err := RedeemInviteForExistingUser(ghost, u.Id); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("ghost project err = %v", err)
	}
	var gi TenantInvite
	DB.Where("code = ?", ghost).Take(&gi)
	if gi.Status != TenantInviteStatusPending {
		t.Fatalf("code spent despite rollback: %d", gi.Status)
	}
}

func TestRedeemInviteForExistingUser_StateGuards(t *testing.T) {
	defer setupSQLiteDB(t)()
	migrateMemberTables(t)
	if err := DB.AutoMigrate(&TenantInvite{}); err != nil {
		t.Fatal(err)
	}
	u := seedRosterUser(t, "ta", "u", "", "u@x", "")
	d := seedRosterUser(t, "default", "d", "", "d@x", "")

	code := inviteFor(t, "ta", "", 0)
	DB.Model(&TenantInvite{}).Where("code = ?", code).Update("status", TenantInviteStatusRevoked)
	if _, err := RedeemInviteForExistingUser(code, u.Id); !errors.Is(err, ErrInviteRevoked) {
		t.Fatalf("revoked err = %v", err)
	}

	code = inviteFor(t, "ta", "", 0)
	DB.Model(&TenantInvite{}).Where("code = ?", code).Update("expired_time", 1)
	if _, err := RedeemInviteForExistingUser(code, u.Id); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("expired err = %v", err)
	}

	code = inviteFor(t, "ta", "", 0)
	DB.Model(&TenantInvite{}).Where("code = ?", code).Update("status", 99)
	if _, err := RedeemInviteForExistingUser(code, u.Id); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("unknown status err = %v", err)
	}

	// default tenant: a role-bearing grant is refused
	code = inviteFor(t, "default", "", 0)
	DB.Model(&TenantInvite{}).Where("code = ?", code).Update("member_role", entity.TenantRoleAdmin)
	if _, err := RedeemInviteForExistingUser(code, d.Id); !errors.Is(err, ErrTenantRoleInDefault) {
		t.Fatalf("default grant err = %v", err)
	}
}

func TestInviteUsable_RevokedAtAlone(t *testing.T) {
	inv := &TenantInvite{Status: TenantInviteStatusPending, RevokedAt: 5}
	if err := inviteUsable(inv); !errors.Is(err, ErrInviteRevoked) {
		t.Fatalf("err = %v", err)
	}
	if err := inviteUsable(&TenantInvite{Status: TenantInviteStatusPending}); err != nil {
		t.Fatalf("clean pending err = %v", err)
	}
}
