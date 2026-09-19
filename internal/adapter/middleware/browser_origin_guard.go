package middleware

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

// browserSessionCookieNames are the cookies that make a request
// cookie-authenticated on this gateway: the gin session cookie this process
// registers (cmd/server/main.go's sessions.Sessions("session", store)) and
// the platform-issued bridge cookie OptionalZitaIdentity reads
// (optional_zita_identity.go). TestBrowserSessionCookieNames_MatchTheStoreSetup
// (session_cookie_options_test.go) pins the first name against
// cmd/server/main.go so the two cannot drift apart silently.
//
// A request carrying neither is not ambient-authority-bearing: an attacker
// page can already issue it from its own machine without the victim, so
// refusing it buys nothing. That is also what keeps POST
// /api/v2/bridge/exchange (no cookie on the way in) out of this guard.
var browserSessionCookieNames = []string{"session", zita.SessionCookieName}

// browserOriginGuardSafeMethods are the methods this guard never inspects.
// Not a claim that they are side-effect free everywhere — it is the
// CSRF-relevant line: a cross-site GET/HEAD cannot be blocked usefully here
// (browsers issue them for images and navigations constantly) and an OPTIONS
// is the CORS preflight the cors middleware answers before any handler runs.
var browserOriginGuardSafeMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// BrowserOriginGuard refuses a state-changing request that is authenticated
// by cookie alone and was initiated by a different site — the classic CSRF
// shape this gateway had no defence against: every /api and /api/v2 console
// write trusts the session cookie, SameSite=Lax (session_cookie_options.go)
// stops a cross-SITE form POST but not a same-site one from another
// subdomain, and there is no anti-CSRF token anywhere in the console.
//
// Decision table (cycle-12 §2, pinned row by row in
// browser_origin_guard_test.go):
//
//	GET / HEAD / OPTIONS ................................. admitted
//	Authorization or X-API-Key header present ............ admitted
//	no gin/platform session cookie ....................... admitted
//	Sec-Fetch-Site: same-origin | none ................... admitted
//	Sec-Fetch-Site: same-site | cross-site ............... 403
//	no Sec-Fetch-Site, Origin in ALLOWED_ORIGINS ......... admitted
//	no Sec-Fetch-Site, Origin elsewhere .................. 403
//	no Sec-Fetch-Site, no Origin ......................... admitted
//
// Two of those rows are worth their reasons:
//
// Credential-header callers are skipped because they are not vulnerable in
// the first place: a browser will not attach an Authorization or X-API-Key
// header to a cross-site request unless the page's own script sets it, and a
// page that can set it already has the credential. This is what keeps relay
// clients, the switch/lutu service callers and the /internal API (X-API-Key)
// out of the guard entirely.
//
// Sec-Fetch-Site is consulted BEFORE Origin and overrides it: the browser
// writes Sec-Fetch-Site itself and script cannot forge it, whereas
// ALLOWED_ORIGINS is a CORS read-permission list, not a list of sites
// trusted to act with the victim's cookie. An allowlisted origin making a
// genuinely cross-site cookie-bearing write is therefore still refused — if
// a sibling product ever needs that, it must present a credential header.
//
// The last row (neither header) admits the request: Fetch Metadata shipped
// in every current mainstream browser, but a scripted client or an old
// embedded webview sends neither header, and failing those closed would
// break callers that are not the attack this guards against.
func BrowserOriginGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if browserOriginGuardSafeMethods[c.Request.Method] {
			c.Next()
			return
		}
		if c.GetHeader("Authorization") != "" || c.GetHeader("X-API-Key") != "" {
			c.Next()
			return
		}
		if !hasBrowserSessionCookie(c) {
			c.Next()
			return
		}

		switch c.GetHeader("Sec-Fetch-Site") {
		case "same-origin", "none":
			c.Next()
			return
		case "same-site", "cross-site":
			rejectCrossSiteRequest(c, "sec_fetch_site")
			return
		}

		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		for _, allowed := range config.Get().CORS.AllowedOrigins {
			if origin == allowed {
				c.Next()
				return
			}
		}
		rejectCrossSiteRequest(c, "origin")
	}
}

// hasBrowserSessionCookie reports whether the request carries one of the
// cookies that would authenticate it without any credential header.
func hasBrowserSessionCookie(c *gin.Context) bool {
	for _, name := range browserSessionCookieNames {
		if v, err := c.Cookie(name); err == nil && v != "" {
			return true
		}
	}
	return false
}

// rejectCrossSiteRequest writes the guard's single refusal shape. reason is
// the metric label, not part of the response: telling a cross-site caller
// which check caught it only helps it probe for the other one.
func rejectCrossSiteRequest(c *gin.Context, reason string) {
	metrics.RecordCSRFRejected(reason)
	c.JSON(http.StatusForbidden, gin.H{
		"success":    false,
		"message":    "cross-site request refused",
		"error_code": "CROSS_SITE_REQUEST",
	})
	c.Abort()
}
