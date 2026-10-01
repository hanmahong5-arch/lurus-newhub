package handler_test

// oidc_login_e2e_test.go — the customer login path, end to end, with a
// browser-faithful cookie jar:
//
//	GET  /api/v2/<slug>/auth/login   (real router, real session middleware)
//	 -> 302 to the mock IdP authorize endpoint (PKCE + nonce + signed state)
//	 -> 302 back to OIDC_REDIRECT_URI with code+state
//	GET  /api/v2/oauth/callback      (token exchange, RS256 ID token, JWKS)
//	 -> Set-Cookie the browser must accept at https://hub.lurus.cn
//	GET  /api/v2/auth/session-info and /api/v2/<slug>/user/me with the jar.
//
// Why it exists: e2e runs against UAT with OIDC off, and the other Go tests
// stub the IdP in isolation. A production outage (SESSION_COOKIE_DOMAIN
// naming another host, so browsers silently dropped the session cookie and
// every SSO login "succeeded" and then looked logged out) was caught by
// nothing. net/http/cookiejar applies the same domain-match rule a browser
// does, so a cookie the jar refuses is a cookie a browser refuses.
//
// Deployment config mirrored from deploy/k8s/r6-stage/deployment.yaml:
// GIN_MODE=release (=> Secure cookie), SESSION_COOKIE_DOMAIN="" (host-only),
// OIDC_ENABLE_PKCE=true, public client (no secret), OIDC_AUTO_CREATE_USER=true,
// redirect URI https://hub.lurus.cn/api/v2/oauth/callback, and a Redis-backed
// session store (miniredis here) built from the same
// middleware.SessionCookieBaseOptions the server's bootstrap uses.
//
// Not covered, on purpose: the process-level engine in cmd/server (package
// main) and the SPA's own /oauth/oidc page; the router under test is
// router.SetApiV2Router.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/handler/router"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	sessionredis "github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	e2eHubHost     = "hub.lurus.cn"
	e2eHubOrigin   = "https://" + e2eHubHost
	e2eRedirectURI = e2eHubOrigin + "/api/v2/oauth/callback"
	e2eClientID    = "oidc-login-e2e-client"
	e2eSessionName = "session" // same name cmd/server registers
)

// e2eIdP is a minimal OIDC provider: discovery, authorize (302 with code),
// token (RS256 ID token, PKCE verified) and JWKS.
type e2eIdP struct {
	srv *httptest.Server

	mu    sync.Mutex
	codes map[string]e2eGrant

	email, sub string
}

type e2eGrant struct {
	nonce, challenge string
}

func newE2EIdP(t *testing.T) *e2eIdP {
	t.Helper()
	idp := &e2eIdP{codes: map[string]e2eGrant{}, email: "login-e2e@example.com", sub: "idp-subject-login-e2e"}
	jwks := middleware.JWKSet{Keys: []middleware.JWK{handler.OIDCSharedJWKForE2E(t)}}
	key := handler.OIDCSharedKeyForE2E(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 idp.srv.URL,
			"authorization_endpoint": idp.srv.URL + "/oauth/v2/authorize",
			"token_endpoint":         idp.srv.URL + "/oauth/v2/token",
			"jwks_uri":               idp.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	mux.HandleFunc("/oauth/v2/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != e2eClientID || q.Get("response_type") != "code" ||
			q.Get("redirect_uri") != e2eRedirectURI || q.Get("state") == "" ||
			q.Get("nonce") == "" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "invalid_request: "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		raw := make([]byte, 16)
		_, _ = rand.Read(raw)
		code := base64.RawURLEncoding.EncodeToString(raw)
		idp.mu.Lock()
		idp.codes[code] = e2eGrant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
		idp.mu.Unlock()
		back := url.Values{"code": {code}, "state": {q.Get("state")}}
		http.Redirect(w, r, e2eRedirectURI+"?"+back.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/oauth/v2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idp.mu.Lock()
		grant, ok := idp.codes[r.PostFormValue("code")]
		delete(idp.codes, r.PostFormValue("code")) // one-time use
		idp.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
		if !ok || r.PostFormValue("grant_type") != "authorization_code" ||
			r.PostFormValue("redirect_uri") != e2eRedirectURI ||
			r.PostFormValue("client_id") != e2eClientID ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		claims := handler.IDTokenClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    idp.srv.URL,
				Subject:   idp.sub,
				Audience:  jwt.ClaimStrings{e2eClientID},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
			Email:         idp.email,
			EmailVerified: true,
			Name:          "Login E2E User",
			Nonce:         grant.nonce,
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = handler.OIDCSharedKidForE2E
		signed, err := tok.SignedString(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "e2e-access", "refresh_token": "e2e-refresh",
			"id_token": signed, "token_type": "Bearer", "expires_in": 3600,
		})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

// e2eBrowser is an http.Client with a cookie jar whose requests to
// hub.lurus.cn are served in-process by the real router (as https) and whose
// requests to the IdP go over real loopback HTTP. It never auto-follows
// redirects: the test walks them so each hop can be asserted.
type e2eBrowser struct {
	t      *testing.T
	client *http.Client
	jar    http.CookieJar
}

type e2eTransport struct {
	engine *gin.Engine
}

func (tr e2eTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != e2eHubHost {
		return http.DefaultTransport.RoundTrip(req)
	}
	sreq := req.Clone(context.Background())
	sreq.RequestURI = req.URL.RequestURI()
	sreq.Host = req.URL.Host
	sreq.RemoteAddr = "192.0.2.10:40000"
	rec := httptest.NewRecorder()
	tr.engine.ServeHTTP(rec, sreq)
	res := rec.Result()
	res.Request = req
	return res, nil
}

func (b *e2eBrowser) get(rawURL string) *http.Response {
	b.t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		b.t.Fatalf("new request %s: %v", rawURL, err)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("GET %s: %v", rawURL, err)
	}
	return resp
}

// hasSessionCookie reports whether the jar would send a session cookie to
// hub.lurus.cn right now.
func (b *e2eBrowser) hasSessionCookie() bool {
	u, _ := url.Parse(e2eHubOrigin + "/")
	for _, c := range b.jar.Cookies(u) {
		if c.Name == e2eSessionName {
			return true
		}
	}
	return false
}

func bodyOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(raw)
}

func sessionSetCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == e2eSessionName {
			return c
		}
	}
	return nil
}

type e2eEnv struct {
	idp     *e2eIdP
	browser *e2eBrowser
	slug    string
}

// setupLoginE2E wires the whole stack. cookieDomain=nil leaves
// SESSION_COOKIE_DOMAIN unset (the code default); otherwise it is set to the
// pointed-to value ("" = production's host-only).
func setupLoginE2E(t *testing.T, cookieDomain *string) *e2eEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// SQLite DB, tenant and repo globals; the helper restores them itself.
	v2ctx := handler.SetupV2TestRouter(t)
	slug := v2ctx.TenantID // the helper uses one value for Tenant.Id and Slug

	// Production registers the tenant plugin at boot (repo/tenant_context.go);
	// the helper's DB does not, and without it a callback-created user lands in
	// tenant "default" instead of the tenant the login URL named.
	if err := repo.DB.Use(&repo.TenantPlugin{}); err != nil {
		t.Fatalf("register tenant plugin: %v", err)
	}

	idp := newE2EIdP(t)

	t.Setenv("GIN_MODE", "release")
	t.Setenv("SESSION_SECURE", "")
	if cookieDomain != nil {
		t.Setenv("SESSION_COOKIE_DOMAIN", *cookieDomain)
	} else {
		// Setenv first so the original value is restored, then really unset:
		// the code distinguishes "" from absent via LookupEnv.
		t.Setenv("SESSION_COOKIE_DOMAIN", "")
		_ = os.Unsetenv("SESSION_COOKIE_DOMAIN")
	}
	t.Setenv("OIDC_ENABLED", "true")
	t.Setenv("OIDC_ISSUER", idp.srv.URL)
	t.Setenv("OIDC_JWKS_URI", idp.srv.URL+"/jwks")
	t.Setenv("OIDC_CLIENT_ID", e2eClientID)
	t.Setenv("OIDC_CLIENT_SECRET", "")
	t.Setenv("OIDC_REDIRECT_URI", e2eRedirectURI)
	t.Setenv("OIDC_ENABLE_PKCE", "true")
	t.Setenv("OIDC_AUTO_CREATE_USER", "true")
	t.Setenv("OIDC_AUTO_CREATE_TENANT", "false")
	if err := middleware.InitOIDCAuth(); err != nil {
		t.Fatalf("InitOIDCAuth: %v", err)
	}

	// The callback asks the platform for the account behind the subject;
	// best-effort in production, so a 404 keeps this hermetic.
	identity := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(identity.Close)
	prevIdentity := common.IdentityServiceURL
	common.IdentityServiceURL = identity.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevIdentity })

	// Session store as the server bootstrap builds it: Redis store, shared key
	// prefix, options from SessionCookieBaseOptions + the 90-day MaxAge.
	mr := miniredis.RunT(t)
	store, err := sessionredis.NewStoreWithDB(10, "tcp", mr.Addr(), "", "", "0", []byte(common.SessionSecret))
	if err != nil {
		t.Fatalf("redis session store: %v", err)
	}
	if err := sessionredis.SetKeyPrefix(store, common.SessionStoreKeyPrefix); err != nil {
		t.Fatalf("session key prefix: %v", err)
	}
	opts := middleware.SessionCookieBaseOptions()
	opts.MaxAge = 7776000
	store.Options(opts)

	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(sessions.Sessions(e2eSessionName, store))
	router.SetApiV2Router(engine)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	b := &e2eBrowser{t: t, jar: jar, client: &http.Client{
		Jar:       jar,
		Transport: e2eTransport{engine: engine},
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	return &e2eEnv{idp: idp, browser: b, slug: slug}
}

// loginLeg walks login -> IdP authorize and returns the callback URL the IdP
// sent the browser back to.
func (e *e2eEnv) loginLeg() string {
	t := e.browser.t
	t.Helper()
	resp := e.browser.get(e2eHubOrigin + "/api/v2/" + e.slug + "/auth/login")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login = %d %s, want 302 to the IdP", resp.StatusCode, bodyOf(t, resp))
	}
	authz := resp.Header.Get("Location")
	if !strings.HasPrefix(authz, e.idp.srv.URL+"/oauth/v2/authorize?") {
		t.Fatalf("login redirected to %q, want the IdP authorize endpoint", authz)
	}
	resp = e.browser.get(authz)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize = %d %s, want 302 back to the app", resp.StatusCode, bodyOf(t, resp))
	}
	cb := resp.Header.Get("Location")
	if !strings.HasPrefix(cb, e2eRedirectURI+"?") {
		t.Fatalf("authorize redirected to %q, want OIDC_REDIRECT_URI", cb)
	}
	return cb
}

type e2eSessionInfo struct {
	Success   bool   `json:"success"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Data      struct {
		Id         int    `json:"id"`
		Username   string `json:"username"`
		TenantSlug string `json:"tenant_slug"`
		Email      string `json:"email"`
	} `json:"data"`
}

func (e *e2eEnv) getJSON(path string) (int, e2eSessionInfo) {
	t := e.browser.t
	t.Helper()
	resp := e.browser.get(e2eHubOrigin + path)
	raw := bodyOf(t, resp)
	var out e2eSessionInfo
	_ = json.Unmarshal([]byte(raw), &out)
	return resp.StatusCode, out
}

// The happy path with the production config: host-only Secure HttpOnly Lax
// cookie, accepted by the jar, then an authenticated v2 call as the right user.
func TestOIDCLoginE2E_FullFlow_HostOnlySecureCookieAuthenticatesV2(t *testing.T) {
	empty := ""
	e := setupLoginE2E(t, &empty)

	cb := e.loginLeg()
	resp := e.browser.get(cb)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d %s, want 302", resp.StatusCode, bodyOf(t, resp))
	}
	if loc := resp.Header.Get("Location"); loc != "/oauth/oidc" {
		t.Errorf("callback redirect = %q, want the SPA completion route /oauth/oidc", loc)
	}

	// Attributes a browser at https://hub.lurus.cn must see.
	c := sessionSetCookie(resp)
	if c == nil {
		t.Fatalf("callback set no %q cookie; Set-Cookie=%v", e2eSessionName, resp.Header.Values("Set-Cookie"))
	}
	if c.Domain != "" {
		t.Errorf("cookie Domain = %q, want host-only (SESSION_COOKIE_DOMAIN=\"\")", c.Domain)
	}
	if !c.Secure {
		t.Error("cookie is not Secure under GIN_MODE=release")
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	// Lax, not Strict: the callback is a cross-site top-level navigation from
	// the IdP and a Strict login-leg cookie would not reach it.
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("cookie Path = %q, want /", c.Path)
	}
	if c.MaxAge <= 0 {
		t.Errorf("cookie MaxAge = %d, want a persistent cookie", c.MaxAge)
	}
	if !e.browser.hasSessionCookie() {
		t.Fatal("the cookie jar (browser rules) refused the session cookie for https://hub.lurus.cn")
	}

	// Authenticated v2 calls with only the jar.
	code, info := e.getJSON("/api/v2/auth/session-info")
	if code != http.StatusOK || !info.Success {
		t.Fatalf("session-info = %d %+v, want 200 for the logged-in browser", code, info)
	}
	if info.Data.TenantSlug != e.slug {
		t.Errorf("session-info tenant_slug = %q, want %q", info.Data.TenantSlug, e.slug)
	}
	user, _, err := repo.GetUserByIDPSubject(e.idp.sub, e.slug)
	if err != nil || user == nil {
		t.Fatalf("callback did not provision the IdP subject: %v, %v", user, err)
	}
	if info.Data.Id != user.Id || user.Email != e.idp.email {
		t.Errorf("session-info id = %d, provisioned user = %d/%q; want the same account with the IdP email", info.Data.Id, user.Id, user.Email)
	}

	code, me := e.getJSON("/api/v2/" + e.slug + "/user/me")
	if code != http.StatusOK || !me.Success {
		t.Fatalf("user/me = %d %+v, want 200", code, me)
	}
	if me.Data.Id != user.Id || me.Data.Email != e.idp.email {
		t.Errorf("user/me = %+v, want the freshly logged-in user %d <%s>", me.Data, user.Id, e.idp.email)
	}
}

// A browser that never logged in must not be authenticated (guards the
// positive test against passing for the wrong reason).
func TestOIDCLoginE2E_NoLogin_SessionInfoRefuses(t *testing.T) {
	empty := ""
	e := setupLoginE2E(t, &empty)
	if code, _ := e.getJSON("/api/v2/auth/session-info"); code != http.StatusUnauthorized {
		t.Fatalf("session-info without a login = %d, want 401", code)
	}
	if code, _ := e.getJSON("/api/v2/" + e.slug + "/user/me"); code != http.StatusUnauthorized {
		t.Fatalf("user/me without a login = %d, want 401", code)
	}
}

// A state the app did not sign must be refused and must not mint a session.
func TestOIDCLoginE2E_TamperedState_Rejected(t *testing.T) {
	empty := ""
	e := setupLoginE2E(t, &empty)

	cb := e.loginLeg()
	u, _ := url.Parse(cb)
	q := u.Query()
	q.Set("state", q.Get("state")+"x")
	resp := e.browser.get(e2eRedirectURI + "?" + q.Encode())
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback with a tampered state = %d %s, want 400", resp.StatusCode, bodyOf(t, resp))
	}
	if code, _ := e.getJSON("/api/v2/auth/session-info"); code != http.StatusUnauthorized {
		t.Fatalf("session-info after a rejected callback = %d, want 401", code)
	}
}

// A well-formed code+state replayed in a browser that never started the login
// (no login-leg cookie, so no PKCE verifier or nonce) must be refused: this is
// login CSRF, where an attacker hands a victim their own authorization code.
func TestOIDCLoginE2E_CallbackWithoutLoginLeg_Rejected(t *testing.T) {
	empty := ""
	e := setupLoginE2E(t, &empty)

	cb := e.loginLeg()
	// A fresh browser: same app, empty jar.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	c := *e.browser.client
	c.Jar = jar
	e.browser = &e2eBrowser{t: t, jar: jar, client: &c}

	resp := e.browser.get(cb)
	if resp.StatusCode == http.StatusFound {
		t.Fatalf("callback without the login-leg cookie = 302, want a refusal; Set-Cookie=%v", resp.Header.Values("Set-Cookie"))
	}
	if code, _ := e.getJSON("/api/v2/auth/session-info"); code != http.StatusUnauthorized {
		t.Fatalf("session-info after a CSRF callback = %d, want 401", code)
	}
}

// The prior outage: SESSION_COOKIE_DOMAIN naming a different host. The server
// answers a normal 302 and a Set-Cookie, so nothing server-side errors — only
// a browser-faithful client shows the login is dead. This pins how it looks:
// the cookie carries the foreign Domain, the jar refuses it, the user is
// logged out. The app does not detect this at config time (see the lane
// report); this test makes the failure mode visible instead.
func TestOIDCLoginE2E_CookieDomainForAnotherHost_BrowserDropsSession(t *testing.T) {
	other := "test-newhub.lurus.cn"
	e := setupLoginE2E(t, &other)

	resp := e.browser.get(e2eHubOrigin + "/api/v2/" + e.slug + "/auth/login")
	c := sessionSetCookie(resp)
	if c == nil {
		t.Fatalf("login set no session cookie; Set-Cookie=%v", resp.Header.Values("Set-Cookie"))
	}
	if strings.TrimPrefix(c.Domain, ".") != other {
		t.Fatalf("cookie Domain = %q, want %q (the misconfiguration under test)", c.Domain, other)
	}
	if e.browser.hasSessionCookie() {
		t.Fatal("the jar accepted a session cookie scoped to another host; the browser model no longer reproduces the outage")
	}
	if code, _ := e.getJSON("/api/v2/auth/session-info"); code != http.StatusUnauthorized {
		t.Fatalf("session-info with a foreign-domain cookie = %d, want 401 (browser never stored it)", code)
	}
}

// The code default (SESSION_COOKIE_DOMAIN unset => ".lurus.cn") is valid for
// hub.lurus.cn, so the whole flow works there too.
func TestOIDCLoginE2E_DefaultCookieDomain_AcceptedAtHubHost(t *testing.T) {
	e := setupLoginE2E(t, nil)

	resp := e.browser.get(e.loginLeg())
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d %s, want 302", resp.StatusCode, bodyOf(t, resp))
	}
	c := sessionSetCookie(resp)
	if c == nil || strings.TrimPrefix(c.Domain, ".") != "lurus.cn" {
		t.Fatalf("session cookie = %+v, want Domain lurus.cn by default", c)
	}
	if code, info := e.getJSON("/api/v2/auth/session-info"); code != http.StatusOK || !info.Success {
		t.Fatalf("session-info = %d %+v, want 200 with the default cookie domain", code, info)
	}
}
