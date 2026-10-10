package handler

// v2_enterprise_issue_test.go — enterprise key issuing and department scoping
// (docs/plans/enterprise-hub §2.4): employee_ref / trusted headers on tokens,
// the roster batch endpoint, project external_code, project members, the
// dept_lead spend scope and role/project invites.
//
// The mock auth loads tenant_role from the users table on every request, so a
// role change made by one request is what the next request is judged by.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var entTestDBCounter atomic.Int64

type entEnv struct {
	r        *gin.Engine
	db       *gorm.DB
	tenant   string
	admin    *repo.User // tenant_role=admin
	lead     *repo.User // tenant_role=dept_lead
	member   *repo.User // no tenant_role
	outsider *repo.User // other tenant
}

func newEntEnv(t *testing.T, tenant string) *entEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:entissue%d?mode=memory&cache=shared", entTestDBCounter.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	for _, m := range []interface{}{
		&repo.User{}, &repo.Token{}, &repo.Log{}, &repo.Tenant{}, &entity.Project{},
		&entity.ProjectMember{}, &entity.TenantInvite{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate %T: %v", m, err)
		}
	}
	prevDB, prevLog, prevSQLite, prevRedis := repo.DB, repo.LOG_DB, common.UsingSQLite, common.RedisEnabled
	repo.DB, repo.LOG_DB = db, db
	repo.InitCol()
	common.UsingSQLite, common.RedisEnabled = true, false
	repo.ResetProjectDeptCache()
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.UsingSQLite, common.RedisEnabled = prevDB, prevLog, prevSQLite, prevRedis
		repo.ResetProjectDeptCache()
		if s, _ := db.DB(); s != nil {
			_ = s.Close()
		}
	})

	if err := db.Create(&repo.Tenant{Id: tenant, Name: tenant, Slug: tenant, Status: repo.TenantStatusEnabled,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("tenant: %v", err)
	}
	mk := func(name, tenantID, role string) *repo.User {
		u := &repo.User{Username: name, Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
			TenantId: tenantID, TenantRole: role, Email: name + "@ent.test"}
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("user %s: %v", name, err)
		}
		return u
	}
	e := &entEnv{db: db, tenant: tenant,
		admin:    mk("ent-admin", tenant, entity.TenantRoleAdmin),
		lead:     mk("ent-lead", tenant, entity.TenantRoleDeptLead),
		member:   mk("ent-member", tenant, ""),
		outsider: mk("ent-outsider", "other-tenant", entity.TenantRoleAdmin),
	}

	// Migration 048: admin-issued keys are booked to the tenant payer. Default
	// the payer to the admin so the pre-048 scenarios keep their meaning; the
	// payer tests overwrite it.
	if err := db.Model(&repo.Tenant{}).Where("id = ?", tenant).Update("payer_user_id", int64(e.admin.Id)).Error; err != nil {
		t.Fatalf("payer: %v", err)
	}

	r := gin.New()
	auth := func(c *gin.Context) {
		uid, _ := strconv.Atoi(c.GetHeader("X-U"))
		u, err := repo.GetUserById(uid, false)
		if err != nil || u == nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		tc := &middleware.TenantContext{
			TenantID: tenant, UserID: uid,
			RoleTenantID: u.TenantId, TenantRole: u.TenantRole,
		}
		c.Set("tenant_context", tc)
		c.Set("tenant_id", tenant)
		c.Set("user_id", uid)
		c.Set("id", uid)
		c.Set("role", u.Role)
		c.Set("identity_account_id", int64(1))
		c.Next()
	}
	g := r.Group("/api/v2/:tenant_slug")
	g.Use(auth)
	g.POST("/tokens", CreateTokenV2)
	g.PUT("/tokens/:id", UpdateTokenV2)
	g.POST("/tokens/batch", BatchCreateTokensV2)
	g.GET("/projects/spend", GetProjectSpendV2)
	g.POST("/projects", CreateProjectV2)
	g.PUT("/projects/:id", UpdateProjectV2)
	g.DELETE("/projects/:id", DeleteProjectV2)
	g.POST("/projects/:id/restore", RestoreProjectV2)
	g.GET("/projects/:id/members", ListProjectMembersV2)
	g.POST("/projects/:id/members", AddProjectMemberV2)
	g.DELETE("/projects/:id/members", RemoveProjectMemberV2)
	g.POST("/invites", IssueMyTenantInviteV2)
	g.GET("/invites", ListMyTenantInvitesV2)
	g.POST("/invites/redeem", RedeemMyTenantInviteV2)
	g.DELETE("/invites/:id", RevokeMyTenantInviteV2)
	g.PUT("/members/:user_id/role", SetTenantMemberRoleV2)
	g.GET("/user/me", GetSelfV2)
	g.GET("/members", ListTenantMembersV2)
	g.GET("/projects", ListProjectsV2)
	g.GET("/tokens", ListTokensV2)
	e.r = r
	return e
}

func (e *entEnv) do(u *repo.User, method, path string, body interface{}) *httptest.ResponseRecorder {
	return V2Request(e.r, method, "/api/v2/"+e.tenant+path, body, map[string]string{"X-U": strconv.Itoa(u.Id)})
}

func (e *entEnv) project(t *testing.T, name, code string) *entity.Project {
	t.Helper()
	p := &entity.Project{TenantId: e.tenant, Name: name, ExternalCode: code}
	if err := e.db.Create(p).Error; err != nil {
		t.Fatalf("project: %v", err)
	}
	return p
}

func (e *entEnv) tokenCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	e.db.Model(&repo.Token{}).Where("employee_ref <> ''").Count(&n)
	return n
}

func (e *entEnv) roleOf(t *testing.T, u *repo.User) string {
	t.Helper()
	var got repo.User
	if err := e.db.First(&got, u.Id).Error; err != nil {
		t.Fatal(err)
	}
	return got.TenantRole
}

func roster(n int, prefix string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]interface{}{"employee_ref": fmt.Sprintf("%s%d", prefix, i), "unlimited_quota": true})
	}
	return out
}

// ---- token create / update: employee_ref + trusted headers ----

func TestCreateTokenV2_EmployeeRefUniqueAndValidated(t *testing.T) {
	e := newEntEnv(t, "ent-a")
	body := func(ref string) map[string]interface{} {
		return map[string]interface{}{"name": "k-" + ref, "employee_ref": ref, "unlimited_quota": true}
	}
	w := e.do(e.admin, http.MethodPost, "/tokens", body("emp-001"))
	AssertV2Status(t, w, http.StatusCreated)
	if d := ParseV2Response(t, w)["data"].(map[string]interface{}); d["employee_ref"] != "emp-001" {
		t.Fatalf("employee_ref not echoed: %v", d)
	}
	w = e.do(e.admin, http.MethodPost, "/tokens", body("emp-001"))
	AssertV2Status(t, w, http.StatusConflict)
	if ParseV2Response(t, w)["error_code"] != "TOKEN_EMPLOYEE_REF_CONFLICT" {
		t.Fatalf("conflict code: %s", w.Body.String())
	}
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/tokens", body("bad ref!")), http.StatusBadRequest)
}

func TestCreateTokenV2_TrustedHeadersAdminOnly(t *testing.T) {
	e := newEntEnv(t, "ent-b")
	req := map[string]interface{}{"name": "gw", "unlimited_quota": true, "trusted_identity_headers": true}
	for _, u := range []*repo.User{e.member, e.lead} {
		w := e.do(u, http.MethodPost, "/tokens", req)
		AssertV2Status(t, w, http.StatusForbidden)
		if ParseV2Response(t, w)["error_code"] != "TOKEN_TRUSTED_HEADERS_ADMIN_ONLY" {
			t.Fatalf("code: %s", w.Body.String())
		}
	}
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/tokens", req), http.StatusCreated)
}

func TestUpdateTokenV2_TrustedFlipAndRefConflict(t *testing.T) {
	e := newEntEnv(t, "ent-c")
	mkTok := func(u *repo.User, ref string) int {
		w := e.do(u, http.MethodPost, "/tokens", map[string]interface{}{"name": "n" + ref, "employee_ref": ref, "unlimited_quota": true})
		AssertV2Status(t, w, http.StatusCreated)
		return int(ParseV2Response(t, w)["data"].(map[string]interface{})["id"].(float64))
	}
	a, b := mkTok(e.admin, "ref-a"), mkTok(e.admin, "ref-b")
	own := mkTok(e.member, "")
	// Turning trusted ON is admin-only: the owner (a plain member) is refused.
	w := e.do(e.member, http.MethodPut, fmt.Sprintf("/tokens/%d", own), map[string]interface{}{"trusted_identity_headers": true})
	AssertV2Status(t, w, http.StatusForbidden)
	// Re-pointing a key at an already-used ref is a conflict.
	w = e.do(e.admin, http.MethodPut, fmt.Sprintf("/tokens/%d", b), map[string]interface{}{"employee_ref": "ref-a"})
	AssertV2Status(t, w, http.StatusConflict)
	// Keeping its own ref is not a conflict.
	w = e.do(e.admin, http.MethodPut, fmt.Sprintf("/tokens/%d", b), map[string]interface{}{"employee_ref": "ref-b"})
	AssertV2Status(t, w, http.StatusOK)
	_ = a
}

// employee_ref is the attribution identity bills group by: only a tenant admin
// may set, change or clear it (otherwise a member could squat a roster id).
func TestTokenV2_EmployeeRefAdminOnly_ThreeState(t *testing.T) {
	e := newEntEnv(t, "ent-ref")
	withRef := map[string]interface{}{"name": "k", "employee_ref": "alice", "unlimited_quota": true}
	for _, u := range []*repo.User{e.member, e.lead} {
		w := e.do(u, http.MethodPost, "/tokens", withRef)
		AssertV2Status(t, w, http.StatusForbidden)
		if ParseV2Response(t, w)["error_code"] != "TOKEN_EMPLOYEE_REF_ADMIN_ONLY" {
			t.Fatalf("code: %s", w.Body.String())
		}
	}
	if n := e.tokenCount(t); n != 0 {
		t.Fatalf("%d refs squatted by refused callers", n)
	}
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/tokens", withRef), http.StatusCreated)
	// A plain member can still mint a key that carries no ref.
	AssertV2Status(t, e.do(e.member, http.MethodPost, "/tokens", map[string]interface{}{"name": "mine", "unlimited_quota": true}), http.StatusCreated)

	// Update: a member-owned key that already carries a (legacy) ref.
	legacy := &repo.Token{UserId: e.member.Id, TenantId: e.tenant, Name: "legacy", Key: "legacykey000000000000000000000000000000000000001",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, EmployeeRef: "bob"}
	if err := e.db.Create(legacy).Error; err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/tokens/%d", legacy.Id)
	AssertV2Status(t, e.do(e.member, http.MethodPut, path, map[string]interface{}{"employee_ref": "carol"}), http.StatusForbidden)
	AssertV2Status(t, e.do(e.member, http.MethodPut, path, map[string]interface{}{"employee_ref": ""}), http.StatusForbidden)
	// Re-sending the unchanged value is not a change.
	AssertV2Status(t, e.do(e.member, http.MethodPut, path, map[string]interface{}{"employee_ref": "bob"}), http.StatusOK)
	var got repo.Token
	e.db.First(&got, legacy.Id)
	if got.EmployeeRef != "bob" {
		t.Fatalf("employee_ref = %q after refused edits, want bob", got.EmployeeRef)
	}
}

// ---- batch ----

func TestBatchCreateTokensV2_ThreeStatePermission(t *testing.T) {
	e := newEntEnv(t, "ent-d")
	body := map[string]interface{}{"tokens": roster(2, "p")}
	AssertV2Status(t, e.do(e.member, http.MethodPost, "/tokens/batch", body), http.StatusForbidden)
	AssertV2Status(t, e.do(e.lead, http.MethodPost, "/tokens/batch", body), http.StatusForbidden)
	if n := e.tokenCount(t); n != 0 {
		t.Fatalf("%d tokens created by refused callers", n)
	}
	w := e.do(e.admin, http.MethodPost, "/tokens/batch", body)
	AssertV2Status(t, w, http.StatusCreated)
	data := ParseV2Response(t, w)["data"].(map[string]interface{})
	created := data["created"].([]interface{})
	if len(created) != 2 {
		t.Fatalf("created = %d, want 2", len(created))
	}
	for _, c := range created {
		k, _ := c.(map[string]interface{})["key"].(string)
		if len(k) < 10 || k[:3] != "sk-" {
			t.Fatalf("plaintext key missing from the create response: %v", c)
		}
	}
}

func TestBatchCreateTokensV2_IdempotentPerEmployeeRef(t *testing.T) {
	e := newEntEnv(t, "ent-e")
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(3, "r")}), http.StatusCreated)
	// Re-upload a superset: 3 skipped, 1 new.
	w := e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(4, "r")})
	AssertV2Status(t, w, http.StatusCreated)
	data := ParseV2Response(t, w)["data"].(map[string]interface{})
	if len(data["created"].([]interface{})) != 1 || len(data["skipped"].([]interface{})) != 3 {
		t.Fatalf("created/skipped = %v/%v, want 1/3", data["created"], data["skipped"])
	}
	// Pure replay creates nothing and says so with 200.
	w = e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(4, "r")})
	AssertV2Status(t, w, http.StatusOK)
	if n := e.tokenCount(t); n != 4 {
		t.Fatalf("tokens = %d after replays, want 4", n)
	}
}

func TestBatchCreateTokensV2_SizeCapAndPayloadValidation(t *testing.T) {
	e := newEntEnv(t, "ent-f")
	w := e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(501, "big")})
	AssertV2Status(t, w, http.StatusBadRequest)
	if ParseV2Response(t, w)["error_code"] != "TOKEN_BATCH_TOO_LARGE" {
		t.Fatalf("code: %s", w.Body.String())
	}
	// Exactly the cap is accepted.
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(500, "ok")}), http.StatusCreated)

	e2 := newEntEnv(t, "ent-f2")
	dup := []map[string]interface{}{{"employee_ref": "x", "unlimited_quota": true}, {"employee_ref": "x", "unlimited_quota": true}}
	AssertV2Status(t, e2.do(e2.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": dup}), http.StatusBadRequest)
	AssertV2Status(t, e2.do(e2.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": []interface{}{}}), http.StatusBadRequest)
	bad := []map[string]interface{}{{"employee_ref": "fine", "unlimited_quota": true}, {"employee_ref": "no spaces", "unlimited_quota": true}}
	AssertV2Status(t, e2.do(e2.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": bad}), http.StatusBadRequest)
	if n := e2.tokenCount(t); n != 0 {
		t.Fatalf("%d tokens created by a rejected upload (must be all-or-nothing)", n)
	}
}

func TestBatchCreateTokensV2_MidBatchFailureRollsBackEverything(t *testing.T) {
	e := newEntEnv(t, "ent-g")
	prev := batchTokenKeyGen
	t.Cleanup(func() { batchTokenKeyGen = prev })
	batchTokenKeyGen = func() (string, error) { return "00000000000000000000000000000000000000000000000a", nil } // same key every time
	w := e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(3, "rb")})
	if w.Code < 400 {
		t.Fatalf("status = %d, want a failure for a colliding key", w.Code)
	}
	if w.Code == http.StatusConflict {
		t.Fatalf("a key collision was reported as an employee_ref conflict: %s", w.Body.String())
	}
	if n := e.tokenCount(t); n != 0 {
		t.Fatalf("%d rows survived a failed batch, want 0 (single transaction)", n)
	}

	calls := 0
	batchTokenKeyGen = func() (string, error) {
		calls++
		if calls == 3 {
			return "", errors.New("entropy exhausted")
		}
		return fmt.Sprintf("%048d", calls), nil
	}
	w = e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": roster(4, "rc")})
	AssertV2Status(t, w, http.StatusInternalServerError)
	if n := e.tokenCount(t); n != 0 {
		t.Fatalf("%d rows after a key-generation failure, want 0", n)
	}
}

func TestBatchCreateTokensV2_CrossTenantProjectRejected(t *testing.T) {
	e := newEntEnv(t, "ent-h")
	foreign := &entity.Project{TenantId: "other-tenant", Name: "theirs"}
	e.db.Create(foreign)
	items := roster(2, "xp")
	items[1]["project_id"] = foreign.Id
	w := e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": items})
	AssertV2Status(t, w, http.StatusBadRequest)
	if n := e.tokenCount(t); n != 0 {
		t.Fatalf("%d tokens created despite a foreign project", n)
	}
	own := e.project(t, "mine", "")
	items[1]["project_id"] = own.Id
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/tokens/batch", map[string]interface{}{"tokens": items}), http.StatusCreated)
}

// ---- project external_code + dept cache ----

func TestProjectV2_ExternalCodeConflict(t *testing.T) {
	e := newEntEnv(t, "ent-i")
	w := e.do(e.admin, http.MethodPost, "/projects", map[string]interface{}{"name": "Finance", "external_code": "FIN"})
	AssertV2Status(t, w, http.StatusCreated)
	if d := ParseV2Response(t, w)["data"].(map[string]interface{}); d["external_code"] != "FIN" {
		t.Fatalf("external_code not echoed: %v", d)
	}
	w = e.do(e.admin, http.MethodPost, "/projects", map[string]interface{}{"name": "Fin2", "external_code": "FIN"})
	AssertV2Status(t, w, http.StatusConflict)
	if ParseV2Response(t, w)["error_code"] != "PROJECT_EXTERNAL_CODE_CONFLICT" {
		t.Fatalf("code: %s", w.Body.String())
	}
	var n int64
	e.db.Model(&entity.Project{}).Where("tenant_id = ? AND name = ?", e.tenant, "Fin2").Count(&n)
	if n != 0 {
		t.Fatal("a rejected create left a project behind")
	}
	other := e.project(t, "Ops", "OPS")
	w = e.do(e.admin, http.MethodPut, fmt.Sprintf("/projects/%d", other.Id), map[string]interface{}{"name": "Ops", "external_code": "FIN"})
	AssertV2Status(t, w, http.StatusConflict)
	// Own code stays valid; a non-admin cannot set one.
	w = e.do(e.admin, http.MethodPut, fmt.Sprintf("/projects/%d", other.Id), map[string]interface{}{"name": "Ops", "external_code": "OPS"})
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Status(t, e.do(e.member, http.MethodPost, "/projects", map[string]interface{}{"name": "Z", "external_code": "Z"}), http.StatusForbidden)
}

// A cached "no such department" must not outlive the project that creates it.
func TestProjectV2_WritesResetDeptCache(t *testing.T) {
	e := newEntEnv(t, "ent-j")
	if _, found := repo.ResolveProjectByDeptCode(e.tenant, "FIN"); found {
		t.Fatal("precondition: FIN must not resolve yet")
	}
	w := e.do(e.admin, http.MethodPost, "/projects", map[string]interface{}{"name": "Finance", "external_code": "FIN"})
	AssertV2Status(t, w, http.StatusCreated)
	id := int(ParseV2Response(t, w)["data"].(map[string]interface{})["id"].(float64))
	if got, found := repo.ResolveProjectByDeptCode(e.tenant, "FIN"); !found || got != id {
		t.Fatalf("after create resolve = %d,%v; want %d,true (stale negative cache)", got, found, id)
	}

	// Re-code: the old code must stop resolving at once.
	AssertV2Status(t, e.do(e.admin, http.MethodPut, fmt.Sprintf("/projects/%d", id), map[string]interface{}{"name": "Finance", "external_code": "FIN2"}), http.StatusOK)
	if _, found := repo.ResolveProjectByDeptCode(e.tenant, "FIN"); found {
		t.Fatal("old code still resolves after update (stale positive cache)")
	}
	if got, found := repo.ResolveProjectByDeptCode(e.tenant, "FIN2"); !found || got != id {
		t.Fatalf("new code resolve = %d,%v", got, found)
	}

	// Delete drops it; restore brings it back.
	AssertV2Status(t, e.do(e.admin, http.MethodDelete, fmt.Sprintf("/projects/%d", id), nil), http.StatusOK)
	if _, found := repo.ResolveProjectByDeptCode(e.tenant, "FIN2"); found {
		t.Fatal("deleted project still resolves")
	}
	AssertV2Status(t, e.do(e.admin, http.MethodPost, fmt.Sprintf("/projects/%d/restore", id), map[string]interface{}{}), http.StatusOK)
	if _, found := repo.ResolveProjectByDeptCode(e.tenant, "FIN2"); !found {
		t.Fatal("restored project does not resolve (stale negative cache)")
	}
}

// ---- members ----

func TestProjectMembersV2_ThreeStatePermission(t *testing.T) {
	e := newEntEnv(t, "ent-k")
	p := e.project(t, "Dept", "")
	path := fmt.Sprintf("/projects/%d/members", p.Id)
	add := map[string]interface{}{"user_id": e.member.Id}
	for _, u := range []*repo.User{e.member, e.lead} {
		AssertV2Status(t, e.do(u, http.MethodPost, path, add), http.StatusForbidden)
		AssertV2Status(t, e.do(u, http.MethodGet, path, nil), http.StatusForbidden)
		AssertV2Status(t, e.do(u, http.MethodDelete, path+"?user_id="+strconv.Itoa(e.member.Id), nil), http.StatusForbidden)
	}
	if got := e.roleOf(t, e.member); got != "" {
		t.Fatalf("refused callers changed a role to %q", got)
	}
	AssertV2Status(t, e.do(e.admin, http.MethodPost, path, add), http.StatusOK)
	if got := e.roleOf(t, e.member); got != entity.TenantRoleDeptLead {
		t.Fatalf("role = %q after add, want dept_lead", got)
	}
	w := e.do(e.admin, http.MethodGet, path, nil)
	AssertV2Status(t, w, http.StatusOK)
	if items := ParseV2Response(t, w)["data"].(map[string]interface{})["items"].([]interface{}); len(items) != 1 {
		t.Fatalf("members = %d, want 1", len(items))
	}
	AssertV2Status(t, e.do(e.admin, http.MethodDelete, path+"?user_id="+strconv.Itoa(e.member.Id), nil), http.StatusOK)
	if got := e.roleOf(t, e.member); got != "" {
		t.Fatalf("role = %q after last removal, want empty", got)
	}
}

func TestProjectMembersV2_CrossTenantIs404(t *testing.T) {
	e := newEntEnv(t, "ent-l")
	mine := e.project(t, "Mine", "")
	foreign := &entity.Project{TenantId: "other-tenant", Name: "Theirs"}
	e.db.Create(foreign)

	w := e.do(e.admin, http.MethodPost, fmt.Sprintf("/projects/%d/members", foreign.Id), map[string]interface{}{"user_id": e.member.Id})
	AssertV2Status(t, w, http.StatusNotFound)
	w = e.do(e.admin, http.MethodPost, fmt.Sprintf("/projects/%d/members", mine.Id), map[string]interface{}{"user_id": e.outsider.Id})
	AssertV2Status(t, w, http.StatusNotFound)
	AssertV2Status(t, e.do(e.admin, http.MethodGet, fmt.Sprintf("/projects/%d/members", foreign.Id), nil), http.StatusNotFound)
	var out repo.User
	e.db.First(&out, e.outsider.Id)
	if out.TenantRole != entity.TenantRoleAdmin {
		t.Fatalf("outsider role mutated to %q", out.TenantRole)
	}
}

func TestProjectMembersV2_DefaultTenantRefused(t *testing.T) {
	e := newEntEnv(t, "default")
	// In "default" only platform staff pass the admin gate; make the admin one.
	e.db.Model(&repo.User{}).Where("id = ?", e.admin.Id).Update("role", common.RoleAdminUser)
	p := e.project(t, "Dept", "")
	w := e.do(e.admin, http.MethodPost, fmt.Sprintf("/projects/%d/members", p.Id), map[string]interface{}{"user_id": e.member.Id})
	AssertV2Status(t, w, http.StatusForbidden)
	if ParseV2Response(t, w)["error_code"] != "TENANT_ROLE_FORBIDDEN_IN_DEFAULT" {
		t.Fatalf("code: %s", w.Body.String())
	}
	if got := e.roleOf(t, e.member); got != "" {
		t.Fatalf("default-tenant role written: %q", got)
	}
}

// ---- dept_lead spend scope ----

func TestProjectSpendV2_DeptLeadSeesOnlyOwnProjects(t *testing.T) {
	e := newEntEnv(t, "ent-m")
	mine, theirs := e.project(t, "Mine", ""), e.project(t, "Theirs", "")
	if err := repo.AddProjectMemberGranting(e.tenant, mine.Id, e.lead.Id); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for _, row := range []struct{ pid, quota int }{{mine.Id, 100}, {theirs.Id, 7000}, {0, 50000}} {
		if err := e.db.Create(&repo.Log{UserId: e.member.Id, TenantId: e.tenant, Type: repo.LogTypeConsume,
			ProjectId: row.pid, Quota: row.quota, CreatedAt: now}).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}

	AssertV2Status(t, e.do(e.member, http.MethodGet, "/projects/spend", nil), http.StatusForbidden)

	w := e.do(e.lead, http.MethodGet, "/projects/spend", nil)
	AssertV2Status(t, w, http.StatusOK)
	data := ParseV2Response(t, w)["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["name"] != "Mine" {
		t.Fatalf("dept_lead items = %v, want only Mine", items)
	}
	if data["total_quota"].(float64) != 100 {
		t.Fatalf("dept_lead total = %v, want 100 (no other department's spend)", data["total_quota"])
	}

	w = e.do(e.admin, http.MethodGet, "/projects/spend", nil)
	AssertV2Status(t, w, http.StatusOK)
	if got := len(ParseV2Response(t, w)["data"].(map[string]interface{})["items"].([]interface{})); got != 3 {
		t.Fatalf("admin items = %d, want 3 (both projects + unassigned)", got)
	}
}

// ---- invites ----

func TestIssueMyTenantInviteV2_GrantValidation(t *testing.T) {
	e := newEntEnv(t, "ent-n")
	p := e.project(t, "Dept", "")
	body := map[string]interface{}{"member_role": "dept_lead", "project_id": p.Id}

	AssertV2Status(t, e.do(e.member, http.MethodPost, "/invites", body), http.StatusForbidden)
	AssertV2Status(t, e.do(e.lead, http.MethodPost, "/invites", body), http.StatusForbidden)

	w := e.do(e.admin, http.MethodPost, "/invites", body)
	AssertV2Status(t, w, http.StatusCreated)
	var inv entity.TenantInvite
	if err := e.db.Where("tenant_id = ?", e.tenant).First(&inv).Error; err != nil {
		t.Fatal(err)
	}
	if inv.MemberRole != "dept_lead" || inv.ProjectId != int64(p.Id) {
		t.Fatalf("stored grant = %q/%d, want dept_lead/%d", inv.MemberRole, inv.ProjectId, p.Id)
	}

	foreign := &entity.Project{TenantId: "other-tenant", Name: "Theirs"}
	e.db.Create(foreign)
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/invites", map[string]interface{}{"project_id": foreign.Id}), http.StatusNotFound)
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/invites", map[string]interface{}{"member_role": "root"}), http.StatusBadRequest)
	// A plain invite (no grant) still works and stores no grant.
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/invites", map[string]interface{}{}), http.StatusCreated)
}

func TestIssueMyTenantInviteV2_DefaultTenantGrantRefused(t *testing.T) {
	e := newEntEnv(t, "default")
	e.db.Model(&repo.User{}).Where("id = ?", e.admin.Id).Update("role", common.RoleAdminUser)
	w := e.do(e.admin, http.MethodPost, "/invites", map[string]interface{}{"member_role": "admin"})
	AssertV2Status(t, w, http.StatusForbidden)
	if ParseV2Response(t, w)["error_code"] != "TENANT_ROLE_FORBIDDEN_IN_DEFAULT" {
		t.Fatalf("code: %s", w.Body.String())
	}
}

// Redeeming a grant-bearing invite at first login confers role and membership.
func TestApplyInviteGrant_HandlerConfersRoleAndProject(t *testing.T) {
	e := newEntEnv(t, "ent-o")
	p := e.project(t, "Dept", "")
	inv, err := repo.CreateTenantInviteWithGrant(e.tenant, e.admin.Id, 0, "", int64(p.Id))
	if err != nil {
		t.Fatal(err)
	}
	tenantID, grant, err := repo.ConsumeTenantInviteGrant(inv.Code, 9001)
	if err != nil || tenantID.Id != e.tenant {
		t.Fatalf("consume: %v %v", tenantID, err)
	}
	fresh := &repo.User{Username: "fresh", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, TenantId: e.tenant}
	if err := e.db.Create(fresh).Error; err != nil {
		t.Fatal(err)
	}
	applyInviteGrant(&gin.Context{}, fresh, e.tenant, grant)
	if got := e.roleOf(t, fresh); got != entity.TenantRoleDeptLead {
		t.Fatalf("role = %q, want dept_lead", got)
	}
	if ids, _ := repo.ListProjectIDsForUser(e.tenant, fresh.Id); len(ids) != 1 || ids[0] != p.Id {
		t.Fatalf("membership = %v, want [%d]", ids, p.Id)
	}
}

// A lead whose only project is retired must lose the role at once: DELETE
// /members 404s on a retired project, so nothing else could take it back.
func TestDeleteProjectV2_RevokesLeadsLeftWithoutProjects(t *testing.T) {
	e := newEntEnv(t, "ent-m")
	only := e.project(t, "Only", "")
	other := e.project(t, "Other", "")
	AssertV2Status(t, e.do(e.admin, http.MethodPost, fmt.Sprintf("/projects/%d/members", only.Id), map[string]interface{}{"user_id": e.member.Id}), http.StatusOK)
	// e.lead keeps a second project, so it must keep the role.
	AssertV2Status(t, e.do(e.admin, http.MethodPost, fmt.Sprintf("/projects/%d/members", only.Id), map[string]interface{}{"user_id": e.lead.Id}), http.StatusOK)
	AssertV2Status(t, e.do(e.admin, http.MethodPost, fmt.Sprintf("/projects/%d/members", other.Id), map[string]interface{}{"user_id": e.lead.Id}), http.StatusOK)

	AssertV2Status(t, e.do(e.admin, http.MethodDelete, fmt.Sprintf("/projects/%d", only.Id), nil), http.StatusOK)
	if got := e.roleOf(t, e.member); got != "" {
		t.Fatalf("member role = %q after its only project was deleted, want revoked", got)
	}
	if got := e.roleOf(t, e.lead); got != entity.TenantRoleDeptLead {
		t.Fatalf("lead with a remaining project lost its role: %q", got)
	}
}

// Restoring onto an external_code a live project has since taken is a code
// conflict, not a (misleading) name conflict.
func TestRestoreProjectV2_ExternalCodeTakenIsCodeConflict(t *testing.T) {
	e := newEntEnv(t, "ent-n")
	w := e.do(e.admin, http.MethodPost, "/projects", map[string]interface{}{"name": "Old", "external_code": "FIN"})
	AssertV2Status(t, w, http.StatusCreated)
	id := int(ParseV2Response(t, w)["data"].(map[string]interface{})["id"].(float64))
	AssertV2Status(t, e.do(e.admin, http.MethodDelete, fmt.Sprintf("/projects/%d", id), nil), http.StatusOK)
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/projects", map[string]interface{}{"name": "New", "external_code": "FIN"}), http.StatusCreated)

	w = e.do(e.admin, http.MethodPost, fmt.Sprintf("/projects/%d/restore", id), map[string]interface{}{})
	AssertV2Status(t, w, http.StatusConflict)
	if ParseV2Response(t, w)["error_code"] != "PROJECT_EXTERNAL_CODE_CONFLICT" {
		t.Fatalf("code: %s", w.Body.String())
	}
}
