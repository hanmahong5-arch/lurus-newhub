package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// ccTestRouter builds a router with the limiter in front of a handler whose
// completion the test controls, so requests can be held "in flight".
func ccTestRouter(t *testing.T, tokenID int, tenantID string, block <-chan struct{}) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tokenID > 0 {
			common.SetContextKey(c, constant.ContextKeyTokenId, tokenID)
		}
		if tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
		c.Next()
	})
	r.Use(RelayConcurrencyLimit())
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		if block != nil {
			<-block
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func ccDo(r *gin.Engine) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	return w
}

func withoutRedis(t *testing.T) {
	t.Helper()
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })
	resetConcurrencyLocalForTest()
}

func TestRelayConcurrencyLimit_DisabledByDefault(t *testing.T) {
	withoutRedis(t)
	// No RELAY_MAX_CONCURRENT_* set → every request passes, which is what an
	// existing deployment must keep seeing after this middleware ships.
	r := ccTestRouter(t, 7, "acme", nil)
	for i := 0; i < 5; i++ {
		if w := ccDo(r); w.Code != http.StatusOK {
			t.Fatalf("request %d rejected while limiter is disabled: %d", i, w.Code)
		}
	}
}

func TestRelayConcurrencyLimit_RejectsOverTokenCap(t *testing.T) {
	withoutRedis(t)
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TOKEN", "2")

	block := make(chan struct{})
	r := ccTestRouter(t, 7, "", block)

	// Two requests occupy both slots and stay open.
	var wg sync.WaitGroup
	started := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			if w := ccDo(r); w.Code != http.StatusOK {
				t.Errorf("in-flight request should have been admitted, got %d", w.Code)
			}
		}()
	}
	for i := 0; i < 2; i++ {
		<-started
	}
	// Give both goroutines time to pass admission and park in the handler.
	waitForLocalSlots(t, "cc:tok:7", 2)

	// Third request while the first two are still open → rejected.
	w := ccDo(r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("third concurrent request: status = %d, want 429", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, concurrencyLimitErrorCode) {
		t.Errorf("429 body must carry %q so clients can distinguish it from RPM: %s",
			concurrencyLimitErrorCode, body)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Error("429 must carry Retry-After")
	}
	if scope := w.Header().Get("X-RateLimit-Scope"); scope != "token" {
		t.Errorf("X-RateLimit-Scope = %q, want token", scope)
	}
	if typ := w.Header().Get("X-RateLimit-Type"); typ != "concurrency" {
		t.Errorf("X-RateLimit-Type = %q, want concurrency", typ)
	}

	close(block)
	wg.Wait()

	// Slots are released when the requests finish — the next one is admitted.
	waitForLocalSlots(t, "cc:tok:7", 0)
	r2 := ccTestRouter(t, 7, "", nil)
	if w := ccDo(r2); w.Code != http.StatusOK {
		t.Fatalf("after in-flight requests completed: status = %d, want 200", w.Code)
	}
}

func TestRelayConcurrencyLimit_TenantDimensionIndependent(t *testing.T) {
	withoutRedis(t)
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TENANT", "1")

	block := make(chan struct{})
	// Two DIFFERENT tokens under the SAME tenant: the tenant cap must still
	// bind, otherwise a tenant could bypass it by minting more tokens.
	r1 := ccTestRouter(t, 1, "acme", block)
	r2 := ccTestRouter(t, 2, "acme", nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ccDo(r1)
	}()
	waitForLocalSlots(t, "cc:tenant:acme", 1)

	w2 := ccDo(r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("second token under the same tenant: status = %d, want 429", w2.Code)
	}
	if scope := w2.Header().Get("X-RateLimit-Scope"); scope != "tenant" {
		t.Errorf("X-RateLimit-Scope = %q, want tenant", scope)
	}

	// A different tenant is unaffected.
	r3 := ccTestRouter(t, 3, "other", nil)
	if w := ccDo(r3); w.Code != http.StatusOK {
		t.Errorf("unrelated tenant must not be blocked: status = %d", w.Code)
	}

	close(block)
	wg.Wait()
}

func TestRelayConcurrencyLimit_NoTokenContextPassesThrough(t *testing.T) {
	withoutRedis(t)
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TOKEN", "1")

	// Unauthenticated / pre-auth paths carry no token_id; they must not be
	// funnelled into a single shared slot.
	r := ccTestRouter(t, 0, "", nil)
	for i := 0; i < 3; i++ {
		if w := ccDo(r); w.Code != http.StatusOK {
			t.Fatalf("request %d without token context was rejected: %d", i, w.Code)
		}
	}
}

// A crashed replica leaves its lease behind; the next admission must reclaim it
// rather than pinning the limit forever.
func TestRelayConcurrencyLimit_ExpiredLeaseIsReclaimed(t *testing.T) {
	withoutRedis(t)
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TOKEN", "1")
	t.Setenv("RELAY_CONCURRENCY_LEASE_TTL", "1")

	// Seed a stale lease as if a replica died mid-request.
	ccLocalMu.Lock()
	ccLocalSlots["cc:tok:7"] = map[string]time.Time{
		"dead-replica-lease": time.Now().Add(-10 * time.Second),
	}
	ccLocalMu.Unlock()

	r := ccTestRouter(t, 7, "", nil)
	if w := ccDo(r); w.Code != http.StatusOK {
		t.Fatalf("stale lease was not reclaimed: status = %d, want 200", w.Code)
	}
}

func TestRelayConcurrencyLimit_ZeroLimitDisablesDimension(t *testing.T) {
	withoutRedis(t)
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TOKEN", "0")
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TENANT", "0")

	block := make(chan struct{})
	r := ccTestRouter(t, 7, "acme", block)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := ccDo(r); w.Code != http.StatusOK {
				t.Errorf("explicit 0 must disable the cap, got %d", w.Code)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(block)
	wg.Wait()
}

// TestRelayConcurrencyLimit_ClearsStaleHeadroomReset is the item-3 lock for
// this dimension: concurrency has no per-minute window, so ccReject has no
// Reset value of its own to overwrite one an earlier check in the same
// chain (BusinessRateLimit) already wrote. A stale Reset from that earlier,
// unrelated admit-path snapshot must not survive next to this 429.
func TestRelayConcurrencyLimit_ClearsStaleHeadroomReset(t *testing.T) {
	withoutRedis(t)
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TOKEN", "1")

	block := make(chan struct{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenId, 7)
		// Simulate BusinessRateLimit's admit-path write from earlier in the
		// SAME chain (rate-limit.go:setRateLimitHeadroomHeaders) — an RPM
		// check's Reset ~60s in the future.
		c.Writer.Header().Set("X-RateLimit-Reset", "9999999999")
		c.Writer.Header().Set("X-RateLimit-Scope", "token")
		c.Writer.Header().Set("X-RateLimit-Type", "rpm")
		c.Next()
	})
	r.Use(RelayConcurrencyLimit())
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		<-block
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ccDo(r) // occupies the one slot, held open by block
	}()
	waitForLocalSlots(t, "cc:tok:7", 1)

	w := ccDo(r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second concurrent request = %d, want 429", w.Code)
	}
	if reset := w.Header().Get("X-RateLimit-Reset"); reset != "" {
		t.Errorf("429 X-RateLimit-Reset = %q, want absent (stale admit-path snapshot must be cleared)", reset)
	}
	if scope := w.Header().Get("X-RateLimit-Scope"); scope != "token" {
		t.Errorf("429 X-RateLimit-Scope = %q, want token (this reject's own value, not the stale rpm check's)", scope)
	}
	if typ := w.Header().Get("X-RateLimit-Type"); typ != "concurrency" {
		t.Errorf("429 X-RateLimit-Type = %q, want concurrency (this reject's own value, not the stale rpm check's)", typ)
	}

	close(block)
	wg.Wait()
}

// waitForLocalSlots blocks until the in-process slot table holds want entries
// for key, so tests never race the goroutines they just launched.
func waitForLocalSlots(t *testing.T, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ccLocalMu.Lock()
		got := len(ccLocalSlots[key])
		ccLocalMu.Unlock()
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	ccLocalMu.Lock()
	got := len(ccLocalSlots[key])
	ccLocalMu.Unlock()
	t.Fatalf("timed out waiting for %s to hold %d slots, got %d", key, want, got)
}
