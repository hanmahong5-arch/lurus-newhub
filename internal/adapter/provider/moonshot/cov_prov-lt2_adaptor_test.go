package moonshot

// Business-acceptance tests for the Moonshot (Kimi) adaptor. Moonshot has no
// prior test coverage in this repo. It is mostly a thin routing/delegation
// layer over the shared claude/openai adaptors plus a URL-composition
// surface (kimi-coding-plan special base, Claude vs OpenAI format, and
// rerank/embeddings/completions/chat-completions dispatch) that is
// billing/protocol critical: get GetRequestURL wrong and traffic silently
// hits the wrong upstream endpoint.

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

func newProvLt2MoonshotCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL: multiple dispatch branches. Getting any of these wrong
// silently routes billed traffic to the wrong upstream path.
// ---------------------------------------------------------------------------

func TestMoonshot_GetRequestURL_SpecialBase_ClaudeFormat(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "kimi-coding-plan"},
		RelayFormat: types.RelayFormatClaude,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.kimi.com/coding/v1/messages"
	if got != want {
		t.Errorf("GetRequestURL = %q, want %q", got, want)
	}
}

func TestMoonshot_GetRequestURL_SpecialBase_OpenAIFormat(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "kimi-coding-plan"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.kimi.com/coding/v1/chat/completions"
	if got != want {
		t.Errorf("GetRequestURL = %q, want %q", got, want)
	}
}

func TestMoonshot_GetRequestURL_ClaudeFormat_RegularBase(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
		RelayFormat: types.RelayFormatClaude,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.moonshot.cn/anthropic/v1/messages"
	if got != want {
		t.Errorf("GetRequestURL = %q, want %q", got, want)
	}
}

func TestMoonshot_GetRequestURL_Rerank(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
		RelayMode:   relayconstant.RelayModeRerank,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.moonshot.cn/v1/rerank" {
		t.Errorf("GetRequestURL = %q, want rerank path", got)
	}
}

func TestMoonshot_GetRequestURL_Embeddings(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
		RelayMode:   relayconstant.RelayModeEmbeddings,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.moonshot.cn/v1/embeddings" {
		t.Errorf("GetRequestURL = %q, want embeddings path", got)
	}
}

func TestMoonshot_GetRequestURL_ChatCompletions(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.moonshot.cn/v1/chat/completions" {
		t.Errorf("GetRequestURL = %q, want chat completions path", got)
	}
}

func TestMoonshot_GetRequestURL_Completions(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
		RelayMode:   relayconstant.RelayModeCompletions,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.moonshot.cn/v1/completions" {
		t.Errorf("GetRequestURL = %q, want completions path", got)
	}
}

func TestMoonshot_GetRequestURL_UnknownRelayMode_DefaultsToChatCompletions(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
		RelayMode:   9999,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.moonshot.cn/v1/chat/completions" {
		t.Errorf("GetRequestURL = %q, want default chat completions path for an unrecognized relay mode", got)
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestMoonshot_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "kimi-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer kimi-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer kimi-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertGeminiRequest / ConvertAudioRequest / ConvertOpenAIResponsesRequest:
// documented "not implemented" surfaces. Must error, not panic.
// ---------------------------------------------------------------------------

func TestMoonshot_ConvertGeminiRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	if err == nil {
		t.Fatal("expected error, gemini format is not supported by moonshot")
	}
	if got != nil {
		t.Errorf("got = %v, want nil on error", got)
	}
}

func TestMoonshot_ConvertAudioRequest_NotSupported_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	if err == nil {
		t.Fatal("expected error, audio is not supported by moonshot")
	}
	if got != nil {
		t.Errorf("got = %v, want nil on error", got)
	}
}

func TestMoonshot_ConvertOpenAIResponsesRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	if err == nil {
		t.Fatal("expected error, /v1/responses is not implemented for moonshot")
	}
	if got != nil {
		t.Errorf("got = %v, want nil on error", got)
	}
}

// ---------------------------------------------------------------------------
// ConvertClaudeRequest: delegates to claude.Adaptor, which for the plain
// (non-conversion) path passes the request straight through unchanged.
// ---------------------------------------------------------------------------

func TestMoonshot_ConvertClaudeRequest_DelegatesPassthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.ClaudeRequest{
		Model:     "kimi-k2-turbo-preview",
		MaxTokens: 512,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	got, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.ClaudeRequest (claude.Adaptor.ConvertClaudeRequest is a passthrough)", got)
	}
	if out.Model != "kimi-k2-turbo-preview" || out.MaxTokens != 512 {
		t.Errorf("request fields not preserved by delegation: %+v", out)
	}
}

// ---------------------------------------------------------------------------
// ConvertImageRequest: delegates to openai.Adaptor.ConvertImageRequest.
// ---------------------------------------------------------------------------

func TestMoonshot_ConvertImageRequest_DelegatesPassthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayMode:   relayconstant.RelayModeImagesGenerations,
	}
	req := dto.ImageRequest{Model: "moonshot-image", Prompt: "a cat", N: 1}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(dto.ImageRequest)
	if !ok {
		t.Fatalf("result type = %T, want dto.ImageRequest (passthrough delegation)", got)
	}
	if out.Prompt != "a cat" || out.Model != "moonshot-image" {
		t.Errorf("request fields not preserved: %+v", out)
	}
}

// ---------------------------------------------------------------------------
// Init is a documented no-op; must not panic with real ChannelMeta.
// ---------------------------------------------------------------------------

func TestMoonshot_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"}}
	a.Init(info)
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest / ConvertRerankRequest / ConvertEmbeddingRequest:
// straight pass-through contracts.
// ---------------------------------------------------------------------------

func TestMoonshot_ConvertOpenAIRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "moonshot-v1-8k", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Errorf("ConvertOpenAIRequest must return the same request pointer unmodified")
	}
}

func TestMoonshot_ConvertRerankRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	req := dto.RerankRequest{Model: "moonshot-rerank", Query: "q"}
	got, err := a.ConvertRerankRequest(c, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(dto.RerankRequest)
	if !ok || out.Query != "q" {
		t.Errorf("got = %#v, want passthrough of the rerank request", got)
	}
}

func TestMoonshot_ConvertEmbeddingRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.EmbeddingRequest{Model: "moonshot-embed", Input: "hello"}
	got, err := a.ConvertEmbeddingRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(dto.EmbeddingRequest)
	if !ok || out.Model != "moonshot-embed" {
		t.Errorf("got = %#v, want passthrough of the embedding request", got)
	}
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest against the channel base URL.
// ---------------------------------------------------------------------------

func TestMoonshot_DoRequest_DelegatesToApiRequest(t *testing.T) {
	app.InitHttpClient()
	// Loopback destinations are blocked by the relay SSRF dial guard by
	// design; allow private IPs for this test only so it exercises the
	// delegation itself, not the (separately tested) dial guard.
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a := &Adaptor{}
	c, _ := newProvLt2MoonshotCtx()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: srv.URL,
			ApiKey:         "kimi-key",
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
	if gotAuth != "Bearer kimi-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer kimi-key", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("upstream saw path = %q, want /v1/chat/completions", gotPath)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: Claude-format (stream + non-stream) vs. default OpenAI dispatch.
// ---------------------------------------------------------------------------

func TestMoonshot_DoResponse_ClaudeFormat_NonStream(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		IsStream:    false,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"kimi-k2-turbo-preview","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected Claude format dispatched to *dto.Usage, got %T", usage)
	}
	if u.PromptTokens != 5 || u.CompletionTokens != 2 {
		t.Errorf("usage = %+v, want PromptTokens=5 CompletionTokens=2 (from Claude-style input_tokens/output_tokens)", u)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestMoonshot_DoResponse_ClaudeFormat_Stream(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2MoonshotCtx()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	stream := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"kimi-k2-turbo-preview\",\"content\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\n" +
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
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestMoonshot_DoResponse_DefaultFormat_DelegatesToOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2MoonshotCtx()
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

func TestMoonshot_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "moonshot" {
		t.Errorf("GetChannelName() = %q, want moonshot", a.GetChannelName())
	}
	list := a.GetModelList()
	if len(list) != 3 {
		t.Fatalf("GetModelList() len = %d, want 3", len(list))
	}
	if list[0] != "moonshot-v1-8k" {
		t.Errorf("GetModelList()[0] = %q, want moonshot-v1-8k", list[0])
	}
}
