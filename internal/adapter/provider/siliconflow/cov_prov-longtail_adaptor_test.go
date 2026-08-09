package siliconflow

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func newProvLongtailSFCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL: rerank uses a dedicated path, everything else falls through
// to the generic base-url + request-path composition.
// ---------------------------------------------------------------------------

func TestSF_GetRequestURL_Rerank(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeRerank,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.siliconflow.cn"},
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.siliconflow.cn/v1/rerank" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.siliconflow.cn/v1/rerank")
	}
}

func TestSF_GetRequestURL_ChatCompletions_GenericComposition(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.siliconflow.cn",
		},
		RequestURLPath: "/v1/chat/completions",
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.siliconflow.cn/v1/chat/completions" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.siliconflow.cn/v1/chat/completions")
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSF_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sf-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer sf-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sf-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: SiliconFlow's FIM quirk — a FIM request (prefix or
// suffix set) with no messages must get a synthetic empty user message,
// otherwise upstream rejects the call outright.
// ---------------------------------------------------------------------------

func TestSF_ConvertOpenAIRequest_FIM_NoMessages_InjectsSyntheticUserMessage(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "deepseek-ai/DeepSeek-Coder-V2-Instruct", Prefix: "def foo("}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.GeneralOpenAIRequest)
	if len(out.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1 synthetic message", len(out.Messages))
	}
	if out.Messages[0].Role != "user" {
		t.Errorf("synthetic message role = %q, want user", out.Messages[0].Role)
	}
}

func TestSF_ConvertOpenAIRequest_FIM_SuffixOnly_AlsoInjects(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "m", Suffix: ")"}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.(*dto.GeneralOpenAIRequest).Messages) != 1 {
		t.Fatal("expected synthetic message injected for suffix-only FIM request")
	}
}

func TestSF_ConvertOpenAIRequest_NonFIM_MessagesUntouched(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{
		Model:    "Qwen/Qwen2-7B-Instruct",
		Messages: []dto.Message{{Role: "system", Content: "sys"}},
	}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.GeneralOpenAIRequest)
	if len(out.Messages) != 1 || out.Messages[0].Role != "system" {
		t.Errorf("existing messages should be left untouched, got %+v", out.Messages)
	}
}

func TestSF_ConvertOpenAIRequest_FIM_WithExistingMessages_NotOverwritten(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{
		Model:    "m",
		Prefix:   "x",
		Messages: []dto.Message{{Role: "user", Content: "already here"}},
	}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.GeneralOpenAIRequest)
	if len(out.Messages) != 1 || out.Messages[0].Content != "already here" {
		t.Errorf("pre-existing messages must not be clobbered, got %+v", out.Messages)
	}
}

// ---------------------------------------------------------------------------
// ConvertImageRequest: SiliconFlow-specific fields take priority over the
// OpenAI-standard size/n fields, but only fall back when the SF field is
// unset. Also verify Extra JSON round-trips into the SF-specific fields
// (guidance_scale, seed, etc.) and that malformed Extra degrades gracefully
// instead of panicking.
// ---------------------------------------------------------------------------

func TestSF_ConvertImageRequest_FallsBackToOpenAIStandardFields(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.ImageRequest{Model: "black-forest-labs/FLUX.1-schnell", Prompt: "a cat", Size: "1024x1024", N: 4}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sf := got.(*SFImageRequest)
	if sf.ImageSize != "1024x1024" {
		t.Errorf("ImageSize = %q, want fallback to OpenAI Size %q", sf.ImageSize, "1024x1024")
	}
	if sf.BatchSize != 4 {
		t.Errorf("BatchSize = %d, want fallback to OpenAI N=4", sf.BatchSize)
	}
	if sf.Model != req.Model || sf.Prompt != req.Prompt {
		t.Errorf("Model/Prompt not copied through: got %+v", sf)
	}
}

func TestSF_ConvertImageRequest_ExtraFieldsWinOverStandardFields(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.ImageRequest{
		Model:  "black-forest-labs/FLUX.1-schnell",
		Prompt: "a cat",
		Size:   "1024x1024",
		N:      4,
		Extra: map[string]json.RawMessage{
			"image_size":     json.RawMessage(`"512x512"`),
			"batch_size":     json.RawMessage(`2`),
			"guidance_scale": json.RawMessage(`7.5`),
			"seed":           json.RawMessage(`42`),
		},
	}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sf := got.(*SFImageRequest)
	if sf.ImageSize != "512x512" {
		t.Errorf("ImageSize = %q, want SF-specific image_size (%q) to win over standard Size", sf.ImageSize, "512x512")
	}
	if sf.BatchSize != 2 {
		t.Errorf("BatchSize = %d, want SF-specific batch_size (2) to win over standard N", sf.BatchSize)
	}
	if sf.GuidanceScale != 7.5 {
		t.Errorf("GuidanceScale = %v, want 7.5 (from Extra)", sf.GuidanceScale)
	}
	if sf.Seed != 42 {
		t.Errorf("Seed = %d, want 42 (from Extra)", sf.Seed)
	}
}

func TestSF_ConvertImageRequest_MalformedExtra_DegradesToEmptySFRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.ImageRequest{
		Model:  "m",
		Prompt: "p",
		Size:   "256x256",
		Extra: map[string]json.RawMessage{
			"image_size": json.RawMessage(`not-valid-json`),
		},
	}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("malformed Extra must not surface as an error (should degrade gracefully): %v", err)
	}
	sf := got.(*SFImageRequest)
	if sf.ImageSize != "256x256" {
		t.Errorf("ImageSize = %q, want fallback to Size after Extra parse failure reset sfRequest", sf.ImageSize)
	}
}

func TestSF_ConvertImageRequest_NilExtra_NoPanic(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.ImageRequest{Model: "m", Prompt: "p"}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(*SFImageRequest).Model != "m" {
		t.Errorf("Model not copied through with nil Extra")
	}
}

// ---------------------------------------------------------------------------
// ConvertEmbeddingRequest / ConvertRerankRequest: pass-through contracts.
// ---------------------------------------------------------------------------

func TestSF_ConvertEmbeddingRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.EmbeddingRequest{Model: "BAAI/bge-m3"}
	got, err := a.ConvertEmbeddingRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(dto.EmbeddingRequest).Model != "BAAI/bge-m3" {
		t.Errorf("got %#v, want pass-through", got)
	}
}

func TestSF_ConvertRerankRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	req := dto.RerankRequest{Model: "BAAI/bge-reranker-v2-m3", Query: "q"}
	got, err := a.ConvertRerankRequest(c, relayconstant.RelayModeRerank, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(dto.RerankRequest).Query != "q" {
		t.Errorf("got %#v, want pass-through", got)
	}
}

// ---------------------------------------------------------------------------
// Unimplemented surfaces + metadata surfaces.
// ---------------------------------------------------------------------------

func TestSF_UnimplementedSurfaces(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertGeminiRequest(c, info, nil); err == nil {
		t.Error("ConvertGeminiRequest should error")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest should error")
	}
	a.Init(info)
}

func TestSF_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "siliconflow" {
		t.Errorf("GetChannelName() = %q, want siliconflow", a.GetChannelName())
	}
	if len(a.GetModelList()) == 0 {
		t.Fatal("GetModelList() must not be empty")
	}
}

// ---------------------------------------------------------------------------
// DoResponse dispatch: rerank mode routes to the SF-specific rerank handler,
// everything else delegates to the shared openai adaptor.
// ---------------------------------------------------------------------------

func TestSF_DoResponse_NonRerankMode_DelegatesToOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if _, ok := usage.(*dto.Usage); !ok {
		t.Fatalf("expected chat completions delegated to shared openai adaptor (*dto.Usage), got %T", usage)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// siliconflowRerankHandler: this is the billing surface — usage tokens must
// come from meta.tokens.input_tokens/output_tokens, not be invented.
// ---------------------------------------------------------------------------

func TestSF_RerankHandler_UsageFromMetaTokens(t *testing.T) {
	c, w := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"results":[{"index":0,"relevance_score":0.9}],"meta":{"tokens":{"input_tokens":100,"output_tokens":5}}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := siliconflowRerankHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100 (meta.tokens.input_tokens)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5 (meta.tokens.output_tokens)", usage.CompletionTokens)
	}
	if usage.TotalTokens != 105 {
		t.Errorf("TotalTokens = %d, want 105 (sum, this is the billed amount)", usage.TotalTokens)
	}
	c.Writer.WriteHeaderNow()
	if w.Code != 200 {
		t.Errorf("status forwarded = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var payload dto.RerankResponse
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if len(payload.Results) != 1 || payload.Results[0].RelevanceScore != 0.9 {
		t.Errorf("Results not passed through correctly: %+v", payload.Results)
	}
	if payload.Usage.TotalTokens != 105 {
		t.Errorf("embedded usage in response body = %d, want 105", payload.Usage.TotalTokens)
	}
}

func TestSF_RerankHandler_MissingTokens_DefaultsToZero(t *testing.T) {
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"results":[]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := siliconflowRerankHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error, usage extraction must degrade gracefully: %v", apiErr.Error())
	}
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 when meta.tokens is absent (not a panic, not a made-up value)", usage.TotalTokens)
	}
}

func TestSF_RerankHandler_MalformedJSON_Errors(t *testing.T) {
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not json")), Header: make(http.Header)}
	usage, apiErr := siliconflowRerankHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed upstream body")
	}
	if usage != nil {
		t.Errorf("usage should be nil on error, got %v", usage)
	}
}

func TestSF_RerankHandler_EmptyBody_Errors(t *testing.T) {
	c, _ := newProvLongtailSFCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}
	_, apiErr := siliconflowRerankHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for empty body (invalid JSON)")
	}
}
