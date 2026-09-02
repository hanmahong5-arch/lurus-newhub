package gemini

// usage_cached_content_test.go — Gemini's cachedContentTokenCount must reach
// both the ledger and the caller.
//
// Until 2026-09-01 dto.GeminiUsageMetadata had no field for it, so a cache hit
// on a Gemini channel was billed at full input price and the OpenAI-wire reply
// carried cached_tokens=0. The same defect class had just been closed for
// OpenAI->Claude (claudeTerminalUsage); this file closes the Gemini->OpenAI and
// OpenAI->Gemini directions and pins them against each other.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestBuildUsageFromGeminiMetadata_CachedContent(t *testing.T) {
	got := buildUsageFromGeminiMetadata(dto.GeminiUsageMetadata{
		PromptTokenCount:        1000, // includes the 700 cached
		CandidatesTokenCount:    50,
		ThoughtsTokenCount:      20,
		TotalTokenCount:         1070,
		CachedContentTokenCount: 700,
	})
	if got.PromptTokensDetails.CachedTokens != 700 {
		t.Errorf("CachedTokens = %d, want 700 — cachedContentTokenCount dropped on the floor, the cache hit is billed at full price", got.PromptTokensDetails.CachedTokens)
	}
	if !got.PromptTokensIncludeCached {
		t.Error("PromptTokensIncludeCached = false; Gemini counts the cached slice INSIDE promptTokenCount, so without the flag " +
			"the settlement paths bill 1000 at full price PLUS 700 at CacheRatio — the same double charge fixed for OpenAI-wire channels on 2026-08-29")
	}
	if got.PromptTokens != 1000 {
		t.Errorf("PromptTokens = %d, want the raw upstream 1000 (the deduction belongs to the billing base, not the parsed figure)", got.PromptTokens)
	}
}

// The non-streaming handler: the figure must reach the returned usage (ledger)
// and the OpenAI-format body (caller) in the same request.
func TestGeminiChatHandler_CachedContentReachesLedgerAndCaller(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	body := `{
		"candidates": [{"content": {"parts":[{"text":"hi"}]}, "finishReason":"STOP", "index":0}],
		"usageMetadata": {"promptTokenCount":1000,"candidatesTokenCount":5,"totalTokenCount":1005,"cachedContentTokenCount":700}
	}`
	resp := respFromBody(200, body)
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := GeminiChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokensDetails.CachedTokens != 700 || !usage.PromptTokensIncludeCached {
		t.Errorf("ledger usage = cached %d / includeCached %v, want 700 / true", usage.PromptTokensDetails.CachedTokens, usage.PromptTokensIncludeCached)
	}
	var out dto.OpenAITextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body not OpenAI JSON: %v (%s)", err, w.Body.String())
	}
	if out.Usage.PromptTokensDetails.CachedTokens != 700 {
		t.Errorf("caller sees prompt_tokens_details.cached_tokens=%d, want 700 — the customer cannot reconcile a cache discount they cannot see", out.Usage.PromptTokensDetails.CachedTokens)
	}
}

// The streaming handler merges per-frame usageMetadata into one accumulator.
// That merge used to copy five named fields by hand, which would have carried
// CachedTokens but silently dropped the wire-semantics flag — a request billed
// at full price PLUS cache price. Asserting the flag after a full stream is
// what pins the whole-value assignment.
func TestGeminiChatStreamHandler_CachedContentSurvivesMerge(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{},
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true, // stream_options.include_usage — the only way an OpenAI-wire stream carries usage
	}
	body := sseBody(
		`{"candidates":[{"content":{"parts":[{"text":"Hel"}]},"index":0}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":2,"totalTokenCount":1002,"cachedContentTokenCount":700}}`,
	)
	resp := respFromBody(200, body)
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := GeminiChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokensDetails.CachedTokens != 700 {
		t.Errorf("stream ledger CachedTokens = %d, want 700", usage.PromptTokensDetails.CachedTokens)
	}
	if !usage.PromptTokensIncludeCached {
		t.Error("stream ledger lost PromptTokensIncludeCached across the per-frame merge — cached tokens would be billed at full price plus CacheRatio")
	}
	if out := w.Body.String(); !strings.Contains(out, `"cached_tokens":700`) {
		t.Errorf("stream output does not carry cached_tokens=700 to the caller: %s", out)
	}
}

// Round trip: an OpenAI-wire usage converted for a Gemini-wire client and then
// parsed back as if it came from a Gemini upstream must reproduce the numbers
// that matter for billing and reconciliation. This is the lock that keeps
// app.geminiUsageMetadata and buildUsageFromGeminiMetadata as inverses — the
// two sides normalise thoughts differently (OpenAI completion INCLUDES
// reasoning, Gemini candidates EXCLUDE it) and this is where a mismatch shows.
func TestGeminiUsageMetadata_RoundTripsThroughBuildUsage(t *testing.T) {
	in := dto.Usage{
		PromptTokens:     3527,
		CompletionTokens: 141, // 100 visible + 41 reasoning
		TotalTokens:      3668,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 3456,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 41},
	}
	gem := app.ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Id:      "resp",
		Model:   "m",
		Choices: []dto.OpenAITextResponseChoice{{Index: 0, Message: dto.Message{Role: "assistant", Content: "hi"}, FinishReason: "stop"}},
		Usage:   in,
	}, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}})

	if gem.UsageMetadata.CandidatesTokenCount != 100 || gem.UsageMetadata.ThoughtsTokenCount != 41 {
		t.Errorf("Gemini wire candidates/thoughts = %d/%d, want 100/41 (candidatesTokenCount must EXCLUDE thoughts)",
			gem.UsageMetadata.CandidatesTokenCount, gem.UsageMetadata.ThoughtsTokenCount)
	}
	if gem.UsageMetadata.CachedContentTokenCount != 3456 {
		t.Errorf("Gemini wire cachedContentTokenCount = %d, want 3456", gem.UsageMetadata.CachedContentTokenCount)
	}

	back := buildUsageFromGeminiMetadata(gem.UsageMetadata)
	got := map[string]int{
		"PromptTokens":     back.PromptTokens,
		"CompletionTokens": back.CompletionTokens,
		"TotalTokens":      back.TotalTokens,
		"ReasoningTokens":  back.CompletionTokenDetails.ReasoningTokens,
		"CachedTokens":     back.PromptTokensDetails.CachedTokens,
	}
	want := map[string]int{
		"PromptTokens":     in.PromptTokens,
		"CompletionTokens": in.CompletionTokens,
		"TotalTokens":      in.TotalTokens,
		"ReasoningTokens":  in.CompletionTokenDetails.ReasoningTokens,
		"CachedTokens":     in.PromptTokensDetails.CachedTokens,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip drifted:\n got %v\nwant %v", got, want)
	}
}

// Census: every field of the Gemini usage DTO is either consumed by
// buildUsageFromGeminiMetadata or listed here with the reason it is not.
// Adding a wire field that nobody maps must be a decision, not a silent zero —
// that is exactly how cachedContentTokenCount went missing.
func TestGeminiUsageMetadata_EveryFieldIsMappedOrExplained(t *testing.T) {
	mapped := map[string]bool{
		"PromptTokenCount":        true,
		"CandidatesTokenCount":    true,
		"TotalTokenCount":         true,
		"ThoughtsTokenCount":      true,
		"ToolUsePromptTokenCount": true,
		"CachedContentTokenCount": true,
		"PromptTokensDetails":     true,
	}
	unmapped := map[string]string{}

	typ := reflect.TypeOf(dto.GeminiUsageMetadata{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if mapped[name] {
			continue
		}
		if _, ok := unmapped[name]; ok {
			continue
		}
		t.Errorf("dto.GeminiUsageMetadata.%s is neither mapped by buildUsageFromGeminiMetadata nor listed as unmapped with a reason", name)
	}
	// And the mapped set must be honest: each named field has to actually move
	// the output when set on its own.
	probes := map[string]func(*dto.GeminiUsageMetadata){
		"PromptTokenCount":        func(m *dto.GeminiUsageMetadata) { m.PromptTokenCount = 7 },
		"CandidatesTokenCount":    func(m *dto.GeminiUsageMetadata) { m.CandidatesTokenCount = 7 },
		"TotalTokenCount":         func(m *dto.GeminiUsageMetadata) { m.TotalTokenCount = 7 },
		"ThoughtsTokenCount":      func(m *dto.GeminiUsageMetadata) { m.ThoughtsTokenCount = 7 },
		"ToolUsePromptTokenCount": func(m *dto.GeminiUsageMetadata) { m.ToolUsePromptTokenCount = 7 },
		"CachedContentTokenCount": func(m *dto.GeminiUsageMetadata) { m.CachedContentTokenCount = 7 },
		"PromptTokensDetails": func(m *dto.GeminiUsageMetadata) {
			m.PromptTokensDetails = []dto.GeminiPromptTokensDetails{{Modality: "AUDIO", TokenCount: 7}}
		},
	}
	base := buildUsageFromGeminiMetadata(dto.GeminiUsageMetadata{})
	for name, set := range probes {
		if !mapped[name] {
			t.Fatalf("probe %q is not in the mapped set", name)
		}
		m := dto.GeminiUsageMetadata{}
		set(&m)
		if reflect.DeepEqual(buildUsageFromGeminiMetadata(m), base) {
			t.Errorf("setting dto.GeminiUsageMetadata.%s alone does not change the built usage — it is listed as mapped but is not", name)
		}
	}
}
