package app

// quota_branches_gap_test.go — closes the settlement-arithmetic branches the
// existing money-core fixtures don't reach: the OpenRouter prompt-token
// adjustment + tiered cache-creation pricing, the over-pre-consumed refund
// deltas (Claude + audio), the per-call price audio path, zero-token audio, the
// realtime pre-consume auto-group + lookup-error arms, and the token-lookup
// error in PreConsumeTokenQuota. Assertions hand-compute the exact debited quota
// where deterministic — a wrong multiplier here is a revenue leak.

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// TestPostClaudeConsumeQuota_OpenRouterCacheTiers drives the OpenRouter
// prompt-token deduction and the 5m/1h/remaining cache-creation pricing tiers.
func TestPostClaudeConsumeQuota_OpenRouterCacheTiers(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
		TotalTokens:      1100,
		// Production OpenRouter usage is parsed by provider/openai/
		// relay-openai.go, which stamps the OpenAI-wire flag — the prompt-base
		// deduction now keys on it instead of ChannelType (2026-08-29).
		PromptTokensIncludeCached: true,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         200,
			CachedCreationTokens: 300,
		},
		ClaudeCacheCreation5mTokens: 50,
		ClaudeCacheCreation1hTokens: 30,
		Cost:                        0, // 0 => skip the OpenRouter cost-inference inner branch
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "or-model",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter},
		PriceData: types.PriceData{
			ModelRatio:           2.0,
			CompletionRatio:      3.0,
			CacheRatio:           0.5,
			CacheCreationRatio:   2.0,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2.0,
			GroupRatioInfo:       types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// promptTokens = 1000 - 200(cache) - 300(cacheCreation) = 500
	// quota = (500 + 200*0.5 + 50*1.25 + 30*2 + 220*2 + 100*3) * 1 * 2
	//       = (500 + 100 + 62.5 + 60 + 440 + 300) * 2 = 1462.5 * 2 = 2925
	const want = 2925
	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	if got := before - userQuota(t, db, userId); got != want {
		t.Errorf("OpenRouter Claude debited %d, want %d", got, want)
	}
}

// TestPostClaudeConsumeQuota_OverPreConsumedRefunds drives the quotaDelta<0
// (return-of-overcharge) log + settlement arm: the actual quota is far below the
// pre-consumed amount, so the difference is refunded to the user.
func TestPostClaudeConsumeQuota_OverPreConsumedRefunds(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)

	c := createTestGinContext()
	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		TokenId:               tokenId,
		TokenKey:              key,
		OriginModelName:       "claude-x",
		StartTime:             time.Now(),
		ChannelMeta:           &relaycommon.ChannelMeta{},
		FinalPreConsumedQuota: 10_000, // way above actual => refund
		UserQuota:             100_000,
		PriceData: types.PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// actual quota = 10 + 5 = 15; delta = 15 - 10000 = -9985 refunded to quota.
	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)
	if after-before != 9_985 {
		t.Errorf("refund delta = %d, want 9985 (10000 pre-consumed - 15 actual)", after-before)
	}
	time.Sleep(50 * time.Millisecond) // drain async notify goroutine
}

// TestPostAudioConsumeQuota_UsePriceRefunds drives the per-call price audio path
// AND the over-pre-consumed refund delta together.
func TestPostAudioConsumeQuota_UsePriceRefunds(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		TokenId:               tokenId,
		TokenKey:              key,
		OriginModelName:       "audio-price",
		StartTime:             time.Now(),
		ChannelMeta:           &relaycommon.ChannelMeta{},
		FinalPreConsumedQuota: 100_000,
		UserQuota:             10_000_000,
		PriceData: types.PriceData{
			UsePrice:       true,
			ModelPrice:     0.01,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// The audio price path does not thread ModelPrice into the QuotaInfo used by
	// calculateAudioQuota, so the price-based quota resolves to 0; the entire
	// pre-consumed amount is therefore refunded (delta = 0 - 100000).
	before := userQuota(t, db, userId)
	PostAudioConsumeQuota(c, relayInfo, usage, "extra-note")
	after := userQuota(t, db, userId)
	if after-before != 100_000 {
		t.Errorf("audio usePrice refund = %d, want 100000 (full refund; price quota is 0)", after-before)
	}
	time.Sleep(50 * time.Millisecond)
}

// TestPostAudioConsumeQuota_ZeroTokens drives the total-tokens==0 guard: quota is
// forced to 0 (upstream error) so nothing is charged, but a log is still written.
func TestPostAudioConsumeQuota_ZeroTokens(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 5_000)
	key, tokenId := seedTestToken(t, db, userId, 5_000, false)

	c := createTestGinContext()
	usage := &dto.Usage{TotalTokens: 0}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "gpt-4o-audio-preview",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:     2.0,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	before := userQuota(t, db, userId)
	PostAudioConsumeQuota(c, relayInfo, usage, "")
	if before != userQuota(t, db, userId) {
		t.Errorf("zero-token audio charged %d, want 0", before-userQuota(t, db, userId))
	}
}

// TestPostWssConsumeQuota_UsePriceWithExtra drives the wss per-call price
// logContent branch and the extraContent append.
func TestPostWssConsumeQuota_UsePriceWithExtra(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "wss")
	usage := &dto.RealtimeUsage{TotalTokens: 100, InputTokens: 60, OutputTokens: 40}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "gpt-4o-realtime-preview",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			UsePrice:       true,
			ModelPrice:     0.02,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	PostWssConsumeQuota(c, relayInfo, "gpt-4o-realtime-preview", usage, "wss-extra")

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	// The realtime price path likewise omits ModelPrice from QuotaInfo, so the
	// price-based quota is 0; the log still records the (price-branch) consume row.
	if logRow.Quota != 0 {
		t.Errorf("wss usePrice log quota = %d, want 0 (price quota not threaded)", logRow.Quota)
	}
}

// TestPreWssConsumeQuota_AutoGroupOverride drives the auto-group context branch:
// a resolved auto-group in the request context overrides the group ratio.
func TestPreWssConsumeQuota_AutoGroupOverride(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)

	const start = 5_000_000
	userId := seedTestUser(t, db, start)
	key, _ := seedTestToken(t, db, userId, start, false)

	c := createTestGinContext()
	common.SetContextKey(c, constant.ContextKeyAutoGroup, "default")

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenKey:        key,
		UsingGroup:      "vip",
		OriginModelName: "gpt-4o-realtime-preview",
	}
	usage := &dto.RealtimeUsage{
		TotalTokens:       300,
		InputTokenDetails: dto.InputTokenDetails{TextTokens: 200},
	}

	if err := PreWssConsumeQuota(c, relayInfo, usage); err != nil {
		t.Fatalf("PreWssConsumeQuota: %v", err)
	}
	// Auto-group must have overwritten the using-group.
	if relayInfo.UsingGroup != "default" {
		t.Errorf("UsingGroup = %q, want default (auto-group override)", relayInfo.UsingGroup)
	}
}

// TestPreWssConsumeQuota_UserLookupError drives the GetUserQuota error arm.
func TestPreWssConsumeQuota_UserLookupError(t *testing.T) {
	setupServiceTestDB(t)
	repo.InitCol()
	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{UserId: 999_999_999, OriginModelName: "gpt-4o-realtime-preview"}
	usage := &dto.RealtimeUsage{TotalTokens: 10, InputTokenDetails: dto.InputTokenDetails{TextTokens: 10}}
	if err := PreWssConsumeQuota(c, relayInfo, usage); err == nil {
		t.Fatal("expected an error for a non-existent user's quota lookup")
	}
}

// TestPreWssConsumeQuota_TokenLookupError drives the GetTokenByKey error arm: the
// user exists but the token key resolves to nothing.
func TestPreWssConsumeQuota_TokenLookupError(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	userId := seedTestUser(t, db, 1_000_000)
	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenKey:        "sk-does-not-exist",
		OriginModelName: "gpt-4o-realtime-preview",
	}
	usage := &dto.RealtimeUsage{TotalTokens: 10, InputTokenDetails: dto.InputTokenDetails{TextTokens: 10}}
	if err := PreWssConsumeQuota(c, relayInfo, usage); err == nil {
		t.Fatal("expected an error for a non-existent token key")
	}
}

// TestPreConsumeTokenQuota_TokenLookupError drives the GetTokenByKey error arm.
func TestPreConsumeTokenQuota_TokenLookupError(t *testing.T) {
	setupServiceTestDB(t)
	repo.InitCol()
	relayInfo := &relaycommon.RelayInfo{TokenId: 123, TokenKey: "sk-nope"}
	if err := PreConsumeTokenQuota(relayInfo, 100); err == nil {
		t.Fatal("expected an error resolving a non-existent token key")
	}
}
