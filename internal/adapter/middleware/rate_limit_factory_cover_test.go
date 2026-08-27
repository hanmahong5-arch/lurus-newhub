package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// rateLimitFactory's Redis branch is only taken when RedisEnabled is true; wire
// miniredis so the returned closure drives redisRateLimiter (not the in-memory
// path) and enforces the limit end-to-end.
func TestRateLimitFactory_RedisBranch_Enforces(t *testing.T) {
	_, _, cleanup := withMiniRedis(t)
	defer cleanup()

	f := rateLimitFactory(1, 3600, "FAC")
	run := func(ip string) int {
		r := gin.New()
		r.GET("/f", f, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		req := httptest.NewRequest(http.MethodGet, "/f", nil)
		req.RemoteAddr = ip + ":1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := run("198.51.100.5"); code != http.StatusOK {
		t.Fatalf("first = %d, want 200", code)
	}
	if code := run("198.51.100.5"); code != http.StatusTooManyRequests {
		t.Errorf("second = %d, want 429", code)
	}
}

// InternalApiRateLimit → keyedRateLimitFactory's Redis branch, keyed by the
// authenticated internal_api_key_id. A second call from the SAME key id (even
// from a different source IP) shares one bucket and is throttled, proving the
// P0-3 per-key bucketing rather than per-IP.
func TestInternalApiRateLimit_RedisBranch_PerKey(t *testing.T) {
	_, _, cleanup := withMiniRedis(t)
	defer cleanup()

	prevEnable := common.InternalApiRateLimitEnable
	common.InternalApiRateLimitEnable = true
	defer func() { common.InternalApiRateLimitEnable = prevEnable }()

	mw := InternalApiRateLimit(1, 3600, "IK1")
	run := func(keyID int, ip string) int {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("internal_api_key_id", keyID); c.Next() })
		r.GET("/i", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		req := httptest.NewRequest(http.MethodGet, "/i", nil)
		req.RemoteAddr = ip + ":1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := run(4242, "203.0.113.1"); code != http.StatusOK {
		t.Fatalf("first (key 4242) = %d, want 200", code)
	}
	// Same key id, different IP → same bucket → throttled.
	if code := run(4242, "203.0.113.99"); code != http.StatusTooManyRequests {
		t.Errorf("second (key 4242, new IP) = %d, want 429 (per-key bucket)", code)
	}
	// A different key id is unaffected.
	if code := run(4343, "203.0.113.99"); code != http.StatusOK {
		t.Errorf("other key 4343 = %d, want 200 (buckets isolate by key id)", code)
	}
}

// InternalApiRateLimit returns a no-op passthrough when the feature is disabled.
func TestInternalApiRateLimit_Disabled_Passthrough(t *testing.T) {
	prevEnable := common.InternalApiRateLimitEnable
	common.InternalApiRateLimitEnable = false
	defer func() { common.InternalApiRateLimitEnable = prevEnable }()

	r := gin.New()
	r.GET("/i", InternalApiRateLimit(1, 3600, "OFF"), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/i", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("disabled limiter must always pass, got %d on call %d", w.Code, i)
		}
	}
}

// TestRedisRateLimitHandler_SuccessCheckError_FailsOpen was
// TestRedisRateLimitHandler_SuccessCheckError_500 until operator decision D1
// (2026-08-27). The expectation was flipped, not deleted, and not because the
// production code was bent to match a failing test:
//
//   - What it used to assert (500) was reachable only through
//     setting.ModelRequestRateLimitEnabled, which shipped as `false`
//     (setting/rate_limit.go, `var ModelRequestRateLimitEnabled = false` before
//     this round). ModelRequestRateLimit (model-rate-limit.go:256) returns a
//     no-op passthrough when that flag is off, so redisRateLimitHandler's error
//     branch had never executed against live traffic. Flipping the expectation
//     therefore changes no behavior any caller has ever observed.
//   - D1 chose fail-OPEN so that a Redis hiccup degrades rate limiting instead
//     of turning every relay request into a 500, matching the sibling limiters
//     BusinessRateLimit and RelayConcurrencyLimit.
//
// This test seeds an unparseable timestamp rather than killing the connection,
// so it pins the half of the contract the sibling test in final_cover_test.go
// (TestModelRateLimit_Redis_CheckError_FailsOpen, which uses a dead Redis)
// does not reach: checkRedisRateLimit's time.Parse failure at
// model-rate-limit.go:92-95 returns a non-nil err from a perfectly healthy
// backend, and that STORED-DATA-CORRUPTION error takes the same fail-open
// branch and the same metric label as a connection error.
//
// Two assertions, so the degradation cannot become silent: the request is
// admitted (200), and metrics.RateLimitDegradedTotal{model_rate_limit_success}
// increments.
func TestRedisRateLimitHandler_SuccessCheckError_FailsOpen(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitCount = 0
	setting.ModelRequestRateLimitSuccessCount = 1
	defer func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
	}()

	const uid = 990301
	// Pre-seed the success key at capacity (len==1) with an unparseable entry so
	// checkRedisRateLimit reaches the time.Parse error branch (model-rate-limit.go:92-95).
	if err := rdb.LPush(context.Background(), "rateLimit:MRRLS:990301", "corrupt").Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("model_rate_limit_success"))
	code, _ := runModelRateLimitMR(uid)
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail-open on rate-limit check failure, operator decision D1)", code)
	}
	after := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("model_rate_limit_success"))
	if after != before+1 {
		t.Errorf("RateLimitDegradedTotal{model_rate_limit_success} = %v, want %v (a corrupt stored timestamp must be counted as a degradation, not swallowed)", after, before+1)
	}
}

// RootJWTAuth must reject a malformed Bearer token with 401 once OIDC is active.
func TestRootJWTAuth_InvalidToken_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	w := doBearer(mountJWT(RootJWTAuth()), "not.a.valid.jwt")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for malformed token; body=%s", w.Code, w.Body.String())
	}
}
