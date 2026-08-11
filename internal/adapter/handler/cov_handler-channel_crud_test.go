package handler

// ============================================================================
// Business scope: channel CRUD edge cases not already exercised by the
// sibling R2Chan / v1 IDOR test files — batch add modes (multi_to_single /
// batch / Vertex AI array keys), multi-key append vs replace on update, list
// filtering (tag mode, type filter, status filter, id_sort) on GetAllChannels
// and SearchChannels, and EditTagChannels' param/header override validation.
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

// ─── AddChannel: multi_to_single / batch / Vertex AI array keys ───────────

// TestAddChannel_MultiToSingle_PlainKeys covers the "multi_to_single" add
// mode for a non-Vertex channel: newline-separated keys collapse into a
// SINGLE multi-key channel row, blank lines are dropped, and MultiKeySize
// reflects the cleaned count.
func TestAddChannel_MultiToSingle_PlainKeys(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{
		"mode":           "multi_to_single",
		"multi_key_mode": "polling",
		"channel": map[string]interface{}{
			"type": constant.ChannelTypeOpenAI, "name": "mts",
			"key": "k1\n\nk2\n k3 \n", "models": "gpt-4",
		},
	})
	AddChannel(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("multi_to_single add failed: %s", w.Body.String())
	}

	var saved repo.Channel
	if err := ctx.DB.Where("name = ?", "mts").First(&saved).Error; err != nil {
		t.Fatalf("load saved channel: %v", err)
	}
	if !saved.ChannelInfo.IsMultiKey {
		t.Error("expected IsMultiKey=true")
	}
	if saved.ChannelInfo.MultiKeySize != 3 {
		t.Errorf("expected 3 cleaned keys (blank line dropped), got %d", saved.ChannelInfo.MultiKeySize)
	}
	if saved.Key != "k1\nk2\nk3" {
		t.Errorf("expected trimmed/joined keys 'k1\\nk2\\nk3', got %q", saved.Key)
	}

	var count int64
	ctx.DB.Model(&repo.Channel{}).Where("name = ?", "mts").Count(&count)
	if count != 1 {
		t.Errorf("multi_to_single must create exactly ONE channel row, got %d", count)
	}
}

// TestAddChannel_Batch_SplitsIntoMultipleChannelsAndSkipsBlank covers the
// "batch" add mode: each newline-separated key becomes its OWN channel row
// (opposite of multi_to_single), blank lines are skipped, and the
// key-prefix-as-name-suffix option is honored.
func TestAddChannel_Batch_SplitsIntoMultipleChannelsAndSkipsBlank(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{
		"mode":                            "batch",
		"batch_add_set_key_prefix_2_name": true,
		"channel": map[string]interface{}{
			"type": constant.ChannelTypeOpenAI, "name": "batchchan",
			"key": "sk-aaaaaaaaaaaa\n\nsk-bbbbbbbbbbbb", "models": "gpt-4",
		},
	})
	AddChannel(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("batch add failed: %s", w.Body.String())
	}

	var channels []repo.Channel
	if err := ctx.DB.Where("name LIKE ?", "batchchan%").Order("id").Find(&channels).Error; err != nil {
		t.Fatalf("load batch channels: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels from batch (blank line skipped), got %d", len(channels))
	}
	if channels[0].Key == channels[1].Key {
		t.Error("expected distinct keys per batch-created channel")
	}
	if channels[0].Name != "batchchan sk-aaaaa" {
		t.Errorf("expected key-prefix appended to name, got %q", channels[0].Name)
	}
	if channels[0].ChannelInfo.IsMultiKey {
		t.Error("batch mode must NOT mark channels as multi-key")
	}
}

// TestAddChannel_VertexAI_ArrayKeys_MultiToSingleAndBatch covers the Vertex
// AI JSON-array key parsing branch in both add modes, plus its malformed-JSON
// error path.
func TestAddChannel_VertexAI_ArrayKeys_MultiToSingleAndBatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	vertexSettings := `{"vertex_key_type":"json"}`
	validArray := `[{"project":"p1"},{"project":"p2"}]`

	// multi_to_single: all vertex JSON keys collapse into one multi-key channel.
	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{
		"mode": "multi_to_single",
		"channel": map[string]interface{}{
			"type": constant.ChannelTypeVertexAi, "name": "vtx-mts",
			"key": validArray, "other": `{"default":"us-central1"}`, "settings": vertexSettings,
		},
	})
	AddChannel(c)
	if resp := r2chanParseBody(t, w); resp["success"] != true {
		t.Fatalf("vertex multi_to_single add failed: %s", w.Body.String())
	}
	var vtxSaved repo.Channel
	if err := ctx.DB.Where("name = ?", "vtx-mts").First(&vtxSaved).Error; err != nil {
		t.Fatalf("load vertex mts channel: %v", err)
	}
	if vtxSaved.ChannelInfo.MultiKeySize != 2 {
		t.Errorf("expected 2 vertex array keys, got %d", vtxSaved.ChannelInfo.MultiKeySize)
	}

	// batch: each vertex JSON array entry becomes its own channel.
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{
		"mode": "batch",
		"channel": map[string]interface{}{
			"type": constant.ChannelTypeVertexAi, "name": "vtx-batch",
			"key": validArray, "other": `{"default":"us-central1"}`, "settings": vertexSettings,
		},
	})
	AddChannel(c)
	if resp := r2chanParseBody(t, w); resp["success"] != true {
		t.Fatalf("vertex batch add failed: %s", w.Body.String())
	}
	var vtxBatch []repo.Channel
	ctx.DB.Where("name LIKE ?", "vtx-batch%").Find(&vtxBatch)
	if len(vtxBatch) != 2 {
		t.Fatalf("expected 2 channels from vertex batch, got %d", len(vtxBatch))
	}

	// malformed JSON array → clean failure, no channel persisted.
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{
		"mode": "batch",
		"channel": map[string]interface{}{
			"type": constant.ChannelTypeVertexAi, "name": "vtx-bad",
			"key": "{not an array", "other": `{"default":"us-central1"}`, "settings": vertexSettings,
		},
	})
	AddChannel(c)
	if resp := r2chanParseBody(t, w); resp["success"] == true {
		t.Error("expected failure for malformed vertex key array")
	}
	var badCount int64
	ctx.DB.Model(&repo.Channel{}).Where("name = ?", "vtx-bad").Count(&badCount)
	if badCount != 0 {
		t.Errorf("malformed vertex batch must not persist any channel, got %d", badCount)
	}
}

// ─── UpdateChannel: multi-key append / replace ─────────────────────────────

func handler_channel_seedUpdateMultiKeyChannel(t *testing.T, ctx *V2TestContext, key string) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name: "upd-mk", TenantId: ctx.TenantID, Key: key,
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI,
		Models: "gpt-4", Group: "default", CreatedTime: common.GetTimestamp(),
		ChannelInfo: repo.ChannelInfo{IsMultiKey: true, MultiKeySize: len(splitNonEmpty(key))},
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed update multi-key channel: %v", err)
	}
	return ch
}

func splitNonEmpty(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '\n' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// TestUpdateChannel_MultiKeyAppend_PlainKeys proves "append" merges new keys
// onto the existing ones (rather than replacing them), which is the whole
// point of offering append vs replace — losing existing keys on an intended
// append would silently drop upstream capacity.
func TestUpdateChannel_MultiKeyAppend_PlainKeys(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedUpdateMultiKeyChannel(t, ctx, "existing1\nexisting2")

	c, w := r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeOpenAI, "name": "upd-mk",
		"key": "newkey1\n newkey2 \n", "key_mode": "append",
	})
	UpdateChannel(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("append update failed: %s", w.Body.String())
	}

	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, ch.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Key != "existing1\nexisting2\nnewkey1\nnewkey2" {
		t.Fatalf("expected merged keys, got %q", reloaded.Key)
	}
}

// TestUpdateChannel_MultiKeyAppend_ExistingJSONArrayFormat covers the branch
// that parses the ORIGINAL key as a JSON array (rather than newline-split)
// when it starts with "[" — a format used by some multi-key imports.
func TestUpdateChannel_MultiKeyAppend_ExistingJSONArrayFormat(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedUpdateMultiKeyChannel(t, ctx, `["a1","a2"]`)
	ch.ChannelInfo.MultiKeySize = 2
	if err := ch.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c, w := r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeOpenAI, "name": "upd-mk",
		"key": "newkey", "key_mode": "append",
	})
	UpdateChannel(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("append (JSON-array-origin) update failed: %s", w.Body.String())
	}
	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, ch.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Key != `"a1"`+"\n"+`"a2"`+"\nnewkey" {
		t.Fatalf("expected existing JSON-array entries preserved + new key appended, got %q", reloaded.Key)
	}
}

// TestUpdateChannel_MultiKeyAppend_VertexArrayAndSingle covers the Vertex AI
// append branch: a JSON-array new key merges as multiple entries, a single
// non-array JSON key merges as one entry, and a malformed array is rejected.
func TestUpdateChannel_MultiKeyAppend_VertexArrayAndSingle(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	vertexSettings := `{"vertex_key_type":"json"}`
	ch := &repo.Channel{
		Name: "upd-vtx", TenantId: ctx.TenantID, Key: `{"project":"p0"}`,
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeVertexAi,
		Other: `{"default":"us-central1"}`, OtherSettings: vertexSettings,
		CreatedTime: common.GetTimestamp(),
		ChannelInfo: repo.ChannelInfo{IsMultiKey: true, MultiKeySize: 1},
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed vertex channel: %v", err)
	}

	// append a JSON array of new keys
	c, w := r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeVertexAi, "name": "upd-vtx",
		"other": `{"default":"us-central1"}`, "settings": vertexSettings,
		"key": `[{"project":"p1"},{"project":"p2"}]`, "key_mode": "append",
	})
	UpdateChannel(c)
	if resp := r2chanParseBody(t, w); resp["success"] != true {
		t.Fatalf("vertex array append failed: %s", w.Body.String())
	}
	var reloaded repo.Channel
	ctx.DB.First(&reloaded, ch.Id)
	if reloaded.Key != `{"project":"p0"}`+"\n"+`{"project":"p1"}`+"\n"+`{"project":"p2"}` {
		t.Fatalf("expected p0+p1+p2 merged, got %q", reloaded.Key)
	}

	// append a single (non-array) JSON key
	c, w = r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeVertexAi, "name": "upd-vtx",
		"other": `{"default":"us-central1"}`, "settings": vertexSettings,
		"key": `{"project":"p3"}`, "key_mode": "append",
	})
	UpdateChannel(c)
	if resp := r2chanParseBody(t, w); resp["success"] != true {
		t.Fatalf("vertex single append failed: %s", w.Body.String())
	}
	ctx.DB.First(&reloaded, ch.Id)
	if reloaded.ChannelInfo.MultiKeySize != 4 {
		t.Fatalf("expected size=4 after two appends, got %d", reloaded.ChannelInfo.MultiKeySize)
	}

	// malformed vertex array on append → rejected, no mutation.
	c, w = r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeVertexAi, "name": "upd-vtx",
		"other": `{"default":"us-central1"}`, "settings": vertexSettings,
		"key": "[not valid", "key_mode": "append",
	})
	UpdateChannel(c)
	if resp := r2chanParseBody(t, w); resp["success"] == true {
		t.Error("expected failure for malformed vertex append array")
	}
	var afterBad repo.Channel
	ctx.DB.First(&afterBad, ch.Id)
	if afterBad.ChannelInfo.MultiKeySize != 4 {
		t.Errorf("malformed append must not mutate existing keys, size=%d", afterBad.ChannelInfo.MultiKeySize)
	}
}

// TestUpdateChannel_KeyModeReplace_And_NotFound covers the "replace" no-op
// branch (explicit case, default overwrite semantics) and the
// GetChannelById-error branch (updating a non-existent channel id).
func TestUpdateChannel_KeyModeReplace_And_NotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedUpdateMultiKeyChannel(t, ctx, "k1\nk2")

	c, w := r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeOpenAI, "name": "upd-mk",
		"key": "onlynewkey", "key_mode": "replace",
	})
	UpdateChannel(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("replace update failed: %s", w.Body.String())
	}
	var reloaded repo.Channel
	ctx.DB.First(&reloaded, ch.Id)
	if reloaded.Key != "onlynewkey" {
		t.Fatalf("expected replace to overwrite key entirely, got %q", reloaded.Key)
	}

	// update a channel id that doesn't exist
	c, w = r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": 987654321, "type": constant.ChannelTypeOpenAI, "name": "ghost",
	})
	UpdateChannel(c)
	if resp := r2chanParseBody(t, w); resp["success"] == true {
		t.Error("expected failure updating a non-existent channel id")
	}
}

// TestUpdateChannel_MultiKeyModeOverride proves an explicit multi_key_mode in
// the request overrides the channel's existing polling/random mode.
func TestUpdateChannel_MultiKeyModeOverride(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedUpdateMultiKeyChannel(t, ctx, "k1\nk2")
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModePolling
	if err := ch.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c, w := r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeOpenAI, "name": "upd-mk",
		"multi_key_mode": "random",
	})
	UpdateChannel(c)
	if resp := r2chanParseBody(t, w); resp["success"] != true {
		t.Fatalf("multi_key_mode override failed: %s", w.Body.String())
	}
	var reloaded repo.Channel
	ctx.DB.First(&reloaded, ch.Id)
	if reloaded.ChannelInfo.MultiKeyMode != constant.MultiKeyModeRandom {
		t.Fatalf("expected mode overridden to random, got %v", reloaded.ChannelInfo.MultiKeyMode)
	}
}

// ─── GetAllChannels: tag mode, type filter, status filter, id_sort ─────────

func TestGetAllChannels_TagModeTenantScopedAndFiltered(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tag := "shared-tag"
	own := r2chanSeedChannel(t, ctx, "own-tagged", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	own.Tag = &tag
	if err := own.Update(); err != nil {
		t.Fatalf("tag own: %v", err)
	}
	victim := &repo.Channel{
		Name: "victim-tagged", TenantId: "other-tenant-tag", Key: "k",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI,
		Tag: &tag, CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/?tag_mode=true&p=1&page_size=50", nil)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	GetAllChannels(c)
	body := handler_channel_mkBody(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body=%s", w.Body.String())
	}
	items, _ := data["items"].([]interface{})
	for _, it := range items {
		m := it.(map[string]interface{})
		if m["tenant_id"] != ctx.TenantID {
			t.Errorf("tag-mode list leaked another tenant's channel: %v", m["tenant_id"])
		}
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 own-tenant tagged channel, got %d (body=%s)", len(items), w.Body.String())
	}
}

func TestGetAllChannels_StatusAndTypeFilters(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	r2chanSeedChannel(t, ctx, "enabled-openai", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	r2chanSeedChannel(t, ctx, "disabled-openai", constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)
	r2chanSeedChannel(t, ctx, "enabled-gemini", constant.ChannelTypeGemini, common.ChannelStatusEnabled)

	cases := []struct {
		name      string
		query     string
		wantTotal float64
	}{
		{"status=enabled", "status=enabled", 2},
		{"status=disabled", "status=disabled", 1},
		{"type filter", "type=" + strconv.Itoa(constant.ChannelTypeGemini), 1},
		{"id_sort", "id_sort=true", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/?"+tc.query, nil)
			c.Set("tenant_id", ctx.TenantID)
			c.Set("id", ctx.AdminUser.Id)
			GetAllChannels(c)
			body := handler_channel_mkBody(t, w)
			data, ok := body["data"].(map[string]interface{})
			if !ok {
				t.Fatalf("%s: expected data object, body=%s", tc.name, w.Body.String())
			}
			if data["total"] != tc.wantTotal {
				t.Errorf("%s: expected total=%v, got %v", tc.name, tc.wantTotal, data["total"])
			}
		})
	}
}

// ─── EditTagChannels: override validation + root-global scope ─────────────

func TestEditTagChannels_MalformedBody(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", []byte("{bad"))
	EditTagChannels(c)
	if resp := r2chanParseBody(t, w); resp["success"] == true {
		t.Error("expected failure for malformed body")
	}
}

func TestEditTagChannels_InvalidParamAndHeaderOverrideJSON(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	tag := "override-tag"
	ch := r2chanSeedChannel(t, ctx, "override-chan", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	ch.Tag = &tag
	if err := ch.Update(); err != nil {
		t.Fatalf("tag seed: %v", err)
	}

	// invalid param_override JSON
	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{
		"tag": tag, "param_override": "{not json",
	})
	EditTagChannels(c)
	if resp := r2chanParseBody(t, w); resp["success"] == true {
		t.Error("expected failure for invalid param_override JSON")
	}

	// invalid header_override JSON
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{
		"tag": tag, "header_override": "[not json}",
	})
	EditTagChannels(c)
	if resp := r2chanParseBody(t, w); resp["success"] == true {
		t.Error("expected failure for invalid header_override JSON")
	}

	// valid overrides persist (trimmed).
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{
		"tag": tag, "param_override": ` {"temperature":0.5} `, "header_override": ` {"X-Foo":"bar"} `,
	})
	EditTagChannels(c)
	if resp := r2chanParseBody(t, w); resp["success"] != true {
		t.Fatalf("valid override edit failed: %s", w.Body.String())
	}
	var reloaded repo.Channel
	ctx.DB.First(&reloaded, ch.Id)
	if reloaded.ParamOverride == nil || *reloaded.ParamOverride != `{"temperature":0.5}` {
		t.Errorf("expected trimmed param_override persisted, got %v", reloaded.ParamOverride)
	}
}

// TestEditTagChannels_RootGlobalScope proves root's tag edit is NOT
// tenant-scoped: it must reach a tag shared across tenants.
func TestEditTagChannels_RootGlobalScope(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	tag := "global-tag"
	own := r2chanSeedChannel(t, ctx, "own-global", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	own.Tag = &tag
	if err := own.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	victim := &repo.Channel{
		Name: "victim-global", TenantId: "other-tenant-global", Key: "k",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI,
		Tag: &tag, CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	newTag := "renamed-global-tag"
	c, w := v1CtxJSON(http.MethodPost, "/x", map[string]interface{}{
		"tag": tag, "new_tag": newTag,
	}, ctx.TenantID, ctx.RootUser.Id)
	c.Set("role", common.RoleRootUser)
	EditTagChannels(c)
	if resp := handler_channel_mkBody(t, w); resp["success"] != true {
		t.Fatalf("root tag edit failed: %s", w.Body.String())
	}

	var reloadedVictim repo.Channel
	ctx.DB.First(&reloadedVictim, victim.Id)
	if reloadedVictim.Tag == nil || *reloadedVictim.Tag != newTag {
		t.Errorf("expected root's tag edit to reach the OTHER tenant's channel too, got %v", reloadedVictim.Tag)
	}
}
