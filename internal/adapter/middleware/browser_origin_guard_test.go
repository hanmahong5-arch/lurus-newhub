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
			name:       "cross_site_with_authorization_admitted",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site", "Authorization": "Bearer sk-not-a-real-key"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "cross_site_with_api_key_admitted",
			method:     http.MethodPost,
			cookies:    []*http.Cookie{ginSessionCookie()},
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site", "X-API-Key": "lurus_ik_not_a_real_key"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "cross_site_without_cookie_admitted",
			method:     http.MethodPost,
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
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
