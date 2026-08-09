package submodel

// Business-acceptance tests for the SubModel adaptor. No prior coverage in
// this repo. SubModel supports only chat/completions (all other request
// formats are documented "not supported" errors) via a URL built from
// info.RequestURLPath — GetFullRequestURL and the stream/non-stream dispatch
// in DoResponse are the billing-critical surfaces.

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

func newProvLt2SubmodelCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL: composed from base + request path.
// ---------------------------------------------------------------------------

func TestSubmodel_GetRequestURL_ComposesBaseAndPath(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.submodel.ai"},
		RequestURLPath: "/v1/chat/completions",
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.submodel.ai/v1/chat/completions" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.submodel.ai/v1/chat/completions")
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSubmodel_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sm-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer sm-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sm-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertGeminiRequest / ConvertClaudeRequest / ConvertAudioRequest /
// ConvertImageRequest / ConvertRerankRequest / ConvertEmbeddingRequest /
// ConvertOpenAIResponsesRequest: documented "not supported" surfaces.
// ---------------------------------------------------------------------------

func TestSubmodel_ConvertGeminiRequest_NotSupported_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{}); err == nil {
		t.Fatal("expected error, gemini format is not supported by submodel")
	}
}

func TestSubmodel_ConvertClaudeRequest_NotSupported_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{}); err == nil {
		t.Fatal("expected error, claude format is not supported by submodel")
	}
}

func TestSubmodel_ConvertAudioRequest_NotSupported_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{}); err == nil {
		t.Fatal("expected error, audio is not supported by submodel")
	}
}

func TestSubmodel_ConvertImageRequest_NotSupported_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertImageRequest(c, info, dto.ImageRequest{}); err == nil {
		t.Fatal("expected error, images are not supported by submodel")
	}
}

func TestSubmodel_ConvertRerankRequest_NotSupported_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	if _, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{}); err == nil {
		t.Fatal("expected error, rerank is not supported by submodel")
	}
}

func TestSubmodel_ConvertEmbeddingRequest_NotSupported_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{}); err == nil {
		t.Fatal("expected error, embeddings are not supported by submodel")
	}
}

func TestSubmodel_ConvertOpenAIResponsesRequest_NotSupported_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Fatal("expected error, /v1/responses is not supported by submodel")
	}
}

// ---------------------------------------------------------------------------
// Init is a documented no-op; must not panic with real ChannelMeta.
// ---------------------------------------------------------------------------

func TestSubmodel_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.submodel.ai"}}
	a.Init(info)
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: nil-safety and passthrough.
// ---------------------------------------------------------------------------

func TestSubmodel_ConvertOpenAIRequest_NilRequest_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestSubmodel_ConvertOpenAIRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "deepseek-ai/DeepSeek-V3.1", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("ConvertOpenAIRequest must return the same request pointer unmodified")
	}
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest against the channel base URL.
// ---------------------------------------------------------------------------

func TestSubmodel_DoRequest_DelegatesToApiRequest(t *testing.T) {
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
	c, _ := newProvLt2SubmodelCtx()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "sm-key"},
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
	if gotAuth != "Bearer sm-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer sm-key", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: stream vs. non-stream dispatch, both money-adjacent (usage
// extraction feeds billing).
// ---------------------------------------------------------------------------

func TestSubmodel_DoResponse_NonStream_ReturnsUsage(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 10 {
		t.Errorf("TotalTokens = %d, want 10 (from upstream usage block)", u.TotalTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestSubmodel_DoResponse_Stream_AccumulatesAndReturnsUsage(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2SubmodelCtx()
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: "openai",
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	body := `data: {"id":"c1","model":"deepseek-ai/DeepSeek-V3.1","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"deepseek-ai/DeepSeek-V3.1","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n" +
		"data: [DONE]\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage from streamed response, got %T", usage)
	}
	if u.TotalTokens != 10 {
		t.Errorf("TotalTokens = %d, want 10 (real upstream usage from the streamed data event)", u.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("client-facing SSE stream should forward [DONE], got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestSubmodel_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "submodel" {
		t.Errorf("GetChannelName() = %q, want submodel", a.GetChannelName())
	}
	list := a.GetModelList()
	if len(list) != 10 {
		t.Fatalf("GetModelList() len = %d, want 10", len(list))
	}
}
