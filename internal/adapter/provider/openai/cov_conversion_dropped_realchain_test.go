package openai

// L5 REAL-CHAIN test (cycle 9 R2). Every other test that proves the
// conversion-fidelity diagnostics stash (internal/app/convert_test.go) calls
// ClaudeToOpenAIRequest/GeminiToOpenAIRequest directly with a hand-built
// *RelayInfo, and the one test that proves the log-row projection
// (internal/app/log_info_generate_test.go) hand-seeds RelayInfo.ConversionDropped.
// Neither shows a value written by the converter actually arriving in the map
// app.GenerateTextOtherInfo produces over the SAME *RelayInfo — the seam
// production actually uses (Adaptor.ConvertClaudeRequest ->
// app.ClaudeToOpenAIRequest, both against the one RelayInfo built once per
// request). This test drives that real seam: if a future change passed a
// copy of info into the converter, or a handler used a second RelayInfo, this
// test — unlike the hand-seeded ones — would go red.

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestConvertClaudeRequest_ConversionDroppedReachesLogProjection(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
	c := newCovTestContext()

	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-5-sonnet",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-4o",
		},
	}
	req := &dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: 128,
		TopK:      40,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hi from claude"},
		},
	}

	if _, err := a.ConvertClaudeRequest(c, info, req); err != nil {
		t.Fatalf("ConvertClaudeRequest unexpected error: %v", err)
	}

	// Same *RelayInfo the adaptor just wrote to — not a second, hand-seeded
	// one — passed into the real success-path funnel
	// (relay/compatible_handler.go:490 calls this in production).
	other := app.GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0, 1)

	got, ok := other["conversion_dropped"]
	if !ok {
		t.Fatal(`other["conversion_dropped"] missing after driving the real ConvertClaudeRequest -> GenerateTextOtherInfo seam over one RelayInfo`)
	}
	names, ok := got.([]string)
	if !ok || len(names) != 1 || names[0] != "top_k" {
		t.Errorf("conversion_dropped = %#v, want [top_k]", got)
	}
}
