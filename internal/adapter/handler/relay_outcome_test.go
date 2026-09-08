package handler

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// ttftSampleCount returns the cumulative sample_count of a single
// HistogramVec.WithLabelValues(...) time series. Observer is not a Collector
// (testutil.ToFloat64 does not accept it for histograms), but its concrete
// type also implements prometheus.Metric, so Write reaches the raw dto and
// its exact observation count — mirrors internal/pkg/metrics's own
// observerSampleCount test helper.
func ttftSampleCount(t *testing.T, o prometheus.Observer) int {
	t.Helper()
	m, ok := o.(prometheus.Metric)
	if !ok {
		t.Fatalf("observer does not implement prometheus.Metric (%T)", o)
	}
	var pb dto.Metric
	if err := m.Write(&pb); err != nil {
		t.Fatalf("observer Write failed: %v", err)
	}
	hist := pb.GetHistogram()
	if hist == nil {
		t.Fatalf("observer metric is not a histogram")
	}
	return int(hist.GetSampleCount())
}

// ttftSampleSum returns the cumulative sample_sum of a single
// HistogramVec.WithLabelValues(...) time series. Used to catch a sign flip
// in the TTFT duration computation (e.g. info.StartTime.Sub(info.FirstResponseTime)
// instead of the correct info.FirstResponseTime.Sub(info.StartTime)) that a
// sample-count-only assertion cannot see: the observation still lands (count
// +1) but the exported duration goes negative.
func ttftSampleSum(t *testing.T, o prometheus.Observer) float64 {
	t.Helper()
	m, ok := o.(prometheus.Metric)
	if !ok {
		t.Fatalf("observer does not implement prometheus.Metric (%T)", o)
	}
	var pb dto.Metric
	if err := m.Write(&pb); err != nil {
		t.Fatalf("observer Write failed: %v", err)
	}
	hist := pb.GetHistogram()
	if hist == nil {
		t.Fatalf("observer metric is not a histogram")
	}
	return hist.GetSampleSum()
}

func TestRelayOutcome_Table(t *testing.T) {
	someErr := &types.NewAPIError{StatusCode: 500}

	cases := []struct {
		name      string
		apiErr    *types.NewAPIError
		endReason string
		want      string
	}{
		{"nil error, no end reason -> success", nil, "", "success"},
		{"nil error, upstream closed -> success (billed/complete path)", nil, relaycommon.StreamEndUpstreamClosed, "success"},
		{"nil error, timeout end reason -> success", nil, relaycommon.StreamEndTimeout, "success"},
		{"nil error, client gone -> client_gone", nil, relaycommon.StreamEndClientGone, "client_gone"},
		{"error present, no end reason -> error", someErr, "", "error"},
		{"error present even with client gone -> error wins", someErr, relaycommon.StreamEndClientGone, "error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relayOutcome(tc.apiErr, tc.endReason); got != tc.want {
				t.Errorf("relayOutcome(%v, %q) = %q, want %q", tc.apiErr, tc.endReason, got, tc.want)
			}
		})
	}
}

// TestObserveRelayOutcome_RequestsTotal drives observeRelayOutcome the same
// way relay.go's post-channel-selection site does (total=false ->
// RecordRelayRequest -> relay_requests_total) with a real *RelayInfo, so the
// consolidated call site is proven to still resolve status from
// StreamEndReason and product from SourceProduct instead of a caller-passed
// literal.
func TestObserveRelayOutcome_RequestsTotal(t *testing.T) {
	provider, model := "openai", "gpt-4o"

	t.Run("client gone: client_gone +1, success +0", func(t *testing.T) {
		info := &relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndClientGone, SourceProduct: "kova"}

		goneSeries := metrics.RelayRequestsTotal.WithLabelValues(provider, model, "client_gone", "kova")
		successSeries := metrics.RelayRequestsTotal.WithLabelValues(provider, model, "success", "kova")
		goneBefore := testutil.ToFloat64(goneSeries)
		successBefore := testutil.ToFloat64(successSeries)

		observeRelayOutcome(provider, model, info, nil, 0.1, false)

		if got := testutil.ToFloat64(goneSeries) - goneBefore; got != 1 {
			t.Errorf("relay_requests_total{status=client_gone,product=kova} delta = %v, want 1", got)
		}
		if got := testutil.ToFloat64(successSeries) - successBefore; got != 0 {
			t.Errorf("relay_requests_total{status=success,product=kova} delta = %v, want 0", got)
		}
	})

	t.Run("apiErr set wins over client gone: error +1", func(t *testing.T) {
		info := &relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndClientGone, SourceProduct: "kova"}
		apiErr := &types.NewAPIError{StatusCode: 500}

		errSeries := metrics.RelayRequestsTotal.WithLabelValues(provider, model, "error", "kova")
		errBefore := testutil.ToFloat64(errSeries)

		observeRelayOutcome(provider, model, info, apiErr, 0.1, false)

		if got := testutil.ToFloat64(errSeries) - errBefore; got != 1 {
			t.Errorf("relay_requests_total{status=error,product=kova} delta = %v, want 1", got)
		}
	})
}

// TestObserveRelayOutcome_TimeToFirstToken drives the total=true (end-to-end
// defer) call site — the only place the TTFT histogram should be observed —
// and covers the branches that must NOT observe: total=false, the
// never-streamed sentinel, a nil RelayInfo, and an OpenAI Realtime session.
func TestObserveRelayOutcome_TimeToFirstToken(t *testing.T) {
	provider, model := "openai", "gpt-4o"

	t.Run("total=true, streamed: observes with product label, sum >= elapsed", func(t *testing.T) {
		now := time.Now()
		info := &relaycommon.RelayInfo{
			StartTime:         now.Add(-200 * time.Millisecond),
			FirstResponseTime: now,
			SourceProduct:     "switch",
		}

		series := metrics.RelayTimeToFirstToken.WithLabelValues(provider, model, "switch")
		countBefore := ttftSampleCount(t, series)
		sumBefore := ttftSampleSum(t, series)

		observeRelayOutcome(provider, model, info, nil, 0.3, true)

		if got := ttftSampleCount(t, series) - countBefore; got != 1 {
			t.Errorf("relay_time_to_first_token_seconds_count{product=switch} delta = %v, want 1", got)
		}
		// info.FirstResponseTime - info.StartTime == 200ms; a sign flip
		// (StartTime - FirstResponseTime) would leave the count assertion
		// above green while exporting a negative sum. 0.2 is the exact gap
		// seeded above; assert >= it rather than == it to tolerate float
		// rounding in the Sub/Seconds conversion.
		if got := ttftSampleSum(t, series) - sumBefore; got < 0.2 {
			t.Errorf("relay_time_to_first_token_seconds_sum{product=switch} delta = %v, want >= 0.2 (sign flip would export a negative sum)", got)
		}
		if n := testutil.CollectAndCount(metrics.RelayTimeToFirstToken, "lurus_gateway_relay_time_to_first_token_seconds"); n < 1 {
			t.Fatalf("no series named lurus_gateway_relay_time_to_first_token_seconds collected (got %d)", n)
		}
	})

	t.Run("total=false: never observes TTFT even when streamed", func(t *testing.T) {
		now := time.Now()
		info := &relaycommon.RelayInfo{
			StartTime:         now.Add(-200 * time.Millisecond),
			FirstResponseTime: now,
			SourceProduct:     "kova-ttft-false",
		}

		series := metrics.RelayTimeToFirstToken.WithLabelValues(provider, model, "kova-ttft-false")
		countBefore := ttftSampleCount(t, series)

		observeRelayOutcome(provider, model, info, nil, 0.3, false)

		if got := ttftSampleCount(t, series) - countBefore; got != 0 {
			t.Errorf("relay_time_to_first_token_seconds_count{product=kova-ttft-false} delta = %v, want 0 (total=false must not observe TTFT)", got)
		}
	})

	t.Run("total=true, sentinel (never streamed): does not observe", func(t *testing.T) {
		now := time.Now()
		info := &relaycommon.RelayInfo{
			StartTime:         now,
			FirstResponseTime: now.Add(-time.Second), // sentinel: FirstResponseTime before StartTime
			SourceProduct:     "kova-ttft-sentinel",
		}

		series := metrics.RelayTimeToFirstToken.WithLabelValues(provider, model, "kova-ttft-sentinel")
		countBefore := ttftSampleCount(t, series)

		observeRelayOutcome(provider, model, info, nil, 0.3, true)

		if got := ttftSampleCount(t, series) - countBefore; got != 0 {
			t.Errorf("relay_time_to_first_token_seconds_count{product=kova-ttft-sentinel} delta = %v, want 0 (never-streamed sentinel must not observe)", got)
		}
	})

	t.Run("total=true, streamed then client gone: still observes (a first token DID arrive)", func(t *testing.T) {
		now := time.Now()
		info := &relaycommon.RelayInfo{
			StartTime:         now.Add(-200 * time.Millisecond),
			FirstResponseTime: now,
			SourceProduct:     "kova-ttft-gone",
			StreamEndReason:   relaycommon.StreamEndClientGone,
		}

		series := metrics.RelayTimeToFirstToken.WithLabelValues(provider, model, "kova-ttft-gone")
		countBefore := ttftSampleCount(t, series)

		observeRelayOutcome(provider, model, info, nil, 0.3, true)

		if got := ttftSampleCount(t, series) - countBefore; got != 1 {
			t.Errorf("relay_time_to_first_token_seconds_count{product=kova-ttft-gone} delta = %v, want 1 (a stream that got a first token then the client disconnected still contributes a TTFT sample)", got)
		}
	})

	t.Run("total=true, OpenAI Realtime session: excluded even though a first token arrived", func(t *testing.T) {
		now := time.Now()
		info := &relaycommon.RelayInfo{
			StartTime:         now.Add(-200 * time.Millisecond),
			FirstResponseTime: now,
			SourceProduct:     "kova-ttft-realtime",
			RelayFormat:       types.RelayFormatOpenAIRealtime,
		}

		series := metrics.RelayTimeToFirstToken.WithLabelValues(provider, model, "kova-ttft-realtime")
		countBefore := ttftSampleCount(t, series)

		observeRelayOutcome(provider, model, info, nil, 0.3, true)

		if got := ttftSampleCount(t, series) - countBefore; got != 0 {
			t.Errorf("relay_time_to_first_token_seconds_count{product=kova-ttft-realtime} delta = %v, want 0 (OpenAI Realtime sessions are excluded from the request-latency TTFT histogram)", got)
		}
	})

	t.Run("total=true, nil info: does not panic and does not observe", func(t *testing.T) {
		series := metrics.RelayTimeToFirstToken.WithLabelValues(provider, model, "unknown")
		countBefore := ttftSampleCount(t, series)

		observeRelayOutcome(provider, model, nil, nil, 0.3, true)

		if got := ttftSampleCount(t, series) - countBefore; got != 0 {
			t.Errorf("relay_time_to_first_token_seconds_count{product=unknown} delta = %v, want 0 (nil info must not observe)", got)
		}
	})
}
