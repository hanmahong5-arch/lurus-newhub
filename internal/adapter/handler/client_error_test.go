package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func postClientError(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/client-error", ReportClientError)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/client-error", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func clientErrors(kind string) float64 {
	return testutil.ToFloat64(metrics.ClientErrorsTotal.WithLabelValues(kind))
}

// A browser crash becomes one count under its kind; the reporter always gets
// 204 (it has nowhere to report a failure of its own).
func TestReportClientError_CountsByKind(t *testing.T) {
	before := clientErrors("render")
	w := postClientError(t, `{"kind":"render","page":"/console/v2/tokens","message":"x is undefined","version":"v1"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if got := clientErrors("render") - before; got != 1 {
		t.Fatalf("render count delta = %v, want 1", got)
	}
}

// The endpoint is unauthenticated: a made-up kind must not mint a new series.
func TestReportClientError_UnknownKindFoldsToOther(t *testing.T) {
	before := clientErrors("other")
	postClientError(t, `{"kind":"`+strings.Repeat("z", 64)+`"}`)
	if got := clientErrors("other") - before; got != 1 {
		t.Fatalf("other count delta = %v, want 1", got)
	}
	if n := testutil.CollectAndCount(metrics.ClientErrorsTotal); n != len(metrics.ClientErrorKinds) {
		t.Fatalf("client_errors_total has %d series, want exactly the %d fixed kinds", n, len(metrics.ClientErrorKinds))
	}
}

// Garbage and oversize bodies are dropped without counting anything.
func TestReportClientError_BadBodiesAreDropped(t *testing.T) {
	before := clientErrors("other") + clientErrors("render")
	for _, body := range []string{"not json", `{"kind":"render","message":"` + strings.Repeat("a", 8<<10) + `"}`} {
		if w := postClientError(t, body); w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", w.Code)
		}
	}
	if got := clientErrors("other") + clientErrors("render") - before; got != 0 {
		t.Fatalf("counted %v reports from unreadable bodies, want 0", got)
	}
}

func TestLogSafe_BoundsAndStripsControlCharacters(t *testing.T) {
	got := logSafe("line1\nFAKE LOG LINE\r\x00"+strings.Repeat("b", 400), 20)
	if strings.ContainsAny(got, "\n\r\x00") {
		t.Fatalf("control characters survived: %q", got)
	}
	if len([]rune(got)) > 20+3 { // quotes + ellipsis
		t.Fatalf("not bounded: %d runes", len([]rune(got)))
	}
}
