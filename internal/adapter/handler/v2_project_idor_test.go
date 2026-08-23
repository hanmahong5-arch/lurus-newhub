package handler

// ============================================================================
// Cost-attribution project endpoints (migration 029) — cross-tenant IDOR and
// authorization regression tests.
//
// projects.id is a bare BIGSERIAL with no foreign key and no tenant prefix, so
// ids are guessable by increment. Isolation rests entirely on every
// repo/project.go function taking tenantID as a mandatory positional argument.
// These tests pin that: another tenant's id must be indistinguishable from a
// nonexistent one (404, not 403 — a 403 would confirm the id exists).
//
// Names are the sensitive payload here. "Project Zeus" leaks a customer's
// internal roadmap; the assertions check the response BODY, not just the code.
//
// Test names referenced by router/v2_completeness_test.go's swept map:
//   TestGetProjectV2_CrossTenantNotFound
//   TestUpdateProjectV2_CrossTenantNotFound
//   TestDeleteProjectV2_CrossTenantNotFound
// ============================================================================

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

const victimProjectName = "Project Zeus CONFIDENTIAL"

// seedForeignProject creates a project owned by a DIFFERENT tenant than the
// test context's — the "B" side of an A-admin-attacks-B-project scenario.
func seedForeignProject(t *testing.T, ctx *V2TestContext) *entity.Project {
	t.Helper()
	victim := &entity.Project{
		TenantId:    "other-tenant-xyz",
		Name:        victimProjectName,
		Description: "another customer's internal cost centre",
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("failed to seed foreign project: %v", err)
	}
	return victim
}

func seedOwnProject(t *testing.T, ctx *V2TestContext, name string) *entity.Project {
	t.Helper()
	p := &entity.Project{TenantId: ctx.TenantID, Name: name}
	if err := ctx.DB.Create(p).Error; err != nil {
		t.Fatalf("failed to seed own project: %v", err)
	}
	return p
}

func TestGetProjectV2_CrossTenantNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedForeignProject(t, ctx)

	path := fmt.Sprintf("/api/v2/test-tenant/projects/%d", victim.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusNotFound)
	if strings.Contains(w.Body.String(), victimProjectName) {
		t.Errorf("another tenant's project NAME leaked in the response body: %s", w.Body.String())
	}
}

func TestUpdateProjectV2_CrossTenantNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedForeignProject(t, ctx)

	path := fmt.Sprintf("/api/v2/test-tenant/projects/%d", victim.Id)
	body := map[string]interface{}{"name": "Hijacked", "description": "owned"}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusNotFound)

	var after entity.Project
	if err := ctx.DB.Where("id = ?", victim.Id).Take(&after).Error; err != nil {
		t.Fatalf("re-read victim project: %v", err)
	}
	if after.Name != victimProjectName {
		t.Errorf("another tenant's project was renamed to %q — the tenant clause is missing", after.Name)
	}
}

func TestDeleteProjectV2_CrossTenantNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedForeignProject(t, ctx)

	path := fmt.Sprintf("/api/v2/test-tenant/projects/%d", victim.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusNotFound)

	var count int64
	if err := ctx.DB.Model(&entity.Project{}).Where("id = ?", victim.Id).Count(&count).Error; err != nil {
		t.Fatalf("count victim project: %v", err)
	}
	if count != 1 {
		t.Error("another tenant's project was soft-deleted through this endpoint")
	}
}

// TestListProjectsV2_ExcludesOtherTenants: the listing is tenant-scoped even
// though it takes no id at all.
func TestListProjectsV2_ExcludesOtherTenants(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedForeignProject(t, ctx)
	seedOwnProject(t, ctx, "Marketing")

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/projects", nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	body := w.Body.String()
	if !strings.Contains(body, "Marketing") {
		t.Errorf("own project missing from listing: %s", body)
	}
	if strings.Contains(body, victimProjectName) {
		t.Errorf("another tenant's project leaked into the listing: %s", body)
	}
}

// TestCreateTokenV2_ForeignProjectRejected is the important one for the write
// path: project_id arrives as a client-supplied integer with no foreign key
// behind it, so without validation a member could stamp their spend onto
// another tenant's project id and that tenant's report would silently gain
// rows it cannot explain.
func TestCreateTokenV2_ForeignProjectRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedForeignProject(t, ctx)

	body := map[string]interface{}{
		"name":       "attacker-key",
		"project_id": victim.Id,
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
}

func TestCreateTokenV2_OwnProjectAccepted(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	p := seedOwnProject(t, ctx, "Marketing")

	body := map[string]interface{}{
		"name":       "tagged-key",
		"project_id": p.Id,
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusCreated)

	// The tag must actually be on the row — that column is what the relay
	// reads and what every future log row copies.
	var tok struct{ ProjectId int }
	if err := ctx.DB.Table("tokens").Select("project_id").
		Where("name = ?", "tagged-key").Scan(&tok).Error; err != nil {
		t.Fatalf("re-read created token: %v", err)
	}
	if tok.ProjectId != p.Id {
		t.Errorf("created token project_id = %d, want %d", tok.ProjectId, p.Id)
	}
}

func TestUpdateTokenV2_ForeignProjectRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedForeignProject(t, ctx)
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "my-key")

	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", tok.Id)
	body := map[string]interface{}{"project_id": victim.Id}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)

	var after struct{ ProjectId int }
	if err := ctx.DB.Table("tokens").Select("project_id").Where("id = ?", tok.Id).Scan(&after).Error; err != nil {
		t.Fatalf("re-read token: %v", err)
	}
	if after.ProjectId != 0 {
		t.Errorf("token was tagged with another tenant's project (project_id = %d)", after.ProjectId)
	}
}

// TestUpdateTokenV2_ProjectUnassignPersists: project_id is a *int on the update
// request precisely so 0 can mean "unassign" rather than "field absent".
func TestUpdateTokenV2_ProjectUnassignPersists(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	p := seedOwnProject(t, ctx, "Marketing")
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "my-key")
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", tok.Id)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path,
		map[string]interface{}{"project_id": p.Id}, nil)
	AssertV2Status(t, w, http.StatusOK)

	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path,
		map[string]interface{}{"project_id": 0}, nil)
	AssertV2Status(t, w, http.StatusOK)

	var after struct{ ProjectId int }
	if err := ctx.DB.Table("tokens").Select("project_id").Where("id = ?", tok.Id).Scan(&after).Error; err != nil {
		t.Fatalf("re-read token: %v", err)
	}
	if after.ProjectId != 0 {
		t.Errorf("unassign did not persist: project_id = %d, want 0", after.ProjectId)
	}
}

// TestUpdateTokenV2_OmittedProjectLeavesTagAlone: a client that does not know
// about projects (an older console build) must not silently strip attribution
// off every token it saves.
func TestUpdateTokenV2_OmittedProjectLeavesTagAlone(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	p := seedOwnProject(t, ctx, "Marketing")
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "my-key")
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", tok.Id)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path,
		map[string]interface{}{"project_id": p.Id}, nil)
	AssertV2Status(t, w, http.StatusOK)

	// Same shape an attribution-unaware client sends.
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path,
		map[string]interface{}{"name": "renamed"}, nil)
	AssertV2Status(t, w, http.StatusOK)

	var after struct{ ProjectId int }
	if err := ctx.DB.Table("tokens").Select("project_id").Where("id = ?", tok.Id).Scan(&after).Error; err != nil {
		t.Fatalf("re-read token: %v", err)
	}
	if after.ProjectId != p.Id {
		t.Errorf("omitting project_id cleared the tag (project_id = %d, want %d)", after.ProjectId, p.Id)
	}
}
