package handler

// cov_handler-deep-c_group_test.go — business-acceptance coverage for
// group.go (GetGroups / GetUserGroups), both at 0% before this file.
//
// GetGroups is a pure read of the package-level group-ratio table.
// GetUserGroups additionally resolves the caller's own group from the DB
// (repo.GetUserGroup) and cross-references it against the visible-group and
// special-usable-group settings — both package-level globals mutated here
// via their own JSON snapshot/restore helpers (mirrors how the settings
// package itself persists them), so no cross-test pollution survives this
// file even if other _test.go files run in the same binary.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// ─── shared snapshot/restore for the two package-level group tables ───────

func handlerDeepCSnapshotGroupRatio(t *testing.T) {
	t.Helper()
	prev := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		if err := ratio_setting.UpdateGroupRatioByJSONString(prev); err != nil {
			t.Fatalf("restore group ratio: %v", err)
		}
	})
}

func handlerDeepCSnapshotUserUsableGroups(t *testing.T) {
	t.Helper()
	prev := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		if err := setting.UpdateUserUsableGroupsByJSONString(prev); err != nil {
			t.Fatalf("restore user usable groups: %v", err)
		}
	})
}

func handlerDeepCGroupRouter(userID int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/groups", GetGroups)
	r.GET("/user-groups", func(c *gin.Context) {
		c.Set("id", userID)
		GetUserGroups(c)
	})
	return r
}

func handlerDeepCDoGroup(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// ─── GetGroups ──────────────────────────────────────────────────────────

func TestGetGroups_ReturnsConfiguredGroupNames(t *testing.T) {
	handlerDeepCSnapshotGroupRatio(t)
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"handlerdeepc-grp":3}`); err != nil {
		t.Fatalf("seed group ratio: %v", err)
	}

	r := handlerDeepCGroupRouter(0)
	w := handlerDeepCDoGroup(r, "/groups")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("data is not a list: %T (%v)", resp["data"], resp["data"])
	}
	seen := make(map[string]bool, len(data))
	for _, v := range data {
		seen[v.(string)] = true
	}
	for _, want := range []string{"default", "vip", "handlerdeepc-grp"} {
		if !seen[want] {
			t.Errorf("expected group %q in response data %v", want, data)
		}
	}
	if len(data) != 3 {
		t.Errorf("expected exactly 3 groups (the seeded set), got %d: %v", len(data), data)
	}
}

func TestGetGroups_EmptyRatioTable_ReturnsEmptyList(t *testing.T) {
	handlerDeepCSnapshotGroupRatio(t)
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{}`); err != nil {
		t.Fatalf("seed empty group ratio: %v", err)
	}

	r := handlerDeepCGroupRouter(0)
	w := handlerDeepCDoGroup(r, "/groups")
	resp := handlerDeployParseBody(t, w)
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("data is not a list: %T", resp["data"])
	}
	if len(data) != 0 {
		t.Errorf("expected empty list for empty ratio table, got %v", data)
	}
}

// ─── GetUserGroups ──────────────────────────────────────────────────────

// TestGetUserGroups_DefaultGroup_MatchesVisibleRatioIntersection covers the
// dominant path: the caller's own group ("default") is both in the visible
// user-usable-groups set AND the group-ratio table, so it must be surfaced
// with a resolved ratio and description. A ratio-only group the caller
// cannot see (no entry in userUsableGroups) must be excluded.
func TestGetUserGroups_DefaultGroup_MatchesVisibleRatioIntersection(t *testing.T) {
	handlerDeepCSnapshotGroupRatio(t)
	handlerDeepCSnapshotUserUsableGroups(t)
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"hidden-from-user":5}`); err != nil {
		t.Fatalf("seed group ratio: %v", err)
	}
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组","vip":"vip分组"}`); err != nil {
		t.Fatalf("seed user usable groups: %v", err)
	}

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ctx.NormalUser.Group = "default"
	if err := ctx.DB.Save(ctx.NormalUser).Error; err != nil {
		t.Fatalf("set user group: %v", err)
	}

	r := handlerDeepCGroupRouter(ctx.NormalUser.Id)
	w := handlerDeepCDoGroup(r, "/user-groups")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T", resp["data"])
	}
	if _, present := data["hidden-from-user"]; present {
		t.Errorf("group with no userUsableGroups entry must be excluded, got %v", data)
	}
	defaultEntry, ok := data["default"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'default' entry, got %v", data)
	}
	if defaultEntry["desc"] != "默认分组" {
		t.Errorf("default desc = %v, want 默认分组", defaultEntry["desc"])
	}
	if ratio, _ := defaultEntry["ratio"].(float64); ratio != 1 {
		t.Errorf("default ratio = %v, want 1", defaultEntry["ratio"])
	}
	if _, present := data["auto"]; present {
		t.Errorf("auto entry must be absent when 'auto' is not in userUsableGroups, got %v", data)
	}
}

// TestGetUserGroups_AutoGroupVisible_SurfacesFixedAutoEntry covers the
// second guard: when the resolved usable-groups set includes "auto" (a
// user-group-specific addition via GroupSpecialUsableGroup), the handler
// must synthesize a fixed "自动" entry independent of the ratio table scan.
func TestGetUserGroups_AutoGroupVisible_SurfacesFixedAutoEntry(t *testing.T) {
	handlerDeepCSnapshotGroupRatio(t)
	handlerDeepCSnapshotUserUsableGroups(t)
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`); err != nil {
		t.Fatalf("seed group ratio: %v", err)
	}
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组"}`); err != nil {
		t.Fatalf("seed user usable groups: %v", err)
	}

	const probeGroup = "handlerdeepc-auto-probe"
	special := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	prevSpecial, hadSpecial := special.Get(probeGroup)
	special.Set(probeGroup, map[string]string{"+:auto": "自动分组(测试)"})
	t.Cleanup(func() {
		if hadSpecial {
			special.Set(probeGroup, prevSpecial)
		}
	})

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ctx.NormalUser.Group = probeGroup
	if err := ctx.DB.Save(ctx.NormalUser).Error; err != nil {
		t.Fatalf("set user group: %v", err)
	}

	r := handlerDeepCGroupRouter(ctx.NormalUser.Id)
	w := handlerDeepCDoGroup(r, "/user-groups")
	resp := handlerDeployParseBody(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T body=%s", resp["data"], w.Body.String())
	}
	autoEntry, ok := data["auto"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'auto' entry when probe group grants it, got %v", data)
	}
	if autoEntry["ratio"] != "自动" {
		t.Errorf("auto ratio = %v, want 自动 (fixed literal, not looked up)", autoEntry["ratio"])
	}
}

// TestGetUserGroups_UnknownUser_NoGroupRow_StillSucceeds covers
// repo.GetUserGroup failing (no such user id) — the handler must degrade to
// an empty-group lookup rather than 500, since userGroup empty just means
// "no special-group matches, only literally-empty-string keys apply".
func TestGetUserGroups_UnknownUser_NoGroupRow_StillSucceeds(t *testing.T) {
	handlerDeepCSnapshotGroupRatio(t)
	handlerDeepCSnapshotUserUsableGroups(t)
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`); err != nil {
		t.Fatalf("seed group ratio: %v", err)
	}
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组"}`); err != nil {
		t.Fatalf("seed user usable groups: %v", err)
	}

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := handlerDeepCGroupRouter(999999999)
	w := handlerDeepCDoGroup(r, "/user-groups")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degrade gracefully), body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true even for an unknown user id, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T", resp["data"])
	}
	// "default" is still visible: it's in userUsableGroups unconditionally
	// (not gated on a matching user group), independent of the failed lookup.
	if _, present := data["default"]; !present {
		t.Errorf("expected 'default' still visible for unresolved user, got %v", data)
	}
}
