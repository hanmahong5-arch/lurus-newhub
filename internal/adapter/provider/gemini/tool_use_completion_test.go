package gemini

// tool_use_completion_test.go — the completion figure on a grounded / tool-use
// Gemini call.
//
// Gemini's totalTokenCount is "prompt + thoughts + response candidates"
// (ai.google.dev/api/generate-content); toolUsePromptTokenCount is reported
// beside it, not inside it. buildUsageFromGeminiMetadata folds the tool-use
// prompt into PromptTokens (it is billed as input), and until 2026-09-02 both
// handlers then overwrote CompletionTokens with TotalTokens - PromptTokens:
// candidates + thoughts - toolUsePrompt. A Google-Search-grounded answer whose
// tool prompt outran the answer produced a NEGATIVE completion count, and the
// non-streaming handler handed that to settlement as a credit against the
// prompt charge. The streaming handler hid it behind its text-length estimate.

import (
	"encoding/json"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// Official composition: prompt 1000 + tool-use prompt 500 (outside the total),
// candidates 5, total 1005.
const groundedUsageMetadata = `"usageMetadata":{"promptTokenCount":1000,"toolUsePromptTokenCount":500,"candidatesTokenCount":5,"totalTokenCount":1005}`

func TestGeminiChatHandler_ToolUsePromptDoesNotEatTheCompletion(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	body := `{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],` + groundedUsageMetadata + `}`
	resp := respFromBody(200, body)
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := GeminiChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 1500 {
		t.Errorf("PromptTokens = %d, want 1500 (prompt + tool-use prompt, both billed as input)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5 (candidatesTokenCount); 1005 - 1500 = -495 was handed to settlement before 2026-09-02", usage.CompletionTokens)
	}
	var out dto.OpenAITextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body not OpenAI JSON: %v (%s)", err, w.Body.String())
	}
	if out.CompletionTokens != 5 {
		t.Errorf("caller sees completion_tokens=%d, want 5", out.CompletionTokens)
	}
}

func TestGeminiChatStreamHandler_ToolUsePromptDoesNotEatTheCompletion(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{},
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
	}
	body := sseBody(
		`{"candidates":[{"content":{"parts":[{"text":"Hel"}]},"index":0}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP","index":0}],`+groundedUsageMetadata+`}`,
	)
	resp := respFromBody(200, body)
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := GeminiChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 1500 || usage.CompletionTokens != 5 {
		t.Errorf("stream usage = prompt %d / completion %d, want 1500 / 5 (the negative 1005-1500 used to fall through to a text-length estimate)",
			usage.PromptTokens, usage.CompletionTokens)
	}
}

// The subtraction survives only as a fallback for an upstream that reports a
// total but no candidate count, and it can never go below zero.
func TestGeminiCompletionTokens_FallbackAndFloor(t *testing.T) {
	cases := []struct {
		name string
		in   dto.Usage
		want int
	}{
		{"builder figure wins", dto.Usage{PromptTokens: 1500, CompletionTokens: 5, TotalTokens: 1005}, 5},
		{"no candidate count: derive from total", dto.Usage{PromptTokens: 100, CompletionTokens: 0, TotalTokens: 130}, 30},
		{"no candidate count and total below prompt: zero, never negative", dto.Usage{PromptTokens: 1500, CompletionTokens: 0, TotalTokens: 1005}, 0},
		{"nothing known", dto.Usage{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := geminiCompletionTokens(tc.in); got != tc.want {
				t.Errorf("geminiCompletionTokens(%+v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
