package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRecordTimeToFirstToken_ObservesWithLabels drives the real
// RecordTimeToFirstToken helper (not a hand-built histogram) and checks the
// provider/model/product-labeled series it feeds actually gets exactly one
// more observation. HistogramVec.WithLabelValues returns Observer, not
// Collector, so the exact count comes from observerSampleCount (dto.Metric),
// same technique metrics_extra_test.go already uses.
func TestRecordTimeToFirstToken_ObservesWithLabels(t *testing.T) {
	series := RelayTimeToFirstToken.WithLabelValues("openai", "gpt-4o-ttft-test", "switch")
	before := observerSampleCount(t, series)

	RecordTimeToFirstToken("openai", "gpt-4o-ttft-test", "switch", 0.42)

	if got := observerSampleCount(t, series) - before; got != 1 {
		t.Errorf("relay_time_to_first_token_seconds_count{provider=openai,model=gpt-4o-ttft-test,product=switch} delta = %v, want 1", got)
	}
}

// TestRelayTimeToFirstToken_MetricName asserts the series is registered under
// the exact documented name — lurus_gateway_relay_time_to_first_token_seconds
// — the one netdata's go.d prometheus collector and any PromQL dashboard
// query addresses. CollectAndCount filtered by the fully-qualified name
// returns 0 if the metric were registered under any other name.
func TestRelayTimeToFirstToken_MetricName(t *testing.T) {
	RecordTimeToFirstToken("anthropic", "claude-3.5", "kova", 1.5) // ensure at least one child exists

	if n := testutil.CollectAndCount(RelayTimeToFirstToken, "lurus_gateway_relay_time_to_first_token_seconds"); n < 1 {
		t.Fatalf("no series named lurus_gateway_relay_time_to_first_token_seconds collected (got %d)", n)
	}
	if n := testutil.CollectAndCount(RelayTimeToFirstToken, "lurus_gateway_wrong_name"); n != 0 {
		t.Fatalf("name filter sanity check failed: %d series under a wrong name", n)
	}
}
