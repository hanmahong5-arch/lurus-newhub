package middleware

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/config"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CORSExposedHeaders are the response headers a browser XHR/fetch caller may
// read via response.headers.get(...) — without Access-Control-Expose-Headers
// these are sent on the wire but invisible to JS. Every gateway-authored
// response header documented in relay.json (X-Request-Id to drive
// GET /v1/generation, rate-limit headroom/reject headers, cost/quota and the
// model provider) lives here so cycle-5's headers stop being
// browser-invisible to any browser origin listed in ALLOWED_ORIGINS — no
// sibling product reads them today (grepped 2026-09-09: none found), this is
// additive capability, not a wired integration; openapi_contract_lock_test.go's
// TestOpenAPIContract_DocumentedResponseHeadersAreCORSExposed checks every
// header documented in relay.json is a subset of this list.
var CORSExposedHeaders = []string{
	"X-Request-Id", "X-Oneapi-Request-Id",
	"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
	"X-RateLimit-Scope", "X-RateLimit-Type", "Retry-After",
	"X-Model-Provider", "X-Request-Cost", "X-Quota-Remaining",
}

// X-Lurus-Instance is deliberately absent: its only emitter is /metrics,
// which is mounted on the root engine without CORS(), so exposing it here
// would advertise a header no cross-origin response can carry.

// CORSAllowedHeaders are the request headers a browser preflight must admit
// through, or the browser strips them from the real request before it
// leaves the client. The first seven are the pre-existing set (frontend
// api.js + relay TokenAuth key extraction + internal API key auth);
// X-Request-Id/X-Session-Id/X-Lurus-Product/traceparent let a browser caller
// send cycle-5's correlation and attribution headers instead of silently
// falling back to the gateway's defaults on every request. tracestate rides
// alongside traceparent because W3C Trace Context allows a propagator to send
// it as well (it is optional, traceparent is not), and a preflight that admits
// traceparent while rejecting a tracestate the client did send fails the whole
// request, not just that one header.
var CORSAllowedHeaders = []string{
	"Origin", "Content-Type", "Authorization",
	"X-API-Key", "lurus-api-User", "New-Api-User", "Cache-Control",
	"X-Request-Id", "X-Session-Id", "X-Lurus-Product", "traceparent", "tracestate",
}

func CORS() gin.HandlerFunc {
	corsConfig := cors.DefaultConfig()
	// Load allowed origins from centralized config (env: ALLOWED_ORIGINS)
	corsConfig.AllowOrigins = config.Get().CORS.AllowedOrigins
	corsConfig.AllowCredentials = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	// A literal "*" is NOT treated as a wildcard by the CORS spec when
	// AllowCredentials is true — browsers reject the response. List the
	// headers actual callers set instead.
	corsConfig.AllowHeaders = CORSAllowedHeaders
	corsConfig.ExposeHeaders = CORSExposedHeaders
	return cors.New(corsConfig)
}
