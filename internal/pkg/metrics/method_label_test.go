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
