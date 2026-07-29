package handler

// v2_project_test.go — behaviour + authorization coverage for the
// cost-attribution project endpoints (migration 029). Cross-tenant IDOR lives
// separately in v2_project_idor_test.go.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func projectDataMap(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	var parsed struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("decode response %q: %v", body, err)
	}
	return parsed.Data
}

func TestProjectV2_CRUDHappyPath(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/projects",
		map[string]interface{}{"name": "  Marketing  ", "description": "brand spend"}, []string{"admin"})
	AssertV2Status(t, w, http.StatusCreated)
	created := projectDataMap(t, w.Body.String())
	if created["name"] != "Marketing" {
		t.Errorf("created name = %v, want trimmed \"Marketing\"", created["name"])
	}
	id := int(created["id"].(float64))
	if id <= 0 {
		t.Fatalf("created id = %d, want positive", id)
	}

	// Read back
	path := fmt.Sprintf("/api/v2/test-tenant/projects/%d", id)
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	if got := projectDataMap(t, w.Body.String())["description"]; got != "brand spend" {
		t.Errorf("description = %v, want \"brand spend\"", got)
	}

	// Update — clearing the description must persist (zero-value trap).
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path,
		map[string]interface{}{"name": "Growth", "description": ""}, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	updated := projectDataMap(t, w.Body.String())
	if updated["name"] != "Growth" || updated["description"] != "" {
		t.Errorf("updated = %+v, want name Growth / empty description", updated)
	}

	// Delete
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, path, nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusNotFound)
}

// TestProjectV2_WritesRequireTenantAdmin_ReadsDoNot pins the deliberate
// asymmetry: an ordinary member must be able to READ the project list (the
// token page's picker needs it) but must not be able to reshape the tenant's
// cost dimensions.
func TestProjectV2_WritesRequireTenantAdmin_ReadsDoNot(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	existing := seedOwnProject(t, ctx, "Marketing")
	path := fmt.Sprintf("/api/v2/test-tenant/projects/%d", existing.Id)

	// Reads: open to a plain member.
	for _, p := range []string{"/api/v2/test-tenant/projects", path, "/api/v2/test-tenant/projects/spend"} {
		w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, p, nil, nil)
		AssertV2Status(t, w, http.StatusOK)
	}

	// Writes: admin only.
	writes := []struct {
		method string
		path   string
		body   interface{}
	}{
		{http.MethodPost, "/api/v2/test-tenant/projects", map[string]interface{}{"name": "Sneaky"}},
		{http.MethodPut, path, map[string]interface{}{"name": "Renamed"}},
		{http.MethodDelete, path, nil},
	}
	for _, wr := range writes {
		w := V2RequestAsUser(ctx, ctx.NormalUser, wr.method, wr.path, wr.body, nil)
		AssertV2Status(t, w, http.StatusForbidden)
	}

	// The row is untouched by all three attempts.
	var after entity.Project
	if err := ctx.DB.Where("id = ?", existing.Id).Take(&after).Error; err != nil {
		t.Fatalf("re-read project: %v", err)
	}
	if after.Name != "Marketing" {
		t.Errorf("non-admin mutated the project: name = %q", after.Name)
	}
	var count int64
	ctx.DB.Model(&entity.Project{}).Where("tenant_id = ?", ctx.TenantID).Count(&count)
	if count != 1 {
		t.Errorf("project count = %d, want 1 (non-admin create must not have landed)", count)
	}
}

func TestProjectV2_ValidationErrors(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	existing := seedOwnProject(t, ctx, "Marketing")

	cases := []struct {
		name   string
		method string
		path   string
		body   interface{}
		want   int
	}{
		{"missing name", http.MethodPost, "/api/v2/test-tenant/projects",
			map[string]interface{}{"description": "no name"}, http.StatusBadRequest},
		{"blank name", http.MethodPost, "/api/v2/test-tenant/projects",
			map[string]interface{}{"name": "   "}, http.StatusBadRequest},
		{"name too long", http.MethodPost, "/api/v2/test-tenant/projects",
			map[string]interface{}{"name": strings.Repeat("x", maxProjectNameLen+1)}, http.StatusBadRequest},
		{"description too long", http.MethodPost, "/api/v2/test-tenant/projects",
			map[string]interface{}{"name": "ok", "description": strings.Repeat("x", maxProjectDescriptionLen+1)}, http.StatusBadRequest},
		{"duplicate name", http.MethodPost, "/api/v2/test-tenant/projects",
			map[string]interface{}{"name": "Marketing"}, http.StatusConflict},
		{"non-numeric id", http.MethodGet, "/api/v2/test-tenant/projects/abc", nil, http.StatusBadRequest},
		{"non-numeric id on update", http.MethodPut, "/api/v2/test-tenant/projects/abc",
			map[string]interface{}{"name": "x"}, http.StatusBadRequest},
		{"non-numeric id on delete", http.MethodDelete, "/api/v2/test-tenant/projects/abc", nil, http.StatusBadRequest},
		{"unknown id", http.MethodGet, "/api/v2/test-tenant/projects/999999", nil, http.StatusNotFound},
		{"update unknown id", http.MethodPut, "/api/v2/test-tenant/projects/999999",
			map[string]interface{}{"name": "x"}, http.StatusNotFound},
		{"delete unknown id", http.MethodDelete, "/api/v2/test-tenant/projects/999999", nil, http.StatusNotFound},
		{"rename onto an existing name", http.MethodPut,
			fmt.Sprintf("/api/v2/test-tenant/projects/%d", existing.Id+1), nil, http.StatusBadRequest},
	}

	// The last case needs a second project to rename; seed and fix its path.
	other := seedOwnProject(t, ctx, "Research")
	cases[len(cases)-1].path = fmt.Sprintf("/api/v2/test-tenant/projects/%d", other.Id)
	cases[len(cases)-1].body = map[string]interface{}{"name": "Marketing"}
	cases[len(cases)-1].want = http.StatusConflict

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := V2RequestAsUser(ctx, ctx.AdminUser, tc.method, tc.path, tc.body, []string{"admin"})
			AssertV2Status(t, w, tc.want)
		})
	}
}

// TestProjectV2_DeleteDetachesTokens: after deleting a project no token may
// keep tagging fresh spend onto it.
func TestProjectV2_DeleteDetachesTokens(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	p := seedOwnProject(t, ctx, "Marketing")
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "tagged")
	if err := ctx.DB.Model(&repo.Token{}).Where("id = ?", tok.Id).
		Update("project_id", p.Id).Error; err != nil {
		t.Fatalf("tag token: %v", err)
	}

	path := fmt.Sprintf("/api/v2/test-tenant/projects/%d", p.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, path, nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)

	var after struct{ ProjectId int }
	if err := ctx.DB.Table("tokens").Select("project_id").Where("id = ?", tok.Id).Scan(&after).Error; err != nil {
		t.Fatalf("re-read token: %v", err)
	}
	if after.ProjectId != 0 {
		t.Errorf("token still tagged with the deleted project (project_id = %d)", after.ProjectId)
	}
}

// seedProjectSpendLog writes one consume row directly, bypassing the relay.
func seedProjectSpendLog(t *testing.T, ctx *V2TestContext, tenantID string, projectID, quota int) {
	t.Helper()
	row := &repo.Log{
		UserId:    ctx.NormalUser.Id,
		TenantId:  tenantID,
		Type:      repo.LogTypeConsume,
		CreatedAt: common.GetTimestamp(),
		ModelName: "gpt-4",
		Quota:     quota,
		ProjectId: projectID,
	}
	if err := ctx.DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

// TestGetProjectSpendV2_SumsToTotalAndIsolatesTenants is the report's load-
// bearing invariant. If the per-project figures do not add up to the tenant
// total, finance stops trusting the page on day one — so the unassigned bucket
// (project_id 0) has to be a first-class row, and a soft-deleted project's
// spend has to stay in the report under its old name.
func TestGetProjectSpendV2_SumsToTotalAndIsolatesTenants(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	marketing := seedOwnProject(t, ctx, "Marketing")
	research := seedOwnProject(t, ctx, "Research")
	foreign := seedForeignProject(t, ctx)

	seedProjectSpendLog(t, ctx, ctx.TenantID, marketing.Id, 100)
	seedProjectSpendLog(t, ctx, ctx.TenantID, marketing.Id, 50)
	seedProjectSpendLog(t, ctx, ctx.TenantID, research.Id, 30)
	seedProjectSpendLog(t, ctx, ctx.TenantID, 0, 7) // unassigned
	// Another tenant's spend, including a row that reuses one of OUR project
	// ids — no foreign key exists, so this collision is realistic.
	seedProjectSpendLog(t, ctx, "other-tenant-xyz", foreign.Id, 9999)
	seedProjectSpendLog(t, ctx, "other-tenant-xyz", marketing.Id, 8888)

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/projects/spend", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)

	var parsed struct {
		Data struct {
			Items []struct {
				ProjectId  int    `json:"project_id"`
				Name       string `json:"name"`
				Unassigned bool   `json:"unassigned"`
				TotalQuota int64  `json:"total_quota"`
			} `json:"items"`
			TotalQuota int64 `json:"total_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode spend response: %v (body %s)", err, w.Body.String())
	}

	byID := map[int]int64{}
	var sum int64
	sawUnassigned := false
	for _, it := range parsed.Data.Items {
		byID[it.ProjectId] = it.TotalQuota
		sum += it.TotalQuota
		if it.ProjectId == 0 {
			sawUnassigned = true
			if !it.Unassigned {
				t.Error("project_id 0 row is not flagged unassigned")
			}
		}
	}

	// Isolation: only this tenant's 187 (100+50+30+7) may appear.
	if sum != 187 {
		t.Errorf("sum of rows = %d, want 187 — another tenant's spend leaked in", sum)
	}
	if parsed.Data.TotalQuota != sum {
		t.Errorf("reported total_quota %d != sum of rows %d — the invariant the page is built on",
			parsed.Data.TotalQuota, sum)
	}
	if byID[marketing.Id] != 150 {
		t.Errorf("Marketing spend = %d, want 150 (the other tenant's row on the same id must not fold in)",
			byID[marketing.Id])
	}
	if !sawUnassigned {
		t.Error("no unassigned row emitted — per-project figures would not sum to the tenant total")
	}

	// A retired project keeps its spend AND its name.
	delPath := fmt.Sprintf("/api/v2/test-tenant/projects/%d", research.Id)
	AssertV2Status(t, V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, delPath, nil, []string{"admin"}), http.StatusOK)

	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/projects/spend", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode spend response after delete: %v", err)
	}
	var afterSum int64
	foundRetired := false
	for _, it := range parsed.Data.Items {
		afterSum += it.TotalQuota
		if it.ProjectId == research.Id {
			foundRetired = true
			if it.Name != "Research" {
				t.Errorf("retired project name = %q, want Research (Unscoped resolution)", it.Name)
			}
		}
	}
	if afterSum != 187 {
		t.Errorf("sum after deleting a project = %d, want 187 — deleting a project must not drop its history", afterSum)
	}
	if !foundRetired {
		t.Error("retired project's spend disappeared from the report")
	}
}

func TestGetProjectSpendV2_EmptyTenantReturnsEmptyList(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/projects/spend", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	body := w.Body.String()
	if !strings.Contains(body, `"items":[]`) {
		t.Errorf("empty tenant spend = %s, want an empty items array (not null)", body)
	}
}
