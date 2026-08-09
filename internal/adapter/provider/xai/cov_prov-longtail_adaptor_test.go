package xai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	// xAIStreamHandler delegates to helper.StreamScannerHandler, which
	// builds a time.NewTicker(StreamingTimeout*time.Second); zero panics.
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
}

type provLongtailXaiCtx struct {
	ctx *gin.Context
	rec *httptest.ResponseRecorder
}

func newProvLongtailXaiCtx() provLongtailXaiCtx {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return provLongtailXaiCtx{ctx: c, rec: w}
}

func xaiSSEBody(s string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(s)), Header: make(http.Header)}
}

// ---------------------------------------------------------------------------
// GetRequestURL: simple base+path concat (Cloudflare-gateway special case is
// covered by relaycommon.GetFullRequestURL's own tests; here we just verify
// xai wires the call through with its own fields).
// ---------------------------------------------------------------------------

func TestXai_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.x.ai"},
		RequestURLPath: "/v1/chat/completions",
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.x.ai/v1/chat/completions" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.x.ai/v1/chat/completions")
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestXai_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "xai-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(w.ctx, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer xai-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer xai-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: -search suffix and grok-3-mini reasoning-effort
// suffix parsing. These rewrite the outbound model name and inject vendor
// parameters, so getting the suffix stripping wrong either breaks routing
// (wrong upstream model) or silently drops a paid feature (search/reasoning).
// ---------------------------------------------------------------------------

func TestXai_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(w.ctx, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestXai_ConvertOpenAIRequest_SearchSuffix(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4-0709-search"}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-4-0709-search"}

	got, err := a.ConvertOpenAIRequest(w.ctx, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any for -search suffix, got %T", got)
	}
	sp, ok := m["search_parameters"].(map[string]any)
	if !ok || sp["mode"] != "on" {
		t.Errorf("search_parameters = %v, want {mode: on} injected", m["search_parameters"])
	}
	if m["model"] != "grok-4-0709" {
		t.Errorf("model in map = %v, want grok-4-0709 (suffix stripped)", m["model"])
	}
	if info.UpstreamModelName != "grok-4-0709" {
		t.Errorf("info.UpstreamModelName = %q, want grok-4-0709 (mutated for downstream routing)", info.UpstreamModelName)
	}
	if req.Model != "grok-4-0709" {
		t.Errorf("request.Model = %q, want grok-4-0709", req.Model)
	}
}

func TestXai_ConvertOpenAIRequest_GrokMiniHighReasoning(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-3-mini-beta-high"}
	req.MaxTokens = 500

	got, err := a.ConvertOpenAIRequest(w.ctx, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
	}
	if gotReq.Model != "grok-3-mini-beta" {
		t.Errorf("Model = %q, want grok-3-mini-beta (-high suffix stripped)", gotReq.Model)
	}
	if gotReq.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high", gotReq.ReasoningEffort)
	}
	if info.ReasoningEffort != "high" {
		t.Errorf("info.ReasoningEffort = %q, want high (mirrored onto RelayInfo)", info.ReasoningEffort)
	}
	if info.UpstreamModelName != "grok-3-mini-beta" {
		t.Errorf("info.UpstreamModelName = %q, want grok-3-mini-beta", info.UpstreamModelName)
	}
	// MaxTokens must migrate to MaxCompletionTokens for grok-3-mini family.
	if gotReq.MaxCompletionTokens != 500 {
		t.Errorf("MaxCompletionTokens = %d, want 500 (migrated from MaxTokens)", gotReq.MaxCompletionTokens)
	}
	if gotReq.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0 (cleared after migration)", gotReq.MaxTokens)
	}
}

func TestXai_ConvertOpenAIRequest_GrokMiniLowReasoning(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-3-mini-fast-beta-low"}

	got, _ := a.ConvertOpenAIRequest(w.ctx, info, req)
	gotReq := got.(*dto.GeneralOpenAIRequest)
	if gotReq.ReasoningEffort != "low" || gotReq.Model != "grok-3-mini-fast-beta" {
		t.Errorf("got model=%q effort=%q, want grok-3-mini-fast-beta / low", gotReq.Model, gotReq.ReasoningEffort)
	}
}

func TestXai_ConvertOpenAIRequest_GrokMiniMediumReasoning(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-3-mini-beta-medium"}

	got, _ := a.ConvertOpenAIRequest(w.ctx, info, req)
	gotReq := got.(*dto.GeneralOpenAIRequest)
	if gotReq.ReasoningEffort != "medium" || gotReq.Model != "grok-3-mini-beta" {
		t.Errorf("got model=%q effort=%q, want grok-3-mini-beta / medium", gotReq.Model, gotReq.ReasoningEffort)
	}
}

func TestXai_ConvertOpenAIRequest_GrokMiniNoSuffix_LeavesModelAlone(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-3-mini-beta"}

	got, _ := a.ConvertOpenAIRequest(w.ctx, info, req)
	gotReq := got.(*dto.GeneralOpenAIRequest)
	if gotReq.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want empty when no -high/-low/-medium suffix present", gotReq.ReasoningEffort)
	}
	if gotReq.Model != "grok-3-mini-beta" {
		t.Errorf("Model = %q, want unchanged", gotReq.Model)
	}
}

func TestXai_ConvertOpenAIRequest_NonMiniModelUntouched(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "grok-4-0709"}
	got, err := a.ConvertOpenAIRequest(w.ctx, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("non grok-3-mini, non -search model should pass through the same pointer untouched")
	}
}

// ---------------------------------------------------------------------------
// ConvertImageRequest: field mapping + unsigned N narrowing.
// ---------------------------------------------------------------------------

func TestXai_ConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.ImageRequest{Model: "grok-2-image", Prompt: "a dog", N: 3, ResponseFormat: "url"}
	got, err := a.ConvertImageRequest(w.ctx, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	xreq, ok := got.(ImageRequest)
	if !ok {
		t.Fatalf("expected ImageRequest, got %T", got)
	}
	if xreq.Model != "grok-2-image" || xreq.Prompt != "a dog" || xreq.N != 3 || xreq.ResponseFormat != "url" {
		t.Errorf("got %+v, want field-for-field mapping from dto.ImageRequest", xreq)
	}
}

// ---------------------------------------------------------------------------
// Unimplemented surfaces
// ---------------------------------------------------------------------------

func TestXai_UnimplementedSurfaces(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	if _, err := a.ConvertGeminiRequest(w.ctx, info, nil); err == nil {
		t.Error("ConvertGeminiRequest should error")
	}
	if _, err := a.ConvertClaudeRequest(w.ctx, info, nil); err == nil {
		t.Error("ConvertClaudeRequest should error")
	}
	if _, err := a.ConvertAudioRequest(w.ctx, info, dto.AudioRequest{}); err == nil {
		t.Error("ConvertAudioRequest should error (xAI has no audio endpoint)")
	}
	if _, err := a.ConvertEmbeddingRequest(w.ctx, info, dto.EmbeddingRequest{}); err == nil {
		t.Error("ConvertEmbeddingRequest should error (xAI has no embeddings endpoint)")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(w.ctx, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest should error")
	}
	got, err := a.ConvertRerankRequest(w.ctx, 0, dto.RerankRequest{})
	if err != nil || got != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil) (current behavior: silently unsupported)", got, err)
	}
	a.Init(info)
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestXai_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "xai" {
		t.Errorf("GetChannelName() = %q, want xai", a.GetChannelName())
	}
	if len(a.GetModelList()) == 0 {
		t.Fatal("GetModelList() must not be empty")
	}
}

// ---------------------------------------------------------------------------
// DoResponse dispatch: image generation routes through the OpenAI-format
// handler; everything else routes through xAI's own stream/non-stream
// handlers, which recompute completion tokens as (total - prompt) since xAI
// doesn't send a reliable completion_tokens field directly — this is the
// money-path computation.
// ---------------------------------------------------------------------------

func TestXai_DoResponse_NonStream_UsageDerivedFromTotalMinusPrompt(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"id":"c1","model":"grok-4","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"total_tokens":37}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := a.DoResponse(w.ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens != 27 {
		t.Errorf("CompletionTokens = %d, want 27 (=total_tokens 37 - prompt_tokens 10, NOT taken from a completion_tokens field)", u.CompletionTokens)
	}
	// Verify the rewritten body forwarded to the client also carries the
	// recomputed value, since that's what downstream billing readers see.
	var out ChatCompletionResponse
	if err := json.Unmarshal(w.rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("forwarded body not valid JSON: %v", err)
	}
	if out.Usage == nil || out.Usage.CompletionTokens != 27 {
		t.Errorf("forwarded usage = %+v, want CompletionTokens=27", out.Usage)
	}
}

func TestXai_DoResponse_NonStream_NoUsage_NilSafely(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"id":"c1","model":"grok-4","choices":[]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := a.DoResponse(w.ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	// Business requirement: absent upstream usage must not panic (nil-usage
	// guard), and the returned usage pointer stays nil rather than a
	// fabricated non-zero value.
	if usage != nil {
		if u, ok := usage.(*dto.Usage); ok && u != nil {
			t.Errorf("usage = %+v, want nil when upstream sent no usage block", u)
		}
	}
}

func TestXai_DoResponse_NonStream_MalformedBody(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{not-json")), Header: make(http.Header)}
	_, apiErr := a.DoResponse(w.ctx, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for malformed body")
	}
}

func TestXai_DoResponse_Stream_UpstreamUsagePresent(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4"},
	}
	body := `data: {"id":"c1","model":"grok-4","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"grok-4","choices":[],"usage":{"prompt_tokens":9,"total_tokens":15}}` + "\n\n" +
		"data: [DONE]\n\n"

	bcResp1 := xaiSSEBody(body)
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	usage, apiErr := a.DoResponse(w.ctx, bcResp1, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u := usage.(*dto.Usage)
	if u.PromptTokens != 9 {
		t.Errorf("PromptTokens = %d, want 9 (from upstream usage chunk)", u.PromptTokens)
	}
	if u.CompletionTokens != 6 {
		t.Errorf("CompletionTokens = %d, want 6 (=total 15 - prompt 9)", u.CompletionTokens)
	}
}

func TestXai_DoResponse_Stream_NoUpstreamUsage_FallsBackToEstimate(t *testing.T) {
	a := &Adaptor{}
	w := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4"},
	}
	info.SetEstimatePromptTokens(4)
	body := `data: {"id":"c1","model":"grok-4","choices":[{"delta":{"content":"hello there"}}]}` + "\n\n" +
		"data: [DONE]\n\n"

	bcResp0 := xaiSSEBody(body)
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	usage, apiErr := a.DoResponse(w.ctx, bcResp0, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u := usage.(*dto.Usage)
	if u.PromptTokens != 4 {
		t.Errorf("PromptTokens = %d, want 4 (fallback estimate since upstream sent no usage)", u.PromptTokens)
	}
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (estimated from accumulated stream text)", u.CompletionTokens)
	}
}
