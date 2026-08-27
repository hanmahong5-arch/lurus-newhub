package middleware

// r6a_rate_limit_failopen_test.go covers the two things operator decision D1
// (2026-08-27) requires beyond "fail open instead of 500": every fail-open
// occurrence must bump metrics.RateLimitDegradedTotal (unconditional, never
// throttled) and the paired log line must be throttled to at most once per
// minute per check so a sustained Redis outage doesn't turn into one ERROR
// line per relay request. TestModelRateLimit_Redis_CheckError_FailsOpen in
// final_cover_test.go already covers the success-count branch end-to-end
// through redisRateLimitHandler; this file adds the total-count branch (the
// other fail-open site) and isolates the throttle logic itself so a broken
// throttle (e.g. someone deletes the `< time.Minute` guard) fails a test
// instead of only showing up as log spam in production.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common/limiter"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// TestModelRateLimit_Redis_TotalCountError_FailsOpen is the total-count
// sibling of TestModelRateLimit_Redis_CheckError_FailsOpen: successMaxCount=0
// makes checkRedisRateLimit return (true, nil) immediately without touching
// Redis (model-rate-limit.go's maxCount==0 short-circuit), so the only call
// this test needs to force an error on is the token-bucket check behind the
// totalMaxCount>0 branch.
//
// This overrides r6aTokenBucketAllowFunc instead of pointing common.RDB at a
// dead client the way the success-count test does. Measured while writing
// this test: the dead-client approach is order-dependent and flaky in the
// full package run — limiter.New (internal/pkg/common/limiter/limiter.go:26)
// caches its *RedisLimiter behind a process-wide sync.Once that ignores the
// rdb argument after the first call, so whichever test in this package
// happens to reach the totalMaxCount>0 branch first (via a real/working
// Redis, e.g. final5_cover_test.go or middleware_cover_test.go, both of
// which sort before this file) permanently pins the singleton to that
// working client; a dead-client fixture in a later test can then no longer
// make this call fail. The seam sidesteps the singleton entirely.
func TestModelRateLimit_Redis_TotalCountError_FailsOpen(t *testing.T) {
	prevFunc := r6aTokenBucketAllowFunc
	defer func() { r6aTokenBucketAllowFunc = prevFunc }()
	r6aTokenBucketAllowFunc = func(ctx context.Context, rdb *redis.Client, key string, opts ...limiter.Option) (bool, error) {
		return false, errors.New("simulated token-bucket backend error")
	}

	before := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("model_rate_limit_total"))
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", 999124); c.Next() })
	r.GET("/m", redisRateLimitHandler(60, 5, 0), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail-open on total-count Redis error, operator decision D1)", w.Code)
	}
	after := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("model_rate_limit_total"))
	if after != before+1 {
		t.Errorf("RateLimitDegradedTotal{model_rate_limit_total} = %v, want %v (must increment on every fail-open, not just log)", after, before+1)
	}
}

// TestR6ARateLimitDegradedLogf_ThrottledPerMinutePerCheck isolates the
// throttle in r6aRateLimitDegradedLogf via the r6aRateLimitDegradedLogFunc
// seam, so it fails deterministically (not "log line count in a captured
// stdout buffer") if the per-minute suppression breaks. A unique checkName
// per run keeps this independent of the "success"/"total" keys the
// redisRateLimitHandler tests exercise on the same shared
// r6aRateLimitDegradedLogLast map.
func TestR6ARateLimitDegradedLogf_ThrottledPerMinutePerCheck(t *testing.T) {
	prevFunc := r6aRateLimitDegradedLogFunc
	defer func() { r6aRateLimitDegradedLogFunc = prevFunc }()
	var calls []string
	r6aRateLimitDegradedLogFunc = func(msg string) { calls = append(calls, msg) }

	checkName := "r6a-throttle-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	defer r6aRateLimitDegradedLogLast.Delete(checkName)

	r6aRateLimitDegradedLogf(checkName, "first")
	r6aRateLimitDegradedLogf(checkName, "second-within-window")
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 log call within the 1-minute throttle window, got %d: %v", len(calls), calls)
	}

	// Simulate the throttle window having elapsed (avoids a real 1-minute
	// sleep in the test).
	r6aRateLimitDegradedLogLast.Store(checkName, time.Now().Add(-2*time.Minute))
	r6aRateLimitDegradedLogf(checkName, "third-after-window")
	if len(calls) != 2 {
		t.Fatalf("expected a 2nd log call once the throttle window elapsed, got %d: %v", len(calls), calls)
	}
}
