package handler

// ============================================================================
// Business scope: ManageMultiKeys (internal/adapter/handler/channel.go).
// Multi-key channel key-lifecycle management: enable/disable/delete individual
// keys, bulk enable/disable, and prune auto-disabled keys, plus the paginated
// key-status listing an operator uses to audit a channel's key pool. This is
// on the money path indirectly: a channel with all keys wrongly disabled (or
// wrongly enabled) changes which upstream key gets billed for relay traffic.
//
// Edge cases covered per the acceptance brief:
//   - cross-tenant IDOR (enforceTenantScope) before any key mutation
//   - not-multi-key channel rejected
//   - key index out of range (negative, == size, > size)
//   - deleting the last remaining key is refused
//   - bulk disable/enable no-ops report failure instead of silently succeeding
//   - get_key_status pagination edge cases: page=0, page<0-like default,
//     page beyond total pages clamps, pageSize<=0 defaults, status filter
//   - reindexing after delete_key / delete_disabled_keys preserves the
//     surviving keys' disabled state under their NEW index
// ============================================================================

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// v1CtxJSON builds a gin test context carrying the v1 admin session identity
// (id/tenant_id) that ManageMultiKeys reads via enforceTenantScope, with a
// real JSON request body so c.ShouldBindJSON works. Local to this file (the
// package's v1Ctx also forces "role", which ManageMultiKeys doesn't gate on
// and which some of our IDOR cases need to set explicitly to Root).
func v1CtxJSON(method, target string, body interface{}, tenant string, id int) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var req *http.Request
	if raw, ok := body.(string); ok {
		req = httptest.NewRequest(method, target, bytes.NewReader([]byte(raw)))
	} else {
		data, _ := json.Marshal(body)
		req = httptest.NewRequest(method, target, bytes.NewReader(data))
	}
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set("tenant_id", tenant)
	c.Set("id", id)
	return c, w
}

func handler_channel_seedMultiKeyChannel(t *testing.T, ctx *V2TestContext, keys []string) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name:        "multikey-chan",
		TenantId:    ctx.TenantID,
		Key:         joinLines(keys),
		Status:      common.ChannelStatusEnabled,
		Type:        1,
		Models:      "gpt-4",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
		ChannelInfo: repo.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: len(keys),
		},
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed multi-key channel: %v", err)
	}
	return ch
}

func joinLines(keys []string) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += "\n"
		}
		out += k
	}
	return out
}

func handler_channel_mkBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	return v1Body(t, w)
}

// ─── guard rails ────────────────────────────────────────────────────────────

func TestManageMultiKeys_ChannelNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{"channel_id": 999999, "action": "get_key_status"}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected success=false for missing channel, body=%s", w.Body.String())
	}
	if body["message"] != "渠道不存在" {
		t.Errorf("expected '渠道不存在' message, got %v", body["message"])
	}
}

// TestManageMultiKeys_CrossTenantIDOR proves a tenant admin cannot manage
// another tenant's channel keys by guessing the numeric channel id — the key
// pool (and hence which upstream key is billed) must stay within the caller's
// tenant boundary.
func TestManageMultiKeys_CrossTenantIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := &repo.Channel{
		Name:        "victim-mk",
		TenantId:    "other-tenant-mk",
		Key:         "k1\nk2",
		Status:      common.ChannelStatusEnabled,
		Type:        1,
		CreatedTime: common.GetTimestamp(),
		ChannelInfo: repo.ChannelInfo{IsMultiKey: true, MultiKeySize: 2},
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": victim.Id, "action": "disable_key", "key_index": 0,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d body=%s", w.Code, w.Body.String())
	}

	// Root, however, may manage any tenant's channel.
	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": victim.Id, "action": "disable_key", "key_index": 0,
	}, ctx.TenantID, ctx.RootUser.Id)
	c.Set("role", common.RoleRootUser)
	ManageMultiKeys(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != true {
		t.Fatalf("root should be able to manage cross-tenant keys, body=%s", w.Body.String())
	}
}

func TestManageMultiKeys_NotMultiKeyChannel(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := SeedV2Channel(t, ctx, "single-key-chan") // IsMultiKey defaults false

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "get_key_status",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false || body["message"] != "该渠道不是多密钥模式" {
		t.Fatalf("expected non-multi-key rejection, body=%s", w.Body.String())
	}
}

func TestManageMultiKeys_UnsupportedAction(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k1", "k2"})

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "teleport_keys",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false || body["message"] != "不支持的操作" {
		t.Fatalf("expected unsupported-action message, body=%s", w.Body.String())
	}
}

func TestManageMultiKeys_MalformedBody(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := v1CtxJSON(http.MethodPost, "/x", "{not json", ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status for malformed JSON: %d", w.Code)
	}
	body := handler_channel_mkBody(t, w)
	if body["success"] == true {
		t.Errorf("malformed body must not report success")
	}
}

// ─── disable_key / enable_key ──────────────────────────────────────────────

func TestManageMultiKeys_DisableKey_IndexBounds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1", "k2"})

	cases := []struct {
		name  string
		index interface{}
		want  string
	}{
		{"nil index", nil, "未指定要禁用的密钥索引"},
		{"negative index", -1, "密钥索引超出范围"},
		{"index == size", 3, "密钥索引超出范围"},
		{"index way beyond size", 999, "密钥索引超出范围"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]interface{}{"channel_id": ch.Id, "action": "disable_key"}
			if tc.index != nil {
				body["key_index"] = tc.index
			}
			c, w := v1CtxJSON(http.MethodPost, "/x", body, ctx.TenantID, ctx.AdminUser.Id)
			ManageMultiKeys(c)
			resp := handler_channel_mkBody(t, w)
			if resp["success"] != false || resp["message"] != tc.want {
				t.Fatalf("%s: got success=%v message=%v, want message=%q", tc.name, resp["success"], resp["message"], tc.want)
			}
		})
	}
}

// TestManageMultiKeys_DisableThenEnableRoundTrip locks in that disabling a
// valid index persists status=2 with disabled_time/reason cleared on demand,
// and enabling it again removes the disabled bookkeeping entirely rather than
// leaving stale disabled_time behind (which would misreport in the status
// listing UI).
func TestManageMultiKeys_DisableThenEnableRoundTrip(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1", "k2"})

	// disable index 1
	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "disable_key", "key_index": 1,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if resp := handler_channel_mkBody(t, w); resp["success"] != true || resp["message"] != "密钥已禁用" {
		t.Fatalf("disable_key failed: %s", w.Body.String())
	}

	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, ch.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ChannelInfo.MultiKeyStatusList[1] != 2 {
		t.Fatalf("expected status[1]=2 (disabled), got %v", reloaded.ChannelInfo.MultiKeyStatusList)
	}

	// nil index on enable → guard
	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "enable_key",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if resp := handler_channel_mkBody(t, w); resp["message"] != "未指定要启用的密钥索引" {
		t.Fatalf("expected nil-index guard, got %v", resp["message"])
	}

	// out of range on enable
	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "enable_key", "key_index": 5,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if resp := handler_channel_mkBody(t, w); resp["message"] != "密钥索引超出范围" {
		t.Fatalf("expected range guard, got %v", resp["message"])
	}

	// enable index 1 back
	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "enable_key", "key_index": 1,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if resp := handler_channel_mkBody(t, w); resp["success"] != true || resp["message"] != "密钥已启用" {
		t.Fatalf("enable_key failed: %s", w.Body.String())
	}
	if err := ctx.DB.First(&reloaded, ch.Id).Error; err != nil {
		t.Fatalf("reload2: %v", err)
	}
	if _, exists := reloaded.ChannelInfo.MultiKeyStatusList[1]; exists {
		t.Errorf("enabled key must be removed from status list entirely, got %v", reloaded.ChannelInfo.MultiKeyStatusList)
	}
}

// ─── bulk enable/disable ────────────────────────────────────────────────────

func TestManageMultiKeys_DisableAllKeys_NoneToDisable(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1"})
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{0: 2, 1: 2} // already all disabled
	if err := ch.Update(); err != nil {
		t.Fatalf("seed pre-disabled: %v", err)
	}

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "disable_all_keys",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != false || resp["message"] != "没有可禁用的密钥" {
		t.Fatalf("expected no-op failure, body=%s", w.Body.String())
	}
}

func TestManageMultiKeys_DisableAllThenEnableAll(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1", "k2"})

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "disable_all_keys",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != true || resp["message"] != "已禁用 3 个密钥" {
		t.Fatalf("expected 3 disabled, body=%s", w.Body.String())
	}

	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "enable_all_keys",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp = handler_channel_mkBody(t, w)
	if resp["success"] != true || resp["message"] != "已启用 3 个密钥" {
		t.Fatalf("expected 3 enabled, body=%s", w.Body.String())
	}

	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, ch.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.ChannelInfo.MultiKeyStatusList) != 0 {
		t.Errorf("expected empty status list after enable_all_keys, got %v", reloaded.ChannelInfo.MultiKeyStatusList)
	}
}

// ─── delete_key ─────────────────────────────────────────────────────────────

func TestManageMultiKeys_DeleteKey_CannotDeleteLast(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"only-key"})

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "delete_key", "key_index": 0,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != false || resp["message"] != "不能删除最后一个密钥" {
		t.Fatalf("expected last-key guard, body=%s", w.Body.String())
	}
}

// TestManageMultiKeys_DeleteKey_ReindexesSurvivors proves that deleting a
// middle key correctly re-indexes the remaining keys' disabled bookkeeping —
// a naive "delete and keep old indices" implementation would leave key 2's
// disabled state attached to the wrong (now-shifted) key.
func TestManageMultiKeys_DeleteKey_ReindexesSurvivors(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1", "k2"})
	// k2 (index 2) is manually disabled with a reason before the delete.
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{2: 2}
	ch.ChannelInfo.MultiKeyDisabledReason = map[int]string{2: "manual-note"}
	ch.ChannelInfo.MultiKeyDisabledTime = map[int]int64{2: 12345}
	if err := ch.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// delete index 0 (k0) → k1 becomes new index 0, k2 becomes new index 1.
	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "delete_key", "key_index": 0,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != true || resp["message"] != "密钥已删除" {
		t.Fatalf("delete_key failed: %s", w.Body.String())
	}

	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, ch.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ChannelInfo.MultiKeySize != 2 {
		t.Fatalf("expected size=2 after delete, got %d", reloaded.ChannelInfo.MultiKeySize)
	}
	if reloaded.Key != "k1\nk2" {
		t.Fatalf("expected remaining keys 'k1\\nk2', got %q", reloaded.Key)
	}
	if reloaded.ChannelInfo.MultiKeyStatusList[1] != 2 {
		t.Fatalf("expected the disabled state to move to new index 1, got %v", reloaded.ChannelInfo.MultiKeyStatusList)
	}
	if reloaded.ChannelInfo.MultiKeyDisabledReason[1] != "manual-note" {
		t.Fatalf("expected disabled reason to follow the key to its new index, got %v", reloaded.ChannelInfo.MultiKeyDisabledReason)
	}

	// deleting an out-of-range index is also guarded here.
	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "delete_key", "key_index": 99,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if resp := handler_channel_mkBody(t, w); resp["message"] != "密钥索引超出范围" {
		t.Fatalf("expected range guard on delete, got %v", resp["message"])
	}

	// nil index guard on delete
	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "delete_key",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	if resp := handler_channel_mkBody(t, w); resp["message"] != "未指定要删除的密钥索引" {
		t.Fatalf("expected nil-index guard on delete, got %v", resp["message"])
	}
}

// ─── delete_disabled_keys ───────────────────────────────────────────────────

func TestManageMultiKeys_DeleteDisabledKeys_NoneAutoDisabled(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1"})
	// one manually disabled (status 2), none auto-disabled (status 3).
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{0: 2}
	if err := ch.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "delete_disabled_keys",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != false || resp["message"] != "没有需要删除的自动禁用密钥" {
		t.Fatalf("expected no-auto-disabled message, body=%s", w.Body.String())
	}

	// The manually-disabled key must survive untouched.
	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, ch.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ChannelInfo.MultiKeySize != 2 {
		t.Errorf("manual-disabled-only prune must not remove keys, size=%d", reloaded.ChannelInfo.MultiKeySize)
	}
}

// TestManageMultiKeys_DeleteDisabledKeys_PrunesOnlyAutoDisabled proves the
// prune keeps enabled (status 1) and manually-disabled (status 2) keys, only
// removing auto-disabled (status 3) ones, and reindexes the survivors.
func TestManageMultiKeys_DeleteDisabledKeys_PrunesOnlyAutoDisabled(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, []string{"k0", "k1", "k2"})
	// k0 enabled(default), k1 auto-disabled(3), k2 manual-disabled(2)
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{1: 3, 2: 2}
	if err := ch.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "delete_disabled_keys",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != true || resp["message"] != "已删除 1 个自动禁用的密钥" {
		t.Fatalf("expected 1 pruned, body=%s", w.Body.String())
	}
	if resp["data"] != float64(1) {
		t.Errorf("expected data=1, got %v", resp["data"])
	}

	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, ch.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Key != "k0\nk2" {
		t.Fatalf("expected surviving keys 'k0\\nk2', got %q", reloaded.Key)
	}
	if reloaded.ChannelInfo.MultiKeySize != 2 {
		t.Fatalf("expected size=2, got %d", reloaded.ChannelInfo.MultiKeySize)
	}
	// k2's manual-disabled state must follow it to new index 1.
	if reloaded.ChannelInfo.MultiKeyStatusList[1] != 2 {
		t.Fatalf("expected manual-disabled state at new index 1, got %v", reloaded.ChannelInfo.MultiKeyStatusList)
	}
	if _, stillThere := reloaded.ChannelInfo.MultiKeyStatusList[0]; stillThere {
		t.Errorf("k0 (enabled) must not gain a status entry, got %v", reloaded.ChannelInfo.MultiKeyStatusList)
	}
}

// ─── get_key_status pagination + filtering ──────────────────────────────────

func TestManageMultiKeys_GetKeyStatus_PaginationAndFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	keys := []string{"key-aaaaaaaaaaaaaaaaaaaaaaaa", "key-bbbbbbbbbbbbbbbbbbbbbbbb", "key-cccccccccccccccccccccccc"}
	ch := handler_channel_seedMultiKeyChannel(t, ctx, keys)
	// index0 enabled(default), index1 manual disabled, index2 auto disabled.
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{1: 2, 2: 3}
	if err := ch.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// page=0 and page_size=0 → defaults (page=1, page_size=50) and full listing.
	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "get_key_status", "page": 0, "page_size": 0,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp := handler_channel_mkBody(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body=%s", w.Body.String())
	}
	if int(data["page"].(float64)) != 1 || int(data["page_size"].(float64)) != 50 {
		t.Errorf("expected default page=1/page_size=50, got %v/%v", data["page"], data["page_size"])
	}
	if int(data["total"].(float64)) != 3 {
		t.Errorf("expected total=3 unfiltered, got %v", data["total"])
	}
	if int(data["enabled_count"].(float64)) != 1 || int(data["manual_disabled_count"].(float64)) != 1 || int(data["auto_disabled_count"].(float64)) != 1 {
		t.Errorf("expected 1/1/1 stat split, got enabled=%v manual=%v auto=%v",
			data["enabled_count"], data["manual_disabled_count"], data["auto_disabled_count"])
	}
	keysList, _ := data["keys"].([]interface{})
	if len(keysList) != 3 {
		t.Fatalf("expected 3 keys returned, got %d", len(keysList))
	}
	firstKey := keysList[0].(map[string]interface{})
	preview, _ := firstKey["key_preview"].(string)
	if preview != "key-aaaaaa..." {
		t.Errorf("expected truncated preview 'key-aaaaaa...', got %q", preview)
	}

	// status filter narrows to only auto-disabled (status=3) → 1 result.
	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "get_key_status", "status": 3,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp = handler_channel_mkBody(t, w)
	data = resp["data"].(map[string]interface{})
	if int(data["total"].(float64)) != 1 {
		t.Fatalf("expected filtered total=1, got %v", data["total"])
	}
	// overall stats remain unfiltered even when the page is filtered.
	if int(data["enabled_count"].(float64)) != 1 {
		t.Errorf("filter must not change overall stats, enabled_count=%v", data["enabled_count"])
	}

	// page beyond total pages clamps to the last page rather than erroring or
	// returning an out-of-range slice.
	c, w = v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "get_key_status", "page": 999, "page_size": 1,
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp = handler_channel_mkBody(t, w)
	data = resp["data"].(map[string]interface{})
	if int(data["page"].(float64)) != 3 {
		t.Fatalf("expected page clamped to total_pages=3, got %v", data["page"])
	}
	keysList, _ = data["keys"].([]interface{})
	if len(keysList) != 1 {
		t.Errorf("expected 1 key on the clamped last page, got %d", len(keysList))
	}
}

// TestManageMultiKeys_GetKeyStatus_EmptyKeyPool guards the totalPages==0 →
// forced to 1 branch (a multi-key channel whose Key field somehow ended up
// empty must not divide-by-zero-shaped its way into a negative/zero page).
func TestManageMultiKeys_GetKeyStatus_EmptyKeyPool(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedMultiKeyChannel(t, ctx, nil)
	ch.Key = ""
	ch.ChannelInfo.MultiKeySize = 0
	if err := ch.Update(); err != nil {
		t.Fatalf("seed empty-key channel: %v", err)
	}

	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"channel_id": ch.Id, "action": "get_key_status",
	}, ctx.TenantID, ctx.AdminUser.Id)
	ManageMultiKeys(c)
	resp := handler_channel_mkBody(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object even for empty key pool, body=%s", w.Body.String())
	}
	if int(data["total_pages"].(float64)) != 1 {
		t.Errorf("expected total_pages forced to 1 for empty pool, got %v", data["total_pages"])
	}
	if int(data["total"].(float64)) != 0 {
		t.Errorf("expected total=0, got %v", data["total"])
	}
}
