package openai

// Business-acceptance tests for OpenaiHandler / OpenaiHandlerWithUsage: the
// non-streaming response path. These decide the final usage numbers billed
// to the tenant and whether a real vendor error body gets correctly
// classified instead of silently treated as a 200 success.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func fakeHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestOpenaiHandler_HappyPath_UsageAlreadyPresent(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: "openai",
	}
	body := `{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	resp := fakeHTTPResponse(200, body)

	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15 (usage taken directly from upstream body)", usage.TotalTokens)
	}
	if !strings.Contains(w.rec.Body.String(), `"total_tokens":15`) {
		t.Errorf("response body should echo original usage untouched, got %q", w.rec.Body.String())
	}
}

func TestOpenaiHandler_EstimatesUsageWhenMissing(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-3.5-turbo"},
		RelayFormat: "openai",
	}
	info.SetEstimatePromptTokens(20)
	// no usage field at all -> handler must estimate completion tokens from
	// message content via CountTextToken and synthesize the usage block.
	body := `{"id":"chatcmpl-2","model":"gpt-3.5-turbo","choices":[{"index":0,"message":{"role":"assistant","content":"hello world"},"finish_reason":"stop"}]}`
	resp := fakeHTTPResponse(200, body)

	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 20 {
		t.Errorf("PromptTokens = %d, want 20 (from GetEstimatePromptTokens)", usage.PromptTokens)
	}
	if usage.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (estimated from message content)", usage.CompletionTokens)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Errorf("TotalTokens = %d, want sum %d", usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
	}
	// the injected usage must also have been written back into the response body
	if !strings.Contains(w.rec.Body.String(), `"prompt_tokens":20`) {
		t.Errorf("response body should carry the synthesized usage, got %q", w.rec.Body.String())
	}
}

func TestOpenaiHandler_UpstreamErrorBody_Classified(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: "openai",
	}
	body := `{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`
	resp := fakeHTTPResponse(401, body)

	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr == nil {
		t.Fatal("expected classified error for upstream error body")
	}
	if usage != nil {
		t.Errorf("usage should be nil on error path, got %+v", usage)
	}
	if !strings.Contains(apiErr.Error(), "invalid api key") {
		t.Errorf("error = %q, want to surface upstream message", apiErr.Error())
	}
}

func TestOpenaiHandler_MalformedJSON_Errors(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: "openai",
	}
	resp := fakeHTTPResponse(200, `{not-json`)
	_, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body")
	}
}

func TestOpenaiHandler_ForceFormat_ReMarshalsStruct(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelSetting: dto.ChannelSettings{ForceFormat: true},
		},
		RelayFormat: "openai",
	}
	body := `{"id":"chatcmpl-3","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	resp := fakeHTTPResponse(200, body)

	_, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if !strings.Contains(w.rec.Body.String(), `"id":"chatcmpl-3"`) {
		t.Errorf("force-format re-marshaled body should still contain the response id, got %q", w.rec.Body.String())
	}
}

func TestOpenaiHandler_OpenRouterEnterprise_UnwrapsSuccessData(t *testing.T) {
	w := newRecorderCtx(t)
	enterpriseEnabled := true
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeOpenRouter,
			ChannelOtherSettings: dto.ChannelOtherSettings{OpenRouterEnterprise: &enterpriseEnabled},
		},
		RelayFormat: "openai",
	}
	inner := `{"id":"chatcmpl-ent","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`
	// OpenRouterEnterpriseResponse.Data is json.RawMessage: it must be
	// embedded as-is (raw JSON), not as an escaped/quoted string.
	wrapped := `{"success":true,"data":` + inner + `}`
	var sanity map[string]json.RawMessage
	if err := json.Unmarshal([]byte(wrapped), &sanity); err != nil {
		t.Fatalf("test fixture itself is invalid JSON: %v", err)
	}
	resp := fakeHTTPResponse(200, wrapped)

	usage, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 4 {
		t.Errorf("TotalTokens = %d, want 4 (unwrapped from enterprise envelope)", usage.TotalTokens)
	}
}

func TestOpenaiHandler_OpenRouterEnterprise_SuccessFalse_Errors(t *testing.T) {
	w := newRecorderCtx(t)
	enterpriseEnabled := true
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeOpenRouter,
			ChannelOtherSettings: dto.ChannelOtherSettings{OpenRouterEnterprise: &enterpriseEnabled},
		},
		RelayFormat: "openai",
	}
	resp := fakeHTTPResponse(200, `{"success":false,"data":"eyJlcnJvciI6InJlZGFjdGVkIn0="}`)
	_, apiErr := OpenaiHandler(w.ctx, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when openrouter enterprise envelope reports success=false")
	}
}

// ---------------------------------------------------------------------------
// OpenaiHandlerWithUsage (used for image generation responses)
// ---------------------------------------------------------------------------

func TestOpenaiHandlerWithUsage_SumsInputOutputIntoPromptCompletion(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	body := `{"usage":{"input_tokens":10,"output_tokens":20,"input_tokens_details":{"image_tokens":3,"text_tokens":7}}}`
	resp := fakeHTTPResponse(200, body)

	usage, apiErr := OpenaiHandlerWithUsage(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10 (from input_tokens)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want 20 (from output_tokens)", usage.CompletionTokens)
	}
	if usage.PromptTokensDetails.ImageTokens != 3 || usage.PromptTokensDetails.TextTokens != 7 {
		t.Errorf("PromptTokensDetails = %+v, want image=3 text=7", usage.PromptTokensDetails)
	}
}

func TestOpenaiHandlerWithUsage_MalformedJSON_Errors(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	resp := fakeHTTPResponse(200, `{not-json`)
	_, apiErr := OpenaiHandlerWithUsage(w.ctx, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body")
	}
}

func TestOpenaiHandlerWithUsage_WritesBodyThroughRegardlessOfParsing(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	body := `{"usage":{"input_tokens":1,"output_tokens":1}}`
	resp := fakeHTTPResponse(200, body)
	_, apiErr := OpenaiHandlerWithUsage(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if w.rec.Body.String() != body {
		t.Errorf("body written to client = %q, want exact passthrough %q", w.rec.Body.String(), body)
	}
}
