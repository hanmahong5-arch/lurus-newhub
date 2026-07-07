package aws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newRelayInfo(apiKey, upstreamModelName string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            apiKey,
			UpstreamModelName: upstreamModelName,
		},
	}
}

func newTestGinContext(body string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c, w
}

// ---------------------------------------------------------------------------
// GetChannelName / GetModelList
// ---------------------------------------------------------------------------

func TestGetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "aws" {
		t.Errorf("GetChannelName() = %q, want %q", got, "aws")
	}
}

func TestGetModelList(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()

	want := make([]string, 0, len(awsModelIDMap))
	for k := range awsModelIDMap {
		want = append(want, k)
	}
	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	t.Run("api key mode with valid api-key|region", func(t *testing.T) {
		a := &Adaptor{}
		info := newRelayInfo("mykey|us-east-1", "claude-3-sonnet-20240229")
		info.ChannelOtherSettings.AwsKeyType = dto.AwsKeyTypeApiKey

		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://bedrock-runtime.anthropic.claude-3-sonnet-20240229-v1:0.amazonaws.com/model/us-east-1/converse"
		if got != want {
			t.Errorf("GetRequestURL() = %q, want %q", got, want)
		}
		if a.ClientMode != ClientModeApiKey {
			t.Errorf("ClientMode = %v, want %v", a.ClientMode, ClientModeApiKey)
		}
	})

	t.Run("api key mode with invalid format returns error", func(t *testing.T) {
		a := &Adaptor{}
		info := newRelayInfo("no-pipe-here", "claude-3-sonnet-20240229")
		info.ChannelOtherSettings.AwsKeyType = dto.AwsKeyTypeApiKey

		got, err := a.GetRequestURL(info)
		if got != "" {
			t.Errorf("expected empty URL, got %q", got)
		}
		wantErr := "invalid aws api key, should be in format of <api-key>|<region>"
		if err == nil || err.Error() != wantErr {
			t.Errorf("error = %v, want %q", err, wantErr)
		}
	})

	t.Run("ak/sk mode returns empty url and sets ClientModeAKSK", func(t *testing.T) {
		a := &Adaptor{}
		info := newRelayInfo("ak|sk|us-east-1", "")
		info.ChannelOtherSettings.AwsKeyType = dto.AwsKeyTypeAKSK

		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("expected empty URL, got %q", got)
		}
		if a.ClientMode != ClientModeAKSK {
			t.Errorf("ClientMode = %v, want %v", a.ClientMode, ClientModeAKSK)
		}
	})
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	t.Run("api key mode sets Authorization header", func(t *testing.T) {
		c, _ := newTestGinContext("", nil)
		a := &Adaptor{ClientMode: ClientModeApiKey}
		info := newRelayInfo("sk-test-key", "")
		header := http.Header{}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test-key")
		}
	})

	t.Run("ak/sk mode does not set Authorization header", func(t *testing.T) {
		c, _ := newTestGinContext("", nil)
		a := &Adaptor{ClientMode: ClientModeAKSK}
		info := newRelayInfo("ak|sk|us-east-1", "")
		header := http.Header{}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("error = %v, want %q", err, "request is nil")
		}
	})

	t.Run("nova model routes to nova request and sets IsNova", func(t *testing.T) {
		a := &Adaptor{}
		req := &dto.GeneralOpenAIRequest{
			Model: "nova-lite-v1:0",
			Messages: []dto.Message{
				{Role: "user", Content: "hello nova"},
			},
		}
		got, err := a.ConvertOpenAIRequest(nil, newRelayInfo("", ""), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		novaReq, ok := got.(*NovaRequest)
		if !ok {
			t.Fatalf("expected *NovaRequest, got %T", got)
		}
		if novaReq.SchemaVersion != "messages-v1" {
			t.Errorf("SchemaVersion = %q, want %q", novaReq.SchemaVersion, "messages-v1")
		}
		if !a.IsNova {
			t.Error("expected IsNova = true")
		}
	})

	t.Run("non-nova model routes to claude conversion", func(t *testing.T) {
		a := &Adaptor{}
		req := &dto.GeneralOpenAIRequest{
			Model: "claude-3-opus-20240229",
			Messages: []dto.Message{
				{Role: "user", Content: "hello claude"},
			},
		}
		info := newRelayInfo("", "")
		got, err := a.ConvertOpenAIRequest(nil, info, req)
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
		if info.UpstreamModelName != claudeReq.Model {
			t.Errorf("info.UpstreamModelName = %q, want %q", info.UpstreamModelName, claudeReq.Model)
		}
		if a.IsNova {
			t.Error("expected IsNova = false for non-nova model")
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertClaudeRequest
// ---------------------------------------------------------------------------

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("string content message is left unchanged", func(t *testing.T) {
		req := &dto.ClaudeRequest{
			Model: "claude-3-opus-20240229",
			Messages: []dto.ClaudeMessage{
				{Role: "user", Content: "plain text"},
			},
		}
		got, err := a.ConvertClaudeRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := got.(*dto.ClaudeRequest)
		if !ok {
			t.Fatalf("expected *dto.ClaudeRequest, got %T", got)
		}
		if result.Messages[0].Content != "plain text" {
			t.Errorf("Content = %v, want %q", result.Messages[0].Content, "plain text")
		}
	})

	t.Run("non-url media source is left unchanged (no network fetch)", func(t *testing.T) {
		req := &dto.ClaudeRequest{
			Model: "claude-3-opus-20240229",
			Messages: []dto.ClaudeMessage{
				{
					Role: "user",
					Content: []any{
						map[string]any{
							"type": "image",
							"source": map[string]any{
								"type":       "base64",
								"media_type": "image/png",
								"data":       "abc123",
							},
						},
					},
				},
			},
		}
		got, err := a.ConvertClaudeRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := got.(*dto.ClaudeRequest)
		if !ok {
			t.Fatalf("expected *dto.ClaudeRequest, got %T", got)
		}
		if result != req {
			t.Errorf("expected the same request pointer to be returned")
		}
	})

	t.Run("unparsable content returns wrapped error", func(t *testing.T) {
		req := &dto.ClaudeRequest{
			Model: "claude-3-opus-20240229",
			Messages: []dto.ClaudeMessage{
				{Role: "user", Content: make(chan int)},
			},
		}
		got, err := a.ConvertClaudeRequest(nil, nil, req)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || !strings.Contains(err.Error(), "failed to parse message content") {
			t.Errorf("error = %v, want to contain %q", err, "failed to parse message content")
		}
	})
}

// ---------------------------------------------------------------------------
// Not-implemented stubs
// ---------------------------------------------------------------------------

func TestNotImplementedStubs(t *testing.T) {
	a := &Adaptor{}

	t.Run("ConvertGeminiRequest", func(t *testing.T) {
		got, err := a.ConvertGeminiRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertAudioRequest", func(t *testing.T) {
		got, err := a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertImageRequest", func(t *testing.T) {
		got, err := a.ConvertImageRequest(nil, nil, dto.ImageRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertEmbeddingRequest", func(t *testing.T) {
		got, err := a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertOpenAIResponsesRequest", func(t *testing.T) {
		got, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})
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
	// Init is a no-op; must not panic on a nil info.
	a.Init(nil)
}

// ---------------------------------------------------------------------------
// isNovaModel / convertToNovaRequest / parseStopSequences
// ---------------------------------------------------------------------------

func TestIsNovaModel(t *testing.T) {
	tests := []struct {
		modelId string
		want    bool
	}{
		{"amazon.nova-micro-v1:0", true},
		{"nova-lite-v1:0", true},
		{"anthropic.claude-3-opus-20240229-v1:0", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isNovaModel(tt.modelId); got != tt.want {
			t.Errorf("isNovaModel(%q) = %v, want %v", tt.modelId, got, tt.want)
		}
	}
}

func TestConvertToNovaRequest(t *testing.T) {
	t.Run("no optional params leaves InferenceConfig nil", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model: "nova-micro-v1:0",
			Messages: []dto.Message{
				{Role: "user", Content: "hi"},
			},
		}
		got := convertToNovaRequest(req)
		if got.SchemaVersion != "messages-v1" {
			t.Errorf("SchemaVersion = %q, want %q", got.SchemaVersion, "messages-v1")
		}
		if len(got.Messages) != 1 || got.Messages[0].Role != "user" || got.Messages[0].Content[0].Text != "hi" {
			t.Errorf("Messages = %+v, unexpected", got.Messages)
		}
		if got.InferenceConfig != nil {
			t.Errorf("expected nil InferenceConfig, got %+v", got.InferenceConfig)
		}
	})

	t.Run("all optional params populate InferenceConfig", func(t *testing.T) {
		temp := 0.5
		req := &dto.GeneralOpenAIRequest{
			Model:       "nova-pro-v1:0",
			MaxTokens:   100,
			Temperature: &temp,
			TopP:        0.8,
			TopK:        20,
			Stop:        "STOP",
			Messages: []dto.Message{
				{Role: "assistant", Content: "yo"},
			},
		}
		got := convertToNovaRequest(req)
		if got.InferenceConfig == nil {
			t.Fatal("expected non-nil InferenceConfig")
		}
		if got.InferenceConfig.MaxTokens != 100 {
			t.Errorf("MaxTokens = %d, want 100", got.InferenceConfig.MaxTokens)
		}
		if got.InferenceConfig.Temperature != 0.5 {
			t.Errorf("Temperature = %v, want 0.5", got.InferenceConfig.Temperature)
		}
		if got.InferenceConfig.TopP != 0.8 {
			t.Errorf("TopP = %v, want 0.8", got.InferenceConfig.TopP)
		}
		if got.InferenceConfig.TopK != 20 {
			t.Errorf("TopK = %d, want 20", got.InferenceConfig.TopK)
		}
		if len(got.InferenceConfig.StopSequences) != 1 || got.InferenceConfig.StopSequences[0] != "STOP" {
			t.Errorf("StopSequences = %v, want [STOP]", got.InferenceConfig.StopSequences)
		}
	})

	t.Run("zero temperature pointer does not populate InferenceConfig by itself", func(t *testing.T) {
		zero := 0.0
		req := &dto.GeneralOpenAIRequest{
			Model:       "nova-micro-v1:0",
			Temperature: &zero,
		}
		got := convertToNovaRequest(req)
		if got.InferenceConfig != nil {
			t.Errorf("expected nil InferenceConfig for zero temperature, got %+v", got.InferenceConfig)
		}
	})
}

func TestParseStopSequences(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []string
	}{
		{"nil returns nil", nil, nil},
		{"empty string returns nil", "", nil},
		{"non-empty string returns single-element slice", "STOP", []string{"STOP"}},
		{"string slice returned as-is", []string{"a", "b"}, []string{"a", "b"}},
		{"interface slice filters non-strings", []interface{}{"a", 1, "", "b"}, []string{"a", "b"}},
		{"unsupported type returns nil", 42, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStopSequences(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseStopSequences(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseStopSequences(%v)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatRequest
// ---------------------------------------------------------------------------

func TestFormatRequest(t *testing.T) {
	t.Run("valid body without anthropic-beta header", func(t *testing.T) {
		body := strings.NewReader(`{"model":"claude-3-opus-20240229","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`)
		got, err := formatRequest(context.Background(), body, http.Header{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.AnthropicVersion != "bedrock-2023-05-31" {
			t.Errorf("AnthropicVersion = %q, want %q", got.AnthropicVersion, "bedrock-2023-05-31")
		}
		if got.MaxTokens != 100 {
			t.Errorf("MaxTokens = %d, want 100", got.MaxTokens)
		}
		if got.AnthropicBeta != nil {
			t.Errorf("expected nil AnthropicBeta, got %s", got.AnthropicBeta)
		}
	})

	t.Run("valid body with anthropic-beta header splits into json array", func(t *testing.T) {
		body := strings.NewReader(`{"model":"claude-3-opus-20240229","messages":[]}`)
		header := http.Header{}
		header.Set("anthropic-beta", "feature-a,feature-b")
		got, err := formatRequest(context.Background(), body, header)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `["feature-a","feature-b"]`
		if string(got.AnthropicBeta) != want {
			t.Errorf("AnthropicBeta = %s, want %s", got.AnthropicBeta, want)
		}
	})

	t.Run("invalid json body returns error", func(t *testing.T) {
		body := strings.NewReader(`not-json`)
		got, err := formatRequest(context.Background(), body, http.Header{})
		if err == nil {
			t.Fatal("expected error for invalid json body")
		}
		if got != nil {
			t.Errorf("expected nil result, got %+v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// getAwsModelID / getAwsRegionPrefix / awsModelCanCrossRegion / awsModelCrossRegion
// ---------------------------------------------------------------------------

func TestGetAwsModelID(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"claude-3-opus-20240229", "anthropic.claude-3-opus-20240229-v1:0"},
		{"nova-micro-v1:0", "amazon.nova-micro-v1:0"},
		{"unknown-model-xyz", "unknown-model-xyz"},
	}
	for _, tt := range tests {
		if got := getAwsModelID(tt.in); got != tt.want {
			t.Errorf("getAwsModelID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGetAwsRegionPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"us-east-1", "us"},
		{"eu-west-1", "eu"},
		{"ap-southeast-1", "ap"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := getAwsRegionPrefix(tt.in); got != tt.want {
			t.Errorf("getAwsRegionPrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAwsModelCanCrossRegion(t *testing.T) {
	tests := []struct {
		modelId, prefix string
		want            bool
	}{
		{"anthropic.claude-3-sonnet-20240229-v1:0", "us", true},
		{"anthropic.claude-3-sonnet-20240229-v1:0", "eu", true},
		{"anthropic.claude-3-opus-20240229-v1:0", "eu", false},
		{"unknown-model", "us", false},
	}
	for _, tt := range tests {
		if got := awsModelCanCrossRegion(tt.modelId, tt.prefix); got != tt.want {
			t.Errorf("awsModelCanCrossRegion(%q, %q) = %v, want %v", tt.modelId, tt.prefix, got, tt.want)
		}
	}
}

func TestAwsModelCrossRegion(t *testing.T) {
	tests := []struct {
		modelId, prefix, want string
	}{
		{"anthropic.claude-3-sonnet-20240229-v1:0", "us", "us.anthropic.claude-3-sonnet-20240229-v1:0"},
		{"anthropic.claude-3-sonnet-20240229-v1:0", "eu", "eu.anthropic.claude-3-sonnet-20240229-v1:0"},
		{"anthropic.claude-3-5-sonnet-20241022-v2:0", "ap", "apac.anthropic.claude-3-5-sonnet-20241022-v2:0"},
		{"anthropic.claude-3-sonnet-20240229-v1:0", "unknown-prefix", "anthropic.claude-3-sonnet-20240229-v1:0"},
	}
	for _, tt := range tests {
		if got := awsModelCrossRegion(tt.modelId, tt.prefix); got != tt.want {
			t.Errorf("awsModelCrossRegion(%q, %q) = %q, want %q", tt.modelId, tt.prefix, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// getAwsErrorStatusCode
// ---------------------------------------------------------------------------

type fakeHTTPStatusError struct {
	code int
}

func (e *fakeHTTPStatusError) Error() string     { return "fake http error" }
func (e *fakeHTTPStatusError) HTTPStatusCode() int { return e.code }

func TestGetAwsErrorStatusCode(t *testing.T) {
	t.Run("error with HTTPStatusCode returns that code", func(t *testing.T) {
		err := &fakeHTTPStatusError{code: 418}
		if got := getAwsErrorStatusCode(err); got != 418 {
			t.Errorf("getAwsErrorStatusCode() = %d, want 418", got)
		}
	})

	t.Run("plain error defaults to 500", func(t *testing.T) {
		err := errors.New("plain error")
		if got := getAwsErrorStatusCode(err); got != http.StatusInternalServerError {
			t.Errorf("getAwsErrorStatusCode() = %d, want %d", got, http.StatusInternalServerError)
		}
	})

	t.Run("wrapped HTTPStatusCode error is still detected", func(t *testing.T) {
		err := errorsWrap(&fakeHTTPStatusError{code: 503}, "wrapped")
		if got := getAwsErrorStatusCode(err); got != 503 {
			t.Errorf("getAwsErrorStatusCode() = %d, want 503", got)
		}
	})
}

// errorsWrap avoids importing github.com/pkg/errors solely for this helper
// while still producing an Unwrap-able chain via fmt-style wrapping.
func errorsWrap(err error, msg string) error {
	return &wrappedErr{msg: msg, err: err}
}

type wrappedErr struct {
	msg string
	err error
}

func (w *wrappedErr) Error() string { return w.msg + ": " + w.err.Error() }
func (w *wrappedErr) Unwrap() error { return w.err }

// ---------------------------------------------------------------------------
// newAwsClient
// ---------------------------------------------------------------------------

func TestNewAwsClient(t *testing.T) {
	t.Run("invalid secret (single part) returns error", func(t *testing.T) {
		c, _ := newTestGinContext("", nil)
		info := newRelayInfo("only-one-part", "")
		client, err := newAwsClient(c, info)
		if client != nil {
			t.Errorf("expected nil client, got %v", client)
		}
		if err == nil || err.Error() != "invalid aws secret key" {
			t.Errorf("error = %v, want %q", err, "invalid aws secret key")
		}
	})

	t.Run("2-part secret (bearer token mode) builds a client", func(t *testing.T) {
		c, _ := newTestGinContext("", nil)
		info := newRelayInfo("bearer-token|us-east-1", "")
		client, err := newAwsClient(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client")
		}
		if client.Options().Region != "us-east-1" {
			t.Errorf("Region = %q, want %q", client.Options().Region, "us-east-1")
		}
	})

	t.Run("3-part secret (ak/sk mode) builds a client", func(t *testing.T) {
		c, _ := newTestGinContext("", nil)
		info := newRelayInfo("ak|sk|eu-west-1", "")
		client, err := newAwsClient(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client")
		}
		if client.Options().Region != "eu-west-1" {
			t.Errorf("Region = %q, want %q", client.Options().Region, "eu-west-1")
		}
	})

	t.Run("invalid proxy URL returns wrapped error", func(t *testing.T) {
		c, _ := newTestGinContext("", nil)
		info := newRelayInfo("ak|sk|us-east-1", "")
		info.ChannelSetting.Proxy = "://not-a-valid-proxy"
		client, err := newAwsClient(c, info)
		if client != nil {
			t.Errorf("expected nil client, got %v", client)
		}
		if err == nil || !strings.Contains(err.Error(), "new proxy http client failed") {
			t.Errorf("error = %v, want to contain %q", err, "new proxy http client failed")
		}
	})
}

// ---------------------------------------------------------------------------
// buildAwsRequestBody
// ---------------------------------------------------------------------------

func TestBuildAwsRequestBody(t *testing.T) {
	t.Run("pass-through disabled marshals the claude request as-is", func(t *testing.T) {
		info := newRelayInfo("", "")
		awsReq := &AwsClaudeRequest{AnthropicVersion: "bedrock-2023-05-31", MaxTokens: 42}

		got, err := buildAwsRequestBody(nil, info, awsReq)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want, err := common.Marshal(awsReq)
		if err != nil {
			t.Fatalf("unexpected marshal error: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("buildAwsRequestBody() = %s, want %s", got, want)
		}
	})

	t.Run("pass-through enabled strips model/stream and forwards the raw body", func(t *testing.T) {
		oldMax := constant.MaxRequestBodyMB
		constant.MaxRequestBodyMB = -1
		defer func() { constant.MaxRequestBodyMB = oldMax }()

		c, _ := newTestGinContext(`{"model":"claude-3-opus-20240229","stream":true,"foo":"bar"}`, nil)
		info := newRelayInfo("", "")
		info.ChannelSetting.PassThroughBodyEnabled = true

		got, err := buildAwsRequestBody(c, info, &AwsClaudeRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var data map[string]interface{}
		if err := common.Unmarshal(got, &data); err != nil {
			t.Fatalf("unexpected unmarshal error: %v", err)
		}
		if _, ok := data["model"]; ok {
			t.Errorf("expected 'model' key to be stripped, got %v", data)
		}
		if _, ok := data["stream"]; ok {
			t.Errorf("expected 'stream' key to be stripped, got %v", data)
		}
		if data["foo"] != "bar" {
			t.Errorf("foo = %v, want %q", data["foo"], "bar")
		}
	})

	t.Run("pass-through enabled with non-object body returns wrapped error", func(t *testing.T) {
		oldMax := constant.MaxRequestBodyMB
		constant.MaxRequestBodyMB = -1
		defer func() { constant.MaxRequestBodyMB = oldMax }()

		c, _ := newTestGinContext(`[1,2,3]`, nil)
		info := newRelayInfo("", "")
		info.ChannelSetting.PassThroughBodyEnabled = true

		got, err := buildAwsRequestBody(c, info, &AwsClaudeRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %s", got)
		}
		if err == nil || !strings.Contains(err.Error(), "pass-through unmarshal request body fail") {
			t.Errorf("error = %v, want to contain %q", err, "pass-through unmarshal request body fail")
		}
	})
}

// ---------------------------------------------------------------------------
// doAwsClientRequest (hermetic paths: client + request assembly only, no
// network call is made by this function — the actual AWS invocation happens
// later in awsHandler/awsStreamHandler/handleNovaRequest, which require a
// live AWS endpoint and are therefore out of scope for hermetic testing).
// ---------------------------------------------------------------------------

func TestDoAwsClientRequest(t *testing.T) {
	t.Run("invalid aws secret returns channel client error", func(t *testing.T) {
		c, _ := newTestGinContext("", nil)
		info := newRelayInfo("bad-secret", "claude-3-opus-20240229")
		a := &Adaptor{}
		got, err := doAwsClientRequest(c, info, a, strings.NewReader(`{}`))
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("non-nova non-stream request builds InvokeModelInput", func(t *testing.T) {
		body := `{"model":"claude-3-opus-20240229","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`
		c, _ := newTestGinContext(body, nil)
		info := newRelayInfo("bearer-token|us-east-1", "claude-3-opus-20240229")
		info.IsStream = false
		a := &Adaptor{}
		got, err := doAwsClientRequest(c, info, a, strings.NewReader(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if a.AwsClient == nil {
			t.Error("expected a.AwsClient to be set")
		}
		if a.AwsReq == nil {
			t.Error("expected a.AwsReq to be set")
		}
	})

	t.Run("non-nova stream request builds InvokeModelWithResponseStreamInput", func(t *testing.T) {
		body := `{"model":"claude-3-opus-20240229","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`
		c, _ := newTestGinContext(body, nil)
		info := newRelayInfo("bearer-token|us-east-1", "claude-3-opus-20240229")
		info.IsStream = true
		a := &Adaptor{}
		got, err := doAwsClientRequest(c, info, a, strings.NewReader(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if a.AwsReq == nil {
			t.Error("expected a.AwsReq to be set")
		}
	})

	t.Run("nova model request decodes and marshals without error", func(t *testing.T) {
		body := `{"schemaVersion":"messages-v1","messages":[{"role":"user","content":[{"text":"hi"}]}]}`
		c, _ := newTestGinContext(body, nil)
		info := newRelayInfo("bearer-token|us-east-1", "nova-micro-v1:0")
		a := &Adaptor{}
		got, err := doAwsClientRequest(c, info, a, strings.NewReader(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
	})

	t.Run("malformed non-nova request body returns bad request error", func(t *testing.T) {
		c, _ := newTestGinContext("not-json", nil)
		info := newRelayInfo("bearer-token|us-east-1", "claude-3-opus-20240229")
		a := &Adaptor{}
		got, err := doAwsClientRequest(c, info, a, strings.NewReader("not-json"))
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil {
			t.Fatal("expected error for malformed request body")
		}
	})

	t.Run("malformed nova request body returns bad request error", func(t *testing.T) {
		c, _ := newTestGinContext("not-json", nil)
		info := newRelayInfo("bearer-token|us-east-1", "nova-micro-v1:0")
		a := &Adaptor{}
		got, err := doAwsClientRequest(c, info, a, strings.NewReader("not-json"))
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil {
			t.Fatal("expected error for malformed nova request body")
		}
	})
}

// ---------------------------------------------------------------------------
// DoRequest dispatch (no live network reached in the ClientModeAKSK branch)
// ---------------------------------------------------------------------------

func TestDoRequestDispatchesToAwsClientPathWhenNotApiKeyMode(t *testing.T) {
	body := `{"model":"claude-3-opus-20240229","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`
	c, _ := newTestGinContext(body, nil)
	info := newRelayInfo("bearer-token|us-east-1", "claude-3-opus-20240229")
	a := &Adaptor{ClientMode: ClientModeAKSK}
	got, err := a.DoRequest(c, info, strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if a.AwsClient == nil {
		t.Error("expected a.AwsClient to be set via doAwsClientRequest path")
	}
}
