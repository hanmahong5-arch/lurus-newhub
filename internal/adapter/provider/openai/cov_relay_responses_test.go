package openai

// Business-acceptance tests for the /v1/responses (Responses API) handlers:
// non-stream OaiResponsesHandler and streaming OaiResponsesStreamHandler.
// These extract usage/cached-token accounting and built-in tool call counts
// (web_search, image_generation) that feed downstream billing and quota
// dashboards, so mis-extraction directly under- or over-bills tenants.

import (
	"context"
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
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "model-a"}}
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

// A COMPLETED stream (response.completed arrived) whose terminal event carries
// no usage block still estimates output tokens from the streamed text: the
// upstream finished, the caller has the whole answer, and billing it is right.
func TestOaiResponsesStreamHandler_CompletedWithoutUsage_EstimatesFromDeltaText(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "model-a"}}
	info.SetEstimatePromptTokens(9)
	body := `data: {"type":"response.output_text.delta","delta":"some streamed text output"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"r1","status":"completed","output":[]}}` + "\n\n"
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
	if strings.Contains(w.rec.Body.String(), "upstream_stream_incomplete") {
		t.Errorf("a completed stream gets no error frame:\n%s", w.rec.Body.String())
	}
}

// The stream ended with deltas but no terminal event: the upstream stopped
// mid-answer. Frames already reached the caller, so the in-band error frame
// goes out, nothing is billed (it used to bill an estimate of the partial
// text and end silently, like a normal completion) and the same failure is
// returned so the relay loop records a breaker failure instead of a success.
func TestOaiResponsesStreamHandler_NoTerminalEvent_InBandErrorAndNoBilling(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "model-a"}}
	info.SetEstimatePromptTokens(9)
	body := `data: {"type":"response.output_text.delta","delta":"some streamed text output"}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
	assertSurfacedIncomplete(t, apiErr)
	if usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.TotalTokens != 0 {
		t.Errorf("incomplete stream must not be billed, usage = %+v", usage)
	}
	out := w.rec.Body.String()
	if !strings.Contains(out, `"code":"upstream_stream_incomplete"`) || !strings.Contains(out, `data: {"error":{`) {
		t.Errorf("caller must get the in-band error frame:\n%s", out)
	}
	if !strings.Contains(out, "some streamed text output") {
		t.Errorf("delivered delta must still be in the body:\n%s", out)
	}
}

// Upstream-declared terminal events are complete as far as the wire goes: the
// caller already holds the event, so no second (invented) error frame.
func TestOaiResponsesStreamHandler_TerminalEvents_NoInBandError(t *testing.T) {
	for _, typ := range []string{"response.completed", "response.incomplete", "response.failed", "error"} {
		t.Run(typ, func(t *testing.T) {
			w := newRecorderCtx(t)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "model-a"}}
			body := `data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n" +
				`data: {"type":"` + typ + `","response":{"id":"r1","output":[],"usage":{"input_tokens":6,"output_tokens":4,"total_tokens":10}}}` + "\n\n"
			resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

			usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
			if apiErr != nil {
				t.Fatalf("unexpected error: %v", apiErr.Error())
			}
			if strings.Contains(w.rec.Body.String(), "upstream_stream_incomplete") {
				t.Errorf("%s is a terminal event; no error frame appended:\n%s", typ, w.rec.Body.String())
			}
			if usage.TotalTokens == 0 {
				t.Errorf("%s: a stream that terminated keeps its usage/estimate, got %+v", typ, usage)
			}
		})
	}
}

// Nothing reached the caller (empty body, or only SSE comments): fail over with
// a retryable error, write nothing, bill nothing.
func TestOaiResponsesStreamHandler_NoTerminalBeforeFirstByte_FailsOver(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "keepalive comment": ": keepalive\n\n"} {
		t.Run(name, func(t *testing.T) {
			w := newRecorderCtx(t)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "model-a"}}
			resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

			usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
			if apiErr == nil {
				t.Fatalf("empty incomplete stream must return an error; body=%q", w.rec.Body.String())
			}
			assertFailoverError(t, apiErr)
			if w.ctx.Writer.Written() || w.rec.Body.Len() != 0 {
				t.Errorf("nothing may be written when failing over: %q", w.rec.Body.String())
			}
			if usage != nil && (usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.TotalTokens != 0) {
				t.Errorf("usage = %+v, want zero", usage)
			}
		})
	}
}

// A caller that hung up gets nothing written (nobody to tell) and no bill.
func TestOaiResponsesStreamHandler_NoTerminal_ClientGone_WritesNoErrorFrame(t *testing.T) {
	w := newRecorderCtx(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.ctx.Request = w.ctx.Request.WithContext(ctx)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "model-a"}}
	body := `data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if strings.Contains(w.rec.Body.String(), "upstream_stream_incomplete") {
		t.Errorf("caller is gone, no error frame:\n%s", w.rec.Body.String())
	}
	if usage.TotalTokens != 0 {
		t.Errorf("usage = %+v, want zero", usage)
	}
}

func TestOaiResponsesStreamHandler_WebSearchToolCallCounted(t *testing.T) {
	w := newRecorderCtx(t)
	webSearchTool := &relaycommon.BuildInToolInfo{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "model-a"},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{"web_search_preview": webSearchTool},
		},
	}
	body := `data: {"type":"response.output_item.done","item":{"type":"web_search_call"}}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	// The stream ends without a terminal event, so the handler also reports the
	// truncation; the tool call seen before the cut is still counted.
	_, apiErr := OaiResponsesStreamHandler(w.ctx, info, resp)
	assertSurfacedIncomplete(t, apiErr)
	if webSearchTool.CallCount != 1 {
		t.Errorf("CallCount = %d, want 1", webSearchTool.CallCount)
	}
}
