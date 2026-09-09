package middleware

// rate_limit_message_lock_test.go — L6 wire-contract lock: the gateway
// rate-limit 429s exercised here (BusinessRateLimit's token and tenant
// rpm+tpm plus model rpm, RelayConcurrencyLimit's token/tenant dimensions,
// and both backends of ModelRequestRateLimit's user dimension, each with its
// success- and total-count branch) answer error.message in English with one shared,
// greppable shape — a foreign integrator must not have to special-case a
// Chinese sentence to read the limit and the code. Drives the real reject
// sites through their existing harnesses (business_rate_limit_test.go,
// business_rate_limit_tpm_test.go, rate_limit_headroom_test.go,
// concurrency_limit_test.go); asserts the wire shape, not handler internals.
// Status codes, error.code, and the X-RateLimit-*/Retry-After headers are
// locked elsewhere (abort_code_structural_test.go, rate_limit_headroom_test.go);
// this file only pins the message text.
//
// Every call to assertEnglishRateLimitMessage records the caller-supplied
// site name into rlMessageSitesSeen; requireRateLimitMessageSitesFloorMet
// (invoked from TestMain in async_seam_test.go, after m.Run()) fails the
// whole test binary if fewer than rlMessageSitesFloor distinct sites were
// exercised — so a future edit that silently guts one of these Test
// functions (or an -run filter someone forgets is not "the full package")
// cannot leave this file looking green while covering nothing.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-gonic/gin"
)

// rateLimitMessageShape is the one shape every gateway rate-limit 429
// message must match: "<scope> ... limit exceeded: <n> ... (<code>)".
var rateLimitMessageShape = regexp.MustCompile(
	`^(token|tenant|model|user) .+ limit exceeded: \d+ .+\((request_rate_limit_exceeded|business_rate_limit_exceeded|concurrency_limit_exceeded)\)`)

type rateLimitLockBody struct {
	Error struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// rlMessageSitesFloor is the scanner-honesty floor for
// requireRateLimitMessageSitesFloorMet: today's Test functions in this file
// drive eleven distinct named sites — token rpm/tpm, tenant rpm/tpm, model
// rpm, concurrency token/tenant, user redis success/total, user memory
// success/total. The floor is set below that count so it does not need
// bumping on every addition; it exists to catch a REGRESSION (a site
// silently dropped or de-fanged), not to pin the current total.
const rlMessageSitesFloor = 9

var (
	rlMessageSitesMu   sync.Mutex
	rlMessageSitesSeen = map[string]bool{}
)

// requireRateLimitMessageSitesFloorMet is called from TestMain
// (async_seam_test.go) after m.Run() returns, so it sees every site any
// test in a full `go test ./internal/adapter/middleware/` run reached. It
// intentionally does not receive a *testing.T (TestMain has none to give
// it that would still report through go test's normal machinery once
// m.Run() has already returned) — a floor miss prints to stderr and the
// caller in TestMain turns it into a non-zero exit code.
func requireRateLimitMessageSitesFloorMet() (ok bool, seen int, sites []string) {
	rlMessageSitesMu.Lock()
	defer rlMessageSitesMu.Unlock()
	for s := range rlMessageSitesSeen {
		sites = append(sites, s)
	}
	return len(rlMessageSitesSeen) >= rlMessageSitesFloor, len(rlMessageSitesSeen), sites
}

// assertEnglishRateLimitMessage decodes a 429 response and checks the
// shared wire contract: ASCII-only, matches rateLimitMessageShape, the
// leading scope word equals X-RateLimit-Scope, and the parenthetical code
// equals error.code. site is a short unique identifier for the reject site
// under test (e.g. "token_rpm") — recorded for the package-wide floor
// check, not asserted against anything itself.
func assertEnglishRateLimitMessage(t *testing.T, w *httptest.ResponseRecorder, wantScope, site string) {
	t.Helper()
	rlMessageSitesMu.Lock()
	rlMessageSitesSeen[site] = true
	rlMessageSitesMu.Unlock()

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
	}
	var body rateLimitLockBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("429 body not JSON: %v (%s)", err, w.Body.String())
	}
	if body.Error.Message == "" {
		t.Fatal("error.message is empty")
	}
	for _, r := range body.Error.Message {
		if r >= 0x80 {
			t.Fatalf("error.message contains non-ASCII rune %q: %s", r, body.Error.Message)
		}
	}
	m := rateLimitMessageShape.FindStringSubmatch(body.Error.Message)
	if m == nil {
		t.Fatalf("error.message = %q, want shape %q", body.Error.Message, rateLimitMessageShape.String())
	}
	if m[1] != wantScope {
		t.Errorf("message leading scope word = %q, want %q", m[1], wantScope)
	}
	if m[2] != body.Error.Code {
		t.Errorf("message code %q != error.code %q", m[2], body.Error.Code)
	}
	if scope := w.Header().Get("X-RateLimit-Scope"); scope != wantScope {
		t.Errorf("X-RateLimit-Scope = %q, want %q", scope, wantScope)
	}
}

// TestRateLimitMessage_TokenRPM_English drives BusinessRateLimit's token-rpm
// reject (bizReject, business_rate_limit.go).
func TestRateLimitMessage_TokenRPM_English(t *testing.T) {
	tokenBase := 91000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tok := tokenBase
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{RPM: 1}, Tenant: "t-msg-lock"},
		}, nil)
		if w := runBizRL(tok); w.Code != http.StatusOK {
			t.Fatalf("first request = %d, want 200", w.Code)
		}
		assertEnglishRateLimitMessage(t, runBizRL(tok), "token", "token_rpm")
	})
}

// TestRateLimitMessage_TokenTPM_English drives BusinessRateLimit's token-tpm
// reject (bizTPMAdmit -> bizReject, business_rate_limit.go) and pins the
// "tokens" measure word explicitly — the shared shape regex's ".+" between
// the scope and "limit exceeded" would also accept "requests", so this is
// the only place the tpm-vs-rpm word choice (business_rate_limit.go:394-396)
// is actually checked.
func TestRateLimitMessage_TokenTPM_English(t *testing.T) {
	tokenBase := 92000
	eachBizBackend(t, func(t *testing.T) {
		tokenBase++
		tok := tokenBase
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{TPM: 10}, Tenant: ""},
		}, nil)
		bizFreezeClocks(t)
		app.RecordBusinessTPMUsage(tok, "", 20) // already over the 10 limit
		w := runBizRLEstimate(tok, 0)
		assertEnglishRateLimitMessage(t, w, "token", "token_tpm")
		var body rateLimitLockBody
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("429 body not JSON: %v (%s)", err, w.Body.String())
		}
		if !strings.Contains(body.Error.Message, "tokens") {
			t.Errorf("tpm error.message = %q, want it to contain %q", body.Error.Message, "tokens")
		}
	})
}

// TestRateLimitMessage_TenantRPM_English drives BusinessRateLimit's
// tenant-rpm reject (bizReject at business_rate_limit.go:527) — the token
// dimension is left unlimited so only the tenant check can trip.
func TestRateLimitMessage_TenantRPM_English(t *testing.T) {
	seq := 0
	eachBizBackend(t, func(t *testing.T) {
		seq++
		tenant := "t-msg-tenant-rpm-" + strconv.Itoa(seq)
		tok := 97000 + seq
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{}, Tenant: tenant},
		}, map[string]bizRateLimits{
			tenant: {RPM: 1},
		})
		if w := runBizRL(tok); w.Code != http.StatusOK {
			t.Fatalf("first request = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		assertEnglishRateLimitMessage(t, runBizRL(tok), "tenant", "tenant_rpm")
	})
}

// TestRateLimitMessage_TenantTPM_English drives BusinessRateLimit's
// tenant-tpm reject (bizTPMAdmit -> bizReject at business_rate_limit.go:466)
// — the token dimension is left unlimited so only the tenant TPM window
// (recorded with tokenID=0, i.e. token-side skipped) can trip.
func TestRateLimitMessage_TenantTPM_English(t *testing.T) {
	seq := 0
	eachBizBackend(t, func(t *testing.T) {
		seq++
		tenant := "t-msg-tenant-tpm-" + strconv.Itoa(seq)
		tok := 98000 + seq
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{}, Tenant: tenant},
		}, map[string]bizRateLimits{
			tenant: {TPM: 10},
		})
		bizFreezeClocks(t)
		app.RecordBusinessTPMUsage(0, tenant, 20) // tenant-only, already over the 10 limit
		assertEnglishRateLimitMessage(t, runBizRLEstimate(tok, 0), "tenant", "tenant_tpm")
	})
}

// TestRateLimitMessage_ModelRPM_English drives BusinessModelRateLimit's
// model-rpm reject (bizReject, business_model_rate_limit.go), chained after
// BusinessRateLimit in real router order via runBizChainRL.
func TestRateLimitMessage_ModelRPM_English(t *testing.T) {
	seq := 0
	eachBizBackend(t, func(t *testing.T) {
		seq++
		tenant := "t-msg-model-" + string(rune('a'+seq))
		tok := 93000 + seq
		bizTestLimits(t, map[int]struct {
			L      bizRateLimits
			Tenant string
		}{
			tok: {L: bizRateLimits{}, Tenant: tenant},
		}, nil)
		bizModelTestLimits(t, map[string]bizRateLimits{
			tenant + "|m-msg-lock": {RPM: 1},
		})
		if w := runBizChainRL(tok, "m-msg-lock"); w.Code != http.StatusOK {
			t.Fatalf("first request = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		assertEnglishRateLimitMessage(t, runBizChainRL(tok, "m-msg-lock"), "model", "model_rpm")
	})
}

// TestRateLimitMessage_Concurrency_English drives RelayConcurrencyLimit's
// token-dimension reject (ccReject, concurrency_limit.go).
func TestRateLimitMessage_Concurrency_English(t *testing.T) {
	withoutRedis(t)
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TOKEN", "1")

	block := make(chan struct{})
	r := ccTestRouter(t, 94001, "", block)

	started := make(chan struct{})
	go func() {
		started <- struct{}{}
		ccDo(r)
	}()
	<-started
	waitForLocalSlots(t, "cc:tok:94001", 1)

	assertEnglishRateLimitMessage(t, ccDo(r), "token", "concurrency_token")
	close(block)
}

// TestRateLimitMessage_TenantConcurrency_English drives
// RelayConcurrencyLimit's tenant-dimension reject (ccReject at
// concurrency_limit.go:257) — no per-token limit is set, so only the
// tenant slot can be exhausted.
func TestRateLimitMessage_TenantConcurrency_English(t *testing.T) {
	withoutRedis(t)
	t.Setenv("RELAY_MAX_CONCURRENT_PER_TENANT", "1")

	block := make(chan struct{})
	r := ccTestRouter(t, 0, "t-msg-cc-tenant", block)

	started := make(chan struct{})
	go func() {
		started <- struct{}{}
		ccDo(r)
	}()
	<-started
	waitForLocalSlots(t, "cc:tenant:t-msg-cc-tenant", 1)

	assertEnglishRateLimitMessage(t, ccDo(r), "tenant", "concurrency_tenant")
	close(block)
}

// TestRateLimitMessage_UserRedis_English drives ModelRequestRateLimit's
// redis-backed user success-count dimension (redisRateLimitHandler,
// model-rate-limit.go) through the real dispatcher against miniredis —
// same fixture shape as TestModelRateLimit_Redis_SuccessCount_Miniredis
// (redis_miniredis_cover_test.go).
func TestRateLimitMessage_UserRedis_English(t *testing.T) {
	_, _, cleanup := withMiniRedis(t)
	defer cleanup()
	setModelRateLimit(t, true /*redis*/, 0 /*skip total*/, 1 /*success*/)

	const uid = 95001
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", uid); c.Next() })
	r.GET("/m", ModelRequestRateLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	do := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
		return w
	}

	if w := do(); w.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	assertEnglishRateLimitMessage(t, do(), "user", "user_redis_success")
}

// TestRateLimitMessage_UserRedisTotalCount_English drives
// ModelRequestRateLimit's redis-backed user TOTAL-count dimension
// (redisRateLimitHandler's token-bucket branch, model-rate-limit.go:229) —
// the success-count check above only ever exercises the success branch
// (:200); this is the only lock on the message text at :229. The
// token-bucket limiter can admit a short burst before it starts denying
// (same behaviour TestModelRateLimit_Redis_TotalCount_MiniRedis works
// around), so this loops a bounded number of times for the first 429
// instead of asserting it lands on request 2.
func TestRateLimitMessage_UserRedisTotalCount_English(t *testing.T) {
	_, _, cleanup := withMiniRedis(t)
	defer cleanup()
	setModelRateLimit(t, true /*redis*/, 1 /*total budget 1*/, 1000 /*success high*/)

	const uid = 95101
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", uid); c.Next() })
	r.GET("/m", ModelRequestRateLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	do := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
		return w
	}

	var last *httptest.ResponseRecorder
	for i := 0; i < 10; i++ {
		last = do()
		if last.Code == http.StatusTooManyRequests {
			break
		}
	}
	assertEnglishRateLimitMessage(t, last, "user", "user_redis_total")
}

// TestRateLimitMessage_UserMemory_English is the RED-on-HEAD case: the
// memory backend's success-count reject branch wrote a bare 429 status with
// NO body (memoryRateLimitHandler, model-rate-limit.go) — this asserts the
// same wire-native JSON envelope the redis twin already carries. Mounts the
// handler directly (not through ModelRequestRateLimit's dispatcher) per the
// lane oracle so the assertion is unambiguous about which branch fired.
func TestRateLimitMessage_UserMemory_English(t *testing.T) {
	prevRedis := common.RedisEnabled
	prevDuration := setting.ModelRequestRateLimitDurationMinutes
	common.RedisEnabled = false
	setting.ModelRequestRateLimitDurationMinutes = 1
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		setting.ModelRequestRateLimitDurationMinutes = prevDuration
	})

	const uid = 96001
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", uid); c.Next() })
	r.GET("/m", memoryRateLimitHandler(60, 0, 1), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	do := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
		return w
	}

	if w := do(); w.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	w := do()
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("429 response Content-Type = %q, want prefix %q", got, "application/json")
	}
	if w.Body.Len() == 0 {
		t.Fatal("429 body is empty — memory backend must carry the same wire envelope as the redis twin")
	}
	assertEnglishRateLimitMessage(t, w, "user", "user_memory_success")
}

// TestRateLimitMessage_UserMemoryTotalCount_English is the total-count
// sibling of TestRateLimitMessage_UserMemory_English: the memory backend's
// TOTAL-count reject branch (model-rate-limit.go:270-284) is a separate
// `if` from the success-count branch above and was not covered by any body
// assertion — a revert of just that branch's abortWithOpenAiMessage call
// left the whole middleware package green.
func TestRateLimitMessage_UserMemoryTotalCount_English(t *testing.T) {
	prevRedis := common.RedisEnabled
	prevDuration := setting.ModelRequestRateLimitDurationMinutes
	common.RedisEnabled = false
	setting.ModelRequestRateLimitDurationMinutes = 1
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		setting.ModelRequestRateLimitDurationMinutes = prevDuration
	})

	const uid = 96101
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", uid); c.Next() })
	// totalMaxCount=1, successMaxCount kept high so only the total-count
	// branch can trip.
	r.GET("/m", memoryRateLimitHandler(60, 1, 1000), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	do := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
		return w
	}

	if w := do(); w.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	w := do()
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("429 response Content-Type = %q, want prefix %q", got, "application/json")
	}
	if w.Body.Len() == 0 {
		t.Fatal("429 body is empty — memory backend's total-count branch must carry the same wire envelope as its success-count sibling")
	}
	assertEnglishRateLimitMessage(t, w, "user", "user_memory_total")
}
