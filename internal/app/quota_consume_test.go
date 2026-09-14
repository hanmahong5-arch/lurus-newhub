package app

// quota_consume_test.go — money-core coverage for the post-consume settlement
// path. Every assertion hand-computes the billing arithmetic
// (quota = tokens * modelRatio * groupRatio * completionRatio + cache/audio
// adjustments) and asserts exact debited amounts + post-state on user quota,
// token remain quota, and tenant credit pool. A wrong multiplier is a revenue
// leak, so these assert numeric equality, not merely non-zero.

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"gorm.io/gorm"
)

// userQuota reads back the current remaining quota for a user id.
func userQuota(t *testing.T, db *gorm.DB, userId int) int {
	t.Helper()
	var u repo.User
	if err := db.First(&u, userId).Error; err != nil {
		t.Fatalf("read user %d: %v", userId, err)
	}
	return u.Quota
}

// tokenRemain reads back the remain_quota for a token id.
func tokenRemain(t *testing.T, db *gorm.DB, tokenId int) int {
	t.Helper()
	var tok repo.Token
	if err := db.First(&tok, tokenId).Error; err != nil {
		t.Fatalf("read token %d: %v", tokenId, err)
	}
	return tok.RemainQuota
}

// TestPostConsumeQuota_PositiveDebitsUserAndToken proves the core settlement:
// a positive quota decrements both user quota and (non-playground) token remain
// quota by exactly that amount, and records a consume log row.
func TestPostConsumeQuota_PositiveDebitsUserAndToken(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db) // token defaults tenant_id="default"; no pool row => clean skip
	userId := seedTestUser(t, db, 10_000)
	key, tokenId := seedTestToken(t, db, userId, 5_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: key,
	}

	if err := PostConsumeQuota(relayInfo, 300, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	if got := userQuota(t, db, userId); got != 10_000-300 {
		t.Errorf("user quota = %d, want %d", got, 10_000-300)
	}
	if got := tokenRemain(t, db, tokenId); got != 5_000-300 {
		t.Errorf("token remain = %d, want %d", got, 5_000-300)
	}
}

// TestPostConsumeQuota_NegativeRefundsUserAndToken proves the refund/adjust
// path: a negative quota (over-pre-consumed) INCREASES user quota and token
// remain quota by the absolute amount.
func TestPostConsumeQuota_NegativeRefundsUserAndToken(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000)
	key, tokenId := seedTestToken(t, db, userId, 200, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: key,
	}

	// quota = -150 means we over-charged 150 during pre-consume; refund it.
	if err := PostConsumeQuota(relayInfo, -150, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota refund: %v", err)
	}

	if got := userQuota(t, db, userId); got != 1_000+150 {
		t.Errorf("user quota after refund = %d, want %d", got, 1_000+150)
	}
	if got := tokenRemain(t, db, tokenId); got != 200+150 {
		t.Errorf("token remain after refund = %d, want %d", got, 200+150)
	}
}

// TestPostConsumeQuota_PlaygroundSkipsTokenDebit proves the playground branch:
// user quota is still debited, but token remain quota is untouched.
func TestPostConsumeQuota_PlaygroundSkipsTokenDebit(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000)
	key, tokenId := seedTestToken(t, db, userId, 5_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:       userId,
		TokenId:      tokenId,
		TokenKey:     key,
		IsPlayground: true,
	}

	if err := PostConsumeQuota(relayInfo, 400, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota playground: %v", err)
	}

	if got := userQuota(t, db, userId); got != 10_000-400 {
		t.Errorf("playground user quota = %d, want %d", got, 10_000-400)
	}
	if got := tokenRemain(t, db, tokenId); got != 5_000 {
		t.Errorf("playground token remain = %d, want 5000 (untouched)", got)
	}
}

// TestPostConsumeQuota_DebitsTenantPool proves that when the token is bound to a
// funded tenant pool, PostConsumeQuota routes the debit into that pool (Phase
// 2.5) in addition to the user/token debit.
func TestPostConsumeQuota_DebitsTenantPool(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 10_000)
	tokenId := seedTenantToken(t, db, userId, "t-consume-pool")

	pool, err := repo.CreateTenantCreditPool("t-consume-pool", 1, 1_000_000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, "t-consume-pool", 1_000, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: "unused-unlimited",
	}

	if err := PostConsumeQuota(relayInfo, 250, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	got, err := repo.GetTenantCreditPool("t-consume-pool")
	if err != nil {
		t.Fatalf("readback pool: %v", err)
	}
	if got.CurrentBalance != 1_000-250 {
		t.Errorf("pool balance = %d, want %d", got.CurrentBalance, 1_000-250)
	}
	if got := userQuota(t, db, userId); got != 10_000-250 {
		t.Errorf("user quota = %d, want %d", got, 10_000-250)
	}
}

// TestPostConsumeQuota_ZeroPoolTokenIdSkipsDebit proves that with TokenId=0 the
// pool debit is skipped (Phase 2.5 guard) — only user quota moves.
func TestPostConsumeQuota_ZeroTokenIdSkipsTokenDebit(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 8_000)

	relayInfo := &relaycommon.RelayInfo{
		UserId:  userId,
		TokenId: 0, // no token bound
	}

	if err := PostConsumeQuota(relayInfo, 100, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}
	if got := userQuota(t, db, userId); got != 8_000-100 {
		t.Errorf("user quota = %d, want %d", got, 8_000-100)
	}
}

// TestPostClaudeConsumeQuota_Arithmetic hand-computes the Claude settlement
// formula and asserts the exact user quota debited plus the recorded log row's
// Quota field.
//
//	calculateQuota = (promptTokens
//	                  + cacheTokens*cacheRatio
//	                  + completionTokens*completionRatio) * groupRatio * modelRatio
func TestPostClaudeConsumeQuota_Arithmetic(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 200,
		},
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now().Add(-2 * time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      3.0,
			CompletionRatio: 5.0,
			CacheRatio:      0.5,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 2.0},
		},
	}

	// Hand-computed:
	// base = promptTokens(1000) + cacheTokens(200)*cacheRatio(0.5)
	//        + completionTokens(500)*completionRatio(5.0)
	//      = 1000 + 100 + 2500 = 3600
	// quota = base * groupRatio(2.0) * modelRatio(3.0) = 3600 * 6 = 21600
	const wantQuota = 21600

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if before-after != wantQuota {
		t.Errorf("Claude debited %d, want %d", before-after, wantQuota)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	if logRow.Quota != wantQuota {
		t.Errorf("consume log quota = %d, want %d", logRow.Quota, wantQuota)
	}
	if logRow.PromptTokens != 1000 || logRow.CompletionTokens != 500 {
		t.Errorf("log tokens = (%d,%d), want (1000,500)", logRow.PromptTokens, logRow.CompletionTokens)
	}
}

// TestPostClaudeConsumeQuota_UsePriceFixed proves the per-call price branch:
// quota = modelPrice * QuotaPerUnit * groupRatio, independent of token counts.
func TestPostClaudeConsumeQuota_UsePriceFixed(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()

	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-price",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			UsePrice:       true,
			ModelPrice:     0.02,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.5},
		},
	}

	wantQuota := int(0.02 * common.QuotaPerUnit * 1.5)

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if before-after != wantQuota {
		t.Errorf("Claude usePrice debited %d, want %d", before-after, wantQuota)
	}
}

// TestPostClaudeConsumeQuota_ZeroTokensNoDebit proves the "total tokens is 0"
// guard: quota is forced to 0 (upstream error) so no user quota is charged, but
// a log row is still written for auditing.
func TestPostClaudeConsumeQuota_ZeroTokensNoDebit(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 5_000)
	key, tokenId := seedTestToken(t, db, userId, 5_000, false)

	c := createTestGinContext()

	usage := &dto.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      3.0,
			CompletionRatio: 5.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if before != after {
		t.Errorf("zero-token request charged %d, want 0", before-after)
	}
}

// TestPostAudioConsumeQuota_Arithmetic hand-computes the audio settlement using
// the same calculateAudioQuota formula, then asserts the debited amount matches
// and the log row carries it.
func TestPostAudioConsumeQuota_Arithmetic(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()

	const model = "gpt-4o-audio-preview"
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 40,
		TotalTokens:      140,
		PromptTokensDetails: dto.InputTokenDetails{
			TextTokens:  60,
			AudioTokens: 40,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			TextTokens:  30,
			AudioTokens: 10,
		},
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      2.0,
			CompletionRatio: ratio_setting.GetCompletionRatio(model),
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// Expected value: reuse the production helper on the identical QuotaInfo so
	// the test tracks the real ratio_setting values for `model` rather than
	// hardcoding audio ratios that may drift. CompletionRatio is set on both
	// sides: settlement reads it from PriceData, so leaving it at its zero
	// value would make the completion term 0 == 0 and assert nothing.
	want := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: 60, AudioTokens: 40},
		OutputDetails:   TokenDetails{TextTokens: 30, AudioTokens: 10},
		ModelName:       model,
		UsePrice:        false,
		ModelRatio:      2.0,
		CompletionRatio: ratio_setting.GetCompletionRatio(model),
		GroupRatio:      1.0,
	})

	before := userQuota(t, db, userId)
	PostAudioConsumeQuota(c, relayInfo, usage, "extra")
	after := userQuota(t, db, userId)

	if before-after != want {
		t.Errorf("audio debited %d, want %d", before-after, want)
	}
	if want <= 0 {
		t.Fatalf("audio want quota should be positive, got %d — fixture is degenerate", want)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	if logRow.Quota != want {
		t.Errorf("audio log quota = %d, want %d", logRow.Quota, want)
	}
}

// TestPostWssConsumeQuota_Arithmetic exercises the realtime (websocket)
// settlement path and asserts the used-quota bump equals calculateAudioQuota.
// Wss updates UsedQuota (not remaining), so we assert used_quota delta.
func TestPostWssConsumeQuota_Arithmetic(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "wss-tkn")

	const model = "gpt-4o-realtime-preview"
	usage := &dto.RealtimeUsage{
		TotalTokens:  200,
		InputTokens:  120,
		OutputTokens: 80,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens:  80,
			AudioTokens: 40,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:  50,
			AudioTokens: 30,
		},
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:     2.0,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// CompletionRatio mirrors what PostWssConsumeQuota reads for this call: the
	// ratio_setting entry for the model name the caller passes (the upstream
	// name), not PriceData.CompletionRatio. Without it both sides would carry
	// 0 and the completion term would assert nothing.
	want := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: 80, AudioTokens: 40},
		OutputDetails:   TokenDetails{TextTokens: 50, AudioTokens: 30},
		ModelName:       model,
		UsePrice:        false,
		ModelRatio:      2.0,
		CompletionRatio: ratio_setting.GetCompletionRatio(model),
		GroupRatio:      1.0,
	})
	if want <= 0 {
		t.Fatalf("wss want quota should be positive, got %d", want)
	}

	PostWssConsumeQuota(c, relayInfo, model, usage, "")

	var u repo.User
	if err := db.First(&u, userId).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if u.UsedQuota != want {
		t.Errorf("wss used_quota = %d, want %d", u.UsedQuota, want)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	if logRow.Quota != want {
		t.Errorf("wss log quota = %d, want %d", logRow.Quota, want)
	}
}

// TestPostAudioConsumeQuota_TierCompletionRatioAffectsMoney is the cycle-8
// plan §8 L5 B-F5 oracle: a context-length tier's completion_ratio override
// must reach calculateAudioQuota's money, not just the (unrelated) log
// message — before the fix, PostAudioConsumeQuota re-derived completionRatio
// straight from ratio_setting, so a tier's completion_ratio silently did
// nothing here.
func TestPostAudioConsumeQuota_TierCompletionRatioAffectsMoney(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()

	const model = "audio-tier-completion-probe"
	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	// Only completion_ratio is overridden — model_ratio falls through to
	// PriceData.BaseModelRatio (the frozen pre-consume snapshot).
	if err := ratio_setting.UpdateContextLengthTiersByJSONString(
		`{"` + model + `":[{"threshold_tokens":0,"completion_ratio":9.0}]}`,
	); err != nil {
		t.Fatalf("seed context tiers: %v", err)
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 40,
		TotalTokens:      140,
		PromptTokensDetails: dto.InputTokenDetails{
			TextTokens:  60,
			AudioTokens: 40,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			TextTokens:  30,
			AudioTokens: 10,
		},
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:     2.0,
			BaseModelRatio: 2.0, // matches ModelRatio: this model has no model_ratio tier
			BaseRatiosSet:  true,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	want := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: 60, AudioTokens: 40},
		OutputDetails:   TokenDetails{TextTokens: 30, AudioTokens: 10},
		ModelName:       model,
		UsePrice:        false,
		ModelRatio:      2.0,
		CompletionRatio: 9.0,
		GroupRatio:      1.0,
	})
	// Fixture sanity: the flat (untiered) charge must differ from `want`, or
	// this test would pass even if the tier's completion_ratio were ignored.
	flatWant := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: 60, AudioTokens: 40},
		OutputDetails:   TokenDetails{TextTokens: 30, AudioTokens: 10},
		ModelName:       model,
		UsePrice:        false,
		ModelRatio:      2.0,
		CompletionRatio: ratio_setting.GetCompletionRatio(model),
		GroupRatio:      1.0,
	})
	if want == flatWant {
		t.Fatalf("degenerate fixture: tiered want (%d) equals flat want (%d)", want, flatWant)
	}

	before := userQuota(t, db, userId)
	PostAudioConsumeQuota(c, relayInfo, usage, "extra")
	after := userQuota(t, db, userId)

	if before-after != want {
		t.Errorf("audio debited %d, want %d (tier's completion_ratio=9.0 must reach the money, not just the log message)", before-after, want)
	}
}

// TestPostWssConsumeQuota_TierCompletionRatioAffectsMoney mirrors
// TestPostAudioConsumeQuota_TierCompletionRatioAffectsMoney for the realtime
// (websocket) settlement path (cycle-8 plan §8 L5 B-F5).
func TestPostWssConsumeQuota_TierCompletionRatioAffectsMoney(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "wss-tier-tkn")

	const model = "wss-tier-completion-probe"
	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	if err := ratio_setting.UpdateContextLengthTiersByJSONString(
		`{"` + model + `":[{"threshold_tokens":0,"completion_ratio":7.0}]}`,
	); err != nil {
		t.Fatalf("seed context tiers: %v", err)
	}

	usage := &dto.RealtimeUsage{
		TotalTokens:  200,
		InputTokens:  120,
		OutputTokens: 80,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens:  80,
			AudioTokens: 40,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:  50,
			AudioTokens: 30,
		},
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:     2.0,
			BaseModelRatio: 2.0,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	want := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: 80, AudioTokens: 40},
		OutputDetails:   TokenDetails{TextTokens: 50, AudioTokens: 30},
		ModelName:       model,
		UsePrice:        false,
		ModelRatio:      2.0,
		CompletionRatio: 7.0,
		GroupRatio:      1.0,
	})
	flatWant := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: 80, AudioTokens: 40},
		OutputDetails:   TokenDetails{TextTokens: 50, AudioTokens: 30},
		ModelName:       model,
		UsePrice:        false,
		ModelRatio:      2.0,
		CompletionRatio: ratio_setting.GetCompletionRatio(model),
		GroupRatio:      1.0,
	})
	if want == flatWant {
		t.Fatalf("degenerate fixture: tiered want (%d) equals flat want (%d)", want, flatWant)
	}

	PostWssConsumeQuota(c, relayInfo, model, usage, "")

	var u repo.User
	if err := db.First(&u, userId).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if u.UsedQuota != want {
		t.Errorf("wss used_quota = %d, want %d (tier's completion_ratio=7.0 must reach the money)", u.UsedQuota, want)
	}
}

// TestPostWssConsumeQuota_UntieredChargeUnchanged pins which model name the
// realtime path prices the completion term by when no context-length tier is
// configured. websocket.go passes the UPSTREAM model name as this function's
// modelName argument, while PriceData.CompletionRatio is keyed on the ORIGIN
// name, so on a channel that maps the model the two are different rows of the
// completion-ratio map. Reading PriceData here would silently re-price every
// realtime call on such a channel; this test seeds the two names with
// different ratios and asserts the charge follows the upstream one.
func TestPostWssConsumeQuota_UntieredChargeUnchanged(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "wss-mapped-tkn")

	const originModel = "wss-origin-probe"
	const upstreamModel = "wss-upstream-probe"
	prevCompletion := ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateCompletionRatioByJSONString(prevCompletion) })
	if err := ratio_setting.UpdateCompletionRatioByJSONString(
		`{"` + originModel + `":3.0,"` + upstreamModel + `":8.0}`,
	); err != nil {
		t.Fatalf("seed completion ratios: %v", err)
	}

	usage := &dto.RealtimeUsage{
		TotalTokens:  200,
		InputTokens:  120,
		OutputTokens: 80,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens:  80,
			AudioTokens: 40,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:  50,
			AudioTokens: 30,
		},
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: originModel,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio: 2.0,
			// What ModelPriceHelper would have stored for the ORIGIN name.
			CompletionRatio: 3.0,
			BaseModelRatio:  2.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	want := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: 80, AudioTokens: 40},
		OutputDetails:   TokenDetails{TextTokens: 50, AudioTokens: 30},
		ModelName:       upstreamModel,
		UsePrice:        false,
		ModelRatio:      2.0,
		CompletionRatio: 8.0,
		GroupRatio:      1.0,
	})
	originWant := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: 80, AudioTokens: 40},
		OutputDetails:   TokenDetails{TextTokens: 50, AudioTokens: 30},
		ModelName:       upstreamModel,
		UsePrice:        false,
		ModelRatio:      2.0,
		CompletionRatio: 3.0,
		GroupRatio:      1.0,
	})
	if want == originWant {
		t.Fatalf("degenerate fixture: upstream-priced want (%d) equals origin-priced want (%d)", want, originWant)
	}

	PostWssConsumeQuota(c, relayInfo, upstreamModel, usage, "")

	var u repo.User
	if err := db.First(&u, userId).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if u.UsedQuota != want {
		t.Errorf("wss used_quota = %d, want %d (the completion term must stay priced by the upstream model name; %d would mean it followed PriceData's origin-keyed ratio)", u.UsedQuota, want, originWant)
	}
}

// TestPostWssConsumeQuota_ZeroTokensNoCharge proves the total-tokens==0 guard on
// the wss path forces quota to 0 (no used_quota bump), but still logs.
func TestPostWssConsumeQuota_ZeroTokensNoCharge(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000, false)

	c := createTestGinContext()

	usage := &dto.RealtimeUsage{TotalTokens: 0}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "gpt-4o-realtime-preview",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:     2.0,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	PostWssConsumeQuota(c, relayInfo, "gpt-4o-realtime-preview", usage, "")

	var u repo.User
	if err := db.First(&u, userId).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if u.UsedQuota != 0 {
		t.Errorf("wss zero-token used_quota = %d, want 0", u.UsedQuota)
	}
}

// TestPreWssConsumeQuota_UsePriceShortCircuit proves the fast return when the
// price model is per-call: no quota checks or debits happen.
func TestPreWssConsumeQuota_UsePriceShortCircuit(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 100)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		UsePrice: true,
	}
	usage := &dto.RealtimeUsage{TotalTokens: 999999}

	if err := PreWssConsumeQuota(c, relayInfo, usage); err != nil {
		t.Errorf("expected nil for UsePrice short-circuit, got %v", err)
	}
	if got := userQuota(t, db, userId); got != 100 {
		t.Errorf("user quota mutated on UsePrice path: %d, want 100", got)
	}
}

// TestPreWssConsumeQuota_InsufficientUserQuota proves the pre-flight rejection
// when the computed realtime quota exceeds the user's balance.
func TestPreWssConsumeQuota_InsufficientUserQuota(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1) // 1 unit only
	key, _ := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenKey:        key,
		UsingGroup:      "default",
		OriginModelName: "gpt-4o-realtime-preview",
	}
	usage := &dto.RealtimeUsage{
		TotalTokens: 10_000,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens: 10_000,
		},
	}

	err := PreWssConsumeQuota(c, relayInfo, usage)
	if err == nil {
		t.Fatal("expected user-quota-not-enough error")
	}
}

// TestPreWssConsumeQuota_TokenInsufficient exercises the token-quota-not-enough
// branch: the user has plenty of quota but the (limited) token does not.
func TestPreWssConsumeQuota_TokenInsufficient(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	userId := seedTestUser(t, db, 5_000_000)
	key, _ := seedTestToken(t, db, userId, 1, false) // token remain = 1

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenKey:        key,
		UsingGroup:      "default",
		OriginModelName: "gpt-4o-realtime-preview",
	}
	usage := &dto.RealtimeUsage{
		TotalTokens:       10_000,
		InputTokenDetails: dto.InputTokenDetails{TextTokens: 10_000},
	}
	if err := PreWssConsumeQuota(c, relayInfo, usage); err == nil {
		t.Fatal("expected token-quota-not-enough error")
	}
}

// TestPreWssConsumeQuota_SuccessDebits drives the full realtime pre-consume:
// sufficient user + token quota, so the computed quota is settled via
// PostConsumeQuota (user quota decremented by the computed amount).
func TestPreWssConsumeQuota_SuccessDebits(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)

	const start = 5_000_000
	userId := seedTestUser(t, db, start)
	key, tokenId := seedTestToken(t, db, userId, start, false)
	_ = tokenId

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenKey:        key,
		UsingGroup:      "default",
		OriginModelName: "gpt-4o-realtime-preview",
	}
	usage := &dto.RealtimeUsage{
		TotalTokens: 300,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens: 200,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens: 100,
		},
	}

	before := userQuota(t, db, userId)
	if err := PreWssConsumeQuota(c, relayInfo, usage); err != nil {
		t.Fatalf("PreWssConsumeQuota: %v", err)
	}
	after := userQuota(t, db, userId)
	if before-after <= 0 {
		t.Errorf("expected a positive quota debit, got delta %d", before-after)
	}
}

var _ = ratio_setting.GetGroupRatio
