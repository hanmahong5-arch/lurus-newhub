package middleware

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

// csrfOriginGuardModeEnv names the environment variable that decides what
// BrowserOriginGuard does with a request it judges cross-site. Read fresh on
// every request — the same posture as TENANT_MISSING_MODE
// (repo.TenantMissingMode) and SESSION_REGISTRY_ENABLED — so an operator who
// has to switch it off during an incident does not have to restart three
// replicas to do it.
const csrfOriginGuardModeEnv = "CSRF_ORIGIN_GUARD_MODE"

// Guard modes.
//
//	enforce — refuse with 403 CROSS_SITE_REQUEST (the default, and what any
//	          unrecognised value means: a typo must not disable a security
//	          control)
//	observe — admit the request, count it under csrf_observed_total{reason}
//	          and log it (throttled). For measuring a suspected legitimate
//	          caller before enforcing, not a permanent setting
//	off     — the guard evaluates nothing at all: no decision, no metric.
//	          The lever for an incident where the guard itself is the fault
const (
	csrfGuardModeEnforce = "enforce"
	csrfGuardModeObserve = "observe"
	csrfGuardModeOff     = "off"
)

// csrfOriginGuardMode returns the configured mode, defaulting to enforce.
func csrfOriginGuardMode() string {
	switch strings.TrimSpace(os.Getenv(csrfOriginGuardModeEnv)) {
	case csrfGuardModeObserve:
		return csrfGuardModeObserve
	case csrfGuardModeOff:
		return csrfGuardModeOff
	default:
		return csrfGuardModeEnforce
	}
}

// csrfObserveLogLast throttles the observe-mode log line to one per reason
// per minute: observe mode is what someone runs while under a real
// cross-site attempt, and one ERROR line per request would be the outage.
// The metric is the unthrottled signal.
var csrfObserveLogLast sync.Map // reason -> time.Time

// csrfObserveLogWindow is how long a reason stays quiet after being logged.
const csrfObserveLogWindow = time.Minute

func csrfObserveLogf(reason, msg string) {
	now := time.Now()
	if last, loaded := csrfObserveLogLast.LoadOrStore(reason, now); loaded {
		if now.Sub(last.(time.Time)) < csrfObserveLogWindow {
			return
		}
		csrfObserveLogLast.Store(reason, now)
	}
	common.SysLog(msg)
}

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
// Decision table (cycle-12 §2 as amended by cycle-13 L8, pinned row by row
// in browser_origin_guard_test.go):
//
//	no gin/platform session cookie ....................... admitted
//	GET / HEAD / OPTIONS ................................. admitted
//	Sec-Fetch-Site: same-origin | none ................... admitted
//	Sec-Fetch-Site: same-site | cross-site ............... 403
//	no Sec-Fetch-Site, Origin in ALLOWED_ORIGINS ......... admitted
//	no Sec-Fetch-Site, Origin elsewhere .................. 403
//	no Sec-Fetch-Site, no Origin ......................... admitted
//
// The cookie row comes FIRST, and there is no credential-header row any
// more. Cycle-12 had one — "Authorization or X-API-Key present -> admitted"
// — ahead of the cookie check, on the theory that a credential-header caller
// is not vulnerable to CSRF. The theory is true of the CALLER and false of
// the CHECK: the guard saw that a header existed, not that it authenticated
// anything, while authHelper (auth.go) resolves the session
// cookie FIRST and ignores the header entirely. So an attacker page that
// added `Authorization: Bearer anything` to its cross-site fetch skipped the
// guard and was then authenticated from the victim's cookie — the exemption
// undid the control for any attacker who read this file.
//
// Deleting it costs the credential-header callers nothing: relay clients,
// the switch/lutu service callers and the /internal API (X-API-Key) carry no
// hub session cookie, so they are admitted one row earlier by the cookie
// check — which is also the row that keeps POST /api/v2/bridge/exchange (no
// cookie on the way in) out of the guard.
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
//
// CSRF_ORIGIN_GUARD_MODE (csrfOriginGuardMode) decides what a refusal row
// does: enforce (default) answers 403, observe counts and admits, off skips
// the guard entirely. The decision table above is the enforce column; in
// observe mode every row admits.
func BrowserOriginGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		mode := csrfOriginGuardMode()
		if mode == csrfGuardModeOff {
			c.Next()
			return
		}
		// Ambient authority first: a request that carries no browser session
		// cookie cannot be made to act with the victim's credentials, so
		// nothing below it can apply. Ordering this ahead of the method check
		// is what makes the "no credential-header exemption" table above
		// readable top to bottom in the same order the code decides.
		if !hasBrowserSessionCookie(c) {
			c.Next()
			return
		}
		if browserOriginGuardSafeMethods[c.Request.Method] {
			c.Next()
			return
		}

		switch c.GetHeader("Sec-Fetch-Site") {
		case "same-origin", "none":
			c.Next()
			return
		case "same-site", "cross-site":
			refuseCrossSiteRequest(c, mode, "sec_fetch_site")
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
		refuseCrossSiteRequest(c, mode, "origin")
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

// refuseCrossSiteRequest applies the configured mode to a request the
// decision table judged cross-site. reason is the metric label, never part
// of the response: telling a cross-site caller which check caught it only
// helps it probe for the other one.
//
// In observe mode the request is admitted and counted separately — the
// enforce counter must keep meaning "was actually refused", so that a
// dashboard cannot read an observe-mode deployment as a protected one.
func refuseCrossSiteRequest(c *gin.Context, mode, reason string) {
	if mode == csrfGuardModeObserve {
		metrics.RecordCSRFObserved(reason)
		csrfObserveLogf(reason, "CSRF origin guard (observe mode) would have refused "+
			c.Request.Method+" "+c.FullPath()+" — reason="+reason+
			"; set CSRF_ORIGIN_GUARD_MODE=enforce to make this a 403")
		c.Next()
		return
	}
	metrics.RecordCSRFRejected(reason)
	c.JSON(http.StatusForbidden, gin.H{
		"success":    false,
		"message":    "cross-site request refused",
		"error_code": "CROSS_SITE_REQUEST",
	})
	c.Abort()
}
