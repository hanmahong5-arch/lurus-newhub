package handler

// ============================================================================
// Business scope: upstream model discovery (internal/adapter/handler/channel.go)
//   - FetchUpstreamModels: pulls the live model list from an EXISTING channel's
//     configured vendor endpoint (per-vendor URL shape + auth headers + key
//     selection), used by the console to populate the model picker.
//   - FetchModels: the pre-save probe used while an admin is still filling out
//     the "add channel" form — base_url/key come straight from the request
//     body, so it is the read-SSRF surface: an attacker-controlled base_url
//     whose response gets reflected back to the caller.
//
// Edge cases covered per the acceptance brief: cross-tenant IDOR, base_url
// malformed/internal (SSRF), key empty (no enabled key), upstream 4xx/5xx,
// malformed upstream JSON, per-vendor URL shape (Gemini strips "models/").
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/gin-gonic/gin"
)

// handler_channel_getCtx builds a GET-style gin context with the given id
// path param and the v1 admin session identity.
func handler_channel_getCtx(id int, tenantID string, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(id)}}
	c.Set("tenant_id", tenantID)
	c.Set("id", userID)
	return c, w
}

func handler_channel_seedTypedChannel(t *testing.T, ctx *V2TestContext, chType int, baseURL, key string) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name:        "upstream-chan",
		TenantId:    ctx.TenantID,
		Key:         key,
		Status:      common.ChannelStatusEnabled,
		Type:        chType,
		Models:      "m1",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if baseURL != "" {
		ch.BaseURL = &baseURL
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed typed channel: %v", err)
	}
	return ch
}

// ─── FetchUpstreamModels ────────────────────────────────────────────────────

func TestFetchUpstreamModels_InvalidId(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Params = gin.Params{{Key: "id", Value: "not-a-number"}}
	FetchUpstreamModels(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure for non-numeric id, body=%s", w.Body.String())
	}
}

func TestFetchUpstreamModels_ChannelNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	c, w := handler_channel_getCtx(999999, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure for missing channel, body=%s", w.Body.String())
	}
}

func TestFetchUpstreamModels_CrossTenantIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	victim := &repo.Channel{
		Name: "victim", TenantId: "other-tenant-fum", Key: "sk-victim",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	c, w := handler_channel_getCtx(victim.Id, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestFetchUpstreamModels_NoEnabledKey exercises the "key selection fails"
// branch: a multi-key channel whose Key field is empty has no keys at all,
// so GetNextEnabledKey errors before any HTTP call is attempted.
func TestFetchUpstreamModels_NoEnabledKey(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOpenAI, "http://127.0.0.1:1", "")
	ch.ChannelInfo.IsMultiKey = true
	if err := ch.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, w := handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure for no enabled key, body=%s", w.Body.String())
	}
}

// TestFetchUpstreamModels_HeaderOverrideTypeMismatch exercises
// buildFetchModelsHeaders' error branch (a non-string header-override value)
// surfacing as a hard ApiError from the handler.
func TestFetchUpstreamModels_HeaderOverrideTypeMismatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOpenAI, "http://127.0.0.1:1", "sk-x")
	badOverride := `{"X-Custom":123}` // number, not string
	ch.HeaderOverride = &badOverride
	if err := ch.Update(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c, w := handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected failure for non-string header override, body=%s", w.Body.String())
	}
}

// TestFetchUpstreamModels_OllamaVendor covers the Ollama-specific branch
// (different response shape than the generic OpenAI-style vendors), both the
// success path and the upstream-error path, against a local mock server.
func TestFetchUpstreamModels_OllamaVendor(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected ollama path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3","size":123}]}`))
	}))
	defer okSrv.Close()

	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, okSrv.URL, "ollama-key")
	c, w := handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != true {
		t.Fatalf("expected ollama fetch success, body=%s", w.Body.String())
	}
	data, ok := body["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("expected 1 model in data, body=%s", w.Body.String())
	}
	first := data[0].(map[string]interface{})
	if first["id"] != "llama3" || first["owned_by"] != "ollama" {
		t.Errorf("unexpected ollama model shape: %v", first)
	}
	meta, _ := first["metadata"].(map[string]interface{})
	if meta == nil || meta["size"].(float64) != 123 {
		t.Errorf("expected metadata.size=123, got %v", first["metadata"])
	}

	// upstream error path
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	ch2 := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, errSrv.URL, "ollama-key")
	c, w = handler_channel_getCtx(ch2.Id, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	body = handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected ollama fetch failure on 500, body=%s", w.Body.String())
	}
}

// TestFetchUpstreamModels_GeminiStripsModelPrefix covers the Gemini-specific
// URL shape and the "models/" id-prefix stripping that only the Gemini branch
// performs.
func TestFetchUpstreamModels_GeminiStripsModelPrefix(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/openai/models" {
			t.Errorf("unexpected gemini path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"models/gemini-pro"},{"id":"models/gemini-flash"}],"success":true}`))
	}))
	defer srv.Close()

	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeGemini, srv.URL, "gk")
	c, w := handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != true {
		t.Fatalf("expected gemini fetch success, body=%s", w.Body.String())
	}
	ids, ok := body["data"].([]interface{})
	if !ok || len(ids) != 2 {
		t.Fatalf("expected 2 model ids, body=%s", w.Body.String())
	}
	if ids[0] != "gemini-pro" || ids[1] != "gemini-flash" {
		t.Errorf("expected 'models/' prefix stripped, got %v", ids)
	}
}

// TestFetchUpstreamModels_VendorURLShapes exercises the remaining per-vendor
// switch branches (Ali / Zhipu_v4 / VolcEngine / Moonshot, none matching a
// ChannelSpecialBases entry) to prove each builds its own documented URL
// shape rather than silently falling through to the generic "/v1/models".
func TestFetchUpstreamModels_VendorURLShapes(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	cases := []struct {
		name     string
		chType   int
		wantPath string
	}{
		{"ali", constant.ChannelTypeAli, "/compatible-mode/v1/models"},
		{"zhipu_v4-no-special-base", constant.ChannelTypeZhipu_v4, "/api/paas/v4/models"},
		{"volcengine-no-special-base", constant.ChannelTypeVolcEngine, "/v1/models"},
		{"moonshot-no-special-base", constant.ChannelTypeMoonshot, "/v1/models"},
		{"generic-default", constant.ChannelTypeOpenAI, "/v1/models"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"m1"}],"success":true}`))
			}))
			defer srv.Close()

			ch := handler_channel_seedTypedChannel(t, ctx, tc.chType, srv.URL, "k-"+tc.name)
			c, w := handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
			FetchUpstreamModels(c)
			body := handler_channel_mkBody(t, w)
			if body["success"] != true {
				t.Fatalf("%s: expected success, body=%s", tc.name, w.Body.String())
			}
			if gotPath != tc.wantPath {
				t.Errorf("%s: expected upstream path %q, got %q", tc.name, tc.wantPath, gotPath)
			}
		})
	}
}

// TestFetchUpstreamModels_MalformedUpstreamJSON covers the response-decode
// error branch: the upstream returns 200 but a body that isn't the expected
// JSON shape.
func TestFetchUpstreamModels_MalformedUpstreamJSON(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()
	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOpenAI, srv.URL, "k")
	c, w := handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected decode failure surfaced as success=false, body=%s", w.Body.String())
	}
}

// TestFetchUpstreamModels_UpstreamNon200 covers GetResponseBody's non-200
// status-code branch propagating as a handler-level error.
func TestFetchUpstreamModels_UpstreamNon200(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOpenAI, srv.URL, "k")
	c, w := handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
	FetchUpstreamModels(c)
	body := handler_channel_mkBody(t, w)
	if body["success"] != false {
		t.Fatalf("expected upstream 401 to surface as failure, body=%s", w.Body.String())
	}
}

// ─── FetchModels ────────────────────────────────────────────────────────────

// handler_channel_withFetchSetting temporarily overrides the global SSRF
// fetch setting and restores it after the test.
func handler_channel_withFetchSetting(t *testing.T, mutate func(*system_setting.FetchSetting)) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	prev := *fs
	mutate(fs)
	t.Cleanup(func() { *fs = prev })
}

func TestFetchModels_MalformedBody(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{not json"))
	c.Request.Header.Set("Content-Type", "application/json")
	FetchModels(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", w.Code)
	}
}

// TestFetchModels_RejectsInternalBaseURL is the read-SSRF regression guard:
// base_url is attacker-controlled request data (no channel exists yet), so
// an internal address must be rejected before any outbound call is made.
func TestFetchModels_RejectsInternalBaseURL(t *testing.T) {
	handler_channel_withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"base_url":"http://169.254.169.254","type":1,"key":"k"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	FetchModels(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for internal base_url, got %d body=%s", w.Code, w.Body.String())
	}
	body := handler_channel_mkBody(t, w)
	msg, _ := body["message"].(string)
	if msg == "" {
		t.Fatalf("expected a rejection message, body=%s", w.Body.String())
	}
}

// TestFetchModels_Ollama covers the Ollama branch's success and failure paths.
func TestFetchModels_Ollama(t *testing.T) {
	handler_channel_withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = false // local httptest server is a loopback address
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"phi3"}]}`))
	}))
	defer srv.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"base_url":"` + srv.URL + `","type":` + strconv.Itoa(constant.ChannelTypeOllama) + `,"key":"k\n"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	FetchModels(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected ollama fetch-models success, body=%s", w.Body.String())
	}
	data, _ := resp["data"].([]interface{})
	if len(data) != 1 || data[0] != "phi3" {
		t.Fatalf("expected ['phi3'], got %v", resp["data"])
	}
}

// TestFetchModels_GenericSuccess covers the default (non-Ollama) branch's
// full happy path: builds "/v1/models", decodes {"data":[{"id":...}]}.
func TestFetchModels_GenericSuccess(t *testing.T) {
	handler_channel_withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = false
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("expected bearer auth, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`))
	}))
	defer srv.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"base_url":"` + srv.URL + `","type":1,"key":"sk-test"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	FetchModels(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success, body=%s", w.Body.String())
	}
	data, _ := resp["data"].([]interface{})
	if len(data) != 2 || data[0] != "gpt-4" || data[1] != "gpt-3.5-turbo" {
		t.Fatalf("unexpected models list: %v", resp["data"])
	}
}

// TestFetchModels_UpstreamNon200 covers the "response.StatusCode != 200"
// branch which returns 500 with a generic message (no upstream body leak).
func TestFetchModels_UpstreamNon200(t *testing.T) {
	handler_channel_withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = false
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"base_url":"` + srv.URL + `","type":1,"key":"k"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	FetchModels(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on upstream non-200, got %d", w.Code)
	}
	resp := handler_channel_mkBody(t, w)
	if resp["message"] != "Failed to fetch models" {
		t.Errorf("expected generic failure message, got %v", resp["message"])
	}
}

// TestFetchModels_MalformedUpstreamJSON covers the decode-error branch.
func TestFetchModels_MalformedUpstreamJSON(t *testing.T) {
	handler_channel_withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = false
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<<not json>>"))
	}))
	defer srv.Close()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"base_url":"` + srv.URL + `","type":1,"key":"k"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	FetchModels(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for malformed upstream JSON, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestFetchModels_EmptyBaseURLFallsThroughToDialError covers the case where
// base_url is omitted (defaults from ChannelBaseURLs[type], empty for an
// unrecognized type) — the outbound request then fails at the HTTP client
// level rather than at URL construction, and must surface as 500 without a
// panic.
func TestFetchModels_EmptyBaseURLFallsThroughToDialError(t *testing.T) {
	handler_channel_withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// type 999999 is not a known vendor → ChannelBaseURLs[999999] is out of
	// range for the slice, but base_url is explicitly empty so the code path
	// under test is req.BaseURL == "" plus a type with no configured default.
	body := `{"base_url":"","type":0,"key":"k"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	FetchModels(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for empty/invalid base_url, got %d body=%s", w.Code, w.Body.String())
	}
}
