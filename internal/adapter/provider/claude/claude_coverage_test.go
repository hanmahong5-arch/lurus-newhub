package claude

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// newClaudeTestContext builds a hermetic gin.Context backed by an in-memory
// ResponseRecorder — no network/socket involved.
func newClaudeTestContext() (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return w, c
}

// pngDataURL encodes a solid w×h PNG as a data URL, for hermetically
// exercising the non-http (base64) image branch of RequestOpenAI2ClaudeMessage.
func pngDataURL(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png %dx%d: %v", w, h, err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptorGetRequestURL(t *testing.T) {
	tests := []struct {
		name        string
		requestMode int
		beta        bool
		wantURL     string
	}{
		{"message mode, no beta", RequestModeMessage, false, "https://api.anthropic.com/v1/messages"},
		{"message mode, beta query", RequestModeMessage, true, "https://api.anthropic.com/v1/messages?beta=true"},
		{"completion mode, no beta", RequestModeCompletion, false, "https://api.anthropic.com/v1/complete"},
		{"completion mode, beta query", RequestModeCompletion, true, "https://api.anthropic.com/v1/complete?beta=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{RequestMode: tt.requestMode}
			info := &relaycommon.RelayInfo{
				ChannelMeta:       &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.anthropic.com"},
				IsClaudeBetaQuery: tt.beta,
			}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantURL {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adaptor.Init
// ---------------------------------------------------------------------------

func TestAdaptorInit(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		wantMode int
	}{
		{"claude-2 prefix -> completion mode", "claude-2.1", RequestModeCompletion},
		{"claude-instant prefix -> completion mode", "claude-instant-1.2", RequestModeCompletion},
		{"claude-3 model -> message mode", "claude-3-opus-20240229", RequestModeMessage},
		{"claude-sonnet-4 model -> message mode", "claude-sonnet-4-20250514", RequestModeMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{}
			a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tt.model}})
			if a.RequestMode != tt.wantMode {
				t.Errorf("RequestMode = %d, want %d", a.RequestMode, tt.wantMode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader / CommonClaudeHeadersOperation
// ---------------------------------------------------------------------------

func TestAdaptorSetupRequestHeader(t *testing.T) {
	t.Run("default anthropic-version when request header absent", func(t *testing.T) {
		_, c := newClaudeTestContext()
		header := http.Header{}
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test-key"}}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("x-api-key"); got != "sk-test-key" {
			t.Errorf("x-api-key = %q, want %q", got, "sk-test-key")
		}
		if got := header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want %q", got, "2023-06-01")
		}
	})

	t.Run("caller-supplied anthropic-version is preserved", func(t *testing.T) {
		_, c := newClaudeTestContext()
		c.Request.Header.Set("anthropic-version", "2024-01-01")
		header := http.Header{}
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("anthropic-version"); got != "2024-01-01" {
			t.Errorf("anthropic-version = %q, want %q", got, "2024-01-01")
		}
	})

	t.Run("anthropic-beta header is forwarded", func(t *testing.T) {
		_, c := newClaudeTestContext()
		c.Request.Header.Set("anthropic-beta", "tools-2024-04-04")
		header := http.Header{}
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("anthropic-beta"); got != "tools-2024-04-04" {
			t.Errorf("anthropic-beta = %q, want %q", got, "tools-2024-04-04")
		}
	})

	t.Run("absent anthropic-beta is not set", func(t *testing.T) {
		_, c := newClaudeTestContext()
		header := http.Header{}
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := header["Anthropic-Beta"]; ok {
			t.Errorf("anthropic-beta header should be absent, got %v", header["Anthropic-Beta"])
		}
	})

	t.Run("model-specific extra headers from settings are applied", func(t *testing.T) {
		settings := model_setting.GetClaudeSettings()
		orig := settings.HeadersSettings
		settings.HeadersSettings = map[string]map[string][]string{
			"claude-3-opus-20240229": {"x-extra": {"v1", "v2"}},
		}
		defer func() { settings.HeadersSettings = orig }()

		_, c := newClaudeTestContext()
		header := http.Header{}
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{},
			OriginModelName: "claude-3-opus-20240229",
		}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := header.Values("x-extra")
		if len(got) != 2 || got[0] != "v1" || got[1] != "v2" {
			t.Errorf("x-extra values = %v, want [v1 v2]", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestAdaptorConvertOpenAIRequest(t *testing.T) {
	t.Run("nil request returns error", func(t *testing.T) {
		a := &Adaptor{}
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if err == nil || err.Error() != "request is nil" {
			t.Fatalf("error = %v, want %q", err, "request is nil")
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
	})

	t.Run("completion mode delegates to RequestOpenAI2ClaudeComplete", func(t *testing.T) {
		a := &Adaptor{RequestMode: RequestModeCompletion}
		req := &dto.GeneralOpenAIRequest{
			Model:    "claude-2.1",
			Messages: []dto.Message{{Role: "user", Content: "Hi"}},
		}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		claudeReq, ok := got.(*dto.ClaudeRequest)
		if !ok {
			t.Fatalf("expected *dto.ClaudeRequest, got %T", got)
		}
		if claudeReq.MaxTokensToSample != 4096 {
			t.Errorf("MaxTokensToSample = %d, want 4096", claudeReq.MaxTokensToSample)
		}
	})

	t.Run("message mode delegates to RequestOpenAI2ClaudeMessage", func(t *testing.T) {
		_, c := newClaudeTestContext()
		a := &Adaptor{RequestMode: RequestModeMessage}
		req := &dto.GeneralOpenAIRequest{
			Model:    "claude-3-opus-20240229",
			Messages: []dto.Message{{Role: "user", Content: "Hi"}},
		}
		got, err := a.ConvertOpenAIRequest(c, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		claudeReq, ok := got.(*dto.ClaudeRequest)
		if !ok {
			t.Fatalf("expected *dto.ClaudeRequest, got %T", got)
		}
		if claudeReq.Model != "claude-3-opus-20240229" {
			t.Errorf("Model = %q, want %q", claudeReq.Model, "claude-3-opus-20240229")
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor misc: ConvertRerankRequest, ConvertClaudeRequest, GetModelList,
// GetChannelName, DoRequest passthrough shape
// ---------------------------------------------------------------------------

func TestAdaptorConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestAdaptorConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	req := &dto.ClaudeRequest{Model: "claude-3-opus-20240229"}
	got, err := a.ConvertClaudeRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("expected *dto.ClaudeRequest, got %T", got)
	}
	if gotReq != req {
		t.Errorf("expected same pointer to be returned unchanged")
	}
}

func TestAdaptorGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Fatalf("len(GetModelList()) = %d, want %d", len(got), len(ModelList))
	}
	for i, m := range ModelList {
		if got[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], m)
		}
	}
	if name := a.GetChannelName(); name != "claude" {
		t.Errorf("GetChannelName() = %q, want %q", name, "claude")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse — exercises ClaudeHandler / ClaudeStreamHandler against
// in-memory http.Response bodies. Hermetic: no live upstream is dialed,
// StreamScannerHandler/IOCopyBytesGracefully only require an io.Reader body
// and a gin.Context writer.
// ---------------------------------------------------------------------------

func TestAdaptorDoResponseNonStream(t *testing.T) {
	body := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"hi there"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":5,"output_tokens":3}}`
	w, c := newClaudeTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatOpenAI,
	}

	a := &Adaptor{RequestMode: RequestModeMessage}
	usageAny, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	usage, ok := usageAny.(*dto.Usage)
	if !ok || usage == nil {
		t.Fatalf("expected non-nil *dto.Usage, got %T %v", usageAny, usageAny)
	}
	if usage.PromptTokens != 5 || usage.CompletionTokens != 3 {
		t.Errorf("usage = %+v, want prompt=5 completion=3", usage)
	}
	if w.Code != http.StatusOK {
		t.Errorf("recorded status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hi there") {
		t.Errorf("recorded body = %q, want it to contain %q", w.Body.String(), "hi there")
	}
}

func TestAdaptorDoResponseStream(t *testing.T) {
	origTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = origTimeout }()

	sse := "" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_2\",\"model\":\"claude-3-opus-20240229\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	w, c := newClaudeTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{},
		RelayFormat:        types.RelayFormatOpenAI,
		IsStream:           true,
		DisablePing:        true,
		ShouldIncludeUsage: true,
	}

	a := &Adaptor{RequestMode: RequestModeMessage}
	usageAny, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	usage, ok := usageAny.(*dto.Usage)
	if !ok || usage == nil {
		t.Fatalf("expected non-nil *dto.Usage, got %T %v", usageAny, usageAny)
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v, want prompt=10 completion=2", usage)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Hello") {
		t.Errorf("streamed body = %q, want it to contain %q", body, "Hello")
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("streamed body = %q, want it to contain the final [DONE] marker", body)
	}
}

func TestAdaptorDoResponseStreamClaudeFormatPassthrough(t *testing.T) {
	origTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = origTimeout }()

	sse := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_3\",\"model\":\"claude-3-opus-20240229\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"

	w, c := newClaudeTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatClaude,
		IsStream:    true,
		DisablePing: true,
	}

	a := &Adaptor{RequestMode: RequestModeMessage}
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if !strings.Contains(w.Body.String(), "message_start") {
		t.Errorf("expected raw claude event to be forwarded, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// RequestOpenAI2ClaudeMessage — the largest untested surface in this package.
// ---------------------------------------------------------------------------

func TestRequestOpenAI2ClaudeMessage_Basic(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 512,
		Messages: []dto.Message{
			{Role: "system", Content: "Be nice."},
			{Role: "user", Content: "Hello"},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "claude-3-opus-20240229" {
		t.Errorf("Model = %q, want %q", got.Model, "claude-3-opus-20240229")
	}
	if got.MaxTokens != 512 {
		t.Errorf("MaxTokens = %d, want 512", got.MaxTokens)
	}
	sys, ok := got.System.([]dto.ClaudeMediaMessage)
	if !ok || len(sys) != 1 || sys[0].GetText() != "Be nice." {
		t.Fatalf("System = %#v, want single text block %q", got.System, "Be nice.")
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Content != "Hello" {
		t.Errorf("Messages[0] = %+v, want user/Hello", got.Messages[0])
	}
}

func TestRequestOpenAI2ClaudeMessage_DefaultMaxTokensFromSettings(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-3-opus-20240229",
		Messages: []dto.Message{{Role: "user", Content: "Hi"}},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d, want default 8192", got.MaxTokens)
	}
}

func TestRequestOpenAI2ClaudeMessage_MaxCompletionTokensTakesPrecedence(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:               "claude-3-opus-20240229",
		MaxTokens:           100,
		MaxCompletionTokens: 250,
		Messages:            []dto.Message{{Role: "user", Content: "Hi"}},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MaxTokens != 250 {
		t.Errorf("MaxTokens = %d, want 250 (MaxCompletionTokens wins)", got.MaxTokens)
	}
}

func TestRequestOpenAI2ClaudeMessage_Tools(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 100,
		Messages:  []dto.Message{{Role: "user", Content: "weather?"}},
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        "get_weather",
					Description: "gets weather",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{"type": "string"},
						},
						"required": []any{"city"},
						"extra":    "should carry through",
					},
				},
			},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools, ok := got.Tools.([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("Tools = %#v, want single tool", got.Tools)
	}
	claudeTool, ok := tools[0].(*dto.Tool)
	if !ok {
		t.Fatalf("Tools[0] type = %T, want *dto.Tool", tools[0])
	}
	if claudeTool.Name != "get_weather" || claudeTool.Description != "gets weather" {
		t.Errorf("tool = %+v, want name=get_weather description='gets weather'", claudeTool)
	}
	if claudeTool.InputSchema["type"] != "object" {
		t.Errorf("InputSchema[type] = %v, want object", claudeTool.InputSchema["type"])
	}
	if claudeTool.InputSchema["extra"] != "should carry through" {
		t.Errorf("InputSchema[extra] = %v, want passthrough value", claudeTool.InputSchema["extra"])
	}
}

func TestRequestOpenAI2ClaudeMessage_WebSearch(t *testing.T) {
	tests := []struct {
		name        string
		contextSize string
		wantMaxUses int
	}{
		{"low context size", "low", WebSearchMaxUsesLow},
		{"medium context size", "medium", WebSearchMaxUsesMedium},
		{"high context size", "high", WebSearchMaxUsesHigh},
		{"unrecognized context size falls through to zero", "extreme", 0},
		{"empty context size is left unset", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newClaudeTestContext()
			req := dto.GeneralOpenAIRequest{
				Model:     "claude-3-opus-20240229",
				MaxTokens: 100,
				Messages:  []dto.Message{{Role: "user", Content: "search this"}},
				WebSearchOptions: &dto.WebSearchOptions{
					SearchContextSize: tt.contextSize,
					UserLocation:      json.RawMessage(`{"approximate":{"timezone":"America/Los_Angeles","country":"US","region":"CA","city":"SF"}}`),
				},
			}
			got, err := RequestOpenAI2ClaudeMessage(c, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tools, ok := got.Tools.([]any)
			if !ok || len(tools) != 1 {
				t.Fatalf("Tools = %#v, want single web-search tool", got.Tools)
			}
			webTool, ok := tools[0].(*dto.ClaudeWebSearchTool)
			if !ok {
				t.Fatalf("Tools[0] type = %T, want *dto.ClaudeWebSearchTool", tools[0])
			}
			if webTool.Type != "web_search_20250305" || webTool.Name != "web_search" {
				t.Errorf("webTool = %+v, unexpected type/name", webTool)
			}
			if webTool.MaxUses != tt.wantMaxUses {
				t.Errorf("MaxUses = %d, want %d", webTool.MaxUses, tt.wantMaxUses)
			}
			if webTool.UserLocation == nil {
				t.Fatal("UserLocation should not be nil")
			}
			if webTool.UserLocation.Type != "approximate" || webTool.UserLocation.Timezone != "America/Los_Angeles" ||
				webTool.UserLocation.Country != "US" || webTool.UserLocation.Region != "CA" || webTool.UserLocation.City != "SF" {
				t.Errorf("UserLocation = %+v, unexpected fields", webTool.UserLocation)
			}
		})
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolChoiceAndParallel(t *testing.T) {
	_, c := newClaudeTestContext()
	parallel := true
	req := dto.GeneralOpenAIRequest{
		Model:            "claude-3-opus-20240229",
		MaxTokens:        100,
		Messages:         []dto.Message{{Role: "user", Content: "Hi"}},
		ToolChoice:       "auto",
		ParallelTooCalls: &parallel,
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ToolChoice == nil {
		t.Fatal("ToolChoice should not be nil")
	}
	tc, ok := got.ToolChoice.(*dto.ClaudeToolChoice)
	if !ok {
		t.Fatalf("ToolChoice type = %T, want *dto.ClaudeToolChoice", got.ToolChoice)
	}
	if tc.Type != "auto" || tc.DisableParallelToolUse != false {
		t.Errorf("ToolChoice = %+v, want type=auto disable=false", tc)
	}
}

func TestRequestOpenAI2ClaudeMessage_Stop(t *testing.T) {
	t.Run("string stop", func(t *testing.T) {
		_, c := newClaudeTestContext()
		req := dto.GeneralOpenAIRequest{
			Model:     "claude-3-opus-20240229",
			MaxTokens: 100,
			Messages:  []dto.Message{{Role: "user", Content: "Hi"}},
			Stop:      "STOP_HERE",
		}
		got, err := RequestOpenAI2ClaudeMessage(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.StopSequences) != 1 || got.StopSequences[0] != "STOP_HERE" {
			t.Errorf("StopSequences = %v, want [STOP_HERE]", got.StopSequences)
		}
	})

	t.Run("array stop", func(t *testing.T) {
		_, c := newClaudeTestContext()
		req := dto.GeneralOpenAIRequest{
			Model:     "claude-3-opus-20240229",
			MaxTokens: 100,
			Messages:  []dto.Message{{Role: "user", Content: "Hi"}},
			Stop:      []interface{}{"A", "B"},
		}
		got, err := RequestOpenAI2ClaudeMessage(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.StopSequences) != 2 || got.StopSequences[0] != "A" || got.StopSequences[1] != "B" {
			t.Errorf("StopSequences = %v, want [A B]", got.StopSequences)
		}
	})
}

func TestRequestOpenAI2ClaudeMessage_ThinkingSuffix(t *testing.T) {
	t.Run("small max tokens is bumped to 1280 floor", func(t *testing.T) {
		_, c := newClaudeTestContext()
		req := dto.GeneralOpenAIRequest{
			Model:     "claude-sonnet-4-20250514-thinking",
			MaxTokens: 100,
			Messages:  []dto.Message{{Role: "user", Content: "Hi"}},
		}
		got, err := RequestOpenAI2ClaudeMessage(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Model != "claude-sonnet-4-20250514" {
			t.Errorf("Model = %q, want -thinking suffix trimmed", got.Model)
		}
		if got.MaxTokens != 1280 {
			t.Errorf("MaxTokens = %d, want floor 1280", got.MaxTokens)
		}
		if got.Thinking == nil || got.Thinking.GetBudgetTokens() != 1024 {
			t.Fatalf("Thinking = %+v, want budget 1024 (80%% of 1280)", got.Thinking)
		}
		if got.TopP != 0 {
			t.Errorf("TopP = %v, want 0", got.TopP)
		}
		if got.Temperature == nil || *got.Temperature != 1.0 {
			t.Errorf("Temperature = %v, want 1.0", got.Temperature)
		}
	})

	t.Run("max tokens above floor keeps its own 80%% budget", func(t *testing.T) {
		_, c := newClaudeTestContext()
		req := dto.GeneralOpenAIRequest{
			Model:     "claude-sonnet-4-20250514-thinking",
			MaxTokens: 2000,
			Messages:  []dto.Message{{Role: "user", Content: "Hi"}},
		}
		got, err := RequestOpenAI2ClaudeMessage(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.MaxTokens != 2000 {
			t.Errorf("MaxTokens = %d, want 2000 (unchanged)", got.MaxTokens)
		}
		if got.Thinking == nil || got.Thinking.GetBudgetTokens() != 1600 {
			t.Fatalf("Thinking = %+v, want budget 1600 (80%% of 2000)", got.Thinking)
		}
	})
}

func TestRequestOpenAI2ClaudeMessage_ReasoningEffort(t *testing.T) {
	tests := []struct {
		effort     string
		wantBudget int
	}{
		{"low", 1280},
		{"medium", 2048},
		{"high", 4096},
	}
	for _, tt := range tests {
		t.Run(tt.effort, func(t *testing.T) {
			_, c := newClaudeTestContext()
			req := dto.GeneralOpenAIRequest{
				Model:           "claude-3-opus-20240229",
				MaxTokens:       500,
				Messages:        []dto.Message{{Role: "user", Content: "Hi"}},
				ReasoningEffort: tt.effort,
			}
			got, err := RequestOpenAI2ClaudeMessage(c, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Thinking == nil || got.Thinking.GetBudgetTokens() != tt.wantBudget {
				t.Fatalf("Thinking = %+v, want budget %d", got.Thinking, tt.wantBudget)
			}
		})
	}
}

func TestRequestOpenAI2ClaudeMessage_ReasoningOverridesBudget(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:           "claude-3-opus-20240229",
		MaxTokens:       500,
		Messages:        []dto.Message{{Role: "user", Content: "Hi"}},
		ReasoningEffort: "low",
		Reasoning:       json.RawMessage(`{"max_tokens":777}`),
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Thinking == nil || got.Thinking.GetBudgetTokens() != 777 {
		t.Fatalf("Thinking = %+v, want budget 777 (reasoning param overrides reasoning_effort)", got.Thinking)
	}
}

func TestRequestOpenAI2ClaudeMessage_ReasoningInvalidJSONReturnsError(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 500,
		Messages:  []dto.Message{{Role: "user", Content: "Hi"}},
		Reasoning: json.RawMessage(`{not-json}`),
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err == nil {
		t.Fatal("expected error for malformed reasoning JSON")
	}
	if got != nil {
		t.Errorf("expected nil result on error, got %+v", got)
	}
}

func TestRequestOpenAI2ClaudeMessage_MessageMerging(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 100,
		Messages: []dto.Message{
			{Role: "user", Content: "foo"},
			{Role: "user", Content: "bar"},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1 (consecutive same-role messages merge)", len(got.Messages))
	}
	if got.Messages[0].Content != "foo bar" {
		t.Errorf("Content = %v, want %q", got.Messages[0].Content, "foo bar")
	}
}

func TestRequestOpenAI2ClaudeMessage_AssistantFirstMessageGetsUserPlaceholder(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 100,
		Messages: []dto.Message{
			{Role: "assistant", Content: "unsolicited reply"},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2 (placeholder + assistant)", len(got.Messages))
	}
	if got.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want user placeholder", got.Messages[0].Role)
	}
	placeholderContent, ok := got.Messages[0].Content.([]dto.ClaudeMediaMessage)
	if !ok || len(placeholderContent) != 1 || placeholderContent[0].GetText() != "..." {
		t.Errorf("Messages[0].Content = %#v, want single '...' text block", got.Messages[0].Content)
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Content != "unsolicited reply" {
		t.Errorf("Messages[1] = %+v, want assistant/'unsolicited reply'", got.Messages[1])
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolResultAfterAssistant(t *testing.T) {
	_, c := newClaudeTestContext()
	toolCallsJSON, _ := json.Marshal([]dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "get_weather", Arguments: `{"city":"NYC"}`}},
	})
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 100,
		Messages: []dto.Message{
			{Role: "user", Content: "weather?"},
			{Role: "assistant", ToolCalls: toolCallsJSON},
			{Role: "tool", ToolCallId: "call_1", Content: "sunny, 20C"},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// user question, assistant tool_use, then a brand-new user message carrying
	// the tool_result (because the message directly preceding "tool" is the
	// assistant message, not a user message).
	if len(got.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3, got %#v", len(got.Messages), got.Messages)
	}
	if got.Messages[1].Role != "assistant" {
		t.Fatalf("Messages[1].Role = %q, want assistant", got.Messages[1].Role)
	}
	assistantContent, ok := got.Messages[1].Content.([]dto.ClaudeMediaMessage)
	// The assistant message has no explicit Content, so the "..." placeholder-
	// content fallback kicks in first (as a text block), followed by the
	// tool_use block derived from ToolCalls.
	if !ok || len(assistantContent) != 2 || assistantContent[0].Type != "text" || assistantContent[1].Type != "tool_use" {
		t.Fatalf("assistant content = %#v, want [text('...'), tool_use]", got.Messages[1].Content)
	}
	if assistantContent[1].Id != "call_1" || assistantContent[1].Name != "get_weather" {
		t.Errorf("tool_use block = %+v, unexpected id/name", assistantContent[1])
	}
	inputMap, ok := assistantContent[1].Input.(map[string]any)
	if !ok || inputMap["city"] != "NYC" {
		t.Errorf("tool_use Input = %#v, want city=NYC", assistantContent[1].Input)
	}

	if got.Messages[2].Role != "user" {
		t.Fatalf("Messages[2].Role = %q, want user (new message for tool_result)", got.Messages[2].Role)
	}
	toolResultContent, ok := got.Messages[2].Content.([]dto.ClaudeMediaMessage)
	if !ok || len(toolResultContent) != 1 || toolResultContent[0].Type != "tool_result" {
		t.Fatalf("tool result message content = %#v, want single tool_result block", got.Messages[2].Content)
	}
	if toolResultContent[0].ToolUseId != "call_1" {
		t.Errorf("ToolUseId = %q, want call_1", toolResultContent[0].ToolUseId)
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolResultAppendedToPrecedingUserMessage(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 100,
		Messages: []dto.Message{
			{Role: "user", Content: "Q"},
			{Role: "tool", ToolCallId: "call_1", Content: "result-data"},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// tool message directly follows a user-role claude message, so its
	// tool_result is folded into that same message instead of creating a new one.
	if len(got.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1 (tool_result folded into preceding user message), got %#v", len(got.Messages), got.Messages)
	}
	content, ok := got.Messages[0].Content.([]dto.ClaudeMediaMessage)
	if !ok || len(content) != 2 {
		t.Fatalf("content = %#v, want [text('Q'), tool_result]", got.Messages[0].Content)
	}
	if content[0].Type != "text" || content[0].GetText() != "Q" {
		t.Errorf("content[0] = %+v, want text('Q')", content[0])
	}
	if content[1].Type != "tool_result" || content[1].ToolUseId != "call_1" {
		t.Errorf("content[1] = %+v, want tool_result/call_1", content[1])
	}
}

func TestRequestOpenAI2ClaudeMessage_ToolCallInvalidArgumentsSkipped(t *testing.T) {
	_, c := newClaudeTestContext()
	toolCallsJSON, _ := json.Marshal([]dto.ToolCallRequest{
		{ID: "call_bad", Type: "function", Function: dto.FunctionRequest{Name: "broken_tool", Arguments: "not-json"}},
	})
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 100,
		Messages: []dto.Message{
			{Role: "assistant", ToolCalls: toolCallsJSON},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// first-message-is-assistant placeholder + the assistant message itself.
	if len(got.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2, got %#v", len(got.Messages), got.Messages)
	}
	assistantContent, ok := got.Messages[1].Content.([]dto.ClaudeMediaMessage)
	if !ok {
		t.Fatalf("assistant content type = %T, want []dto.ClaudeMediaMessage", got.Messages[1].Content)
	}
	// Only the "..." placeholder-content text block survives; the tool_use
	// block is skipped because its arguments fail to unmarshal as a map.
	if len(assistantContent) != 1 || assistantContent[0].Type != "text" || assistantContent[0].GetText() != "..." {
		t.Errorf("assistant content = %#v, want single '...' text block (malformed tool arguments are skipped)", assistantContent)
	}
}

func TestRequestOpenAI2ClaudeMessage_ImageContentBase64(t *testing.T) {
	_, c := newClaudeTestContext()
	dataURL := pngDataURL(t, 2, 2)
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 100,
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "what is this"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(got.Messages))
	}
	content, ok := got.Messages[0].Content.([]dto.ClaudeMediaMessage)
	if !ok || len(content) != 2 {
		t.Fatalf("content = %#v, want [text, image]", got.Messages[0].Content)
	}
	if content[0].Type != "text" || content[0].GetText() != "what is this" {
		t.Errorf("content[0] = %+v, unexpected", content[0])
	}
	if content[1].Type != "image" {
		t.Fatalf("content[1].Type = %q, want image", content[1].Type)
	}
	if content[1].Source == nil || content[1].Source.MediaType != "image/png" {
		t.Errorf("content[1].Source = %+v, want media type image/png", content[1].Source)
	}
	if content[1].Source == nil || content[1].Source.Data == "" {
		t.Error("content[1].Source.Data should not be empty")
	}
}

func TestRequestOpenAI2ClaudeMessage_MultipleSystemMessages(t *testing.T) {
	_, c := newClaudeTestContext()
	req := dto.GeneralOpenAIRequest{
		Model:     "claude-3-opus-20240229",
		MaxTokens: 100,
		Messages: []dto.Message{
			{Role: "system", Content: "first system rule"},
			{Role: "system", Content: "second system rule"},
			{Role: "user", Content: "Hi"},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Consecutive same-role ("system") messages are merged into a single
	// space-joined message before system-block extraction runs, so this
	// yields one accumulated text block rather than two.
	sys, ok := got.System.([]dto.ClaudeMediaMessage)
	if !ok || len(sys) != 1 {
		t.Fatalf("System = %#v, want 1 merged text block", got.System)
	}
	if sys[0].GetText() != "first system rule second system rule" {
		t.Errorf("System block = %+v, want merged text", sys[0])
	}
}

// ---------------------------------------------------------------------------
// common.GetPointer sanity guard for the dto ClaudeToolChoice construction
// used above (kept minimal — the exhaustive matrix already lives in
// relay_claude_test.go's TestMapToolChoice).
// ---------------------------------------------------------------------------

func TestRequestOpenAI2ClaudeMessage_TemperatureAndTopKPreserved(t *testing.T) {
	_, c := newClaudeTestContext()
	temp := 0.3
	req := dto.GeneralOpenAIRequest{
		Model:       "claude-3-opus-20240229",
		MaxTokens:   100,
		Temperature: common.GetPointer(temp),
		TopK:        7,
		Messages:    []dto.Message{{Role: "user", Content: "Hi"}},
	}
	got, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Temperature == nil || *got.Temperature != 0.3 {
		t.Errorf("Temperature = %v, want 0.3", got.Temperature)
	}
	if got.TopK != 7 {
		t.Errorf("TopK = %d, want 7", got.TopK)
	}
}
