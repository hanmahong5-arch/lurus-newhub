package openai

// Business-acceptance tests for the streaming-glue helpers in helper.go:
// batch/per-item SSE chunk parsing (processTokens family), extraction of the
// final chunk's metadata (handleLastResponse), emission of the final SSE
// frame per wire format (HandleFinalResponse), and the per-vendor cached
// token extraction used for accurate billing (applyUsagePostProcessing and
// friends). These directly gate what usage numbers get billed to tenants.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// recorderCtx bundles a gin.Context with its underlying ResponseRecorder so
// tests can both drive a handler and inspect exactly what bytes it wrote.
type recorderCtx struct {
	ctx *gin.Context
	rec *httptest.ResponseRecorder
}

func newRecorderCtx(t *testing.T) recorderCtx {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return recorderCtx{ctx: c, rec: w}
}

// ---------------------------------------------------------------------------
// processTokens / processChatCompletions / processCompletions
// ---------------------------------------------------------------------------

func TestProcessTokens_ChatCompletions_BatchParse(t *testing.T) {
	var b strings.Builder
	toolCount := 0
	items := []string{
		`{"choices":[{"delta":{"content":"hi "}}]}`,
		`{"choices":[{"delta":{"content":"there"}}]}`,
	}
	if err := processTokens(relayconstant.RelayModeChatCompletions, items, &b, &toolCount); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.String() != "hi there" {
		t.Errorf("accumulated = %q, want %q", b.String(), "hi there")
	}
}

func TestProcessTokens_ChatCompletions_FallsBackToPerItemOnBatchFailure(t *testing.T) {
	var b strings.Builder
	toolCount := 0
	// second item is malformed as part of a JSON *array* (trailing garbage),
	// forcing the one-shot array unmarshal to fail and the per-item fallback
	// to run; the well-formed items must still be processed.
	items := []string{
		`{"choices":[{"delta":{"content":"ok"}}]}`,
		`{"choices":[{"delta":{"content":"still-ok"}}], "extra_trailing_garbage"`,
	}
	err := processTokens(relayconstant.RelayModeChatCompletions, items, &b, &toolCount)
	if err == nil {
		t.Fatal("expected error: the malformed item cannot even be parsed standalone")
	}
	// the first (valid) item must have been processed before the fallback hit
	// the malformed second item and returned early.
	if !strings.Contains(b.String(), "ok") {
		t.Errorf("accumulated = %q, want it to contain the first valid item's content", b.String())
	}
}

func TestProcessTokens_ChatCompletions_ToolCallCount(t *testing.T) {
	var b strings.Builder
	toolCount := 0
	items := []string{
		`{"choices":[{"delta":{"tool_calls":[{"function":{"name":"f1","arguments":"{}"}},{"function":{"name":"f2","arguments":"{}"}}]}}]}`,
	}
	if err := processTokens(relayconstant.RelayModeChatCompletions, items, &b, &toolCount); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if toolCount != 2 {
		t.Errorf("toolCount = %d, want 2", toolCount)
	}
}

func TestProcessTokens_Completions_LegacyMode(t *testing.T) {
	var b strings.Builder
	toolCount := 0
	items := []string{
		`{"choices":[{"text":"legacy-a"}]}`,
		`{"choices":[{"text":"legacy-b"}]}`,
	}
	if err := processTokens(relayconstant.RelayModeCompletions, items, &b, &toolCount); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.String() != "legacy-alegacy-b" {
		t.Errorf("accumulated = %q, want %q", b.String(), "legacy-alegacy-b")
	}
}

func TestProcessTokens_Completions_PerItemFallbackSkipsBadItems(t *testing.T) {
	var b strings.Builder
	items := []string{
		`{"choices":[{"text":"good"}]}`,
		`not-json-at-all`,
	}
	// legacy completions per-item fallback silently `continue`s on a bad item
	// rather than erroring -- verify the good item still lands and no error
	// escapes (that's the documented behavior, unlike the chat-completions path).
	err := processTokens(relayconstant.RelayModeCompletions, items, &b, new(int))
	if err != nil {
		t.Fatalf("unexpected error (completions fallback should swallow bad items): %v", err)
	}
	if b.String() != "good" {
		t.Errorf("accumulated = %q, want %q", b.String(), "good")
	}
}

func TestProcessTokens_UnknownRelayMode_NoOp(t *testing.T) {
	var b strings.Builder
	if err := processTokens(relayconstant.RelayModeEmbeddings, []string{`{"choices":[{"text":"x"}]}`}, &b, new(int)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Len() != 0 {
		t.Errorf("builder should stay empty for unhandled relay mode, got %q", b.String())
	}
}

// ---------------------------------------------------------------------------
// handleLastResponse
// ---------------------------------------------------------------------------

func TestHandleLastResponse_PopulatesMetadataAndUsage(t *testing.T) {
	data := `{"id":"chatcmpl-1","created":1700000000,"system_fingerprint":"fp_1","model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15},"choices":[{"delta":{}}]}`
	var responseId, systemFingerprint, model string
	var createAt int64
	var usage *dto.Usage
	var containStreamUsage bool
	shouldSendLastResp := true
	info := &relaycommon.RelayInfo{ShouldIncludeUsage: true}

	err := handleLastResponse(data, &responseId, &createAt, &systemFingerprint, &model, &usage, &containStreamUsage, info, &shouldSendLastResp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if responseId != "chatcmpl-1" || model != "gpt-4o" || systemFingerprint != "fp_1" || createAt != 1700000000 {
		t.Errorf("metadata not extracted correctly: id=%q model=%q fp=%q created=%d", responseId, model, systemFingerprint, createAt)
	}
	if !containStreamUsage {
		t.Error("containStreamUsage should be true when a valid usage object is present")
	}
	if usage == nil || usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v, want TotalTokens=15", usage)
	}
}

func TestHandleLastResponse_ShouldIncludeUsageFalse_ContentPresentTriggersSend(t *testing.T) {
	content := "final chunk text"
	data := `{"id":"c1","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"choices":[{"delta":{"content":"` + content + `"}}]}`
	var responseId, systemFingerprint, model string
	var createAt int64
	var usage *dto.Usage
	var containStreamUsage bool
	shouldSendLastResp := false // start false; only set true by lo.SomeBy match
	info := &relaycommon.RelayInfo{ShouldIncludeUsage: false}

	if err := handleLastResponse(data, &responseId, &createAt, &systemFingerprint, &model, &usage, &containStreamUsage, info, &shouldSendLastResp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shouldSendLastResp {
		t.Error("shouldSendLastResp should flip true: last chunk carries real content even though ShouldIncludeUsage=false")
	}
}

func TestHandleLastResponse_NoUsage_ContainStreamUsageNeverReset(t *testing.T) {
	data := `{"id":"c1","choices":[{"delta":{"content":"hi"}}]}`
	var responseId, systemFingerprint, model string
	var createAt int64
	var usage *dto.Usage
	containStreamUsage := true // pre-seed true to prove it is NOT reset by an absent usage
	shouldSendLastResp := true
	info := &relaycommon.RelayInfo{}

	if err := handleLastResponse(data, &responseId, &createAt, &systemFingerprint, &model, &usage, &containStreamUsage, info, &shouldSendLastResp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if responseId != "c1" {
		t.Errorf("responseId = %q, want %q (proves real parsing ran)", responseId, "c1")
	}
	if !containStreamUsage {
		t.Error("containStreamUsage must stay true (caller-seeded) — handleLastResponse only ever sets it true, never resets it on a missing usage field")
	}
}

func TestHandleLastResponse_MalformedJSON(t *testing.T) {
	var responseId, systemFingerprint, model string
	var createAt int64
	var usage *dto.Usage
	var containStreamUsage bool
	shouldSendLastResp := true
	info := &relaycommon.RelayInfo{}

	err := handleLastResponse(`{not-json`, &responseId, &createAt, &systemFingerprint, &model, &usage, &containStreamUsage, info, &shouldSendLastResp)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// ---------------------------------------------------------------------------
// HandleFinalResponse (OpenAI format branch)
// ---------------------------------------------------------------------------

func TestHandleFinalResponse_OpenAIFormat_SkipsUsageWhenAlreadyContained(t *testing.T) {
	w := newRecorderCtx(t)
	c := w.ctx
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat:        "openai",
		ShouldIncludeUsage: true,
	}
	usage := &dto.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}
	HandleFinalResponse(c, info, `{"id":"x"}`, "chatcmpl-x", 1700000000, "gpt-4o", "fp", usage, true)
	body := w.rec.Body.String()
	// containStreamUsage=true -> the synthetic usage chunk must NOT be emitted,
	// only the terminal [DONE] marker.
	if strings.Contains(body, `"total_tokens":7`) {
		t.Errorf("body should not contain a synthetic usage frame when containStreamUsage=true: %q", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("body should always end with [DONE], got %q", body)
	}
}

func TestHandleFinalResponse_OpenAIFormat_EmitsUsageFrame(t *testing.T) {
	w := newRecorderCtx(t)
	c := w.ctx
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat:        "openai",
		ShouldIncludeUsage: true,
	}
	usage := &dto.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}
	HandleFinalResponse(c, info, `{"id":"x"}`, "chatcmpl-x", 1700000000, "gpt-4o", "fp", usage, false)
	body := w.rec.Body.String()
	if !strings.Contains(body, `"total_tokens":7`) {
		t.Errorf("body should contain the synthetic usage frame when ShouldIncludeUsage && !containStreamUsage, got %q", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("body should end with [DONE], got %q", body)
	}
}

func TestHandleFinalResponse_ClaudeFormat_MarksDone(t *testing.T) {
	w := newRecorderCtx(t)
	c := w.ctx
	info := &relaycommon.RelayInfo{
		ChannelMeta:       &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat:       "claude",
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{},
	}
	usage := &dto.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}
	lastData := `{"id":"c1","choices":[{"delta":{},"finish_reason":"stop"}]}`
	HandleFinalResponse(c, info, lastData, "c1", 1700000000, "gpt-4o", "fp", usage, false)
	if !info.ClaudeConvertInfo.Done {
		t.Error("ClaudeConvertInfo.Done should be set true after final claude-format response")
	}
	if info.ClaudeConvertInfo.Usage != usage {
		t.Error("ClaudeConvertInfo.Usage should be set to the passed-in usage pointer")
	}
}

func TestHandleFinalResponse_ClaudeFormat_MalformedDataReturnsEarly(t *testing.T) {
	w := newRecorderCtx(t)
	c := w.ctx
	info := &relaycommon.RelayInfo{
		ChannelMeta:       &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat:       "claude",
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{},
	}
	usage := &dto.Usage{TotalTokens: 1}
	HandleFinalResponse(c, info, `{not-json`, "c1", 0, "gpt-4o", "fp", usage, false)
	if info.ClaudeConvertInfo.Done {
		t.Error("Done should NOT be set when the last stream data fails to unmarshal (early return)")
	}
}

// ---------------------------------------------------------------------------
// HandleStreamFormat dispatch
// ---------------------------------------------------------------------------

func TestHandleStreamFormat_OpenAIDefaultDispatchesToSendStreamData(t *testing.T) {
	w := newRecorderCtx(t)
	c := w.ctx
	info := &relaycommon.RelayInfo{RelayFormat: "openai"}
	err := HandleStreamFormat(c, info, `{"id":"c1","choices":[{"delta":{"content":"hi"}}]}`, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SendResponseCount != 1 {
		t.Errorf("SendResponseCount = %d, want 1 (incremented on every call)", info.SendResponseCount)
	}
	if !strings.Contains(w.rec.Body.String(), `"content":"hi"`) {
		t.Errorf("body should contain raw passthrough content, got %q", w.rec.Body.String())
	}
}

func TestHandleStreamFormat_ClaudeMalformedPropagatesError(t *testing.T) {
	w := newRecorderCtx(t)
	c := w.ctx
	info := &relaycommon.RelayInfo{RelayFormat: "claude", ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{}}
	err := HandleStreamFormat(c, info, `{not-json`, false, false)
	if err == nil {
		t.Fatal("expected error for malformed claude-format stream chunk")
	}
}

func TestHandleStreamFormat_GeminiMalformedPropagatesError(t *testing.T) {
	w := newRecorderCtx(t)
	c := w.ctx
	info := &relaycommon.RelayInfo{RelayFormat: "gemini"}
	err := HandleStreamFormat(c, info, `{not-json`, false, false)
	if err == nil {
		t.Fatal("expected error for malformed gemini-format stream chunk")
	}
}

// ---------------------------------------------------------------------------
// applyUsagePostProcessing / extractCachedTokensFromBody / extractMoonshotCachedTokensFromBody
// ---------------------------------------------------------------------------

func TestApplyUsagePostProcessing_NilGuards(t *testing.T) {
	// must not panic
	applyUsagePostProcessing(nil, &dto.Usage{}, nil)
	applyUsagePostProcessing(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, nil, nil)
}

func TestApplyUsagePostProcessing_DeepSeek_PromoteCacheHitTokens(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeDeepSeek}}
	usage := &dto.Usage{PromptCacheHitTokens: 42}
	applyUsagePostProcessing(info, usage, nil)
	if usage.PromptTokensDetails.CachedTokens != 42 {
		t.Errorf("CachedTokens = %d, want 42 promoted from PromptCacheHitTokens", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestApplyUsagePostProcessing_DeepSeek_DoesNotOverwriteExisting(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeDeepSeek}}
	usage := &dto.Usage{PromptCacheHitTokens: 42, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 7}}
	applyUsagePostProcessing(info, usage, nil)
	if usage.PromptTokensDetails.CachedTokens != 7 {
		t.Errorf("CachedTokens = %d, want unchanged 7 (already non-zero)", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestApplyUsagePostProcessing_Zhipu_FallsBackThroughPriorityChain(t *testing.T) {
	t.Run("InputTokensDetails wins first", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZhipu_v4}}
		usage := &dto.Usage{InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 11}, PromptCacheHitTokens: 99}
		applyUsagePostProcessing(info, usage, nil)
		if usage.PromptTokensDetails.CachedTokens != 11 {
			t.Errorf("CachedTokens = %d, want 11 from InputTokensDetails", usage.PromptTokensDetails.CachedTokens)
		}
	})

	t.Run("falls back to response body extraction", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZhipu_v4}}
		usage := &dto.Usage{}
		body := []byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":33}}}`)
		applyUsagePostProcessing(info, usage, body)
		if usage.PromptTokensDetails.CachedTokens != 33 {
			t.Errorf("CachedTokens = %d, want 33 extracted from body", usage.PromptTokensDetails.CachedTokens)
		}
	})

	t.Run("falls back to PromptCacheHitTokens as last resort", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZhipu_v4}}
		usage := &dto.Usage{PromptCacheHitTokens: 55}
		applyUsagePostProcessing(info, usage, []byte(`{}`))
		if usage.PromptTokensDetails.CachedTokens != 55 {
			t.Errorf("CachedTokens = %d, want 55 from PromptCacheHitTokens fallback", usage.PromptTokensDetails.CachedTokens)
		}
	})
}

func TestApplyUsagePostProcessing_Moonshot_ExtractsFromChoicesArray(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot}}
	usage := &dto.Usage{}
	body := []byte(`{"choices":[{"usage":{"cached_tokens":77}}]}`)
	applyUsagePostProcessing(info, usage, body)
	if usage.PromptTokensDetails.CachedTokens != 77 {
		t.Errorf("CachedTokens = %d, want 77 extracted from Moonshot choices[].usage", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestApplyUsagePostProcessing_Moonshot_InputTokensDetailsWinsFirst(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot}}
	usage := &dto.Usage{InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 21}}
	body := []byte(`{"choices":[{"usage":{"cached_tokens":77}}]}`)
	applyUsagePostProcessing(info, usage, body)
	if usage.PromptTokensDetails.CachedTokens != 21 {
		t.Errorf("CachedTokens = %d, want 21 from InputTokensDetails (highest priority, must beat the choices[] body extraction)", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestApplyUsagePostProcessing_Moonshot_FallsBackToGenericBodyExtraction(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot}}
	usage := &dto.Usage{}
	// No choices[].usage.cached_tokens (Moonshot's non-standard spot), but the
	// standard usage.prompt_tokens_details.cached_tokens location is present.
	body := []byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":9}}}`)
	applyUsagePostProcessing(info, usage, body)
	if usage.PromptTokensDetails.CachedTokens != 9 {
		t.Errorf("CachedTokens = %d, want 9 from the generic-body-extraction fallback", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestApplyUsagePostProcessing_Moonshot_FallsBackToPromptCacheHitTokens(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot}}
	usage := &dto.Usage{PromptCacheHitTokens: 64}
	applyUsagePostProcessing(info, usage, []byte(`{}`))
	if usage.PromptTokensDetails.CachedTokens != 64 {
		t.Errorf("CachedTokens = %d, want 64 as the last-resort fallback when no body extraction succeeds", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestApplyUsagePostProcessing_UnrelatedChannelUntouched(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
	usage := &dto.Usage{PromptCacheHitTokens: 123}
	applyUsagePostProcessing(info, usage, nil)
	if usage.PromptTokensDetails.CachedTokens != 0 {
		t.Errorf("CachedTokens = %d, want 0 (openai channel has no special-case handling)", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestExtractCachedTokensFromBody(t *testing.T) {
	t.Run("empty body", func(t *testing.T) {
		if _, ok := extractCachedTokensFromBody(nil); ok {
			t.Error("empty body should not extract")
		}
	})
	t.Run("malformed JSON", func(t *testing.T) {
		if _, ok := extractCachedTokensFromBody([]byte(`{not-json`)); ok {
			t.Error("malformed JSON should not extract")
		}
	})
	t.Run("standard position wins", func(t *testing.T) {
		v, ok := extractCachedTokensFromBody([]byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":5},"cached_tokens":9,"prompt_cache_hit_tokens":2}}`))
		if !ok || v != 5 {
			t.Errorf("got (%d,%v), want (5,true)", v, ok)
		}
	})
	t.Run("zero value is still a valid extracted signal", func(t *testing.T) {
		// cached_tokens present but explicitly 0 -- distinguishing "known zero"
		// from "field absent" matters for billing correctness.
		v, ok := extractCachedTokensFromBody([]byte(`{"usage":{"cached_tokens":0}}`))
		if !ok || v != 0 {
			t.Errorf("got (%d,%v), want (0,true) since the field was present", v, ok)
		}
	})
	t.Run("no matching field", func(t *testing.T) {
		if _, ok := extractCachedTokensFromBody([]byte(`{"usage":{}}`)); ok {
			t.Error("no cached-token field present should not extract")
		}
	})
}

func TestExtractMoonshotCachedTokensFromBody(t *testing.T) {
	t.Run("empty body", func(t *testing.T) {
		if _, ok := extractMoonshotCachedTokensFromBody(nil); ok {
			t.Error("empty body should not extract")
		}
	})
	t.Run("skips zero entries and returns first positive", func(t *testing.T) {
		body := []byte(`{"choices":[{"usage":{"cached_tokens":0}},{"usage":{"cached_tokens":8}}]}`)
		v, ok := extractMoonshotCachedTokensFromBody(body)
		if !ok || v != 8 {
			t.Errorf("got (%d,%v), want (8,true)", v, ok)
		}
	})
	t.Run("no choices present", func(t *testing.T) {
		if _, ok := extractMoonshotCachedTokensFromBody([]byte(`{"choices":[]}`)); ok {
			t.Error("empty choices should not extract")
		}
	})
}

