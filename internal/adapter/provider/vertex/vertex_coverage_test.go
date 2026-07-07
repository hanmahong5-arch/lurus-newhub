package vertex

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/claude"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/gemini"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"

	"github.com/golang-jwt/jwt/v5"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---- GetModelRegion ----

func TestGetModelRegion(t *testing.T) {
	cases := []struct {
		name     string
		other    string
		local    string
		expected string
	}{
		{"plain-region-string", "us-central1", "any-model", "us-central1"},
		{"json-with-model-key", `{"model-a":"asia-east1"}`, "model-a", "asia-east1"},
		{"json-without-model-fallback-default", `{"default":"us-west1"}`, "model-b", "us-west1"},
		{"json-without-model-or-default", `{"foo":"bar"}`, "model-b", "global"},
		{"empty-string", "", "model-b", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GetModelRegion(tc.other, tc.local)
			if got != tc.expected {
				t.Fatalf("GetModelRegion(%q, %q) = %q, want %q", tc.other, tc.local, got, tc.expected)
			}
		})
	}
}

// ---- GetChannelName / GetModelList ----

func TestGetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "vertex-ai" {
		t.Fatalf("GetChannelName() = %q, want %q", got, "vertex-ai")
	}
	if ChannelName != "vertex-ai" {
		t.Fatalf("ChannelName = %q, want %q", ChannelName, "vertex-ai")
	}
}

func TestGetModelList(t *testing.T) {
	a := &Adaptor{}
	list := a.GetModelList()
	wantLen := len(ModelList) + len(claude.ModelList) + len(gemini.ModelList)
	if len(list) != wantLen {
		t.Fatalf("GetModelList() len = %d, want %d", len(list), wantLen)
	}
	found := map[string]bool{}
	for _, m := range list {
		found[m] = true
	}
	if !found["meta/llama3-405b-instruct-maas"] {
		t.Fatalf("expected vertex own model list entry in GetModelList() result: %v", list)
	}
	for _, m := range claude.ModelList {
		if !found[m] {
			t.Fatalf("expected claude model %q present in GetModelList()", m)
		}
	}
	for _, m := range gemini.ModelList {
		if !found[m] {
			t.Fatalf("expected gemini model %q present in GetModelList()", m)
		}
	}
}

// ---- Init (RequestMode dispatch) ----

func TestInit(t *testing.T) {
	cases := []struct {
		name      string
		modelName string
		want      int
	}{
		{"claude-prefixed", "claude-3-opus-20240229", RequestModeClaude},
		{"llama-substring", "meta/llama3-405b-instruct", RequestModeLlama},
		{"maas-suffix-substring", "some-open-source-maas", RequestModeLlama},
		{"gemini-default", "gemini-2.0-flash", RequestModeGemini},
		{"unrelated-defaults-to-gemini", "text-bison", RequestModeGemini},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adaptor{}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tc.modelName}}
			a.Init(info)
			if a.RequestMode != tc.want {
				t.Fatalf("Init(%q) RequestMode = %d, want %d", tc.modelName, a.RequestMode, tc.want)
			}
		})
	}
}

// ---- not-implemented stubs ----

func TestConvertAudioRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	r, err := a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
	if r != nil {
		t.Fatalf("expected nil reader, got %v", r)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertEmbeddingRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	res, err := a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	if res != nil {
		t.Fatalf("expected nil result, got %v", res)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertOpenAIResponsesRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	res, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
	if res != nil {
		t.Fatalf("expected nil result, got %v", res)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertRerankRequest_AlwaysNilNil(t *testing.T) {
	a := &Adaptor{}
	res, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if res != nil || err != nil {
		t.Fatalf("ConvertRerankRequest() = (%v, %v), want (nil, nil)", res, err)
	}
}

// ---- removeFunctionResponseID / ConvertGeminiRequest ----

func TestRemoveFunctionResponseID_NilRequest(t *testing.T) {
	// must not panic on nil
	removeFunctionResponseID(nil)
}

func TestRemoveFunctionResponseID_StripsID(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Parts: []dto.GeminiPart{
					{FunctionResponse: &dto.GeminiFunctionResponse{ID: []byte(`"abc"`)}},
					{FunctionResponse: nil},                                  // guarded, no-op
					{FunctionResponse: &dto.GeminiFunctionResponse{ID: nil}}, // already empty, no-op
				},
			},
			{Parts: nil}, // guarded empty-parts branch
		},
		Requests: []dto.GeminiChatRequest{
			{
				Contents: []dto.GeminiChatContent{
					{Parts: []dto.GeminiPart{{FunctionResponse: &dto.GeminiFunctionResponse{ID: []byte(`"nested"`)}}}},
				},
			},
		},
	}
	removeFunctionResponseID(req)

	if req.Contents[0].Parts[0].FunctionResponse.ID != nil {
		t.Fatalf("expected top-level FunctionResponse.ID stripped, got %v", req.Contents[0].Parts[0].FunctionResponse.ID)
	}
	if req.Contents[0].Parts[2].FunctionResponse.ID != nil {
		t.Fatalf("expected already-nil ID to remain nil")
	}
	if req.Requests[0].Contents[0].Parts[0].FunctionResponse.ID != nil {
		t.Fatalf("expected nested batch request FunctionResponse.ID stripped, got %v", req.Requests[0].Contents[0].Parts[0].FunctionResponse.ID)
	}
}

func TestConvertGeminiRequest_RemovesIDWhenEnabled(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	orig := settings.RemoveFunctionResponseIdEnabled
	defer func() { settings.RemoveFunctionResponseIdEnabled = orig }()
	settings.RemoveFunctionResponseIdEnabled = true

	a := &Adaptor{}
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Parts: []dto.GeminiPart{{FunctionResponse: &dto.GeminiFunctionResponse{ID: []byte(`"abc"`)}}}},
		},
	}
	res, err := a.ConvertGeminiRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := res.(*dto.GeminiChatRequest)
	if !ok {
		t.Fatalf("expected *dto.GeminiChatRequest, got %T", res)
	}
	if got.Contents[0].Parts[0].FunctionResponse.ID != nil {
		t.Fatalf("expected FunctionResponse.ID stripped when enabled")
	}
}

func TestConvertGeminiRequest_KeepsIDWhenDisabled(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	orig := settings.RemoveFunctionResponseIdEnabled
	defer func() { settings.RemoveFunctionResponseIdEnabled = orig }()
	settings.RemoveFunctionResponseIdEnabled = false

	a := &Adaptor{}
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Parts: []dto.GeminiPart{{FunctionResponse: &dto.GeminiFunctionResponse{ID: []byte(`"abc"`)}}}},
		},
	}
	res, err := a.ConvertGeminiRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := res.(*dto.GeminiChatRequest)
	if string(got.Contents[0].Parts[0].FunctionResponse.ID) != `"abc"` {
		t.Fatalf("expected FunctionResponse.ID preserved when disabled, got %v", got.Contents[0].Parts[0].FunctionResponse.ID)
	}
}

// ---- ConvertImageRequest passthrough to gemini ----

func TestConvertImageRequest_DelegatesAndRejectsNonImagenModel(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	_, err := a.ConvertImageRequest(nil, info, dto.ImageRequest{Prompt: "a cat"})
	if err == nil || err.Error() != "not supported model for image generation" {
		t.Fatalf("expected delegated gemini error, got %v", err)
	}
}

// ---- ConvertClaudeRequest ----

func TestConvertClaudeRequest_MapsKnownModel(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-5-sonnet-20241022"}}
	req := &dto.ClaudeRequest{Model: "claude-3-5-sonnet-20241022", MaxTokens: 100}

	res, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vr, ok := res.(*VertexAIClaudeRequest)
	if !ok {
		t.Fatalf("expected *VertexAIClaudeRequest, got %T", res)
	}
	if vr.AnthropicVersion != anthropicVersion {
		t.Fatalf("AnthropicVersion = %q, want %q", vr.AnthropicVersion, anthropicVersion)
	}
	if vr.MaxTokens != 100 {
		t.Fatalf("MaxTokens = %d, want 100", vr.MaxTokens)
	}
	if got, _ := c.Get("request_model"); got != "claude-3-5-sonnet-v2@20241022" {
		t.Fatalf("request_model = %v, want mapped vertex claude model name", got)
	}
}

func TestConvertClaudeRequest_UnknownModelFallsBackToRequestModel(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-unknown-999"}}
	req := &dto.ClaudeRequest{Model: "claude-unknown-999"}

	_, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := c.Get("request_model"); got != "claude-unknown-999" {
		t.Fatalf("request_model = %v, want fallback to request.Model", got)
	}
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	_, err := a.ConvertOpenAIRequest(nil, nil, nil)
	if err == nil || err.Error() != "request is nil" {
		t.Fatalf("expected 'request is nil' error, got %v", err)
	}
}

func TestConvertOpenAIRequest_LlamaPassthrough(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeLlama}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "meta/llama3-405b-instruct-maas"}}
	req := &dto.GeneralOpenAIRequest{Model: "meta/llama3-405b-instruct-maas"}
	res, err := a.ConvertOpenAIRequest(nil, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != req {
		t.Fatalf("expected llama mode to pass request through unchanged, got %v", res)
	}
}

func TestConvertOpenAIRequest_ImagenPromptRequiredError(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0"}}
	req := &dto.GeneralOpenAIRequest{
		Model:    "imagen-3.0",
		Messages: []dto.Message{{Role: "system", Content: "no user turn"}},
		Prompt:   42, // not a string
	}
	_, err := a.ConvertOpenAIRequest(c, info, req)
	if err == nil || err.Error() != "prompt is required for image generation" {
		t.Fatalf("expected 'prompt is required for image generation' error, got %v", err)
	}
}

func TestConvertOpenAIRequest_ImagenSuccessWithExtraBody(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0"}}
	extra, _ := json.Marshal(map[string]any{
		"n":           float64(3),
		"aspectRatio": "16:9",
	})
	req := &dto.GeneralOpenAIRequest{
		Model:     "imagen-3.0",
		Messages:  []dto.Message{{Role: "user", Content: "a cat riding a bike"}},
		ExtraBody: extra,
	}
	res, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil converted image request")
	}
	if got, _ := c.Get("request_model"); got != "imagen-3.0" {
		t.Fatalf("request_model = %v, want %q", got, "imagen-3.0")
	}
}

func TestConvertOpenAIRequest_ImagenPromptFallsBackToTopLevelPrompt(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0"}}
	req := &dto.GeneralOpenAIRequest{
		Model:  "imagen-3.0",
		Prompt: "a top-level prompt string",
		N:      2,
		Size:   "1536x1024",
	}
	res, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil converted image request")
	}
}

func TestConvertOpenAIRequest_ClaudeMode(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeClaude}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-5-sonnet-20241022"}}
	req := &dto.GeneralOpenAIRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}
	res, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := res.(*VertexAIClaudeRequest); !ok {
		t.Fatalf("expected *VertexAIClaudeRequest, got %T", res)
	}
	if info.UpstreamModelName != "claude-3-5-sonnet-20241022" {
		t.Fatalf("expected info.UpstreamModelName synced from converted claude request, got %q", info.UpstreamModelName)
	}
}

// ---- GetRequestURL / getRequestUrl ----

func newRelayInfoAPIKey(requestMode int, apiVersion, upstream, apiKey string, isStream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		IsStream: isStream,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiVersion:        apiVersion,
			ApiKey:            apiKey,
			UpstreamModelName: upstream,
			ChannelOtherSettings: dto.ChannelOtherSettings{
				VertexKeyType: dto.VertexKeyTypeAPIKey,
			},
		},
	}
}

func TestGetRequestURL_APIKeyMode_GeminiGlobal(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := newRelayInfoAPIKey(RequestModeGemini, "global", "gemini-2.0-flash", "APIKEY123", false)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.0-flash:generateContent?key=APIKEY123"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}
}

func TestGetRequestURL_APIKeyMode_GeminiRegionalStream(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := newRelayInfoAPIKey(RequestModeGemini, "us-central1", "gemini-2.0-flash", "APIKEY123", true)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://us-central1-aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.0-flash:streamGenerateContent?alt=sse&key=APIKEY123"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}
}

func TestGetRequestURL_APIKeyMode_ImagenModelUsesPredictSuffix(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := newRelayInfoAPIKey(RequestModeGemini, "global", "imagen-3.0", "APIKEY123", true)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://aiplatform.googleapis.com/v1/publishers/google/models/imagen-3.0:predict?key=APIKEY123"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}
}

func TestGetRequestURL_ThinkingSuffixVariants(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	origEnabled := settings.ThinkingAdapterEnabled
	defer func() { settings.ThinkingAdapterEnabled = origEnabled }()
	settings.ThinkingAdapterEnabled = true

	cases := []struct {
		name      string
		upstream  string
		wantModel string
	}{
		{"new-thinking-budget-format", "gemini-2.0-flash-thinking-1024", "gemini-2.0-flash"},
		{"old-thinking-suffix", "gemini-2.0-flash-thinking", "gemini-2.0-flash"},
		{"nothinking-suffix", "gemini-2.0-flash-nothinking", "gemini-2.0-flash"},
		{"effort-suffix-high", "gemini-2.0-flash-high", "gemini-2.0-flash"},
		{"no-suffix-unchanged", "gemini-2.0-flash", "gemini-2.0-flash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adaptor{RequestMode: RequestModeGemini}
			info := newRelayInfoAPIKey(RequestModeGemini, "global", tc.upstream, "KEY", false)
			url, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantModelPart := tc.wantModel + ":generateContent"
			if !strings.Contains(url, wantModelPart) {
				t.Fatalf("GetRequestURL() = %q, want it to contain %q", url, wantModelPart)
			}
		})
	}
}

func TestGetRequestURL_APIKeyMode_ClaudeMappedModel(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeClaude}
	info := newRelayInfoAPIKey(RequestModeClaude, "global", "claude-3-sonnet-20240229", "APIKEY123", false)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://aiplatform.googleapis.com/v1/publishers/google/models/claude-3-sonnet@20240229:rawPredict?key=APIKEY123"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}
}

func TestGetRequestURL_APIKeyMode_ClaudeStream(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeClaude}
	info := newRelayInfoAPIKey(RequestModeClaude, "global", "claude-3-sonnet-20240229", "APIKEY123", true)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://aiplatform.googleapis.com/v1/publishers/google/models/claude-3-sonnet@20240229:streamRawPredict?alt=sse&key=APIKEY123"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}
}

func TestGetRequestURL_APIKeyMode_LlamaMode(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeLlama}
	info := newRelayInfoAPIKey(RequestModeLlama, "us-east4", "meta/llama3-405b-instruct-maas", "APIKEY123", false)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://us-east4-aiplatform.googleapis.com/v1/publishers/google/models/:?key=APIKEY123"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}
}

func TestGetRequestURL_UnsupportedRequestMode(t *testing.T) {
	a := &Adaptor{RequestMode: 0}
	info := newRelayInfoAPIKey(0, "global", "some-model", "APIKEY123", false)
	url, err := a.GetRequestURL(info)
	if url != "" {
		t.Fatalf("expected empty url, got %q", url)
	}
	if err == nil || err.Error() != "unsupported request mode" {
		t.Fatalf("expected 'unsupported request mode' error, got %v", err)
	}
}

// ---- JSON credentials mode (still hermetic: only json.Unmarshal, no crypto/network) ----

func newRelayInfoJSONCreds(apiVersion, upstream, apiKey string, isStream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		IsStream: isStream,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiVersion:        apiVersion,
			ApiKey:            apiKey,
			UpstreamModelName: upstream,
			// zero-value ChannelOtherSettings.VertexKeyType != VertexKeyTypeAPIKey
		},
	}
}

func TestGetRequestURL_JSONCredsMode_InvalidCredentialsJSON(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := newRelayInfoJSONCreds("global", "gemini-2.0-flash", "{not valid json", false)
	url, err := a.GetRequestURL(info)
	if url != "" {
		t.Fatalf("expected empty url on decode failure, got %q", url)
	}
	if err == nil || !strings.Contains(err.Error(), "failed to decode credentials file") {
		t.Fatalf("expected credentials decode error, got %v", err)
	}
}

func TestGetRequestURL_JSONCredsMode_GeminiGlobalAndRegional(t *testing.T) {
	creds := `{"project_id":"proj1"}`

	a := &Adaptor{RequestMode: RequestModeGemini}
	info := newRelayInfoJSONCreds("global", "gemini-2.0-flash", creds, false)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://aiplatform.googleapis.com/v1/projects/proj1/locations/global/publishers/google/models/gemini-2.0-flash:generateContent"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}
	if a.AccountCredentials.ProjectID != "proj1" {
		t.Fatalf("expected AccountCredentials.ProjectID populated, got %q", a.AccountCredentials.ProjectID)
	}

	a2 := &Adaptor{RequestMode: RequestModeGemini}
	info2 := newRelayInfoJSONCreds("us-central1", "gemini-2.0-flash", creds, true)
	url2, err := a2.GetRequestURL(info2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want2 := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj1/locations/us-central1/publishers/google/models/gemini-2.0-flash:streamGenerateContent?alt=sse"
	if url2 != want2 {
		t.Fatalf("GetRequestURL() = %q, want %q", url2, want2)
	}
}

func TestGetRequestURL_JSONCredsMode_ClaudeGlobalAndRegional(t *testing.T) {
	creds := `{"project_id":"proj1"}`

	a := &Adaptor{RequestMode: RequestModeClaude}
	info := newRelayInfoJSONCreds("global", "claude-3-opus-20240229", creds, false)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://aiplatform.googleapis.com/v1/projects/proj1/locations/global/publishers/anthropic/models/claude-3-opus@20240229:rawPredict"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}

	a2 := &Adaptor{RequestMode: RequestModeClaude}
	info2 := newRelayInfoJSONCreds("europe-west1", "claude-3-opus-20240229", creds, true)
	url2, err := a2.GetRequestURL(info2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want2 := "https://europe-west1-aiplatform.googleapis.com/v1/projects/proj1/locations/europe-west1/publishers/anthropic/models/claude-3-opus@20240229:streamRawPredict?alt=sse"
	if url2 != want2 {
		t.Fatalf("GetRequestURL() = %q, want %q", url2, want2)
	}
}

func TestGetRequestURL_JSONCredsMode_LlamaMode(t *testing.T) {
	creds := `{"project_id":"proj1"}`
	a := &Adaptor{RequestMode: RequestModeLlama}
	info := newRelayInfoJSONCreds("us-east4", "meta/llama3-405b-instruct-maas", creds, false)
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://us-east4-aiplatform.googleapis.com/v1beta1/projects/proj1/locations/us-east4/endpoints/openapi/chat/completions"
	if url != want {
		t.Fatalf("GetRequestURL() = %q, want %q", url, want)
	}
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader_APIKeyModeSkipsAccessToken(t *testing.T) {
	c, _ := newTestGinContext()
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("anthropic-version", "2024-01-01")

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		IsStream: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-2.0-flash",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				VertexKeyType: dto.VertexKeyTypeAPIKey,
			},
		},
	}
	req := &http.Header{}
	err := a.SetupRequestHeader(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type header not propagated: %q", req.Get("Content-Type"))
	}
	if req.Get("Authorization") != "" {
		t.Fatalf("expected no Authorization header in api-key mode, got %q", req.Get("Authorization"))
	}
	if req.Get("x-goog-user-project") != "" {
		t.Fatalf("expected no x-goog-user-project header without loaded credentials, got %q", req.Get("x-goog-user-project"))
	}
}

func TestSetupRequestHeader_ClaudeModelAddsAnthropicHeaders(t *testing.T) {
	c, _ := newTestGinContext()
	c.Request.Header.Set("anthropic-beta", "beta-flag-1")

	a := &Adaptor{}
	a.AccountCredentials = Credentials{ProjectID: "proj-xyz"}
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-opus-20240229",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-3-opus-20240229",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				VertexKeyType: dto.VertexKeyTypeAPIKey, // avoid network access-token fetch
			},
		},
	}
	req := &http.Header{}
	err := a.SetupRequestHeader(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("x-goog-user-project") != "proj-xyz" {
		t.Fatalf("x-goog-user-project = %q, want %q", req.Get("x-goog-user-project"), "proj-xyz")
	}
	if req.Get("anthropic-beta") != "beta-flag-1" {
		t.Fatalf("anthropic-beta not propagated by CommonClaudeHeadersOperation: %q", req.Get("anthropic-beta"))
	}
}

// ---- DoResponse dispatch (routing only; no live network reachable to exercise further) ----

func TestDoResponse_NonStreamUnknownRequestModeReturnsZeroValues(t *testing.T) {
	a := &Adaptor{RequestMode: 999} // no case matches -> falls through to bare return
	info := &relaycommon.RelayInfo{IsStream: false}
	resp := &http.Response{}
	usage, err := a.DoResponse(nil, resp, info)
	if usage != nil || err != nil {
		t.Fatalf("expected (nil, nil) for unmatched request mode, got (%v, %v)", usage, err)
	}
}

func TestDoResponse_StreamUnknownRequestModeReturnsZeroValues(t *testing.T) {
	a := &Adaptor{RequestMode: 999}
	info := &relaycommon.RelayInfo{IsStream: true}
	resp := &http.Response{}
	usage, err := a.DoResponse(nil, resp, info)
	if usage != nil || err != nil {
		t.Fatalf("expected (nil, nil) for unmatched request mode, got (%v, %v)", usage, err)
	}
}

// ---- createSignedJWT (pure crypto, no network) ----

func genTestRSAKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal PKCS8 key: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block)), key
}

func TestCreateSignedJWT_Success(t *testing.T) {
	pemStr, key := genTestRSAKeyPEM(t)

	signed, err := createSignedJWT("svc-account@example.iam.gserviceaccount.com", pemStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signed == "" {
		t.Fatalf("expected non-empty signed JWT")
	}

	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(signed, &claims, func(token *jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("expected signed JWT to be verifiable with the matching public key: %v", err)
	}
	if claims["iss"] != "svc-account@example.iam.gserviceaccount.com" {
		t.Fatalf("iss claim = %v, want service account email", claims["iss"])
	}
	if claims["scope"] != "https://www.googleapis.com/auth/cloud-platform" {
		t.Fatalf("scope claim = %v, want cloud-platform scope", claims["scope"])
	}
	if claims["aud"] != "https://www.googleapis.com/oauth2/v4/token" {
		t.Fatalf("aud claim = %v, want google token endpoint", claims["aud"])
	}
}

func TestCreateSignedJWT_HandlesEscapedNewlinesAndCRLF(t *testing.T) {
	pemStr, _ := genTestRSAKeyPEM(t)
	// simulate a private key stored as an escaped single-line env var, as commonly
	// happens with service-account JSON pasted into env files.
	escaped := strings.ReplaceAll(pemStr, "\n", "\\n")
	escaped = strings.ReplaceAll(escaped, "-----BEGIN PRIVATE KEY-----\\n", "")
	escaped = strings.ReplaceAll(escaped, "\\n-----END PRIVATE KEY-----\\n", "")

	signed, err := createSignedJWT("svc@example.com", escaped)
	if err != nil {
		t.Fatalf("unexpected error decoding escaped-newline PEM body: %v", err)
	}
	if signed == "" {
		t.Fatalf("expected non-empty signed JWT")
	}
}

func TestCreateSignedJWT_InvalidPEM(t *testing.T) {
	_, err := createSignedJWT("svc@example.com", "not a pem block at all")
	if err == nil || !strings.Contains(err.Error(), "failed to parse PEM block") {
		t.Fatalf("expected PEM parse error, got %v", err)
	}
}

func TestCreateSignedJWT_MalformedPKCS8Body(t *testing.T) {
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-valid-asn1-der-bytes")}
	body := string(pem.EncodeToMemory(block))
	body = strings.TrimPrefix(body, "-----BEGIN PRIVATE KEY-----\n")
	body = strings.TrimSuffix(body, "-----END PRIVATE KEY-----\n")

	_, err := createSignedJWT("svc@example.com", body)
	if err == nil {
		t.Fatalf("expected ParsePKCS8PrivateKey error for malformed DER bytes")
	}
}

func TestCreateSignedJWT_NonRSAKeyRejected(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal PKCS8 key: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	body := string(pem.EncodeToMemory(block))
	body = strings.TrimPrefix(body, "-----BEGIN PRIVATE KEY-----\n")
	body = strings.TrimSuffix(body, "-----END PRIVATE KEY-----\n")

	_, err = createSignedJWT("svc@example.com", body)
	if err == nil || err.Error() != "not an RSA private key" {
		t.Fatalf("expected 'not an RSA private key' error, got %v", err)
	}
}

// ---- getAccessToken (cache hit is hermetic; cache miss w/ invalid creds fails before any network I/O) ----

func TestGetAccessToken_CacheHitSkipsJWTCreation(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 918273, ChannelIsMultiKey: false},
	}
	cacheKey := fmt.Sprintf("access-token-%d", info.ChannelId)
	Cache.SetDefault(cacheKey, "cached-token-value")

	token, err := getAccessToken(a, info)
	if err != nil {
		t.Fatalf("unexpected error on cache hit: %v", err)
	}
	if token != "cached-token-value" {
		t.Fatalf("token = %q, want cached value", token)
	}
}

func TestGetAccessToken_CacheHitMultiKeyUsesDistinctKey(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 918274, ChannelIsMultiKey: true, ChannelMultiKeyIndex: 3},
	}
	cacheKey := fmt.Sprintf("access-token-%d-%d", info.ChannelId, info.ChannelMultiKeyIndex)
	Cache.SetDefault(cacheKey, "multi-key-cached-token")

	token, err := getAccessToken(a, info)
	if err != nil {
		t.Fatalf("unexpected error on multi-key cache hit: %v", err)
	}
	if token != "multi-key-cached-token" {
		t.Fatalf("token = %q, want multi-key cached value", token)
	}
}

func TestGetAccessToken_CacheMissInvalidCredentialsFailsBeforeNetwork(t *testing.T) {
	a := &Adaptor{AccountCredentials: Credentials{ClientEmail: "svc@example.com", PrivateKey: "not a valid key"}}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 918275},
	}
	_, err := getAccessToken(a, info)
	if err == nil || !strings.Contains(err.Error(), "failed to create signed JWT") {
		t.Fatalf("expected JWT-creation failure surfaced before any network call, got %v", err)
	}
}

// ---- AcquireAccessToken / exchangeJwtForAccessToken*: only the hermetic pre-network
// failure branches are reachable without live I/O (invalid credentials, and proxy-client
// construction failure triggered by a malformed proxy URL). ----

func TestAcquireAccessToken_InvalidCredentialsFailsBeforeNetwork(t *testing.T) {
	_, err := AcquireAccessToken(Credentials{ClientEmail: "svc@example.com", PrivateKey: "garbage"}, "")
	if err == nil || !strings.Contains(err.Error(), "failed to create signed JWT") {
		t.Fatalf("expected JWT-creation failure, got %v", err)
	}
}

func TestAcquireAccessToken_MalformedProxyFailsBeforeNetwork(t *testing.T) {
	pemStr, _ := genTestRSAKeyPEM(t)
	_, err := AcquireAccessToken(Credentials{ClientEmail: "svc@example.com", PrivateKey: pemStr}, "://malformed-proxy")
	if err == nil || !strings.Contains(err.Error(), "new proxy http client failed") {
		t.Fatalf("expected proxy client construction failure, got %v", err)
	}
}

func TestGetAccessToken_CacheMissMalformedProxyFailsBeforeNetwork(t *testing.T) {
	pemStr, _ := genTestRSAKeyPEM(t)
	a := &Adaptor{AccountCredentials: Credentials{ClientEmail: "svc@example.com", PrivateKey: pemStr}}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:      918276,
			ChannelSetting: dto.ChannelSettings{Proxy: "://malformed-proxy"},
		},
	}
	_, err := getAccessToken(a, info)
	if err == nil || !strings.Contains(err.Error(), "failed to exchange JWT for access token") {
		t.Fatalf("expected exchange failure surfaced via proxy client construction error, got %v", err)
	}
}

// ---- SetupRequestHeader: non-API-key mode, cache-hit path is hermetic ----

func TestSetupRequestHeader_JSONCredsModeCacheHitSetsAuthorization(t *testing.T) {
	c, _ := newTestGinContext()

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         918277,
			UpstreamModelName: "gemini-2.0-flash",
			// zero-value VertexKeyType != VertexKeyTypeAPIKey -> goes through getAccessToken
		},
	}
	Cache.SetDefault(fmt.Sprintf("access-token-%d", info.ChannelId), "hermetic-cached-access-token")

	req := &http.Header{}
	err := a.SetupRequestHeader(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("Authorization") != "Bearer hermetic-cached-access-token" {
		t.Fatalf("Authorization = %q, want Bearer hermetic-cached-access-token", req.Get("Authorization"))
	}
}

func TestSetupRequestHeader_JSONCredsModeCacheMissInvalidCredsReturnsError(t *testing.T) {
	c, _ := newTestGinContext()

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         918278, // fresh id, guaranteed cache miss
			UpstreamModelName: "gemini-2.0-flash",
		},
	}
	req := &http.Header{}
	err := a.SetupRequestHeader(c, req, info)
	if err == nil || !strings.Contains(err.Error(), "failed to create signed JWT") {
		t.Fatalf("expected access-token error to propagate from SetupRequestHeader, got %v", err)
	}
}

// ---- getRequestUrl direct: unsupported request mode branch inside the JSON-credentials
// path (unreachable via the public GetRequestURL wrapper, which pre-validates RequestMode) ----

func TestGetRequestUrl_JSONCredsMode_UnsupportedRequestMode(t *testing.T) {
	a := &Adaptor{RequestMode: 99}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiVersion: "global", ApiKey: `{"project_id":"proj1"}`},
	}
	url, err := a.getRequestUrl(info, "some-model", "generateContent")
	if url != "" {
		t.Fatalf("expected empty url, got %q", url)
	}
	if err == nil || err.Error() != "unsupported request mode" {
		t.Fatalf("expected 'unsupported request mode' error, got %v", err)
	}
}

// ---- ConvertOpenAIRequest imagen extra-body branch coverage ----

func TestConvertOpenAIRequest_ImagenExtraBodySizeOverride(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0"}}
	extra, _ := json.Marshal(map[string]any{"size": "1024x1792"})
	req := &dto.GeneralOpenAIRequest{
		Model:     "imagen-3.0",
		Messages:  []dto.Message{{Role: "user", Content: "a dog"}},
		ExtraBody: extra,
	}
	res, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil converted image request")
	}
}

func TestConvertOpenAIRequest_ImagenExtraBodyNestedParametersAspectRatio(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0"}}
	extra, _ := json.Marshal(map[string]any{
		"parameters": map[string]any{"aspectRatio": "9:16"},
	})
	req := &dto.GeneralOpenAIRequest{
		Model:     "imagen-3.0",
		Messages:  []dto.Message{{Role: "user", Content: "a fish"}},
		ExtraBody: extra,
	}
	res, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil converted image request")
	}
}

func TestConvertOpenAIRequest_GeminiModeConversionErrorPropagates(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	req := &dto.GeneralOpenAIRequest{
		Model:     "gemini-2.0-flash",
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: []byte("{not valid json"),
	}
	_, err := a.ConvertOpenAIRequest(c, info, req)
	if err == nil || !strings.Contains(err.Error(), "invalid extra body") {
		t.Fatalf("expected gemini conversion error to propagate, got %v", err)
	}
}

// ---- DoRequest: hermetic pre-network failure (unsupported request mode short-circuits
// before any HTTP call is attempted) ----

func TestDoRequest_UnsupportedModeFailsBeforeNetwork(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: 0}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.DoRequest(c, info, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported request mode") {
		t.Fatalf("expected DoRequest to surface GetRequestURL's unsupported-mode error, got %v", err)
	}
}

// ---- DoResponse dispatch: exercises each switch-case branch. The delegated handlers
// parse an already-materialized *http.Response body (no network I/O of their own), so
// feeding them a deliberately-invalid body lets us hit vertex's own dispatch lines
// hermetically; the downstream parsing logic itself belongs to claude/gemini/openai's
// own test suites. ----

func fakeResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestDoResponse_NonStream_ClaudeBranch(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeClaude}
	info := &relaycommon.RelayInfo{IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus@20240229"}}
	resp := fakeResponse("not valid json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatalf("expected a parse error surfaced through the claude branch, got nil")
	}
}

func TestDoResponse_NonStream_GeminiNativeBranch(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{
		IsStream:  false,
		RelayMode: constant.RelayModeGemini,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-2.0-flash",
		},
	}
	resp := fakeResponse("not valid json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatalf("expected a parse error surfaced through the gemini-native branch, got nil")
	}
}

func TestDoResponse_NonStream_GeminiChatBranch(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{
		IsStream:  false,
		RelayMode: constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-2.0-flash",
		},
	}
	resp := fakeResponse("not valid json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatalf("expected a parse error surfaced through the gemini-chat branch, got nil")
	}
}

func TestDoResponse_NonStream_GeminiImagenBranch(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeGemini}
	info := &relaycommon.RelayInfo{
		IsStream:  false,
		RelayMode: constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "imagen-3.0",
		},
	}
	resp := fakeResponse("not valid json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatalf("expected a parse error surfaced through the gemini-imagen branch, got nil")
	}
}

func TestDoResponse_NonStream_LlamaBranch(t *testing.T) {
	c, _ := newTestGinContext()
	a := &Adaptor{RequestMode: RequestModeLlama}
	info := &relaycommon.RelayInfo{IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "meta/llama3-405b-instruct-maas"}}
	resp := fakeResponse("not valid json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatalf("expected a parse error surfaced through the llama/openai branch, got nil")
	}
}

// NOTE: the IsStream==true branches of DoResponse (claude.ClaudeStreamHandler,
// gemini.Gemini*StreamHandler, openai.OaiStreamHandler) all delegate into
// helper.StreamScannerHandler, which derives a heartbeat time.NewTicker interval from
// operation_setting global config. That config is only populated by the server's real
// startup path, so under a bare `go test` process the ticker interval is zero and the
// call panics ("non-positive interval for NewTicker") regardless of the response body
// content. Exercising these branches hermetically would require either duplicating that
// production init here (out of scope for this package) or the kind of scaffolding this
// task's rules say not to add — so they're left uncovered as a real, documented ceiling.
