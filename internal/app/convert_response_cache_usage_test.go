package app

// convert_response_cache_usage_test.go — the NON-streaming OpenAI→Claude
// response conversion must carry the cache token counts, exactly as its
// streaming twin does, and must present input_tokens in the CALLER's wire
// semantics.
//
// The streaming path (convert.go, message_delta usage) has always mapped
// CacheCreationInputTokens / CacheReadInputTokens; the non-streaming builder
// filled only InputTokens/OutputTokens. Net effect on a Claude-wire client
// talking to an OpenAI-wire upstream: `cache_read_input_tokens` came back 0 on
// every non-streamed reply while the request was in fact billed at the cache
// discount — measured on UAT, a request whose upstream reported 3072 cached
// tokens (and settled 111 quota instead of the uncached 422) returned
// {"cache_read_input_tokens":0,"cache_creation_input_tokens":0}. Cache-hit-rate
// dashboards built on the Anthropic SDK therefore read a flat zero forever.
//
// Second half (2026-09-02): the lock this file used to carry expected
// input_tokens == 3127 — the OpenAI prompt_tokens, which INCLUDES the 3072
// cached and 41 cache-creation tokens — while also emitting both cache fields.
// Anthropic defines input_tokens as the tokens NOT read from or written to the
// cache, so a Claude SDK doing input + cache_read + cache_creation read 6240 for
// a 3127-token prompt. That number pinned "fields carried", not semantics; the
// fixture never set the wire flag, so the assertion could not tell the two
// apart. It now sets the flag (the fixture always described an OpenAI-wire
// upstream) and expects 3127 - 3072 - 41 = 14, and the sibling test below
// pins that an unflagged (already Anthropic-semantics) usage is left alone.
//
// Mutation oracle: drop either cache field from claudeTerminalUsage and the
// matching assertion here goes red; revert InputTokens to the raw prompt and
// the 14 goes red; drop the flag short-circuit in AnthropicInputTokens and the
// unflagged sibling goes red.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func cacheFixtureUsage(includesCached bool) dto.Usage {
	return dto.Usage{
		PromptTokens:     3127,
		CompletionTokens: 5,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         3072,
			CachedCreationTokens: 41,
		},
		PromptTokensIncludeCached: includesCached,
	}
}

func TestResponseOpenAI2Claude_CarriesCacheTokenCounts(t *testing.T) {
	in := &dto.OpenAITextResponse{
		Id:      "resp_cache",
		Model:   "some-openai-wire-model",
		Choices: []dto.OpenAITextResponseChoice{textChoice("ok", "stop")},
		Usage:   cacheFixtureUsage(true), // OpenAI wire: prompt_tokens includes the cached slice
	}

	out := ResponseOpenAI2Claude(in, nonOpenRouterInfo())
	if out.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if out.Usage.CacheReadInputTokens != 3072 {
		t.Errorf("cache_read_input_tokens = %d, want 3072 — a Claude-wire client cannot see its cache "+
			"hits, so cache-hit-rate monitoring reads zero while the request is billed at the cache discount",
			out.Usage.CacheReadInputTokens)
	}
	if out.Usage.CacheCreationInputTokens != 41 {
		t.Errorf("cache_creation_input_tokens = %d, want 41", out.Usage.CacheCreationInputTokens)
	}
	// Anthropic semantics: input_tokens excludes both cache terms.
	if out.Usage.InputTokens != 14 || out.Usage.OutputTokens != 5 {
		t.Errorf("input/output tokens = %d/%d, want 14/5 (3127 prompt - 3072 cached - 41 creation); "+
			"the raw OpenAI prompt_tokens would make a Claude SDK count the cached slice twice",
			out.Usage.InputTokens, out.Usage.OutputTokens)
	}
}

// TestResponseOpenAI2Claude_AnthropicSemanticsSourceIsNotReduced is the
// negative control for the subtraction: a usage whose prompt already excludes
// the cache terms (flag false) must be passed through unchanged, or a
// Claude-format passthrough would under-report input_tokens.
func TestResponseOpenAI2Claude_AnthropicSemanticsSourceIsNotReduced(t *testing.T) {
	in := &dto.OpenAITextResponse{
		Id:      "resp_cache_anthropic",
		Model:   "some-model",
		Choices: []dto.OpenAITextResponseChoice{textChoice("ok", "stop")},
		Usage:   cacheFixtureUsage(false),
	}

	out := ResponseOpenAI2Claude(in, nonOpenRouterInfo())
	if out.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if out.Usage.InputTokens != 3127 {
		t.Errorf("input_tokens = %d, want 3127: the source already excludes cache read/creation, "+
			"subtracting them again under-reports the caller's uncached input", out.Usage.InputTokens)
	}
	if out.Usage.CacheReadInputTokens != 3072 || out.Usage.CacheCreationInputTokens != 41 {
		t.Errorf("cache fields = %d/%d, want 3072/41", out.Usage.CacheReadInputTokens, out.Usage.CacheCreationInputTokens)
	}
}

// TestResponseOpenAI2Claude_NoCacheStaysZero is the negative control: an
// upstream that reports no cache activity must not grow phantom cache counts,
// and its input_tokens is the prompt whichever wire it came from.
func TestResponseOpenAI2Claude_NoCacheStaysZero(t *testing.T) {
	for _, flag := range []bool{false, true} {
		in := &dto.OpenAITextResponse{
			Id:      "resp_nocache",
			Model:   "some-openai-wire-model",
			Choices: []dto.OpenAITextResponseChoice{textChoice("ok", "stop")},
			Usage:   dto.Usage{PromptTokens: 12, CompletionTokens: 3, PromptTokensIncludeCached: flag},
		}

		out := ResponseOpenAI2Claude(in, nonOpenRouterInfo())
		if out.Usage.CacheReadInputTokens != 0 || out.Usage.CacheCreationInputTokens != 0 {
			t.Errorf("flag=%v: cache fields = %d/%d, want 0/0 for an upstream that reported no cache activity",
				flag, out.Usage.CacheReadInputTokens, out.Usage.CacheCreationInputTokens)
		}
		if out.Usage.InputTokens != 12 {
			t.Errorf("flag=%v: input_tokens = %d, want 12 (no cache activity: the figure is wire-independent)", flag, out.Usage.InputTokens)
		}
	}
}
