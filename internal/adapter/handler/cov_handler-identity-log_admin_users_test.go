package handler

// ============================================================================
// v2_admin_users.go edge cases beyond v2_admin_users_test.go:
//   - the "cannot delete the last root user" lockout guard (the highest-value
//     untested branch: without it, an operator error could brick platform
//     admin access for everyone).
//   - ListAdminUsersV2 pagination normalization (page<1, page_size out of
//     [1,100]).
//   - UpdateAdminUserV2 / DeleteAdminUserV2 malformed-id, not-found, invalid
//     role/status/quota input.
// Reuses V2TestContext / V2RequestAsUser / AssertV2* (v2_testutil_test.go).
// ============================================================================

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TestHandlerIdentityLog_DeleteAdminUserV2_LastRootLockoutGuard proves the
// delete endpoint refuses to remove the platform's sole remaining root
// account — even when reached by a caller whose gin-context role claims
// "root" but doesn't correspond to the sole root DB row (defense-in-depth:
// the guard must hold regardless of how the root-context was established,
// not just for the ordinary self-delete case already covered by
// TestDeleteAdminUserV2_SelfGuard).
func TestHandlerIdentityLog_DeleteAdminUserV2_LastRootLockoutGuard(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Demote everyone else so ctx.RootUser is the ONLY root row in the DB.
	if err := ctx.DB.Model(&repo.User{}).Where("id <> ?", ctx.RootUser.Id).Update("role", common.RoleCommonUser).Error; err != nil {
		t.Fatalf("demote other users: %v", err)
	}
	var rootCount int64
	ctx.DB.Model(&repo.User{}).Where("role = ?", common.RoleRootUser).Count(&rootCount)
	if rootCount != 1 {
		t.Fatalf("test setup invariant broken: want exactly 1 root, got %d", rootCount)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/admin/users/%d", ctx.RootUser.Id), nil)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ctx.RootUser.Id)}}
	// A different actor id than the target, so the earlier "cannot delete
	// yourself" guard does not short-circuit before reaching the root-count check.
	c.Set("id", 999999)
	c.Set("role", common.RoleRootUser)

	DeleteAdminUserV2(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	body := r2authBody(t, w)
	if body["message"] != "Cannot delete the last root user" {
		t.Errorf("message = %v", body["message"])
	}

	var stillThere repo.User
	if err := ctx.DB.First(&stillThere, ctx.RootUser.Id).Error; err != nil {
		t.Fatalf("the sole root account must survive the refused delete: %v", err)
	}
}

// TestHandlerIdentityLog_DeleteAdminUserV2_SecondRootDeletable proves the
// converse: with TWO root accounts, deleting one succeeds and leaves exactly
// one root behind (the guard is a floor of 1, not a freeze).
func TestHandlerIdentityLog_DeleteAdminUserV2_SecondRootDeletable(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	secondRoot := &repo.User{
		Username: "second-root", DisplayName: "Second Root", Role: common.RoleRootUser,
		Status: common.UserStatusEnabled, Email: "root2@test.local", TenantId: ctx.TenantID,
	}
	if err := ctx.DB.Create(secondRoot).Error; err != nil {
		t.Fatalf("seed second root: %v", err)
	}

	path := fmt.Sprintf("/api/v2/admin/users/%d", secondRoot.Id)
	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodDelete, path, nil, []string{"root"})
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var remainingRoots int64
	ctx.DB.Model(&repo.User{}).Where("role = ?", common.RoleRootUser).Count(&remainingRoots)
	if remainingRoots != 1 {
		t.Errorf("expected exactly 1 root left, got %d", remainingRoots)
	}
}

// TestHandlerIdentityLog_DeleteAdminUserV2_NotFoundAndBadID covers the two
// input-validation branches before the delete guard: a non-numeric id and a
// numeric id that doesn't resolve to any user.
func TestHandlerIdentityLog_DeleteAdminUserV2_NotFoundAndBadID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodDelete, "/api/v2/admin/users/not-a-number", nil, []string{"root"})
	AssertV2Error(t, w, http.StatusBadRequest)

	w = V2RequestAsUser(ctx, ctx.RootUser, http.MethodDelete, "/api/v2/admin/users/8888888", nil, []string{"root"})
	AssertV2Error(t, w, http.StatusNotFound)
}

// TestHandlerIdentityLog_UpdateAdminUserV2_ValidationGaps covers the
// bad-id / not-found / malformed-body / invalid-role / invalid-status /
// negative-quota branches TestUpdateAdminUserV2_* didn't reach.
func TestHandlerIdentityLog_UpdateAdminUserV2_ValidationGaps(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	t.Run("non_numeric_id", func(t *testing.T) {
		w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut, "/api/v2/admin/users/abc", map[string]interface{}{"quota": 1}, []string{"root"})
		AssertV2Error(t, w, http.StatusBadRequest)
	})

	t.Run("not_found", func(t *testing.T) {
		w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut, "/api/v2/admin/users/8888888", map[string]interface{}{"quota": 1}, []string{"root"})
		AssertV2Error(t, w, http.StatusNotFound)
	})

	t.Run("malformed_body", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v2/admin/users/%d", ctx.NormalUser.Id), strings.NewReader("{bad-json"))
		req.Header.Set("Content-Type", "application/json")
		c.Request = req
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", ctx.NormalUser.Id)}}
		c.Set("id", ctx.RootUser.Id)
		c.Set("role", common.RoleRootUser)
		UpdateAdminUserV2(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("invalid_role_rejected", func(t *testing.T) {
		w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut,
			fmt.Sprintf("/api/v2/admin/users/%d", ctx.NormalUser.Id),
			map[string]interface{}{"role": 77}, []string{"root"})
		AssertV2Error(t, w, http.StatusBadRequest)

		var reloaded repo.User
		ctx.DB.First(&reloaded, ctx.NormalUser.Id)
		if reloaded.Role == 77 {
			t.Error("invalid role value must not be persisted")
		}
	})

	t.Run("invalid_status_rejected", func(t *testing.T) {
		w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut,
			fmt.Sprintf("/api/v2/admin/users/%d", ctx.NormalUser.Id),
			map[string]interface{}{"status": 999}, []string{"root"})
		AssertV2Error(t, w, http.StatusBadRequest)
	})

	t.Run("negative_quota_rejected", func(t *testing.T) {
		w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut,
			fmt.Sprintf("/api/v2/admin/users/%d", ctx.NormalUser.Id),
			map[string]interface{}{"quota": -1}, []string{"root"})
		AssertV2Error(t, w, http.StatusBadRequest)

		var reloaded repo.User
		before := ctx.NormalUser.Quota
		ctx.DB.First(&reloaded, ctx.NormalUser.Id)
		if reloaded.Quota != before {
			t.Errorf("quota mutated despite rejection: got %d, want unchanged %d", reloaded.Quota, before)
		}
	})

	t.Run("empty_group_keeps_existing", func(t *testing.T) {
		w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodPut,
			fmt.Sprintf("/api/v2/admin/users/%d", ctx.NormalUser.Id),
			map[string]interface{}{"group": ""}, []string{"root"})
		AssertV2Status(t, w, http.StatusOK)
		resp := AssertV2Success(t, w)
		d := resp["data"].(map[string]interface{})
		if d["group"] != ctx.NormalUser.Group {
			t.Errorf("empty group string must leave the existing group unchanged, got %v want %v", d["group"], ctx.NormalUser.Group)
		}
	})
}

// TestHandlerIdentityLog_ListAdminUsersV2_PaginationNormalization proves
// out-of-range page/page_size query params are clamped to sane defaults
// instead of producing a negative offset or an unbounded page size.
func TestHandlerIdentityLog_ListAdminUsersV2_PaginationNormalization(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	t.Run("page_below_one_clamped_to_one", func(t *testing.T) {
		w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodGet, "/api/v2/admin/users?page=0", nil, []string{"root"})
		AssertV2Status(t, w, http.StatusOK)
		resp := AssertV2Success(t, w)
		d := resp["data"].(map[string]interface{})
		if int(d["page"].(float64)) != 1 {
			t.Errorf("page = %v, want clamped to 1", d["page"])
		}
	})

	t.Run("page_size_out_of_range_clamped_to_default", func(t *testing.T) {
		w := V2RequestAsUser(ctx, ctx.RootUser, http.MethodGet, "/api/v2/admin/users?page_size=99999", nil, []string{"root"})
		AssertV2Status(t, w, http.StatusOK)
		resp := AssertV2Success(t, w)
		d := resp["data"].(map[string]interface{})
		if int(d["page_size"].(float64)) != 20 {
			t.Errorf("page_size = %v, want clamped default 20", d["page_size"])
		}
	})
}
