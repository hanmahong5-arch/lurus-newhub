package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// TestGlobalV2RateLimit_TripsAtBudgetAndIsOwnBucket drives the real
// GlobalV2RateLimit() middleware (no hand-built limiter): a budget of 2
// admits the first two /api/v2 calls from one IP, 429s the third with
// X-RateLimit-Limit reflecting that budget, and proves BOTH directions of
// "own bucket" — the SAME IP against /api/x (GlobalAPIRateLimit's "GA"
// bucket) is unaffected after /api/v2 trips, and a SECOND IP that exhausts
// /api/x first still gets a 200 from /api/v2. GlobalApiRateLimitNum is
// deliberately set to the SAME budget (2), not a generous one: if GV and GA
// ever shared a bucket key, the two admitted /api/v2 calls would already
// have exhausted a 2-budget GA bucket too, and this assertion would go red
// — a much higher GA budget would hide that (the shared bucket would still
// have plenty of headroom left), which is why it is pinned here rather than
// left at its default.
func TestGlobalV2RateLimit_TripsAtBudgetAndIsOwnBucket(t *testing.T) {
	prevRedis := common.RedisEnabled
	prevV2Enable := common.GlobalV2RateLimitEnable
	prevV2Num := common.GlobalV2RateLimitNum
	prevV2Dur := common.GlobalV2RateLimitDuration
	prevApiEnable := common.GlobalApiRateLimitEnable
	prevApiNum := common.GlobalApiRateLimitNum
	prevApiDur := common.GlobalApiRateLimitDuration
	common.RedisEnabled = false
	common.GlobalV2RateLimitEnable = true
	common.GlobalV2RateLimitNum = 2
	common.GlobalV2RateLimitDuration = 180
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum = 2
	common.GlobalApiRateLimitDuration = 180
	defer func() {
		common.RedisEnabled = prevRedis
		common.GlobalV2RateLimitEnable = prevV2Enable
		common.GlobalV2RateLimitNum = prevV2Num
		common.GlobalV2RateLimitDuration = prevV2Dur
		common.GlobalApiRateLimitEnable = prevApiEnable
		common.GlobalApiRateLimitNum = prevApiNum
		common.GlobalApiRateLimitDuration = prevApiDur
	}()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/x", GlobalV2RateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/x", GlobalAPIRateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	const ip = "203.0.113.221"
	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = ip + ":1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := do("/api/v2/x"); w.Code != http.StatusOK {
		t.Fatalf("1st v2 call = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w := do("/api/v2/x"); w.Code != http.StatusOK {
		t.Fatalf("2nd v2 call = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	w3 := do("/api/v2/x")
	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd v2 call = %d, want 429 (body=%s)", w3.Code, w3.Body.String())
	}
	if got := w3.Header().Get("X-RateLimit-Limit"); got != "2" {
		t.Fatalf("3rd v2 call X-RateLimit-Limit = %q, want %q", got, "2")
	}

	wApi := do("/api/x")
	if wApi.Code != http.StatusOK {
		t.Fatalf("/api/x from the same IP after /api/v2 tripped = %d, want 200 — GV and GA must not share a bucket", wApi.Code)
	}

	// Mirror direction, on a fresh IP so this half starts from a clean
	// bucket: exhaust /api/x (GA) at the budget first, then /api/v2 (GV)
	// from the SAME IP must still admit — a burst against GA must not trip
	// GV either.
	const mirrorIP = "203.0.113.231"
	doMirror := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = mirrorIP + ":1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := doMirror("/api/x"); w.Code != http.StatusOK {
		t.Fatalf("1st api call (mirror IP) = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w := doMirror("/api/x"); w.Code != http.StatusOK {
		t.Fatalf("2nd api call (mirror IP) = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w := doMirror("/api/x"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd api call (mirror IP) = %d, want 429 (body=%s) — GA budget must be tripped before the mirror assertion means anything", w.Code, w.Body.String())
	}
	if w := doMirror("/api/v2/x"); w.Code != http.StatusOK {
		t.Fatalf("/api/v2/x from the same IP after /api/x tripped = %d, want 200 — GA and GV must not share a bucket", w.Code)
	}
}

// TestGlobalV2RateLimit_DisabledIsPassThrough: with the flag off, the
// middleware never touches the limiter at all — five consecutive calls from
// one IP all admit.
func TestGlobalV2RateLimit_DisabledIsPassThrough(t *testing.T) {
	prev := common.GlobalV2RateLimitEnable
	common.GlobalV2RateLimitEnable = false
	defer func() { common.GlobalV2RateLimitEnable = prev }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/x", GlobalV2RateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/x", nil)
		req.RemoteAddr = "203.0.113.222:1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("call %d with GLOBAL_V2_RATE_LIMIT_ENABLE=false = %d, want 200", i, w.Code)
		}
	}
}

// TestGlobalV2RateLimit_ZeroLimitIsDisabled: GLOBAL_V2_RATE_LIMIT=0 (an
// operator following RELAY_MAX_CONCURRENT_PER_TENANT's "0 = unlimited"
// convention elsewhere in .env.example) must NOT behave like a budget of
// one request per window — it must disable the limiter entirely, the same
// as GLOBAL_V2_RATE_LIMIT_ENABLE=false. Five consecutive calls all admit.
func TestGlobalV2RateLimit_ZeroLimitIsDisabled(t *testing.T) {
	prevEnable := common.GlobalV2RateLimitEnable
	prevNum := common.GlobalV2RateLimitNum
	prevDur := common.GlobalV2RateLimitDuration
	common.GlobalV2RateLimitEnable = true
	common.GlobalV2RateLimitNum = 0
	// Duration must be a real window, not the package's unset zero value:
	// with duration==0 the underlying in-memory limiter's "now - oldest >=
	// duration" check is trivially true on every call, which would let this
	// test pass even with the num<=0 guard removed — masking exactly the
	// mutation this test exists to catch.
	common.GlobalV2RateLimitDuration = 180
	defer func() {
		common.GlobalV2RateLimitEnable = prevEnable
		common.GlobalV2RateLimitNum = prevNum
		common.GlobalV2RateLimitDuration = prevDur
	}()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/x", GlobalV2RateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/x", nil)
		req.RemoteAddr = "203.0.113.223:1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("call %d with GLOBAL_V2_RATE_LIMIT=0 = %d, want 200 (0 must disable, not mean a budget of one)", i, w.Code)
		}
	}
}

// TestGlobalV2RateLimit_RedisBranch_Enforces drives GlobalV2RateLimit()
// with common.RedisEnabled=true (miniredis-backed, via withMiniRedis — the
// same helper rate_limit_factory_cover_test.go uses for the sibling
// limiters) so the production Redis branch of this middleware, not just its
// in-memory fallback, is exercised by at least one test: budget 2 admits
// the first two /api/v2 calls from one IP, the third 429s.
func TestGlobalV2RateLimit_RedisBranch_Enforces(t *testing.T) {
	_, _, cleanup := withMiniRedis(t)
	defer cleanup()

	prevEnable := common.GlobalV2RateLimitEnable
	prevNum := common.GlobalV2RateLimitNum
	prevDur := common.GlobalV2RateLimitDuration
	common.GlobalV2RateLimitEnable = true
	common.GlobalV2RateLimitNum = 2
	common.GlobalV2RateLimitDuration = 180
	defer func() {
		common.GlobalV2RateLimitEnable = prevEnable
		common.GlobalV2RateLimitNum = prevNum
		common.GlobalV2RateLimitDuration = prevDur
	}()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/x", GlobalV2RateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/x", nil)
		req.RemoteAddr = "203.0.113.224:1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := do(); w.Code != http.StatusOK {
		t.Fatalf("1st call (redis branch) = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w := do(); w.Code != http.StatusOK {
		t.Fatalf("2nd call (redis branch) = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w := do(); w.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd call (redis branch) = %d, want 429 (body=%s)", w.Code, w.Body.String())
	}
}

// TestGlobalV2RateLimit_NonPositiveDurationAdmitsEverything pins the
// in-memory backend's behaviour for GLOBAL_V2_RATE_LIMIT_DURATION=0: every
// window is already expired, so the limiter admits every request. This is
// a behaviour pin, not a guard — there is no explicit check for it.
func TestGlobalV2RateLimit_NonPositiveDurationAdmitsEverything(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false // in-memory backend; an earlier test may leave the Redis flag on with no client
	prevEnable, prevNum, prevDur := common.GlobalV2RateLimitEnable, common.GlobalV2RateLimitNum, common.GlobalV2RateLimitDuration
	common.GlobalV2RateLimitEnable = true
	common.GlobalV2RateLimitNum = 1
	common.GlobalV2RateLimitDuration = 0
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.GlobalV2RateLimitEnable, common.GlobalV2RateLimitNum, common.GlobalV2RateLimitDuration = prevEnable, prevNum, prevDur
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/x", GlobalV2RateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/x", nil)
		req.RemoteAddr = "203.0.113.223:1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("call %d with GLOBAL_V2_RATE_LIMIT_DURATION=0 = %d, want 200 (every window already expired)", i, w.Code)
		}
	}
}
