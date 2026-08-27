package middleware

// r5e_model_rate_limit_default_test.go — lane L-E (2026-08-26 UAT G6: "out of
// the box there is no request-rate ceiling on the relay at all").
//
// This does NOT re-test the limiter arithmetic already covered by
// redis_miniredis_cover_test.go's ModelRequestRateLimit_Redis_* cases. It pins
// the two things that were actually wrong:
//   - the DEFAULT of setting.ModelRequestRateLimitEnabled (a value, not
//     behavior a unit test of the limiter function could ever catch);
//   - that the switch, wired the same way relay-router.go mounts it, actually
//     gates real requests end to end (a "wiring" test per the lane brief,
//     not a direct call into a helper);
//   - that a Redis backend error under an ARMED switch fails OPEN instead of
//     500ing every relay request.
//
// Every test here mutates package-level settings.ModelRequestRateLimit* /
// common.RedisEnabled / common.RDB; each save/restores via defer, so the
// file is -count=1 safe and order-independent. Distinct, unlikely-elsewhere
// user ids are used per case because the memory backend's inMemoryRateLimiter
// (cover_helpers_test.go) is a single package-level map shared with every
// other memory-backed limiter test in this package.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// r5eRunModelRateLimit drives one request through a gin engine wired exactly
// like relay-router.go mounts ModelRequestRateLimit(): the middleware first,
// c.Set("id", uid) upstream of it (TokenAuth's job in production), then a
// handler that records whether it actually ran.
func r5eRunModelRateLimit(uid int) (status int, hdr http.Header, reachedHandler bool) {
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", uid); c.Next() })
	r.Use(ModelRequestRateLimit())
	r.GET("/m", func(c *gin.Context) {
		reachedHandler = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
	return w.Code, w.Header(), reachedHandler
}

// (a) Default-state lock: the entire G6 defect was a default value, so pin
// the defaults directly. ModelRequestRateLimitEnabled must be true (armed)
// and ModelRequestRateLimitCount must stay 0 (total-request dimension
// remains opt-in — see the comment on rate_limit.go:12).
func TestR5e_ModelRequestRateLimit_DefaultsArmedButNarrow(t *testing.T) {
	if !setting.ModelRequestRateLimitEnabled {
		t.Fatal("setting.ModelRequestRateLimitEnabled default = false — the relay has no request-rate ceiling at all (G6)")
	}
	if setting.ModelRequestRateLimitCount != 0 {
		t.Fatalf("setting.ModelRequestRateLimitCount default = %d, want 0 (total-request dimension must stay opt-in, not a new default nobody chose)", setting.ModelRequestRateLimitCount)
	}
	if setting.ModelRequestRateLimitSuccessCount != 1000 {
		t.Fatalf("setting.ModelRequestRateLimitSuccessCount default = %d, want 1000 (the wide ceiling the lane was scoped to ship)", setting.ModelRequestRateLimitSuccessCount)
	}
}

// (b) Wiring test: the switch itself must be what gates real requests
// through the real middleware chain — not just an assertion about a helper
// function's arithmetic.
func TestR5e_ModelRequestRateLimit_Wiring_MemoryBackend(t *testing.T) {
	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	prevRedisEnabled := common.RedisEnabled
	defer func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
		common.RedisEnabled = prevRedisEnabled
	}()

	common.RedisEnabled = false // force the in-process backend
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitCount = 0 // total-request dimension stays skipped
	const successMax = 3
	setting.ModelRequestRateLimitSuccessCount = successMax

	const uid = 5885031 // unlikely to collide with any other in-package fixture
	for i := 1; i <= successMax; i++ {
		status, _, reached := r5eRunModelRateLimit(uid)
		if status != http.StatusOK || !reached {
			t.Fatalf("request %d/%d: status=%d reachedHandler=%v, want 200 and handler reached (within the armed ceiling)", i, successMax, status, reached)
		}
	}

	// Request successMax+1 must trip the ceiling.
	status, hdr, reached := r5eRunModelRateLimit(uid)
	if status != http.StatusTooManyRequests || reached {
		t.Fatalf("request %d/%d: status=%d reachedHandler=%v, want 429 and handler NOT reached (armed switch must actually block)", successMax+1, successMax, status, reached)
	}
	if got := hdr.Get("X-RateLimit-Limit"); got != "3" {
		t.Errorf("X-RateLimit-Limit = %q, want %q", got, "3")
	}

	// Disarming the switch — WITHOUT touching the already-exhausted bucket for
	// this same uid — must immediately let the request through. This is what
	// pins the switch itself (not the counter reset) as the thing under test:
	// if ModelRequestRateLimit() ignored setting.ModelRequestRateLimitEnabled,
	// this same uid would still be blocked here.
	setting.ModelRequestRateLimitEnabled = false
	status, _, reached = r5eRunModelRateLimit(uid)
	if status != http.StatusOK || !reached {
		t.Fatalf("after disabling: status=%d reachedHandler=%v, want 200 and handler reached — the exhausted bucket must not matter once the switch is off", status, reached)
	}
}

// (c) Fail-open test: an armed switch whose Redis backend is unreachable must
// let the request through (and reach the handler), not 500 the relay.
// Restoring the deleted abortWithOpenAiMessage(..., 500, ...) branch in
// model-rate-limit.go's success-count error path must turn this test red —
// see the mutation-proof note in the lane's status report.
func TestR5e_ModelRequestRateLimit_FailsOpen_RedisDown(t *testing.T) {
	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	prevRedisEnabled := common.RedisEnabled
	prevRDB := common.RDB
	defer func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
		common.RedisEnabled = prevRedisEnabled
		common.RDB = prevRDB
	}()

	// A throwaway miniredis, closed before use, gives real connection-refused
	// errors from go-redis (not a nil-pointer panic), matching how a live
	// Redis outage actually surfaces. Owning a dedicated instance (rather than
	// the shared one in redis_miniredis_cover_test.go) means closing it here
	// cannot affect any other test in the package.
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mr.Close() // every subsequent Redis command now errors out
	defer func() { _ = rdb.Close() }()

	common.RedisEnabled = true
	common.RDB = rdb
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitCount = 0
	setting.ModelRequestRateLimitSuccessCount = 1000 // must stay > 0 so checkRedisRateLimit actually calls LLen and errors

	const uid = 5885032
	status, _, reached := r5eRunModelRateLimit(uid)
	if status == http.StatusInternalServerError {
		t.Fatalf("status=500 — a Redis outage under an armed switch turned into a relay outage instead of failing open")
	}
	if status != http.StatusOK || !reached {
		t.Fatalf("status=%d reachedHandler=%v, want 200 and handler reached (fail-open on backend error)", status, reached)
	}
}
