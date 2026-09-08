package handler

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

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
