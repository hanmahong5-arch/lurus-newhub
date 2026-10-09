package handler

// v2_tenant_roster_test.go — GET /members, dept_lead-scoped GET /projects and
// GET /tokens?scope=tenant.

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func listItems(t *testing.T, body map[string]interface{}) []interface{} {
	t.Helper()
	d, _ := body["data"].(map[string]interface{})
	items, _ := d["items"].([]interface{})
	return items
}

func TestListTenantMembersV2_AdminSeesRosterWithDepartments(t *testing.T) {
	e := newEntEnv(t, "roster-a")
	p := e.project(t, "Eng", "")
	e.project(t, "Empty", "") // zero-member, zero-usage department
	AssertV2Status(t, e.do(e.admin, http.MethodPost, "/projects/"+strconv.Itoa(p.Id)+"/members",
		map[string]interface{}{"user_id": e.member.Id}), http.StatusOK)

	w := e.do(e.admin, http.MethodGet, "/members", nil)
	AssertV2Status(t, w, http.StatusOK)
	body := ParseV2Response(t, w)
	items := listItems(t, body)
	if len(items) != 3 { // admin, lead, member; outsider excluded
		t.Fatalf("items = %d, want 3: %s", len(items), w.Body.String())
	}
	if total := body["data"].(map[string]interface{})["total"].(float64); total != 3 {
		t.Fatalf("total = %v", total)
	}
	byID := map[int]map[string]interface{}{}
	for _, it := range items {
		m := it.(map[string]interface{})
		byID[int(m["user_id"].(float64))] = m
	}
	if _, ok := byID[e.outsider.Id]; ok {
		t.Fatal("foreign-tenant user leaked into roster")
	}
	if byID[e.admin.Id]["is_payer"] != true || byID[e.member.Id]["is_payer"] != false {
		t.Fatalf("payer flags wrong: %v", byID)
	}
	if byID[e.member.Id]["email"] != "ent-member@ent.test" {
		t.Fatalf("email = %v", byID[e.member.Id]["email"])
	}
	deps := byID[e.member.Id]["departments"].([]interface{})
	// member got promoted to dept_lead by the grant; the dept is listed.
	if len(deps) != 1 || deps[0].(map[string]interface{})["name"] != "Eng" || deps[0].(map[string]interface{})["is_lead"] != true {
		t.Fatalf("departments = %v", deps)
	}
	if byID[e.admin.Id]["joined_at"] != nil {
		t.Fatalf("joined_at must be null (no source column): %v", byID[e.admin.Id]["joined_at"])
	}
}

func TestListTenantMembersV2_FilterAndPaging(t *testing.T) {
	e := newEntEnv(t, "roster-b")
	w := e.do(e.admin, http.MethodGet, "/members?role=dept_lead", nil)
	AssertV2Status(t, w, http.StatusOK)
	if n := len(listItems(t, ParseV2Response(t, w))); n != 1 {
		t.Fatalf("role=dept_lead -> %d", n)
	}
	w = e.do(e.admin, http.MethodGet, "/members?role=member", nil)
	if n := len(listItems(t, ParseV2Response(t, w))); n != 1 {
		t.Fatalf("role=member -> %d", n)
	}
	w = e.do(e.admin, http.MethodGet, "/members?keyword=ent-ad", nil)
	if n := len(listItems(t, ParseV2Response(t, w))); n != 1 {
		t.Fatalf("keyword -> %d", n)
	}
	w = e.do(e.admin, http.MethodGet, "/members?keyword=%25", nil) // literal %, not wildcard
	if n := len(listItems(t, ParseV2Response(t, w))); n != 0 {
		t.Fatalf("wildcard keyword matched %d", n)
	}
	w = e.do(e.admin, http.MethodGet, "/members?page=2&page_size=2", nil)
	body := ParseV2Response(t, w)
	if n := len(listItems(t, body)); n != 1 || body["data"].(map[string]interface{})["total"].(float64) != 3 {
		t.Fatalf("page 2: %s", w.Body.String())
	}
	AssertV2Status(t, e.do(e.admin, http.MethodGet, "/members?role=root", nil), http.StatusBadRequest)
}

func TestListTenantMembersV2_NonAdminForbidden(t *testing.T) {
	e := newEntEnv(t, "roster-c")
	for _, u := range []*repo.User{e.member, e.lead} {
		AssertV2Status(t, e.do(u, http.MethodGet, "/members", nil), http.StatusForbidden)
	}
}

func TestListProjectsV2_DeptLeadSeesOnlyOwnIncludingZeroUsage(t *testing.T) {
	e := newEntEnv(t, "roster-d")
	mine := e.project(t, "Mine", "")
	e.project(t, "Other", "")
	if err := repo.AddProjectMember(e.tenant, mine.Id, e.lead.Id); err != nil {
		t.Fatal(err)
	}
	w := e.do(e.lead, http.MethodGet, "/projects", nil)
	AssertV2Status(t, w, http.StatusOK)
	items := listItems(t, ParseV2Response(t, w))
	if len(items) != 1 || items[0].(map[string]interface{})["name"] != "Mine" {
		t.Fatalf("lead list = %s", w.Body.String())
	}
	// Admin and plain member are unchanged: all projects.
	for _, u := range []*repo.User{e.admin, e.member} {
		if n := len(listItems(t, ParseV2Response(t, e.do(u, http.MethodGet, "/projects", nil)))); n != 2 {
			t.Fatalf("user %d sees %d projects, want 2", u.Id, n)
		}
	}
}

func TestListProjectsV2_DeptLeadWithNoProjectsGetsEmpty(t *testing.T) {
	e := newEntEnv(t, "roster-e")
	e.project(t, "Other", "")
	w := e.do(e.lead, http.MethodGet, "/projects", nil)
	if n := len(listItems(t, ParseV2Response(t, w))); n != 0 {
		t.Fatalf("lead without projects sees %d", n)
	}
}

func TestListTokensV2_ScopeTenant(t *testing.T) {
	e := newEntEnv(t, "roster-f")
	mk := func(u *repo.User, tenant, name, ref string) {
		tk := &repo.Token{UserId: u.Id, TenantId: tenant, Name: name, Key: name + "-0123456789012345678901234567890123456789", EmployeeRef: ref, LogRetention: "none", Status: 1}
		if err := e.db.Create(tk).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk(e.admin, e.tenant, "a1", "emp-1")
	mk(e.member, e.tenant, "m1", "")
	mk(e.outsider, "other-tenant", "x1", "")

	w := e.do(e.admin, http.MethodGet, "/tokens?scope=tenant", nil)
	AssertV2Status(t, w, http.StatusOK)
	body := ParseV2Response(t, w)
	items := listItems(t, body)
	if len(items) != 2 || body["data"].(map[string]interface{})["total"].(float64) != 2 {
		t.Fatalf("tenant tokens = %s", w.Body.String())
	}
	first := items[0].(map[string]interface{})
	if first["owner_name"] != "ent-member" || first["content_retention"] != "none" {
		t.Fatalf("owner/retention: %v", first)
	}
	if k, _ := first["key"].(string); len(k) > 0 && k == "m1-0123456789012345678901234567890123456789" {
		t.Fatal("raw key leaked")
	}
	w = e.do(e.admin, http.MethodGet, "/tokens?scope=tenant&keyword=emp-1", nil)
	if n := len(listItems(t, ParseV2Response(t, w))); n != 1 {
		t.Fatalf("keyword -> %d", n)
	}
	w = e.do(e.admin, http.MethodGet, "/tokens?scope=tenant&user_id="+strconv.Itoa(e.member.Id), nil)
	if n := len(listItems(t, ParseV2Response(t, w))); n != 1 {
		t.Fatalf("user filter -> %d", n)
	}
	// Filtering by a foreign user id yields nothing, never the foreign token.
	w = e.do(e.admin, http.MethodGet, "/tokens?scope=tenant&user_id="+strconv.Itoa(e.outsider.Id), nil)
	if n := len(listItems(t, ParseV2Response(t, w))); n != 0 {
		t.Fatalf("cross-tenant user filter leaked %d", n)
	}
}

func TestListTokensV2_ScopeTenantForbiddenForNonAdmin(t *testing.T) {
	e := newEntEnv(t, "roster-g")
	for _, u := range []*repo.User{e.member, e.lead} {
		AssertV2Status(t, e.do(u, http.MethodGet, "/tokens?scope=tenant", nil), http.StatusForbidden)
	}
	AssertV2Status(t, e.do(e.admin, http.MethodGet, "/tokens?scope=all", nil), http.StatusBadRequest)
	// Default (no scope) still returns only the caller's own tokens.
	AssertV2Status(t, e.do(e.member, http.MethodGet, "/tokens", nil), http.StatusOK)
}
