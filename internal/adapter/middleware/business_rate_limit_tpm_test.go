package middleware

// business_rate_limit_tpm_test.go — hermetic coverage for the TPM
// (tokens-per-minute) dimension of BusinessRateLimit. The window store lives
// in package app (business_tpm.go); these tests exercise the REAL store
// through app.RecordBusinessTPMUsage (the production settlement write) plus
// the middleware read path, on both backends. Clocks (middleware bizNow and
// the store's app.BizTPMNow) are frozen together so sliding is deterministic;
// unique token/tenant ids per case keep the package -count=1 safe.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// bizFreezeClocks pins bizNow AND the TPM store's clock to one settable
// instant so RPM admissions and TPM records/queries slide in lockstep.
func bizFreezeClocks(t *testing.T) func(d time.Duration) {
	t.Helper()
	now := time.Now()
	prevRPM := bizNow
	prevTPM := app.BizTPMNow
	bizNow = func() time.Time { return now }
	app.BizTPMNow = func() time.Time { return now }
	t.Cleanup(func() {
		bizNow = prevRPM
		app.BizTPMNow = prevTPM
	})
	return func(d time.Duration) { now = now.Add(d) }
}

// runBizRLEstimate is runBizRL with an optional pre-set prompt-token estimate
// (constant.ContextKeyPromptTokens), simulating a chain where an earlier stage
// already counted the prompt.
func runBizRLEstimate(tokenID int, estimate int) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tokenID != 0 {
			c.Set("token_id", tokenID)
		}
		if estimate != 0 {
			c.Set(string(constant.ContextKeyPromptTokens), estimate)
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

func TestBusinessRateLimit_TokenTPM_EnforcesAndSlides(t *testing.T) {
	tokenBase := 61000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++ // fresh token id per backend so windows never collide
		tok := tokenBase
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{TPM: 100}, Tenant: ""},
		}, nil)
		advance := bizFreezeClocks(t)

		// 40 settled tokens in-window: under the 100 limit → admitted.
		app.RecordBusinessTPMUsage(tok, "", 40)
		if w := runBizRLEstimate(tok, 0); w.Code != http.StatusOK {
			t.Fatalf("request under TPM limit = %d, want 200", w.Code)
		}

		// 70 more (window 110 > 100): sustained overuse → 429 with headers,
		// relay-format body code, and the {token,tpm} counter.
		app.RecordBusinessTPMUsage(tok, "", 70)
		before := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("token", "tpm"))
		w := runBizRLEstimate(tok, 0)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("request over TPM limit = %d, want 429", w.Code)
		}
		ra, err := strconv.ParseInt(w.Header().Get("Retry-After"), 10, 64)
		if err != nil || ra < 1 || ra > 60 {
			t.Errorf("Retry-After = %q, want integer in [1,60]", w.Header().Get("Retry-After"))
		}
		if lim := w.Header().Get("X-RateLimit-Limit"); lim != "100" {
			t.Errorf("X-RateLimit-Limit = %q, want 100", lim)
		}
		after := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("token", "tpm"))
		if after-before != 1 {
			t.Errorf("rate_limited_total{token,tpm} delta = %v, want 1", after-before)
		}

		// The window slides: past 60s the settled usage ages out → admitted.
		advance(61 * time.Second)
		if w := runBizRLEstimate(tok, 0); w.Code != http.StatusOK {
			t.Errorf("post-window request = %d, want 200 (TPM window must slide)", w.Code)
		}
	})
}

// A single oversized request on a quiet window is ADMITTED (the window only
// holds settled usage and admission records nothing) — TPM throttles the
// requests that FOLLOW the spike, i.e. sustained overuse.
func TestBusinessRateLimit_TokenTPM_SingleSpikeAdmitted(t *testing.T) {
	tokenBase := 62000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tok := tokenBase
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{TPM: 100}, Tenant: ""},
		}, nil)
		bizFreezeClocks(t)

		// Quiet window: the spike request itself passes.
		if w := runBizRLEstimate(tok, 0); w.Code != http.StatusOK {
			t.Fatalf("spike request on quiet window = %d, want 200 (single spikes allowed)", w.Code)
		}
		// Its settlement lands 10x the limit in the window...
		app.RecordBusinessTPMUsage(tok, "", 1000)
		// ...so the NEXT request inside the window is throttled.
		if w := runBizRLEstimate(tok, 0); w.Code != http.StatusTooManyRequests {
			t.Errorf("request after settled spike = %d, want 429 (sustained overuse blocked)", w.Code)
		}
	})
}

// When an earlier stage did count the prompt, the estimate tightens admission:
// windowTotal + estimate > limit denies even though the window alone is under.
func TestBusinessRateLimit_TokenTPM_EstimateTightensAdmission(t *testing.T) {
	tokenBase := 63000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tok := tokenBase
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{TPM: 100}, Tenant: ""},
		}, nil)
		bizFreezeClocks(t)

		app.RecordBusinessTPMUsage(tok, "", 80)
		// 80 + 30 = 110 > 100 → denied.
		if w := runBizRLEstimate(tok, 30); w.Code != http.StatusTooManyRequests {
			t.Fatalf("window 80 + estimate 30 over limit 100 = %d, want 429", w.Code)
		}
		// 80 + 10 = 90 ≤ 100 → admitted.
		if w := runBizRLEstimate(tok, 10); w.Code != http.StatusOK {
			t.Errorf("window 80 + estimate 10 under limit 100 = %d, want 200", w.Code)
		}
	})
}

func TestBusinessRateLimit_TenantTPM_SharedAcrossTokens(t *testing.T) {
	tokenBase := 64000
	tenantSeq := 0
	eachBizBackend(t, func(t *testing.T) {
		tokenBase += 10
		tenantSeq++
		tenant := "t-tpm-shared-" + strconv.Itoa(tenantSeq)
		other := "t-tpm-other-" + strconv.Itoa(tenantSeq)
		tokA, tokB, tokC := tokenBase, tokenBase+1, tokenBase+2
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tokA: {L: bizRateLimits{}, Tenant: tenant},
			tokB: {L: bizRateLimits{}, Tenant: tenant},
			tokC: {L: bizRateLimits{}, Tenant: other},
		}, map[string]bizRateLimits{
			tenant: {TPM: 100},
			other:  {TPM: 100},
		})
		bizFreezeClocks(t)

		// Aggregate settled usage under the shared tenant exceeds its limit.
		app.RecordBusinessTPMUsage(tokA, tenant, 150)

		before := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("tenant", "tpm"))
		// BOTH tokens of that tenant are throttled — the window is shared.
		if w := runBizRLEstimate(tokA, 0); w.Code != http.StatusTooManyRequests {
			t.Fatalf("tokenA = %d, want 429 (tenant TPM window shared)", w.Code)
		}
		if w := runBizRLEstimate(tokB, 0); w.Code != http.StatusTooManyRequests {
			t.Fatalf("tokenB = %d, want 429 (tenant TPM window shared)", w.Code)
		}
		after := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("tenant", "tpm"))
		if after-before != 2 {
			t.Errorf("rate_limited_total{tenant,tpm} delta = %v, want 2", after-before)
		}
		// A different tenant with its own quiet window is unaffected.
		if w := runBizRLEstimate(tokC, 0); w.Code != http.StatusOK {
			t.Errorf("other tenant's token = %d, want 200 (windows isolate per tenant)", w.Code)
		}
	})
}

func TestBusinessRateLimit_TPMZeroMeansUnlimited(t *testing.T) {
	tokenBase := 65000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tok := tokenBase
		tenant := "t-tpm-unlimited-" + strconv.Itoa(tokenBase)
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{TPM: 0}, Tenant: tenant},
		}, map[string]bizRateLimits{tenant: {TPM: 0}})
		bizFreezeClocks(t)

		// Massive settled usage in both windows — 0 limit must never consult it.
		app.RecordBusinessTPMUsage(tok, tenant, 10_000_000)
		for i := 0; i < 5; i++ {
			if w := runBizRLEstimate(tok, 0); w.Code != http.StatusOK {
				t.Fatalf("request %d = %d, want 200 (TPM 0 = unlimited)", i+1, w.Code)
			}
		}
	})
}

// Redis down mid-flight → the TPM window read errors and the limiter fails
// OPEN, matching the RPM path's contract.
func TestBusinessRateLimit_TPM_RedisDown_FailsOpen(t *testing.T) {
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

	tok := 66001
	bizTestLimits(t, map[int]struct {
		L      bizRateLimits
		Tenant string
	}{
		tok: {L: bizRateLimits{TPM: 1}, Tenant: ""},
	}, nil)
	bizFreezeClocks(t)

	if w := runBizRLEstimate(tok, 0); w.Code != http.StatusOK {
		t.Errorf("redis-down TPM request = %d, want 200 (fail open)", w.Code)
	}
}
