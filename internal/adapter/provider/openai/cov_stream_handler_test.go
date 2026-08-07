package openai

// Business-acceptance tests for OaiStreamHandler: the streaming (SSE)
// response path. This decides what gets billed for streamed completions and
// enforces the "don't bill on truncated/abnormal streams" safety net, so
// getting sawDone/usage wrong here is a direct billing correctness bug.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func init() {
	// OaiStreamHandler delegates to helper.StreamScannerHandler, which builds
	// a time.NewTicker(StreamingTimeout*time.Second); a zero/unset value
	// panics ("non-positive interval"), so ensure a safe default here.
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
}

func sseResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestOaiStreamHandler_NilResponseGuard(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := OaiStreamHandler(w.ctx, info, nil)
	if apiErr == nil {
		t.Fatal("expected error for nil response")
	}
	if usage != nil {
		t.Errorf("usage should be nil, got %+v", usage)
	}
}

func TestOaiStreamHandler_NormalCompletion_AccumulatesAndSignalsDone(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-3.5-turbo"},
		RelayFormat: "openai",
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	info.SetEstimatePromptTokens(5)
	body := `data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"Hello "}}]}` + "\n\n" +
		`data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"world"},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	usage, apiErr := OaiStreamHandler(w.ctx, info, sseResponse(body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 5 {
		t.Errorf("PromptTokens = %d, want 5 (estimate baseline, no upstream usage present)", usage.PromptTokens)
	}
	if usage.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (estimated from accumulated content)", usage.CompletionTokens)
	}
	if usage.TotalTokens == 0 {
		t.Error("TotalTokens should be non-zero: sawDone=true, a completed stream must be billable")
	}
	if !strings.Contains(w.rec.Body.String(), "[DONE]") {
		t.Errorf("client-facing SSE stream should end with [DONE], got %q", w.rec.Body.String())
	}
}

func TestOaiStreamHandler_UpstreamUsagePresent_TakesPrecedenceOverEstimate(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-3.5-turbo"},
		RelayFormat:        "openai",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		ShouldIncludeUsage: true,
	}
	info.SetEstimatePromptTokens(999) // deliberately wrong baseline to prove it's NOT used
	body := `data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"gpt-3.5-turbo","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n" +
		"data: [DONE]\n\n"

	usage, apiErr := OaiStreamHandler(w.ctx, info, sseResponse(body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 10 {
		t.Errorf("TotalTokens = %d, want 10 (real upstream usage, not the 999 estimate baseline)", usage.TotalTokens)
	}
}

func TestOaiStreamHandler_AbnormalTermination_NoBilling(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-3.5-turbo"},
		RelayFormat: "openai",
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	info.SetEstimatePromptTokens(5)
	// Stream drops mid-way: no [DONE] terminator ever arrives (simulates
	// client cancel / upstream connection reset).
	body := `data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"partial content that would otherwise bill"}}]}` + "\n\n"

	usage, apiErr := OaiStreamHandler(w.ctx, info, sseResponse(body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 0 || usage.PromptTokens != 0 || usage.CompletionTokens != 0 {
		t.Errorf("usage = %+v, want all-zero: abnormal stream end (no [DONE]) must suppress billing", usage)
	}
}

func TestOaiStreamHandler_AudioModel_ExtractsUsageFromSecondLastChunk(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o-audio-preview"},
		RelayFormat: "openai",
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	info.SetEstimatePromptTokens(5)
	// audio models carry real usage on the second-to-last SSE frame, with the
	// truly-last frame being a trailer without content.
	body := `data: {"id":"c1","model":"gpt-4o-audio-preview","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"gpt-4o-audio-preview","choices":[{"delta":{}}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}` + "\n\n" +
		`data: {"id":"c1","model":"gpt-4o-audio-preview","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	usage, apiErr := OaiStreamHandler(w.ctx, info, sseResponse(body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 10 {
		t.Errorf("TotalTokens = %d, want 10 (extracted from second-to-last SSE frame for audio models)", usage.TotalTokens)
	}
}

func TestOaiStreamHandler_MalformedMidStreamChunk_DoesNotPanicAndStillCompletes(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-3.5-turbo"},
		RelayFormat: "openai",
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	info.SetEstimatePromptTokens(5)
	// a genuinely non-JSON line embedded mid-stream (e.g. a vendor comment or
	// keep-alive artifact) must not crash the handler; the well-formed
	// surrounding chunks should still be processed.
	body := `data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"ok"}}]}` + "\n\n" +
		"data: not even json\n\n" +
		`data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	usage, apiErr := OaiStreamHandler(w.ctx, info, sseResponse(body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage == nil {
		t.Fatal("usage should not be nil even when a mid-stream chunk fails to parse")
	}
}
