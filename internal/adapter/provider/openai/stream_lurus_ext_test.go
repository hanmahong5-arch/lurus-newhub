package openai

// Business-acceptance tests for the streaming x_lurus perception extension.
// Defect: when an upstream (e.g. deepseek-chat) inlines usage in the final
// SSE chunk, containStreamUsage becomes true and HandleFinalResponse's
// synthetic-usage-frame branch (info.ShouldIncludeUsage && !containStreamUsage)
// is skipped, so the computed cost/quota extension was silently discarded and
// the client never saw usage.x_lurus on any frame. sendFinalStreamData fixes
// this by attaching x_lurus onto that same inline-usage chunk instead of
// inventing a new one.

import (
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// perceptiveRelayInfo returns a RelayInfo with non-zero pricing ratios so
// ComputeLurusExtension produces a distinguishable, non-trivial cost value
// rather than the zero-ratio default.
func perceptiveRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-3.5-turbo"},
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
		UserQuota: 1000000,
	}
}

func TestOaiStreamHandler_InlineUsage_AttachesXLurusExactlyOnce(t *testing.T) {
	w := newRecorderCtx(t)
	info := perceptiveRelayInfo()
	info.ShouldIncludeUsage = true

	body := `data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"gpt-3.5-turbo","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n" +
		"data: [DONE]\n\n"
	resp := sseResponse(body)
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := OaiStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 10 {
		t.Fatalf("TotalTokens = %d, want 10 (real inline upstream usage)", usage.TotalTokens)
	}

	respBody := w.rec.Body.String()
	if got := strings.Count(respBody, "x_lurus"); got != 1 {
		t.Fatalf("x_lurus should appear exactly once in the SSE stream, got %d; body=%q", got, respBody)
	}

	// x_lurus must live inside the same SSE line that carries the real usage
	// numbers, not a separately invented frame.
	var usageLine string
	for _, line := range strings.Split(respBody, "\n") {
		if strings.Contains(line, `"total_tokens":10`) {
			usageLine = line
			break
		}
	}
	if usageLine == "" {
		t.Fatalf("expected a line containing the real usage numbers, body=%q", respBody)
	}
	if !strings.Contains(usageLine, "x_lurus") {
		t.Errorf("x_lurus should be attached to the usage-carrying frame, not a separate one; usage line=%q", usageLine)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Errorf("stream should still end with [DONE], got %q", respBody)
	}
}

// An abnormally-ended stream is not billed (the sawDone==false path zeroes
// usage so postConsumeQuota's totalTokens==0 safety net suppresses the
// charge). The perception extension must agree with that: quoting a cost on
// the frame the client receives would advertise a charge that never happens.
func TestOaiStreamHandler_InlineUsage_AbnormalEnd_QuotesNoCost(t *testing.T) {
	w := newRecorderCtx(t)
	info := perceptiveRelayInfo()
	info.ShouldIncludeUsage = true

	// Upstream emitted its inline-usage chunk, then the connection ended
	// without "[DONE]".
	body := `data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"gpt-3.5-turbo","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n"
	resp := sseResponse(body)
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := OaiStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 0 {
		t.Fatalf("TotalTokens = %d, want 0 — an abnormally-ended stream must not be billed", usage.TotalTokens)
	}

	respBody := w.rec.Body.String()
	if strings.Contains(respBody, "x_lurus") {
		t.Errorf("no cost should be quoted to the client for an unbilled stream, body=%q", respBody)
	}
	// The upstream's own token counts are still forwarded untouched — this
	// path suppresses our cost claim, not the upstream's data.
	if !strings.Contains(respBody, `"total_tokens":10`) {
		t.Errorf("the upstream usage chunk should still be relayed verbatim, body=%q", respBody)
	}
}

func TestOaiStreamHandler_InlineUsage_ShouldIncludeUsageFalse_NoContent_NoFrameEmitted(t *testing.T) {
	w := newRecorderCtx(t)
	info := perceptiveRelayInfo()
	info.ShouldIncludeUsage = false

	body := `data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"gpt-3.5-turbo","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n" +
		"data: [DONE]\n\n"
	resp := sseResponse(body)
	defer func() { _ = resp.Body.Close() }()

	_, apiErr := OaiStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}

	respBody := w.rec.Body.String()
	// The client never asked for usage (stream_options.include_usage=false)
	// and the trailing usage-only chunk (empty choices) carries no visible
	// content, so it must not be forwarded at all -- inventing a frame here
	// would violate strict OpenAI SDKs that reject an unrequested usage chunk.
	if strings.Contains(respBody, "x_lurus") {
		t.Errorf("no x_lurus should be emitted when ShouldIncludeUsage=false and the usage chunk has no content, body=%q", respBody)
	}
	if strings.Contains(respBody, `"usage"`) {
		t.Errorf("no usage frame should be invented when ShouldIncludeUsage=false, body=%q", respBody)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Errorf("stream should still end with [DONE], got %q", respBody)
	}
}

func TestOaiStreamHandler_NoUpstreamUsage_SyntheticFrame_StillHasXLurusOnce(t *testing.T) {
	w := newRecorderCtx(t)
	info := perceptiveRelayInfo()
	info.ShouldIncludeUsage = true

	// Upstream never inlines usage anywhere in the stream: this exercises the
	// pre-existing synthetic-usage-frame path in HandleFinalResponse
	// (info.ShouldIncludeUsage && !containStreamUsage), which must keep
	// working unchanged after this fix.
	body := `data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{"content":"hello"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"gpt-3.5-turbo","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	resp := sseResponse(body)
	defer func() { _ = resp.Body.Close() }()

	_, apiErr := OaiStreamHandler(w.ctx, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}

	respBody := w.rec.Body.String()
	if got := strings.Count(respBody, "x_lurus"); got != 1 {
		t.Errorf("x_lurus should appear exactly once via the synthetic usage frame, got %d; body=%q", got, respBody)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Errorf("stream should still end with [DONE], got %q", respBody)
	}
}

func TestSendFinalStreamData_NilExt_DelegatesToRawPassthrough(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	data := `{"id":"c1","choices":[],"usage":{"total_tokens":10}}`
	if err := sendFinalStreamData(w.ctx, info, data, false, false, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.rec.Body.String() != "data: "+data+"\n\n" {
		t.Errorf("nil ext should pass the raw chunk through unchanged, got %q", w.rec.Body.String())
	}
}

func TestSendFinalStreamData_MalformedJSON_FallsBackRaw(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	ext := &types.LurusUsageExtension{CostLB: 1}
	if err := sendFinalStreamData(w.ctx, info, "{not-json", false, false, ext); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.rec.Body.String(), "{not-json") {
		t.Errorf("malformed JSON should fall back to raw passthrough, got %q", w.rec.Body.String())
	}
	if strings.Contains(w.rec.Body.String(), "x_lurus") {
		t.Errorf("malformed JSON must not attempt to inject x_lurus, got %q", w.rec.Body.String())
	}
}

func TestSendFinalStreamData_UsageNotObject_FallsBackRaw(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	ext := &types.LurusUsageExtension{CostLB: 1}

	cases := []string{
		`{"id":"c1","choices":[],"usage":"not-an-object"}`,
		`{"id":"c1","choices":[]}`, // usage key absent entirely
	}
	for _, data := range cases {
		w := newRecorderCtx(t)
		if err := sendFinalStreamData(w.ctx, info, data, false, false, ext); err != nil {
			t.Fatalf("unexpected error for %q: %v", data, err)
		}
		if !strings.Contains(w.rec.Body.String(), data) {
			t.Errorf("non-object/absent usage should fall back to raw passthrough of %q, got %q", data, w.rec.Body.String())
		}
		if strings.Contains(w.rec.Body.String(), "x_lurus") {
			t.Errorf("must not inject x_lurus when usage is not an object, data=%q got %q", data, w.rec.Body.String())
		}
	}
}

func TestSendFinalStreamData_ForceFormatOrThinkToContent_Unaffected(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	ext := &types.LurusUsageExtension{CostLB: 1}
	data := `{"id":"c1","choices":[{"delta":{"content":"hi"}}],"usage":{"total_tokens":10}}`

	w1 := newRecorderCtx(t)
	if err := sendFinalStreamData(w1.ctx, info, data, true, false, ext); err != nil {
		t.Fatalf("forceFormat: unexpected error: %v", err)
	}
	if strings.Contains(w1.rec.Body.String(), "x_lurus") {
		t.Errorf("forceFormat=true should delegate to sendStreamData unmodified (no x_lurus injection), got %q", w1.rec.Body.String())
	}

	w2 := newRecorderCtx(t)
	if err := sendFinalStreamData(w2.ctx, info, data, false, true, ext); err != nil {
		t.Fatalf("thinkToContent: unexpected error: %v", err)
	}
	if strings.Contains(w2.rec.Body.String(), "x_lurus") {
		t.Errorf("thinkToContent=true should delegate to sendStreamData unmodified (no x_lurus injection), got %q", w2.rec.Body.String())
	}
}

func TestSendFinalStreamData_ValidUsage_InjectsAndPreservesTokenCounts(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{}
	ext := &types.LurusUsageExtension{CostLB: 0.5, BillingMode: "pre_auth"}
	data := `{"id":"c1","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`

	if err := sendFinalStreamData(w.ctx, info, data, false, false, ext); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := w.rec.Body.String()
	if !strings.Contains(got, `"x_lurus"`) {
		t.Fatalf("expected x_lurus to be injected, got %q", got)
	}
	// Integer token counts must round-trip through the map-merge without
	// turning into floats (e.g. "total_tokens":10 must not become 10.0).
	if !strings.Contains(got, `"total_tokens":10`) || strings.Contains(got, `"total_tokens":10.0`) ||
		!strings.Contains(got, `"prompt_tokens":7`) || !strings.Contains(got, `"completion_tokens":3`) {
		t.Errorf("token counts should survive the map-merge as integers unchanged, got %q", got)
	}
	if !strings.Contains(got, `"cost_lb":0.5`) || !strings.Contains(got, `"billing_mode":"pre_auth"`) {
		t.Errorf("expected the extension fields to be present in the merged usage object, got %q", got)
	}
}
