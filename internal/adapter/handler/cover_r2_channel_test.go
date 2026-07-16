package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// R2Chan shared test scaffolding
//
// These tests exercise the admin channel/model/ratio/openrouter-sync HTTP
// handlers by invoking them directly against a gin test context backed by the
// in-memory SQLite DB set up by SetupV2TestRouter (which also swaps repo.DB and
// the relevant common.* flags and restores them on Cleanup). We reuse that
// harness's seed data and only migrate the extra tables these handlers touch.
// ============================================================================

// r2chanSetup reuses the package V2 harness for DB + seeded users/tenant and
// migrates the additional tables the R2Chan handlers depend on.
func r2chanSetup(t *testing.T) *V2TestContext {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	if err := ctx.DB.AutoMigrate(&repo.OpenRouterSyncJob{}); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("migrate OpenRouterSyncJob: %v", err)
	}
	if err := ctx.DB.AutoMigrate(&repo.Model{}); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("migrate Model: %v", err)
	}
	return ctx
}

// r2chanNewCtx builds a gin test context with an HTTP request (query params are
// parsed from target) and returns it plus the recorder.
func r2chanNewCtx(method, target string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var req *http.Request
	if body != nil {
		var data []byte
		switch b := body.(type) {
		case []byte:
			data = b
		case string:
			data = []byte(b)
		default:
			data, _ = json.Marshal(body)
		}
		req = httptest.NewRequest(method, target, bytes.NewReader(data))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

// r2chanAdminCtx builds a context that carries an admin identity (id + tenant).
func r2chanAdminCtx(ctx *V2TestContext, method, target string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := r2chanNewCtx(method, target, body)
	c.Set("id", ctx.AdminUser.Id)
	c.Set("tenant_id", ctx.TenantID)
	return c, w
}

func r2chanParseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	return m
}

func r2chanSeedChannel(t *testing.T, ctx *V2TestContext, name string, chType, status int) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name:        name,
		TenantId:    ctx.TenantID,
		Key:         "sk-" + common.GetRandomString(24),
		Status:      status,
		Type:        chType,
		Models:      "gpt-4,gpt-3.5-turbo",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return ch
}

// ---------------------------------------------------------------------------
// channel.go — pure helpers
// ---------------------------------------------------------------------------

func TestR2Chan_ParseStatusFilter(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"enabled", common.ChannelStatusEnabled},
		{"1", common.ChannelStatusEnabled},
		{"disabled", 0},
		{"0", 0},
		{"", -1},
		{"garbage", -1},
	}
	for _, tc := range cases {
		if got := parseStatusFilter(tc.in); got != tc.want {
			t.Errorf("parseStatusFilter(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestR2Chan_ClearChannelInfo(t *testing.T) {
	ch := &repo.Channel{}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeyDisabledReason = map[int]string{0: "banned"}
	ch.ChannelInfo.MultiKeyDisabledTime = map[int]int64{0: 123}
	clearChannelInfo(ch)
	if ch.ChannelInfo.MultiKeyDisabledReason != nil || ch.ChannelInfo.MultiKeyDisabledTime != nil {
		t.Error("expected multi-key disabled reason/time cleared")
	}

	// Non multi-key channel is left untouched.
	ch2 := &repo.Channel{}
	ch2.ChannelInfo.MultiKeyDisabledReason = map[int]string{0: "banned"}
	clearChannelInfo(ch2)
	if ch2.ChannelInfo.MultiKeyDisabledReason == nil {
		t.Error("non-multikey channel should not be cleared")
	}
}

func TestR2Chan_GetVertexArrayKeys(t *testing.T) {
	// empty input
	keys, err := getVertexArrayKeys("")
	if err != nil || keys != nil {
		t.Errorf("empty: got keys=%v err=%v", keys, err)
	}
	// invalid JSON
	if _, err := getVertexArrayKeys("not-json"); err == nil {
		t.Error("expected error for invalid json")
	}
	// empty array
	if _, err := getVertexArrayKeys("[]"); err == nil {
		t.Error("expected error for empty array")
	}
	// string + object elements
	got, err := getVertexArrayKeys(`["abc", {"type":"service_account"}]`)
	if err != nil {
		t.Fatalf("valid array: %v", err)
	}
	if len(got) != 2 || got[0] != "abc" {
		t.Errorf("unexpected keys: %#v", got)
	}
	if !strings.Contains(got[1], "service_account") {
		t.Errorf("expected object encoded, got %q", got[1])
	}
}

func TestR2Chan_BuildFetchModelsHeaders(t *testing.T) {
	// Anthropic uses x-api-key.
	claudeCh := &repo.Channel{Type: constant.ChannelTypeAnthropic}
	h, err := buildFetchModelsHeaders(claudeCh, "secret")
	if err != nil {
		t.Fatalf("claude headers: %v", err)
	}
	if h.Get("x-api-key") != "secret" {
		t.Errorf("expected x-api-key=secret, got %q", h.Get("x-api-key"))
	}

	// Default uses Bearer + honors {api_key} placeholder override.
	override := `{"X-Proxy":"pre-{api_key}"}`
	openaiCh := &repo.Channel{Type: constant.ChannelTypeOpenAI, HeaderOverride: &override}
	h2, err := buildFetchModelsHeaders(openaiCh, "tok")
	if err != nil {
		t.Fatalf("openai headers: %v", err)
	}
	if h2.Get("Authorization") != "Bearer tok" {
		t.Errorf("expected Bearer tok, got %q", h2.Get("Authorization"))
	}
	if h2.Get("X-Proxy") != "pre-tok" {
		t.Errorf("expected override replaced, got %q", h2.Get("X-Proxy"))
	}

	// Non-string override value is rejected.
	badOverride := `{"X-Bad":123}`
	badCh := &repo.Channel{Type: constant.ChannelTypeOpenAI, HeaderOverride: &badOverride}
	if _, err := buildFetchModelsHeaders(badCh, "tok"); err == nil {
		t.Error("expected error for non-string override value")
	}
}

func TestR2Chan_ValidateChannel(t *testing.T) {
	// add mode with empty key
	if err := validateChannel(&repo.Channel{Type: constant.ChannelTypeOpenAI}, true); err == nil {
		t.Error("expected error for empty key on add")
	}
	// model name too long
	long := strings.Repeat("m", 256)
	if err := validateChannel(&repo.Channel{Type: constant.ChannelTypeOpenAI, Key: "k", Models: long}, true); err == nil {
		t.Error("expected error for over-long model name")
	}
	// Vertex without Other
	if err := validateChannel(&repo.Channel{Type: constant.ChannelTypeVertexAi, Key: "k"}, true); err == nil {
		t.Error("expected error for vertex missing region")
	}
	// Vertex with invalid JSON Other
	if err := validateChannel(&repo.Channel{Type: constant.ChannelTypeVertexAi, Key: "k", Other: "not-json"}, true); err == nil {
		t.Error("expected error for vertex invalid json region")
	}
	// Vertex missing default field
	if err := validateChannel(&repo.Channel{Type: constant.ChannelTypeVertexAi, Key: "k", Other: `{"r2":"x"}`}, true); err == nil {
		t.Error("expected error for vertex missing default region")
	}
	// valid non-add
	if err := validateChannel(&repo.Channel{Type: constant.ChannelTypeOpenAI}, false); err != nil {
		t.Errorf("expected valid channel, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// channel.go — DB-backed handlers
// ---------------------------------------------------------------------------

func TestR2Chan_GetAllChannels(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	r2chanSeedChannel(t, ctx, "chan-a", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	r2chanSeedChannel(t, ctx, "chan-b", constant.ChannelTypeAnthropic, 2) // disabled

	c, w := r2chanAdminCtx(ctx, http.MethodGet, "/api/channel/?p=1&page_size=10", nil)
	GetAllChannels(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	if data["total"].(float64) < 2 {
		t.Errorf("expected total>=2, got %v", data["total"])
	}

	// status=enabled filter should exclude the disabled channel from total.
	c2, w2 := r2chanAdminCtx(ctx, http.MethodGet, "/api/channel/?status=enabled", nil)
	GetAllChannels(c2)
	resp2 := r2chanParseBody(t, w2)
	data2 := resp2["data"].(map[string]interface{})
	if data2["total"].(float64) != 1 {
		t.Errorf("expected 1 enabled channel, got %v", data2["total"])
	}
}

func TestR2Chan_SearchChannels(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	r2chanSeedChannel(t, ctx, "searchable-unique", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)

	c, w := r2chanAdminCtx(ctx, http.MethodGet, "/api/channel/search?keyword=searchable-unique", nil)
	SearchChannels(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) == 0 {
		t.Error("expected at least one search result")
	}
}

func TestR2Chan_GetChannel(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	ch := r2chanSeedChannel(t, ctx, "get-me", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)

	// success
	c, w := r2chanAdminCtx(ctx, http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(ch.Id)}}
	GetChannel(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}

	// non-numeric id → error
	c2, w2 := r2chanNewCtx(http.MethodGet, "/", nil)
	c2.Params = gin.Params{{Key: "id", Value: "abc"}}
	GetChannel(c2)
	if r2chanParseBody(t, w2)["success"] == true {
		t.Error("expected failure for non-numeric id")
	}

	// not found id → error
	c3, w3 := r2chanNewCtx(http.MethodGet, "/", nil)
	c3.Params = gin.Params{{Key: "id", Value: "999999"}}
	GetChannel(c3)
	if r2chanParseBody(t, w3)["success"] == true {
		t.Error("expected failure for missing channel")
	}
}

func TestR2Chan_DeleteChannel(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	ch := r2chanSeedChannel(t, ctx, "delete-me", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)

	c, w := r2chanAdminCtx(ctx, http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(ch.Id)}}
	DeleteChannel(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("delete failed: %s", w.Body.String())
	}
	var count int64
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Count(&count)
	if count != 0 {
		t.Errorf("expected channel row deleted, count=%d", count)
	}
}

func TestR2Chan_DeleteDisabledChannel(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	r2chanSeedChannel(t, ctx, "dis", constant.ChannelTypeOpenAI, 2) // disabled status

	c, w := r2chanNewCtx(http.MethodDelete, "/", nil)
	DeleteDisabledChannel(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("delete disabled failed: %s", w.Body.String())
	}
}

func TestR2Chan_FixChannelsAbilities(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	c, w := r2chanNewCtx(http.MethodPost, "/", nil)
	FixChannelsAbilities(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("fix abilities failed: %s", w.Body.String())
	}
}

func TestR2Chan_TagHandlers(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	tag := "team-tag"
	ch := r2chanSeedChannel(t, ctx, "tagged", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Update("tag", tag)

	// DisableTagChannels — empty tag rejected
	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", ChannelTag{})
	DisableTagChannels(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for empty tag disable")
	}
	// DisableTagChannels — valid
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", ChannelTag{Tag: tag})
	DisableTagChannels(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Errorf("disable tag failed: %s", w.Body.String())
	}
	// EnableTagChannels — empty tag rejected
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", ChannelTag{})
	EnableTagChannels(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for empty tag enable")
	}
	// EnableTagChannels — valid
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", ChannelTag{Tag: tag})
	EnableTagChannels(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Errorf("enable tag failed: %s", w.Body.String())
	}
}

func TestR2Chan_EditTagChannels(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	tag := "edit-tag"
	ch := r2chanSeedChannel(t, ctx, "edit-tagged", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Update("tag", tag)

	// empty tag → error
	c, w := r2chanAdminCtx(ctx, http.MethodPut, "/", ChannelTag{})
	EditTagChannels(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for empty tag edit")
	}
	// invalid ParamOverride JSON → error
	bad := "{not json"
	c, w = r2chanAdminCtx(ctx, http.MethodPut, "/", ChannelTag{Tag: tag, ParamOverride: &bad})
	EditTagChannels(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for invalid param override")
	}
	// valid edit
	newTag := "edit-tag-new"
	c, w = r2chanAdminCtx(ctx, http.MethodPut, "/", ChannelTag{Tag: tag, NewTag: &newTag})
	EditTagChannels(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Errorf("edit tag failed: %s", w.Body.String())
	}
}

func TestR2Chan_DeleteChannelBatch(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	a := r2chanSeedChannel(t, ctx, "b1", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	b := r2chanSeedChannel(t, ctx, "b2", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)

	// empty ids → error
	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", ChannelBatch{})
	DeleteChannelBatch(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for empty ids")
	}
	// valid
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", ChannelBatch{Ids: []int{a.Id, b.Id}})
	DeleteChannelBatch(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true || resp["data"].(float64) != 2 {
		t.Errorf("batch delete failed: %s", w.Body.String())
	}
}

func TestR2Chan_BatchSetChannelTag(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()

	// empty ids → validation error (returns before any DB access).
	//
	// The valid execution path is intentionally NOT exercised here: the repo's
	// BatchSetChannelTag opens a write transaction and then issues a read via the
	// global DB handle on a separate connection. That works on PostgreSQL's
	// connection pool but deadlocks under the shared-cache in-memory SQLite used
	// by this hermetic harness, so only the guard branch is covered.
	c, w := r2chanNewCtx(http.MethodPost, "/", ChannelBatch{})
	BatchSetChannelTag(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for empty ids")
	}
}

func TestR2Chan_GetTagModels(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	tag := "modeltag"
	ch := r2chanSeedChannel(t, ctx, "tm", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Update("tag", tag)

	// missing tag → 400
	c, w := r2chanNewCtx(http.MethodGet, "/api/channel/tag/models", nil)
	GetTagModels(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing tag, got %d", w.Code)
	}
	// valid tag
	c, w = r2chanNewCtx(http.MethodGet, "/api/channel/tag/models?tag="+tag, nil)
	GetTagModels(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("get tag models failed: %s", w.Body.String())
	}
	if resp["data"].(string) != "gpt-4,gpt-3.5-turbo" {
		t.Errorf("expected longest models, got %v", resp["data"])
	}
}

func TestR2Chan_UpdateChannel(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	ch := r2chanSeedChannel(t, ctx, "upd", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)

	// invalid JSON body → error
	c, w := r2chanAdminCtx(ctx, http.MethodPut, "/", []byte("{bad"))
	UpdateChannel(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for invalid body")
	}

	// validate failure: Vertex without region
	c, w = r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeVertexAi,
	})
	UpdateChannel(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected validation failure for vertex")
	}

	// valid update of name
	c, w = r2chanAdminCtx(ctx, http.MethodPut, "/", map[string]interface{}{
		"id": ch.Id, "type": constant.ChannelTypeOpenAI, "name": "renamed",
	})
	UpdateChannel(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("update failed: %s", w.Body.String())
	}
	var name string
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Select("name").Scan(&name)
	if name != "renamed" {
		t.Errorf("expected name persisted, got %q", name)
	}
}

func TestR2Chan_AddChannel(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()

	// nil channel → 400
	c, w := r2chanAdminCtx(ctx, http.MethodPost, "/", map[string]interface{}{"mode": "single"})
	AddChannel(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for nil channel, got %d", w.Code)
	}

	// unsupported mode → failure
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", AddChannelRequest{
		Mode:    "bogus",
		Channel: &repo.Channel{Type: constant.ChannelTypeOpenAI, Key: "k", Name: "x"},
	})
	AddChannel(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for unsupported mode")
	}

	// valid single add
	c, w = r2chanAdminCtx(ctx, http.MethodPost, "/", AddChannelRequest{
		Mode:    "single",
		Channel: &repo.Channel{Type: constant.ChannelTypeOpenAI, Key: "sk-added", Name: "added", Models: "gpt-4"},
	})
	AddChannel(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("add channel failed: %s", w.Body.String())
	}
	var count int64
	ctx.DB.Model(&repo.Channel{}).Where("name = ?", "added").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 added channel, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// channel-billing.go
// ---------------------------------------------------------------------------

func TestR2Chan_AuthHeaders(t *testing.T) {
	if h := GetAuthHeader("abc"); h.Get("Authorization") != "Bearer abc" {
		t.Errorf("GetAuthHeader = %q", h.Get("Authorization"))
	}
	h := GetClaudeAuthHeader("xyz")
	if h.Get("x-api-key") != "xyz" || h.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("GetClaudeAuthHeader unexpected: %v", h)
	}
}

func TestR2Chan_UpdateChannelBalanceHandler(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()

	// invalid id → error
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	UpdateChannelBalance(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for invalid id")
	}

	// Azure channel → "尚未实现" surfaced as an error
	azure := r2chanSeedChannel(t, ctx, "azure", constant.ChannelTypeAzure, common.ChannelStatusEnabled)
	c, w = r2chanAdminCtx(ctx, http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(azure.Id)}}
	UpdateChannelBalance(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for azure balance (not implemented)")
	}
}

func TestR2Chan_UpdateChannelBalance_Direct(t *testing.T) {
	// Azure short-circuits with "尚未实现" and makes no network call.
	if _, err := updateChannelBalance(&repo.Channel{Type: constant.ChannelTypeAzure}); err == nil {
		t.Error("expected error for azure")
	}
}

func TestR2Chan_UpdateAllChannelsBalanceHandler(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	// Only an Azure channel is enabled; its per-channel update errors are skipped,
	// so the aggregate handler still succeeds without any network I/O.
	r2chanSeedChannel(t, ctx, "azure-only", constant.ChannelTypeAzure, common.ChannelStatusEnabled)

	c, w := r2chanNewCtx(http.MethodPost, "/", nil)
	UpdateAllChannelsBalance(c)
	if w.Code != http.StatusOK || r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("update all balance failed: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// model.go
// ---------------------------------------------------------------------------

func TestR2Chan_StaticModelLists(t *testing.T) {
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	ChannelListModels(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Error("ChannelListModels not success")
	}

	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	DashboardListModels(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Error("DashboardListModels not success")
	}

	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	EnabledListModels(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Error("EnabledListModels not success")
	}
}

func TestR2Chan_RetrieveModel(t *testing.T) {
	// pick an existing model id from the package map
	var existing string
	for id := range openAIModelsMap {
		existing = id
		break
	}
	if existing == "" {
		t.Skip("no models registered")
	}
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "model", Value: existing}}
	RetrieveModel(c, constant.ChannelTypeOpenAI)
	resp := r2chanParseBody(t, w)
	if resp["id"] != existing {
		t.Errorf("expected model id %q, got %v", existing, resp["id"])
	}

	// non-existent → error object
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "model", Value: "no-such-model-xyz"}}
	RetrieveModel(c, constant.ChannelTypeOpenAI)
	if _, ok := r2chanParseBody(t, w)["error"]; !ok {
		t.Error("expected error object for missing model")
	}
}

func TestR2Chan_ListModels(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	c.Set("id", ctx.NormalUser.Id)
	ListModels(c, constant.ChannelTypeOpenAI)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	if resp["object"] != "list" {
		t.Errorf("expected object=list, got %v", resp["object"])
	}
}

// ---------------------------------------------------------------------------
// openrouter_sync.go
// ---------------------------------------------------------------------------

func TestR2Chan_OpenRouterSync_Validators(t *testing.T) {
	if isValidCategory("bogus") {
		t.Error("bogus category should be invalid")
	}
	if !isValidCategory("vision") {
		t.Error("vision should be valid")
	}
	if isValidSchedule("hourly") {
		t.Error("hourly schedule should be invalid")
	}
	if !isValidSchedule("daily") {
		t.Error("daily should be valid")
	}
	if err := validateJobBody("", 1, []string{"vision"}, "daily"); err == nil {
		t.Error("expected error for empty name")
	}
	if err := validateJobBody("n", 0, []string{"vision"}, "daily"); err == nil {
		t.Error("expected error for bad channel id")
	}
	if err := validateJobBody("n", 1, nil, "daily"); err == nil {
		t.Error("expected error for empty categories")
	}
	if err := validateJobBody("n", 1, []string{"bogus"}, "daily"); err == nil {
		t.Error("expected error for bad category")
	}
	if err := validateJobBody("n", 1, []string{"vision"}, "bogus"); err == nil {
		t.Error("expected error for bad schedule")
	}
	if err := validateJobBody("n", 1, []string{"vision"}, "daily"); err != nil {
		t.Errorf("expected valid, got %v", err)
	}
	if got := errInvalidJobField("name").Error(); !strings.Contains(got, "name") {
		t.Errorf("errInvalidJobField message unexpected: %q", got)
	}
}

func TestR2Chan_OpenRouterSync_CRUD(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()

	// list empty
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	ListOpenRouterSyncJobs(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("list failed: %s", w.Body.String())
	}

	// create: invalid body
	c, w = r2chanNewCtx(http.MethodPost, "/", []byte("{bad"))
	CreateOpenRouterSyncJob(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for invalid create body")
	}

	// create: validation failure (empty name)
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{
		"target_channel_id": 1, "categories": []string{"vision"}, "schedule": "daily",
	})
	CreateOpenRouterSyncJob(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for missing name")
	}

	// create: valid
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{
		"name": "job1", "target_channel_id": 7, "categories": []string{"vision"}, "schedule": "daily",
	})
	CreateOpenRouterSyncJob(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("create failed: %s", w.Body.String())
	}
	jobData := resp["data"].(map[string]interface{})
	jobID := int(jobData["id"].(float64))
	if jobID == 0 {
		t.Fatal("expected non-zero job id")
	}

	// update: invalid id
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{})
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	UpdateOpenRouterSyncJob(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for invalid update id")
	}

	// update: valid rename
	newName := "job1-renamed"
	c, w = r2chanNewCtx(http.MethodPut, "/", map[string]interface{}{"name": newName})
	c.Params = gin.Params{{Key: "id", Value: intToStr(jobID)}}
	UpdateOpenRouterSyncJob(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("update failed: %s", w.Body.String())
	}

	// preview: not-found id short-circuits before any network call
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "888888"}}
	PreviewOpenRouterSyncJob(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for missing preview job")
	}

	// run: invalid id
	c, w = r2chanNewCtx(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "0"}}
	RunOpenRouterSyncJob(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for invalid run id")
	}

	// delete: invalid id
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "-1"}}
	DeleteOpenRouterSyncJob(c)
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for invalid delete id")
	}

	// delete: valid
	c, w = r2chanNewCtx(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: intToStr(jobID)}}
	DeleteOpenRouterSyncJob(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Fatalf("delete failed: %s", w.Body.String())
	}
	var count int64
	ctx.DB.Model(&repo.OpenRouterSyncJob{}).Where("id = ?", jobID).Count(&count)
	if count != 0 {
		t.Errorf("expected job deleted, count=%d", count)
	}
}

func TestR2Chan_OpenRouterSync_Misc(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()

	// categories list
	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	ListOpenRouterSyncCategories(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true || len(resp["data"].([]interface{})) != 5 {
		t.Errorf("expected 5 categories, body=%s", w.Body.String())
	}

	// last status (empty)
	c, w = r2chanNewCtx(http.MethodGet, "/", nil)
	GetOpenRouterSyncLastStatus(c)
	if r2chanParseBody(t, w)["success"] != true {
		t.Errorf("last status failed: %s", w.Body.String())
	}

	// run-all with no enabled jobs → skipped
	c, w = r2chanNewCtx(http.MethodPost, "/", nil)
	RunAllOpenRouterSyncJobs(c)
	resp = r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("run-all failed: %s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	if data["skipped"] != true {
		t.Errorf("expected skipped=true, got %v", data)
	}
}

// ---------------------------------------------------------------------------
// ratio_sync.go
// ---------------------------------------------------------------------------

func TestR2Chan_RatioHelpers(t *testing.T) {
	if !nearlyEqual(1.0, 1.0+1e-12) {
		t.Error("nearlyEqual should treat tiny delta as equal")
	}
	if nearlyEqual(1.0, 1.5) {
		t.Error("nearlyEqual should treat 1.0 vs 1.5 as unequal")
	}
	if !valuesEqual(2.0, 2.0) {
		t.Error("valuesEqual float equal")
	}
	if valuesEqual(2.0, 3.0) {
		t.Error("valuesEqual float unequal")
	}
	if !valuesEqual("x", "x") {
		t.Error("valuesEqual string equal")
	}
}

func TestR2Chan_BuildDifferences(t *testing.T) {
	localData := map[string]any{
		"model_ratio": map[string]float64{"gpt-4": 1.0},
	}
	channels := []struct {
		name string
		data map[string]any
	}{
		{
			name: "upstream-a",
			data: map[string]any{
				"model_ratio": map[string]any{"gpt-4": float64(2.0)},
			},
		},
	}
	diffs := buildDifferences(localData, channels)
	item, ok := diffs["gpt-4"]["model_ratio"]
	if !ok {
		t.Fatalf("expected a model_ratio difference for gpt-4, got %#v", diffs)
	}
	if item.Current.(float64) != 1.0 {
		t.Errorf("expected current 1.0, got %v", item.Current)
	}
	if item.Upstreams["upstream-a"].(float64) != 2.0 {
		t.Errorf("expected upstream 2.0, got %v", item.Upstreams["upstream-a"])
	}
}

func TestR2Chan_GetSyncableChannels(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	ch := r2chanSeedChannel(t, ctx, "syncable", constant.ChannelTypeOpenAI, common.ChannelStatusEnabled)
	base := "https://api.example.com"
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", ch.Id).Update("base_url", base)

	c, w := r2chanNewCtx(http.MethodGet, "/", nil)
	GetSyncableChannels(c)
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("syncable failed: %s", w.Body.String())
	}
	items := resp["data"].([]interface{})
	var foundPreset bool
	for _, it := range items {
		m := it.(map[string]interface{})
		if int(m["id"].(float64)) == -100 {
			foundPreset = true
		}
	}
	if !foundPreset {
		t.Error("expected official preset channel (-100) in syncable list")
	}
}

func TestR2Chan_FetchUpstreamRatios(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()

	// invalid body → 400
	c, w := r2chanNewCtx(http.MethodPost, "/", []byte("{bad"))
	FetchUpstreamRatios(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w.Code)
	}

	// no upstreams/channels → success:false "无有效上游渠道"
	c, w = r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{})
	FetchUpstreamRatios(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	if r2chanParseBody(t, w)["success"] == true {
		t.Error("expected failure for empty upstreams")
	}
}

// ---------------------------------------------------------------------------
// model_sync.go — offline early-return path
// ---------------------------------------------------------------------------

func TestR2Chan_SyncUpstreamModels_NoWork(t *testing.T) {
	ctx := r2chanSetup(t)
	defer ctx.Cleanup()
	// No abilities enabled → GetMissingModels returns empty, and with no
	// overwrite fields the handler returns success without any network fetch.
	c, w := r2chanNewCtx(http.MethodPost, "/", map[string]interface{}{})
	SyncUpstreamModels(c)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	resp := r2chanParseBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	if data["created_models"].(float64) != 0 {
		t.Errorf("expected 0 created models, got %v", data["created_models"])
	}
}

// intToStr is a tiny local helper for building path params.
func intToStr(i int) string {
	return strconv.Itoa(i)
}
