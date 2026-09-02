package app

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// The OpenRouter cost inference solves for cache-creation tokens against the
// configured CacheCreationRatio (CalcOpenRouterCacheCreateTokens) and then
// bills them. Since 2026-09-02 an OpenAI-wire cache write on an unlisted model
// is otherwise billed at the plain input rate (CacheCreationRatioForWire); the
// inferred tokens must stay on the ratio they were derived from, or the charge
// no longer reproduces the upstream cost. Two identical requests, differing
// only in whether the ratio is the map default, must cost the same.
func TestPostClaudeConsumeQuota_OpenRouterInferenceKeepsConfiguredRatio(t *testing.T) {
	db := setupServiceTestDB(t)

	defMap := ratio_setting.GetDefaultModelRatioMap()
	var model string
	var modelRatio float64
	for k, v := range defMap {
		model, modelRatio = k, v
		break
	}
	if model == "" {
		t.Skip("no default model ratios available")
	}

	charge := func(defaulted bool) int {
		userId := seedTestUser(t, db, 100_000_000)
		key, tokenId := seedTestToken(t, db, userId, 100_000_000, false)
		usage := &dto.Usage{
			PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100,
			PromptTokensIncludeCached: true,
			PromptTokensDetails:       dto.InputTokenDetails{CachedTokens: 100},
			Cost:                      float64(0.5),
		}
		relayInfo := &relaycommon.RelayInfo{
			UserId: userId, TokenId: tokenId, TokenKey: key,
			OriginModelName: model,
			StartTime:       time.Now(),
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter},
			PriceData: types.PriceData{
				ModelRatio:                  modelRatio,
				CompletionRatio:             3.0,
				CacheRatio:                  0.5,
				CacheCreationRatio:          2.0,
				CacheCreationRatioDefaulted: defaulted,
				GroupRatioInfo:              types.GroupRatioInfo{GroupRatio: 1.0},
			},
		}
		before := userQuota(t, db, userId)
		PostClaudeConsumeQuota(createTestGinContext(), relayInfo, usage)
		return before - userQuota(t, db, userId)
	}

	listed, defaulted := charge(false), charge(true)
	if listed <= 0 {
		t.Fatalf("inference path charged %d, want > 0", listed)
	}
	if listed != defaulted {
		t.Errorf("inference charge with default ratio = %d, with listed ratio = %d; inferred tokens must be billed at the ratio they were solved against", defaulted, listed)
	}
}
