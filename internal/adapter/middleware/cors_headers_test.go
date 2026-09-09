package middleware

// cors_headers_test.go — L6 wire-contract lock: cycle-5's headers
// (X-Request-Id, headroom, cost, instance) were being written but were
// invisible to browser callers — no Access-Control-Expose-Headers meant a
// sibling product's JS could not read them, and X-Lurus-Product/X-Session-Id
// were not in Access-Control-Allow-Headers so a browser preflight would
// strip them before the real request left the client. Drives the real
// CORS() middleware (cors.go), not a hand-built cors.Config.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/config"

	"github.com/gin-gonic/gin"
)

// withCORSTestOrigin points config.Get().CORS.AllowedOrigins at a single
// origin for the duration of the test and restores it after — CORS() reads
// the singleton at construction time, so the override must be in place
// before the middleware is built.
func withCORSTestOrigin(t *testing.T, origins ...string) {
	t.Helper()
	cfg := config.Get()
	prev := cfg.CORS.AllowedOrigins
	cfg.CORS.AllowedOrigins = origins
	t.Cleanup(func() { cfg.CORS.AllowedOrigins = prev })
}

// wantExposedHeaders is a literal snapshot of the header names
// CORSExposedHeaders is expected to carry — NOT read from that variable, so
// deleting an entry from cors.go makes this test fail instead of silently
// shrinking the thing it iterates over.
var wantExposedHeaders = []string{
	"X-Request-Id", "X-Oneapi-Request-Id",
	"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
	"X-RateLimit-Scope", "X-RateLimit-Type", "Retry-After",
	"X-Model-Provider", "X-Request-Cost", "X-Quota-Remaining",
}

// TestCORS_ExposesCycle5Headers is case (a): a GET from an allowed origin
// must expose every header in wantExposedHeaders so browser JS can read
// X-Request-Id (GET /v1/generation's query key), headroom, cost and
// instance headers.
func TestCORS_ExposesCycle5Headers(t *testing.T) {
	withCORSTestOrigin(t, "https://allowed.example")
	r := gin.New()
	r.Use(CORS())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://allowed.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// http.Header canonicalizes each hyphen-separated segment
	// ("X-RateLimit-Limit" -> "X-Ratelimit-Limit"), and HTTP header names
	// are case-insensitive on the wire regardless — compare lower-cased.
	exposed := strings.ToLower(w.Header().Get("Access-Control-Expose-Headers"))
	for _, h := range wantExposedHeaders {
		if !strings.Contains(exposed, strings.ToLower(h)) {
			t.Errorf("Access-Control-Expose-Headers = %q, missing %q", exposed, h)
		}
	}
}

// TestCORS_PreflightAllowsCycle5RequestHeaders is case (b): a preflight
// asking for the cycle-5 request headers must be allowed through, or a
// browser strips them from the real request before it ever reaches the
// gateway.
func TestCORS_PreflightAllowsCycle5RequestHeaders(t *testing.T) {
	withCORSTestOrigin(t, "https://allowed.example")
	r := gin.New()
	r.Use(CORS())
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://allowed.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers",
		"authorization,content-type,x-lurus-product,x-session-id,x-request-id,traceparent,tracestate")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want 204/200", w.Code)
	}
	allowed := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
	// tracestate is asserted alongside traceparent: a browser tracer that
	// sends both fails the whole preflight if only one is admitted.
	for _, h := range []string{"x-lurus-product", "x-session-id", "x-request-id", "traceparent", "tracestate"} {
		if !strings.Contains(allowed, h) {
			t.Errorf("Access-Control-Allow-Headers = %q, missing %q", allowed, h)
		}
	}
}

// TestCORS_UnlistedOriginGetsNothing is case (c): an origin that is not on
// the allow-list must not receive Allow-Origin or Expose-Headers — adding
// the expose/allow lists must not widen who gets them.
func TestCORS_UnlistedOriginGetsNothing(t *testing.T) {
	withCORSTestOrigin(t, "https://allowed.example")
	r := gin.New()
	r.Use(CORS())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for an unlisted origin", got)
	}
	if got := w.Header().Get("Access-Control-Expose-Headers"); got != "" {
		t.Errorf("Access-Control-Expose-Headers = %q, want empty for an unlisted origin", got)
	}
}

// TestCORS_ExposesHeadersOnRelayShapedRoute is case (d): the same check as
// (a) but through a relay-shaped POST route with a JSON body and response,
// so a regression that only wired ExposeHeaders into a bare GET/200 (and
// not into a POST that reads a body and returns JSON) would still be
// caught. This mounts CORS() itself, like (a) — it does not exercise the
// real router files that mount it (api-router.go, api-v2-router.go,
// dashboard.go, relay-router.go).
func TestCORS_ExposesHeadersOnRelayShapedRoute(t *testing.T) {
	withCORSTestOrigin(t, "https://allowed.example")
	router := gin.New()
	router.Use(CORS())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://allowed.example")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	exposed := strings.ToLower(w.Header().Get("Access-Control-Expose-Headers"))
	if !strings.Contains(exposed, "x-request-id") {
		t.Errorf("relay-shaped POST Access-Control-Expose-Headers = %q, missing X-Request-Id", exposed)
	}
}
