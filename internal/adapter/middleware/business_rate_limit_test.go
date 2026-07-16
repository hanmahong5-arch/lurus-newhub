package middleware

// business_rate_limit_test.go — hermetic coverage for the per-token /
// per-tenant RPM sliding-window limiter (business_rate_limit.go). Limits are
// injected through the fetch seams (no DB); the Redis tier runs against the
// shared miniredis from redis_miniredis_cover_test.go; the memory tier runs
// with RedisEnabled=false. The clock is injected via bizNow so window sliding
// is deterministic. No test starts goroutines and every global mutated is
// restored, keeping the package -count=1 safe.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// bizTestLimits programs the fetch seams: tokLimits maps token id → (limits,
// tenant id); tenLimits maps tenant id → limits. Restores everything and
// resets both window backends so cases can't bleed into each other.
func bizTestLimits(t *testing.T, tokLimits map[int]struct {
	L      bizRateLimits
	Tenant string
}, tenLimits map[string]bizRateLimits) {
	t.Helper()
	prevTok := fetchTokenBizLimits
	prevTen := fetchTenantBizLimits
	fetchTokenBizLimits = func(tokenID int) (bizRateLimits, string, error) {
		e, ok := tokLimits[tokenID]
		if !ok {
			return bizRateLimits{}, "", errors.New("stub: unknown token")
		}
		return e.L, e.Tenant, nil
	}
	fetchTenantBizLimits = func(tenantID string) (bizRateLimits, error) {
		return tenLimits[tenantID], nil
	}
	t.Cleanup(func() {
		fetchTokenBizLimits = prevTok
		fetchTenantBizLimits = prevTen
		bizMemLimiter.mu.Lock()
		bizMemLimiter.entries = make(map[string][]int64)
		bizMemLimiter.mu.Unlock()
	})
}

// bizFreezeClock pins bizNow to a settable instant and returns an advance fn.
func bizFreezeClock(t *testing.T) func(d time.Duration) {
	t.Helper()
	now := time.Now()
	prev := bizNow
	bizNow = func() time.Time { return now }
	t.Cleanup(func() { bizNow = prev })
	return func(d time.Duration) { now = now.Add(d) }
}

// runBizRL sends one request through BusinessRateLimit with token_id set
// (0 = not set) and returns the recorder.
func runBizRL(tokenID int) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tokenID != 0 {
			c.Set("token_id", tokenID)
		}
		c.Next()
	})
	r.POST("/v1/chat/completions", BusinessRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	return w
}

// eachBizBackend runs fn once on the in-memory backend and once on miniredis.
func eachBizBackend(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		prev := common.RedisEnabled
		common.RedisEnabled = false
		t.Cleanup(func() { common.RedisEnabled = prev })
		fn(t)
	})
	t.Run("redis", func(t *testing.T) {
		_, _, cleanup := withMiniRedis(t)
		t.Cleanup(cleanup)
		fn(t)
	})
}

func TestBusinessRateLimit_TokenRPM_EnforcesAndSlides(t *testing.T) {
	tokenBase := 51000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++ // fresh token id per backend so buckets never collide
		tok := tokenBase
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 2}, Tenant: "t-free"},
		}, nil)
		advance := bizFreezeClock(t)

		// Two admissions under limit 2.
		for i := 0; i < 2; i++ {
			if w := runBizRL(tok); w.Code != http.StatusOK {
				t.Fatalf("request %d = %d, want 200", i+1, w.Code)
			}
		}

		// Third within the window trips the token bucket.
		before := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("token", "rpm"))
		w := runBizRL(tok)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("third request = %d, want 429", w.Code)
		}
		// Retry-After must be a positive integer no larger than the window.
		ra, err := strconv.ParseInt(w.Header().Get("Retry-After"), 10, 64)
		if err != nil || ra < 1 || ra > 60 {
			t.Errorf("Retry-After = %q, want integer in [1,60]", w.Header().Get("Retry-After"))
		}
		if lim := w.Header().Get("X-RateLimit-Limit"); lim != "2" {
			t.Errorf("X-RateLimit-Limit = %q, want 2", lim)
		}
		// Body follows the relay error format (same shape abortWithOpenAiMessage emits).
		var body struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("429 body not JSON: %v (%s)", err, w.Body.String())
		}
		if body.Error.Type != "new_api_error" || body.Error.Code != bizRateLimitErrorCode || body.Error.Message == "" {
			t.Errorf("429 body = %+v, want type=new_api_error code=%s non-empty message", body.Error, bizRateLimitErrorCode)
		}
		after := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("token", "rpm"))
		if after-before != 1 {
			t.Errorf("rate_limited_total{token,rpm} delta = %v, want 1", after-before)
		}

		// Slide the window past 60s: the bucket must admit again.
		advance(61 * time.Second)
		if w := runBizRL(tok); w.Code != http.StatusOK {
			t.Errorf("post-window request = %d, want 200 (window must slide)", w.Code)
		}
	})
}

func TestBusinessRateLimit_ZeroMeansUnlimited(t *testing.T) {
	tokenBase := 52000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tok := tokenBase
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 0}, Tenant: "t-unlimited"},
		}, map[string]bizRateLimits{"t-unlimited": {RPM: 0}})
		bizFreezeClock(t)

		for i := 0; i < 10; i++ {
			if w := runBizRL(tok); w.Code != http.StatusOK {
				t.Fatalf("request %d = %d, want 200 (0 = unlimited)", i+1, w.Code)
			}
		}
	})
}

func TestBusinessRateLimit_TokensDoNotShareBuckets(t *testing.T) {
	tokenBase := 53000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase += 10
		tokA, tokB := tokenBase, tokenBase+1
		limits := map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tokA: {L: bizRateLimits{RPM: 1}, Tenant: ""},
			tokB: {L: bizRateLimits{RPM: 1}, Tenant: ""},
		}
		bizTestLimits(t, limits, nil)
		bizFreezeClock(t)

		if w := runBizRL(tokA); w.Code != http.StatusOK {
			t.Fatalf("tokenA first = %d, want 200", w.Code)
		}
		if w := runBizRL(tokA); w.Code != http.StatusTooManyRequests {
			t.Fatalf("tokenA second = %d, want 429", w.Code)
		}
		// tokenB has its own bucket and must be unaffected by tokenA's burst.
		if w := runBizRL(tokB); w.Code != http.StatusOK {
			t.Errorf("tokenB first = %d, want 200 (buckets must isolate per token)", w.Code)
		}
	})
}

func TestBusinessRateLimit_TenantBucketSharedAcrossTokens(t *testing.T) {
	tokenBase := 54000
	tenantSeq := 0
	eachBizBackend(t, func(t *testing.T) {
		tokenBase += 10
		tenantSeq++
		tenant := "t-shared-" + strconv.Itoa(tenantSeq)
		tokA, tokB, tokC := tokenBase, tokenBase+1, tokenBase+2
		limits := map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tokA: {L: bizRateLimits{}, Tenant: tenant},
			tokB: {L: bizRateLimits{}, Tenant: tenant},
			tokC: {L: bizRateLimits{}, Tenant: tenant},
		}
		bizTestLimits(t, limits, map[string]bizRateLimits{tenant: {RPM: 2}})
		bizFreezeClock(t)

		before := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("tenant", "rpm"))
		if w := runBizRL(tokA); w.Code != http.StatusOK {
			t.Fatalf("tokenA = %d, want 200", w.Code)
		}
		if w := runBizRL(tokB); w.Code != http.StatusOK {
			t.Fatalf("tokenB = %d, want 200", w.Code)
		}
		// Third request from a THIRD token still trips the shared tenant bucket.
		w := runBizRL(tokC)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("tokenC = %d, want 429 (tenant bucket is shared)", w.Code)
		}
		if ra := w.Header().Get("Retry-After"); ra == "" || ra == "0" {
			t.Errorf("Retry-After = %q, want positive seconds", ra)
		}
		after := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("tenant", "rpm"))
		if after-before != 1 {
			t.Errorf("rate_limited_total{tenant,rpm} delta = %v, want 1", after-before)
		}
	})
}

func TestBusinessRateLimit_NoTokenID_PassesThrough(t *testing.T) {
	eachBizBackend(t, func(t *testing.T) {
		// Seams that would fail loudly if the middleware consulted them.
		bizTestLimits(t, nil, nil)
		prevTok := fetchTokenBizLimits
		fetchTokenBizLimits = func(int) (bizRateLimits, string, error) {
			t.Error("limit lookup must not run without token_id")
			return bizRateLimits{}, "", nil
		}
		t.Cleanup(func() { fetchTokenBizLimits = prevTok })

		if w := runBizRL(0); w.Code != http.StatusOK {
			t.Errorf("no token_id = %d, want 200 pass-through", w.Code)
		}
	})
}

func TestBusinessRateLimit_LookupError_FailsOpen(t *testing.T) {
	eachBizBackend(t, func(t *testing.T) {
		// bizTestLimits stub returns an error for unknown token ids.
		bizTestLimits(t, nil, nil)
		if w := runBizRL(59999); w.Code != http.StatusOK {
			t.Errorf("lookup-error request = %d, want 200 (fail open)", w.Code)
		}
	})
}

// The memory window prunes on access: after the window slides, denied keys
// admit again and stored timestamps do not grow unboundedly.
func TestBizMemoryLimiter_PruneAndRetryAfter(t *testing.T) {
	l := bizMemoryLimiter{entries: make(map[string][]int64)}
	base := time.Unix(1_700_000_000, 0)

	if ok, _ := l.allow("k", 1, time.Minute, base); !ok {
		t.Fatalf("first admission must pass")
	}
	ok, retry := l.allow("k", 1, time.Minute, base.Add(10*time.Second))
	if ok {
		t.Fatalf("second admission within window must be denied")
	}
	// Oldest entry is 10s old → 50s left in the window.
	if retry != 50 {
		t.Errorf("Retry-After = %d, want 50", retry)
	}
	// After the window slides the same key admits again and holds ONE entry.
	if ok, _ := l.allow("k", 1, time.Minute, base.Add(61*time.Second)); !ok {
		t.Fatalf("post-window admission must pass")
	}
	l.mu.Lock()
	n := len(l.entries["k"])
	l.mu.Unlock()
	if n != 1 {
		t.Errorf("stored entries after prune = %d, want 1", n)
	}
}

func TestBizRetryAfterSec_ClampsToOneSecond(t *testing.T) {
	// Oldest is about to expire → remaining ~0 must still report 1s.
	nowMs := int64(1_700_000_060_000)
	oldest := nowMs - time.Minute.Milliseconds() // exactly at cutoff
	if got := bizRetryAfterSec(oldest, nowMs, time.Minute); got != 1 {
		t.Errorf("retry-after at cutoff = %d, want clamp to 1", got)
	}
	// Fractional seconds round UP so clients never retry early.
	oldest = nowMs - time.Minute.Milliseconds() + 1500 // 1.5s remaining
	if got := bizRetryAfterSec(oldest, nowMs, time.Minute); got != 2 {
		t.Errorf("retry-after 1.5s remaining = %d, want ceil to 2", got)
	}
}

// Redis backend down mid-flight → the limiter fails OPEN (contrast with the
// legacy IP limiter's fail-closed 500): traffic keeps flowing. Owns a
// throwaway miniredis (it must close the server), leaving the shared
// instance from redis_miniredis_cover_test.go untouched.
func TestBusinessRateLimit_RedisDown_FailsOpen(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	common.RDB = rdb
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
		_ = rdb.Close()
	})
	mr.Close() // subsequent commands error out

	tok := 56001
	bizTestLimits(t, map[int]struct {
		L      bizRateLimits
		Tenant string
	}{
		tok: {L: bizRateLimits{RPM: 1}, Tenant: ""},
	}, nil)
	bizFreezeClock(t)

	if w := runBizRL(tok); w.Code != http.StatusOK {
		t.Errorf("redis-down request = %d, want 200 (fail open)", w.Code)
	}
}
