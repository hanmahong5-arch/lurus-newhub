package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/web"
	"github.com/gin-gonic/gin"
)

// GlobalWebRateLimit budgets REQUESTS PER IP, and a cold first paint is not one
// request: index.html pulls the entry chunk plus its modulepreloads plus the
// stylesheets, and every lazily routed page adds another hashed chunk. With the
// limiter mounted above static.Serve, all of those counted against the same
// 60-per-180s budget the HTML document did, so one visitor opening the console
// on a cleared cache could 429 their own assets and get a white screen. That is
// what happened on the UAT instance on 2026-08-30; it was worked around there by
// raising the number to 600 rather than by scoping the limiter.
//
// The budget itself is unchanged, and the SPA document is still behind it. This
// pins the scope: hashed immutable assets are served outside it.
func TestSetWebRouter_StaticAssetsAreNotWebRateLimited(t *testing.T) {
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

	engine := gin.New()
	SetWebRouter(engine, web.BuildFS, web.IndexPage)

	// The memory limiter is a package-level store in middleware, keyed by
	// mark+client IP and shared with every other test in this binary. A client
	// IP nothing else uses keeps this budget to itself.
	const clientIP = "198.51.100.23:40000"
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = clientIP
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// Five asset requests against a budget of two. The name is deliberately one
	// no build produces, so the outcome is a 404 through RelayNotFound both here
	// and in CI (the Go jobs stub web/dist down to index.html) — what is being
	// asserted is the absence of a 429, not the presence of a file.
	for i := 1; i <= 5; i++ {
		w := get("/assets/cycle12-no-such-chunk-DEADBEEF.js")
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("asset request %d of 5 came back 429: the web rate limiter is still counting /assets/ requests (budget %d per %ds)",
				i, common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration)
		}
	}

	// ...while the SPA document keeps the limiter it has always had, and the
	// five asset requests above did not eat any of its budget.
	for i := 1; i <= 2; i++ {
		if w := get("/console/v2/dashboard"); w.Code != http.StatusOK {
			t.Fatalf("SPA document request %d of 2 should still be served (200), got %d", i, w.Code)
		}
	}
	if w := get("/console/v2/dashboard"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the third SPA document request should exhaust the budget of %d and return 429, got %d — the limiter is no longer mounted on the SPA path",
			common.GlobalWebRateLimitNum, w.Code)
	}
}
