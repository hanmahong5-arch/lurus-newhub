package app

// quota_openrouter_gap_test.go — two remaining money-path arms:
//   - PostClaudeConsumeQuota's OpenRouter cost-inference inner branch, which
//     back-solves cache-creation tokens from the reported cost when the channel
//     uses default (non-custom) pricing;
//   - debitTenantPool's exhausted-then-overdraft-also-fails "lost debit" arm.

import (
	"sort"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestPostClaudeConsumeQuota_OpenRouterCostInference(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 100_000_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000_000, false)

	// Pick a model that exists in the default ratio map, and use its DEFAULT
	// ratio, so hasCustomModelRatio()==false => the cost-inference branch runs.
	//
	// The pick is deterministic on purpose. This used to take the first key
	// of a range over the map — Go randomises map iteration, and the map
	// holds models whose ratio-priced cost is legitimately zero (ratio 0, or
	// per-request priced via the price map instead). Landing on one of those
	// is not a defect in the inference path, but the assertion below cannot
	// tell that apart from the branch silently doing nothing, so the test
	// went red on whichever CI run drew a bad model (main bbc82c91 and #157
	// both drew llama-3-sonar-large-32k-chat, quota 0). Sorted keys, first
	// one with a positive ratio and no per-request price.
	defMap := ratio_setting.GetDefaultModelRatioMap()
	priceMap := ratio_setting.GetDefaultModelPriceMap()
	keys := make([]string, 0, len(defMap))
	for k := range defMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var model string
	var modelRatio float64
	for _, k := range keys {
		if _, priced := priceMap[k]; priced || defMap[k] <= 0 {
			continue
		}
		model, modelRatio = k, defMap[k]
		break
	}
	if model == "" {
		t.Fatal("no ratio-priced model with a positive default ratio — the fixture's precondition no longer holds")
	}

	c := createTestGinContext()
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
		TotalTokens:      1100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 0, // 0 => eligible for cost inference
		},
		Cost: float64(0.5), // non-zero float => enters the inference branch
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter},
		PriceData: types.PriceData{
			ModelRatio:         modelRatio, // == default => not custom
			CompletionRatio:    3.0,
			CacheRatio:         0.5,
			CacheCreationRatio: 2.0, // != 1 => inference eligible
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	if before == userQuota(t, db, userId) {
		t.Error("expected some quota to be consumed on the OpenRouter cost-inference path")
	}
}

func TestDebitTenantPool_OverdraftAlsoFailsLostDebit(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1_000)
	tokenId := seedTenantToken(t, db, userId, "t-ovd-lost")

	// Balance 0 < debit => DebitPool returns ErrPoolExhausted; then the overdraft
	// draw insert fails because the draw table is gone => the debit is lost.
	pool, err := repo.CreateTenantCreditPool("t-ovd-lost", 1, 1_000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	_ = pool
	if err := db.Migrator().DropTable(&entity.TenantCreditPoolDraw{}); err != nil {
		t.Fatalf("drop draws: %v", err)
	}

	// Must not panic; the loss is surfaced via log/metric.
	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 100)

	got, err := repo.GetTenantCreditPool("t-ovd-lost")
	if err != nil {
		t.Fatalf("readback pool: %v", err)
	}
	if got.CurrentBalance != 0 {
		t.Errorf("pool balance = %d, want 0 (lost debit must not move balance)", got.CurrentBalance)
	}
}
