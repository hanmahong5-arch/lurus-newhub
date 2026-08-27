package router

// r6a_rate_limit_mount_test.go — wiring lock for operator decision D2/G6
// (2026-08-27): proves middleware.ModelRequestRateLimit() is actually
// mounted on the production /v1 relay chain and that the mount actually
// rejects a request once its window is exhausted, not merely that the
// middleware exists somewhere in the tree.
//
// Why this test and not internal/adapter/middleware/r5e_model_rate_limit_default_test.go:
// that test builds its own gin.Engine and calls .Use(ModelRequestRateLimit())
// directly — a hand-copy of relay-router.go's wiring, not the wiring itself.
// Grep-confirmed (2026-08-26 ledger): deleting
// `relayV1Router.Use(middleware.ModelRequestRateLimit())` at
// relay-router.go:81 leaves all three of those tests green, because they
// never go through SetRelayRouter. This test does: it calls SetRelayRouter,
// the same function cmd/server/main.go reaches via SetRouter, so a future
// route-registration regression here is the same regression production ships.
// Verified by mutation while writing this test: commenting out that same
// Use() call in relay-router.go makes
// TestSetRelayRouter_ModelRequestRateLimit_MountedAndRejects fail (second
// request no longer 429) while every other test in this package still
// passes; the line was restored immediately after (relay-router.go is not on
// this lane's file list).
//
// What this test does NOT prove: per-token rpm/tpm limits
// (middleware/business_rate_limit.go) are a SEPARATE dimension, still
// zero-by-default on every live token as of 2026-08-26 UAT G6 — arming
// ModelRequestRateLimitEnabled does not give those tokens a per-token
// ceiling, only the global one this test locks (see setting/rate_limit.go's
// doc comment on ModelRequestRateLimitEnabled).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var r6aRateLimitMountDBCounter atomic.Int64

// r6aSeedRateLimitToken sets up an isolated in-memory SQLite DB (mirrors the
// setupCoverDB/seedUserToken pattern established in
// internal/adapter/middleware) with a single enabled user + unlimited-quota
// token, wired into repo.DB. Returns the bearer key and a cleanup func that
// restores every global it touched.
func r6aSeedRateLimitToken(t *testing.T) (key string, cleanup func()) {
	t.Helper()

	// One fresh sequence number per call, used for BOTH the SQLite file name and
	// the user's primary key. The user id matters as much as the DB name:
	// middleware's inMemoryRateLimiter is a package-level singleton whose bucket
	// key is derived from the user id (model-rate-limit.go:224-225), and it
	// outlives repo.DB teardown. Letting SQLite auto-assign would hand every run
	// id=1, so the buckets would carry over and the second run would see its
	// first request already throttled. Reproduced before this fix:
	// `go test -count=2 -run TestSetRelayRouter_ModelRequestRateLimit_MountedAndRejects`
	// failed the second iteration at the "fixture broken" guard below.
	seq := r6aRateLimitMountDBCounter.Add(1)

	dbName := fmt.Sprintf("file:r6a_rate_limit_mount_%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Token{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	user := &repo.User{
		Id:       6885031 + int(seq),
		Username: fmt.Sprintf("r6a-ratelimit-user-%d", seq), DisplayName: "R6A Rate Limit User",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: fmt.Sprintf("r6a-ratelimit-%d@local", seq), TenantId: "default", Quota: 1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tokenKey := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: tokenKey, Status: common.TokenStatusEnabled,
		Name: "r6a-ratelimit-token", CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, UnlimitedQuota: true,
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	return tokenKey, func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

func TestSetRelayRouter_ModelRequestRateLimit_MountedAndRejects(t *testing.T) {
	key, cleanup := r6aSeedRateLimitToken(t)
	defer cleanup()

	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	prevDuration := setting.ModelRequestRateLimitDurationMinutes
	setting.ModelRequestRateLimitEnabled = true
	// Total-request dimension: the memory backend (RedisEnabled=false, set by
	// r6aSeedRateLimitToken) increments this on every admitted request
	// regardless of outcome (see setting/rate_limit.go's doc comment), so
	// capping at 1 makes the SECOND request in this test trip it
	// deterministically without depending on what the first request's
	// downstream handler does.
	setting.ModelRequestRateLimitCount = 1
	setting.ModelRequestRateLimitSuccessCount = 0
	setting.ModelRequestRateLimitDurationMinutes = 1
	defer func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
		setting.ModelRequestRateLimitDurationMinutes = prevDuration
	}()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	authHeader := "Bearer sk-" + key

	// First request: TokenAuth must pass (real token row) and
	// ModelRequestRateLimit must admit it (window not yet exhausted). What
	// happens further downstream (Distribute has no channel to pick, no
	// upstream configured) is irrelevant to this lock — only asserting it is
	// NOT a 429 from the rate limiter, so the second request's 429 is
	// unambiguous.
	req1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req1.Header.Set("Authorization", authHeader)
	w1 := httptest.NewRecorder()
	engine.ServeHTTP(w1, req1)
	if w1.Code == http.StatusTooManyRequests {
		t.Fatalf("first request already got 429 — fixture broken (expected the rate-limit window to admit exactly one request before tripping); body=%s", w1.Body.String())
	}

	// Second request, same token, same window: must be rejected by
	// ModelRequestRateLimit specifically, proving it is both mounted on the
	// real /v1 chain (relay-router.go's relayV1Router.Use) and functionally
	// enforcing, not just present as dead code.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req2.Header.Set("Authorization", authHeader)
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status=%d, want 429 (ModelRequestRateLimit must be mounted on the /v1 chain and actually reject once its window is exhausted); body=%s", w2.Code, w2.Body.String())
	}
	// The memory backend's total-count branch (model-rate-limit.go's
	// memoryRateLimitHandler) sets status+headers via setRateLimitResponseHeaders
	// but writes no JSON body (unlike the Redis backend's abortWithOpenAiMessage
	// path) — so the fingerprint that this 429 came from ModelRequestRateLimit,
	// and not some other limiter later in the chain, is the X-RateLimit-Limit
	// header carrying the exact cap we configured (1). BusinessRateLimit never
	// runs for this request at all: ModelRequestRateLimit aborts the chain
	// before c.Next() reaches it.
	if got := w2.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("second request X-RateLimit-Limit=%q, want \"1\" — a 429 from a different limiter earlier in the chain would false-pass a bare status-code check", got)
	}
}

// TestSetRelayRouter_ModelRequestRateLimit_MountedOnGeminiChain locks the OTHER
// production mount point. setting/rate_limit.go's doc comment on
// ModelRequestRateLimitEnabled claims the ceiling covers the /v1 and /v1beta
// chains; without this test only the /v1 half of that sentence was observed —
// commenting out relay-router.go:210
// (`relayGeminiRouter.Use(middleware.ModelRequestRateLimit())`) left this whole
// package green, which is exactly the shape of hole this round exists to close.
//
// Same fixture and same 429 fingerprint as the /v1 test above; the only
// difference is the request path, which must resolve through
// relayGeminiRouter's `POST /models/*path` registration (relay-router.go:214).
func TestSetRelayRouter_ModelRequestRateLimit_MountedOnGeminiChain(t *testing.T) {
	key, cleanup := r6aSeedRateLimitToken(t)
	defer cleanup()

	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	prevDuration := setting.ModelRequestRateLimitDurationMinutes
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitCount = 1
	setting.ModelRequestRateLimitSuccessCount = 0
	setting.ModelRequestRateLimitDurationMinutes = 1
	defer func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
		setting.ModelRequestRateLimitDurationMinutes = prevDuration
	}()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	authHeader := "Bearer sk-" + key
	const geminiPath = "/v1beta/models/gemini-2.0-flash:generateContent"

	req1 := httptest.NewRequest(http.MethodPost, geminiPath, nil)
	req1.Header.Set("Authorization", authHeader)
	w1 := httptest.NewRecorder()
	engine.ServeHTTP(w1, req1)
	if w1.Code == http.StatusNotFound {
		t.Fatalf("first request 404 — %s does not resolve to relayGeminiRouter, so this test would prove nothing about its middleware chain", geminiPath)
	}
	if w1.Code == http.StatusTooManyRequests {
		t.Fatalf("first request already got 429 — fixture broken; body=%s", w1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, geminiPath, nil)
	req2.Header.Set("Authorization", authHeader)
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status=%d, want 429 (ModelRequestRateLimit must be mounted on the /v1beta chain too, per relay-router.go:210); body=%s", w2.Code, w2.Body.String())
	}
	if got := w2.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("second request X-RateLimit-Limit=%q, want \"1\"", got)
	}
}
