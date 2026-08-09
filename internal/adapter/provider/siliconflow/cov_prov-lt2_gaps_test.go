package siliconflow

// Business-acceptance tests for the remaining SiliconFlow adaptor surfaces
// that just delegate straight through to the shared OpenAI adaptor
// (ConvertClaudeRequest, ConvertAudioRequest, DoRequest, Init) plus the
// rerank branch of DoResponse dispatch. These delegations are billing/
// protocol-critical: if the delegation wiring breaks silently, Claude-style
// and audio traffic routed through a SiliconFlow channel would either
// crash or drop fields instead of translating correctly.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// ConvertClaudeRequest: delegates to openai.Adaptor.ConvertClaudeRequest,
// which converts a Claude Messages-API request into the internal OpenAI
// request shape. Verify the translated model/messages survive the trip.
// ---------------------------------------------------------------------------

func TestSF_ConvertClaudeRequest_DelegatesAndTranslates(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.ClaudeRequest{
		Model:     "deepseek-ai/DeepSeek-V2.5",
		MaxTokens: 256,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello there"},
		},
	}
	got, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest", got)
	}
	if out.Model != "deepseek-ai/DeepSeek-V2.5" {
		t.Errorf("Model = %q, want passthrough of claude request model", out.Model)
	}
	if out.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256 (billing-relevant cap)", out.MaxTokens)
	}
	if len(out.Messages) != 1 || out.Messages[0].Role != "user" {
		t.Fatalf("Messages not translated correctly: %+v", out.Messages)
	}
	if got := out.Messages[0].StringContent(); got != "hello there" {
		t.Errorf("message content = %q, want %q", got, "hello there")
	}
}

// ---------------------------------------------------------------------------
// ConvertAudioRequest: delegates to openai.Adaptor.ConvertAudioRequest,
// which extracts the uploaded file bytes from a multipart request.
// ---------------------------------------------------------------------------

func TestSF_ConvertAudioRequest_MissingFile_Errors(t *testing.T) {
	a := &Adaptor{}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	if err == nil {
		t.Fatal("expected error when multipart request carries no 'file' field")
	}
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to openai.Adaptor.DoRequest which (for the default
// relay mode, as used by SiliconFlow chat/rerank/embeddings) routes through
// provider.DoApiRequest against the channel base URL.
// ---------------------------------------------------------------------------

func TestSF_DoRequest_DelegatesToOpenAIApiRequest(t *testing.T) {
	app.InitHttpClient()
	a := &Adaptor{}

	// Loopback destinations are blocked by the relay SSRF dial guard by
	// design; allow private IPs for this test only so it exercises the
	// delegation itself, not the (separately tested) dial guard.
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, _ := newProvLongtailSFCtx()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeSiliconFlow,
			ChannelBaseUrl: srv.URL,
			ApiKey:         "sf-key",
		},
		RequestURLPath: "/v1/chat/completions",
		RelayMode:      relayconstant.RelayModeChatCompletions,
	}
	result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("result type = %T, want *http.Response", result)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotAuth != "Bearer sf-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer sf-key (SetupRequestHeader must run)", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// Init is a no-op but must not panic when called with real ChannelMeta.
// ---------------------------------------------------------------------------

func TestSF_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.siliconflow.cn"}}
	a.Init(info) // must not panic
}

// ---------------------------------------------------------------------------
// DoResponse: rerank relay mode routes to siliconflowRerankHandler, not the
// shared openai adaptor. Exercise the dispatch end-to-end.
// ---------------------------------------------------------------------------

func TestSF_DoResponse_RerankMode_RoutesToRerankHandler(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"results":[{"index":0,"relevance_score":0.42}],"meta":{"tokens":{"input_tokens":10,"output_tokens":1}}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	sfUsage, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected rerank mode delegated to siliconflowRerankHandler (*dto.Usage), got %T", usage)
	}
	if sfUsage.TotalTokens != 11 {
		t.Errorf("TotalTokens = %d, want 11 (billing amount from meta.tokens)", sfUsage.TotalTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
