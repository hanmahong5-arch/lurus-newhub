package middleware

// browser_origin_guard_test.go — L4 (cycle 12). Pins every branch of
// BrowserOriginGuard's decision table, including the four it deliberately
// lets through (credential-header callers, cookie-less callers, safe
// methods, and a browser that sends neither Sec-Fetch-Site nor Origin).
//
// The table below is the whole contract: a reviewer who wants to know what
// the guard does reads these rows, not the middleware's prose.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// browserOriginGuardEngine mounts the guard ahead of a terminal handler that
// answers 200, so "reached the handler" and "the guard admitted it" are the
// same observation.
func browserOriginGuardEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(BrowserOriginGuard())
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	r.POST("/t", pass)
	r.PUT("/t", pass)
	r.DELETE("/t", pass)
	r.GET("/t", pass)
	r.OPTIONS("/t", pass)
	return r
}

type browserOriginGuardCase struct {
	name       string
	method     string
	cookies    []*http.Cookie
	headers    map[string]string
	wantStatus int
	// wantCode is asserted only on a rejection.
	wantCode string
}

func (tc browserOriginGuardCase) run(t *testing.T) {
	t.Helper()
	req := httptest.NewRequest(tc.method, "/t", nil)
	for _, ck := range tc.cookies {
		req.AddCookie(ck)
	}
	for k, v := range tc.headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	browserOriginGuardEngine().ServeHTTP(w, req)

	if w.Code != tc.wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.wantStatus, w.Body.String())
	}
	if tc.wantStatus == http.StatusOK {
		if !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatalf("admitted request did not reach the terminal handler; body=%s", w.Body.String())
		}
		return
	}
	if tc.wantCode != "" && !strings.Contains(w.Body.String(), tc.wantCode) {
		t.Fatalf("body = %s, want it to carry error_code %q", w.Body.String(), tc.wantCode)
	}
}

// ginSessionCookie is the gin session cookie this process actually sets
// (cmd/server/main.go's sessions.Sessions("session", store)).
func ginSessionCookie() *http.Cookie {
	return &http.Cookie{Name: "session", Value: "MTIzNA=="}
}

// platformSessionCookie is the platform-issued bridge cookie
// (zita.SessionCookieName); a request carrying only this one is still a
// cookie-authenticated request as far as OptionalZitaIdentity is concerned.
func platformSessionCookie() *http.Cookie {
	return &http.Cookie{Name: "lurus_session", Value: "header.payload.sig"}
}

func TestBrowserOriginGuard_DecisionTable(t *testing.T) {
	withCORSTestOrigin(t, "https://hub.lurus.cn")

	cases := []browserOriginGuardCase{
		{
			name:       "same_site_cookie_post_rejected",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "same-site"},
			wantStatus: http.StatusForbidden,
			wantCode:   "CROSS_SITE_REQUEST",
		},
		{
			name:       "cross_site_cookie_post_rejected",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusForbidden,
			wantCode:   "CROSS_SITE_REQUEST",
		},
		{
			name:       "cross_site_platform_cookie_delete_rejected",
			method:     http.MethodDelete,
			cookies:    []*http.Cookie{platformSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusForbidden,
			wantCode:   "CROSS_SITE_REQUEST",
		},
		{
			name:       "same_origin_cookie_post_admitted",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "same-origin"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "none_cookie_put_admitted",
			method:     http.MethodPut,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "none"},
			wantStatus: http.StatusOK,
		},
		{
			// Cycle-13 L8. This row used to be 200: the guard consulted the
			// credential headers BEFORE the cookie, so any attacker-supplied
			// Authorization value — it does not have to authenticate
			// anything — bought a pass out of the guard while authHelper
			// still authenticated the request from the cookie it ignored.
			// The junk value below is the whole point: it is not a
			// credential, and the request is still a cookie-authenticated
			// cross-site write.
			name:       "cross_site_cookie_plus_junk_authorization_rejected",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site", "Authorization": "Bearer not-a-real-key"},
			wantStatus: http.StatusForbidden,
			wantCode:   "CROSS_SITE_REQUEST",
		},
		{
			name:       "cross_site_cookie_plus_junk_api_key_rejected",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site", "X-API-Key": "lurus_ik_not_a_real_key"},
			wantStatus: http.StatusForbidden,
			wantCode:   "CROSS_SITE_REQUEST",
		},
		{
			name:       "cross_site_without_cookie_admitted",
			method:     http.MethodPost,
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusOK,
		},
		{
			// The relay / switch / lutu / internal-API callers: a credential
			// header and NO browser session cookie. They keep passing, and
			// they pass because of the cookie row, not because of the header
			// — which is what makes deleting the header exemption safe.
			name:       "credential_header_without_cookie_admitted",
			method:     http.MethodPost,
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site", "Authorization": "Bearer sk-relay-client"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "api_key_header_without_cookie_admitted",
			method:     http.MethodPost,
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site", "X-API-Key": "lurus_ik_internal_caller"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "unrelated_cookie_only_admitted",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{{Name: "theme", Value: "dark"}},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "no_fetch_metadata_no_origin_admitted",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			wantStatus: http.StatusOK,
		},
		{
			name:       "origin_allowlisted_admitted",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Origin": "https://hub.lurus.cn"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "origin_not_allowlisted_rejected",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Origin": "https://attacker.example"},
			wantStatus: http.StatusForbidden,
			wantCode:   "CROSS_SITE_REQUEST",
		},
		{
			name:       "fetch_metadata_wins_over_allowlisted_origin",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://hub.lurus.cn"},
			wantStatus: http.StatusForbidden,
			wantCode:   "CROSS_SITE_REQUEST",
		},
		{
			name:       "get_is_never_guarded",
			method:     http.MethodGet,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "options_is_never_guarded",
			method:     http.MethodOptions,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// TestBrowserOriginGuard_CountsRejections proves the rejection is visible on
// /metrics with the reason that produced it, not only in the response the
// attacker sees: the two reject branches carry different labels.
func TestBrowserOriginGuard_CountsRejections(t *testing.T) {
	withCORSTestOrigin(t, "https://hub.lurus.cn")

	beforeFetch := testutil.ToFloat64(metrics.CSRFRejectedTotal.WithLabelValues("sec_fetch_site"))
	beforeOrigin := testutil.ToFloat64(metrics.CSRFRejectedTotal.WithLabelValues("origin"))

	browserOriginGuardCase{
		name:       "metric_fetch_metadata",
		method:     http.MethodPost,
		cookies:    []*http.Cookie{ginSessionCookie()},
		headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
		wantStatus: http.StatusForbidden,
		wantCode:   "CROSS_SITE_REQUEST",
	}.run(t)

	browserOriginGuardCase{
		name:       "metric_origin",
		method:     http.MethodPost,
		cookies:    []*http.Cookie{ginSessionCookie()},
		headers:    map[string]string{"Origin": "https://attacker.example"},
		wantStatus: http.StatusForbidden,
		wantCode:   "CROSS_SITE_REQUEST",
	}.run(t)

	if got := testutil.ToFloat64(metrics.CSRFRejectedTotal.WithLabelValues("sec_fetch_site")); got != beforeFetch+1 {
		t.Errorf("csrf_rejected_total{reason=sec_fetch_site} = %v, want %v", got, beforeFetch+1)
	}
	if got := testutil.ToFloat64(metrics.CSRFRejectedTotal.WithLabelValues("origin")); got != beforeOrigin+1 {
		t.Errorf("csrf_rejected_total{reason=origin} = %v, want %v", got, beforeOrigin+1)
	}
}

// ---------------------------------------------------------------------------
// CSRF_ORIGIN_GUARD_MODE (cycle-12 L4, repair round). The guard had no lever
// short of a revert; the operator decision gave it three modes. These rows
// are the whole contract of that switch.
// ---------------------------------------------------------------------------

// crossSiteCookiePost is the one request shape the mode changes the answer
// to: cookie-authenticated, state-changing, and cross-site by the browser's
// own account.
func crossSiteCookiePost(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/t", nil)
	req.AddCookie(ginSessionCookie())
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	browserOriginGuardEngine().ServeHTTP(w, req)
	return w
}

func TestBrowserOriginGuard_ModeTable(t *testing.T) {
	withCORSTestOrigin(t, "https://hub.lurus.cn")

	t.Run("unset_enforces", func(t *testing.T) {
		t.Setenv("CSRF_ORIGIN_GUARD_MODE", "")
		if w := crossSiteCookiePost(t); w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 — enforce is the default; body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("typo_enforces", func(t *testing.T) {
		// A misspelt value must not disable a security control.
		t.Setenv("CSRF_ORIGIN_GUARD_MODE", "enforcing")
		if w := crossSiteCookiePost(t); w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 — an unrecognised mode must mean enforce; body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("observe_admits_and_counts_separately", func(t *testing.T) {
		t.Setenv("CSRF_ORIGIN_GUARD_MODE", "observe")
		beforeObserved := testutil.ToFloat64(metrics.CSRFObservedTotal.WithLabelValues("sec_fetch_site"))
		beforeRejected := testutil.ToFloat64(metrics.CSRFRejectedTotal.WithLabelValues("sec_fetch_site"))

		w := crossSiteCookiePost(t)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatalf("status = %d body = %s, want the request admitted in observe mode", w.Code, w.Body.String())
		}
		if got := testutil.ToFloat64(metrics.CSRFObservedTotal.WithLabelValues("sec_fetch_site")); got != beforeObserved+1 {
			t.Errorf("csrf_observed_total{reason=sec_fetch_site} = %v, want %v — observe mode must still be measurable", got, beforeObserved+1)
		}
		if got := testutil.ToFloat64(metrics.CSRFRejectedTotal.WithLabelValues("sec_fetch_site")); got != beforeRejected {
			t.Errorf("csrf_rejected_total{reason=sec_fetch_site} = %v, want it unchanged at %v — nothing was refused, and an alarm on that counter must not read an observe deployment as protected",
				got, beforeRejected)
		}
	})

	t.Run("off_evaluates_nothing", func(t *testing.T) {
		t.Setenv("CSRF_ORIGIN_GUARD_MODE", "off")
		beforeObserved := testutil.ToFloat64(metrics.CSRFObservedTotal.WithLabelValues("sec_fetch_site"))
		beforeRejected := testutil.ToFloat64(metrics.CSRFRejectedTotal.WithLabelValues("sec_fetch_site"))

		w := crossSiteCookiePost(t)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 with the guard off; body=%s", w.Code, w.Body.String())
		}
		if got := testutil.ToFloat64(metrics.CSRFObservedTotal.WithLabelValues("sec_fetch_site")); got != beforeObserved {
			t.Errorf("csrf_observed_total moved with the guard off (%v -> %v) — off must mean no decision at all", beforeObserved, got)
		}
		if got := testutil.ToFloat64(metrics.CSRFRejectedTotal.WithLabelValues("sec_fetch_site")); got != beforeRejected {
			t.Errorf("csrf_rejected_total moved with the guard off (%v -> %v)", beforeRejected, got)
		}
	})
}

// ---------------------------------------------------------------------------
// Reachability. Everything above proves the guard DECIDES correctly; none of
// it proves the guard RUNS. The mount is a W hand-off in two router files
// this lane does not own, and both metric series read 0 whether the guard is
// mounted or not — so without this gate, dropping the mount would leave
// every test in this lane green and the only production signal
// indistinguishable from "no attacks".
//
// EXPECTED RED until W applies hand-off #1 and #2 (the two
// middleware.BrowserOriginGuard() mounts). That is the point: the lane's
// work is not done until the guard is on the route table production serves.
// ---------------------------------------------------------------------------

// browserOriginGuardMountSites are the route files that must mount the
// guard, with where in each the mount belongs.
//
// `after` is the full Use STATEMENT, not the bare "middleware.CORS()" this
// gate anchored on until cycle-12 W: api-v2-router.go names middleware.CORS()
// in a COMMENT three lines above the real call, so the ordering check was
// measuring the comment's offset. It passed either way — including for a
// mount wedged between the comment and the actual CORS Use, which is the one
// arrangement it exists to catch.
var browserOriginGuardMountSites = []struct {
	path  string
	after string
}{
	{filepath.Join("..", "handler", "router", "api-v2-router.go"), "apiV2.Use(middleware.CORS())"},
	{filepath.Join("..", "handler", "router", "api-router.go"), "apiRouter.Use(middleware.CORS())"},
}

func TestBrowserOriginGuard_IsMountedInProductionRouters(t *testing.T) {
	const mount = "middleware.BrowserOriginGuard()"

	for _, site := range browserOriginGuardMountSites {
		raw, err := os.ReadFile(site.path)
		if err != nil {
			t.Fatalf("read %s: %v", site.path, err)
		}
		source := string(raw)

		mountAt := strings.Index(source, mount)
		if mountAt < 0 {
			t.Errorf("%s does not mount the guard: no %q in the file. The middleware is dead code until it is mounted, and its metrics read 0 either way — mount it directly after %s.",
				site.path, mount, site.after)
			continue
		}
		corsAt := strings.Index(source, site.after)
		if corsAt < 0 {
			t.Errorf("%s no longer contains %q — this gate's ordering check cannot verify anything; fix the gate rather than deleting it", site.path, site.after)
			continue
		}
		if mountAt < corsAt {
			t.Errorf("%s mounts the guard BEFORE %s — a cross-site request would be refused without the CORS headers that let the browser read the refusal", site.path, site.after)
		}
	}
}
