package perplexity

// Business-acceptance tests for the Perplexity adaptor. No prior coverage in
// this repo. Perplexity's protocol-critical surface is requestOpenAI2Perplexity
// (drops fields Perplexity's API rejects and carries the search-specific
// params) plus the TopP >= 1 clamp in ConvertOpenAIRequest (Perplexity errors
// on top_p == 1).

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

func init() {
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
}

func newProvLt2PerplexityCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL / SetupRequestHeader
// ---------------------------------------------------------------------------

func TestPerplexity_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.perplexity.ai"}}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.perplexity.ai/chat/completions" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.perplexity.ai/chat/completions")
	}
}

func TestPerplexity_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "px-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer px-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer px-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertGeminiRequest / ConvertAudioRequest / ConvertImageRequest /
// ConvertEmbeddingRequest / ConvertOpenAIResponsesRequest: documented
// "not implemented" surfaces.
// ---------------------------------------------------------------------------

func TestPerplexity_ConvertGeminiRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{}); err == nil {
		t.Fatal("expected error, gemini format is not supported by perplexity")
	}
}

func TestPerplexity_ConvertAudioRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{}); err == nil {
		t.Fatal("expected error, audio is not implemented for perplexity")
	}
}

func TestPerplexity_ConvertImageRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertImageRequest(c, info, dto.ImageRequest{}); err == nil {
		t.Fatal("expected error, images are not implemented for perplexity")
	}
}

func TestPerplexity_ConvertEmbeddingRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{}); err == nil {
		t.Fatal("expected error, embeddings are not implemented for perplexity")
	}
}

func TestPerplexity_ConvertOpenAIResponsesRequest_NotImplemented_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Fatal("expected error, /v1/responses is not implemented for perplexity")
	}
}

// ---------------------------------------------------------------------------
// ConvertClaudeRequest: delegates to openai.Adaptor, a real conversion.
// ---------------------------------------------------------------------------

func TestPerplexity_ConvertClaudeRequest_DelegatesAndConverts(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.ClaudeRequest{
		Model:     "sonar-pro",
		MaxTokens: 200,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	got, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest (openai.Adaptor conversion)", got)
	}
	if out.MaxTokens != 200 {
		t.Errorf("MaxTokens = %d, want 200", out.MaxTokens)
	}
	if len(out.Messages) != 1 || out.Messages[0].Role != "user" {
		t.Fatalf("Messages not translated correctly: %+v", out.Messages)
	}
}

// ---------------------------------------------------------------------------
// Init is a documented no-op; must not panic with real ChannelMeta.
// ---------------------------------------------------------------------------

func TestPerplexity_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.perplexity.ai"}}
	a.Init(info)
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: nil-safety, TopP clamp, and field mapping via
// requestOpenAI2Perplexity.
// ---------------------------------------------------------------------------

func TestPerplexity_ConvertOpenAIRequest_NilRequest_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestPerplexity_ConvertOpenAIRequest_TopPAtOrAboveOne_ClampedTo099(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "sonar", Messages: []dto.Message{{Role: "user", Content: "hi"}}, TopP: 1.0}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.GeneralOpenAIRequest)
	if out.TopP != 0.99 {
		t.Errorf("TopP = %v, want 0.99 (Perplexity rejects top_p==1, must be clamped)", out.TopP)
	}
}

func TestPerplexity_ConvertOpenAIRequest_TopPBelowOne_LeftUnchanged(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "sonar", Messages: []dto.Message{{Role: "user", Content: "hi"}}, TopP: 0.5}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.GeneralOpenAIRequest)
	if out.TopP != 0.5 {
		t.Errorf("TopP = %v, want unchanged 0.5", out.TopP)
	}
}

func TestPerplexity_ConvertOpenAIRequest_MapsSearchFieldsAndDropsUnmapped(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	temp := 0.3
	name := "Bob"
	req := &dto.GeneralOpenAIRequest{
		Model:                  "sonar-pro",
		Stream:                 true,
		Messages:               []dto.Message{{Role: "user", Content: "hi", Name: &name}},
		Temperature:            &temp,
		TopP:                   0.4,
		MaxTokens:              128,
		FrequencyPenalty:       0.1,
		PresencePenalty:        0.2,
		SearchRecencyFilter:    "week",
		ReturnImages:           true,
		ReturnRelatedQuestions: true,
		SearchMode:             "web",
		User:                   "u-123",
	}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.GeneralOpenAIRequest)
	if out.Model != "sonar-pro" || !out.Stream {
		t.Errorf("Model/Stream not carried: %+v", out)
	}
	if out.GetMaxTokens() != 128 {
		t.Errorf("MaxTokens = %d, want 128", out.GetMaxTokens())
	}
	if out.FrequencyPenalty != 0.1 || out.PresencePenalty != 0.2 {
		t.Errorf("penalty fields not carried: freq=%v pres=%v", out.FrequencyPenalty, out.PresencePenalty)
	}
	if out.SearchRecencyFilter != "week" || !out.ReturnImages || !out.ReturnRelatedQuestions || out.SearchMode != "web" {
		t.Errorf("search-specific fields not carried: %+v", out)
	}
	// FINDING: requestOpenAI2Perplexity rebuilds each dto.Message with only
	// Role/Content — Name is silently dropped in translation.
	if out.Messages[0].Name != nil {
		t.Errorf("Name = %v, want nil — current implementation drops per-message Name field", out.Messages[0].Name)
	}
	// FINDING: the top-level User field has no corresponding field on the
	// perplexity-shaped struct, so it is silently dropped too.
	if out.User != "" {
		t.Errorf("User = %q, want empty — current implementation drops the top-level User field", out.User)
	}
}

func TestPerplexity_ConvertOpenAIRequest_EmptyMessages_ProducesEmptySlice(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "sonar", Messages: []dto.Message{}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.GeneralOpenAIRequest)
	if out.Messages == nil {
		t.Error("Messages should be an empty (non-nil) slice for empty input, not nil")
	}
	if len(out.Messages) != 0 {
		t.Errorf("Messages len = %d, want 0", len(out.Messages))
	}
}

// ---------------------------------------------------------------------------
// ConvertRerankRequest: documented (nil, nil) surface.
// ---------------------------------------------------------------------------

func TestPerplexity_ConvertRerankRequest_UnsupportedNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2PerplexityCtx()
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{Query: "q"})
	if err != nil || got != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil) — perplexity has no rerank support", got, err)
	}
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest against the channel base URL.
// ---------------------------------------------------------------------------

func TestPerplexity_DoRequest_DelegatesToApiRequest(t *testing.T) {
	app.InitHttpClient()
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
	c, _ := newProvLt2PerplexityCtx()
	c.Request = httptest.NewRequest(http.MethodPost, "/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "px-key"},
		RequestURLPath: "/chat/completions",
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
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotAuth != "Bearer px-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer px-key", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: delegates to openai.Adaptor.DoResponse.
// ---------------------------------------------------------------------------

func TestPerplexity_DoResponse_DelegatesToOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2PerplexityCtx()
	info := &relaycommon.RelayInfo{IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected chat completions delegated to shared openai adaptor (*dto.Usage), got %T", usage)
	}
	if u.TotalTokens != 13 {
		t.Errorf("TotalTokens = %d, want 13 (billing amount from upstream usage)", u.TotalTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestPerplexity_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "perplexity" {
		t.Errorf("GetChannelName() = %q, want perplexity", a.GetChannelName())
	}
	if len(a.GetModelList()) == 0 {
		t.Fatal("GetModelList() must not be empty")
	}
}
