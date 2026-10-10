package handler

// v2_tenant_members_test.go — tenant onboarding loop (migration 048): payer
// ownership of admin-issued keys, role assignment (root and tenant admin),
// tenant-side invite list / revoke / redeem, and the role fields on user/me.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func (e *entEnv) setPayer(t *testing.T, userID int) {
	t.Helper()
	if err := e.db.Model(&repo.Tenant{}).Where("id = ?", e.tenant).Update("payer_user_id", int64(userID)).Error; err != nil {
		t.Fatal(err)
	}
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	s, _ := ParseV2Response(t, w)["error_code"].(string)
	return s
}

// ---- payer ----

func TestBatchCreateTokensV2_PayerNotSetIs409(t *testing.T) {
	e := newEntEnv(t, "pay-a")
	e.setPayer(t, 0)
	w := e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(2, "x")})
	AssertV2Status(t, w, http.StatusConflict)
	if errCode(t, w) != "payer_not_set" {
		t.Fatalf("error_code = %q: %s", errCode(t, w), w.Body.String())
	}
	if n := e.tokenCount(t); n != 0 {
		t.Fatalf("%d tokens created without a payer (silent fallback to the caller)", n)
	}
}

func TestBatchCreateTokensV2_PayerFromAnotherTenantIs409(t *testing.T) {
	e := newEntEnv(t, "pay-b")
	e.setPayer(t, e.outsider.Id) // stale / foreign payer must not count as set
	w := e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(1, "x")})
	AssertV2Status(t, w, http.StatusConflict)
	if errCode(t, w) != "payer_not_set" {
		t.Fatalf("error_code = %q", errCode(t, w))
	}
}

func TestBatchCreateTokensV2_KeysBelongToPayerNotCaller(t *testing.T) {
	e := newEntEnv(t, "pay-c")
	acct := int64(4242)
	e.db.Model(&repo.User{}).Where("id = ?", e.member.Id).Update("lurus_account_id", acct)
	e.setPayer(t, e.member.Id)
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(2, "k")}), http.StatusCreated)
	var toks []repo.Token
	e.db.Where("employee_ref <> ''").Find(&toks)
	if len(toks) != 2 {
		t.Fatalf("tokens = %d, want 2", len(toks))
	}
	for _, tk := range toks {
		if tk.UserId != e.member.Id || tk.IdentityAccountID != acct {
			t.Fatalf("token owner=%d acct=%d, want payer %d / %d", tk.UserId, tk.IdentityAccountID, e.member.Id, acct)
		}
	}
}

func TestCreateTokenV2_EmployeeRefKeyBelongsToPayer(t *testing.T) {
	e := newEntEnv(t, "pay-d")
	e.setPayer(t, e.member.Id)
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/tokens",
		map[string]interface{}{"name": "n", "unlimited_quota": true, "employee_ref": "emp-1"}), http.StatusCreated)
	var tk repo.Token
	if err := e.db.Where("employee_ref = ?", "emp-1").First(&tk).Error; err != nil {
		t.Fatal(err)
	}
	if tk.UserId != e.member.Id {
		t.Fatalf("owner = %d, want payer %d", tk.UserId, e.member.Id)
	}
	e.setPayer(t, 0)
	w := e.do(e.admin, http.MethodPost, "/tokens",
		map[string]interface{}{"name": "n2", "unlimited_quota": true, "employee_ref": "emp-2"})
	AssertV2Status(t, w, http.StatusConflict)
	// A member's own plain key (no employee_ref) is unaffected by the payer.
	AssertV2Status(t, e.do(e.member, http.MethodPost, "/tokens",
		map[string]interface{}{"name": "mine", "unlimited_quota": true}), http.StatusCreated)
}

// ---- root role endpoint ----

func rootRoleEngine(tenantUserCaller int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", tenantUserCaller); c.Set("role", common.RoleRootUser); c.Next() })
	r.PUT("/api/v2/admin/tenants/:id/members/:user_id/role", SetTenantMemberRoleAdmin)
	return r
}

func TestSetTenantMemberRoleAdmin(t *testing.T) {
	e := newEntEnv(t, "role-a")
	r := rootRoleEngine(1)
	put := func(tenant string, uid int, body interface{}) *httptest.ResponseRecorder {
		return V2Request(r, http.MethodPut, "/api/v2/admin/tenants/"+tenant+"/members/"+strconv.Itoa(uid)+"/role", body, nil)
	}
	yes := true
	AssertV2Status(t, put(e.tenant, e.member.Id, map[string]interface{}{"tenant_role": "admin", "payer": yes}), http.StatusOK)
	if got := e.roleOf(t, e.member); got != entity.TenantRoleAdmin {
		t.Fatalf("role = %q, want admin", got)
	}
	if pid, _ := repo.TenantPayerID(e.tenant); pid != e.member.Id {
		t.Fatalf("payer = %d, want %d", pid, e.member.Id)
	}
	// payer:false clears only when that user is the payer.
	AssertV2Status(t, put(e.tenant, e.lead.Id, map[string]interface{}{"tenant_role": "dept_lead", "payer": false}), http.StatusOK)
	if pid, _ := repo.TenantPayerID(e.tenant); pid != e.member.Id {
		t.Fatalf("payer cleared by a non-payer: %d", pid)
	}
	AssertV2Status(t, put(e.tenant, e.member.Id, map[string]interface{}{"tenant_role": "", "payer": false}), http.StatusOK)
	if pid, _ := repo.TenantPayerID(e.tenant); pid != 0 {
		t.Fatalf("payer = %d, want cleared", pid)
	}
	// Root may demote the last admin (no keepAdmin guard on this path).
	// Validation and isolation.
	AssertV2Status(t, put(e.tenant, e.member.Id, map[string]interface{}{"tenant_role": "root"}), http.StatusBadRequest)
	AssertV2Status(t, put(e.tenant, e.member.Id, map[string]interface{}{"payer": true}), http.StatusBadRequest)
	AssertV2Status(t, put(e.tenant, e.outsider.Id, map[string]interface{}{"tenant_role": "admin"}), http.StatusNotFound)
	AssertV2Status(t, put("no-such-tenant", e.member.Id, map[string]interface{}{"tenant_role": "admin"}), http.StatusNotFound)
	if got := e.roleOf(t, e.outsider); got != entity.TenantRoleAdmin { // unchanged seed value
		t.Fatalf("outsider role mutated: %q", got)
	}
}

func TestSetTenantMemberRoleAdmin_DefaultTenantRefused(t *testing.T) {
	e := newEntEnv(t, "default")
	r := rootRoleEngine(1)
	w := V2Request(r, http.MethodPut, "/api/v2/admin/tenants/default/members/"+strconv.Itoa(e.member.Id)+"/role",
		map[string]interface{}{"tenant_role": "admin"}, nil)
	AssertV2Status(t, w, http.StatusForbidden)
}

// ---- tenant admin role endpoint ----

func TestSetTenantMemberRoleV2(t *testing.T) {
	e := newEntEnv(t, "role-b")
	path := func(u *repo.User) string { return "/members/" + strconv.Itoa(u.Id) + "/role" }
	body := map[string]interface{}{"tenant_role": "dept_lead"}

	AssertV2Status(t, e.do(e.member, http.MethodPut, path(e.member), map[string]interface{}{"tenant_role": "admin"}), http.StatusForbidden)
	AssertV2Status(t, e.do(e.lead, http.MethodPut, path(e.member), body), http.StatusForbidden)
	if got := e.roleOf(t, e.member); got != "" {
		t.Fatalf("member escalated itself: %q", got)
	}

	AssertV2Status(t, e.do(e.admin, http.MethodPut, path(e.member), body), http.StatusOK)
	if got := e.roleOf(t, e.member); got != entity.TenantRoleDeptLead {
		t.Fatalf("role = %q, want dept_lead", got)
	}
	// Foreign tenant's user: 404, untouched.
	AssertV2Status(t, e.do(e.admin, http.MethodPut, path(e.outsider), map[string]interface{}{"tenant_role": ""}), http.StatusNotFound)
	if got := e.roleOf(t, e.outsider); got != entity.TenantRoleAdmin {
		t.Fatalf("foreign admin demoted: %q", got)
	}
	AssertV2Status(t, e.do(e.admin, http.MethodPut, path(e.member), map[string]interface{}{"tenant_role": "owner"}), http.StatusBadRequest)
	// payer in the body is root-only: ignored here.
	AssertV2Status(t, e.do(e.admin, http.MethodPut, path(e.member), map[string]interface{}{"tenant_role": "dept_lead", "payer": true}), http.StatusOK)
	if pid, _ := repo.TenantPayerID(e.tenant); pid != e.admin.Id {
		t.Fatalf("tenant admin changed the payer: %d", pid)
	}
}

func TestSetTenantMemberRoleV2_LastAdminKept(t *testing.T) {
	e := newEntEnv(t, "role-c")
	self := "/members/" + strconv.Itoa(e.admin.Id) + "/role"
	w := e.do(e.admin, http.MethodPut, self, map[string]interface{}{"tenant_role": ""})
	AssertV2Status(t, w, http.StatusConflict)
	if errCode(t, w) != "LAST_TENANT_ADMIN" {
		t.Fatalf("code = %q", errCode(t, w))
	}
	if got := e.roleOf(t, e.admin); got != entity.TenantRoleAdmin {
		t.Fatalf("last admin demoted: %q", got)
	}
	// With a second admin the first may step down.
	AssertV2Status(t, e.do(e.admin, http.MethodPut, "/members/"+strconv.Itoa(e.member.Id)+"/role",
		map[string]interface{}{"tenant_role": "admin"}), http.StatusOK)
	AssertV2Status(t, e.do(e.admin, http.MethodPut, self, map[string]interface{}{"tenant_role": "dept_lead"}), http.StatusOK)
}

func TestSetTenantMemberRoleV2_CrossTenantUserIs404(t *testing.T) {
	TestSetTenantMemberRoleV2(t) // the 404 / untouched-foreign-admin assertions live there
}

// ---- invites ----

func (e *entEnv) invite(t *testing.T, tenant string, mut func(*entity.TenantInvite)) *entity.TenantInvite {
	t.Helper()
	inv := &entity.TenantInvite{TenantId: tenant, Code: common.GetUUID(), Status: entity.TenantInviteStatusPending,
		CreatedByUserId: e.admin.Id, CreatedAt: time.Now()}
	if mut != nil {
		mut(inv)
	}
	if err := e.db.Create(inv).Error; err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestIssueMyTenantInviteV2_DefaultTTLAndCap(t *testing.T) {
	e := newEntEnv(t, "inv-a")
	before := common.GetTimestamp()
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/invites", map[string]interface{}{}), http.StatusCreated)
	var inv entity.TenantInvite
	e.db.Where("tenant_id = ?", e.tenant).First(&inv)
	want := before + 72*3600
	if inv.ExpiredTime < want || inv.ExpiredTime > want+5 {
		t.Fatalf("expired_time = %d, want ~%d (72h default)", inv.ExpiredTime, want)
	}
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/invites", map[string]interface{}{"ttl_hours": 720}), http.StatusCreated)
	w := e.do(e.admin, http.MethodPost, "/invites", map[string]interface{}{"ttl_hours": 721})
	AssertV2Status(t, w, http.StatusBadRequest)
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/invites", map[string]interface{}{"ttl_hours": -1}), http.StatusBadRequest)
}

func TestListMyTenantInvitesV2(t *testing.T) {
	e := newEntEnv(t, "inv-b")
	p := e.project(t, "Dept", "")
	e.invite(t, e.tenant, func(i *entity.TenantInvite) {
		i.MemberRole = "dept_lead"
		i.ProjectId = int64(p.Id)
		i.ExpiredTime = common.GetTimestamp() + 3600
	})
	e.invite(t, e.tenant, func(i *entity.TenantInvite) { i.Status = entity.TenantInviteStatusRevoked; i.RevokedAt = 5 })
	e.invite(t, e.tenant, func(i *entity.TenantInvite) { i.Status = entity.TenantInviteStatusConsumed })
	e.invite(t, "other-tenant", nil)

	AssertV2Status(t, e.do(e.member, http.MethodGet, "/invites", nil), http.StatusForbidden)
	AssertV2Status(t, e.do(e.lead, http.MethodGet, "/invites", nil), http.StatusForbidden)
	w := e.do(e.admin, http.MethodGet, "/invites", nil)
	AssertV2Status(t, w, http.StatusOK)
	data := ParseV2Response(t, w)["data"].(map[string]interface{})
	items := data["invites"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("invites = %d, want 3 (other tenant's must not appear)", len(items))
	}
	var used, revoked, withGrant int
	for _, it := range items {
		m := it.(map[string]interface{})
		if m["used"] == true {
			used++
		}
		if m["revoked"] == true {
			revoked++
		}
		if m["member_role"] == "dept_lead" && int(m["project_id"].(float64)) == p.Id && m["expires_at"].(float64) > 0 {
			withGrant++
		}
		if _, leaked := m["code"]; leaked {
			t.Fatalf("full code leaked in list: %v", m)
		}
	}
	if used != 1 || revoked != 1 || withGrant != 1 {
		t.Fatalf("used/revoked/grant = %d/%d/%d, want 1/1/1", used, revoked, withGrant)
	}
}

func TestRevokeMyTenantInviteV2_CrossTenantIs404(t *testing.T) {
	e := newEntEnv(t, "inv-c")
	mine := e.invite(t, e.tenant, nil)
	theirs := e.invite(t, "other-tenant", nil)
	del := func(u *repo.User, id int) *httptest.ResponseRecorder {
		return e.do(u, http.MethodDelete, "/invites/"+strconv.Itoa(id), nil)
	}
	AssertV2Status(t, del(e.member, mine.Id), http.StatusForbidden)
	AssertV2Status(t, del(e.admin, theirs.Id), http.StatusNotFound)
	var got entity.TenantInvite
	e.db.First(&got, theirs.Id)
	if got.Status != entity.TenantInviteStatusPending || got.RevokedAt != 0 {
		t.Fatalf("foreign invite revoked: %+v", got)
	}
	AssertV2Status(t, del(e.admin, mine.Id), http.StatusOK)
	got = entity.TenantInvite{}
	e.db.First(&got, mine.Id)
	if got.Status != entity.TenantInviteStatusRevoked || got.RevokedAt == 0 {
		t.Fatalf("revoke not recorded: %+v", got)
	}
	AssertV2Status(t, del(e.admin, mine.Id), http.StatusNotFound) // already revoked
}

func TestRedeemMyTenantInviteV2_FailureCodes(t *testing.T) {
	e := newEntEnv(t, "inv-d")
	redeem := func(u *repo.User, code string) *httptest.ResponseRecorder {
		return e.do(u, http.MethodPost, "/invites/redeem", map[string]interface{}{"code": code})
	}
	revoked := e.invite(t, e.tenant, nil)
	AssertV2Status(t, e.do(e.admin, http.MethodDelete, "/invites/"+strconv.Itoa(revoked.Id), nil), http.StatusOK)
	w := redeem(e.member, revoked.Code)
	AssertV2Status(t, w, http.StatusGone)
	if errCode(t, w) != "INVITE_REVOKED" {
		t.Fatalf("code = %q", errCode(t, w))
	}
	expired := e.invite(t, e.tenant, func(i *entity.TenantInvite) { i.ExpiredTime = common.GetTimestamp() - 10 })
	w = redeem(e.member, expired.Code)
	AssertV2Status(t, w, http.StatusGone)
	if errCode(t, w) != "INVITE_EXPIRED" {
		t.Fatalf("code = %q", errCode(t, w))
	}
	AssertV2Status(t, redeem(e.member, "nope"), http.StatusNotFound)
	AssertV2Status(t, e.do(e.member, http.MethodPost, "/invites/redeem", map[string]interface{}{}), http.StatusBadRequest)
	if got := e.roleOf(t, e.member); got != "" {
		t.Fatalf("failed redemption changed the role: %q", got)
	}
}

func TestRedeemMyTenantInviteV2_ExistingUserGetsGrantOnce(t *testing.T) {
	e := newEntEnv(t, "inv-e")
	p := e.project(t, "Dept", "")
	inv := e.invite(t, e.tenant, func(i *entity.TenantInvite) { i.MemberRole = "dept_lead"; i.ProjectId = int64(p.Id) })
	w := e.do(e.member, http.MethodPost, "/invites/redeem", map[string]interface{}{"code": inv.Code})
	AssertV2Status(t, w, http.StatusOK)
	if got := e.roleOf(t, e.member); got != entity.TenantRoleDeptLead {
		t.Fatalf("role = %q, want dept_lead", got)
	}
	if ids, _ := repo.ListProjectIDsForUser(e.tenant, e.member.Id); len(ids) != 1 || ids[0] != p.Id {
		t.Fatalf("membership = %v", ids)
	}
	// One-time: a second redemption fails and the code is spent.
	w = e.do(e.lead, http.MethodPost, "/invites/redeem", map[string]interface{}{"code": inv.Code})
	AssertV2Status(t, w, http.StatusConflict)
	if errCode(t, w) != "INVITE_ALREADY_CONSUMED" {
		t.Fatalf("code = %q", errCode(t, w))
	}
}

func TestRedeemMyTenantInviteV2_AdminNotDemoted(t *testing.T) {
	e := newEntEnv(t, "inv-f")
	inv := e.invite(t, e.tenant, func(i *entity.TenantInvite) { i.MemberRole = "dept_lead" })
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/invites/redeem", map[string]interface{}{"code": inv.Code}), http.StatusOK)
	if got := e.roleOf(t, e.admin); got != entity.TenantRoleAdmin {
		t.Fatalf("admin demoted by a dept_lead code: %q", got)
	}
}

func TestRedeemMyTenantInviteV2_CrossTenantRejectedAndNotConsumed(t *testing.T) {
	e := newEntEnv(t, "inv-g")
	foreign := e.invite(t, "other-tenant", func(i *entity.TenantInvite) { i.MemberRole = "admin" })
	w := e.do(e.member, http.MethodPost, "/invites/redeem", map[string]interface{}{"code": foreign.Code})
	AssertV2Status(t, w, http.StatusNotFound)
	if got := e.roleOf(t, e.member); got != "" {
		t.Fatalf("cross-tenant code escalated the member: %q", got)
	}
	// And the reverse: another tenant's user cannot burn this tenant's code.
	mine := e.invite(t, e.tenant, func(i *entity.TenantInvite) { i.MemberRole = "admin" })
	AssertV2Status(t, e.do(e.outsider, http.MethodPost, "/invites/redeem", map[string]interface{}{"code": mine.Code}), http.StatusNotFound)
	var got entity.TenantInvite
	e.db.First(&got, mine.Id)
	if got.Status != entity.TenantInviteStatusPending {
		t.Fatalf("code consumed by a foreign user: status %d", got.Status)
	}
	got = entity.TenantInvite{}
	e.db.First(&got, foreign.Id)
	if got.Status != entity.TenantInviteStatusPending {
		t.Fatalf("foreign code consumed: status %d", got.Status)
	}
}

func TestConsumeTenantInviteGrant_RevokedAtBlocks(t *testing.T) {
	e := newEntEnv(t, "inv-h")
	inv := e.invite(t, e.tenant, func(i *entity.TenantInvite) { i.RevokedAt = 7 })
	if _, _, err := repo.ConsumeTenantInviteGrant(inv.Code, 1); !errors.Is(err, repo.ErrInviteRevoked) {
		t.Fatalf("err = %v, want ErrInviteRevoked", err)
	}
}

// ---- role fields on user/me ----

func TestGetSelfV2_ReportsTenantRoleAndPayer(t *testing.T) {
	e := newEntEnv(t, "me-a")
	get := func(u *repo.User) map[string]interface{} {
		w := e.do(u, http.MethodGet, "/user/me", nil)
		AssertV2Status(t, w, http.StatusOK)
		return ParseV2Response(t, w)["data"].(map[string]interface{})
	}
	d := get(e.admin)
	if d["tenant_role"] != "admin" || d["is_payer"] != true {
		t.Fatalf("admin me = role %v payer %v", d["tenant_role"], d["is_payer"])
	}
	d = get(e.member)
	if d["tenant_role"] != "" || d["is_payer"] != false {
		t.Fatalf("member me = role %v payer %v", d["tenant_role"], d["is_payer"])
	}
	d = get(e.lead)
	if d["tenant_role"] != "dept_lead" {
		t.Fatalf("lead me = %v", d["tenant_role"])
	}
}

func TestTenantRoleView_DefaultTenantHidden(t *testing.T) {
	if role, payer := tenantRoleView(&repo.User{Id: 3, TenantId: "default", TenantRole: "admin"}); role != "" || payer {
		t.Fatalf("default-tenant role leaked: %q %v", role, payer)
	}
}
