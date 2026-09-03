package claude

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The native Anthropic incomplete path must report through
// helper.ReportIncompleteStream so the abandoned stream lands in
// relay_errors_total instead of counting as a success.
func TestHandleStreamFinalResponse_Incomplete_CountsInRelayErrorsTotal(t *testing.T) {
	series := metrics.RelayErrorsTotal.WithLabelValues("Anthropic", "claude-x", "upstream_5xx")
	before := testutil.ToFloat64(series)

	c, _, info, claudeInfo := newIncompleteClaudeCtx(t, types.RelayFormatClaude, false)
	info.ChannelType = constant.ChannelTypeAnthropic
	info.OriginModelName = "claude-x"
	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	if got := testutil.ToFloat64(series) - before; got != 1 {
		t.Errorf("relay_errors_total delta = %v, want 1", got)
	}

	before = testutil.ToFloat64(series)
	c, _, info, claudeInfo = newIncompleteClaudeCtx(t, types.RelayFormatClaude, false)
	info.ChannelType = constant.ChannelTypeAnthropic
	info.OriginModelName = "claude-x"
	claudeInfo.Done = true
	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	if got := testutil.ToFloat64(series) - before; got != 0 {
		t.Errorf("a complete stream must not be counted: delta = %v", got)
	}
}
