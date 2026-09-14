package common

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// TestSupportsResponsesCompact_Table is the oracle for the /v1/responses/compact
// channel-type gate (cycle-8 L6): only OpenAI is in scope this cycle;
// everything else — including channels that DO speak an OpenAI-compatible
// wire elsewhere in this codebase (Azure, DeepSeek, OpenRouter) — must be
// refused until explicitly added to responsesCompactSupportedChannels.
func TestSupportsResponsesCompact_Table(t *testing.T) {
	cases := []struct {
		name        string
		channelType int
		want        bool
	}{
		{"OpenAI", constant.ChannelTypeOpenAI, true},
		{"Anthropic", constant.ChannelTypeAnthropic, false},
		{"Azure", constant.ChannelTypeAzure, false},
		{"Gemini", constant.ChannelTypeGemini, false},
		{"DeepSeek", constant.ChannelTypeDeepSeek, false},
		{"unknown/zero", 0, false},
		{"negative", -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SupportsResponsesCompact(tc.channelType); got != tc.want {
				t.Errorf("SupportsResponsesCompact(%d) = %v, want %v", tc.channelType, got, tc.want)
			}
		})
	}
}
