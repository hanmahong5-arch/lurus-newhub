package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// A single shared miniredis + go-redis client serves every limiter test.
// Standing up a fresh server+client per test spawns blocking-socket OS threads
// that, multiplied across ~14 cases under the (heavier) coverage-instrumented
// binary, exhaust the Windows/MSYS OS-thread budget and hard-abort the run.
// Reusing one instance (flushed between cases for isolation) keeps the resource
// footprint flat while still exercising the real Redis code paths.
var (
	sharedMR     *miniredis.Miniredis
	sharedRDB    *redis.Client
	sharedMROnce sync.Once
	sharedMRErr  error
)

func sharedMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	sharedMROnce.Do(func() {
		sharedMR, sharedMRErr = miniredis.Run()
		if sharedMRErr != nil {
			return
		}
		sharedRDB = redis.NewClient(&redis.Options{Addr: sharedMR.Addr()})
		sharedMRErr = sharedRDB.Ping(context.Background()).Err()
	})
	if sharedMRErr != nil {
		t.Fatalf("shared miniredis: %v", sharedMRErr)
	}
	return sharedMR, sharedRDB
}

// withMiniRedis installs the shared miniredis as common.RDB with
// RedisEnabled=true so every Redis-limiter branch actually executes (a nil /
// unreachable RDB would short-circuit and leave those branches uncovered). It
// returns the live *miniredis.Miniredis (for direct key-state assertions) and a
// cleanup that flushes all keys and restores the globals it mutated, keeping
// tests -count=1 safe and mutually isolated.
//
// The restore is registered TWICE on purpose (cycle-12 L1), and the second
// registration is the one that matters:
//
// `defer cleanup()` runs when the test function's body returns, which is
// BEFORE any t.Cleanup the test registered. Several tests here call an inner
// helper that saves/restores common.RedisEnabled through t.Cleanup —
// setModelRateLimit (limiter_jwks_extra_cover_test.go) is the live example —
// and that inner helper snapshots the global AFTER this one set it to true. So
// the sequence was: defer restores (RDB=nil, enabled=false), then the inner
// t.Cleanup puts enabled=true back while RDB stays nil. Every later test in the
// binary that reads the user cache then panicked on a nil client
// (common.RedisHGetObj -> RDB.HGetAll) and gin.Recovery turned it into a 500;
// under `go test -shuffle=on` that surfaced as
// TestAuthHelper_InvalidStatusType/InvalidRoleType getting 500 instead of 401.
//
// Registering the same restore on t.Cleanup here — first registration, so LIFO
// runs it LAST — makes the globals coherent at the end of the test no matter
// what an inner helper registered later. The returned closure is kept so the
// 27 existing `defer cleanup()` call sites read unchanged and so a test can
// still put the globals back mid-body; running it twice is harmless (both runs
// assign the same snapshot).
func withMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client, func()) {
	t.Helper()
	mr, rdb := sharedMiniRedis(t)
	mr.FlushAll()

	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	common.RDB = rdb
	common.RedisEnabled = true

	cleanup := func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
		mr.FlushAll()
	}
	t.Cleanup(cleanup)
	return mr, rdb, cleanup
}

// -------------------- redisRateLimiterKeyed enforcement + key state --------------------

func TestRedisRateLimiterKeyed_Enforcement_Miniredis(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	const mark, ident = "RLK", "clientA"
	key := "rateLimit:" + mark + ident

	// First request (limit=1) is admitted and pushes exactly one entry.
	c1, _ := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(c1, 1, 3600, mark, ident)
	if c1.IsAborted() {
		t.Fatalf("first request must pass under limit")
	}
	if got, _ := rdb.LLen(context.Background(), key).Result(); got != 1 {
		t.Fatalf("counter after first = %d, want 1 (INCR/LPush must have happened)", got)
	}

	// Second request within the window trips the limit exactly at limit+1.
	c2, w2 := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(c2, 1, 3600, mark, ident)
	if !c2.IsAborted() || c2.Writer.Status() != http.StatusTooManyRequests {
		t.Fatalf("second request aborted=%v status=%d, want abort 429", c2.IsAborted(), c2.Writer.Status())
	}
	// 429 must carry rate-limit headers with a positive Retry-After.
	if ra := w2.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q, want positive seconds", ra)
	}
	if lim := w2.Header().Get("X-RateLimit-Limit"); lim != "1" {
		t.Errorf("X-RateLimit-Limit = %q, want 1", lim)
	}
	if rem := w2.Header().Get("X-RateLimit-Remaining"); rem != "0" {
		t.Errorf("X-RateLimit-Remaining = %q, want 0", rem)
	}
	// The denied request must NOT have appended a new entry.
	if got, _ := rdb.LLen(context.Background(), key).Result(); got != 1 {
		t.Errorf("counter after denied request = %d, want still 1", got)
	}
}

// A burst from tenant/client A must not throttle a first request from client B:
// the two derive independent Redis keys.
func TestRedisRateLimiterKeyed_IsolatesByKey_Miniredis(t *testing.T) {
	_, _, cleanup := withMiniRedis(t)
	defer cleanup()

	// Exhaust clientA's budget of 1.
	cA1, _ := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(cA1, 1, 3600, "ISO", "tenantA")
	cA2, _ := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(cA2, 1, 3600, "ISO", "tenantA")
	if !cA2.IsAborted() {
		t.Fatalf("tenantA second request must be throttled")
	}

	// tenantB's first request must still pass despite tenantA being throttled.
	cB1, _ := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(cB1, 1, 3600, "ISO", "tenantB")
	if cB1.IsAborted() {
		t.Errorf("tenantB request throttled by tenantA burst — keys are not isolated")
	}
}

// redisRateLimiter derives the ident from ClientIP; drive the wrapper directly.
func TestRedisRateLimiter_ClientIP_Miniredis(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	c1, _ := newTestContext(http.MethodGet, "/x", "", "")
	c1.Request.RemoteAddr = "203.0.113.7:5555"
	redisRateLimiter(c1, 1, 3600, "IPM")
	if c1.IsAborted() {
		t.Fatalf("first ip request must pass")
	}
	if got, _ := rdb.LLen(context.Background(), "rateLimit:IPM203.0.113.7").Result(); got != 1 {
		t.Errorf("client-ip keyed counter = %d, want 1", got)
	}

	c2, _ := newTestContext(http.MethodGet, "/x", "", "")
	c2.Request.RemoteAddr = "203.0.113.7:5556"
	redisRateLimiter(c2, 1, 3600, "IPM")
	if !c2.IsAborted() || c2.Writer.Status() != http.StatusTooManyRequests {
		t.Errorf("second ip request status=%d, want 429", c2.Writer.Status())
	}
}

// When the window has already elapsed (duration=0), the limiter must admit the
// request via the LTrim/LPush refresh branch instead of denying.
func TestRedisRateLimiterKeyed_WindowExpired_Miniredis(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	const mark, ident = "EXP", "c"
	key := "rateLimit:" + mark + ident
	// Seed the list at capacity with a valid past timestamp.
	past := time.Now().Add(-time.Hour).Format(timeFormat)
	if err := rdb.LPush(context.Background(), key, past).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c, _ := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(c, 1, 0 /*duration*/, mark, ident)
	if c.IsAborted() {
		t.Errorf("expired-window request must be admitted, not throttled")
	}
}

// A closed backend must fail OPEN, not closed (cycle-11 L2 / operator
// decision D1's web/API half): the request is admitted, not 500+aborted,
// and the degradation is visible via the web_rate_limit_backend counter.
// This case owns a dedicated throwaway miniredis (it must close the
// server), leaving the shared instance untouched.
func TestRedisRateLimiterKeyed_BackendDown_FailsOpen_Miniredis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	common.RDB = rdb
	common.RedisEnabled = true
	defer func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
		_ = rdb.Close()
	}()
	mr.Close() // subsequent commands error out

	before := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("web_rate_limit_backend"))

	c, _ := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(c, 1, 3600, "DOWN", "c")
	if c.IsAborted() {
		t.Errorf("backend-down request must not be aborted (fail open), aborted=%v", c.IsAborted())
	}
	if c.Writer.Status() == http.StatusInternalServerError {
		t.Errorf("backend-down status = %d, must not be 500", c.Writer.Status())
	}

	after := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("web_rate_limit_backend"))
	if after != before+1 {
		t.Errorf("web_rate_limit_backend counter = %v, want %v (before %v + 1)", after, before+1, before)
	}
}

// A corrupt stored timestamp must fail OPEN and self-heal by deleting the
// unusable key, instead of 500-ing every future request for that ident
// forever.
func TestRedisRateLimiterKeyed_CorruptTimestamp_FailsOpenAndClearsKey_Miniredis(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	const mark, ident = "BAD", "c"
	key := "rateLimit:" + mark + ident
	if err := rdb.LPush(context.Background(), key, "not-a-timestamp").Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	before := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("web_rate_limit_corrupt"))

	c, _ := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(c, 1, 3600, mark, ident)
	if c.IsAborted() {
		t.Errorf("corrupt-timestamp request must not be aborted (fail open), aborted=%v", c.IsAborted())
	}
	if c.Writer.Status() == http.StatusInternalServerError {
		t.Errorf("corrupt-timestamp status = %d, must not be 500", c.Writer.Status())
	}

	if exists, _ := rdb.Exists(context.Background(), key).Result(); exists != 0 {
		t.Errorf("corrupt key must be deleted to self-heal, still exists (Exists=%d)", exists)
	}

	after := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("web_rate_limit_corrupt"))
	if after != before+1 {
		t.Errorf("web_rate_limit_corrupt counter = %v, want %v (before %v + 1)", after, before+1, before)
	}
}

// -------------------- checkRedisRateLimit / recordRedisRequest --------------------

func TestCheckRedisRateLimit_And_Record_Miniredis(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	ctx := context.Background()
	key := "rateLimit:MRRLS:unit"

	// maxCount==0 → unlimited, records nothing.
	if ok, err := checkRedisRateLimit(ctx, rdb, key, 0, 60); !ok || err != nil {
		t.Fatalf("maxCount=0 must always allow, got ok=%v err=%v", ok, err)
	}
	recordRedisRequest(ctx, rdb, key, 0)
	if got, _ := rdb.LLen(ctx, key).Result(); got != 0 {
		t.Fatalf("maxCount=0 must not record, LLen=%d", got)
	}

	// Empty key under limit 1 → allowed.
	if ok, err := checkRedisRateLimit(ctx, rdb, key, 1, 60); !ok || err != nil {
		t.Fatalf("under-limit check ok=%v err=%v, want allow", ok, err)
	}
	// Record one request; the list now holds exactly one entry.
	recordRedisRequest(ctx, rdb, key, 1)
	if got, _ := rdb.LLen(ctx, key).Result(); got != 1 {
		t.Fatalf("after record LLen=%d, want 1", got)
	}
	// At capacity within the window → denied.
	if ok, err := checkRedisRateLimit(ctx, rdb, key, 1, 60); ok || err != nil {
		t.Errorf("at-capacity check ok=%v err=%v, want deny (false,nil)", ok, err)
	}
}

func TestCheckRedisRateLimit_CorruptTimestamp_Miniredis(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()
	ctx := context.Background()
	key := "rateLimit:MRRLS:bad"
	if err := rdb.LPush(ctx, key, "garbage").Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := checkRedisRateLimit(ctx, rdb, key, 1, 60); err == nil {
		t.Errorf("corrupt timestamp must surface a parse error")
	}
}

// -------------------- ModelRequestRateLimit redis success-count path --------------------

func runModelRateLimitMR(uid int) (int, http.Header) {
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", uid); c.Next() })
	r.GET("/m", ModelRequestRateLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
	return w.Code, w.Header()
}

func TestModelRateLimit_Redis_SuccessCount_Miniredis(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitCount = 0 // skip token-bucket total path (shared singleton)
	setting.ModelRequestRateLimitSuccessCount = 1
	defer func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
	}()

	const uid = 990101
	// First success passes and is recorded.
	if code, _ := runModelRateLimitMR(uid); code != http.StatusOK {
		t.Fatalf("first = %d, want 200", code)
	}
	successKey := "rateLimit:MRRLS:990101"
	if got, _ := rdb.LLen(context.Background(), successKey).Result(); got != 1 {
		t.Fatalf("success counter after first = %d, want 1", got)
	}
	// Second trips the success-count limit → 429 with headers.
	code, hdr := runModelRateLimitMR(uid)
	if code != http.StatusTooManyRequests {
		t.Fatalf("second = %d, want 429 (success-count limit)", code)
	}
	if hdr.Get("X-RateLimit-Limit") != "1" {
		t.Errorf("X-RateLimit-Limit = %q, want 1", hdr.Get("X-RateLimit-Limit"))
	}
}

// Two distinct users must not share a success-count bucket.
func TestModelRateLimit_Redis_IsolatesByUser_Miniredis(t *testing.T) {
	_, _, cleanup := withMiniRedis(t)
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

	if code, _ := runModelRateLimitMR(990201); code != http.StatusOK {
		t.Fatalf("userA first = %d, want 200", code)
	}
	if code, _ := runModelRateLimitMR(990201); code != http.StatusTooManyRequests {
		t.Fatalf("userA second = %d, want 429", code)
	}
	// userB is unaffected.
	if code, _ := runModelRateLimitMR(990202); code != http.StatusOK {
		t.Errorf("userB first = %d, want 200 (buckets must isolate by user id)", code)
	}
}
