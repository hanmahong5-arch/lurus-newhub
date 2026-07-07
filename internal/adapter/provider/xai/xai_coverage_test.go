package xai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func newTestContext() (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return w, c
}

// ---------------------------------------------------------------------------
// Not-implemented / not-available stub methods.
// ---------------------------------------------------------------------------

func TestConvertGeminiRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertGeminiRequest(nil, nil, nil)
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Errorf("error = %v, want %q", err, "not implemented")
	}
}

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertClaudeRequest(nil, nil, nil)
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not available" {
		t.Errorf("error = %v, want %q", err, "not available")
	}
}

func TestConvertAudioRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not available" {
		t.Errorf("error = %v, want %q", err, "not available")
	}
}

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not available" {
		t.Errorf("error = %v, want %q", err, "not available")
	}
}

func TestConvertOpenAIResponsesRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Errorf("error = %v, want %q", err, "not implemented")
	}
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestInit(t *testing.T) {
	a := &Adaptor{}
	// Init is a no-op; calling it must not panic even with a bare info.
	a.Init(&relaycommon.RelayInfo{})
}

// ---------------------------------------------------------------------------
// ConvertImageRequest
// ---------------------------------------------------------------------------

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.ImageRequest{
		Model:          "grok-2-image",
		Prompt:         "a cat",
		N:              2,
		ResponseFormat: "url",
	}
	got, err := a.ConvertImageRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	imgReq, ok := got.(ImageRequest)
	if !ok {
		t.Fatalf("expected ImageRequest, got %T", got)
	}
	if imgReq.Model != "grok-2-image" || imgReq.Prompt != "a cat" || imgReq.N != 2 || imgReq.ResponseFormat != "url" {
		t.Errorf("unexpected ImageRequest: %+v", imgReq)
	}
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.x.ai", ChannelType: 1},
		RequestURLPath: "/v1/chat/completions",
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.x.ai/v1/chat/completions"
	if got != want {
		t.Errorf("GetRequestURL() = %q, want %q", got, want)
	}
}

func TestGetRequestURL_CloudflareGateway(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://gateway.ai.cloudflare.com/x", ChannelType: pkgconstant.ChannelTypeOpenAI},
		RequestURLPath: "/v1/chat/completions",
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://gateway.ai.cloudflare.com/x/chat/completions"
	if got != want {
		t.Errorf("GetRequestURL() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	_, c := newTestContext()
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-xai-key"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer sk-xai-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer sk-xai-key")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest — every branch: nil request, "-search" suffix,
// grok-3-mini prefix with -high/-low/-medium suffixes, and a plain pass-through.
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, nil)
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "request is nil" {
		t.Errorf("error = %v, want %q", err, "request is nil")
	}
}

func TestConvertOpenAIRequest_SearchSuffix(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4-0709-search"}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-4-0709-search"}
	got, err := a.ConvertOpenAIRequest(nil, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if info.UpstreamModelName != "grok-4-0709" {
		t.Errorf("info.UpstreamModelName = %q, want %q", info.UpstreamModelName, "grok-4-0709")
	}
	if req.Model != "grok-4-0709" {
		t.Errorf("request.Model = %q, want %q", req.Model, "grok-4-0709")
	}
	sp, ok := m["search_parameters"].(map[string]any)
	if !ok {
		t.Fatalf("expected search_parameters map, got %v", m["search_parameters"])
	}
	if sp["mode"] != "on" {
		t.Errorf("search_parameters.mode = %v, want %q", sp["mode"], "on")
	}
}

func TestConvertOpenAIRequest_GrokMiniReasoningSuffixes(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		wantModel      string
		wantEffort     string
		maxTokens      uint
		maxCompletion  uint
		wantMaxComp    uint
		wantOrigTokens uint
	}{
		{name: "high suffix", model: "grok-3-mini-beta-high", wantModel: "grok-3-mini-beta", wantEffort: "high"},
		{name: "low suffix", model: "grok-3-mini-beta-low", wantModel: "grok-3-mini-beta", wantEffort: "low"},
		{name: "medium suffix", model: "grok-3-mini-beta-medium", wantModel: "grok-3-mini-beta", wantEffort: "medium"},
		{name: "no suffix", model: "grok-3-mini-beta", wantModel: "grok-3-mini-beta", wantEffort: ""},
	}
	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
			req := &dto.GeneralOpenAIRequest{Model: tt.model, MaxTokens: 100}
			got, err := a.ConvertOpenAIRequest(nil, info, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			outReq, ok := got.(*dto.GeneralOpenAIRequest)
			if !ok {
				t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
			}
			if outReq.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", outReq.Model, tt.wantModel)
			}
			if outReq.ReasoningEffort != tt.wantEffort {
				t.Errorf("ReasoningEffort = %q, want %q", outReq.ReasoningEffort, tt.wantEffort)
			}
			if info.ReasoningEffort != tt.wantEffort {
				t.Errorf("info.ReasoningEffort = %q, want %q", info.ReasoningEffort, tt.wantEffort)
			}
			if info.UpstreamModelName != tt.wantModel {
				t.Errorf("info.UpstreamModelName = %q, want %q", info.UpstreamModelName, tt.wantModel)
			}
			// MaxTokens -> MaxCompletionTokens migration only fires when
			// MaxCompletionTokens starts at 0 and MaxTokens != 0.
			if outReq.MaxCompletionTokens != 100 {
				t.Errorf("MaxCompletionTokens = %d, want 100", outReq.MaxCompletionTokens)
			}
			if outReq.MaxTokens != 0 {
				t.Errorf("MaxTokens = %d, want 0 (moved to MaxCompletionTokens)", outReq.MaxTokens)
			}
		})
	}
}

func TestConvertOpenAIRequest_GrokMiniMaxCompletionAlreadySet(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-3-mini-beta", MaxTokens: 50, MaxCompletionTokens: 20}
	got, err := a.ConvertOpenAIRequest(nil, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outReq := got.(*dto.GeneralOpenAIRequest)
	// MaxCompletionTokens already non-zero, so the migration branch must not fire.
	if outReq.MaxCompletionTokens != 20 {
		t.Errorf("MaxCompletionTokens = %d, want 20 (unchanged)", outReq.MaxCompletionTokens)
	}
	if outReq.MaxTokens != 50 {
		t.Errorf("MaxTokens = %d, want 50 (unchanged)", outReq.MaxTokens)
	}
}

func TestConvertOpenAIRequest_PlainPassthrough(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-2"}
	got, err := a.ConvertOpenAIRequest(nil, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outReq, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
	}
	if outReq != req {
		t.Errorf("expected pass-through of the same pointer, got a different value: %+v", outReq)
	}
	if info.ReasoningEffort != "" || info.UpstreamModelName != "" {
		t.Errorf("info should be untouched for plain models, got ReasoningEffort=%q UpstreamModelName=%q", info.ReasoningEffort, info.UpstreamModelName)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(ModelList))
	}
	for i, m := range ModelList {
		if got[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], m)
		}
	}
	if a.GetChannelName() != "xai" {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), "xai")
	}
}

// ---------------------------------------------------------------------------
// xAIHandler (non-stream) — hermetic (only requires an io.Reader body).
// ---------------------------------------------------------------------------

func TestXAIHandler_Success(t *testing.T) {
	w, c := newTestContext()
	body := `{"id":"cmpl-1","object":"chat.completion","created":100,"model":"grok-2",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` +
		`"usage":{"total_tokens":10,"prompt_tokens":4,"completion_tokens_details":{"reasoning_tokens":1}}}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := xAIHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	// CompletionTokens = TotalTokens(10) - PromptTokens(4) = 6.
	if usage.CompletionTokens != 6 {
		t.Errorf("CompletionTokens = %d, want 6", usage.CompletionTokens)
	}
	// TextTokens = CompletionTokens(6) - ReasoningTokens(1) = 5.
	if usage.CompletionTokenDetails.TextTokens != 5 {
		t.Errorf("CompletionTokenDetails.TextTokens = %d, want 5", usage.CompletionTokenDetails.TextTokens)
	}
	if w.Code != http.StatusOK {
		t.Errorf("http status written = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"hi"`) {
		t.Errorf("response body = %q, want it to contain %q", w.Body.String(), `"hi"`)
	}
}

func TestXAIHandler_NilUsage(t *testing.T) {
	_, c := newTestContext()
	body := `{"id":"cmpl-2","object":"chat.completion","created":1,"model":"grok-2","choices":[]}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := xAIHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil (no usage block in response)", usage)
	}
}

func TestXAIHandler_MalformedJSON(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{not-json"))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := xAIHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// errReader always fails on Read, to exercise the io.ReadAll error branch.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReader) Close() error               { return nil }

func TestXAIHandler_BodyReadError(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: errReader{}}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := xAIHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when body read fails, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// ---------------------------------------------------------------------------
// streamResponseXAI2OpenAI
// ---------------------------------------------------------------------------

func TestStreamResponseXAI2OpenAI_Nil(t *testing.T) {
	got := streamResponseXAI2OpenAI(nil, &dto.Usage{})
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestStreamResponseXAI2OpenAI_CopiesFieldsAndUsage(t *testing.T) {
	xResp := &dto.ChatCompletionsStreamResponse{
		Id:      "s-1",
		Object:  "chat.completion.chunk",
		Created: 42,
		Model:   "grok-2",
		Usage:   &dto.Usage{PromptTokens: 3, TotalTokens: 9},
	}
	usage := &dto.Usage{CompletionTokens: 6}
	got := streamResponseXAI2OpenAI(xResp, usage)
	if got.Id != "s-1" || got.Object != "chat.completion.chunk" || got.Created != int64(42) || got.Model != "grok-2" {
		t.Errorf("unexpected envelope: %+v", got)
	}
	if got.Usage.CompletionTokens != 6 {
		t.Errorf("Usage.CompletionTokens = %d, want 6 (overwritten from usage arg)", got.Usage.CompletionTokens)
	}
}

// ---------------------------------------------------------------------------
// xAIStreamHandler — hermetic scan loop (in-memory reader, no network).
// ---------------------------------------------------------------------------

func TestXAIStreamHandler_WithUsageInStream(t *testing.T) {
	origTimeout := pkgconstant.StreamingTimeout
	pkgconstant.StreamingTimeout = 30
	defer func() { pkgconstant.StreamingTimeout = origTimeout }()

	sse := "data: {\"id\":\"s-1\",\"model\":\"grok-2\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"total_tokens\":8}}\n\n" +
		"data: [DONE]\n\n"
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, DisablePing: true}

	usage, apiErr := xAIStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if usage.PromptTokens != 2 || usage.TotalTokens != 8 {
		t.Errorf("Prompt/Total = %d/%d, want 2/8", usage.PromptTokens, usage.TotalTokens)
	}
	if usage.CompletionTokens != 6 {
		t.Errorf("CompletionTokens = %d, want 6 (TotalTokens - PromptTokens)", usage.CompletionTokens)
	}
}

func TestXAIStreamHandler_MalformedEventIsSkipped(t *testing.T) {
	origTimeout := pkgconstant.StreamingTimeout
	pkgconstant.StreamingTimeout = 30
	defer func() { pkgconstant.StreamingTimeout = origTimeout }()

	sse := "data: {not-json}\n\n"
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-2"}, DisablePing: true}

	usage, apiErr := xAIStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	// No usage event was ever parsed, so the fallback ResponseText2Usage path
	// is taken with an empty response text, yielding zeroed token counts.
	if usage.PromptTokens != 0 || usage.TotalTokens != 0 {
		t.Errorf("Prompt/Total = %d/%d, want 0/0 (no usage event parsed)", usage.PromptTokens, usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// DoResponse — dispatch across IsStream / RelayMode branches.
// ---------------------------------------------------------------------------

func TestDoResponse_Dispatch(t *testing.T) {
	a := &Adaptor{}

	t.Run("images generations", func(t *testing.T) {
		_, c := newTestContext()
		body := `{"created":1,"data":[{"url":"http://example.com/x.png"}],"usage":{"total_tokens":5}}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesGenerations, ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 5 {
			t.Errorf("usage = %+v, want TotalTokens=5", usage)
		}
	})

	t.Run("images edits", func(t *testing.T) {
		_, c := newTestContext()
		body := `{"created":1,"data":[],"usage":{"total_tokens":3}}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 3 {
			t.Errorf("usage = %+v, want TotalTokens=3", usage)
		}
	})

	t.Run("default non-stream chat", func(t *testing.T) {
		_, c := newTestContext()
		body := `{"id":"c-1","object":"chat.completion","created":1,"model":"grok-2","choices":[],"usage":{"total_tokens":2}}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 2 {
			t.Errorf("usage = %+v, want TotalTokens=2", usage)
		}
	})

	t.Run("default stream chat", func(t *testing.T) {
		origTimeout := pkgconstant.StreamingTimeout
		pkgconstant.StreamingTimeout = 30
		defer func() { pkgconstant.StreamingTimeout = origTimeout }()

		_, c := newTestContext()
		sse := "data: {\"id\":\"s-1\",\"model\":\"grok-2\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"total_tokens\":3}}\n\n"
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}, DisablePing: true}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 3 {
			t.Errorf("usage = %+v, want TotalTokens=3", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// DoRequest — delegates entirely to provider.DoApiRequest, which performs a
// live HTTP round trip. Not exercised here: no network I/O is permitted in
// this hermetic test file.
// ---------------------------------------------------------------------------
