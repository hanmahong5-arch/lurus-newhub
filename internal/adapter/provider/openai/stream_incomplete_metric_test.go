package openai

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The OpenAI-compatible incomplete path must report through
// helper.ReportIncompleteStream, so the abandoned stream shows up in
// relay_errors_total (it used to count as a success: the handler returns no
// error). Labels: provider from the channel type, model from OriginModelName
// (empty in this harness → "unknown").
func TestOaiStreamHandler_IncompleteStream_CountsInRelayErrorsTotal(t *testing.T) {
	series := metrics.RelayErrorsTotal.WithLabelValues("OpenAI", "unknown", "upstream_5xx")
	before := testutil.ToFloat64(series)
	runIncompleteStream(t, types.RelayFormatOpenAI, truncatedStream(), false)
	if got := testutil.ToFloat64(series) - before; got != 1 {
		t.Errorf("relay_errors_total delta = %v, want exactly 1 per abandoned stream", got)
	}

	// A finished stream and a caller that hung up must not be counted.
	before = testutil.ToFloat64(series)
	runIncompleteStream(t, types.RelayFormatOpenAI, truncatedStream()+"data: "+finishChunk+"\n\n", false)
	runIncompleteStream(t, types.RelayFormatOpenAI, truncatedStream(), true)
	if got := testutil.ToFloat64(series) - before; got != 0 {
		t.Errorf("finished / client-gone streams counted as incomplete: delta = %v", got)
	}
}
