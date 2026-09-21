package handler

// zita_logout_test.go — cycle-13 L8. GET/POST /api/v2/auth/zita-logout is
// unauthenticated by design (it has to work when the session is already
// stale), and it used to echo whatever ?return_to= said straight into a 302
// Location / the POST body's redirect_to. That is an open redirect on the
// one endpoint an attacker can be sure a logged-out victim will follow: a
// "session expired, sign in again" mail linking to
// hub.lurus.cn/api/v2/auth/zita-logout?return_to=https://evil.example/login
// lands the victim on a credential-harvesting page that the hub's own domain
// vouched for.
//
// The table below is the contract. isLurusReturnURL (zita_login.go) is the
// same predicate ZitaLogin already applies to its own return_to; this test
// pins that both arms of the logout route apply it too.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// buildLogoutRouter mounts ZitaLogout on both verbs the production router
// registers (api-v2-router.go:87-88) behind a cookie-backed session store,
// so the handler can clear a session without Redis.
func buildLogoutRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sessions.Sessions("test_session", cookie.NewStore([]byte("logout-test-secret"))))
	r.GET("/api/v2/auth/zita-logout", ZitaLogout)
	r.POST("/api/v2/auth/zita-logout", ZitaLogout)
	return r
}

// logoutRedirectCases enumerate the return_to values worth pinning: two
// off-site absolute forms (one ordinary, one protocol-relative — which
// url.Parse reads as host evil.example with an empty scheme, the shape a
// naive strings.HasPrefix("http") check waves through), a same-origin
// relative path, an on-domain https URL, and a javascript: URI.
var logoutRedirectCases = []struct {
	name     string
	returnTo string
	want     string
}{
	{"offsite_absolute", "https://evil.example/", "/login"},
	{"protocol_relative", "//evil.example/", "/login"},
	{"relative_path", "/dashboard", "/login"},
	{"on_domain_https", "https://hub.lurus.cn/x", "https://hub.lurus.cn/x"},
	{"javascript_uri", "javascript:alert(1)", "/login"},
	{"absent", "", "/login"},
}

// offSiteHosts are the hosts no answer may send the browser to. Checked
// alongside the exact `want` so that a future predicate change which starts
// admitting a different off-site host still trips this test.
var offSiteHosts = []string{"evil.example", "attacker.example"}

func assertNotOffSite(t *testing.T, where, got string) {
	t.Helper()
	for _, host := range offSiteHosts {
		if strings.Contains(got, host) {
			t.Errorf("%s = %q — it sends the browser to %s; logout must never redirect off-site", where, got, host)
		}
	}
}

// TestZitaLogout_GetRedirectIsGated: the GET arm answers 302 and its
// Location must be the gated value.
func TestZitaLogout_GetRedirectIsGated(t *testing.T) {
	for _, tc := range logoutRedirectCases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/api/v2/auth/zita-logout"
			if tc.returnTo != "" {
				url += "?return_to=" + tc.returnTo
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()
			buildLogoutRouter().ServeHTTP(w, req)

			if w.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302; body=%s", w.Code, w.Body.String())
			}
			got := w.Header().Get("Location")
			assertNotOffSite(t, "Location", got)
			if got != tc.want {
				t.Errorf("Location = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestZitaLogout_PostRedirectToIsGated: the POST arm answers 200 and hands
// the SPA a redirect_to it will assign to window.location — same gate.
func TestZitaLogout_PostRedirectToIsGated(t *testing.T) {
	for _, tc := range logoutRedirectCases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/api/v2/auth/zita-logout"
			if tc.returnTo != "" {
				url += "?return_to=" + tc.returnTo
			}
			req := httptest.NewRequest(http.MethodPost, url, nil)
			w := httptest.NewRecorder()
			buildLogoutRouter().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
			var resp struct {
				Success bool `json:"success"`
				Data    struct {
					RedirectTo string `json:"redirect_to"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
			}
			if !resp.Success {
				t.Errorf("success = false, want true; body=%s", w.Body.String())
			}
			assertNotOffSite(t, "data.redirect_to", resp.Data.RedirectTo)
			if resp.Data.RedirectTo != tc.want {
				t.Errorf("data.redirect_to = %q, want %q", resp.Data.RedirectTo, tc.want)
			}
		})
	}
}

// TestZitaLogout_StillClearsCookies proves the redirect gate did not cost
// the handler its actual job: both session cookies are still expired on the
// way out, on both verbs. (do-not-regress: the "switch account" link on the
// disabled-account page depends on the platform cookie being cleared.)
func TestZitaLogout_StillClearsCookies(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v2/auth/zita-logout?return_to=https://evil.example/", nil)
			w := httptest.NewRecorder()
			buildLogoutRouter().ServeHTTP(w, req)

			setCookies := w.Header().Values("Set-Cookie")
			if len(setCookies) == 0 {
				t.Fatalf("no Set-Cookie headers; logout must expire the session cookies")
			}
			platformExpired := false
			for _, ck := range setCookies {
				if strings.HasPrefix(ck, "lurus_session=") && strings.Contains(ck, "Max-Age=0") {
					platformExpired = true
				}
			}
			if !platformExpired {
				t.Errorf("no expiring lurus_session cookie among %v", setCookies)
			}
		})
	}
}
