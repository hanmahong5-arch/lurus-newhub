package app

// convert_response_cache_usage_test.go — the NON-streaming OpenAI→Claude
// response conversion must carry the cache token counts, exactly as its
// streaming twin does.
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
// Mutation oracle: drop either cache field from the ClaudeUsage literal in
// ResponseOpenAI2Claude and the matching assertion here goes red.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestResponseOpenAI2Claude_CarriesCacheTokenCounts(t *testing.T) {
	in := &dto.OpenAITextResponse{
		Id:      "resp_cache",
		Model:   "some-openai-wire-model",
		Choices: []dto.OpenAITextResponseChoice{textChoice("ok", "stop")},
		Usage: dto.Usage{
			PromptTokens:     3127,
			CompletionTokens: 5,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         3072,
				CachedCreationTokens: 41,
			},
		},
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
	// Existing fields must be untouched.
	if out.Usage.InputTokens != 3127 || out.Usage.OutputTokens != 5 {
		t.Errorf("input/output tokens = %d/%d, want 3127/5", out.Usage.InputTokens, out.Usage.OutputTokens)
	}
}

// TestResponseOpenAI2Claude_NoCacheStaysZero is the negative control: an
// upstream that reports no cache activity must not grow phantom cache counts.
func TestResponseOpenAI2Claude_NoCacheStaysZero(t *testing.T) {
	in := &dto.OpenAITextResponse{
		Id:      "resp_nocache",
		Model:   "some-openai-wire-model",
		Choices: []dto.OpenAITextResponseChoice{textChoice("ok", "stop")},
		Usage:   dto.Usage{PromptTokens: 12, CompletionTokens: 3},
	}

	out := ResponseOpenAI2Claude(in, nonOpenRouterInfo())
	if out.Usage.CacheReadInputTokens != 0 || out.Usage.CacheCreationInputTokens != 0 {
		t.Errorf("cache fields = %d/%d, want 0/0 for an upstream that reported no cache activity",
			out.Usage.CacheReadInputTokens, out.Usage.CacheCreationInputTokens)
	}
}
