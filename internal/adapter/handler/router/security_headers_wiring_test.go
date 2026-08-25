package router

// End-to-end lock for the hardening response headers.
//
// middleware.SecurityHeaders has its own unit test next to the middleware,
// but that test cannot see the thing most likely to break: WHERE the
// engine-level Use() sits inside SetRouter. Gin snapshots a group's handler
// chain when Group() is called, so a router.Use() placed after
// SetApiRouter/SetRelayRouter/SetInternalApiRouter would compile, pass the
// middleware's own unit test, still set headers on engine-level routes like
// the SPA fallback — and silently set nothing on every grouped API and relay
// route. That is a whole-surface regression with no visible symptom.
//
// So these assertions go through the real SetRouter wiring, on routes that
// belong to three different groups plus the NoRoute fallback. Verified by
// mutation: moving `router.Use(middleware.SecurityHeaders())` in main.go to
// the line after SetInternalApiRouter(router) makes the grouped cases below
// fail while the SPA case still passes.
//
// Every route used here is picked to short-circuit before touching a
// database: TokenAuth rejects a missing Authorization header, InternalApiAuth
// rejects a missing X-API-Key, and the /api/* miss is answered by RelayNotFound.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/web"

	"github.com/gin-gonic/gin"
)

func securityHeadersEngine(t *testing.T) *gin.Engine {
	t.Helper()

	common.RedisEnabled = false
	prevMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = prevMaster })

	prevEnv, hadEnv := os.LookupEnv("FRONTEND_BASE_URL")
	_ = os.Unsetenv("FRONTEND_BASE_URL")
	t.Cleanup(func() {
		if hadEnv {
			_ = os.Setenv("FRONTEND_BASE_URL", prevEnv)
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRouter(engine, web.BuildFS, web.IndexPage)
	return engine
}

func TestSetRouter_SecurityHeaders_ReachEveryRouteGroup(t *testing.T) {
	engine := securityHeadersEngine(t)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"relay group (TokenAuth rejects, headers must already be set)", http.MethodGet, "/v1/models"},
		{"internal group (InternalApiAuth rejects)", http.MethodGet, "/internal/user/1"},
		{"dashboard group", http.MethodGet, "/dashboard/billing/subscription"},
		{"/api/* miss → RelayNotFound", http.MethodGet, "/api/this-route-does-not-exist"},
		{"SPA fallback (engine-level NoRoute)", http.MethodGet, "/some/spa/deep-link"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			for header, want := range map[string]string{
				"X-Content-Type-Options": "nosniff",
				"X-Frame-Options":        "DENY",
				"Referrer-Policy":        "strict-origin-when-cross-origin",
			} {
				if got := w.Header().Get(header); got != want {
					t.Errorf("%s %s: %s = %q, want %q (status was %d — the headers must be set regardless of how the request is answered)",
						tc.method, tc.path, header, got, want, w.Code)
				}
			}
		})
	}
}

// The relay routes stream: bytes start flowing the moment the handler runs, so
// a header set after c.Next() would be dropped by net/http as "too late". This
// pins the ordering choice inside SecurityHeaders itself — a rejected relay
// call still carries the headers, which is only possible if they were written
// before the handler chain ran.
func TestSetRouter_SecurityHeaders_SetBeforeHandlerWritesBody(t *testing.T) {
	engine := securityHeadersEngine(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Body.Len() == 0 {
		t.Fatal("expected the auth guard to write a rejection body — without a written body this test proves nothing about header ordering")
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q on a response whose body was already written, want %q", got, "nosniff")
	}
}

// HSTS must never be asserted to a plain-HTTP caller (local `go run`, no proxy)
// and must be asserted behind the R6 host nginx, which sets
// X-Forwarded-Proto: https on every request. Checked through the real router so
// that a future engine-level rewrite of the forwarded-proto handling shows up.
func TestSetRouter_SecurityHeaders_HSTSFollowsForwardedProto(t *testing.T) {
	engine := securityHeadersEngine(t)

	plain := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	plain.RemoteAddr = "127.0.0.1:1234"
	wPlain := httptest.NewRecorder()
	engine.ServeHTTP(wPlain, plain)
	if got := wPlain.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("plain HTTP request should not be told to force HTTPS, got %q", got)
	}

	proxied := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	proxied.RemoteAddr = "127.0.0.1:1234"
	proxied.Header.Set("X-Forwarded-Proto", "https")
	wProxied := httptest.NewRecorder()
	engine.ServeHTTP(wProxied, proxied)
	if got := wProxied.Header().Get("Strict-Transport-Security"); got != "max-age=15552000" {
		t.Errorf("request forwarded as https should carry HSTS, got %q", got)
	}
}
