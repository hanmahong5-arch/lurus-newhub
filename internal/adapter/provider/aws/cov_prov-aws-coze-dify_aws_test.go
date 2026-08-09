package aws

// Business-acceptance tests for the AWS Bedrock adaptor: protocol translation
// (OpenAI/Claude -> Bedrock Claude/Nova request shapes), model-id/region
// routing (cross-region inference), and response/usage extraction (the
// billing-critical path). Upstream calls are faked with httptest.Server plus
// the real AWS SDK event-stream encoder so the actual wire protocol is
// exercised, not a stand-in.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/gin-gonic/gin"
)

// httpNopCloserForTest / common_MaxRequestBodyMBForTest / restore... let
// buildAwsRequestBody's pass-through branch (which reads c.Request.Body via
// common.GetRequestBody, bounded by constant.MaxRequestBodyMB) run against an
// in-memory body in tests, mirroring the pattern already used by sibling
// provider packages (e.g. openai's multipart tests) for the same env-derived
// zero-value gotcha (MaxRequestBodyMB defaults to 0, which would otherwise
// read zero bytes).
func httpNopCloserForTest(r io.Reader) io.ReadCloser { return io.NopCloser(r) }

func common_MaxRequestBodyMBForTest() int {
	orig := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	return orig
}

func restore_MaxRequestBodyMBForTest(orig int) { constant.MaxRequestBodyMB = orig }

// ---------------------------------------------------------------------------
// test helpers (prefixed to avoid collisions with sibling provider packages)
// ---------------------------------------------------------------------------

func prov_aws_coze_dify_newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, w
}

// prov_aws_coze_dify_newBedrockClient builds a real bedrockruntime.Client
// wired to a local httptest.Server, so InvokeModel/InvokeModelWithResponseStream
// exercise the genuine AWS SDK request-signing/response-parsing path against a
// fake (never external) upstream.
func prov_aws_coze_dify_newBedrockClient(baseURL string) *bedrockruntime.Client {
	base := baseURL
	return bedrockruntime.New(bedrockruntime.Options{
		Region:       "us-east-1",
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
		HTTPClient:   http.DefaultClient,
		BaseEndpoint: &base,
	})
}

// prov_aws_coze_dify_encodeBedrockChunk wraps a Claude-shaped SSE JSON payload
// into a single AWS event-stream "chunk" event frame (the real Bedrock wire
// format: base64 payload nested inside {"bytes": "..."}).
func prov_aws_coze_dify_encodeBedrockChunk(t *testing.T, payloadJSON string) []byte {
	t.Helper()
	inner, err := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(payloadJSON))})
	if err != nil {
		t.Fatalf("marshal inner payload: %v", err)
	}
	buf := &bytes.Buffer{}
	msg := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue("event")},
			{Name: eventstreamapi.EventTypeHeader, Value: eventstream.StringValue("chunk")},
			{Name: eventstreamapi.ContentTypeHeader, Value: eventstream.StringValue("application/json")},
		},
		Payload: inner,
	}
	if err := eventstream.NewEncoder().Encode(buf, msg); err != nil {
		t.Fatalf("encode event: %v", err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// isNovaModel / getAwsModelID
// ---------------------------------------------------------------------------

func TestIsNovaModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"nova-micro-v1:0", true},
		{"amazon.nova-pro-v1:0", true},
		{"claude-3-opus-20240229", false},
		{"", false},
		{"nova", false}, // must contain "nova-" not just "nova"
	}
	for _, tt := range cases {
		if got := isNovaModel(tt.model); got != tt.want {
			t.Errorf("isNovaModel(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestGetAwsModelID(t *testing.T) {
	t.Run("known alias maps to bedrock model id", func(t *testing.T) {
		got := getAwsModelID("claude-3-opus-20240229")
		want := "anthropic.claude-3-opus-20240229-v1:0"
		if got != want {
			t.Errorf("getAwsModelID = %q, want %q", got, want)
		}
	})
	t.Run("unknown model id passes through unchanged", func(t *testing.T) {
		got := getAwsModelID("some-custom-inference-profile-id")
		if got != "some-custom-inference-profile-id" {
			t.Errorf("getAwsModelID = %q, want pass-through unchanged", got)
		}
	})
}

// ---------------------------------------------------------------------------
// getAwsRegionPrefix / awsModelCanCrossRegion / awsModelCrossRegion
// ---------------------------------------------------------------------------

func TestGetAwsRegionPrefix(t *testing.T) {
	cases := []struct {
		region string
		want   string
	}{
		{"us-east-1", "us"},
		{"eu-west-1", "eu"},
		{"", ""},
		{"apeast1", "apeast1"}, // no dash: whole string is the "prefix"
	}
	for _, tt := range cases {
		if got := getAwsRegionPrefix(tt.region); got != tt.want {
			t.Errorf("getAwsRegionPrefix(%q) = %q, want %q", tt.region, got, tt.want)
		}
	}
}

func TestAwsModelCanCrossRegion(t *testing.T) {
	t.Run("known model+region combo allowed", func(t *testing.T) {
		if !awsModelCanCrossRegion("anthropic.claude-3-opus-20240229-v1:0", "us") {
			t.Error("expected claude-3-opus + us to allow cross-region inference")
		}
	})
	t.Run("known model, unsupported region prefix", func(t *testing.T) {
		if awsModelCanCrossRegion("anthropic.claude-3-opus-20240229-v1:0", "eu") {
			t.Error("claude-3-opus only supports us cross-region, eu must be false")
		}
	})
	t.Run("unknown model always false", func(t *testing.T) {
		if awsModelCanCrossRegion("not-a-real-model", "us") {
			t.Error("unknown model must never be treated as cross-region eligible")
		}
	})
}

func TestAwsModelCrossRegion(t *testing.T) {
	cases := []struct {
		name     string
		modelId  string
		prefix   string
		want     string
	}{
		{"us prefix", "anthropic.claude-3-opus-20240229-v1:0", "us", "us.anthropic.claude-3-opus-20240229-v1:0"},
		{"eu prefix", "anthropic.claude-3-opus-20240229-v1:0", "eu", "eu.anthropic.claude-3-opus-20240229-v1:0"},
		{"ap prefix maps to apac", "amazon.nova-pro-v1:0", "ap", "apac.amazon.nova-pro-v1:0"},
		{"unmapped prefix returns unchanged", "anthropic.claude-3-opus-20240229-v1:0", "xx", "anthropic.claude-3-opus-20240229-v1:0"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := awsModelCrossRegion(tt.modelId, tt.prefix); got != tt.want {
				t.Errorf("awsModelCrossRegion(%q,%q) = %q, want %q", tt.modelId, tt.prefix, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getAwsErrorStatusCode
// ---------------------------------------------------------------------------

type prov_aws_coze_dify_httpStatusErr struct{ code int }

func (e *prov_aws_coze_dify_httpStatusErr) Error() string    { return "boom" }
func (e *prov_aws_coze_dify_httpStatusErr) HTTPStatusCode() int { return e.code }

func TestGetAwsErrorStatusCode(t *testing.T) {
	t.Run("error exposing HTTPStatusCode is used verbatim", func(t *testing.T) {
		err := &prov_aws_coze_dify_httpStatusErr{code: 429}
		if got := getAwsErrorStatusCode(err); got != 429 {
			t.Errorf("getAwsErrorStatusCode = %d, want 429 (extracted from wrapped SDK error)", got)
		}
	})
	t.Run("plain error without status code defaults to 500", func(t *testing.T) {
		err := errorsNew("boring transport error")
		if got := getAwsErrorStatusCode(err); got != http.StatusInternalServerError {
			t.Errorf("getAwsErrorStatusCode = %d, want %d fallback", got, http.StatusInternalServerError)
		}
	})
}

// small local helper so this file doesn't need a second stdlib "errors" import
// alias fight with github.com/pkg/errors used elsewhere in the package.
func errorsNew(msg string) error { return &prov_aws_coze_dify_plainErr{msg} }

type prov_aws_coze_dify_plainErr struct{ msg string }

func (e *prov_aws_coze_dify_plainErr) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// convertToNovaRequest / parseStopSequences
// ---------------------------------------------------------------------------

func TestConvertToNovaRequest_MessageMapping(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "user", Content: "hello nova"},
			{Role: "assistant", Content: "hi there"},
		},
	}
	got := convertToNovaRequest(req)
	if got.SchemaVersion != "messages-v1" {
		t.Errorf("SchemaVersion = %q, want messages-v1", got.SchemaVersion)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(got.Messages))
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Content[0].Text != "hello nova" {
		t.Errorf("Messages[0] = %+v, want role=user text=%q", got.Messages[0], "hello nova")
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Content[0].Text != "hi there" {
		t.Errorf("Messages[1] = %+v, want role=assistant text=%q", got.Messages[1], "hi there")
	}
	if got.InferenceConfig != nil {
		t.Errorf("InferenceConfig = %+v, want nil when no inference params were set", got.InferenceConfig)
	}
}

func TestConvertToNovaRequest_InferenceConfigOmittedWhenAllZero(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got := convertToNovaRequest(req)
	if got.InferenceConfig != nil {
		t.Fatalf("InferenceConfig = %+v, want nil (all params zero/unset)", got.InferenceConfig)
	}
}

func TestConvertToNovaRequest_InferenceConfigPopulated(t *testing.T) {
	temp := 0.5
	req := &dto.GeneralOpenAIRequest{
		Messages:    []dto.Message{{Role: "user", Content: "hi"}},
		MaxTokens:   256,
		Temperature: &temp,
		TopP:        0.8,
		TopK:        40,
		Stop:        "STOP",
	}
	got := convertToNovaRequest(req)
	if got.InferenceConfig == nil {
		t.Fatal("InferenceConfig = nil, want populated struct")
	}
	if got.InferenceConfig.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256", got.InferenceConfig.MaxTokens)
	}
	if got.InferenceConfig.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", got.InferenceConfig.Temperature)
	}
	if got.InferenceConfig.TopP != 0.8 {
		t.Errorf("TopP = %v, want 0.8", got.InferenceConfig.TopP)
	}
	if got.InferenceConfig.TopK != 40 {
		t.Errorf("TopK = %d, want 40", got.InferenceConfig.TopK)
	}
	if len(got.InferenceConfig.StopSequences) != 1 || got.InferenceConfig.StopSequences[0] != "STOP" {
		t.Errorf("StopSequences = %v, want [STOP]", got.InferenceConfig.StopSequences)
	}
}

func TestConvertToNovaRequest_ZeroTemperaturePointerNotCounted(t *testing.T) {
	// A caller-supplied *float64 pointing at 0.0 must NOT by itself trigger
	// InferenceConfig construction (business rule: 0 temperature is
	// indistinguishable from "not set" here), unlike MaxTokens which is a
	// plain uint where 0 legitimately means unset too.
	zero := 0.0
	req := &dto.GeneralOpenAIRequest{
		Messages:    []dto.Message{{Role: "user", Content: "hi"}},
		Temperature: &zero,
	}
	got := convertToNovaRequest(req)
	if got.InferenceConfig != nil {
		t.Errorf("InferenceConfig = %+v, want nil: a zero-valued temperature pointer must not force config construction", got.InferenceConfig)
	}
}

func TestParseStopSequences(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"nil", nil, nil},
		{"empty string dropped", "", nil},
		{"single string", "STOP", []string{"STOP"}},
		{"string slice passthrough", []string{"A", "B"}, []string{"A", "B"}},
		{"interface slice filters non-strings and empties", []interface{}{"A", 5, "", "B"}, []string{"A", "B"}},
		{"unsupported type yields nil", 42, nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStopSequences(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseStopSequences(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
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

func TestFormatRequest_SetsAnthropicVersionAlways(t *testing.T) {
	c, _ := prov_aws_coze_dify_newTestContext()
	body := strings.NewReader(`{"messages":[{"role":"user","content":"hi"}],"anthropic_version":"should-be-overwritten"}`)
	got, err := formatRequest(c.Request.Context(), body, http.Header{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AnthropicVersion != "bedrock-2023-05-31" {
		t.Errorf("AnthropicVersion = %q, want forced bedrock-2023-05-31 regardless of client input", got.AnthropicVersion)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Errorf("Messages = %+v, want single user message decoded from body", got.Messages)
	}
}

func TestFormatRequest_AnthropicBetaHeaderForwardedAsJSONArray(t *testing.T) {
	c, _ := prov_aws_coze_dify_newTestContext()
	header := http.Header{}
	header.Set("anthropic-beta", "prompt-caching-2024-07-31,computer-use-2024-10-22")
	got, err := formatRequest(c.Request.Context(), strings.NewReader(`{}`), header)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var betas []string
	if err := json.Unmarshal(got.AnthropicBeta, &betas); err != nil {
		t.Fatalf("AnthropicBeta not valid JSON array: %v (%s)", err, got.AnthropicBeta)
	}
	if len(betas) != 2 || betas[0] != "prompt-caching-2024-07-31" || betas[1] != "computer-use-2024-10-22" {
		t.Errorf("betas = %v, want split+forwarded comma list", betas)
	}
}

func TestFormatRequest_NoAnthropicBetaHeaderLeavesFieldUnset(t *testing.T) {
	c, _ := prov_aws_coze_dify_newTestContext()
	got, err := formatRequest(c.Request.Context(), strings.NewReader(`{}`), http.Header{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AnthropicBeta != nil {
		t.Errorf("AnthropicBeta = %s, want unset when caller sent no header", got.AnthropicBeta)
	}
}

func TestFormatRequest_MalformedJSONBodyErrors(t *testing.T) {
	c, _ := prov_aws_coze_dify_newTestContext()
	_, err := formatRequest(c.Request.Context(), strings.NewReader(`{not-json`), http.Header{})
	if err == nil {
		t.Fatal("expected decode error for malformed JSON body")
	}
}

// ---------------------------------------------------------------------------
// buildAwsRequestBody
// ---------------------------------------------------------------------------

func TestBuildAwsRequestBody_DefaultMarshalsFormattedRequest(t *testing.T) {
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	awsReq := &AwsClaudeRequest{AnthropicVersion: "bedrock-2023-05-31", MaxTokens: 10}
	body, err := buildAwsRequestBody(c, info, awsReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if decoded["max_tokens"] != float64(10) {
		t.Errorf("max_tokens = %v, want 10 (marshalled from the formatted AWS request, not pass-through)", decoded["max_tokens"])
	}
}

func TestBuildAwsRequestBody_ChannelPassThroughStripsModelAndStream(t *testing.T) {
	origMax := common_MaxRequestBodyMBForTest()
	defer restore_MaxRequestBodyMBForTest(origMax)

	c, _ := prov_aws_coze_dify_newTestContext()
	c.Request.Body = httpNopCloserForTest(strings.NewReader(`{"model":"claude-3-opus","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: true}},
	}
	body, err := buildAwsRequestBody(c, info, &AwsClaudeRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if _, has := decoded["model"]; has {
		t.Error("pass-through body must strip 'model' key before forwarding to Bedrock")
	}
	if _, has := decoded["stream"]; has {
		t.Error("pass-through body must strip 'stream' key before forwarding to Bedrock")
	}
	if _, has := decoded["messages"]; !has {
		t.Error("pass-through body must preserve unrelated keys like 'messages'")
	}
}

func TestBuildAwsRequestBody_GlobalPassThroughAlsoStrips(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	orig := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = true
	defer func() { settings.PassThroughRequestEnabled = orig }()

	origMax := common_MaxRequestBodyMBForTest()
	defer restore_MaxRequestBodyMBForTest(origMax)

	c, _ := prov_aws_coze_dify_newTestContext()
	c.Request.Body = httpNopCloserForTest(strings.NewReader(`{"model":"x","stream":false}`))
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body, err := buildAwsRequestBody(c, info, &AwsClaudeRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if _, has := decoded["model"]; has {
		t.Error("global pass-through setting must also strip 'model'")
	}
}

// ---------------------------------------------------------------------------
// Adaptor: GetRequestURL / SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL_ApiKeyMode(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-abcdef|us-east-1",
			UpstreamModelName: "claude-3-opus-20240229",
			ChannelOtherSettings: dto.ChannelOtherSettings{AwsKeyType: dto.AwsKeyTypeApiKey},
		},
	}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://bedrock-runtime.anthropic.claude-3-opus-20240229-v1:0.amazonaws.com/model/us-east-1/converse"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
	if a.ClientMode != ClientModeApiKey {
		t.Errorf("ClientMode = %d, want ClientModeApiKey", a.ClientMode)
	}
}

func TestAdaptor_GetRequestURL_ApiKeyMode_InvalidFormatErrors(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:               "no-pipe-separator",
			ChannelOtherSettings: dto.ChannelOtherSettings{AwsKeyType: dto.AwsKeyTypeApiKey},
		},
	}
	_, err := a.GetRequestURL(info)
	if err == nil {
		t.Fatal("expected error for api-key without <key>|<region> format")
	}
}

func TestAdaptor_GetRequestURL_AKSKMode_ReturnsEmptyURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{AwsKeyType: dto.AwsKeyTypeAKSK}},
	}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("url = %q, want empty string: AK/SK mode dials via the AWS SDK client, not a relay-constructed URL", url)
	}
	if a.ClientMode != ClientModeAKSK {
		t.Errorf("ClientMode = %d, want ClientModeAKSK", a.ClientMode)
	}
}

func TestAdaptor_SetupRequestHeader_ApiKeyModeSetsBearerAuth(t *testing.T) {
	c, _ := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{ClientMode: ClientModeApiKey}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-bearer-secret|us-east-1"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("Authorization") != "Bearer sk-bearer-secret|us-east-1" {
		t.Errorf("Authorization = %q, want Bearer-prefixed api key", header.Get("Authorization"))
	}
}

func TestAdaptor_SetupRequestHeader_AKSKModeDoesNotSetBearerAuth(t *testing.T) {
	c, _ := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{ClientMode: ClientModeAKSK}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "AKID|SECRET|us-east-1"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("Authorization") != "" {
		t.Errorf("Authorization = %q, want unset: AK/SK mode authenticates via SigV4 signing, not a Bearer header", header.Get("Authorization"))
	}
}

// ---------------------------------------------------------------------------
// Adaptor: ConvertOpenAIRequest (Nova dispatch vs Claude dispatch)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest_NilRequestErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestAdaptor_ConvertOpenAIRequest_NovaModelDispatchesToNovaFormat(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	req := &dto.GeneralOpenAIRequest{Model: "nova-lite-v1:0", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.IsNova {
		t.Error("IsNova = false, want true after routing a nova- model through ConvertOpenAIRequest")
	}
	if _, ok := got.(*NovaRequest); !ok {
		t.Fatalf("result type = %T, want *NovaRequest", got)
	}
}

func TestAdaptor_ConvertOpenAIRequest_ClaudeModelDispatchesToClaudeFormat(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "claude-3-opus-20240229", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.IsNova {
		t.Error("IsNova = true, want false for a non-nova model")
	}
	claudeReq, ok := got.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.ClaudeRequest", got)
	}
	if info.UpstreamModelName != claudeReq.Model {
		t.Errorf("info.UpstreamModelName = %q, want propagated claude model %q", info.UpstreamModelName, claudeReq.Model)
	}
}

// ---------------------------------------------------------------------------
// Adaptor: ConvertClaudeRequest (media source url -> base64 rewrite)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertClaudeRequest_StringContentUnchanged(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	req := &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{{Role: "user", Content: "plain text"}}}
	got, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(*dto.ClaudeRequest) != req {
		t.Error("expected the same request pointer returned unmodified for plain string content")
	}
}

func TestAdaptor_ConvertClaudeRequest_UnparsableContentErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	msg := dto.ClaudeMessage{Role: "user"}
	msg.SetContent(42) // not a string, not an array of media blocks
	req := &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{msg}}
	_, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
	if err == nil {
		t.Fatal("expected error parsing unrepresentable message content")
	}
	if !strings.Contains(err.Error(), "failed to parse message content") {
		t.Errorf("error = %q, want to mention content parse failure", err.Error())
	}
}

func TestAdaptor_ConvertClaudeRequest_RemoteImageURLBlockedBySSRFDefault(t *testing.T) {
	// Exercises the "url" media-source branch, which fetches the remote
	// image via app.GetFileBase64FromUrl before rewriting to base64. With
	// default settings (SSRF protection on, private IPs disallowed) a
	// loopback target is rejected -- proving this branch executed (not
	// silently skipped) and that the rejection surfaces as an error rather
	// than a panic or silent data loss.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-a-real-image"))
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	msg := dto.ClaudeMessage{Role: "user"}
	msg.SetContent([]dto.ClaudeMediaMessage{
		{Type: "image", Source: &dto.ClaudeMessageSource{Type: "url", Url: upstream.URL + "/x.png"}},
	})
	req := &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{msg}}
	_, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
	if err == nil {
		t.Fatal("expected SSRF-blocked error for a loopback image url under default settings")
	}
	if !strings.Contains(err.Error(), "get file base64 from url failed") {
		t.Errorf("error = %q, want to mention the url-fetch failure wrapper", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Adaptor: DoRequest AKSK-mode request construction (no real network call --
// doAwsClientRequest only builds the SDK input struct, it does not invoke it)
// ---------------------------------------------------------------------------

func TestAdaptor_DoRequest_AKSKMode_InvalidSecretErrors(t *testing.T) {
	a := &Adaptor{ClientMode: ClientModeAKSK}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "only-one-part"}}
	_, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err == nil {
		t.Fatal("expected error for AK/SK secret missing the ak|sk|region format")
	}
}

func TestAdaptor_DoRequest_AKSKMode_ClaudeNonStreamBuildsRequest(t *testing.T) {
	origMax := common_MaxRequestBodyMBForTest()
	defer restore_MaxRequestBodyMBForTest(origMax)

	a := &Adaptor{ClientMode: ClientModeAKSK}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "AKID|SECRET|us-east-1", UpstreamModelName: "claude-3-opus-20240229"},
	}
	_, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, ok := a.AwsReq.(*bedrockruntime.InvokeModelInput)
	if !ok {
		t.Fatalf("AwsReq type = %T, want *bedrockruntime.InvokeModelInput (non-stream, non-nova)", a.AwsReq)
	}
	// us-east-1 supports claude-3-opus cross-region inference, so the "us."
	// prefix must be applied on top of the base model-id mapping.
	if req.ModelId == nil || *req.ModelId != "us.anthropic.claude-3-opus-20240229-v1:0" {
		t.Errorf("ModelId = %v, want cross-region-prefixed bedrock model id", req.ModelId)
	}
	var decoded map[string]any
	if err := json.Unmarshal(req.Body, &decoded); err != nil {
		t.Fatalf("request body not valid JSON: %v", err)
	}
	if decoded["anthropic_version"] != "bedrock-2023-05-31" {
		t.Errorf("anthropic_version = %v, want bedrock-2023-05-31 forced by formatRequest", decoded["anthropic_version"])
	}
}

func TestAdaptor_DoRequest_AKSKMode_ClaudeStreamBuildsStreamInput(t *testing.T) {
	origMax := common_MaxRequestBodyMBForTest()
	defer restore_MaxRequestBodyMBForTest(origMax)

	a := &Adaptor{ClientMode: ClientModeAKSK}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "AKID|SECRET|us-east-1", UpstreamModelName: "claude-3-opus-20240229"},
		IsStream:    true,
	}
	_, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput); !ok {
		t.Fatalf("AwsReq type = %T, want *bedrockruntime.InvokeModelWithResponseStreamInput when info.IsStream=true", a.AwsReq)
	}
}

// doAwsClientRequest's Nova branch (relay-aws.go) used to build a local
// `awsReq := &bedrockruntime.InvokeModelInput{...}` and drop it on the floor —
// unlike the Claude stream/non-stream branches right below it, it never did
// `a.AwsReq = awsReq`. Every AK/SK Bedrock channel serving a "nova-*" model
// therefore reached DoResponse with a nil a.AwsReq, and the single-return type
// assertion in handleNovaRequest panicked the request goroutine.
//
// fix_nova_awsreq_test.go pins the persisted request's contents and calls
// handleNovaRequest directly; this test keeps the DoResponse dispatch itself
// under coverage — that is the layer where the panic actually surfaced.
func TestAdaptor_DoRequest_AKSKMode_NovaModelPersistsAwsReq(t *testing.T) {
	a := &Adaptor{ClientMode: ClientModeAKSK}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "AKID|SECRET|us-east-1", UpstreamModelName: "nova-lite-v1:0"},
	}
	novaBody := `{"schemaVersion":"messages-v1","messages":[{"role":"user","content":[{"text":"hi"}]}]}`
	_, err := a.DoRequest(c, info, strings.NewReader(novaBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := a.AwsReq.(*bedrockruntime.InvokeModelInput); !ok {
		t.Fatalf("a.AwsReq = %#v, want a *bedrockruntime.InvokeModelInput built by the Nova branch", a.AwsReq)
	}

	// An adaptor that never went through DoRequest still must not panic when
	// DoResponse dispatches into the Nova path — it must fail cleanly.
	unprepared := &Adaptor{ClientMode: ClientModeAKSK, IsNova: true}
	// DoResponse's usage return is `any`, so the typed nil *dto.Usage that
	// handleNovaRequest returns on the error path arrives as a non-nil
	// interface — assert on the error, which is the part that matters here.
	_, apiErr := unprepared.DoResponse(c, nil, info)
	if apiErr == nil {
		t.Fatal("DoResponse with an unprepared a.AwsReq returned no error — the Nova path must fail cleanly, not panic")
	}
}

func TestAdaptor_DoRequest_AKSKMode_NovaMalformedBodyErrors(t *testing.T) {
	a := &Adaptor{ClientMode: ClientModeAKSK}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "AKID|SECRET|us-east-1", UpstreamModelName: "nova-lite-v1:0"},
	}
	_, err := a.DoRequest(c, info, strings.NewReader(`{not-json`))
	if err == nil {
		t.Fatal("expected decode error for malformed nova request body")
	}
}

// ---------------------------------------------------------------------------
// awsHandler / awsStreamHandler / handleNovaRequest -- billing/usage
// extraction against a fake Bedrock upstream (real AWS SDK wire protocol).
// ---------------------------------------------------------------------------

func TestAwsHandler_NonStream_ExtractsUsageForBilling(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":11,"output_tokens":4}}`))
	}))
	defer upstream.Close()

	c, w := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		AwsClient: prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq: &bedrockruntime.InvokeModelInput{
			ModelId: aws.String("anthropic.claude-3-opus-20240229-v1:0"),
			Body:    []byte(`{}`),
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	apiErr, usage := awsHandler(c, info, a)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 11 || usage.CompletionTokens != 4 {
		t.Errorf("usage = %+v, want PromptTokens=11 CompletionTokens=4 (billing-critical extraction from Bedrock response)", usage)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type header = %q, want propagated from upstream", w.Header().Get("Content-Type"))
	}
}

func TestAwsHandler_UpstreamErrorStatusPropagated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"message":"rate limited"}`))
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		AwsClient: prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq:    &bedrockruntime.InvokeModelInput{ModelId: aws.String("anthropic.claude-3-opus-20240229-v1:0"), Body: []byte(`{}`)},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"}}
	apiErr, usage := awsHandler(c, info, a)
	if apiErr == nil {
		t.Fatal("expected an error for a 429 upstream response")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil on upstream error (must not bill a failed call)", usage)
	}
}

func TestAwsStreamHandler_MultiChunk_AccumulatesUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(200)
		_, _ = w.Write(prov_aws_coze_dify_encodeBedrockChunk(t, `{"type":"message_start","message":{"id":"msg_1","model":"claude-3-opus-20240229","usage":{"input_tokens":7,"output_tokens":0}}}`))
		_, _ = w.Write(prov_aws_coze_dify_encodeBedrockChunk(t, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`))
		_, _ = w.Write(prov_aws_coze_dify_encodeBedrockChunk(t, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":3}}`))
	}))
	defer upstream.Close()

	c, w := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		AwsClient: prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq: &bedrockruntime.InvokeModelWithResponseStreamInput{
			ModelId: aws.String("anthropic.claude-3-opus-20240229-v1:0"),
			Body:    []byte(`{}`),
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatOpenAI,
		IsStream:    true,
	}
	apiErr, usage := awsStreamHandler(c, info, a)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	// PromptTokens comes from message_start (7); message_delta's usage.InputTokens
	// is 0 so it must NOT clobber the earlier real value (relay-claude.go's
	// "only take the latest if > 0" rule) -- getting this wrong either double
	// counts or zeroes out the prompt-token bill.
	if usage.PromptTokens != 7 {
		t.Errorf("PromptTokens = %d, want 7 (from message_start, not overwritten by message_delta's 0)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 3 {
		t.Errorf("CompletionTokens = %d, want 3 (final value from message_delta)", usage.CompletionTokens)
	}
	if usage.TotalTokens != 10 {
		t.Errorf("TotalTokens = %d, want 10", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), "hi") {
		t.Errorf("client-facing stream body = %q, want to contain the streamed text delta", w.Body.String())
	}
}

func TestAwsStreamHandler_UnknownEventType_ReturnsError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(200)
		buf := &bytes.Buffer{}
		msg := eventstream.Message{
			Headers: eventstream.Headers{
				{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue("event")},
				{Name: eventstreamapi.EventTypeHeader, Value: eventstream.StringValue("totally-unrecognized-event")},
			},
			Payload: []byte(`{}`),
		}
		_ = eventstream.NewEncoder().Encode(buf, msg)
		_, _ = w.Write(buf.Bytes())
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		AwsClient: prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq:    &bedrockruntime.InvokeModelWithResponseStreamInput{ModelId: aws.String("m"), Body: []byte(`{}`)},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayFormat: types.RelayFormatOpenAI, IsStream: true}
	apiErr, usage := awsStreamHandler(c, info, a)
	if apiErr == nil {
		t.Fatal("expected error for an unrecognized Bedrock event-stream union member")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil when the stream produced an unrecognized event", usage)
	}
}

func TestHandleNovaRequest_ExtractsUsageAndBuildsOpenAIResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"output":{"message":{"content":[{"text":"nova says hi"}]}},"usage":{"inputTokens":6,"outputTokens":2,"totalTokens":8}}`))
	}))
	defer upstream.Close()

	c, w := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		AwsClient: prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq:    &bedrockruntime.InvokeModelInput{ModelId: aws.String("amazon.nova-lite-v1:0"), Body: []byte(`{}`)},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "nova-lite-v1:0"}}
	apiErr, usage := handleNovaRequest(c, info, a)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 6 || usage.CompletionTokens != 2 || usage.TotalTokens != 8 {
		t.Errorf("usage = %+v, want {6 2 8} mapped from Nova's inputTokens/outputTokens/totalTokens", usage)
	}
	var out dto.OpenAITextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body not valid JSON: %v (%s)", err, w.Body.String())
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "nova says hi" {
		t.Errorf("Choices = %+v, want single choice with Nova's answer text translated to OpenAI shape", out.Choices)
	}
}

func TestHandleNovaRequest_EmptyContentArrayDoesNotPanic(t *testing.T) {
	// FINDING: guarded (comment in relay-aws.go explicitly documents this was
	// fixed to avoid an index-out-of-range panic on safety refusals / truncated
	// generations that return an empty content array). This test locks that
	// guard in as a regression test: a real production input (empty content)
	// must degrade to a clean error, not crash the request goroutine.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"output":{"message":{"content":[]}},"usage":{"inputTokens":1,"outputTokens":0,"totalTokens":1}}`))
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		AwsClient: prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq:    &bedrockruntime.InvokeModelInput{ModelId: aws.String("amazon.nova-lite-v1:0"), Body: []byte(`{}`)},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "nova-lite-v1:0"}}
	apiErr, usage := handleNovaRequest(c, info, a)
	if apiErr == nil {
		t.Fatal("expected a clean error for an empty Nova content array, not a panic")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil when there is no content to bill for", usage)
	}
}

func TestHandleNovaRequest_MalformedJSONErrors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		AwsClient: prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq:    &bedrockruntime.InvokeModelInput{ModelId: aws.String("amazon.nova-lite-v1:0"), Body: []byte(`{}`)},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "nova-lite-v1:0"}}
	apiErr, usage := handleNovaRequest(c, info, a)
	if apiErr == nil {
		t.Fatal("expected error for malformed Nova response JSON")
	}
	if usage != nil {
		t.Error("usage should be nil on malformed response")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse dispatch: proves each ClientMode/IsNova/IsStream
// combination routes to the correct handler (a wrong branch here silently
// mis-bills a whole traffic class).
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_ApiKeyMode_DelegatesToClaudeAdaptor(t *testing.T) {
	c, _ := prov_aws_coze_dify_newTestContext()
	body := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":2,"output_tokens":1}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: httpNopCloserForTest(strings.NewReader(body))}
	a := &Adaptor{ClientMode: ClientModeApiKey}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"}, RelayFormat: types.RelayFormatClaude}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok || u.PromptTokens != 2 || u.CompletionTokens != 1 {
		t.Errorf("usage = %+v (%T), want *dto.Usage{PromptTokens:2, CompletionTokens:1} via claude.Adaptor delegation", usage, usage)
	}
}

func TestAdaptor_DoResponse_AKSKMode_NovaDispatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"output":{"message":{"content":[{"text":"ok"}]}},"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`))
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		ClientMode: ClientModeAKSK,
		IsNova:     true,
		AwsClient:  prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq:     &bedrockruntime.InvokeModelInput{ModelId: aws.String("amazon.nova-lite-v1:0"), Body: []byte(`{}`)},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "nova-lite-v1:0"}}
	usage, apiErr := a.DoResponse(c, nil, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u := usage.(*dto.Usage)
	if u.TotalTokens != 2 {
		t.Errorf("TotalTokens = %d, want 2 (proves IsNova=true routed to handleNovaRequest, not the Claude handlers)", u.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelList_ContainsKnownModelsNoDuplicates(t *testing.T) {
	a := &Adaptor{}
	list := a.GetModelList()
	if len(list) == 0 {
		t.Fatal("model list must not be empty")
	}
	seen := map[string]bool{}
	for _, m := range list {
		if seen[m] {
			t.Errorf("duplicate model in list: %q", m)
		}
		seen[m] = true
	}
	if !seen["claude-3-opus-20240229"] {
		t.Error("model list should contain claude-3-opus-20240229")
	}
	if !seen["nova-lite-v1:0"] {
		t.Error("model list should contain nova-lite-v1:0")
	}
}

func TestAdaptor_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "aws" {
		t.Errorf("GetChannelName() = %q, want %q", got, "aws")
	}
}

// ---------------------------------------------------------------------------
// Not-implemented stub methods -- verify they fail loudly rather than
// silently returning zero values that would look like a successful no-op.
// ---------------------------------------------------------------------------

func TestAdaptor_UnimplementedMethodsReturnErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	if _, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{}); err == nil {
		t.Error("ConvertGeminiRequest: expected not-implemented error")
	}
	if _, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{}); err == nil {
		t.Error("ConvertAudioRequest: expected not-implemented error")
	}
	if _, err := a.ConvertImageRequest(c, info, dto.ImageRequest{}); err == nil {
		t.Error("ConvertImageRequest: expected not-implemented error")
	}
	if _, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{}); err == nil {
		t.Error("ConvertEmbeddingRequest: expected not-implemented error")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest: expected not-implemented error")
	}
	if result, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{}); err != nil || result != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil): AWS has no native rerank support", result, err)
	}
}

func TestAdaptor_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}) // documented no-op; must not panic
}

// ---------------------------------------------------------------------------
// Additional edge coverage: newAwsClient auth-mode branches, proxy wiring,
// ConvertClaudeRequest non-url media, DoRequest/DoResponse ApiKey-mode over a
// real (fake) HTTP upstream, and error branches in buildAwsRequestBody /
// awsHandler.
// ---------------------------------------------------------------------------

func TestAdaptor_DoRequest_AKSKMode_BearerTwoPartSecret(t *testing.T) {
	// AK/SK mode's newAwsClient treats a 2-segment secret ("token|region") as
	// bearer auth (distinct from the top-level ClientModeApiKey bearer path,
	// which uses a completely different URL/header construction). Proves the
	// bearer-credential branch of newAwsClient builds a usable client rather
	// than erroring.
	origMax := common_MaxRequestBodyMBForTest()
	defer restore_MaxRequestBodyMBForTest(origMax)

	a := &Adaptor{ClientMode: ClientModeAKSK}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "bearer-token|us-east-1", UpstreamModelName: "claude-3-opus-20240229"},
	}
	_, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error building request with a 2-part (bearer) AK/SK secret: %v", err)
	}
	if a.AwsClient == nil {
		t.Fatal("AwsClient should be constructed for a valid 2-part bearer secret")
	}
}

func TestAdaptor_DoRequest_AKSKMode_ProxyConfigured(t *testing.T) {
	origMax := common_MaxRequestBodyMBForTest()
	defer restore_MaxRequestBodyMBForTest(origMax)

	a := &Adaptor{ClientMode: ClientModeAKSK}
	c, _ := prov_aws_coze_dify_newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "AKID|SECRET|us-east-1",
			UpstreamModelName: "claude-3-opus-20240229",
			ChannelSetting:    dto.ChannelSettings{Proxy: "http://127.0.0.1:1"},
		},
	}
	_, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error building request through a configured proxy client: %v", err)
	}
	if a.AwsClient == nil {
		t.Fatal("AwsClient should still be constructed when a channel proxy is configured")
	}
}

func TestAdaptor_ConvertClaudeRequest_AlreadyBase64MediaLeftUnchanged(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	msg := dto.ClaudeMessage{Role: "user"}
	msg.SetContent([]dto.ClaudeMediaMessage{
		{Type: "image", Source: &dto.ClaudeMessageSource{Type: "base64", MediaType: "image/png", Data: "already-encoded"}},
	})
	req := &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{msg}}
	got, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Since Source.Type != "url", the rewrite branch never runs, so the
	// returned request must be the identical pointer with content untouched.
	if got.(*dto.ClaudeRequest) != req {
		t.Error("expected the same request pointer for a source that is already base64-encoded")
	}
}

func TestAdaptor_ConvertClaudeRequest_RemoteImageURLRewrittenToBase64(t *testing.T) {
	if app.GetHttpClient() == nil {
		app.InitHttpClient()
	}
	fs := system_setting.GetFetchSetting()
	prevSSRF, prevAllow := fs.EnableSSRFProtection, fs.AllowPrivateIp
	fs.EnableSSRFProtection = false
	fs.AllowPrivateIp = true
	defer func() {
		fs := system_setting.GetFetchSetting()
		fs.EnableSSRFProtection, fs.AllowPrivateIp = prevSSRF, prevAllow
	}()
	prevMaxDL := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 10
	defer func() { constant.MaxFileDownloadMB = prevMaxDL }()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("fake-png-bytes"))
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_newTestContext()
	msg := dto.ClaudeMessage{Role: "user"}
	msg.SetContent([]dto.ClaudeMediaMessage{
		{Type: "image", Source: &dto.ClaudeMessageSource{Type: "url", Url: upstream.URL + "/x.png"}},
	})
	req := &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{msg}}
	got, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outReq := got.(*dto.ClaudeRequest)
	content, perr := outReq.Messages[0].ParseContent()
	if perr != nil {
		t.Fatalf("unexpected parse error on rewritten content: %v", perr)
	}
	if len(content) != 1 || content[0].Source == nil {
		t.Fatalf("content = %+v, want one media block with a Source", content)
	}
	if content[0].Source.Type != "base64" {
		t.Errorf("Source.Type = %q, want rewritten to base64", content[0].Source.Type)
	}
	if content[0].Source.Url != "" {
		t.Errorf("Source.Url = %q, want cleared after inlining as base64", content[0].Source.Url)
	}
	if content[0].Source.Data == "" {
		t.Error("Source.Data should contain the base64-encoded fetched bytes")
	}
}

func TestBuildAwsRequestBody_PassThroughMalformedBodyErrors(t *testing.T) {
	origMax := common_MaxRequestBodyMBForTest()
	defer restore_MaxRequestBodyMBForTest(origMax)

	c, _ := prov_aws_coze_dify_newTestContext()
	c.Request.Body = httpNopCloserForTest(strings.NewReader(`{not-json`))
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: true}}}
	_, err := buildAwsRequestBody(c, info, &AwsClaudeRequest{})
	if err == nil {
		t.Fatal("expected error for malformed pass-through request body")
	}
}

func TestAwsHandler_MalformedUpstreamBodyErrors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`not-json-at-all`))
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_newTestContext()
	a := &Adaptor{
		AwsClient: prov_aws_coze_dify_newBedrockClient(upstream.URL),
		AwsReq:    &bedrockruntime.InvokeModelInput{ModelId: aws.String("anthropic.claude-3-opus-20240229-v1:0"), Body: []byte(`{}`)},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"}}
	apiErr, usage := awsHandler(c, info, a)
	if apiErr == nil {
		t.Fatal("expected error for malformed (non-JSON) Bedrock response body")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil on a response parse failure", usage)
	}
}

// NOTE: Adaptor.DoRequest's ClientModeApiKey branch (-> provider.DoApiRequest)
// is intentionally not exercised here. Unlike every other provider's
// ApiKey/base-url flow, this adaptor's GetRequestURL for ClientModeApiKey
// hardcodes the real "bedrock-runtime.<model>.amazonaws.com" host straight
// from info.ApiKey/UpstreamModelName -- it does not consult
// info.ChannelBaseUrl at all, so there is no way to redirect it at a local
// httptest.Server the way the other providers' base-url-driven adaptors are
// tested. Actually invoking it would mean dialing a real external AWS
// endpoint, which this task forbids. The header/URL-construction halves of
// this path (SetupRequestHeader's Bearer auth, GetRequestURL's URL shape) are
// covered directly above without going through the network dispatcher.
