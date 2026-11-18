package app

import (
	"fmt"
	"math"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func PostWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelName string,
	usage *dto.RealtimeUsage, extraContent string) {
	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	helper.ResettleContextTier(&relayInfo.PriceData, relayInfo.OriginModelName, usage.InputTokens)

	tokenName := ctx.GetString("token_name")
	completionRatioValue := ratio_setting.GetCompletionRatio(modelName)
	if tier := ratio_setting.GetContextLengthTier(relayInfo.OriginModelName, usage.InputTokens); tier != nil && tier.CompletionRatio != nil {
		completionRatioValue = *tier.CompletionRatio
	}
	completionRatio := decimal.NewFromFloat(completionRatioValue)
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(relayInfo.OriginModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(modelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quota := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: usage.InputTokenDetails.TextTokens, AudioTokens: usage.InputTokenDetails.AudioTokens},
		OutputDetails:   TokenDetails{TextTokens: usage.OutputTokenDetails.TextTokens, AudioTokens: usage.OutputTokenDetails.AudioTokens},
		ModelName:       modelName,
		UsePrice:        usePrice,
		ModelRatio:      modelRatio,
		CompletionRatio: completionRatio.InexactFloat64(),
		GroupRatio:      groupRatio,
	})

	logContent := formatAudioRateLogContent(usePrice, modelRatio, completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, groupRatio)
	if usage.TotalTokens == 0 {
		quota = 0
		logContent += "（可能是上游超时）"
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	} else {
		repo.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		repo.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := GenerateWssOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)

	if relayInfo.FinalPreConsumedQuota != 0 {
		settleErr := SettleConsume(ctx, relayInfo, 0-relayInfo.FinalPreConsumedQuota, relayInfo.FinalPreConsumedQuota, "realtime")
		FlagSettlementOutcome(other, settleErr)
	}

	recordEnrichedConsumeLog(ctx, relayInfo, repo.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		ModelName:        modelName,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
}

func PostClaudeConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) {
	RecordUpstreamCost(relayInfo, usage)
	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	promptTokens := usage.PromptTokens
	completionTokens := usage.CompletionTokens
	modelName := relayInfo.OriginModelName

	helper.ResettleContextTier(&relayInfo.PriceData, modelName, usage.AsOpenAIWire().PromptTokens)

	tokenName := ctx.GetString("token_name")
	completionRatio := relayInfo.PriceData.CompletionRatio
	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	cacheRatio := relayInfo.PriceData.CacheRatio
	cacheTokens := usage.PromptTokensDetails.CachedTokens

	cacheCreationRatio := relayInfo.PriceData.CacheCreationRatioForWire(usage.PromptTokensIncludeCached)
	cacheCreationRatio5m := relayInfo.PriceData.CacheCreation5mRatio
	cacheCreationRatio1h := relayInfo.PriceData.CacheCreation1hRatio
	cacheCreationTokens := usage.PromptTokensDetails.CachedCreationTokens
	cacheCreationTokens5m := usage.ClaudeCacheCreation5mTokens
	cacheCreationTokens1h := usage.ClaudeCacheCreation1hTokens

	billablePromptTokens := promptTokens
	if usage.PromptTokensIncludeCached {
		billablePromptTokens -= cacheTokens
		if relayInfo.ChannelType == constant.ChannelTypeOpenRouter {
			isUsingCustomSettings := relayInfo.PriceData.UsePrice || hasCustomModelRatio(modelName, relayInfo.PriceData.ModelRatio)
			if cacheCreationTokens == 0 && relayInfo.PriceData.CacheCreationRatio != 1 && usage.Cost != 0 && !isUsingCustomSettings {
				maybeCacheCreationTokens := CalcOpenRouterCacheCreateTokens(*usage, relayInfo.PriceData)
				if maybeCacheCreationTokens >= 0 && billablePromptTokens >= maybeCacheCreationTokens {
					cacheCreationTokens = maybeCacheCreationTokens
					cacheCreationRatio = relayInfo.PriceData.CacheCreationRatio
				}
			}
		}
		billablePromptTokens -= cacheCreationTokens
	}

	remainingCacheCreationTokens := cacheCreationTokens - cacheCreationTokens5m - cacheCreationTokens1h
	if remainingCacheCreationTokens < 0 {
		remainingCacheCreationTokens = 0
	}

	calculateQuota := claudeCalculateQuota(relayInfo.PriceData.UsePrice,
		billablePromptTokens, cacheTokens, cacheCreationTokens5m, cacheCreationTokens1h, remainingCacheCreationTokens, completionTokens,
		cacheRatio, cacheCreationRatio5m, cacheCreationRatio1h, cacheCreationRatio, completionRatio, groupRatio, modelRatio, modelPrice)

	if ChargeableInputNonZero(relayInfo.PriceData.UsePrice, modelPrice, modelRatio) && calculateQuota.LessThanOrEqual(decimal.Zero) {
		calculateQuota = decimal.NewFromInt(1)
	}

	claudeWebSearchCallCount, claudeWebSearchPrice, logContent, calculateQuota := applyClaudeWebSearchFee(ctx, calculateQuota, groupRatio)
	quota := int(calculateQuota.Round(0).IntPart())
	totalTokens := billablePromptTokens + completionTokens

	if totalTokens == 0 {
		quota = 0
		logContent += "（可能是上游出错）"
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	} else if ChargeableInputNonZero(relayInfo.PriceData.UsePrice, modelPrice, modelRatio) && quota == 0 {
		quota = 1
	}

	quotaDelta := quota - relayInfo.FinalPreConsumedQuota
	logPostConsumeQuotaDelta(ctx, quota, relayInfo.FinalPreConsumedQuota, quotaDelta)

	settleErr := SettleConsume(ctx, relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, "claude")
	CommitUsedQuota(relayInfo, quota, settleErr, totalTokens != 0)

	other := GenerateClaudeOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio,
		cacheTokens, cacheRatio, cacheCreationTokens, cacheCreationRatio,
		cacheCreationTokens5m, cacheCreationRatio5m, cacheCreationTokens1h, cacheCreationRatio1h,
		modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	FlagSettlementOutcome(other, settleErr)
	if claudeWebSearchCallCount > 0 {
		other["web_search"] = true
		other["web_search_call_count"] = claudeWebSearchCallCount
		other["web_search_price"] = claudeWebSearchPrice
	}
	recordEnrichedConsumeLog(ctx, relayInfo, repo.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ModelName:        modelName,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
}

func CalcOpenRouterCacheCreateTokens(usage dto.Usage, priceData types.PriceData) int {
	if priceData.CacheCreationRatio == 1 {
		return 0
	}
	quotaPrice := priceData.ModelRatio / common.QuotaPerUnit
	promptCacheCreatePrice := quotaPrice * priceData.CacheCreationRatio
	promptCacheReadPrice := quotaPrice * priceData.CacheRatio
	completionPrice := quotaPrice * priceData.CompletionRatio

	cost, _ := usage.Cost.(float64)
	totalPromptTokens := float64(usage.PromptTokens)
	completionTokens := float64(usage.CompletionTokens)
	promptCacheReadTokens := float64(usage.PromptTokensDetails.CachedTokens)

	return int(math.Round((cost -
		totalPromptTokens*quotaPrice +
		promptCacheReadTokens*(quotaPrice-promptCacheReadPrice) -
		completionTokens*completionPrice) /
		(promptCacheCreatePrice - quotaPrice)))
}

func PostAudioConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent string) {
	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	audioInputTokens := usage.PromptTokensDetails.AudioTokens
	audioOutTokens := usage.CompletionTokenDetails.AudioTokens

	helper.ResettleContextTier(&relayInfo.PriceData, relayInfo.OriginModelName, usage.AsOpenAIWire().PromptTokens)

	tokenName := ctx.GetString("token_name")
	completionRatio := decimal.NewFromFloat(relayInfo.PriceData.CompletionRatio)
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(relayInfo.OriginModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(relayInfo.OriginModelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quota := calculateAudioQuota(QuotaInfo{
		InputDetails:    TokenDetails{TextTokens: usage.PromptTokensDetails.TextTokens, AudioTokens: audioInputTokens},
		OutputDetails:   TokenDetails{TextTokens: usage.CompletionTokenDetails.TextTokens, AudioTokens: audioOutTokens},
		ModelName:       relayInfo.OriginModelName,
		UsePrice:        usePrice,
		ModelRatio:      modelRatio,
		CompletionRatio: completionRatio.InexactFloat64(),
		GroupRatio:      groupRatio,
	})

	totalTokens := usage.TotalTokens
	usageUnit, usageQty, usageSource := "token", int64(totalTokens), "upstream"
	audioSec := settleAudioSecond(ctx, relayInfo, audioInputTokens+audioOutTokens, groupRatio)
	if audioSec.active {
		quota, modelPrice, usePrice = audioSec.quota, audioSec.modelPrice, audioSec.usePrice
		usageUnit, usageQty, usageSource = audioSec.usageUnit, audioSec.usageQty, audioSec.usageSource
	}

	logContent := formatAudioRateLogContent(usePrice, modelRatio, completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, groupRatio)
	if audioSec.active {
		logContent = audioSec.logContent
	}
	if totalTokens == 0 && !audioSec.active {
		quota = 0
		logContent += "（可能是上游超时）"
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, relayInfo.OriginModelName, relayInfo.FinalPreConsumedQuota))
		usageSource = "unreported"
	}

	quotaDelta := quota - relayInfo.FinalPreConsumedQuota
	logPostConsumeQuotaDelta(ctx, quota, relayInfo.FinalPreConsumedQuota, quotaDelta)

	settleErr := SettleConsume(ctx, relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, "audio")
	CommitUsedQuota(relayInfo, quota, settleErr, totalTokens != 0 || (audioSec.active && usageQty > 0))

	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := GenerateAudioOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	FlagSettlementOutcome(other, settleErr)
	recordEnrichedConsumeLog(ctx, relayInfo, repo.RecordConsumeLogParams{
		ChannelId: relayInfo.ChannelId, PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
		ModelName: relayInfo.OriginModelName, TokenName: tokenName, Quota: quota, Content: logContent,
		TokenId: relayInfo.TokenId, UseTimeSeconds: int(useTimeSeconds), IsStream: relayInfo.IsStream,
		Group: relayInfo.UsingGroup, Other: other, UsageUnit: usageUnit, UsageQuantity: usageQty, UsageSource: usageSource,
	})
}
