package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets a conservative set of hardening response headers on
// every response (SPA, JSON API, and streaming relay alike). Headers are set
// before c.Next() — a streaming relay response starts writing bytes as soon
// as the handler runs, so anything added after c.Next() would be too late.
//
// Deliberately does NOT set Content-Security-Policy: the SPA (Semi UI, Vite)
// has never been audited for CSP compatibility, and a wrong policy silently
// breaks the whole frontend with no visible error for whoever hits it next —
// that risk outweighs what a CSP would additionally buy here. CORS is
// untouched; that's handled separately by CORS() in cors.go.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Stops browsers from MIME-sniffing a response body into a different
		// content type than the server declared (e.g. treating a JSON error
		// body as HTML/JS). No functional cost: the SPA and every JSON/SSE
		// relay response already declare an accurate Content-Type.
		c.Header("X-Content-Type-Options", "nosniff")

		// Nothing in this codebase embeds newhub pages in an <iframe> from
		// another origin (or same-origin either), so there is no known use
		// case to preserve — refuse all framing rather than allow
		// same-origin framing that nothing currently relies on.
		c.Header("X-Frame-Options", "DENY")

		// Cross-origin navigations/requests only see the origin, not the full
		// path or query string (avoids leaking tenant slugs or token-bearing
		// URLs to third-party Referer headers). Same-origin navigation still
		// sends the full referrer, so in-app SPA routing is unaffected.
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		// Only advertise HSTS when this request actually arrived over TLS —
		// either directly (c.Request.TLS != nil) or via the host nginx that
		// terminates TLS and forwards X-Forwarded-Proto: https (both
		// R6 vhosts set this on every request). Sending it to a plain-HTTP
		// caller (e.g. local `go run`, no TLS) would tell the browser to force
		// HTTPS on a host that doesn't speak it. No includeSubDomains or
		// preload: neither has been deliberately decided for every subdomain,
		// and HSTS preload submission is a separate, hard-to-reverse decision
		// this fix should not make on its own.
		if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
			c.Header("Strict-Transport-Security", "max-age=15552000")
		}

		c.Next()
	}
}
