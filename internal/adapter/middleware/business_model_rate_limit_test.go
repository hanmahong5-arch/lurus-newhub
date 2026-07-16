package middleware

// business_model_rate_limit_test.go — hermetic coverage for the (tenant,
// model) dimension (business_model_rate_limit.go). Limits are injected
// through the fetchModelBizLimits seam; the tenant arrives via the context
// stash (production: set by BusinessRateLimit) or the fetchTokenBizLimits
// fallback. Backends and clock follow business_rate_limit_test.go: memory +
// miniredis via eachBizBackend, bizNow frozen. Unique tenant/model names per
// test keep the package -count=1 safe.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// bizModelTestLimits programs the model-limit seam from a "tenant|model" map
// and resets the memory window on cleanup (mirrors bizTestLimits).
func bizModelTestLimits(t *testing.T, limits map[string]bizRateLimits) {
	t.Helper()
	prev := fetchModelBizLimits
	fetchModelBizLimits = func(tenantID, model string) (bizRateLimits, error) {
		return limits[tenantID+"|"+model], nil
	}
	t.Cleanup(func() {
		fetchModelBizLimits = prev
		bizMemLimiter.mu.Lock()
		bizMemLimiter.entries = make(map[string][]int64)
		bizMemLimiter.mu.Unlock()
	})
}

// runBizModelRL sends one request through BusinessModelRateLimit. tokenID 0 =
// unauthenticated; model "" = Distribute parsed no model; stashTenant "" =
// no BusinessRateLimit stash (with stash=false the key is absent entirely).
func runBizModelRL(tokenID int, model string, stash bool, stashTenant string) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tokenID != 0 {
			c.Set("token_id", tokenID)
		}
		if model != "" {
			c.Set("original_model", model) // exactly what Distribute sets
		}
		if stash {
			c.Set(bizTenantIDContextKey, stashTenant)
		}
		c.Next()
	})
	r.POST("/v1/chat/completions", BusinessModelRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	return w
}

func TestBusinessModelRateLimit_RPM_PerTenantPerModel(t *testing.T) {
	seq := 0
	eachBizBackend(t, func(t *testing.T) {
		seq++
		tenant := "t-model-rpm-" + string(rune('a'+seq))
		bizModelTestLimits(t, map[string]bizRateLimits{
			tenant + "|m-limited": {RPM: 2},
		})
		advance := bizFreezeClock(t)

		for i := 0; i < 2; i++ {
			if w := runBizModelRL(71001, "m-limited", true, tenant); w.Code != http.StatusOK {
				t.Fatalf("request %d = %d, want 200", i+1, w.Code)
			}
		}
		before := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("model", "rpm"))
		w := runBizModelRL(71001, "m-limited", true, tenant)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("third request = %d, want 429", w.Code)
		}
		if ra := w.Header().Get("Retry-After"); ra == "" || ra == "0" {
			t.Errorf("Retry-After = %q, want positive seconds", ra)
		}
		if lim := w.Header().Get("X-RateLimit-Limit"); lim != "2" {
			t.Errorf("X-RateLimit-Limit = %q, want 2", lim)
		}
		after := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("model", "rpm"))
		if after-before != 1 {
			t.Errorf("rate_limited_total{model,rpm} delta = %v, want 1", after-before)
		}

		// Another model under the same tenant/token has no limit row → free.
		if w := runBizModelRL(71001, "m-free", true, tenant); w.Code != http.StatusOK {
			t.Errorf("unlimited model = %d, want 200 (limits must isolate per model)", w.Code)
		}
		// Same model under ANOTHER tenant has its own (absent) limit → free.
		if w := runBizModelRL(71002, "m-limited", true, tenant+"-other"); w.Code != http.StatusOK {
			t.Errorf("other tenant same model = %d, want 200 (limits must isolate per tenant)", w.Code)
		}

		// Window slides.
		advance(61 * time.Second)
		if w := runBizModelRL(71001, "m-limited", true, tenant); w.Code != http.StatusOK {
			t.Errorf("post-window request = %d, want 200 (window must slide)", w.Code)
		}
	})
}

func TestBusinessModelRateLimit_TPM_FromSettledWindow(t *testing.T) {
	seq := 0
	eachBizBackend(t, func(t *testing.T) {
		seq++
		tenant := "t-model-tpm-" + string(rune('a'+seq))
		bizModelTestLimits(t, map[string]bizRateLimits{
			tenant + "|m-tpm": {TPM: 50},
		})
		bizFreezeClock(t)

		// Quiet window → admitted (single spikes allowed by design).
		if w := runBizModelRL(72001, "m-tpm", true, tenant); w.Code != http.StatusOK {
			t.Fatalf("quiet window = %d, want 200", w.Code)
		}

		// 60 settled tokens land via the production settlement write → deny.
		app.RecordBusinessTPMModelUsage(tenant, "m-tpm", 60)
		before := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("model", "tpm"))
		if w := runBizModelRL(72001, "m-tpm", true, tenant); w.Code != http.StatusTooManyRequests {
			t.Fatalf("hot window = %d, want 429", w.Code)
		}
		after := testutil.ToFloat64(metrics.RateLimitedTotal.WithLabelValues("model", "tpm"))
		if after-before != 1 {
			t.Errorf("rate_limited_total{model,tpm} delta = %v, want 1", after-before)
		}

		// The same tenant's OTHER model is unaffected by that usage.
		bizModelTestLimits(t, map[string]bizRateLimits{
			tenant + "|m-tpm-other": {TPM: 50},
		})
		if w := runBizModelRL(72001, "m-tpm-other", true, tenant); w.Code != http.StatusOK {
			t.Errorf("other model on hot tenant = %d, want 200 (TPM windows isolate per model)", w.Code)
		}
	})
}

func TestBusinessModelRateLimit_PassThroughs(t *testing.T) {
	eachBizBackend(t, func(t *testing.T) {
		// Seam that fails the test loudly if consulted on a pass-through.
		prev := fetchModelBizLimits
		fetchModelBizLimits = func(string, string) (bizRateLimits, error) {
			t.Error("model limit lookup must not run without token_id+model+tenant")
			return bizRateLimits{}, nil
		}
		t.Cleanup(func() { fetchModelBizLimits = prev })

		if w := runBizModelRL(0, "m-x", true, "t-x"); w.Code != http.StatusOK {
			t.Errorf("no token_id = %d, want 200 pass-through", w.Code)
		}
		if w := runBizModelRL(73001, "", true, "t-x"); w.Code != http.StatusOK {
			t.Errorf("no model = %d, want 200 pass-through", w.Code)
		}
		// Stashed-but-empty tenant → unattributable, pass.
		if w := runBizModelRL(73001, "m-x", true, ""); w.Code != http.StatusOK {
			t.Errorf("empty stashed tenant = %d, want 200 pass-through", w.Code)
		}
	})
}

func TestBusinessModelRateLimit_LookupError_FailsOpen(t *testing.T) {
	eachBizBackend(t, func(t *testing.T) {
		prev := fetchModelBizLimits
		fetchModelBizLimits = func(string, string) (bizRateLimits, error) {
			return bizRateLimits{}, errors.New("stub: db down")
		}
		t.Cleanup(func() { fetchModelBizLimits = prev })

		if w := runBizModelRL(74001, "m-err", true, "t-err"); w.Code != http.StatusOK {
			t.Errorf("lookup-error request = %d, want 200 (fail open)", w.Code)
		}
	})
}

// Without the BusinessRateLimit stash the middleware resolves the tenant from
// the token row (fetchTokenBizLimits) — routes wired with only the model
// dimension still enforce.
func TestBusinessModelRateLimit_TenantFallbackFromToken(t *testing.T) {
	eachBizBackend(t, func(t *testing.T) {
		tok := 75001
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{}, Tenant: "t-fallback"},
		}, nil)
		bizModelTestLimits(t, map[string]bizRateLimits{
			"t-fallback|m-fb": {RPM: 1},
		})
		bizFreezeClock(t)

		if w := runBizModelRL(tok, "m-fb", false, ""); w.Code != http.StatusOK {
			t.Fatalf("first request = %d, want 200", w.Code)
		}
		if w := runBizModelRL(tok, "m-fb", false, ""); w.Code != http.StatusTooManyRequests {
			t.Errorf("second request = %d, want 429 (fallback tenant lookup must enforce)", w.Code)
		}

		// Fallback lookup error → fail open.
		prevTok := fetchTokenBizLimits
		fetchTokenBizLimits = func(int) (bizRateLimits, string, error) {
			return bizRateLimits{}, "", errors.New("stub: token lookup down")
		}
		t.Cleanup(func() { fetchTokenBizLimits = prevTok })
		if w := runBizModelRL(tok, "m-fb", false, ""); w.Code != http.StatusOK {
			t.Errorf("fallback-error request = %d, want 200 (fail open)", w.Code)
		}
	})
}
