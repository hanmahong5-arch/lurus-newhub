package deepseek

// Business-acceptance tests for the DeepSeek adaptor. No prior coverage in
// this repo. DeepSeek is mostly delegation over claude/openai adaptors, plus
// a URL-composition surface: the FIM (fill-in-the-middle) /beta suffix logic
// is billing/protocol critical — get it wrong and completions requests
// silently hit the wrong upstream path (or double-append "/beta/beta").

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func init() {
	// ClaudeStreamHandler delegates to helper.StreamScannerHandler, which
	// builds a time.NewTicker(StreamingTimeout*time.Second); a zero/unset
	// value panics ("non-positive interval"), so ensure a safe default.
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
}

func newProvLt2DeepseekCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestDeepseek_GetRequestURL_ClaudeFormat(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com"},
		RelayFormat: types.RelayFormatClaude,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.deepseek.com/anthropic/v1/messages" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.deepseek.com/anthropic/v1/messages")
	}
}

func TestDeepseek_GetRequestURL_ChatCompletions_DefaultBase(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.deepseek.com/v1/chat/completions" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.deepseek.com/v1/chat/completions")
	}
}

func TestDeepseek_GetRequestURL_FIMCompletions_AppendsBetaSuffix(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com"},
		RelayMode:   relayconstant.RelayModeCompletions,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.deepseek.com/beta/completions" {
		t.Errorf("GetRequestURL = %q, want %q (FIM requires the /beta prefix)", got, "https://api.deepseek.com/beta/completions")
	}
}

func TestDeepseek_GetRequestURL_FIMCompletions_BaseAlreadyHasBetaSuffix_NotDoubled(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com/beta"},
		RelayMode:   relayconstant.RelayModeCompletions,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.deepseek.com/beta/completions" {
		t.Errorf("GetRequestURL = %q, want %q (must not double-append /beta)", got, "https://api.deepseek.com/beta/completions")
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestDeepseek_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "ds-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer ds-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer ds-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertGeminiRequest / ConvertAudioRequest / ConvertImageRequest /
// ConvertEmbeddingRequest / ConvertOpenAIResponsesRequest: documented
// "not implemented" surfaces. Must error, not panic.
// ---------------------------------------------------------------------------

func TestDeepseek_ConvertGeminiRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{}); err == nil {
		t.Fatal("expected error, gemini format is not supported by deepseek")
	}
}

func TestDeepseek_ConvertAudioRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{}); err == nil {
		t.Fatal("expected error, audio is not implemented for deepseek")
	}
}

func TestDeepseek_ConvertImageRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertImageRequest(c, info, dto.ImageRequest{}); err == nil {
		t.Fatal("expected error, images are not implemented for deepseek")
	}
}

func TestDeepseek_ConvertEmbeddingRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{}); err == nil {
		t.Fatal("expected error, embeddings are not implemented for deepseek")
	}
}

func TestDeepseek_ConvertOpenAIResponsesRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Fatal("expected error, /v1/responses is not implemented for deepseek")
	}
}

// ---------------------------------------------------------------------------
// ConvertClaudeRequest: delegates to claude.Adaptor, a passthrough.
// ---------------------------------------------------------------------------

func TestDeepseek_ConvertClaudeRequest_DelegatesPassthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.ClaudeRequest{Model: "deepseek-chat", MaxTokens: 128, Messages: []dto.ClaudeMessage{{Role: "user", Content: "hi"}}}
	got, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.ClaudeRequest (delegation passthrough)", got)
	}
	if out.Model != "deepseek-chat" || out.MaxTokens != 128 {
		t.Errorf("request fields not preserved by delegation: %+v", out)
	}
}

// ---------------------------------------------------------------------------
// Init is a documented no-op; must not panic with real ChannelMeta.
// ---------------------------------------------------------------------------

func TestDeepseek_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com"}}
	a.Init(info)
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: passthrough, but a nil request must error (not panic).
// ---------------------------------------------------------------------------

func TestDeepseek_ConvertOpenAIRequest_NilRequest_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestDeepseek_ConvertOpenAIRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "deepseek-reasoner", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Errorf("ConvertOpenAIRequest must return the same request pointer unmodified")
	}
}

// ---------------------------------------------------------------------------
// ConvertRerankRequest: documented (nil, nil) surface.
// ---------------------------------------------------------------------------

func TestDeepseek_ConvertRerankRequest_UnsupportedNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{Query: "q"})
	if err != nil || got != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil) — deepseek has no rerank support", got, err)
	}
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest against the channel base URL.
// ---------------------------------------------------------------------------

func TestDeepseek_DoRequest_DelegatesToApiRequest(t *testing.T) {
	app.InitHttpClient()
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

	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "ds-key"},
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
	if gotAuth != "Bearer ds-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer ds-key", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: Claude-format (stream + non-stream) vs. default OpenAI dispatch.
// ---------------------------------------------------------------------------

func TestDeepseek_DoResponse_ClaudeFormat_NonStream(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		IsStream:    false,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"deepseek-chat","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":6,"output_tokens":3}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected Claude format dispatched to *dto.Usage, got %T", usage)
	}
	if u.PromptTokens != 6 || u.CompletionTokens != 3 {
		t.Errorf("usage = %+v, want PromptTokens=6 CompletionTokens=3", u)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDeepseek_DoResponse_ClaudeFormat_Stream(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	stream := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-chat\",\"content\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(stream)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage from streamed Claude response, got %T", usage)
	}
	if u.PromptTokens != 4 {
		t.Errorf("PromptTokens = %d, want 4 (from message_start input_tokens)", u.PromptTokens)
	}
}

func TestDeepseek_DoResponse_DefaultFormat_DelegatesToOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2DeepseekCtx()
	info := &relaycommon.RelayInfo{IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected chat completions delegated to shared openai adaptor (*dto.Usage), got %T", usage)
	}
	if u.TotalTokens != 4 {
		t.Errorf("TotalTokens = %d, want 4 (billing amount from upstream usage)", u.TotalTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestDeepseek_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "deepseek" {
		t.Errorf("GetChannelName() = %q, want deepseek", a.GetChannelName())
	}
	list := a.GetModelList()
	if len(list) != 2 {
		t.Fatalf("GetModelList() len = %d, want 2", len(list))
	}
}
