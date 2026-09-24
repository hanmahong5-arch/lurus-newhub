package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// identity_client_usage_report_test.go — cycle-13 L9 defect (3).
//
// ReportLLMUsage checked only the transport error and then closed the body:
// the platform's answer was never read. A wrong-scope 403, a renamed route's
// 404 and a 500 all produced no log, no error and no metric — the leg looked
// identical to a success from every vantage point, including the gRPC
// transport, which falls back to this same function.

// reportUsageAgainst runs one ReportLLMUsage against a server that answers
// with the given status, and returns whatever SysLog wrote plus the delta on
// each usageReportTotal label. The counter is a process-global that other
// tests also move, so the deltas are read around this one call rather than
// compared against an absolute value.
func reportUsageAgainst(t *testing.T, status int) (logged string, errDelta, okDelta float64) {
	t.Helper()
	covInitTextMode(t)
	out, _ := covSwapGinWriters(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prev })

	errBefore := testutil.ToFloat64(usageReportTotal.WithLabelValues("error"))
	okBefore := testutil.ToFloat64(usageReportTotal.WithLabelValues("success"))
	ReportLLMUsage(context.Background(), 42, 1.25)
	return out.String(),
		testutil.ToFloat64(usageReportTotal.WithLabelValues("error")) - errBefore,
		testutil.ToFloat64(usageReportTotal.WithLabelValues("success")) - okBefore
}

// TestReportLLMUsage_NonOKStatusIsLogged is the oracle for defect (3): the
// platform's refusal has to leave a trace — a log line naming the status, and
// a tick on the error counter. Three statuses, because they are three
// different operational stories (scope, route, platform fault) and all three
// were silent.
func TestReportLLMUsage_NonOKStatusIsLogged(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			logged, errDelta, okDelta := reportUsageAgainst(t, status)
			if !strings.Contains(logged, "ReportLLMUsage") {
				t.Fatalf("a %d from the platform produced no ReportLLMUsage log line at all; log=%q",
					status, logged)
			}
			if !strings.Contains(logged, http.StatusText(status)) &&
				!strings.Contains(logged, statusDigits(status)) {
				t.Errorf("the log line does not name the status %d; log=%q", status, logged)
			}
			if errDelta != 1 {
				t.Errorf("usage_report_total{status=error} moved by %v, want 1", errDelta)
			}
			if okDelta != 0 {
				t.Errorf("a %d also counted as a success (delta %v)", status, okDelta)
			}
		})
	}
}

// statusDigits renders an HTTP status as its decimal digits for substring
// assertions ("403"), independent of how the log line formats it.
func statusDigits(status int) string {
	return string(rune('0'+status/100)) + string(rune('0'+(status/10)%10)) + string(rune('0'+status%10))
}

// TestReportLLMUsage_SuccessStaysQuiet is the do-not-regress half: a 200 must
// not start writing a log line on every relay settle, and must count as a
// success rather than an error.
func TestReportLLMUsage_SuccessStaysQuiet(t *testing.T) {
	logged, errDelta, okDelta := reportUsageAgainst(t, http.StatusOK)
	if strings.Contains(logged, "ReportLLMUsage") {
		t.Errorf("a successful usage report must stay quiet, got %q", logged)
	}
	if okDelta != 1 || errDelta != 0 {
		t.Errorf("200 counted as success=%v error=%v, want 1/0", okDelta, errDelta)
	}
}

// TestUsageReportTotal_IsActuallyScrapeable proves the counter reaches
// /metrics rather than merely existing as a Go variable: the route handler is
// promhttp.Handler() (router/main.go), which gathers from
// prometheus.DefaultGatherer, and promauto registers into the matching
// DefaultRegisterer. This series is declared outside internal/pkg/metrics (see
// the hand-off note), so the M3 gate in that package cannot see it — this
// assertion is what stands in for that gate here.
func TestUsageReportTotal_IsActuallyScrapeable(t *testing.T) {
	usageReportTotal.WithLabelValues("success").Add(0) // materialise the child
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "lurus_billing_usage_report_total" {
			return
		}
	}
	t.Fatal("lurus_billing_usage_report_total is not in the default gatherer — /metrics will never show it")
}

// TestReportLLMUsage_TransportFailureCounts closes the other half of the
// counter: an unreachable platform (no HTTP response at all) also ticks the
// error label, so the metric measures "reports that did not land" rather than
// "reports the platform refused".
func TestReportLLMUsage_TransportFailureCounts(t *testing.T) {
	covInitTextMode(t)
	out, _ := covSwapGinWriters(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening any more
	prev := IdentityServiceURL
	IdentityServiceURL = url
	t.Cleanup(func() { IdentityServiceURL = prev })

	before := testutil.ToFloat64(usageReportTotal.WithLabelValues("error"))
	ReportLLMUsage(context.Background(), 42, 1.25)
	if got := testutil.ToFloat64(usageReportTotal.WithLabelValues("error")) - before; got != 1 {
		t.Errorf("transport failure moved the error counter by %v, want 1; log=%q", got, out.String())
	}
}
