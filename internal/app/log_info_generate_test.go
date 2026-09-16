package app

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// TestGenerateTextOtherInfo_ConversionDropped drives GenerateTextOtherInfo
// directly (rather than through a converter) to pin the funnel contract: the
// funnel itself is a plain read of RelayInfo.ConversionDropped, with no
// per-call-site branching, so stashing it on RelayInfo is enough to reach
// compatible_handler.go:490 and the Claude/audio/wss generators
// (log_info_generate.go) that wrap this function — a claim
// TestConvertClaudeRequest_ConversionDroppedReachesLogProjection
// (internal/adapter/provider/openai) additionally proves end-to-end, driving
// the real converter and this function over one production-shaped RelayInfo
// rather than a hand-seeded field.
func TestGenerateTextOtherInfo_ConversionDropped(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ConversionDropped: []string{"tool_choice", "top_k"},
		ChannelMeta:       &relaycommon.ChannelMeta{},
	}
	other := GenerateTextOtherInfo(createTestGinContext(), info, 1, 1, 1, 0, 0, 0, 1)

	got, ok := other["conversion_dropped"]
	if !ok {
		t.Fatal(`other["conversion_dropped"] missing, want the RelayInfo value projected through`)
	}
	names, ok := got.([]string)
	if !ok || len(names) != 2 || names[0] != "tool_choice" || names[1] != "top_k" {
		t.Errorf("conversion_dropped = %#v, want [tool_choice top_k]", got)
	}
}

// TestGenerateTextOtherInfo_NoConversionDroppedKeyWhenEmpty proves the
// omission half of the contract: a nil RelayInfo.ConversionDropped (the
// common case — nothing was dropped, or this request never went through a
// cross-wire converter) must not write an always-present empty key.
func TestGenerateTextOtherInfo_NoConversionDroppedKeyWhenEmpty(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	other := GenerateTextOtherInfo(createTestGinContext(), info, 1, 1, 1, 0, 0, 0, 1)

	if _, ok := other["conversion_dropped"]; ok {
		t.Errorf(`other["conversion_dropped"] present with nil ConversionDropped, want key absent`)
	}
}

// TestConversionDropped_ResetAcrossRetryAttempts pins R3: relayInfo is built
// once per request and reused by every retry attempt
// (adapter/handler/relay.go), so a value a converter set on a failed attempt
// must not survive onto a later attempt that never ran a converter (a native
// Claude/Gemini channel, or a pass-through channel). InitChannelMeta
// (provider/common/relay_info.go) is the seam every relay handler calls at
// the start of each attempt, after the channel for that attempt has been
// (re-)selected — this drives two attempts over one RelayInfo the way
// production does, and asserts the settled log row reflects only the last.
func TestConversionDropped_ResetAcrossRetryAttempts(t *testing.T) {
	c := createTestGinContext()
	info := nonOpenRouterInfo()

	// Attempt 1: an OpenAI-format channel, converter runs and sets the list.
	claudeReq := dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: 100,
		TopK:      40,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	if _, err := ClaudeToOpenAIRequest(claudeReq, info); err != nil {
		t.Fatalf("attempt 1 unexpected error: %v", err)
	}
	if len(info.ConversionDropped) == 0 {
		t.Fatal("attempt 1: ConversionDropped is empty, test fixture bug")
	}

	// Attempt 2: retry re-selects a channel and every relay handler calls
	// InitChannelMeta at the start of the attempt, before running (or, for a
	// native/pass-through channel, NOT running) a converter.
	info.InitChannelMeta(c)

	if info.ConversionDropped != nil {
		t.Fatalf("after InitChannelMeta (attempt 2 start): ConversionDropped = %v, want nil — a stale value from attempt 1 must not survive", info.ConversionDropped)
	}

	// Attempt 2 is native-format (no converter call, e.g. the Claude/Gemini
	// native adaptors or a PassThroughBodyEnabled channel) and it is the one
	// that settles — top_k reached the vendor natively this time.
	other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0, 1)
	if _, ok := other["conversion_dropped"]; ok {
		t.Errorf(`other["conversion_dropped"] present after the settling attempt ran no converter, want key absent — got %#v`, other["conversion_dropped"])
	}
}
