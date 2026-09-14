package helper

import (
	"fmt"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// https://docs.claude.com/en/docs/build-with-claude/prompt-caching#1-hour-cache-duration
const claudeCacheCreation1hMultiplier = 6 / 3.75

// HandleGroupRatio checks for "auto_group" in the context and updates the group ratio and relayInfo.UsingGroup if present
func HandleGroupRatio(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) types.GroupRatioInfo {
	groupRatioInfo := types.GroupRatioInfo{
		GroupRatio:        1.0, // default ratio
		GroupSpecialRatio: -1,
	}

	// check auto group
	autoGroup, exists := ctx.Get("auto_group")
	if exists {
		logger.LogDebug(ctx, fmt.Sprintf("final group: %s", autoGroup))
		relayInfo.UsingGroup = autoGroup.(string)
	}

	// check user group special ratio
	userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
	if ok {
		// user group special ratio
		groupRatioInfo.GroupSpecialRatio = userGroupRatio
		groupRatioInfo.GroupRatio = userGroupRatio
		groupRatioInfo.HasSpecialRatio = true
	} else {
		// normal group ratio
		groupRatioInfo.GroupRatio = ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
	}

	return groupRatioInfo
}

func ModelPriceHelper(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta) (types.PriceData, error) {
	modelPrice, usePrice := ratio_setting.GetModelPrice(info.OriginModelName, false)

	groupRatioInfo := HandleGroupRatio(c, info)

	var preConsumedQuota int
	var modelRatio float64
	var completionRatio float64
	var cacheRatio float64
	var imageRatio float64
	var cacheCreationRatio float64
	var cacheCreationRatio5m float64
	var cacheCreationRatio1h float64
	var cacheCreationRatioDefaulted bool
	var audioRatio float64
	var audioCompletionRatio float64
	var contextTierThreshold int
	var baseModelRatio float64
	var baseCompletionRatio float64
	var baseCacheRatio float64
	var freeModel bool
	if !usePrice {
		preConsumedTokens := common.Max(promptTokens, common.PreConsumedQuota)
		if meta.MaxTokens != 0 {
			preConsumedTokens += meta.MaxTokens
		}
		var success bool
		var matchName string
		modelRatio, success, matchName = ratio_setting.GetModelRatio(info.OriginModelName)
		if !success {
			acceptUnsetRatio := false
			if info.UserSetting.AcceptUnsetRatioModel {
				acceptUnsetRatio = true
			}
			if !acceptUnsetRatio {
				return types.PriceData{}, fmt.Errorf("模型 %s 倍率或价格未配置，请联系管理员设置或开始自用模式；Model %s ratio or price not set, please set or start self-use mode", matchName, matchName)
			}
		}
		completionRatio = ratio_setting.GetCompletionRatio(info.OriginModelName)
		cacheRatio, _ = ratio_setting.GetCacheRatio(info.OriginModelName)
		var cacheCreationRatioListed bool
		cacheCreationRatio, cacheCreationRatioListed = ratio_setting.GetCreateCacheRatio(info.OriginModelName)
		cacheCreationRatioDefaulted = !cacheCreationRatioListed
		cacheCreationRatio5m = cacheCreationRatio
		// 固定1h和5min缓存写入价格的比例
		cacheCreationRatio1h = cacheCreationRatio * claudeCacheCreation1hMultiplier
		imageRatio, _ = ratio_setting.GetImageRatio(info.OriginModelName)
		audioRatio = ratio_setting.GetAudioRatio(info.OriginModelName)
		audioCompletionRatio = ratio_setting.GetAudioCompletionRatio(info.OriginModelName)
		// Snapshot the flat, non-tiered ratios BEFORE any tier override below.
		// helper.ResettleContextTier re-derives the settled ratio starting
		// from this snapshot rather than re-reading the live ratio_setting
		// maps at settlement time, so a concurrent admin edit (or a routine
		// repo.SyncOptions tick pulling another replica's write) cannot
		// re-price a request that is already in flight (cycle-8 plan §8 L5
		// B-F3).
		baseModelRatio = modelRatio
		baseCompletionRatio = completionRatio
		baseCacheRatio = cacheRatio
		// Declarative context-length pricing tier (billing-pricing-14): the
		// pre-consume estimate decides which tier applies here; every
		// settlement site re-evaluates against the actual context length
		// derived from the upstream-reported usage (see ResettleContextTier
		// and its callers) so the logged ratio and prompt_tokens land on the
		// same side of a configured threshold. Default empty map (no
		// operator-configured tiers) makes this a no-op — GetContextLengthTier
		// returns nil and none of modelRatio/completionRatio/cacheRatio change.
		if t := ratio_setting.GetContextLengthTier(info.OriginModelName, promptTokens); t != nil {
			if t.ModelRatio != nil {
				modelRatio = *t.ModelRatio
			}
			if t.CompletionRatio != nil {
				completionRatio = *t.CompletionRatio
			}
			if t.CacheRatio != nil {
				cacheRatio = *t.CacheRatio
			}
			contextTierThreshold = t.ThresholdTokens
		}
		ratio := modelRatio * groupRatioInfo.GroupRatio
		preConsumedQuota = int(float64(preConsumedTokens) * ratio)
	} else {
		if meta.ImagePriceRatio != 0 {
			modelPrice = modelPrice * meta.ImagePriceRatio
		}
		preConsumedQuota = int(modelPrice * common.QuotaPerUnit * groupRatioInfo.GroupRatio)
	}

	// check if free model pre-consume is disabled
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		// if model price or ratio is 0, do not pre-consume quota
		if groupRatioInfo.GroupRatio == 0 {
			preConsumedQuota = 0
			freeModel = true
		} else if usePrice {
			if modelPrice == 0 {
				preConsumedQuota = 0
				freeModel = true
			}
		} else {
			if modelRatio == 0 {
				preConsumedQuota = 0
				freeModel = true
			}
		}
	}

	priceData := types.PriceData{
		FreeModel:                   freeModel,
		ModelPrice:                  modelPrice,
		ModelRatio:                  modelRatio,
		CompletionRatio:             completionRatio,
		GroupRatioInfo:              groupRatioInfo,
		UsePrice:                    usePrice,
		CacheRatio:                  cacheRatio,
		ImageRatio:                  imageRatio,
		AudioRatio:                  audioRatio,
		AudioCompletionRatio:        audioCompletionRatio,
		CacheCreationRatio:          cacheCreationRatio,
		CacheCreation5mRatio:        cacheCreationRatio5m,
		CacheCreation1hRatio:        cacheCreationRatio1h,
		ContextTierThreshold:        contextTierThreshold,
		CacheCreationRatioDefaulted: cacheCreationRatioDefaulted,
		QuotaToPreConsume:           preConsumedQuota,
		BaseModelRatio:              baseModelRatio,
		BaseCompletionRatio:         baseCompletionRatio,
		BaseCacheRatio:              baseCacheRatio,
		BaseRatiosSet:               true,
	}

	if common.DebugEnabled {
		println(fmt.Sprintf("model_price_helper result: %s", priceData.ToSetting()))
	}
	info.PriceData = priceData
	return priceData, nil
}

// ModelPriceHelperPerCall 按次计费的 PriceHelper (MJ、Task)
func ModelPriceHelperPerCall(c *gin.Context, info *relaycommon.RelayInfo) types.PerCallPriceData {
	groupRatioInfo := HandleGroupRatio(c, info)

	modelPrice, success := ratio_setting.GetModelPrice(info.OriginModelName, true)
	// 如果没有配置价格，则使用默认价格
	if !success {
		defaultPrice, ok := ratio_setting.GetDefaultModelPriceMap()[info.OriginModelName]
		if !ok {
			modelPrice = 0.1
		} else {
			modelPrice = defaultPrice
		}
	}
	quota := int(modelPrice * common.QuotaPerUnit * groupRatioInfo.GroupRatio)
	priceData := types.PerCallPriceData{
		ModelPrice:     modelPrice,
		Quota:          quota,
		GroupRatioInfo: groupRatioInfo,
	}
	return priceData
}

// ResettleContextTier re-evaluates a declarative context-length pricing tier
// (billing-pricing-14) against the actual context length of the call
// (actualPromptTokens — callers on a wire where the prompt-token field
// excludes cache read/creation must add those back in before calling this;
// see usage.AsOpenAIWire().PromptTokens at each call site). ModelPriceHelper
// chose (or did not choose) a tier from the pre-consume ESTIMATE; the
// estimate and the actual context length can land on different sides of a
// configured threshold, so every settlement site that reads
// PriceData.ModelRatio for a !UsePrice model must call this before computing
// the final quota — otherwise the logged model_ratio and prompt_tokens could
// disagree about which tier applied. A no-op for UsePrice (per-call) models:
// tiers only ever apply to the token-based branch
// (TestModelPriceHelper_PerCallModelIgnoresTiers covers the pre-consume side
// of that same rule).
//
// modelRatio/completionRatio/cacheRatio start from priceData's
// Base{ModelRatio,CompletionRatio,CacheRatio} — the flat, non-tiered ratios
// ModelPriceHelper snapshotted at pre-consume — rather than a fresh read of
// the live ratio_setting maps. Re-reading live would let a concurrent admin
// edit or a routine repo.SyncOptions tick re-price a request that is already
// in flight (cycle-8 plan §8 L5 B-F3); the frozen snapshot is the only
// record of the model's flat ratio once priceData may already carry the
// pre-consume tier's override.
func ResettleContextTier(priceData *types.PriceData, modelName string, actualPromptTokens int) {
	if priceData.UsePrice {
		return
	}
	// Short-circuit for every model with no tier list at all: this keeps
	// settlement byte-identical to pre-consume for the default-empty
	// mechanism (and for any model an admin has never given tiers), instead
	// of re-deriving modelRatio/completionRatio/cacheRatio from the frozen
	// snapshot on every settlement regardless of whether this lane's
	// mechanism is in play for that model.
	if !ratio_setting.HasContextLengthTiers(modelName) {
		return
	}
	// The frozen snapshot is written by ModelPriceHelper's token-based branch.
	// A PriceData that never went through it (a hand-built value, or a future
	// settlement path that constructs its own) has no snapshot to start from,
	// and overwriting the live ratios from its zero fields would charge the
	// call at ratio 0. Leave such a value alone:
	// TestResettleContextTier_NoBaseSnapshotIsNoOp pins it.
	if !priceData.BaseRatiosSet {
		return
	}
	modelRatio := priceData.BaseModelRatio
	completionRatio := priceData.BaseCompletionRatio
	cacheRatio := priceData.BaseCacheRatio
	threshold := 0
	if t := ratio_setting.GetContextLengthTier(modelName, actualPromptTokens); t != nil {
		if t.ModelRatio != nil {
			modelRatio = *t.ModelRatio
		}
		if t.CompletionRatio != nil {
			completionRatio = *t.CompletionRatio
		}
		if t.CacheRatio != nil {
			cacheRatio = *t.CacheRatio
		}
		threshold = t.ThresholdTokens
	}
	priceData.ModelRatio = modelRatio
	priceData.CompletionRatio = completionRatio
	priceData.CacheRatio = cacheRatio
	priceData.ContextTierThreshold = threshold
}

func ContainPriceOrRatio(modelName string) bool {
	_, ok := ratio_setting.GetModelPrice(modelName, false)
	if ok {
		return true
	}
	_, ok, _ = ratio_setting.GetModelRatio(modelName)
	if ok {
		return true
	}
	return false
}
