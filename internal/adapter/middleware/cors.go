package middleware

import (
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CORSExposedHeaders are the response headers a browser XHR/fetch caller may
// read via response.headers.get(...) — without Access-Control-Expose-Headers
// these are sent on the wire but invisible to JS. Every gateway-authored
// response header documented in relay.json (X-Request-Id to drive
// GET /v1/generation, rate-limit headroom/reject headers, cost/quota and the
// adapter the call was relayed through) lives here so cycle-5's headers stop
// being browser-invisible — no sibling product reads them today (grepped
// 2026-09-09 and re-checked 2026-09-21: none found), this is additive
// capability, not a wired integration; openapi_contract_lock_test.go's
// TestOpenAPIContract_DocumentedResponseHeadersAreCORSExposed checks every
// header documented in relay.json is a subset of this list.
var CORSExposedHeaders = []string{
	"X-Request-Id", "X-Oneapi-Request-Id",
	"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
	"X-RateLimit-Scope", "X-RateLimit-Type", "Retry-After",
	"X-Request-Cost", "X-Quota-Remaining",
	// X-Relay-Adapter replaced X-Model-Provider (cycle-14 L6). The value was
	// always constant.GetChannelTypeName(info.ChannelType) — the adapter
	// family the channel is configured as, i.e. the wire dialect we spoke
	// upstream — never a measurement of who serves the model. A model served
	// by one vendor through an OpenAI-compatible channel reported the other
	// vendor's name. See helper.SetPerceptionHeaders for the full reasoning.
	"X-Relay-Adapter",
	// X-Cost-Reporting tells a caller WHERE the cost of this call is
	// reported: "headers" (X-Request-Cost/X-Quota-Remaining are present) or
	// "final-usage-frame" (a streamed response — the cost is not known when
	// the headers must be flushed, so it arrives in the last SSE frame's
	// usage.x_lurus object instead). Without it the streaming/non-streaming
	// asymmetry is something a customer discovers by not finding a header.
	"X-Cost-Reporting",
	// X-Model-Provider is retained here ONLY because docs/openapi/relay.json
	// still documents it and the contract lock above requires
	// documented ⊆ exposed. Nothing emits it any more. Naming a header in
	// Access-Control-Expose-Headers that no response carries is a no-op for
	// the browser. Remove this entry in the same change that drops the
	// header from relay.json (L6 hand-off #3).
	"X-Model-Provider",
	// X-Lurus-Affinity-Key (L5, console-ux-30): set only when the request
	// carried a session-affinity source (app.DeriveSessionAffinityKey);
	// exposed so a browser caller/admin tool can read it back to purge that
	// binding via DELETE /api/v2/admin/routing/affinity/:key.
	"X-Lurus-Affinity-Key",
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

// RelayCORSAllowedHeaders is CORSAllowedHeaders plus the credential and
// version headers the machine plane's non-OpenAI wires read off the request.
// Each name below was taken from an actual read site, not from a vendor's
// documentation:
//
//	anthropic-version / anthropic-beta — relay-router.go's /v1/models switch
//	  and the Claude request path (provider/claude).
//	x-goog-api-key — the Gemini native wire (/v1beta) and the /v1/models
//	  switch.
//	mj-api-secret — the Midjourney proxy plane (/mj, /:mode/mj).
//	Accept — already CORS-safelisted for simple values, listed so a preflight
//	  that names it explicitly (text/event-stream for SSE) is not refused.
//
// A browser client that cannot send these cannot speak those wires at all:
// the preflight strips the header before the real request leaves the client.
var RelayCORSAllowedHeaders = append(append([]string{}, CORSAllowedHeaders...),
	"anthropic-version", "anthropic-beta", "x-goog-api-key", "mj-api-secret", "Accept",
)

// corsAllowedMethods is shared by both postures; the relay plane uses the
// same verb set as the console.
var corsAllowedMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}

// CORS is the CONSOLE posture: a strict origin allow-list
// (env ALLOWED_ORIGINS) with credentials allowed, because the console API is
// authenticated by a session cookie and therefore does have a cross-site
// request forgery surface.
//
// Refusals carry a real envelope. gin-contrib/cors answers a disallowed
// origin with c.AbortWithStatus(403) and a ZERO-LENGTH body (cors.go's
// applyCors, v1.7.2) — no code, no message, no request id, nothing a
// customer can paste into a support ticket. So the allow-list decision is
// made here, before the library sees the request, and the library is
// configured with an always-true AllowOriginFunc: it never rejects anything,
// it only writes the Access-Control-* headers for origins this wrapper
// already admitted. That keeps ONE origin check rather than two that can
// disagree.
func CORS() gin.HandlerFunc {
	// Read once, at construction time — tests (and config reloads) that
	// point config.Get().CORS.AllowedOrigins somewhere else must do it
	// before the middleware is built, which is the pre-existing contract
	// this function had.
	allowed := append([]string{}, config.Get().CORS.AllowedOrigins...)

	corsConfig := cors.DefaultConfig()
	// AllowOriginFunc (not AllowOrigins): the decision is made by the
	// wrapper below. Note this also removes a startup panic — cors.Config's
	// Validate() panics on an empty AllowOrigins with no origin func, i.e.
	// on a deployment with ALLOWED_ORIGINS unset.
	corsConfig.AllowOriginFunc = func(string) bool { return true }
	corsConfig.AllowCredentials = true
	corsConfig.AllowMethods = corsAllowedMethods
	// A literal "*" is NOT treated as a wildcard by the CORS spec when
	// AllowCredentials is true — browsers reject the response. List the
	// headers actual callers set instead.
	corsConfig.AllowHeaders = CORSAllowedHeaders
	corsConfig.ExposeHeaders = CORSExposedHeaders
	inner := cors.New(corsConfig)

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin == "" || corsIsSameOrigin(origin, c.Request.Host) {
			// Not a cross-origin request; the library would return here too.
			return
		}
		if !corsOriginAllowed(origin, allowed) {
			abortWithOpenAiMessage(c, http.StatusForbidden,
				"cross-origin request refused: "+corsSafeOriginForMessage(origin)+
					" is not in this deployment's browser origin allow-list. The console API answers browser calls only from its own origins; the machine API (/v1, /v1beta, /mj, /suno) accepts any origin and authenticates with the bearer key instead.",
				string(types.ErrorCodeAccessDenied))
			return
		}
		inner(c)
	}
}

// RelayCORS is the MACHINE posture, for the bearer-authenticated relay plane
// (/v1, /v1beta, /mj, /:mode/mj, /suno, /v1/audio, /pg, /internal and the
// static web assets — everything SetRelayRouter's engine-level Use reaches).
//
// Permissive origin, credentials NOT allowed. That pairing is what makes it
// safe: these routes read a bearer key (Authorization / x-api-key /
// x-goog-api-key / X-API-Key), never a cookie, so a browser is forbidden
// from attaching ambient credentials to a cross-origin call here and there
// is no cross-site request forgery surface to protect. The console's
// allow-list protected nothing on this plane — an attacker simply omits the
// Origin header, which no browser-based attack can be forced to send — while
// refusing every honest customer building a browser client, before
// authentication, with an empty body.
//
// Consequence worth stating plainly: with AllowCredentials false the library
// answers with "Access-Control-Allow-Origin: *", so a browser will REFUSE to
// send cookies here even if a caller asks it to. That is intended. Any route
// that ever needs cookie auth must not live behind this middleware.
//
// BLIND SPOT: this function decides nothing about WHERE it is mounted. It
// cannot see a console (cookie-authenticated) route accidentally registered
// after SetRelayRouter's engine-level Use — gin snapshots a group's
// middleware chain at Group() call time, so today's posture split relies on
// main.go's SetRouter ordering (console groups built first). That ordering
// is asserted separately by TestRelayCORS_ConsoleGroupsAreBuiltBeforeTheRelayMount.
func RelayCORS() gin.HandlerFunc {
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowCredentials = false
	corsConfig.AllowMethods = corsAllowedMethods
	corsConfig.AllowHeaders = RelayCORSAllowedHeaders
	corsConfig.ExposeHeaders = CORSExposedHeaders
	return cors.New(corsConfig)
}

// corsIsSameOrigin mirrors gin-contrib/cors's own same-origin short-circuit:
// a request whose Origin is this host is not a cross-origin request at all
// (fetch() sets Origin on same-origin POSTs too).
func corsIsSameOrigin(origin, host string) bool {
	return origin == "http://"+host || origin == "https://"+host
}

// corsOriginAllowed is the console allow-list check. Case-insensitive and
// whitespace-trimmed, matching the normalisation gin-contrib/cors applied to
// the configured list. A bare "*" entry means "any origin"; unlike the
// library's own handling of "*", this keeps AllowCredentials working because
// the library then echoes the concrete origin back rather than the literal
// star (a starred Allow-Origin with credentials is rejected by every
// browser). Patterns with an embedded "*" are NOT supported — AllowWildcard
// was never enabled here, so an entry like "https://*.example" never matched
// anything before this change either.
func corsOriginAllowed(origin string, allowed []string) bool {
	o := strings.ToLower(strings.TrimSpace(origin))
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "*" || a == o {
			return true
		}
	}
	return false
}

// corsSafeOriginForMessage bounds what of an attacker-controlled Origin
// header is echoed into the refusal body: printable ASCII only, at most 128
// bytes, dropped rather than truncated — the same rule
// provider.boundUpstreamRequestId applies to vendor ids. A truncated origin
// reads as valid but matches nothing.
func corsSafeOriginForMessage(origin string) string {
	if origin == "" || len(origin) > 128 {
		return "this origin"
	}
	for i := 0; i < len(origin); i++ {
		if origin[i] < 0x20 || origin[i] > 0x7E {
			return "this origin"
		}
	}
	return origin
}
