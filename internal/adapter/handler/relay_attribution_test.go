package handler

// relay_attribution_test.go — proves the cross-product attribution tag
// (Workstream 0) reaches an error-log row, not just a successful settlement.
//
// recordRelayErrorLog resolves the tag itself, straight off the still-live
// request header, rather than reading RelayInfo.SourceProduct — because some
// error paths (request binding) fire BEFORE GenRelayInfo ever runs, so there
// is no RelayInfo to read from yet. This file drives the sensitive-word
// rejection, a failure that happens AFTER GenRelayInfo; the BEFORE timing (a
// request-binding failure) is asserted by relay_error_log_fallback_test.go's
// TestRelay_PreChannelBindingError_RecordsErrorLog, so both are locked.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// observerSampleCount reads the cumulative sample_count of a single
// prometheus.Observer time series (a HistogramVec.WithLabelValues(...)
// result). testutil.ToFloat64 only works for Counter/Gauge, so histogram
// series (like relay_total_duration_seconds) need this instead.
func observerSampleCount(t *testing.T, o prometheus.Observer) int {
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

// setupSensitiveRejection mirrors relay_sensitive_rejection_test.go's fixture:
// a prompt containing a blocklisted word is rejected with 400 AFTER
// GenRelayInfo has already run, so relayInfo.SourceProduct is populated by
// the time recordTerminalRelayError fires.
func setupSensitiveRejection(t *testing.T) {
	t.Helper()
	prevMB := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	prevWords := setting.SensitiveWords
	prevEnabled := setting.CheckSensitiveEnabled
	prevPrompt := setting.CheckSensitiveOnPromptEnabled
	setting.SensitiveWords = []string{"test_sensitive"}
	setting.CheckSensitiveEnabled = true
	setting.CheckSensitiveOnPromptEnabled = true
	t.Cleanup(func() {
		constant.MaxRequestBodyMB = prevMB
		setting.SensitiveWords = prevWords
		setting.CheckSensitiveEnabled = prevEnabled
		setting.CheckSensitiveOnPromptEnabled = prevPrompt
	})
}

func TestRelay_SourceProductHeader_ReachesErrorLogRow(t *testing.T) {
	cases := []struct {
		name        string
		header      string
		wantProduct string
	}{
		{name: "allow-listed header is trusted verbatim", header: "kova", wantProduct: "kova"},
		{name: "unknown header falls back to the default, never echoed raw", header: "bogus", wantProduct: ratio_setting.DefaultSourceProduct},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, cleanup := handlerRelaySetupDB(t)
			defer cleanup()
			errorLogFallbackEnable(t)
			setupSensitiveRejection(t)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body := `{"model":"gpt-4o","messages":[{"role":"user","content":"test_sensitive please"}]}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set(ratio_setting.SourceProductHeader, tc.header)

			// A sensitive-word rejection fires before any channel is tried
			// (constant.GetChannelTypeName(0) == "Unknown", capitalized — the
			// package's own no-such-channel default) and before
			// "original_model" is set in gin context by the real route chain
			// (this test drives Relay() directly, skipping it, so it falls to
			// relay.go's "unknown" literal) — only the product label
			// distinguishes the two cases. ErrorCodeSensitiveWordsDetected
			// classifies as error_type "internal" (types.RelayErrorType).
			errSeries := metrics.RelayErrorsTotal.WithLabelValues("Unknown", "unknown", "internal", tc.wantProduct)
			errBefore := testutil.ToFloat64(errSeries)

			// relay_total_duration_seconds{status="error",product=...} is the
			// series the end-to-end defer in Relay() records via
			// observeRelayOutcome(..., total=true) — the only metrics site that
			// fires for a pre-channel-selection rejection like this one (the
			// post-channel-selection site, relay_requests_total, never runs
			// because no channel was ever selected). Asserting on it here locks
			// that relay.go still passes the live relayInfo (not nil, not a
			// hand-rolled product string) into the consolidated call site.
			totalSeries := metrics.RelayTotalDuration.WithLabelValues("Unknown", "unknown", "error", tc.wantProduct)
			totalBefore := observerSampleCount(t, totalSeries)

			Relay(c, types.RelayFormatOpenAI)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for a rejected sensitive prompt; body=%s", w.Code, w.Body.String())
			}

			if got := testutil.ToFloat64(errSeries) - errBefore; got != 1 {
				t.Errorf("relay_errors_total{provider=Unknown,model=unknown,error_type=internal,product=%s} delta = %v, want 1",
					tc.wantProduct, got)
			}

			if got := observerSampleCount(t, totalSeries) - totalBefore; got != 1 {
				t.Errorf("relay_total_duration_seconds{provider=Unknown,model=unknown,status=error,product=%s} sample delta = %v, want 1",
					tc.wantProduct, got)
			}

			var logs []repo.Log
			if err := db.Where("type = ?", repo.LogTypeError).Find(&logs).Error; err != nil {
				t.Fatalf("query error logs: %v", err)
			}
			if len(logs) != 1 {
				t.Fatalf("error log rows = %d, want exactly 1", len(logs))
			}

			want := `"source_product":"` + tc.wantProduct + `"`
			if !strings.Contains(logs[0].Other, want) {
				t.Errorf("Other = %q, want to contain %q", logs[0].Other, want)
			}
		})
	}
}
