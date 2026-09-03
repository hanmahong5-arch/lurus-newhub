package helper

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// An abandoned stream returns no error from its handler, so the relay's
// terminal-error metric never saw it: dashboards counted it as a success.
// ReportIncompleteStream lands it in relay_errors_total under the same
// provider/model/error_type taxonomy as every other terminal failure.
func TestReportIncompleteStream_CountsInRelayErrorsTotal(t *testing.T) {
	c, _ := newStreamCtx()

	t.Run("upstream closed → upstream_5xx", func(t *testing.T) {
		series := metrics.RelayErrorsTotal.WithLabelValues("OpenAI", "gpt-x", "upstream_5xx")
		before := testutil.ToFloat64(series)
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
			OriginModelName: "gpt-x",
			StreamEndReason: relaycommon.StreamEndUpstreamClosed,
		}
		err := ReportIncompleteStream(c, info)
		if err == nil || err.GetErrorCode() != types.ErrorCodeUpstreamStreamIncomplete || err.StatusCode != 502 {
			t.Fatalf("returned error = %+v", err)
		}
		if got := testutil.ToFloat64(series) - before; got != 1 {
			t.Errorf("relay_errors_total{OpenAI,gpt-x,upstream_5xx} delta = %v, want 1", got)
		}
	})

	t.Run("idle timeout → upstream_timeout", func(t *testing.T) {
		series := metrics.RelayErrorsTotal.WithLabelValues("Anthropic", "claude-x", "upstream_timeout")
		before := testutil.ToFloat64(series)
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
			OriginModelName: "claude-x",
			StreamEndReason: relaycommon.StreamEndTimeout,
		}
		if err := ReportIncompleteStream(c, info); err.StatusCode != 504 {
			t.Fatalf("status = %d, want 504", err.StatusCode)
		}
		if got := testutil.ToFloat64(series) - before; got != 1 {
			t.Errorf("relay_errors_total{Anthropic,claude-x,upstream_timeout} delta = %v, want 1", got)
		}
	})

	t.Run("nil info still counts under Unknown/unknown", func(t *testing.T) {
		series := metrics.RelayErrorsTotal.WithLabelValues("Unknown", "unknown", "upstream_5xx")
		before := testutil.ToFloat64(series)
		if err := ReportIncompleteStream(c, nil); err == nil {
			t.Fatal("nil info must still return the error")
		}
		if got := testutil.ToFloat64(series) - before; got != 1 {
			t.Errorf("delta = %v, want 1", got)
		}
	})
}
