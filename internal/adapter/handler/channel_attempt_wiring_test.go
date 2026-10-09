package handler

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// reportBreakerOutcome is the single wiring point of the per-attempt series;
// replacing its recordChannelAttempt call must turn this red.
func TestReportBreakerOutcome_CountsUpstreamAttempts(t *testing.T) {
	swapChannelBreakers(t, resilience.Config{Threshold: 100, Timeout: time.Hour, HalfOpenTimeout: time.Hour})
	const id = 9151
	lbl := strconv.Itoa(id)
	req := func(class string) float64 {
		return testutil.ToFloat64(metrics.ChannelRequestsTotal.WithLabelValues(lbl, class))
	}

	reportBreakerOutcome(id, nil, "")
	if req("2xx") != 1 {
		t.Fatalf("success must count one 2xx attempt, got %v", req("2xx"))
	}

	reportBreakerOutcome(id, types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), "")
	if req("5xx") != 1 || testutil.ToFloat64(metrics.ChannelErrorsTotal.WithLabelValues(lbl, "upstream_5xx")) != 1 {
		t.Fatalf("502 must count one 5xx attempt and one upstream_5xx error")
	}

	// Caller hang-up (nil error + client-gone): not an attempt.
	reportBreakerOutcome(id, nil, relaycommon.StreamEndClientGone)
	if req("2xx") != 1 {
		t.Errorf("client-gone moved the 2xx counter to %v", req("2xx"))
	}
}

// InstallContentMetricsSink must make the guard's events reach the series.
func TestInstallContentMetricsSink_RoutesGuardEvents(t *testing.T) {
	t.Cleanup(func() { contentpolicy.SetMetricsSink(nil) })
	hits := func() float64 {
		return testutil.ToFloat64(metrics.ContentRuleHitsTotal.WithLabelValues("builtin", "mask", "observe"))
	}
	h0, r0 := hits(), testutil.ToFloat64(metrics.ContentRejectedTotal)

	contentpolicy.SetMetricsSink(nil)
	contentpolicy.ObserveRuleHit(1, "mask", "observe", "phone_cn", 3)
	if hits() != h0 {
		t.Fatal("without an installed sink the series must not move")
	}

	InstallContentMetricsSink()
	contentpolicy.ObserveRuleHit(1, "mask", "observe", "phone_cn", 3)
	contentpolicy.ObserveRuleReject(1)
	if hits() != h0+3 || testutil.ToFloat64(metrics.ContentRejectedTotal) != r0+1 {
		t.Errorf("installed sink: hits %v->%v rejected %v->%v", h0, hits(), r0, testutil.ToFloat64(metrics.ContentRejectedTotal))
	}
}
