package router

// b1_gemini_chain_mount_test.go — ITEM B1 mount locks for the /v1beta native
// Gemini relay group.
//
// r6a_rate_limit_mount_test.go already locks ModelRequestRateLimit on this
// chain (TestSetRelayRouter_ModelRequestRateLimit_MountedOnGeminiChain); this
// file extends the same "drive the real SetRelayRouter, assert a rejection
// fingerprinted to the exact middleware" style to the five middlewares that
// were missing from the group before this fix: CostSpikeLimit,
// EntitlementCheck, BusinessRateLimit, RelayConcurrencyLimit and
// BusinessModelRateLimit. Before the fix /v1beta mounted only
// TokenAuth+PoolBalanceCheck+ModelRequestRateLimit+Distribute — none of these
// five ceilings applied to the native Gemini path at all, even though every
// other billed relay group (/v1, /mj, /suno, /v1/audio/music) carries the
// full chain (see relay-router.go's invariant comment above relayGeminiRouter
// and task_route_rate_limit_mount_test.go for the earlier /mj+/suno round of
// this same gap).
//
// Mutation-verified while writing this file: commenting out each
// relayGeminiRouter.Use(middleware.X()) call (or, for BusinessModelRateLimit,
// removing it from geminiHTTPRouter.Use) in relay-router.go turned the
// matching test below red while every other test in this package stayed
// green; each line was restored immediately after and confirmed present via
// grep (see mutation_evidence in the task's structured output).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var b1DBCounter atomic.Int64

// b1Fixture bundles the seeded identity a test needs to drive requests
// through the real SetRelayRouter chain.
type b1Fixture struct {
	AuthHeader string
	TokenID    int
	UserID     int
}

// b1SeedGeminiToken mirrors r6aSeedRateLimitToken (r6a_rate_limit_mount_test.go)
// — same isolated in-memory SQLite + repo.DB wiring, same per-call sequence
// number so the middleware package's package-level singletons (rate limit
// buckets, entitlement cache) never collide across runs — but exposes the
// numeric token/user ids and lets the caller set the token's RateLimitRPM /
// IdentityAccountID columns, the dimensions BusinessRateLimit and
// EntitlementCheck key off, which r6aSeedRateLimitToken's fixed shape does
// not need.
func b1SeedGeminiToken(t *testing.T, rpmLimit int, identityAccountID int64) (fx b1Fixture, cleanup func()) {
	t.Helper()

	seq := b1DBCounter.Add(1)
	dbName := fmt.Sprintf("file:b1_gemini_chain_%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Token{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	user := &repo.User{
		Id:       6890001 + int(seq),
		Username: fmt.Sprintf("b1-gemini-user-%d", seq), DisplayName: "B1 Gemini Chain User",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: fmt.Sprintf("b1-gemini-%d@local", seq), TenantId: "default", Quota: 1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tokenKey := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: tokenKey, Status: common.TokenStatusEnabled,
		Name: "b1-gemini-token", CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, UnlimitedQuota: true,
		RateLimitRPM: rpmLimit, IdentityAccountID: identityAccountID,
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

	fx = b1Fixture{AuthHeader: "Bearer sk-" + tokenKey, TokenID: tok.Id, UserID: user.Id}
	cleanup = func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
	return fx, cleanup
}

// b1NewEngine builds the real production router (gin.TestMode + the actual
// SetRelayRouter, not a hand-copy of its wiring).
func b1NewEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)
	return engine
}

const b1GeminiPath = "/v1beta/models/gemini-2.0-flash:generateContent"

// b1Do fires one authenticated POST against path and returns the recorder.
func b1Do(engine *gin.Engine, authHeader, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", authHeader)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// TestSetRelayRouter_BusinessRateLimit_MountedOnGeminiChain locks
// middleware.BusinessRateLimit(): a token with RateLimitRPM=1 must be
// rejected on its second /v1beta request within the 1-minute window, with
// the 令牌-scoped message that distinguishes it from ModelRequestRateLimit
// (already locked on this chain) or any other limiter.
//
// Fingerprint changed from the "business_rate_limit_exceeded" OpenAI-wire
// error code to this message substring by the wire-native-envelope fix
// (renderRejection): /v1beta rejections now answer in Gemini's own error
// shape, which carries a numeric HTTP code and a message, not New API's
// internal string reason-code — so that code no longer appears on this wire
// at all, by design (see internal/adapter/middleware/wire_format.go).
func TestSetRelayRouter_BusinessRateLimit_MountedOnGeminiChain(t *testing.T) {
	fx, cleanup := b1SeedGeminiToken(t, 1 /* rpmLimit */, 0)
	defer cleanup()

	engine := b1NewEngine()

	w1 := b1Do(engine, fx.AuthHeader, b1GeminiPath)
	if w1.Code == http.StatusTooManyRequests {
		t.Fatalf("first request already 429 — fixture broken; body=%s", w1.Body.String())
	}

	w2 := b1Do(engine, fx.AuthHeader, b1GeminiPath)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status=%d, want 429 (BusinessRateLimit must be mounted on the /v1beta chain); body=%s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "令牌每分钟请求数已达上限") {
		t.Fatalf("second request body=%s, want the token-scoped rate-limit message — a 429 from a different limiter earlier in the chain would false-pass a bare status check", w2.Body.String())
	}
}

// TestSetRelayRouter_EntitlementCheck_MountedOnGeminiChain locks
// middleware.EntitlementCheck(): a token whose owning identity account has
// quota_remaining=0 (per a local stand-in for the platform entitlement
// service, wired via common.IdentityServiceURL — the same override point
// internal/pkg/common/billing_cache_refresh_test.go uses) must be rejected on
// its very first /v1beta request, before any rate window has a chance to
// build up.
func TestSetRelayRouter_EntitlementCheck_MountedOnGeminiChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"quota_remaining":"0"}`))
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	defer func() { common.IdentityServiceURL = prevURL }()

	// Distinct, never-reused account id: EntitlementCheck's cache is a
	// package-level sync.Map keyed by account id that outlives this test —
	// b1DBCounter (shared with b1SeedGeminiToken) guarantees no other test in
	// this run picks the same id.
	accountID := int64(9_100_000_000) + b1DBCounter.Add(1)
	fx, cleanup := b1SeedGeminiToken(t, 0, accountID)
	defer cleanup()

	engine := b1NewEngine()

	w := b1Do(engine, fx.AuthHeader, b1GeminiPath)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429 (EntitlementCheck must be mounted on the /v1beta chain and reject a quota_remaining=0 account); body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "quota_exceeded") {
		t.Fatalf("body=%s, want the quota_exceeded code — a 429 from a different limiter would false-pass a bare status check", w.Body.String())
	}
}

// TestSetRelayRouter_CostSpikeLimit_MountedOnGeminiChain locks
// middleware.CostSpikeLimit(): a user whose 5-minute Redis window (seeded
// directly under app.CostSpikeKeyPrefix, the same key QueryCostSpikeWindow
// reads) already exceeds the hard limit must be rejected — and, in enforce
// mode, disabled — on the very first /v1beta request.
func TestSetRelayRouter_CostSpikeLimit_MountedOnGeminiChain(t *testing.T) {
	fx, cleanup := b1SeedGeminiToken(t, 0, 0)
	defer cleanup()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	prevRedisEnabled := common.RedisEnabled
	prevRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = rdb
	defer func() {
		common.RedisEnabled = prevRedisEnabled
		common.RDB = prevRDB
	}()

	prevProtected := common.CostSpikeProtectionEnabled
	prevLimit := common.CostSpikeHardLimitPer5Min
	prevEnforce := common.CostSpikeEnforce
	common.CostSpikeProtectionEnabled = true
	common.CostSpikeHardLimitPer5Min = 100
	common.CostSpikeEnforce = true
	defer func() {
		common.CostSpikeProtectionEnabled = prevProtected
		common.CostSpikeHardLimitPer5Min = prevLimit
		common.CostSpikeEnforce = prevEnforce
	}()

	// Seed the window directly above the limit — no need for a real relay
	// call to have happened first.
	nowMs := time.Now().UnixMilli()
	key := app.CostSpikeKeyPrefix + strconv.Itoa(fx.UserID)
	if err := rdb.ZAdd(context.Background(), key, redis.Z{
		Score:  float64(nowMs),
		Member: fmt.Sprintf("%d:%d", nowMs, 999_999), // >> 100 limit
	}).Err(); err != nil {
		t.Fatalf("seed cost-spike window: %v", err)
	}

	engine := b1NewEngine()

	w := b1Do(engine, fx.AuthHeader, b1GeminiPath)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429 (CostSpikeLimit must be mounted on the /v1beta chain); body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cost_spike_limit_exceeded") {
		t.Fatalf("body=%s, want the cost_spike_limit_exceeded code — a 429 from a different limiter would false-pass a bare status check", w.Body.String())
	}
}

// TestSetRelayRouter_RelayConcurrencyLimit_MountedOnGeminiChain locks
// middleware.RelayConcurrencyLimit(). Rather than racing two real concurrent
// requests (inherently timing-dependent — the middleware's own lease is
// released the instant this synchronous httptest request returns, so a
// second SEQUENTIAL request would never see the slot occupied), this
// pre-occupies the token's concurrency slot directly in the same Redis
// backend the middleware itself uses (ccAcquire routes to Redis whenever
// common.RedisEnabled+common.RDB are set, per concurrency_limit.go) under its
// documented key format ("cc:tok:<id>", concurrencyTokenKeyPrefix) — an
// occupied slot rejects the very first request deterministically, no races.
func TestSetRelayRouter_RelayConcurrencyLimit_MountedOnGeminiChain(t *testing.T) {
	fx, cleanup := b1SeedGeminiToken(t, 0, 0)
	defer cleanup()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	prevRedisEnabled := common.RedisEnabled
	prevRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = rdb
	defer func() {
		common.RedisEnabled = prevRedisEnabled
		common.RDB = prevRDB
	}()

	t.Setenv("RELAY_MAX_CONCURRENT_PER_TOKEN", "1")

	// Occupy the sole slot before the request even starts — key format pinned
	// by concurrency_limit.go's concurrencyTokenKeyPrefix doc comment.
	key := "cc:tok:" + strconv.Itoa(fx.TokenID)
	if err := rdb.ZAdd(context.Background(), key, redis.Z{
		Score: float64(time.Now().UnixMilli()), Member: "b1-occupied-lease",
	}).Err(); err != nil {
		t.Fatalf("seed concurrency slot: %v", err)
	}

	engine := b1NewEngine()

	w := b1Do(engine, fx.AuthHeader, b1GeminiPath)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429 (RelayConcurrencyLimit must be mounted on the /v1beta chain); body=%s", w.Code, w.Body.String())
	}
	// Fingerprint changed from the "concurrency_limit_exceeded" OpenAI-wire
	// error code to this message substring by the wire-native-envelope fix —
	// see the BusinessRateLimit test above for why.
	if !strings.Contains(w.Body.String(), "并发请求数已达上限") {
		t.Fatalf("body=%s, want the concurrency-limit message — a 429 from a different limiter would false-pass a bare status check", w.Body.String())
	}
}

// b1SeedGeminiChannel seeds a repo.Channel (+ its abilities, via the same
// Channel.AddAbilities helper production channel-creation uses) that can
// actually serve "gemini-2.0-flash" in the "default" group — the minimum
// Distribute() needs to admit a request, which BusinessModelRateLimit (last
// in the chain, mounted AFTER Distribute) requires to ever run at all.
func b1SeedGeminiChannel(t *testing.T, db *gorm.DB, channelID int) {
	t.Helper()
	if err := db.AutoMigrate(&repo.Channel{}, &entity.Ability{}, &entity.Tenant{}, &entity.ModelRateLimit{}); err != nil {
		t.Fatalf("automigrate channel/ability/tenant/model_rate_limit: %v", err)
	}
	base := "http://127.0.0.1:1" // nothing listens here — any downstream dial fails fast, no hang
	ch := &repo.Channel{
		Id: channelID, Name: "b1-gemini-channel", TenantId: "default",
		Type: 24 /* constant.ChannelTypeGemini */, Key: "b1-fake-key",
		Status: common.ChannelStatusEnabled, Models: "gemini-2.0-flash", Group: "default",
		BaseURL: &base,
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := ch.AddAbilities(db); err != nil {
		t.Fatalf("add abilities: %v", err)
	}
}

// TestSetRelayRouter_BusinessModelRateLimit_MountedOnGeminiChain locks
// middleware.BusinessModelRateLimit(): a (tenant, model) row with
// RateLimitRPM=1 must reject the SECOND /v1beta request for that model
// within the window. Unlike the other four locks in this file, this
// middleware only runs once Distribute has resolved a channel — so this test
// seeds a real channel + ability row (b1SeedGeminiChannel) to get a genuine
// admission through the rest of the chain first.
func TestSetRelayRouter_BusinessModelRateLimit_MountedOnGeminiChain(t *testing.T) {
	fx, cleanup := b1SeedGeminiToken(t, 0, 0)
	defer cleanup()

	b1SeedGeminiChannel(t, repo.DB, 5001+int(b1DBCounter.Load()))

	if err := repo.DB.Create(&entity.ModelRateLimit{
		TenantId: "default", Model: "gemini-2.0-flash", RateLimitRPM: 1,
	}).Error; err != nil {
		t.Fatalf("seed model_rate_limits: %v", err)
	}

	engine := b1NewEngine()

	w1 := b1Do(engine, fx.AuthHeader, b1GeminiPath)
	if w1.Code == http.StatusTooManyRequests {
		t.Fatalf("first request already 429 — fixture broken (channel/ability not resolving); body=%s", w1.Body.String())
	}

	w2 := b1Do(engine, fx.AuthHeader, b1GeminiPath)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status=%d, want 429 (BusinessModelRateLimit must be mounted AFTER Distribute on the /v1beta chain); body=%s", w2.Code, w2.Body.String())
	}
	// Fingerprint changed from the "business_rate_limit_exceeded" OpenAI-wire
	// error code to this message substring by the wire-native-envelope fix —
	// see the BusinessRateLimit test above for why. The 模型-scoped wording
	// (as opposed to that test's 令牌-scoped one) is what distinguishes this
	// limiter from the token-rpm one.
	if !strings.Contains(w2.Body.String(), "模型每分钟请求数已达上限") {
		t.Fatalf("second request body=%s, want the model-scoped rate-limit message — a 429 from a different limiter would false-pass a bare status check", w2.Body.String())
	}
}
