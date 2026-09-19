package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/web"
	"github.com/gin-gonic/gin"
)

// budgetOfTwo arms the in-process web rate limiter with a budget small enough
// that a handful of requests can exhaust it, and restores whatever the rest of
// the binary had set.
func budgetOfTwo(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	prevEnable := common.GlobalWebRateLimitEnable
	prevNum := common.GlobalWebRateLimitNum
	prevDuration := common.GlobalWebRateLimitDuration
	prevRedis := common.RedisEnabled
	t.Cleanup(func() {
		common.GlobalWebRateLimitEnable = prevEnable
		common.GlobalWebRateLimitNum = prevNum
		common.GlobalWebRateLimitDuration = prevDuration
		common.RedisEnabled = prevRedis
	})
	// No Redis in a unit test: rateLimitFactory then hands out the in-process
	// memory limiter, which counts exactly the same way.
	common.RedisEnabled = false
	common.GlobalWebRateLimitEnable = true
	common.GlobalWebRateLimitNum = 2
	common.GlobalWebRateLimitDuration = 180
}

// getFrom issues a GET as one fixed client IP. The memory limiter is a
// package-level store in middleware, keyed by mark+client IP and shared with
// every other test in this binary, so each test here uses an IP nothing else
// uses and keeps its budget to itself.
func getFrom(engine *gin.Engine, clientIP, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = clientIP
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// GlobalWebRateLimit budgets REQUESTS PER IP, and a cold first paint is not one
// request: index.html pulls the entry chunk plus its modulepreloads plus the
// stylesheets plus the favicon, and every lazily routed page adds another hashed
// chunk. With the limiter mounted on the engine above static.Serve, all of those
// counted against the same 60-per-180s budget the HTML document did, so one
// visitor opening the console on a cleared cache could 429 their own assets and
// get a white screen. That is what happened on the UAT instance on 2026-08-30;
// it was worked around there by raising the number to 600 rather than by scoping
// the limiter. An enterprise office shares one NAT egress IP, which is the same
// arithmetic for a whole company.
//
// The budget itself is unchanged, and the SPA document is still behind it. This
// pins the scope against the real embedded build output.
func TestSetWebRouter_StaticAssetsAreNotWebRateLimited(t *testing.T) {
	budgetOfTwo(t)

	engine := gin.New()
	SetWebRouter(engine, web.BuildFS, web.IndexPage)

	const clientIP = "198.51.100.23:40000"

	// Five asset requests against a budget of two. The name is deliberately one
	// no build produces, so the outcome is a 404 through RelayNotFound both here
	// and in CI (the Go jobs stub web/dist down to index.html) — what is being
	// asserted is the absence of a 429, not the presence of a file.
	for i := 1; i <= 5; i++ {
		w := getFrom(engine, clientIP, "/assets/cycle12-no-such-chunk-DEADBEEF.js")
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("asset request %d of 5 came back 429: the web rate limiter is still counting /assets/ requests (budget %d per %ds)",
				i, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration)
		}
	}

	// ...while the SPA document keeps the limiter it has always had, and the
	// five asset requests above did not eat any of its budget.
	for i := 1; i <= 2; i++ {
		if w := getFrom(engine, clientIP, "/console/v2/dashboard"); w.Code != http.StatusOK {
			t.Fatalf("SPA document request %d of 2 should still be served (200), got %d", i, w.Code)
		}
	}
	if w := getFrom(engine, clientIP, "/console/v2/dashboard"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the third SPA document request should exhaust the budget of %d and return 429, got %d — the limiter is no longer mounted on the SPA path",
			common.GlobalWebRateLimitNum, w.Code)
	}
}

// The /assets/ prefix is not the boundary; "can static.Serve answer it out of
// the embedded build" is. web/dist/index.html references /logo.png at its
// dist root (line 5), and the manifest icons, robots.txt and favicon live there
// too — none of them under /assets/. While the limiter was scoped by URL prefix
// those files still consumed budget, so a cold first paint cost two tokens
// rather than one and the arithmetic for an office behind a single NAT address
// stayed twice as bad as it looked.
//
// This drives the real mounted chain over a synthetic dist, because the Go CI
// jobs stub web/dist down to a single index.html and a dist-root file has to
// actually exist for static.Serve to answer it.
func TestSetWebRoutes_DistRootFilesAreNotWebRateLimited(t *testing.T) {
	budgetOfTwo(t)

	dist := fstest.MapFS{
		"index.html":   {Data: []byte("<!doctype html><title>spa</title>")},
		"logo.png":     {Data: []byte("\x89PNG\r\n\x1a\n not really a png")},
		"robots.txt":   {Data: []byte("User-agent: *\n")},
		"assets/x.js":  {Data: []byte("console.log('cycle12');")},
		"assets/x.css": {Data: []byte(".c12{color:red}")},
	}
	engine := gin.New()
	setWebRoutes(engine, dist, mapServeFS{http.FS(dist)}, []byte("<!doctype html><title>spa</title>"))

	const clientIP = "198.51.100.24:40000"

	// Budget is 2. Every one of these is a file static.Serve answers, and none
	// of them is under /assets/.
	for _, path := range []string{"/logo.png", "/robots.txt", "/logo.png", "/robots.txt", "/logo.png"} {
		w := getFrom(engine, clientIP, path)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("GET %s came back 429: a dist-root static file is still counted against the SPA document budget (%d per %ds)",
				path, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 from static.Serve", path, w.Code)
		}
	}
	for i := 1; i <= 3; i++ {
		if w := getFrom(engine, clientIP, "/assets/x.js"); w.Code != http.StatusOK {
			t.Fatalf("hashed asset request %d of 3 = %d, want 200", i, w.Code)
		}
	}

	// The document is what the budget is for, and none of the eight requests
	// above spent any of it.
	for i := 1; i <= 2; i++ {
		if w := getFrom(engine, clientIP, "/console/v2/dashboard"); w.Code != http.StatusOK {
			t.Fatalf("SPA document request %d of 2 = %d, want 200", i, w.Code)
		}
	}
	refused := getFrom(engine, clientIP, "/console/v2/dashboard")
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the third SPA document request should exhaust the budget of %d and return 429, got %d — the limiter is no longer bound to the SPA document",
			common.GlobalWebRateLimitNum, refused.Code)
	}
	// Binding the limiter inside NoRoute puts it AFTER middleware.Cache(),
	// which has already written max-age=604800 for every path but "/". A 429
	// carrying a week of explicit freshness is cacheable regardless of its
	// status code (RFC 9111 §3), so a shared cache in front of a NAT-ed office
	// could answer the whole building with a stored refusal.
	if got := refused.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control on the 429 = %q, want no-cache: a refusal must not be storable", got)
	}
}
