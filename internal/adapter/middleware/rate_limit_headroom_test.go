package middleware

// rate_limit_headroom_test.go — self-pacing header coverage for
// BusinessRateLimit / BusinessModelRateLimit (business_rate_limit.go,
// business_model_rate_limit.go). Rides the same harness as
// business_rate_limit_test.go (eachBizBackend / bizTestLimits /
// bizFreezeClock / runBizRL): limits injected through the fetch seams,
// backends exercised on both memory and miniredis, clock frozen so window
// math is deterministic.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// TestCeilMsToUnixSec is the item-5 lock: Reset must ceil, not floor, so it
// never reports an instant that (unlike the whole-second Retry-After a 429
// at the same moment would promise) has effectively already passed by
// truncating away a sub-second remainder.
func TestCeilMsToUnixSec(t *testing.T) {
	cases := []struct{ ms, want int64 }{
		{0, 0},
		{1, 1},
		{999, 1},
		{1000, 1},
		{1001, 2},
		{60000, 60},
		{60001, 61},
	}
	for _, tc := range cases {
		if got := ceilMsToUnixSec(tc.ms); got != tc.want {
			t.Errorf("ceilMsToUnixSec(%d) = %d, want %d", tc.ms, got, tc.want)
		}
	}
}

// TestRateLimit_ScopeDerivedFromIdent is the lock for item 1: the keyed
// reject sites (redisRateLimiterKeyed / memoryRateLimiterKeyed) are shared by
// InternalApiRateLimit (ident = "ik:<key id>", via internalApiRateLimitKey)
// and the plain client-IP limiters (ident = the bare ClientIP, no prefix) —
// they must not hard-code X-RateLimit-Scope: ip, or an internal-API-key 429
// would lie about what tripped it. Also locks the new X-RateLimit-Type:
// requests on both, present on neither before this lane.
func TestRateLimit_ScopeDerivedFromIdent(t *testing.T) {
	doGet := func(r *gin.Engine) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		return w
	}

	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "redis" {
				_, _, cleanup := withMiniRedis(t)
				t.Cleanup(cleanup)
			} else {
				prev := common.RedisEnabled
				common.RedisEnabled = false
				t.Cleanup(func() { common.RedisEnabled = prev })
			}

			// Internal-API-key bucket.
			ikRouter := gin.New()
			ikRouter.Use(func(c *gin.Context) {
				c.Set("internal_api_key_id", 555)
				c.Next()
			})
			ikRouter.GET("/x", keyedRateLimitFactory(1, 60, "TEST_SCOPE_IK_"+backend, internalApiRateLimitKey),
				func(c *gin.Context) { c.Status(http.StatusOK) })
			if w := doGet(ikRouter); w.Code != http.StatusOK {
				t.Fatalf("first ik request = %d, want 200", w.Code)
			}
			w := doGet(ikRouter)
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("second ik request = %d, want 429", w.Code)
			}
			if scope := w.Header().Get("X-RateLimit-Scope"); scope != "key" {
				t.Errorf("internal-key 429 X-RateLimit-Scope = %q, want key", scope)
			}
			if typ := w.Header().Get("X-RateLimit-Type"); typ != "requests" {
				t.Errorf("internal-key 429 X-RateLimit-Type = %q, want requests", typ)
			}

			// Plain client-IP bucket.
			ipRouter := gin.New()
			ipRouter.GET("/x", rateLimitFactory(1, 60, "TEST_SCOPE_IP_"+backend),
				func(c *gin.Context) { c.Status(http.StatusOK) })
			if w := doGet(ipRouter); w.Code != http.StatusOK {
				t.Fatalf("first ip request = %d, want 200", w.Code)
			}
			w2 := doGet(ipRouter)
			if w2.Code != http.StatusTooManyRequests {
				t.Fatalf("second ip request = %d, want 429", w2.Code)
			}
			if scope := w2.Header().Get("X-RateLimit-Scope"); scope != "ip" {
				t.Errorf("ip 429 X-RateLimit-Scope = %q, want ip", scope)
			}
			if typ := w2.Header().Get("X-RateLimit-Type"); typ != "requests" {
				t.Errorf("ip 429 X-RateLimit-Type = %q, want requests", typ)
			}
		})
	}
}

func TestBusinessRateLimit_Headroom_TokenRPM(t *testing.T) {
	tokenBase := 61000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tok := tokenBase
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 3}, Tenant: "t-headroom"},
		}, nil)
		bizFreezeClock(t)
		now := time.Now().Unix()

		wantRemaining := []string{"2", "1", "0"}
		for i, want := range wantRemaining {
			w := runBizRL(tok)
			if w.Code != http.StatusOK {
				t.Fatalf("request %d = %d, want 200", i+1, w.Code)
			}
			if lim := w.Header().Get("X-RateLimit-Limit"); lim != "3" {
				t.Errorf("request %d: X-RateLimit-Limit = %q, want 3", i+1, lim)
			}
			if rem := w.Header().Get("X-RateLimit-Remaining"); rem != want {
				t.Errorf("request %d: X-RateLimit-Remaining = %q, want %s", i+1, rem, want)
			}
			if scope := w.Header().Get("X-RateLimit-Scope"); scope != "token" {
				t.Errorf("request %d: X-RateLimit-Scope = %q, want token", i+1, scope)
			}
			if typ := w.Header().Get("X-RateLimit-Type"); typ != "rpm" {
				t.Errorf("request %d: X-RateLimit-Type = %q, want rpm", i+1, typ)
			}
			// Upper bound is now+61, not now+60: Reset is ceil'd to whole
			// seconds (item 5 — matches bizRetryAfterSec's own ceil), and
			// `now` here was floored by time.Now().Unix() before bizFreezeClock
			// captured the (sub-second-precise) frozen instant, so the ceil can
			// legitimately land one second past a floor-based now+60.
			reset, err := strconv.ParseInt(w.Header().Get("X-RateLimit-Reset"), 10, 64)
			if err != nil || reset < now || reset > now+61 {
				t.Errorf("request %d: X-RateLimit-Reset = %q, want integer in [%d,%d]", i+1, w.Header().Get("X-RateLimit-Reset"), now, now+61)
			}
			if ra := w.Header().Get("Retry-After"); ra != "" {
				t.Errorf("request %d: admitted response must not carry Retry-After, got %q", i+1, ra)
			}
		}

		// Fourth request within the window trips the bucket.
		w := runBizRL(tok)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("fourth request = %d, want 429", w.Code)
		}
		if ra := w.Header().Get("Retry-After"); ra == "" || ra == "0" {
			t.Errorf("429 Retry-After = %q, want positive seconds", ra)
		}
		if rem := w.Header().Get("X-RateLimit-Remaining"); rem != "0" {
			t.Errorf("429 X-RateLimit-Remaining = %q, want 0", rem)
		}
		if scope := w.Header().Get("X-RateLimit-Scope"); scope != "token" {
			t.Errorf("429 X-RateLimit-Scope = %q, want token", scope)
		}
		if typ := w.Header().Get("X-RateLimit-Type"); typ != "rpm" {
			t.Errorf("429 X-RateLimit-Type = %q, want rpm", typ)
		}
	})
}

func TestBusinessRateLimit_Headroom_TightestScope(t *testing.T) {
	tokenBase := 62000
	tenantSeq := 0
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tenantSeq++
		tok := tokenBase
		tenant := "t-tightest-" + strconv.Itoa(tenantSeq)
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 10}, Tenant: tenant},
		}, map[string]bizRateLimits{tenant: {RPM: 2}})
		bizFreezeClock(t)

		// First request: token headroom 9/10 (ratio .9), tenant headroom 1/2
		// (ratio .5) — tenant is already tighter.
		if w := runBizRL(tok); w.Code != http.StatusOK {
			t.Fatalf("first request = %d, want 200", w.Code)
		}

		// Second request: tenant bucket is now exhausted (0/2) while the
		// token bucket still has 8/10 left — tenant must win.
		w := runBizRL(tok)
		if w.Code != http.StatusOK {
			t.Fatalf("second request = %d, want 200", w.Code)
		}
		if scope := w.Header().Get("X-RateLimit-Scope"); scope != "tenant" {
			t.Errorf("X-RateLimit-Scope = %q, want tenant (tenant bucket is tighter)", scope)
		}
		if rem := w.Header().Get("X-RateLimit-Remaining"); rem != "0" {
			t.Errorf("X-RateLimit-Remaining = %q, want 0", rem)
		}
		if lim := w.Header().Get("X-RateLimit-Limit"); lim != "2" {
			t.Errorf("X-RateLimit-Limit = %q, want 2 (the tenant limit)", lim)
		}
	})
}

func TestBusinessRateLimit_Headroom_TPM(t *testing.T) {
	tokenBase := 63000
	tenantSeq := 0
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tenantSeq++
		tok := tokenBase
		tenant := "t-tpm-headroom-" + strconv.Itoa(tenantSeq)
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{}, Tenant: tenant},
		}, map[string]bizRateLimits{tenant: {TPM: 1000}})
		bizFreezeClock(t)

		app.RecordBusinessTPMUsage(0, tenant, 400)

		w := runBizRL(tok)
		if w.Code != http.StatusOK {
			t.Fatalf("request = %d, want 200", w.Code)
		}
		if rem := w.Header().Get("X-RateLimit-Remaining"); rem != "600" {
			t.Errorf("X-RateLimit-Remaining = %q, want 600 (1000 limit - 400 settled)", rem)
		}
		if typ := w.Header().Get("X-RateLimit-Type"); typ != "tpm" {
			t.Errorf("X-RateLimit-Type = %q, want tpm", typ)
		}
		if scope := w.Header().Get("X-RateLimit-Scope"); scope != "tenant" {
			t.Errorf("X-RateLimit-Scope = %q, want tenant", scope)
		}
	})
}

// runBizChainRL drives BOTH BusinessRateLimit and BusinessModelRateLimit in
// the same request, in their real router order (business_model_rate_limit.go
// runs after Distribute, which is after BusinessRateLimit — this mirrors that
// ordering without needing Distribute itself). Unlike
// business_model_rate_limit_test.go's runBizModelRL (which drives
// BusinessModelRateLimit alone with a hand-set stash), this is the only
// harness that actually exercises BusinessModelRateLimit's
// bizHeadroomFromHeaders read-back against headers BusinessRateLimit really
// wrote — the seam item 2 flags as unoracled without it.
func runBizChainRL(tokenID int, model string) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tokenID != 0 {
			c.Set("token_id", tokenID)
		}
		if model != "" {
			c.Set("original_model", model) // exactly what Distribute sets
		}
		c.Next()
	})
	r.POST("/v1/chat/completions", BusinessRateLimit(), BusinessModelRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	return w
}

// TestBusinessRateLimit_Headroom_ModelTighterThanToken is the chained lock
// for item 2: a model-scoped limit stricter than the token's must overwrite
// what BusinessRateLimit already wrote, once BusinessModelRateLimit runs
// later in the SAME real chain (not a hand-set stash).
func TestBusinessRateLimit_Headroom_ModelTighterThanToken(t *testing.T) {
	tokenBase := 65000
	seq := 0
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		seq++
		tok := tokenBase
		tenant := "t-chain-tighter-" + strconv.Itoa(seq)
		model := "m-chain-tighter"
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 10}, Tenant: tenant},
		}, nil)
		bizModelTestLimits(t, map[string]bizRateLimits{
			tenant + "|" + model: {RPM: 2},
		})
		bizFreezeClock(t)

		if w := runBizChainRL(tok, model); w.Code != http.StatusOK {
			t.Fatalf("first request = %d, want 200", w.Code)
		}

		// Second request: token headroom is 8/10 (ratio .8); model headroom is
		// 0/2 (ratio 0) — model must win and overwrite BusinessRateLimit's write.
		w := runBizChainRL(tok, model)
		if w.Code != http.StatusOK {
			t.Fatalf("second request = %d, want 200", w.Code)
		}
		if scope := w.Header().Get("X-RateLimit-Scope"); scope != "model" {
			t.Errorf("X-RateLimit-Scope = %q, want model (model limit is tighter than the token's)", scope)
		}
		if rem := w.Header().Get("X-RateLimit-Remaining"); rem != "0" {
			t.Errorf("X-RateLimit-Remaining = %q, want 0", rem)
		}
		if lim := w.Header().Get("X-RateLimit-Limit"); lim != "2" {
			t.Errorf("X-RateLimit-Limit = %q, want 2 (the model limit)", lim)
		}
	})
}

// TestBusinessRateLimit_Headroom_TokenTighterThanModel is the other half of
// item 2's chained lock: BusinessModelRateLimit must NOT overwrite a tighter
// headroom BusinessRateLimit already wrote just because the model dimension
// ran later — "overwrite only when tighter" (business_model_rate_limit.go's
// bizHeadroomFromHeaders comparison), not "last write wins".
func TestBusinessRateLimit_Headroom_TokenTighterThanModel(t *testing.T) {
	tokenBase := 66000
	seq := 0
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		seq++
		tok := tokenBase
		tenant := "t-chain-token-wins-" + strconv.Itoa(seq)
		model := "m-chain-token-wins"
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 2}, Tenant: tenant},
		}, nil)
		bizModelTestLimits(t, map[string]bizRateLimits{
			tenant + "|" + model: {RPM: 10},
		})
		bizFreezeClock(t)

		w := runBizChainRL(tok, model)
		if w.Code != http.StatusOK {
			t.Fatalf("request = %d, want 200", w.Code)
		}
		// Token: 1/2 remaining (ratio .5). Model: 9/10 remaining (ratio .9).
		// Token is tighter — its scope must survive BusinessModelRateLimit.
		if scope := w.Header().Get("X-RateLimit-Scope"); scope != "token" {
			t.Errorf("X-RateLimit-Scope = %q, want token (token limit is tighter than the model's, must not be overwritten)", scope)
		}
		if lim := w.Header().Get("X-RateLimit-Limit"); lim != "2" {
			t.Errorf("X-RateLimit-Limit = %q, want 2 (the token limit)", lim)
		}
	})
}

// TestBusinessRateLimit_Headroom_StaleResetClearedOnLaterReject is the lock
// for item 3: BusinessRateLimit's admit-path write leaves X-RateLimit-Reset
// on the writer (up to 60s in the future); when BusinessModelRateLimit later
// rejects in the SAME request, that Reset must not survive next to the
// reject's own Retry-After — a reject has no window-Reset value of its own to
// overwrite it with, so bizReject must delete it outright.
func TestBusinessRateLimit_Headroom_StaleResetClearedOnLaterReject(t *testing.T) {
	tokenBase := 67000
	seq := 0
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		seq++
		tok := tokenBase
		tenant := "t-chain-stale-reset-" + strconv.Itoa(seq)
		model := "m-chain-stale-reset"
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 10}, Tenant: tenant},
		}, nil)
		bizModelTestLimits(t, map[string]bizRateLimits{
			tenant + "|" + model: {RPM: 1},
		})
		bizFreezeClock(t)

		// First request: token admits (writes Reset ~60s out), model admits too
		// (1/1 used) and — being tighter (0 remaining) — overwrites the token's
		// headroom with its own, still Reset-bearing, snapshot.
		if w := runBizChainRL(tok, model); w.Code != http.StatusOK {
			t.Fatalf("first request = %d, want 200", w.Code)
		}

		// Second request: token still admits (headroom write happens BEFORE
		// c.Next(), so it always runs), but the model bucket is now exhausted —
		// BusinessModelRateLimit rejects. The reject must not leave the token
		// pass's (or its own admit attempt's) Reset on the response.
		w := runBizChainRL(tok, model)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("second request = %d, want 429", w.Code)
		}
		if reset := w.Header().Get("X-RateLimit-Reset"); reset != "" {
			t.Errorf("429 X-RateLimit-Reset = %q, want absent (reject has no window-reset value to publish)", reset)
		}
		if scope := w.Header().Get("X-RateLimit-Scope"); scope != "model" {
			t.Errorf("429 X-RateLimit-Scope = %q, want model", scope)
		}
		if ra := w.Header().Get("Retry-After"); ra == "" || ra == "0" {
			t.Errorf("429 Retry-After = %q, want positive seconds", ra)
		}
	})
}

func TestBusinessRateLimit_Headroom_DisabledSeam(t *testing.T) {
	tokenBase := 64000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tok := tokenBase
		prev := bizHeadroomEnabled
		bizHeadroomEnabled = false
		t.Cleanup(func() { bizHeadroomEnabled = prev })

		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 1}, Tenant: ""},
		}, nil)
		bizFreezeClock(t)

		w := runBizRL(tok)
		if w.Code != http.StatusOK {
			t.Fatalf("first request = %d, want 200", w.Code)
		}
		for _, h := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "X-RateLimit-Scope", "X-RateLimit-Type"} {
			if v := w.Header().Get(h); v != "" {
				t.Errorf("seam disabled: admitted response carries %s=%q, want absent", h, v)
			}
		}

		// The 429 headers are unconditional — the seam only gates the
		// admit-path headroom, never the reject-path contract.
		w429 := runBizRL(tok)
		if w429.Code != http.StatusTooManyRequests {
			t.Fatalf("second request = %d, want 429", w429.Code)
		}
		if scope := w429.Header().Get("X-RateLimit-Scope"); scope != "token" {
			t.Errorf("seam disabled: 429 X-RateLimit-Scope = %q, want token (429 headers stay on)", scope)
		}
		if ra := w429.Header().Get("Retry-After"); ra == "" {
			t.Error("seam disabled: 429 must still carry Retry-After")
		}
	})
}

// TestBusinessRateLimit_Headroom_ExactReset is the residuals-round-2 item-3
// lock: TestBusinessRateLimit_Headroom_TokenRPM above only bounds Reset to a
// range ([now, now+61]), which a regression from ceil to floor at
// business_rate_limit.go:118 (headroom()'s ceilMsToUnixSec call) would often
// still land inside — the two only visibly differ when the frozen instant's
// sub-second remainder is non-zero, which time.Now()-based freezing does not
// guarantee. This test captures the exact frozen instant itself and asserts
// the precise formula: for the FIRST admission into an empty window,
// oldestMs == that instant's own unix-milli, so Reset must be EXACTLY
// ceil((frozenUnixMilli+60000)/1000) — not merely "close enough".
func TestBusinessRateLimit_Headroom_ExactReset(t *testing.T) {
	eachBizBackend(t, func(t *testing.T) {
		tok := 64000
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 5}, Tenant: "t-exact-reset"},
		}, nil)

		frozen := time.Now()
		prevNow := bizNow
		bizNow = func() time.Time { return frozen }
		t.Cleanup(func() { bizNow = prevNow })

		wantReset := (frozen.UnixMilli() + bizRateLimitWindow.Milliseconds() + 999) / 1000

		w := runBizRL(tok)
		if w.Code != http.StatusOK {
			t.Fatalf("request = %d, want 200", w.Code)
		}
		reset, err := strconv.ParseInt(w.Header().Get("X-RateLimit-Reset"), 10, 64)
		if err != nil {
			t.Fatalf("X-RateLimit-Reset = %q, not an integer: %v", w.Header().Get("X-RateLimit-Reset"), err)
		}
		if reset != wantReset {
			t.Errorf("X-RateLimit-Reset = %d, want exactly %d (ceil((%d+60000)/1000))", reset, wantReset, frozen.UnixMilli())
		}
	})
}
