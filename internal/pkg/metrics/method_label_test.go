package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// methodLabelValuesForPath collects the distinct "method" label values
// RequestsTotal currently carries for one "path" label value, by walking the
// CounterVec's own Collect() output rather than guessing at label sets in
// advance — this is what lets the assertion below count cardinality (how
// many DISTINCT series exist) instead of just reading one series' value.
func methodLabelValuesForPath(t *testing.T, path string) map[string]bool {
	t.Helper()
	ch := make(chan prometheus.Metric, 4096)
	RequestsTotal.Collect(ch)
	close(ch)

	seen := map[string]bool{}
	for m := range ch {
		var pm dto.Metric
		if err := m.Write(&pm); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		var method, p string
		for _, lp := range pm.GetLabel() {
			switch lp.GetName() {
			case "method":
				method = lp.GetValue()
			case "path":
				p = lp.GetValue()
			}
		}
		if p == path {
			seen[method] = true
		}
	}
	return seen
}

// TestMiddleware_MethodWhitelist_CollapsesUnknownMethodsToOther pins the
// method-label cardinality guard added to Middleware(): an unauthenticated,
// unrate-limited caller can send any HTTP verb (nothing ahead of this
// middleware validates it — it runs on every request gin accepts, matched
// route or not), so before normalizeMethodLabel existed, requests_total and
// request_duration_seconds let that caller grow the "method" label's
// cardinality without bound just by varying the verb (cycle-13 §1.2 finding
// 15). Driven through the real gin middleware + httptest, not a hand-built
// WithLabelValues call: GET (whitelisted) plus two distinct made-up verbs
// must collapse to exactly one MORE distinct label set — method="other" —
// not two.
func TestMiddleware_MethodWhitelist_CollapsesUnknownMethodsToOther(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	const path = "/method-label-test-probe"
	// gin.Any() only registers its own fixed list of standard verbs, so a
	// garbage method would never match and would instead hit NoRoute (path
	// label "unknown", shared with other tests) — that would test the
	// unknown-path fallback, not this guard. r.Handle accepts an arbitrary
	// method string, which is what actually exercises "gin matched a route
	// for a non-whitelisted verb" and keeps every request on the same,
	// test-unique path label.
	for _, m := range []string{http.MethodGet, "FOOBAR", "PROPFIND"} {
		r.Handle(m, path, func(c *gin.Context) { c.Status(http.StatusOK) })
	}

	if before := methodLabelValuesForPath(t, path); len(before) != 0 {
		t.Fatalf("path %q already has method label sets %v before the probe — "+
			"pick a path unique to this test", path, before)
	}

	for _, m := range []string{http.MethodGet, "FOOBAR", "PROPFIND"} {
		req := httptest.NewRequest(m, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("method %s: got status %d, want 200", m, w.Code)
		}
	}

	got := methodLabelValuesForPath(t, path)
	// GET is whitelisted (its own label set) + both garbage verbs collapse
	// into the single "other" label set = 2 total, never 3.
	if len(got) != 2 {
		t.Errorf("method label sets for %q = %v (%d), want exactly 2 ({\"GET\",\"other\"}) — "+
			"an unlisted method must not create its own unbounded-cardinality label",
			path, got, len(got))
	}
	if !got["GET"] {
		t.Errorf("method label sets for %q = %v, missing whitelisted \"GET\"", path, got)
	}
	if !got["other"] {
		t.Errorf("method label sets for %q = %v, missing \"other\" — garbage verbs must "+
			"collapse into it", path, got)
	}
	if got["FOOBAR"] || got["PROPFIND"] {
		t.Errorf("method label sets for %q = %v, a raw garbage verb leaked through as its "+
			"own label instead of collapsing to \"other\"", path, got)
	}

	if v := testutil.ToFloat64(RequestsTotal.WithLabelValues("other", path, "200")); v != 2 {
		t.Errorf(`RequestsTotal{method="other",path=%q,status="200"} = %v, want 2 `+
			`(both garbage verbs counted into the same series)`, path, v)
	}
}

// TestNormalizeMethodLabel_Table pins every whitelisted verb passing through
// unchanged and a representative set of non-whitelisted input (including the
// empty string and a lowercase verb — HTTP methods are case-sensitive
// tokens, and gin/net-http never lowercase them, but a defensive default
// only holds if it is actually tested) collapsing to "other".
func TestNormalizeMethodLabel_Table(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{http.MethodGet, http.MethodGet},
		{http.MethodHead, http.MethodHead},
		{http.MethodPost, http.MethodPost},
		{http.MethodPut, http.MethodPut},
		{http.MethodPatch, http.MethodPatch},
		{http.MethodDelete, http.MethodDelete},
		{http.MethodOptions, http.MethodOptions},
		{http.MethodTrace, http.MethodTrace},
		{http.MethodConnect, http.MethodConnect},
		{"PROPFIND", "other"},
		{"get", "other"},
		{"", "other"},
		{"GET ", "other"},
	}
	for _, tc := range cases {
		if got := normalizeMethodLabel(tc.in); got != tc.want {
			t.Errorf("normalizeMethodLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMiddleware_NonProbe5xxExcludesKubernetesProbePaths is the oracle for
// NonProbe5xxTotal, the series newhub_relay_5xx_elevated
// (deploy/r6-host-netdata/health.d/newhub.conf) was rebound to in cycle-13
// L10's repair round.
//
// That alarm used to watch lurus_gateway_requests_total with
// `chart labels: status=5*`, and the operator's 2026-09-15 live check found
// the only chart it matched was path=/api/health status=503 — so it was in
// practice a health-probe alarm. Cycle-13 L11 then made /api/health and
// /api/status answer 503 for the whole graceful-drain window on purpose,
// which would have turned every rollout into a WARNING with no incident
// behind it. A netdata `chart labels:` filter cannot express "this path AND
// that status" (the file's own GATE GAP/⚠VERIFY notes cover what its
// simple-pattern syntax can be trusted to do), so the split is made here,
// in code, where it is testable: a 5xx on a path a Kubernetes probe uses is
// not counted; a 5xx anywhere else is.
//
// Driven through the real gin middleware and httptest, and through the real
// route templates (c.FullPath()), not a hand-built WithLabelValues call.
func TestMiddleware_NonProbe5xxExcludesKubernetesProbePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/api/health", func(c *gin.Context) { c.Status(http.StatusServiceUnavailable) })
	r.GET("/api/status", func(c *gin.Context) { c.Status(http.StatusServiceUnavailable) })
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })
	r.GET("/api/user/self", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/token/999", func(c *gin.Context) { c.Status(http.StatusNotFound) })

	before := testutil.ToFloat64(NonProbe5xxTotal)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/health", nil),
		httptest.NewRequest(http.MethodGet, "/api/status", nil),
		httptest.NewRequest(http.MethodGet, "/api/user/self", nil),
		httptest.NewRequest(http.MethodGet, "/api/token/999", nil),
	} {
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	if got := testutil.ToFloat64(NonProbe5xxTotal) - before; got != 0 {
		t.Errorf("NonProbe5xxTotal rose by %v after two probe 503s, a 200 and a 404 — want 0. "+
			"A drain makes /api/health and /api/status answer 503 for the whole shutdown window "+
			"(internal/lifecycle/drain.go), so counting those would make every rollout look like "+
			"a 5xx incident", got)
	}

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if got := testutil.ToFloat64(NonProbe5xxTotal) - before; got != 1 {
		t.Errorf("NonProbe5xxTotal rose by %v in total after a relay 500 followed the four requests "+
			"above — want exactly 1: a customer-facing 5xx is what this series exists to count", got)
	}
}
