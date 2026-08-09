package openai

// Business-acceptance tests for the /v1/responses (Responses API) handlers:
// non-stream OaiResponsesHandler and streaming OaiResponsesStreamHandler.
// These extract usage/cached-token accounting and built-in tool call counts
// (web_search, image_generation) that feed downstream billing and quota
// dashboards, so mis-extraction directly under- or over-bills tenants.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestOaiResponsesHandler_UsageAndCachedTokens(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	body := `{"id":"resp1","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":2}}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 5 || usage.TotalTokens != 15 {
		t.Errorf("usage = %+v, want prompt=10 completion=5 total=15", usage)
	}
	if usage.PromptTokensDetails.CachedTokens != 2 {
		t.Errorf("CachedTokens = %d, want 2", usage.PromptTokensDetails.CachedTokens)
	}
	if w.rec.Body.String() != body {
		t.Errorf("response body should be forwarded verbatim, got %q", w.rec.Body.String())
	}
}

func TestOaiResponsesHandler_ErrorBodyClassified(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	body := `{"error":{"message":"context length exceeded","type":"invalid_request_error"}}`
	resp := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesHandler(w.ctx, info, resp)
	if apiErr == nil {
		t.Fatal("expected classified error")
	}
	if usage != nil {
		t.Errorf("usage should be nil on error path, got %+v", usage)
	}
}

func TestOaiResponsesHandler_MalformedJSON_Errors(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{not-json`)), Header: make(http.Header)}
	_, apiErr := OaiResponsesHandler(w.ctx, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestOaiResponsesHandler_ImageGenerationCall_SetsGinKeys(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	body := `{"id":"resp2","status":"completed","output":[{"type":"image_generation_call","quality":"high","size":"1024x1024"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	if _, apiErr := OaiResponsesHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if v, ok := w.ctx.Get("image_generation_call"); !ok || v != true {
		t.Errorf("image_generation_call = %v (ok=%v), want true", v, ok)
	}
	if v, _ := w.ctx.Get("image_generation_call_quality"); v != "high" {
		t.Errorf("quality = %v, want high", v)
	}
	if v, _ := w.ctx.Get("image_generation_call_size"); v != "1024x1024" {
		t.Errorf("size = %v, want 1024x1024", v)
	}
}

func TestOaiResponsesHandler_BuiltInToolCallCountIncremented(t *testing.T) {
	w := newRecorderCtx(t)
	webSearchTool := &relaycommon.BuildInToolInfo{ToolName: "web_search_preview"}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{"web_search_preview": webSearchTool},
		},
	}
	body := `{"id":"resp3","status":"completed","output":[],"tools":[{"type":"web_search_preview"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	if _, apiErr := OaiResponsesHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if webSearchTool.CallCount != 1 {
		t.Errorf("CallCount = %d, want 1", webSearchTool.CallCount)
	}
}

func TestOaiResponsesHandler_NoUsageInfo_ReturnsZeroUsageWithoutPanic(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	body := `{"id":"resp4","status":"completed","output":[]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := OaiResponsesHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 when upstream omits usage", usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// OaiResponsesStreamHandler
// ---------------------------------------------------------------------------

func TestOaiResponsesStreamHandler_NilResponseGuard(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, nil)
	if apiErr == nil {
		t.Fatal("expected error for nil response")
	}
	if usage != nil {
		t.Errorf("usage should be nil, got %+v", usage)
	}
}

func TestOaiResponsesStreamHandler_CompletedEventCarriesUsage(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"}}
	body := `data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"r1","status":"completed","output":[],"usage":{"input_tokens":6,"output_tokens":4,"total_tokens":10}}}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 10 || usage.PromptTokens != 6 || usage.CompletionTokens != 4 {
		t.Errorf("usage = %+v, want prompt=6 completion=4 total=10", usage)
	}
}

func TestOaiResponsesStreamHandler_NoUsage_EstimatesFromDeltaText(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"}}
	info.SetEstimatePromptTokens(9)
	body := `data: {"type":"response.output_text.delta","delta":"some streamed text output"}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (estimated from delta text)", usage.CompletionTokens)
	}
	if usage.PromptTokens != 9 {
		t.Errorf("PromptTokens = %d, want 9 (estimate baseline since completion>0 but prompt missing)", usage.PromptTokens)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Errorf("TotalTokens = %d, want sum", usage.TotalTokens)
	}
}

func TestOaiResponsesStreamHandler_WebSearchToolCallCounted(t *testing.T) {
	w := newRecorderCtx(t)
	webSearchTool := &relaycommon.BuildInToolInfo{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{"web_search_preview": webSearchTool},
		},
	}
	body := `data: {"type":"response.output_item.done","item":{"type":"web_search_call"}}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	if _, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if webSearchTool.CallCount != 1 {
		t.Errorf("CallCount = %d, want 1", webSearchTool.CallCount)
	}
}

func TestOaiResponsesStreamHandler_EmptyStream_ZeroUsageNoPanic(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}
	usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 for a completely empty stream", usage.TotalTokens)
	}
}
