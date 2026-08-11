package router

// cov_router-relay_wiring_test.go — business-acceptance tests for the route
// registration layer: which middleware chain each route group gets, and
// that unauthenticated/misrouted requests are rejected before reaching a
// handler. None of these need a real database: every path exercised here
// is rejected by an early-return auth guard (TokenAuth with an empty
// Authorization header short-circuits before any repo call; InternalApiAuth
// short-circuits on a missing X-API-Key header; metricsAuthMiddleware is a
// pure IP check) — so a stubbed-out handler could never make these tests
// pass, only a genuinely wired guard can.
//
// Self-check performed while writing these: replacing SetInternalApiRouter/
// SetRelayRouter/SetVideoRouter/SetDashboardRouter/SetRouter's bodies with a
// no-op (register nothing) makes every test below fail — either the route
// stops existing (404 instead of 401/403) or /metrics never gets its IP
// guard (200 instead of 403 for a public RemoteAddr).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/web"

	"github.com/glebarez/sqlite"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func routerRelayHasRoute(engine *gin.Engine, method, path string) bool {
	for _, rt := range engine.Routes() {
		if rt.Method == method && rt.Path == path {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// metricsAuthMiddleware: pure IP-classification guard for /metrics.
// ---------------------------------------------------------------------

func TestMetricsAuthMiddleware_IPClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		remoteAddr string
		wantBlock  bool
	}{
		{"loopback IPv4 allowed", "127.0.0.1:54321", false},
		{"loopback IPv6 allowed", "[::1]:54321", false},
		{"RFC1918 10.x allowed (in-cluster pod CIDR)", "10.42.0.7:9100", false},
		{"RFC1918 172.16.x allowed", "172.16.5.5:9100", false},
		{"RFC1918 192.168.x allowed", "192.168.1.50:9100", false},
		{"public internet IP blocked", "8.8.8.8:443", true},
		{"another public IP blocked", "203.0.113.9:12345", true},
		{"no port in RemoteAddr falls back to bare host (public → blocked)", "8.8.8.8", true},
		{"no port in RemoteAddr falls back to bare host (loopback → allowed)", "127.0.0.1", false},
		{"unparseable RemoteAddr blocked (fails open to deny, not allow)", "not-an-ip:1234", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
			c.Request.RemoteAddr = tc.remoteAddr

			metricsAuthMiddleware()(c)

			blocked := c.IsAborted() && rec.Code == http.StatusForbidden
			if blocked != tc.wantBlock {
				t.Fatalf("RemoteAddr=%q: expected blocked=%v, got blocked=%v (code=%d, aborted=%v)",
					tc.remoteAddr, tc.wantBlock, blocked, rec.Code, c.IsAborted())
			}
			if !tc.wantBlock && c.IsAborted() {
				t.Fatalf("RemoteAddr=%q: legitimate scraper request was aborted", tc.remoteAddr)
			}
		})
	}
}

// ---------------------------------------------------------------------
// SetRouter: end-to-end wiring — /metrics gets the IP guard, and the
// catch-all NoRoute behavior differs by FRONTEND_BASE_URL / IsMasterNode.
// ---------------------------------------------------------------------

func TestSetRouter_MetricsEndpoint_RejectsPublicScraper(t *testing.T) {
	common.RedisEnabled = false
	prevMaster := common.IsMasterNode
	common.IsMasterNode = false
	defer func() { common.IsMasterNode = prevMaster }()
	prevEnv, hadEnv := os.LookupEnv("FRONTEND_BASE_URL")
	_ = os.Unsetenv("FRONTEND_BASE_URL")
	defer func() {
		if hadEnv {
			_ = os.Setenv("FRONTEND_BASE_URL", prevEnv)
		}
	}()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRouter(engine, web.BuildFS, web.IndexPage)

	if !routerRelayHasRoute(engine, http.MethodGet, "/metrics") {
		t.Fatal("expected GET /metrics to be registered by SetRouter")
	}

	// A public-internet scraper must be rejected before reaching promhttp's
	// handler — proves metricsAuthMiddleware is actually wired on the route,
	// not merely defined.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "8.8.8.8:443"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected public scraper rejected with 403, got %d body=%s", w.Code, w.Body.String())
	}

	// A loopback scraper (in-cluster Prometheus-equivalent) must be admitted
	// and see real Prometheus exposition output.
	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req2.RemoteAddr = "127.0.0.1:9100"
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected loopback scraper admitted with 200, got %d", w2.Code)
	}
}

// SPA fallback: with no FRONTEND_BASE_URL configured, an unknown non-API path
// must serve the embedded index.html (client-side routing), while an unknown
// path under /api must fall through to the JSON "not found" relay handler
// instead of HTML — this is the exact distinction web-router.go's NoRoute
// switch encodes, and a caller (e.g. an SDK doing REST error handling) that
// gets HTML back for a bad /api/ call would silently break on JSON parsing.
func TestSetRouter_NoRoute_SPAFallbackVsAPINotFound(t *testing.T) {
	common.RedisEnabled = false
	prevMaster := common.IsMasterNode
	common.IsMasterNode = false
	defer func() { common.IsMasterNode = prevMaster }()
	prevEnv, hadEnv := os.LookupEnv("FRONTEND_BASE_URL")
	_ = os.Unsetenv("FRONTEND_BASE_URL")
	defer func() {
		if hadEnv {
			_ = os.Setenv("FRONTEND_BASE_URL", prevEnv)
		}
	}()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRouter(engine, web.BuildFS, web.IndexPage)

	// Unknown SPA route (e.g. a client-side /dashboard/foo path) → HTML shell.
	req := httptest.NewRequest(http.MethodGet, "/some/spa/deep-link", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected SPA fallback to serve 200 index.html, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct == "" || ct[:9] != "text/html" {
		t.Fatalf("expected SPA fallback Content-Type text/html, got %q", ct)
	}

	// Unknown /api path → JSON not-found from RelayNotFound, not the SPA shell.
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/this-route-does-not-exist", nil)
	reqAPI.RemoteAddr = "127.0.0.1:1234"
	wAPI := httptest.NewRecorder()
	engine.ServeHTTP(wAPI, reqAPI)
	apiCT := wAPI.Header().Get("Content-Type")
	if apiCT != "" && len(apiCT) >= 9 && apiCT[:9] == "text/html" {
		t.Fatalf("expected /api/* miss to return JSON not-found, got HTML shell (body=%s)", wAPI.Body.String())
	}
}

// When FRONTEND_BASE_URL is set on a non-master node, SetRouter must skip
// the embedded SPA entirely and 301-redirect every unmatched path to the
// external frontend, preserving the original request URI.
func TestSetRouter_FrontendBaseURL_RedirectsUnmatchedPaths(t *testing.T) {
	common.RedisEnabled = false
	prevMaster := common.IsMasterNode
	common.IsMasterNode = false
	defer func() { common.IsMasterNode = prevMaster }()
	prevEnv, hadEnv := os.LookupEnv("FRONTEND_BASE_URL")
	_ = os.Setenv("FRONTEND_BASE_URL", "https://external-frontend.example.com/")
	defer func() {
		if hadEnv {
			_ = os.Setenv("FRONTEND_BASE_URL", prevEnv)
		} else {
			_ = os.Unsetenv("FRONTEND_BASE_URL")
		}
	}()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRouter(engine, web.BuildFS, web.IndexPage)

	req := httptest.NewRequest(http.MethodGet, "/some/unmatched/path?x=1", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301 redirect to external frontend, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	want := "https://external-frontend.example.com/some/unmatched/path?x=1"
	if loc != want {
		t.Fatalf("expected redirect Location %q (trailing slash trimmed + original URI preserved), got %q", want, loc)
	}
}

// On the master node, FRONTEND_BASE_URL is a foot-gun (a master serving a
// stale/incompatible frontend build) and must be ignored in favor of the
// embedded SPA — this is the exact guard at the top of SetRouter.
func TestSetRouter_FrontendBaseURL_IgnoredOnMasterNode(t *testing.T) {
	common.RedisEnabled = false
	prevMaster := common.IsMasterNode
	common.IsMasterNode = true
	defer func() { common.IsMasterNode = prevMaster }()
	prevEnv, hadEnv := os.LookupEnv("FRONTEND_BASE_URL")
	_ = os.Setenv("FRONTEND_BASE_URL", "https://external-frontend.example.com/")
	defer func() {
		if hadEnv {
			_ = os.Setenv("FRONTEND_BASE_URL", prevEnv)
		} else {
			_ = os.Unsetenv("FRONTEND_BASE_URL")
		}
	}()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRouter(engine, web.BuildFS, web.IndexPage)

	req := httptest.NewRequest(http.MethodGet, "/some/unmatched/path", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code == http.StatusMovedPermanently {
		t.Fatalf("master node must ignore FRONTEND_BASE_URL and serve the embedded SPA, but got a redirect to %q",
			w.Header().Get("Location"))
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected master node to serve embedded SPA (200), got %d", w.Code)
	}
}

// ---------------------------------------------------------------------
// SetInternalApiRouter: service-to-service surface, X-API-Key gated.
// ---------------------------------------------------------------------

func TestSetInternalApiRouter_MissingAPIKey_Rejected(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetInternalApiRouter(engine)

	// Representative sample across every scope group registered by
	// SetInternalApiRouter — read, write, delete, quota, balance, currency,
	// log, model-catalog, admin, provisioning, pool-fund, privacy.
	routes := []struct{ method, path string }{
		{http.MethodGet, "/internal/user/1"},
		{http.MethodPost, "/internal/user"},
		{http.MethodDelete, "/internal/user/1"},
		{http.MethodGet, "/internal/token/1"},
		{http.MethodPost, "/internal/token"},
		{http.MethodGet, "/internal/quota/user/1"},
		{http.MethodPost, "/internal/quota/adjust"},
		{http.MethodGet, "/internal/balance/user/1"},
		{http.MethodPost, "/internal/balance/topup"},
		{http.MethodGet, "/internal/currency/info"},
		{http.MethodPost, "/internal/currency/exchange"},
		{http.MethodGet, "/internal/log/user/1"},
		{http.MethodGet, "/internal/models/catalog"},
		{http.MethodPost, "/internal/admin/backfill-token-accounts"},
		{http.MethodPost, "/internal/v1/provisioning/tenants/acme/keys"},
		{http.MethodPost, "/internal/v1/provisioning/tenants/acme/credit-pool/fund"},
		{http.MethodPost, "/internal/v1/privacy/erase"},
	}

	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401 without X-API-Key, got %d body=%s", r.method, r.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "API key required") {
			t.Errorf("%s %s: expected \"API key required\" message, got %s", r.method, r.path, w.Body.String())
		}
	}
}

var routerRelayInternalKeyDBCounter atomic.Int64

// A bogus API key (well-formed header, but not a real key) must be rejected
// distinctly from the missing-header case (both 401, but this path proves
// repo.ValidateInternalApiKey's failure is wired to abort, not silently
// admit). Needs a real (in-memory) DB behind repo.DB because
// ValidateInternalApiKey issues a real query once a non-empty key reaches it.
func TestSetInternalApiRouter_InvalidAPIKey_Rejected(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:internal_api_key_test_%d?mode=memory&cache=shared", routerRelayInternalKeyDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.InternalApiKey{}); err != nil {
		t.Fatalf("migrate InternalApiKey: %v", err)
	}
	prevDB := repo.DB
	repo.DB = db
	defer func() {
		repo.DB = prevDB
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	engine := gin.New()
	SetInternalApiRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/internal/user/1", nil)
	req.Header.Set("X-API-Key", "lurus_ik_this_key_does_not_exist_in_any_db")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a well-formed but unregistered API key, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Invalid or expired API key") {
		t.Fatalf("expected \"Invalid or expired API key\" message, got %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------
// SetDashboardRouter: legacy dashboard billing endpoints, TokenAuth gated.
// ---------------------------------------------------------------------

func TestSetDashboardRouter_NoToken_Rejected(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetDashboardRouter(engine)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/dashboard/billing/subscription"},
		{http.MethodGet, "/v1/dashboard/billing/subscription"},
		{http.MethodGet, "/dashboard/billing/usage"},
		{http.MethodGet, "/v1/dashboard/billing/usage"},
	}
	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401 without a token, got %d body=%s", r.method, r.path, w.Code, w.Body.String())
		}
	}
}

// ---------------------------------------------------------------------
// SetRelayRouter / SetVideoRouter: the money-path relay surface. Every
// route must reject an unauthenticated caller before it can reach the
// billing/upstream-dispatch logic. Also exercises registerMjRouterGroup
// (invoked twice by SetRelayRouter, for /mj and /:mode/mj).
// ---------------------------------------------------------------------

func TestSetRelayRouter_NoToken_Rejected(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1/models"},
		{http.MethodGet, "/v1/models/gpt-4"},
		{http.MethodGet, "/v1beta/models"},
		{http.MethodGet, "/v1beta/openai/models"},
		{http.MethodGet, "/v1/billing/balance"},
		{http.MethodPost, "/v1/messages"},
		{http.MethodPost, "/v1/messages/count_tokens"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/v1/completions"},
		{http.MethodPost, "/v1/responses"},
		{http.MethodPost, "/v1/images/generations"},
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodPost, "/v1/audio/transcriptions"},
		{http.MethodPost, "/v1/rerank"},
		{http.MethodPost, "/v1/moderations"},
		{http.MethodGet, "/v1/realtime"},
		{http.MethodPost, "/suno/fetch"},
		{http.MethodGet, "/suno/fetch/task-123"},
		{http.MethodPost, "/v1/audio/music"},
		{http.MethodPost, "/v1beta/models/gemini-pro:generateContent"},
		// mj registered twice (registerMjRouterGroup called for both groups)
		{http.MethodPost, "/mj/submit/imagine"},
		{http.MethodGet, "/mj/image/abc123"},
		{http.MethodPost, "/turbo/mj/submit/imagine"},
	}
	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401 without a token (relay money-path must never admit an anonymous caller), got %d body=%s",
				r.method, r.path, w.Code, w.Body.String())
		}
	}
}

// A registered path hit with a method that was never wired must not be
// silently accepted; this repo's routers use gin.New() without
// HandleMethodNotAllowed enabled, so a method mismatch on a real relay path
// falls through to the generic NoRoute handler (404) rather than a 405 —
// pinning this actual behavior so a future flip to 405 (or to a handler
// that starts accepting GET) is a deliberate, reviewed change.
func TestSetRelayRouter_MethodMismatch_Returns404NotHandlerLeak(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	// /v1/chat/completions is POST-only; GET was never registered.
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected GET on a POST-only relay path to fall through to gin's default 404 (no HandleMethodNotAllowed configured), got %d body=%s",
			w.Code, w.Body.String())
	}
}

// A completely unregistered relay-shaped path must 404, not fall into any
// handler (proves the router isn't accidentally wildcard-catching).
func TestSetRelayRouter_UnknownPath_404(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodPost, "/v1/this-endpoint-was-never-registered", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unregistered relay path, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------
// SetVideoRouter: video/kling/jimeng task surfaces, TokenAuth gated.
// ---------------------------------------------------------------------

func TestSetVideoRouter_NoToken_Rejected(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetVideoRouter(engine)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1/videos/task-1/content"},
		{http.MethodPost, "/v1/video/generations"},
		{http.MethodGet, "/v1/video/generations/task-1"},
		{http.MethodPost, "/v1/videos/vid-1/remix"},
		{http.MethodPost, "/v1/videos"},
		{http.MethodGet, "/v1/videos/task-1"},
	}
	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401 without a token, got %d body=%s", r.method, r.path, w.Code, w.Body.String())
		}
	}
}

// Kling routes run KlingRequestConvert() BEFORE TokenAuth: a well-formed
// JSON body with no model still reaches the request-conversion step, but an
// unauthenticated caller must still ultimately be rejected by TokenAuth
// further down the chain, not admitted because the adapter middleware ran
// first.
func TestSetVideoRouter_KlingRoute_NoToken_StillRejected(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetVideoRouter(engine)

	req := httptest.NewRequest(http.MethodPost, "/kling/v1/videos/text2video",
		httptest.NewRequest(http.MethodPost, "/x", nil).Body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected kling video route to still reject an unauthenticated caller (401), got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------
// SetWebRouter: SPA static-file + NoRoute fallback.
// ---------------------------------------------------------------------

func TestSetWebRouter_ServesEmbeddedIndexForUnknownPath(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetWebRouter(engine, web.BuildFS, web.IndexPage)

	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected embedded SPA index served for unknown non-API path, got %d", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Fatal("expected non-empty index.html body")
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected Cache-Control: no-cache on the SPA fallback response, got %q", w.Header().Get("Cache-Control"))
	}
}

func TestSetWebRouter_APIPathMiss_UsesRelayNotFoundNotHTML(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetWebRouter(engine, web.BuildFS, web.IndexPage)

	req := httptest.NewRequest(http.MethodGet, "/api/definitely-not-a-real-endpoint", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	// RelayNotFound must have produced a JSON body distinct from the SPA
	// index.html shell (which starts with "<" for a real Vite build).
	body := w.Body.String()
	if len(body) > 0 && body[0] == '<' {
		t.Fatalf("expected /api/* 404 miss to go through RelayNotFound (JSON), got HTML shell: %s", body)
	}
}
