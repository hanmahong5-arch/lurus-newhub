package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Adaptor.ConvertClaudeRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestGinContext()
	req := &dto.ClaudeRequest{Model: "claude-3-opus-20240229"}
	result, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := result.(*dto.ClaudeRequest)
	if !ok || got != req {
		t.Errorf("ConvertClaudeRequest should return the same request pointer unmodified, got %+v", result)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.Init
// ---------------------------------------------------------------------------

func TestAdaptor_Init_RequestMode(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"claude-2", RequestModeCompletion},
		{"claude-2.0", RequestModeCompletion},
		{"claude-2.1", RequestModeCompletion},
		{"claude-instant-1.2", RequestModeCompletion},
		{"claude-3-opus-20240229", RequestModeMessage},
		{"claude-3-5-sonnet-20241022", RequestModeMessage},
		{"claude-sonnet-4-20250514", RequestModeMessage},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			a := &Adaptor{}
			a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tt.model}})
			if a.RequestMode != tt.want {
				t.Errorf("RequestMode for %q = %d, want %d", tt.model, a.RequestMode, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	t.Run("message mode", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeMessage}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.anthropic.example"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://api.anthropic.example/v1/messages" {
			t.Errorf("url = %q, want %q", url, "https://api.anthropic.example/v1/messages")
		}
	})

	t.Run("completion mode", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeCompletion}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.anthropic.example"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://api.anthropic.example/v1/complete" {
			t.Errorf("url = %q, want %q", url, "https://api.anthropic.example/v1/complete")
		}
	})

	t.Run("beta query appended", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeMessage}
		info := &relaycommon.RelayInfo{
			ChannelMeta:       &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.anthropic.example"},
			IsClaudeBetaQuery: true,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://api.anthropic.example/v1/messages?beta=true" {
			t.Errorf("url = %q, want beta query suffix", url)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader / CommonClaudeHeadersOperation
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader_DefaultAnthropicVersion(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test-key"}}
	header := http.Header{}
	err := a.SetupRequestHeader(c, &header, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want default %q", header.Get("anthropic-version"), "2023-06-01")
	}
	if header.Get("x-api-key") != "sk-test-key" {
		t.Errorf("x-api-key = %q, want %q", header.Get("x-api-key"), "sk-test-key")
	}
}

func TestAdaptor_SetupRequestHeader_OverrideAnthropicVersion(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("anthropic-version", "2024-01-01")

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("anthropic-version") != "2024-01-01" {
		t.Errorf("anthropic-version = %q, want caller-provided %q", header.Get("anthropic-version"), "2024-01-01")
	}
}

func TestAdaptor_SetupRequestHeader_AnthropicBetaForwarded(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("anthropic-beta") != "prompt-caching-2024-07-31" {
		t.Errorf("anthropic-beta = %q, want forwarded value", header.Get("anthropic-beta"))
	}
}

func TestAdaptor_SetupRequestHeader_NoAnthropicBetaNotSet(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("anthropic-beta") != "" {
		t.Errorf("anthropic-beta = %q, want unset when caller did not send one", header.Get("anthropic-beta"))
	}
}

func TestCommonClaudeHeadersOperation_ModelSpecificHeadersWritten(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	settings := model_setting.GetClaudeSettings()
	original := settings.HeadersSettings
	settings.HeadersSettings = map[string]map[string][]string{
		"claude-3-opus-20240229": {"X-Custom-Header": {"custom-value"}},
	}
	defer func() { settings.HeadersSettings = original }()

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, OriginModelName: "claude-3-opus-20240229"}
	header := http.Header{}
	CommonClaudeHeadersOperation(c, &header, info)
	if header.Get("X-Custom-Header") != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", header.Get("X-Custom-Header"), "custom-value")
	}
}

func TestCommonClaudeHeadersOperation_UnknownModelNoHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	settings := model_setting.GetClaudeSettings()
	original := settings.HeadersSettings
	settings.HeadersSettings = map[string]map[string][]string{}
	defer func() { settings.HeadersSettings = original }()

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, OriginModelName: "claude-3-opus-20240229"}
	header := http.Header{}
	CommonClaudeHeadersOperation(c, &header, info)
	if len(header) != 0 {
		t.Errorf("header = %+v, want empty for a model with no configured headers", header)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestGinContext()
	_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error = %q, want to mention nil", err.Error())
	}
}

func TestAdaptor_ConvertOpenAIRequest_CompletionModeDispatch(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeCompletion}
	c := newTestGinContext()
	req := &dto.GeneralOpenAIRequest{
		Model:    "claude-2.1",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	claudeReq, ok := result.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.ClaudeRequest", result)
	}
	if !strings.Contains(claudeReq.Prompt, "Human: hi") {
		t.Errorf("Prompt = %q, want to contain 'Human: hi' (completion-mode conversion)", claudeReq.Prompt)
	}
}

func TestAdaptor_ConvertOpenAIRequest_MessageModeDispatch(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeMessage}
	c := newTestGinContext()
	req := &dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	claudeReq, ok := result.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.ClaudeRequest", result)
	}
	if len(claudeReq.Messages) != 1 || claudeReq.Messages[0].Role != "user" {
		t.Errorf("Messages = %+v, want single user message (message-mode conversion)", claudeReq.Messages)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertRerankRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertRerankRequest_AlwaysNil(t *testing.T) {
	a := &Adaptor{}
	c := newTestGinContext()
	result, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if result != nil {
		t.Errorf("result = %+v, want nil (Claude does not support rerank)", result)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelList(t *testing.T) {
	a := &Adaptor{}
	list := a.GetModelList()
	if len(list) == 0 {
		t.Fatal("model list should not be empty")
	}
	found := false
	for _, m := range list {
		if m == "claude-3-opus-20240229" {
			found = true
		}
	}
	if !found {
		t.Error("model list should contain claude-3-opus-20240229")
	}
	seen := map[string]bool{}
	for _, m := range list {
		if seen[m] {
			t.Errorf("model list contains duplicate entry %q", m)
		}
		seen[m] = true
	}
}

func TestAdaptor_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "claude" {
		t.Errorf("GetChannelName() = %q, want %q", got, "claude")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse dispatch (stream vs non-stream)
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_NonStreamDispatch(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	body := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":5,"output_tokens":3}}`
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	a := &Adaptor{RequestMode: RequestModeMessage}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage from non-stream dispatch")
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("usage type = %T, want *dto.Usage", usage)
	}
	if u.PromptTokens != 5 || u.CompletionTokens != 3 {
		t.Errorf("usage = %+v, want PromptTokens=5 CompletionTokens=3", u)
	}
}

// NOTE: DoResponse's IsStream=true branch (-> ClaudeStreamHandler ->
// helper.StreamScannerHandler) reads process-global constant.StreamingTimeout
// (seconds) to size a time.NewTicker. That constant is only populated by
// common.InitEnv() (full server bootstrap from env vars), which this package
// does not — and should not — invoke from a unit test (flag.Parse() +
// os.Exit/log.Fatal side effects). Left at its zero value it makes
// time.NewTicker panic ("non-positive interval"), so the streaming dispatch
// path is exercised indirectly instead, via the composable pieces
// (HandleStreamResponseData / FormatClaudeResponseInfo / HandleStreamFinalResponse)
// in cov_response_handler_test.go, which do not depend on that ticker.

// NOTE: Adaptor.DoRequest is a 1-line delegate to provider.DoApiRequest,
// which in turn calls an internal getHttpClient()-style helper that is only
// populated by the server's lifecycle bootstrap (see
// internal/adapter/provider/api_request.go: `client.Do(req)`). Outside that
// bootstrap the client is nil and `client.Do` nil-derefs. Exercising it here
// would require replicating that global bootstrap, which is out of scope for
// this package's unit tests (and risks fighting the real integration tests
// that already cover it) — skipped per task instructions rather than mocked.
