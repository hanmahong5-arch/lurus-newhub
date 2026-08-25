package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// --- extractModelNameFromGeminiPath (pure) -------------------------------

func TestExtractModelNameFromGeminiPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1beta/models/gemini-2.0-flash:generateContent", "gemini-2.0-flash"},
		{"/v1/models/gemini-1.5-pro:streamGenerateContent", "gemini-1.5-pro"},
		{"/v1beta/models/gemini-pro", "gemini-pro"}, // no colon → to end
		{"/v1beta/models/", ""},     // empty after prefix
		{"/v1beta/openai/chat", ""}, // no /models/ segment
	}
	for _, tc := range cases {
		if got := extractModelNameFromGeminiPath(tc.path); got != tc.want {
			t.Errorf("extractModelNameFromGeminiPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// --- getModelRequest (path/body driven, mostly pure) ---------------------

func TestGetModelRequest_JSONBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o"}`, "application/json")
	mr, shouldSelect, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", mr.Model)
	}
	if !shouldSelect {
		t.Errorf("shouldSelectChannel = false, want true for plain chat path")
	}
}

func TestGetModelRequest_BadJSON(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{not-json`, "application/json")
	_, _, err := getModelRequest(c)
	if err == nil {
		t.Fatalf("expected error for malformed JSON body")
	}
}

func TestGetModelRequest_DefaultModelsByPath(t *testing.T) {
	cases := []struct {
		name, path, body, want string
	}{
		{"images_default", "/v1/images/generations", `{}`, "dall-e"},
		{"audio_speech_default", "/v1/audio/speech", `{}`, "tts-1"},
		{"moderations_default", "/v1/moderations", `{}`, "text-moderation-stable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestContext(http.MethodPost, tc.path, tc.body, "application/json")
			mr, _, err := getModelRequest(c)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if mr.Model != tc.want {
				t.Errorf("model = %q, want %q", mr.Model, tc.want)
			}
		})
	}
}

func TestGetModelRequest_GeminiPath(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", ``, "")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "gemini-2.0-flash" {
		t.Errorf("model = %q, want gemini-2.0-flash", mr.Model)
	}
	if rm, ok := c.Get("relay_mode"); !ok || rm == nil {
		t.Errorf("relay_mode not set for gemini path")
	}
}

func TestGetModelRequest_VideosGet_SkipsSelect(t *testing.T) {
	c, _ := newTestContext(http.MethodGet, "/v1/videos/abc", ``, "")
	_, shouldSelect, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if shouldSelect {
		t.Errorf("GET /v1/videos must not select a channel (fetch-by-id)")
	}
}

func TestGetModelRequest_VideoGenerationsPost(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/video/generations", `{"model":"sora-2"}`, "application/json")
	mr, shouldSelect, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "sora-2" {
		t.Errorf("model = %q, want sora-2", mr.Model)
	}
	if !shouldSelect {
		t.Errorf("POST video generations should select a channel")
	}
}

func TestGetModelRequest_AudioMusicSubmitDefault(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/audio/music", `{}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "music_generate" {
		t.Errorf("model = %q, want music_generate", mr.Model)
	}
}

func TestGetModelRequest_PlaygroundSetsGroup(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/pg/chat/completions", `{"model":"gpt-4o","group":"vip"}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "gpt-4o" || mr.Group != "vip" {
		t.Errorf("got model=%q group=%q, want gpt-4o/vip", mr.Model, mr.Group)
	}
	if g := common.GetContextKeyString(c, constant.ContextKeyTokenGroup); g != "vip" {
		t.Errorf("token group context = %q, want vip", g)
	}
}

func TestGetModelRequest_AudioTranscriptions_DefaultWhisper(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/audio/transcriptions", `{}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "whisper-1" {
		t.Errorf("model = %q, want whisper-1", mr.Model)
	}
}

func TestGetModelRequest_AudioTranslations_DefaultWhisper(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/audio/translations", `{}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "whisper-1" {
		t.Errorf("model = %q, want whisper-1", mr.Model)
	}
}

func TestGetModelRequest_Realtime_ModelFromQuery(t *testing.T) {
	c, _ := newTestContext(http.MethodGet, "/v1/realtime?model=gpt-4o-realtime-preview", ``, "")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "gpt-4o-realtime-preview" {
		t.Errorf("model = %q, want gpt-4o-realtime-preview", mr.Model)
	}
}

func TestGetModelRequest_Embeddings_ModelFromBody(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/embeddings", `{"model":"text-embedding-3-small"}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "text-embedding-3-small" {
		t.Errorf("model = %q, want text-embedding-3-small", mr.Model)
	}
}

func TestGetModelRequest_VideosRemix_SkipsSelect(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/videos/vid-123/remix", `{}`, "application/json")
	_, shouldSelect, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if shouldSelect {
		t.Errorf("video remix must not select a channel")
	}
}

func TestGetModelRequest_ImagesEdits_ModelDefault(t *testing.T) {
	// JSON content-type (not multipart) → the images/edits branch leaves the
	// model as parsed from the JSON body.
	c, _ := newTestContext(http.MethodPost, "/v1/images/edits", `{"model":"gpt-image-1"}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "gpt-image-1" {
		t.Errorf("model = %q, want gpt-image-1", mr.Model)
	}
}

// --- SetupContextForSelectedChannel --------------------------------------

func TestSetupContextForSelectedChannel_NilChannel(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	err := SetupContextForSelectedChannel(c, nil, "gpt-4o")
	if err == nil {
		t.Fatalf("expected error for nil channel")
	}
	if v, _ := c.Get("original_model"); v != "gpt-4o" {
		t.Errorf("original_model = %v, want gpt-4o", v)
	}
}

func TestSetupContextForSelectedChannel_SingleKeyChannel(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	base := "https://api.example.com"
	ch := &repo.Channel{
		Id:      7,
		Name:    "primary",
		Type:    constant.ChannelTypeAzure,
		Key:     "sk-secret-key",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &base,
		Other:   "2024-02-01",
	}
	if err := SetupContextForSelectedChannel(c, ch, "gpt-4o"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id := common.GetContextKeyInt(c, constant.ContextKeyChannelId); id != 7 {
		t.Errorf("channel id ctx = %d, want 7", id)
	}
	if k := common.GetContextKeyString(c, constant.ContextKeyChannelKey); k != "sk-secret-key" {
		t.Errorf("channel key ctx = %q, want sk-secret-key", k)
	}
	// Azure branch sets api_version from channel.Other.
	if v, _ := c.Get("api_version"); v != "2024-02-01" {
		t.Errorf("api_version = %v, want 2024-02-01", v)
	}
}

func TestSetupContextForSelectedChannel_ChannelTypeSwitch(t *testing.T) {
	// Each of these channel types routes channel.Other into a distinct context
	// key via the type switch — cover every arm.
	cases := []struct {
		name   string
		typ    int
		ctxKey string
		other  string
	}{
		{"vertex_region", constant.ChannelTypeVertexAi, "region", "us-central1"},
		{"gemini_version", constant.ChannelTypeGemini, "api_version", "v1beta"},
		{"ali_plugin", constant.ChannelTypeAli, "plugin", "web_search"},
		{"coze_bot", constant.ChannelTypeCoze, "bot_id", "bot-42"},
		{"xunfei_version", constant.ChannelTypeXunfei, "api_version", "v3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
			ch := &repo.Channel{Id: 1, Name: "c", Type: tc.typ, Key: "k", Status: common.ChannelStatusEnabled, Other: tc.other}
			if err := SetupContextForSelectedChannel(c, ch, "m"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v, _ := c.Get(tc.ctxKey); v != tc.other {
				t.Errorf("%s = %v, want %q", tc.ctxKey, v, tc.other)
			}
		})
	}
}

// --- Distribute: full middleware chain, every abort branch ---------------

// mountDistribute wires an engine that seeds ctxSetup values before Distribute
// runs, then a 200 terminal handler. Anything other than 200 means an abort.
func mountDistribute(ctxSetup func(c *gin.Context)) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if ctxSetup != nil {
			ctxSetup(c)
		}
		c.Next()
	})
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	for _, p := range []string{"/v1/chat/completions"} {
		r.POST(p, Distribute(), pass)
	}
	return r
}

func doDistribute(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDistribute_SpecificChannel_InvalidIdFormat(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "not-an-int")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for non-integer channel id; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_SpecificChannel_NotFound(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "99999")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing channel; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_SpecificChannel_Disabled(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	ch := &repo.Channel{Id: 11, Name: "disabled", TenantId: "default", Key: "k", Status: common.ChannelStatusManuallyDisabled, Models: "gpt-4o", Group: "default"}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "11")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for disabled channel; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_SpecificChannel_EnabledPasses(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	ch := &repo.Channel{Id: 12, Name: "ok", TenantId: "default", Key: "k", Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default"}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "12")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for enabled specific channel; body=%s", w.Code, w.Body.String())
	}
}

// Distribute must abort (not c.Next()) when SetupContextForSelectedChannel
// fails to obtain a usable key (e.g. a multi-key channel with an empty key
// list). Previously the error was discarded and the request proceeded to the
// relay with no ContextKeyChannelKey/ContextKeyChannelBaseUrl set.
func TestDistribute_SpecificChannel_NoKeysAvailable_Aborts(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	ch := &repo.Channel{Id: 13, Name: "no-keys", TenantId: "default", Key: "", Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default"}
	ch.ChannelInfo.IsMultiKey = true
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "13")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when channel has no available keys; body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("downstream handler ran despite SetupContextForSelectedChannel error; body=%s", w.Body.String())
	}
}

func TestDistribute_ModelLimit_NoLimitMap_Rejects(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		// ContextKeyTokenModelLimit intentionally NOT set → deny-all.
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when model limit enabled with no allowlist; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_ModelLimit_ModelNotAllowed_Rejects(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"gpt-3.5-turbo": true})
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for model outside allowlist; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_ShouldSelect_EmptyModel_Rejects(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := mountDistribute(nil)
	w := doDistribute(r, `{"model":""}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty model name; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_ShouldSelect_NoChannelAvailable(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	// Empty channel/ability tables → CacheGetRandomSatisfiedChannel fails.
	// The tenant-isolation safety net is that the request must never fall
	// through to relay — that holds for 404 just as much as for 503; with no
	// ability row ever configured for this (group, model), 404 is the
	// correct status (a 503 here would make a client retry forever).
	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := doDistribute(r, `{"model":"nonexistent-model"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when the model was never configured for this group; body=%s", w.Code, w.Body.String())
	}
}
