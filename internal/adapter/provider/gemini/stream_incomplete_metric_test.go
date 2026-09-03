package gemini

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Both native Gemini incomplete paths (converted and passthrough) must report
// through helper.ReportIncompleteStream so the abandoned stream lands in
// relay_errors_total instead of counting as a success.
func TestGeminiStreamHandlers_Incomplete_CountInRelayErrorsTotal(t *testing.T) {
	series := metrics.RelayErrorsTotal.WithLabelValues("Gemini", "gemini-x", "upstream_5xx")

	t.Run("converted (openai wire)", func(t *testing.T) {
		before := testutil.ToFloat64(series)
		c, _ := newHandlerContext()
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini, UpstreamModelName: "gemini-x"},
			OriginModelName: "gemini-x",
			RelayFormat:     types.RelayFormatOpenAI,
		}
		resp := respFromBody(200, sseBody(geminiChunkA, geminiChunkB))
		defer func() { _ = resp.Body.Close() }()
		if _, apiErr := GeminiChatStreamHandler(c, info, resp); apiErr != nil {
			t.Fatal(apiErr)
		}
		if got := testutil.ToFloat64(series) - before; got != 1 {
			t.Errorf("delta = %v, want 1", got)
		}
	})

	t.Run("passthrough (gemini wire)", func(t *testing.T) {
		before := testutil.ToFloat64(series)
		c, _ := newHandlerContext()
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini},
			OriginModelName: "gemini-x",
			RelayFormat:     types.RelayFormatGemini,
		}
		resp := respFromBody(200, sseBody(geminiChunkA, geminiChunkB))
		defer func() { _ = resp.Body.Close() }()
		if _, apiErr := GeminiTextGenerationStreamHandler(c, info, resp); apiErr != nil {
			t.Fatal(apiErr)
		}
		if got := testutil.ToFloat64(series) - before; got != 1 {
			t.Errorf("delta = %v, want 1", got)
		}
	})

	t.Run("finished stream not counted", func(t *testing.T) {
		before := testutil.ToFloat64(series)
		c, _ := newHandlerContext()
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini},
			OriginModelName: "gemini-x",
			RelayFormat:     types.RelayFormatGemini,
		}
		resp := respFromBody(200, sseBody(geminiChunkA, geminiFinishChunk))
		defer func() { _ = resp.Body.Close() }()
		if _, apiErr := GeminiTextGenerationStreamHandler(c, info, resp); apiErr != nil {
			t.Fatal(apiErr)
		}
		if got := testutil.ToFloat64(series) - before; got != 0 {
			t.Errorf("delta = %v, want 0", got)
		}
	})
}
