package openai

// Business-acceptance tests closing the remaining billing-relevant and
// format-dispatch gaps in this already-heavily-tested package:
//   - per-vendor cached-token extraction fallbacks (Zhipu's generic
//     prompt_cache_hit_tokens-in-body path, Moonshot's per-choice scan that
//     must skip a zero/absent value and keep looking) — wrong here means a
//     tenant is billed for tokens they got a cache discount on;
//   - the AudioTranslation relay mode, which shares OpenaiSTTHandler with
//     AudioTranscription via a `fallthrough` in DoResponse;
//   - graceful (non-panicking, no-write) handling of an unrecognized
//     RelayFormat and of malformed upstream data on the Gemini final-chunk
//     path.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ---------------------------------------------------------------------------
// applyUsagePostProcessing: cached-token billing fallbacks
// ---------------------------------------------------------------------------

func TestR6bApplyUsagePostProcessing_Zhipu_FallsBackToPromptCacheHitTokensInBody(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZhipu_v4}}
	usage := &dto.Usage{PromptTokens: 100}
	// No prompt_tokens_details.cached_tokens and no top-level cached_tokens in
	// the body — only the generic prompt_cache_hit_tokens field is present, so
	// extractCachedTokensFromBody must fall through to its third branch.
	body := []byte(`{"usage":{"prompt_cache_hit_tokens":77}}`)
	applyUsagePostProcessing(info, usage, body)
	if usage.PromptTokensDetails.CachedTokens != 77 {
		t.Fatalf("CachedTokens = %d, want 77 extracted from body's prompt_cache_hit_tokens field", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestR6bApplyUsagePostProcessing_Moonshot_SkipsZeroChoiceThenUsesRealValue(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot}}
	usage := &dto.Usage{PromptTokens: 50}
	// First choice has no real cached_tokens (must be skipped, not treated as
	// the answer); the second choice carries the actual billable value.
	body := []byte(`{"choices":[{"usage":{"cached_tokens":0}},{"usage":{"cached_tokens":42}}]}`)
	applyUsagePostProcessing(info, usage, body)
	if usage.PromptTokensDetails.CachedTokens != 42 {
		t.Fatalf("CachedTokens = %d, want 42 (from the second choice, first zero-value choice must be skipped)", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestR6bApplyUsagePostProcessing_Moonshot_NoUsableCachedTokensLeavesZero(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot}}
	usage := &dto.Usage{PromptTokens: 50}
	body := []byte(`{"choices":[{"usage":{"cached_tokens":0}}]}`)
	applyUsagePostProcessing(info, usage, body)
	if usage.PromptTokensDetails.CachedTokens != 0 {
		t.Fatalf("CachedTokens = %d, want 0 when no choice reports a positive cached_tokens (must not fabricate a discount)", usage.PromptTokensDetails.CachedTokens)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: AudioTranslation shares OpenaiSTTHandler via fallthrough
// ---------------------------------------------------------------------------

func TestR6bAdaptor_DoResponse_AudioTranslation_DelegatesToSTTHandler(t *testing.T) {
	a := &Adaptor{}
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayMode:   relayconstant.RelayModeAudioTranslation,
	}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"text":"bonjour becomes hello"}`)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(w.ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	du, ok := usage.(*dto.Usage)
	if !ok || du == nil {
		t.Fatalf("usage type = %T, want *dto.Usage (proves the AudioTranslation branch fell through into the same STT handler as AudioTranscription)", usage)
	}
}

// ---------------------------------------------------------------------------
// HandleStreamFormat: unrecognized RelayFormat is a graceful no-op
// ---------------------------------------------------------------------------

func TestR6bHandleStreamFormat_UnrecognizedFormat_NoopNoWrite(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		// RelayFormatRerank is a real, valid format value but is not one of
		// the three streaming formats HandleStreamFormat switches on.
		RelayFormat: types.RelayFormatRerank,
	}
	err := HandleStreamFormat(w.ctx, info, `{"choices":[{"delta":{"content":"x"}}]}`, false, false)
	if err != nil {
		t.Fatalf("unexpected error for unrecognized format: %v", err)
	}
	if w.rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: an unrecognized RelayFormat must not emit any SSE frame", w.rec.Body.String())
	}
	if info.SendResponseCount != 1 {
		t.Errorf("SendResponseCount = %d, want 1 (the counter increments regardless of which format branch runs)", info.SendResponseCount)
	}
}

// ---------------------------------------------------------------------------
// HandleFinalResponse: Gemini format, malformed upstream data must not panic
// ---------------------------------------------------------------------------

func TestR6bHandleFinalResponse_GeminiFormat_MalformedDataReturnsEarlyNoWrite(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: types.RelayFormatGemini,
	}
	usage := &dto.Usage{TotalTokens: 1}
	// Must not panic on malformed lastStreamData, and must not write a
	// half-formed SSE frame to the client.
	HandleFinalResponse(w.ctx, info, `{not-json`, "c1", 0, "gemini-model", "fp", usage, false)
	if w.rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: malformed final chunk must short-circuit before any Gemini frame is rendered", w.rec.Body.String())
	}
}
