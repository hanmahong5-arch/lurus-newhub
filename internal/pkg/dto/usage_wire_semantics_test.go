package dto

// usage_wire_semantics_test.go — the two presentation derivations on Usage.
//
// Usage is the settlement record and keeps the semantics of the wire it was
// parsed from (PromptTokensIncludeCached says which). When the figure is shown
// to a caller on the OTHER wire:
//
//   - Anthropic wire: input_tokens EXCLUDES cache read and cache creation
//     (the three terms are mutually exclusive), so an includes-cached prompt
//     has both subtracted — AnthropicInputTokens.
//   - OpenAI/Gemini wire: prompt_tokens/promptTokenCount INCLUDES the cached
//     slice and cached_tokens is the subset that hit, so an Anthropic
//     input_tokens has both added back and total_tokens recomputed —
//     AsOpenAIWire.
//
// Neither method changes the receiver; both are identity when the record is
// already in the target semantics, and both are wire-independent when there
// is no cache activity (so unflagged adaptors are bit-for-bit unaffected).

import "testing"

func wireUsage(flag bool, prompt, cached, creation, completion int) Usage {
	return Usage{
		PromptTokens:              prompt,
		CompletionTokens:          completion,
		TotalTokens:               prompt + completion,
		PromptTokensIncludeCached: flag,
		PromptTokensDetails:       InputTokenDetails{CachedTokens: cached, CachedCreationTokens: creation},
	}
}

func TestUsage_AnthropicInputTokens(t *testing.T) {
	cases := []struct {
		name string
		u    Usage
		want int
	}{
		{"openai wire, read only: 120-50", wireUsage(true, 120, 50, 0, 30), 70},
		{"openai wire, read+creation: 3527-3456-17", wireUsage(true, 3527, 3456, 17, 41), 54},
		{"anthropic wire is already exclusive: 70 stays 70", wireUsage(false, 70, 50, 0, 30), 70},
		{"anthropic wire with creation stays put", wireUsage(false, 11, 3, 2, 7), 11},
		{"no cache activity, flag true: identity", wireUsage(true, 100, 0, 0, 5), 100},
		{"no cache activity, flag false: identity", wireUsage(false, 100, 0, 0, 5), 100},
		{"upstream misreport cached > prompt clamps to 0, never negative", wireUsage(true, 40, 50, 0, 1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.u
			if got := tc.u.AnthropicInputTokens(); got != tc.want {
				t.Errorf("AnthropicInputTokens() = %d, want %d", got, tc.want)
			}
			if tc.u != before {
				t.Errorf("receiver mutated: %+v -> %+v", before, tc.u)
			}
		})
	}
}

func TestUsage_AsOpenAIWire(t *testing.T) {
	t.Run("anthropic wire: cache terms added back, total recomputed, flag set", func(t *testing.T) {
		u := wireUsage(false, 70, 50, 20, 30)
		got := u.AsOpenAIWire()
		if got.PromptTokens != 140 || got.TotalTokens != 170 || got.CompletionTokens != 30 || !got.PromptTokensIncludeCached {
			t.Errorf("AsOpenAIWire() = prompt %d total %d completion %d flag %v, want 140/170/30/true",
				got.PromptTokens, got.TotalTokens, got.CompletionTokens, got.PromptTokensIncludeCached)
		}
		if got.PromptTokensDetails.CachedTokens != 50 || got.PromptTokensDetails.CachedCreationTokens != 20 {
			t.Errorf("cache detail fields changed: %+v", got.PromptTokensDetails)
		}
		if u.PromptTokens != 70 || u.TotalTokens != 100 || u.PromptTokensIncludeCached {
			t.Errorf("receiver (the settlement record) mutated: %+v", u)
		}
	})
	t.Run("openai wire is returned unchanged", func(t *testing.T) {
		u := wireUsage(true, 120, 50, 0, 30)
		if got := u.AsOpenAIWire(); got != u {
			t.Errorf("AsOpenAIWire() = %+v, want identity %+v", got, u)
		}
	})
	t.Run("idempotent", func(t *testing.T) {
		u := wireUsage(false, 70, 50, 20, 30)
		once := u.AsOpenAIWire()
		if twice := once.AsOpenAIWire(); twice != once {
			t.Errorf("second application changed the figure: %+v -> %+v (cache terms added twice)", once, twice)
		}
	})
	t.Run("no cache activity: identity on both flags", func(t *testing.T) {
		for _, flag := range []bool{false, true} {
			u := wireUsage(flag, 100, 0, 0, 5)
			got := u.AsOpenAIWire()
			if got.PromptTokens != 100 || got.TotalTokens != 105 {
				t.Errorf("flag=%v: prompt/total = %d/%d, want 100/105", flag, got.PromptTokens, got.TotalTokens)
			}
		}
	})
}
