package handler

// v2_project_undo_test.go — reversibility and repeat-safety for the project
// endpoints (migration 029).
//
// Two properties are pinned here:
//
//  1. UNDO. Deleting a project detaches its tokens, and nothing else in the
//     schema records which ones. DELETE therefore hands the ids back and
//     POST .../restore puts them where they were.
//  2. REPEAT-SAFETY. Every mutating route survives being called twice — a lost
//     response, an impatient double-click, a retrying proxy — without turning
//     into an error or a duplicate.
//
// Named by router/v2_completeness_test.go: TestRestoreProjectV2_CrossTenantNotFound.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func projectPath(id int) string {
	return fmt.Sprintf("/api/v2/test-tenant/projects/%d", id)
}

func restorePath(id int) string {
	return fmt.Sprintf("/api/v2/test-tenant/projects/%d/restore", id)
}

// deletedTokenIDs pulls the undo payload out of a DELETE response.
func deletedTokenIDs(t *testing.T, body []byte) []int {
	t.Helper()
	var parsed struct {
		Data struct {
			DetachedTokenIDs []int `json:"detached_token_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode delete response %s: %v", body, err)
	}
	return parsed.Data.DetachedTokenIDs
}

func tokenProjectID(t *testing.T, ctx *V2TestContext, tokenID int) int {
	t.Helper()
	var row struct{ ProjectId int }
	if err := ctx.DB.Table("tokens").Select("project_id").
		Where("id = ?", tokenID).Scan(&row).Error; err != nil {
		t.Fatalf("read token %d: %v", tokenID, err)
	}
	return row.ProjectId
}

func TestRestoreProjectV2_CrossTenantNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedForeignProject(t, ctx)
	// Retire it directly so the only thing standing between the caller and
	// another tenant's row is the tenant clause, not its live/deleted state.
	if err := ctx.DB.Delete(&entity.Project{}, victim.Id).Error; err != nil {
		t.Fatalf("retire foreign project: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, restorePath(victim.Id), nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusNotFound)

	var after entity.Project
	if err := ctx.DB.Unscoped().Where("id = ?", victim.Id).Take(&after).Error; err != nil {
		t.Fatalf("re-read foreign project: %v", err)
	}
	if !after.DeletedAt.Valid {
		t.Error("another tenant's project was un-retired through this endpoint")
	}
}

// TestProjectV2_DeleteThenRestoreRoundTrip is the whole point of the undo: the
// tenant ends up exactly where it started, tokens included.
func TestProjectV2_DeleteThenRestoreRoundTrip(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	p := seedOwnProject(t, ctx, "Marketing")
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "tagged")
	if err := ctx.DB.Table("tokens").Where("id = ?", tok.Id).
		Update("project_id", p.Id).Error; err != nil {
		t.Fatalf("tag token: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, projectPath(p.Id), nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	detached := deletedTokenIDs(t, w.Body.Bytes())
	if len(detached) != 1 || detached[0] != tok.Id {
		t.Fatalf("DELETE returned detached ids %v, want [%d] — without them the undo is impossible", detached, tok.Id)
	}
	if got := tokenProjectID(t, ctx, tok.Id); got != 0 {
		t.Fatalf("token still attached after delete: project_id = %d", got)
	}

	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, restorePath(p.Id),
		map[string]interface{}{"reattach_token_ids": detached}, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)

	if _, err := ctx.DB.DB(); err != nil {
		t.Fatalf("db handle: %v", err)
	}
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, projectPath(p.Id), nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	if got := tokenProjectID(t, ctx, tok.Id); got != p.Id {
		t.Errorf("token project_id = %d after undo, want %d", got, p.Id)
	}
}

// TestProjectV2_MutationsSurviveBeingClickedTwice: every write route is safe to
// replay. Nothing here may produce a duplicate row or a spurious error.
func TestProjectV2_MutationsSurviveBeingClickedTwice(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	p := seedOwnProject(t, ctx, "Marketing")

	t.Run("update twice", func(t *testing.T) {
		body := map[string]interface{}{"name": "Growth", "description": "same"}
		for i := 0; i < 2; i++ {
			w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, projectPath(p.Id), body, []string{"admin"})
			AssertV2Status(t, w, http.StatusOK)
		}
	})

	t.Run("delete twice", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, projectPath(p.Id), nil, []string{"admin"})
			// The second call asks for a state that already holds. Answering
			// 404 would make an impatient double-click look like a failure.
			AssertV2Status(t, w, http.StatusOK)
			if ids := deletedTokenIDs(t, w.Body.Bytes()); i == 1 && len(ids) != 0 {
				t.Errorf("second delete reported detached ids %v; want none", ids)
			}
		}
	})

	t.Run("restore twice", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, restorePath(p.Id), nil, []string{"admin"})
			AssertV2Status(t, w, http.StatusOK)
		}
		var live int64
		ctx.DB.Model(&entity.Project{}).Where("tenant_id = ?", ctx.TenantID).Count(&live)
		if live != 1 {
			t.Errorf("live projects after repeated restores = %d, want 1", live)
		}
	})

	t.Run("create twice is refused, not duplicated", func(t *testing.T) {
		// Unlike the others, a repeated CREATE is genuinely ambiguous — it may
		// be a double-click or a second project the user really wants. The
		// per-tenant name uniqueness makes the choice for us: 409, and exactly
		// one row.
		body := map[string]interface{}{"name": "Research"}
		first := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/projects", body, []string{"admin"})
		AssertV2Status(t, first, http.StatusCreated)
		second := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/projects", body, []string{"admin"})
		AssertV2Status(t, second, http.StatusConflict)

		var n int64
		ctx.DB.Model(&entity.Project{}).Where("tenant_id = ? AND name = ?", ctx.TenantID, "Research").Count(&n)
		if n != 1 {
			t.Errorf("rows named Research = %d, want exactly 1", n)
		}
	})
}

// TestRestoreProjectV2_WithoutBody: the undo must work when the caller has
// nothing to re-attach (or sends no payload at all). A missing body is not an
// error — restoring the project alone is a complete request.
func TestRestoreProjectV2_WithoutBody(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	p := seedOwnProject(t, ctx, "Marketing")
	AssertV2Status(t, V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, projectPath(p.Id), nil, []string{"admin"}), http.StatusOK)

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, restorePath(p.Id), nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)

	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, projectPath(p.Id), nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
}

// TestRestoreProjectV2_NameTakenReturnsConflict: the partial unique index is
// what let a new project claim the retired name, so the undo has to fail with
// something the user can act on rather than a raw constraint error.
func TestRestoreProjectV2_NameTakenReturnsConflict(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	old := seedOwnProject(t, ctx, "Marketing")
	AssertV2Status(t, V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, projectPath(old.Id), nil, []string{"admin"}), http.StatusOK)
	seedOwnProject(t, ctx, "Marketing") // the name is free again, and taken

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, restorePath(old.Id), nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusConflict)
}

func TestRestoreProjectV2_RequiresTenantAdmin(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	p := seedOwnProject(t, ctx, "Marketing")
	AssertV2Status(t, V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, projectPath(p.Id), nil, []string{"admin"}), http.StatusOK)

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, restorePath(p.Id), nil, nil)
	AssertV2Status(t, w, http.StatusForbidden)

	var after entity.Project
	if err := ctx.DB.Unscoped().Where("id = ?", p.Id).Take(&after).Error; err != nil {
		t.Fatalf("re-read project: %v", err)
	}
	if !after.DeletedAt.Valid {
		t.Error("a non-admin restored a project")
	}
}

func TestRestoreProjectV2_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost,
		"/api/v2/test-tenant/projects/abc/restore", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusBadRequest)

	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, restorePath(999999), nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusNotFound)
}

// TestListProjectsV2_IncludeDeleted: the console cannot offer an undo for a
// project it cannot see, but the default listing stays live-only so existing
// clients do not suddenly render retired rows.
func TestListProjectsV2_IncludeDeleted(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	live := seedOwnProject(t, ctx, "Marketing")
	gone := seedOwnProject(t, ctx, "Research")
	AssertV2Status(t, V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, projectPath(gone.Id), nil, []string{"admin"}), http.StatusOK)
	seedForeignProject(t, ctx)

	parse := func(w interface{ Bytes() []byte }) []struct {
		Id      int    `json:"id"`
		Name    string `json:"name"`
		Deleted bool   `json:"deleted"`
	} {
		var parsed struct {
			Data struct {
				Items []struct {
					Id      int    `json:"id"`
					Name    string `json:"name"`
					Deleted bool   `json:"deleted"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Bytes(), &parsed); err != nil {
			t.Fatalf("decode listing: %v", err)
		}
		return parsed.Data.Items
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/projects", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	items := parse(w.Body)
	if len(items) != 1 || items[0].Id != live.Id {
		t.Errorf("default listing = %+v, want only the live project", items)
	}

	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet,
		"/api/v2/test-tenant/projects?include_deleted=1", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	items = parse(w.Body)
	if len(items) != 2 {
		t.Fatalf("include_deleted listing = %d items, want 2", len(items))
	}
	for _, it := range items {
		if it.Name == victimProjectName {
			t.Fatal("include_deleted leaked another tenant's project")
		}
		if it.Id == gone.Id && !it.Deleted {
			t.Error("retired project is not flagged deleted; the console cannot offer an undo")
		}
		if it.Id == live.Id && it.Deleted {
			t.Error("live project flagged deleted")
		}
	}
}
