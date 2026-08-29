package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	hubnats "github.com/LurusTech/lurus-hub/internal/pkg/nats"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// AsyncGo dispatches PostConsumeQuota's fire-and-forget side effects (usage
// reporting, wallet debit, cost-spike window, quota-threshold notify). In
// production it is gopool.Go, i.e. the same async behaviour as before. Tests
// (in this package and in other packages such as relay that call
// PostConsumeQuota) override it to run the work inline so those goroutines
// cannot outlive the test and race a later test's global-state teardown under
// the -race gate. Exported only so cross-package test binaries can set it.
var AsyncGo = gopool.Go

type TokenDetails struct {
	TextTokens  int
	AudioTokens int
}

type QuotaInfo struct {
	InputDetails  TokenDetails
	OutputDetails TokenDetails
	ModelName     string
	UsePrice      bool
	ModelPrice    float64
	ModelRatio    float64
	GroupRatio    float64
}

func hasCustomModelRatio(modelName string, currentRatio float64) bool {
	defaultRatio, exists := ratio_setting.GetDefaultModelRatioMap()[modelName]
	if !exists {
		return true
	}
	return currentRatio != defaultRatio
}

func calculateAudioQuota(info QuotaInfo) int {
	if info.UsePrice {
		modelPrice := decimal.NewFromFloat(info.ModelPrice)
		quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		groupRatio := decimal.NewFromFloat(info.GroupRatio)

		quota := modelPrice.Mul(quotaPerUnit).Mul(groupRatio)
		// Round half-up rather than truncate — matches the non-UsePrice branch
		// below (:123) and compatible_handler.go:399's Round(0) (that line
		// runs for both the UsePrice and non-UsePrice branches there, it is
		// not UsePrice-specific). A bare IntPart() truncated every fractional
		// cent down, e.g. a modelPrice*groupRatio*QuotaPerUnit of 6.5 settled
		// to 6, not 7.
		rounded := int(quota.Round(0).IntPart())
		// Post-hoc floor: on this branch there is no modelRatio/groupRatio
		// "ratio" term to test (the price is per-call, not per-token), so the
		// correct nonzero-input guard is ModelPrice != 0, not the `ratio` used
		// below. Same intent as compatible_handler.go:422-424's
		// floor-sub-unit-to-1, different predicate (see that branch's own
		// note further down in this file). Triggers only when
		// modelPrice*groupRatio*QuotaPerUnit(500000) rounds to 0, i.e.
		// modelPrice*groupRatio < 1e-6 — reachable in production whenever an
		// admin-configured per-call model price is sub-$0.000001; see
		// TestR1CalculateAudioQuota_UsePriceSubHalfUnitFloorsToOne for the
		// exemplar.
		if info.ModelPrice != 0 && rounded == 0 {
			rounded = 1
		}
		return rounded
	}

	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(info.ModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(info.ModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(info.ModelName))

	groupRatio := decimal.NewFromFloat(info.GroupRatio)
	modelRatio := decimal.NewFromFloat(info.ModelRatio)
	ratio := groupRatio.Mul(modelRatio)

	inputTextTokens := decimal.NewFromInt(int64(info.InputDetails.TextTokens))
	outputTextTokens := decimal.NewFromInt(int64(info.OutputDetails.TextTokens))
	inputAudioTokens := decimal.NewFromInt(int64(info.InputDetails.AudioTokens))
	outputAudioTokens := decimal.NewFromInt(int64(info.OutputDetails.AudioTokens))

	quota := decimal.Zero
	quota = quota.Add(inputTextTokens)
	quota = quota.Add(outputTextTokens.Mul(completionRatio))
	quota = quota.Add(inputAudioTokens.Mul(audioRatio))
	quota = quota.Add(outputAudioTokens.Mul(audioRatio).Mul(audioCompletionRatio))

	quota = quota.Mul(ratio)

	// If ratio is not zero and quota is less than or equal to zero, set quota to 1
	if !ratio.IsZero() && quota.LessThanOrEqual(decimal.Zero) {
		quota = decimal.NewFromInt(1)
	}

	rounded := int(quota.Round(0).IntPart())
	// Post-hoc floor: the guard above only fires on quota<=0 (exactly zero or
	// negative pre-round); a strictly-positive 0<quota<0.5 still rounds down
	// to 0 here without this, settling a real, already-served audio call to a
	// free one. Mirrors compatible_handler.go:422-424.
	if !ratio.IsZero() && rounded == 0 {
		rounded = 1
	}
	return rounded
}

func PreWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage) error {
	if relayInfo.UsePrice {
		return nil
	}
	userQuota, err := repo.GetUserQuota(relayInfo.UserId, false)
	if err != nil {
		return err
	}

	token, err := repo.GetTokenByKey(strings.TrimPrefix(relayInfo.TokenKey, "sk-"), false)
	if err != nil {
		return err
	}

	modelName := relayInfo.OriginModelName
	textInputTokens := usage.InputTokenDetails.TextTokens
	textOutTokens := usage.OutputTokenDetails.TextTokens
	audioInputTokens := usage.InputTokenDetails.AudioTokens
	audioOutTokens := usage.OutputTokenDetails.AudioTokens
	groupRatio := ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
	modelRatio, _, _ := ratio_setting.GetModelRatio(modelName)

	autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
	if exists {
		groupRatio = ratio_setting.GetGroupRatio(autoGroup.(string))
		log.Printf("final group ratio: %f", groupRatio)
		relayInfo.UsingGroup = autoGroup.(string)
	}

	actualGroupRatio := groupRatio
	userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
	if ok {
		actualGroupRatio = userGroupRatio
	}

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  modelName,
		UsePrice:   relayInfo.UsePrice,
		ModelRatio: modelRatio,
		GroupRatio: actualGroupRatio,
	}

	quota := calculateAudioQuota(quotaInfo)

	if userQuota < quota {
		return fmt.Errorf("user quota is not enough, user quota: %s, need quota: %s", logger.FormatQuota(userQuota), logger.FormatQuota(quota))
	}

	if !token.UnlimitedQuota && token.RemainQuota < quota {
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(token.RemainQuota), logger.FormatQuota(quota))
	}

	err = PostConsumeQuota(relayInfo, quota, 0, false)
	if err != nil {
		return err
	}
	logger.LogInfo(ctx, "realtime streaming consume quota success, quota: "+fmt.Sprintf("%d", quota))
	return nil
}

func PostWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelName string,
	usage *dto.RealtimeUsage, extraContent string) {

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	textInputTokens := usage.InputTokenDetails.TextTokens
	textOutTokens := usage.OutputTokenDetails.TextTokens

	audioInputTokens := usage.InputTokenDetails.AudioTokens
	audioOutTokens := usage.OutputTokenDetails.AudioTokens

	tokenName := ctx.GetString("token_name")
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(modelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(relayInfo.OriginModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(modelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  modelName,
		UsePrice:   usePrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}

	quota := calculateAudioQuota(quotaInfo)

	totalTokens := usage.TotalTokens
	var logContent string
	if !usePrice {
		logContent = fmt.Sprintf("模型倍率 %.2f，补全倍率 %.2f，音频倍率 %.2f，音频补全倍率 %.2f，分组倍率 %.2f",
			modelRatio, completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), groupRatio)
	} else {
		logContent = fmt.Sprintf("模型价格 %.2f，分组倍率 %.2f", modelPrice, groupRatio)
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += fmt.Sprintf("（可能是上游超时）")
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	} else {
		repo.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		repo.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	logModel := modelName
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := GenerateWssOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	logParams := repo.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	}
	governance.EnrichLogParams(ctx, relayInfo, &logParams)
	repo.RecordConsumeLog(ctx, relayInfo.UserId, logParams)
}

func PostClaudeConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) {

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	promptTokens := usage.PromptTokens
	completionTokens := usage.CompletionTokens
	modelName := relayInfo.OriginModelName

	tokenName := ctx.GetString("token_name")
	completionRatio := relayInfo.PriceData.CompletionRatio
	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	cacheRatio := relayInfo.PriceData.CacheRatio
	cacheTokens := usage.PromptTokensDetails.CachedTokens

	cacheCreationRatio := relayInfo.PriceData.CacheCreationRatio
	cacheCreationRatio5m := relayInfo.PriceData.CacheCreation5mRatio
	cacheCreationRatio1h := relayInfo.PriceData.CacheCreation1hRatio
	cacheCreationTokens := usage.PromptTokensDetails.CachedCreationTokens
	cacheCreationTokens5m := usage.ClaudeCacheCreation5mTokens
	cacheCreationTokens1h := usage.ClaudeCacheCreation1hTokens

	// Prompt-base deduction keyed on the WIRE SEMANTICS stamped where the
	// usage was parsed (dto.Usage.PromptTokensIncludeCached), not on channel
	// type: the old gate here was `ChannelType == OpenRouter`, which missed
	// every other OpenAI-wire channel serving /v1/messages (OpenAI, Azure,
	// Ollama, Xinference, SiliconFlow, Perplexity, BaiduV2, VolcEngine on a
	// non-special base — all funnel through provider/openai/relay-openai.go,
	// whose prompt_tokens INCLUDES cached_tokens). On those, cached tokens
	// were billed at full input price PLUS CacheRatio — 11x the intended
	// price on a fully cached prompt at CacheRatio=0.1. Anthropic-wire
	// channels (anthropic/aws/vertex-claude and the ali/zhipu_4v/deepseek/
	// moonshot Claude passthroughs) leave the flag false: their input_tokens
	// already exclude cache reads/writes, so no deduction — unchanged.
	if usage.PromptTokensIncludeCached {
		promptTokens -= cacheTokens
		// The cost-based cache-creation inference stays OpenRouter-only: it
		// reads OpenRouter's proprietary usage.Cost field.
		if relayInfo.ChannelType == constant.ChannelTypeOpenRouter {
			isUsingCustomSettings := relayInfo.PriceData.UsePrice || hasCustomModelRatio(modelName, relayInfo.PriceData.ModelRatio)
			if cacheCreationTokens == 0 && relayInfo.PriceData.CacheCreationRatio != 1 && usage.Cost != 0 && !isUsingCustomSettings {
				maybeCacheCreationTokens := CalcOpenRouterCacheCreateTokens(*usage, relayInfo.PriceData)
				if maybeCacheCreationTokens >= 0 && promptTokens >= maybeCacheCreationTokens {
					cacheCreationTokens = maybeCacheCreationTokens
				}
			}
		}
		promptTokens -= cacheCreationTokens
	}

	remainingCacheCreationTokens := cacheCreationTokens - cacheCreationTokens5m - cacheCreationTokens1h
	if remainingCacheCreationTokens < 0 {
		remainingCacheCreationTokens = 0
	}

	calculateQuota := claudeCalculateQuota(relayInfo.PriceData.UsePrice,
		promptTokens, cacheTokens, cacheCreationTokens5m, cacheCreationTokens1h, remainingCacheCreationTokens, completionTokens,
		cacheRatio, cacheCreationRatio5m, cacheCreationRatio1h, cacheCreationRatio, completionRatio, groupRatio, modelRatio, modelPrice)

	// Predicate centralized in r5a_price_floor.go (ChargeableInputNonZero):
	// under UsePrice this reads modelPrice!=0 instead of the always-false
	// modelRatio!=0 (helper.ModelPriceHelper never assigns ModelRatio when
	// UsePrice is true — see that file's header comment for the full chain).
	if ChargeableInputNonZero(relayInfo.PriceData.UsePrice, modelPrice, modelRatio) && calculateQuota.LessThanOrEqual(decimal.Zero) {
		calculateQuota = decimal.NewFromInt(1)
	}

	// Claude native web search tool fee — same formula as the
	// OpenAI-compatible sibling path (relay/compatible_handler.go:280-286),
	// added here so /v1/messages charges it too instead of only logging it
	// (F1). claude_web_search_requests has exactly two non-test write sites
	// (grepped 2026-08-27): provider/claude/relay-claude.go's
	// HandleClaudeResponseData (non-streaming — reached from ClaudeHandler and
	// aws/relay-aws.go's awsHandler) and, since the G2 fix, the same file's
	// HandleStreamFinalResponse (streaming — reached from ClaudeStreamHandler
	// and aws/relay-aws.go's awsStreamHandler). A single request reaches
	// exactly one of the two Set call sites, so this read below sees the
	// count from whichever path actually served that request — streaming and
	// non-streaming Claude native web search calls are now both charged
	// through this same block.
	claudeWebSearchCallCount := ctx.GetInt("claude_web_search_requests")
	var claudeWebSearchPrice float64
	var claudeWebSearchFee decimal.Decimal
	if claudeWebSearchCallCount > 0 {
		claudeWebSearchPrice = operation_setting.GetClaudeWebSearchPricePerThousand()
		claudeWebSearchFee = decimal.NewFromFloat(claudeWebSearchPrice).
			Div(decimal.NewFromInt(1000)).
			Mul(decimal.NewFromFloat(groupRatio)).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
			Mul(decimal.NewFromInt(int64(claudeWebSearchCallCount)))
		calculateQuota = calculateQuota.Add(claudeWebSearchFee)
	}

	// Round half-up rather than truncate — matches the OpenAI-compatible
	// sibling path (compatible_handler.go:399, decimal.Round(0)). The whole
	// accumulation above is decimal end-to-end (claudeCalculateQuota), not
	// float64: a float64 accumulator hit real precision loss on this path —
	// e.g. modelRatio=0.125, groupRatio=2.5, completionRatio=0.7, prompt=4,
	// completion=24 summed to 6.49999999999999911182 in float64 (rounds to 6)
	// versus the exact decimal 6.5 (rounds to 7) the compatible-handler
	// sibling path computes for the same inputs. See
	// r1_quota_decimal_parity_test.go for the reproduction.
	quota := int(calculateQuota.Round(0).IntPart())

	totalTokens := promptTokens + completionTokens

	var logContent string
	if claudeWebSearchCallCount > 0 {
		logContent += fmt.Sprintf("Claude Web Search 调用 %d 次，调用花费 %s", claudeWebSearchCallCount, claudeWebSearchFee.String())
	}
	// record all the consume log even if quota is 0
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += fmt.Sprintf("（可能是上游出错）")
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	} else {
		// Post-hoc floor: a chargeable input (ChargeableInputNonZero — same
		// predicate as the pre-round guard above) with a nonzero-but-
		// sub-half-unit calc must never round down to a free call. That
		// pre-round guard does NOT cover this case on its own (it only fires
		// on an exactly-zero or negative calculateQuota), which is exactly the
		// gap that let a 0<calc<1 call settle to quota==0 while the upstream
		// had already served a real response. When groupRatio==0 instead,
		// calculateQuota is exactly 0 and the pre-round guard (sharing this
		// floor's predicate) already lifts it to 1 before Round(0) runs, so
		// this floor sees quota==1 and never fires for that case — verified by
		// probe (modelRatio=0.75, groupRatio=0 via PostClaudeConsumeQuota):
		// debited quota is 1 whether or not this floor exists.
		if ChargeableInputNonZero(relayInfo.PriceData.UsePrice, modelPrice, modelRatio) && quota == 0 {
			quota = 1
		}
		repo.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		repo.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	quotaDelta := quota - relayInfo.FinalPreConsumedQuota

	if quotaDelta > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费后补扣费：%s（实际消耗：%s，预扣费：%s）",
			logger.FormatQuota(quotaDelta),
			logger.FormatQuota(quota),
			logger.FormatQuota(relayInfo.FinalPreConsumedQuota),
		))
	} else if quotaDelta < 0 {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费后返还扣费：%s（实际消耗：%s，预扣费：%s）",
			logger.FormatQuota(-quotaDelta),
			logger.FormatQuota(quota),
			logger.FormatQuota(relayInfo.FinalPreConsumedQuota),
		))
	}

	// Always settle, even when quotaDelta == 0 (exact estimate, e.g. fixed-price
	// models). Skipping the call on a zero delta also skipped the tenant-pool
	// debit and the platform pre-auth settlement, letting the pre-auth TTL
	// expire and release paid-for revenue. PostConsumeQuota handles quota == 0
	// safely (the +0 local writes are no-ops; pool + settle key on totalQuota).
	err := PostConsumeQuota(relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, true)
	if err != nil {
		logger.LogError(ctx, "error consuming token remain quota: "+err.Error())
	}

	other := GenerateClaudeOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio,
		cacheTokens, cacheRatio,
		cacheCreationTokens, cacheCreationRatio,
		cacheCreationTokens5m, cacheCreationRatio5m,
		cacheCreationTokens1h, cacheCreationRatio1h,
		modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if claudeWebSearchCallCount > 0 {
		// Set directly on the map GenerateClaudeOtherInfo returns rather than
		// inside that shared generator — these three keys already exist in the
		// projection contract (repo/log.go:146-147 strips web_search_price /
		// web_search_call_count for non-admins; the frontend already reads all
		// three: web/src/hooks/usage-logs/useUsageLogsData.jsx:382-383,459-461),
		// so no new key + no change to GenerateClaudeOtherInfo itself (its only
		// production call site is the one immediately above, this function).
		other["web_search"] = true
		other["web_search_call_count"] = claudeWebSearchCallCount
		other["web_search_price"] = claudeWebSearchPrice
	}
	logParams := repo.RecordConsumeLogParams{
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
	}
	governance.EnrichLogParams(ctx, relayInfo, &logParams)
	repo.RecordConsumeLog(ctx, relayInfo.UserId, logParams)
}

// claudeCalculateQuota computes the Claude-native settlement amount entirely
// in decimal (shopspring/decimal) — the accumulation used to be float64 (see
// git history) and lost precision (see the r1_quota_decimal_parity_test.go
// exemplar: modelRatio=0.125, groupRatio=2.5, completionRatio=0.7, prompt=4,
// completion=24 summed to 6.49999999999999911182 in float64, one Round(0)
// step away from a wrong answer). Since 2026-08-29 the prompt-base deduction
// in BOTH this function's caller (PostClaudeConsumeQuota) and the
// OpenAI-compatible sibling (relay/compatible_handler.go postConsumeQuota) is
// keyed on the wire flag stamped at the usage parse site
// (dto.Usage.PromptTokensIncludeCached) — the two paths now apply the same
// deduction rule for the same usage. (The earlier version of this comment
// claimed ali/zhipu_4v/moonshot/deepseek/gemini/aws "hit that subtraction"
// when serving RelayFormatClaude — traced 2026-08-29, that was wrong: the
// dispatch in handler/relay.go switches on relay format only, so every
// /v1/messages request settles here regardless of channel, and those
// channels' Claude passthroughs produce Anthropic-wire usage anyway.)
// Extracted to a pure function (no gin.Context, no DB) so a fixed-input grid
// test can diff it against an independently hand-typed decimal expression
// without sharing code with the thing under test.
func claudeCalculateQuota(usePrice bool,
	promptTokens, cacheTokens, cacheCreationTokens5m, cacheCreationTokens1h, remainingCacheCreationTokens, completionTokens int,
	cacheRatio, cacheCreationRatio5m, cacheCreationRatio1h, cacheCreationRatio, completionRatio, groupRatio, modelRatio, modelPrice float64,
) decimal.Decimal {
	dGroupRatio := decimal.NewFromFloat(groupRatio)

	if usePrice {
		return decimal.NewFromFloat(modelPrice).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(dGroupRatio)
	}

	ratio := decimal.NewFromFloat(modelRatio).Mul(dGroupRatio)

	promptQuota := decimal.NewFromInt(int64(promptTokens)).
		Add(decimal.NewFromInt(int64(cacheTokens)).Mul(decimal.NewFromFloat(cacheRatio))).
		Add(decimal.NewFromInt(int64(cacheCreationTokens5m)).Mul(decimal.NewFromFloat(cacheCreationRatio5m))).
		Add(decimal.NewFromInt(int64(cacheCreationTokens1h)).Mul(decimal.NewFromFloat(cacheCreationRatio1h))).
		Add(decimal.NewFromInt(int64(remainingCacheCreationTokens)).Mul(decimal.NewFromFloat(cacheCreationRatio)))

	completionQuota := decimal.NewFromInt(int64(completionTokens)).Mul(decimal.NewFromFloat(completionRatio))

	return promptQuota.Add(completionQuota).Mul(ratio)
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
	textInputTokens := usage.PromptTokensDetails.TextTokens
	textOutTokens := usage.CompletionTokenDetails.TextTokens

	audioInputTokens := usage.PromptTokensDetails.AudioTokens
	audioOutTokens := usage.CompletionTokenDetails.AudioTokens

	tokenName := ctx.GetString("token_name")
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(relayInfo.OriginModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(relayInfo.OriginModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(relayInfo.OriginModelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  relayInfo.OriginModelName,
		UsePrice:   usePrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}

	quota := calculateAudioQuota(quotaInfo)

	totalTokens := usage.TotalTokens
	var logContent string
	if !usePrice {
		logContent = fmt.Sprintf("模型倍率 %.2f，补全倍率 %.2f，音频倍率 %.2f，音频补全倍率 %.2f，分组倍率 %.2f",
			modelRatio, completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), groupRatio)
	} else {
		logContent = fmt.Sprintf("模型价格 %.2f，分组倍率 %.2f", modelPrice, groupRatio)
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += fmt.Sprintf("（可能是上游超时）")
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, relayInfo.OriginModelName, relayInfo.FinalPreConsumedQuota))
	} else {
		repo.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		repo.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	quotaDelta := quota - relayInfo.FinalPreConsumedQuota

	if quotaDelta > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费后补扣费：%s（实际消耗：%s，预扣费：%s）",
			logger.FormatQuota(quotaDelta),
			logger.FormatQuota(quota),
			logger.FormatQuota(relayInfo.FinalPreConsumedQuota),
		))
	} else if quotaDelta < 0 {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费后返还扣费：%s（实际消耗：%s，预扣费：%s）",
			logger.FormatQuota(-quotaDelta),
			logger.FormatQuota(quota),
			logger.FormatQuota(relayInfo.FinalPreConsumedQuota),
		))
	}

	// Always settle, even when quotaDelta == 0 (exact estimate) — see the same
	// note in PostClaudeConsumeQuota. Skipping a zero delta skipped the pool
	// debit and the platform pre-auth settlement.
	err := PostConsumeQuota(relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, true)
	if err != nil {
		logger.LogError(ctx, "error consuming token remain quota: "+err.Error())
	}

	logModel := relayInfo.OriginModelName
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := GenerateAudioOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	logParams := repo.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	}
	governance.EnrichLogParams(ctx, relayInfo, &logParams)
	repo.RecordConsumeLog(ctx, relayInfo.UserId, logParams)
}

// ErrTokenQuotaInsufficient marks the per-key spending cap rejection inside
// PreConsumeTokenQuota. Distinguished from DB errors so LOCAL_LEDGER_ADVISORY
// can keep enforcing the cap (a user-set limit, not ledger state) while
// treating write failures as shadow-bookkeeping loss.
var ErrTokenQuotaInsufficient = errors.New("token quota is not enough")

func PreConsumeTokenQuota(relayInfo *relaycommon.RelayInfo, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if relayInfo.IsPlayground {
		return nil
	}
	if relayInfo.TokenUnlimited {
		// Unlimited tokens have no per-key spending cap: keep the existing
		// unconditional debit (used_quota accounting only; remain_quota is not a
		// gate for them). Behavior unchanged.
		return repo.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota)
	}
	// Limited token: atomic check-and-debit closes the read/compare/debit TOCTOU.
	// ok==false means the row lacked enough remain_quota — the same rejection the
	// old `token.RemainQuota < quota` comparison produced. It carries
	// ErrTokenQuotaInsufficient so PreConsumeQuota's advisory path still tells a
	// cap rejection (enforced even under advisory) apart from a shadow-write
	// failure.
	ok, err := repo.DecreaseTokenQuotaIfEnough(relayInfo.TokenId, relayInfo.TokenKey, quota)
	if err != nil {
		return err
	}
	if !ok {
		// Best-effort re-read only for the error message's remaining figure — the
		// atomic UPDATE already rejected; this read does not gate anything.
		remain := 0
		if tok, gErr := repo.GetTokenByKey(relayInfo.TokenKey, false); gErr == nil && tok != nil {
			remain = tok.RemainQuota
		}
		return fmt.Errorf("%w, token remain quota: %s, need quota: %s",
			ErrTokenQuotaInsufficient, logger.FormatQuota(remain), logger.FormatQuota(quota))
	}
	return nil
}

// sourceProductOf returns the resolved cross-product attribution tag for a
// relay (Workstream 0), or the default product id when unset. Used as the
// wallet productId so spend is attributable per product instead of the old
// hardcoded "lurus-api".
func sourceProductOf(relayInfo *relaycommon.RelayInfo) string {
	if relayInfo != nil && relayInfo.SourceProduct != "" {
		return relayInfo.SourceProduct
	}
	return ratio_setting.DefaultSourceProduct
}

// debitTenantPool records the post-consume debit against the token's tenant
// credit pool. Three outcomes (P0-3, ADR 2026-06-10 pool-overdraft):
//
//  1. Normal: DebitPool succeeds — relay_debit draw row.
//  2. Exhausted: the gate admitted a request that out-raced the balance.
//     The user quota and upstream tokens are already spent, so we record the
//     debt via OverdraftDebitPool (balance goes negative, relay_overdraft
//     draw row) instead of dropping the debit. The negative balance keeps
//     the relay gate closed until a topup repays it.
//  3. Hard DB error: the debit is lost — CRITICAL structured log +
//     CreditPoolDebitLostTotal counter (honest residual gap, needs manual
//     reconciliation).
//
// Tokens without a tenant, tenants without a pool row, and unlimited pools
// all skip the debit (pool gate semantics: no pool = unlimited).
func debitTenantPool(relayInfo *relaycommon.RelayInfo, quota int) {
	tok, terr := repo.GetTokenById(relayInfo.TokenId)
	if terr != nil || tok == nil || tok.TenantId == "" {
		return
	}
	pool, perr := repo.GetTenantCreditPool(tok.TenantId)
	if perr != nil {
		if errors.Is(perr, repo.ErrPoolNotFound) {
			// Distinct from IsUnlimited(): this tenant has NO pool row at all,
			// which is either a legitimate "no pool configured" tenant or a
			// tenant-id/pool drift (orphaned token pointing at a tenant that
			// never got a pool row). Surface it — a silent return here would
			// hide the same class of bug the tenant-id drift incident found.
			metrics.CreditPoolLookupMissTotal.WithLabelValues(tok.TenantId, "no_pool").Inc()
			common.SysLog(fmt.Sprintf(
				`{"event":"pool_lookup_miss","who":"tenant:%s","what":"post-consume debit %d skipped: no credit pool row (token %d)","result":"treated as unlimited, verify tenant-id is not drifted"}`,
				tok.TenantId, quota, relayInfo.TokenId))
			return
		}
		// Hard DB error resolving the pool row — same severity as a lost debit,
		// since we can't even tell whether this tenant is pool-gated.
		metrics.CreditPoolLookupMissTotal.WithLabelValues(tok.TenantId, "lookup_error").Inc()
		common.SysError(fmt.Sprintf(
			`{"event":"pool_lookup_error","who":"tenant:%s","what":"post-consume debit %d: credit pool lookup failed (token %d)","result":"debit skipped, conservation broken: %s"}`,
			tok.TenantId, quota, relayInfo.TokenId, perr.Error()))
		return
	}
	if pool == nil || pool.IsUnlimited() {
		return
	}

	derr := repo.DebitPool(pool.ID, tok.TenantId, int64(quota), relayInfo.TokenId, 0)
	if derr == nil {
		metrics.CreditPoolDebitTotal.WithLabelValues(tok.TenantId).Inc()
		metrics.CreditPoolBalance.WithLabelValues(tok.TenantId).Set(float64(pool.CurrentBalance - int64(quota)))
		governance.RecordAuditEvent(governance.NewDetachedAuditEvent(
			tok.TenantId, governance.ActorSystem, relayInfo.UserId,
			governance.ActionBillingDebit, governance.ResourceTenant, int(pool.ID),
			fmt.Sprintf(`{"quota":%d,"pool_id":%d,"token_id":%d}`, quota, pool.ID, relayInfo.TokenId)))
		return
	}

	if errors.Is(derr, repo.ErrPoolExhausted) {
		newBalance, oerr := repo.OverdraftDebitPool(pool.ID, tok.TenantId, int64(quota), relayInfo.TokenId, 0)
		if oerr == nil {
			metrics.CreditPoolOverdraftTotal.WithLabelValues(tok.TenantId).Inc()
			metrics.CreditPoolBalance.WithLabelValues(tok.TenantId).Set(float64(newBalance))
			common.SysLog(fmt.Sprintf(
				`{"event":"pool_overdraft","who":"tenant:%s","what":"post-consume debit %d on exhausted pool %d (token %d)","result":"recorded as relay_overdraft, new_balance=%d"}`,
				tok.TenantId, quota, pool.ID, relayInfo.TokenId, newBalance))
			return
		}
		derr = oerr
	}

	// Hard DB error on either path: the debit is dropped. This is the honest
	// residual gap — surface it loudly instead of a quiet info log.
	metrics.CreditPoolDebitLostTotal.Inc()
	common.SysError(fmt.Sprintf(
		`{"event":"pool_debit_lost","who":"tenant:%s","what":"post-consume debit %d on pool %d (token %d) failed","result":"debit NOT recorded, conservation broken: %s"}`,
		tok.TenantId, quota, pool.ID, relayInfo.TokenId, derr.Error()))
}

func PostConsumeQuota(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) (err error) {
	// Track A: for platform-governed requests under LOCAL_LEDGER_ADVISORY the
	// local ledger is a shadow meter — its write failures are recorded as
	// meter loss (metrics + drift gap) but never block the platform
	// settlement, which is the ledger of record.
	advisory := common.LocalLedgerAdvisory() && relayInfo.PlatformGoverned

	// Phase 1: Update local user quota
	if quota > 0 {
		err = repo.DecreaseUserQuota(relayInfo.UserId, quota)
	} else {
		err = repo.IncreaseUserQuota(relayInfo.UserId, -quota, false)
	}
	if err != nil {
		if !advisory {
			return err
		}
		metrics.BillingAdvisoryMeterLost.WithLabelValues("user_quota").Inc()
		common.SysLog(fmt.Sprintf("advisory: user quota shadow write lost (settle proceeds): userId=%d, quota=%d, err=%s",
			relayInfo.UserId, quota, err.Error()))
		err = nil
	}

	// Phase 2: Update daily quota (non-critical, best-effort)
	if quota > 0 {
		if dailyErr := repo.PostConsumeDailyQuota(relayInfo.UserId, quota); dailyErr != nil {
			common.SysLog("failed to update daily quota: " + dailyErr.Error())
		}
	}

	// Phase 2.5: Tenant credit pool debit (ADR 2026-05-18 §5, P0-3 overdraft
	// semantics 2026-06-10). Never fails the user-facing response, but the
	// debit itself is no longer best-effort: exhaustion falls back to an
	// overdraft draw so the ledger stays conserved.
	//
	// The pool is NOT touched during pre-consume, so it must be debited the
	// request's ACTUAL COST (delta + preConsumed = totalQuota), not just the
	// post-consume delta `quota`. Using the delta under-charged every pooled
	// request by the pre-consumed amount and dropped the debit entirely when
	// the estimate met or exceeded the actual cost (quota <= 0).
	poolDebit := quota + preConsumedQuota
	if poolDebit > 0 && relayInfo.TokenId > 0 {
		debitTenantPool(relayInfo, poolDebit)
	}

	// PHASE B SEAM (project budget enforcement / chargeback) — NOT IMPLEMENTED.
	// This is where the post-settlement budget check belongs: after the tenant
	// pool debit and before Phase 3, with relayInfo.ProjectId (migration 029)
	// already in scope and poolDebit being the request's actual cost. The
	// paired pre-request rejection belongs in a middleware.ProjectBudgetGate()
	// mounted after TokenAuth. Migration 029 deliberately ships NO budget
	// column: showback (per-project reporting) is what exists today.



	// Phase 3: Update token quota with compensation on failure.
	// If token quota update fails, we release the platform pre-auth rather than
	// settling — prevents double-debit when local state is inconsistent.
	localQuotaConsistent := true
	if !relayInfo.IsPlayground {
		if quota > 0 {
			err = repo.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota)
		} else {
			err = repo.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, -quota)
		}
		if err != nil {
			localQuotaConsistent = false
			if advisory {
				metrics.BillingAdvisoryMeterLost.WithLabelValues("token_quota").Inc()
			}
			common.SysLog(fmt.Sprintf("token quota update failed, compensating: userId=%d, quota=%d, err=%s",
				relayInfo.UserId, quota, err.Error()))
			if quota > 0 {
				if compErr := repo.IncreaseUserQuota(relayInfo.UserId, quota, false); compErr != nil {
					common.SysError(fmt.Sprintf("CRITICAL: compensation failed: userId=%d, quota=%d, err=%s",
						relayInfo.UserId, quota, compErr.Error()))
				}
			} else {
				if compErr := repo.DecreaseUserQuota(relayInfo.UserId, -quota); compErr != nil {
					common.SysError(fmt.Sprintf("CRITICAL: compensation failed: userId=%d, quota=%d, err=%s",
						relayInfo.UserId, -quota, compErr.Error()))
				}
			}
			// Don't return yet — must still handle platform pre-auth release below
		}
	}

	// Phase 4: Email notification (non-critical, async)
	if sendEmail && (quota+preConsumedQuota) != 0 {
		checkAndSendQuotaNotify(relayInfo, quota, preConsumedQuota)
	}

	// Phase 5: Platform wallet settlement.
	// Decision: use PlatformPreAuthID > 0 as the signal (not the feature flag),
	// because the pre-auth was created under the flag state at request start.
	// This prevents orphaned pre-auths when the flag is toggled mid-flight.
	totalQuota := quota + preConsumedQuota

	// Business TPM window recording — the usage side of
	// middleware.BusinessRateLimit's tokens-per-minute dimension
	// (business_tpm.go). Deliberately OUTSIDE the platform-wallet gate below:
	// tpm_limit must bite for local-quota tenants too, not only
	// wallet-bridged accounts. Same fire-and-forget fail-soft contract as
	// RecordCostSpikeWindow: recording never fails or slows the settlement.
	// The recorded measure is the settled quota (ratio-weighted token
	// equivalents) — the settled dto.Usage never reaches this funnel (the
	// relay pipeline converts it to quota before calling here), and it is the
	// same per-request usage measure the cost-spike window records. The
	// tenant is resolved synchronously (one PK SELECT, the weight class
	// debitTenantPool already pays above) so the async closure stays DB-free.
	if totalQuota > 0 && relayInfo.TokenId > 0 {
		tpmTokenID := relayInfo.TokenId
		tpmTenantID := bizTPMTenantOf(tpmTokenID)
		// Model dimension keys on the CLIENT-requested name (OriginModelName)
		// — the same value BusinessModelRateLimit reads from the gin context —
		// not the upstream-mapped name, so limit and usage always agree.
		tpmModel := relayInfo.OriginModelName
		AsyncGo(func() {
			RecordBusinessTPMUsage(tpmTokenID, tpmTenantID, totalQuota)
			RecordBusinessTPMModelUsage(tpmTenantID, tpmModel, totalQuota)
		})
	}

	// Cost-spike window recording, for the same reason the TPM block above sits
	// out here: middleware.CostSpikeLimit reads this window on EVERY relay and
	// admits the request when it finds nothing, so a window written only for
	// wallet-bridged accounts is a fuse that cannot trip for anybody else. It
	// used to live inside the platform-wallet gate below, which on the live
	// gateway meant 1 of 6 tokens accumulated a window and the other 5 were
	// permanently invisible to the fuse.
	//
	// No TokenId > 0 condition, unlike the TPM block: the window is keyed on the
	// user, and adding a token predicate here would silently narrow what gets
	// recorded relative to the behaviour this replaces. RecordCostSpikeWindow
	// carries its own no-op guards (protection disabled, Redis absent,
	// non-positive tokens), so this stays a fire-and-forget that never fails or
	// slows the settlement.
	if totalQuota > 0 {
		costSpikeUserID := relayInfo.UserId
		AsyncGo(func() {
			RecordCostSpikeWindow(costSpikeUserID, totalQuota)
		})
	}

	if relayInfo.IdentityAccountID > 0 && totalQuota > 0 {
		accountID := relayInfo.IdentityAccountID
		amountLB := float64(totalQuota) / common.QuotaPerUnit
		warnZeroWalletAmount(accountID, totalQuota, amountLB)

		if relayInfo.PlatformPreAuthID > 0 {
			preAuthID := relayInfo.PlatformPreAuthID
			charged := false
			if !localQuotaConsistent && !advisory {
				// Local quota is inconsistent — release pre-auth instead of settling,
				// to avoid charging the wallet for a request that wasn't properly recorded locally.
				releasePlatformPreAuth(relayInfo)
			} else {
				// Advisory mode settles even when the local shadow write was
				// inconsistent: the platform wallet is the ledger of record and
				// the meter loss was already counted above — dropping revenue
				// over a shadow-bookkeeping failure would invert the hierarchy.
				settleCtx, settleCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, settleErr := common.SettleWithBreaker(settleCtx, relayInfo.PlatformPreAuthID, amountLB)
				settleCancel()
				charged = true

				if settleErr != nil {
					metrics.BillingSettleTotal.WithLabelValues("error").Inc()
					common.SysLog(fmt.Sprintf("settle pre-auth %d failed, enqueuing: %s",
						relayInfo.PlatformPreAuthID, settleErr.Error()))
					if enqErr := EnqueueSettle(accountID, relayInfo.PlatformPreAuthID, amountLB); enqErr != nil {
						common.SysError(fmt.Sprintf("CRITICAL: pre-auth %d — settle failed (%s), outbox failed (%s). "+
							"Platform TTL will auto-expire. Manual reconciliation may be needed.",
							relayInfo.PlatformPreAuthID, settleErr.Error(), enqErr.Error()))
					}
				} else {
					metrics.BillingSettleTotal.WithLabelValues("success").Inc()
					// Invalidate cached balance so next request gets fresh data
					common.InvalidateCachedWalletBalance(accountID)
				}
			}
			// Mark pre-auth as handled (settled or released)
			relayInfo.PlatformPreAuthID = 0

			// Report usage for VIP accumulation (async, non-critical)
			AsyncGo(func() {
				rptCtx, rptCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer rptCancel()
				common.ReportLLMUsageGRPC(rptCtx, accountID, amountLB)
				reportQuotaThreshold(rptCtx, relayInfo, totalQuota)
				if charged {
					// Track A mirror metering: one usage_events row per charged
					// relay, joined against wallet_transactions by the daily
					// drift reconciliation. Keyed on the pre-auth id so a
					// re-report is deduped platform-side.
					mirrorUsageEvent(rptCtx, relayInfo, accountID, totalQuota, amountLB,
						fmt.Sprintf("llm-relay:settle:%d", preAuthID), preAuthID)
				}
			})
		} else {
			// Legacy path: fire-and-forget debit (no pre-auth)
			refID := "llm-usage:" + uuid.NewString()
			AsyncGo(func() {
				debitCtx, debitCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer debitCancel()
				if _, debitErr := common.DebitWalletGRPC(debitCtx, accountID, amountLB, "llm_usage",
					fmt.Sprintf("relay userId=%d", relayInfo.UserId), sourceProductOf(relayInfo),
					refID); debitErr != nil {
					common.SysLog(fmt.Sprintf("legacy wallet debit failed: accountID=%d, amount=%.4f LB, err=%s",
						accountID, amountLB, debitErr.Error()))
				}
				common.ReportLLMUsageGRPC(debitCtx, accountID, amountLB)
				reportQuotaThreshold(debitCtx, relayInfo, totalQuota)
				// Mirror the legacy debit too (shared refID joins the pair):
				// wallet>usage here would hide double charges from the drift SQL.
				mirrorUsageEvent(debitCtx, relayInfo, accountID, totalQuota, amountLB, refID, 0)
			})
		}
	}

	// Return the original token quota error if local state was inconsistent
	// (advisory mode already counted it as shadow meter loss and settled).
	if !localQuotaConsistent && !advisory {
		return err
	}
	return nil
}

// mirrorUsageEvent posts one charged relay into the platform's
// billing.usage_events (metric=llm_relay) — the metering half the Track A
// drift reconciliation joins against wallet_transactions. Fire-and-forget:
// failure only loses this reconciliation data point (counted), never money.
func mirrorUsageEvent(ctx context.Context, relayInfo *relaycommon.RelayInfo, accountID int64, totalQuota int, amountLB float64, idemKey string, preAuthID int64) {
	meta := map[string]any{
		"amount_cny": amountLB,
		"model":      relayInfo.OriginModelName,
		"user_id":    relayInfo.UserId,
	}
	if preAuthID > 0 {
		meta["preauth_id"] = preAuthID
	}
	if rptErr := common.ReportUsageEvent(ctx, accountID, sourceProductOf(relayInfo), "llm_relay",
		int64(totalQuota), idemKey, meta); rptErr != nil {
		metrics.BillingUsageMirrorTotal.WithLabelValues("error").Inc()
		common.SysLog(fmt.Sprintf("usage mirror failed (drift data point lost): account=%d, key=%s, err=%s",
			accountID, idemKey, rptErr.Error()))
		return
	}
	metrics.BillingUsageMirrorTotal.WithLabelValues("success").Inc()
}

// zeroWalletWarnLast tracks, per platform account, the last time
// warnZeroWalletAmount emitted its ERROR-level log line — a 1/minute-per-
// account throttle. QuotaPerUnit=500000 means any settlement with
// totalQuota<25 trips the leak condition below, and a live single relay call
// settles to totalQuota in the 1-2 range, so before this throttle every
// identity-linked-token request logged one ERROR line (D-A5): with several
// such tokens in flight this was effectively a log line per request, not a
// leak signal. The metrics counter below stays UNCONDITIONAL (D-A5) —
// throttling it too would hide *how many* charges were lost, only the log
// noise is gated. Deliberately per-account, not global: a global throttle
// would mask a second, different account tripping the same condition in the
// same window (D-A5 explicitly forbids a global throttle for this reason).
var zeroWalletWarnLast sync.Map // accountID(int64) -> time.Time

// zeroWalletWarnLogf is a seam over common.SysError so tests can observe the
// throttle's actual output. Without this indirection, tests have no way to
// tell "the throttle suppressed this call" apart from "the throttle never
// fired at all" — common.SysError writes to the process log, which is not
// something a unit test can assert against; grep across the repo (before
// this seam existed) found zero tests capturing common.SysError or the
// wallet_zero_amount_charge event, i.e. the throttle body itself (the
// zeroWalletWarnLast.LoadOrStore block below) had no test that would fail if
// its suppression `return` were deleted.
var zeroWalletWarnLogf = common.SysError

// resetZeroWalletWarnThrottle clears the per-account throttle state. Test-only
// seam: without it, warnZeroWalletAmount's log-suppression window makes tests
// that call it directly for the same accountID order-dependent on wall clock.
func resetZeroWalletWarnThrottle() {
	zeroWalletWarnLast.Range(func(key, _ any) bool {
		zeroWalletWarnLast.Delete(key)
		return true
	})
}

// warnZeroWalletAmount detects a strictly-positive local quota that converts
// to a platform-wallet amount that will round to 0.0000 under the wallet's
// numeric(14,4) column (< 0.00005 LB) — i.e. the local ledger recorded real,
// already-paid-for upstream usage, but the wallet-side charge is silently
// lost to truncation. Pure observation: it does not alter amountLB or the
// settle/debit call that follows it, and it does not touch the wallet schema
// itself: the wallet tables live in the platform/identity repository, not this
// one, so widening that column is a separate change there. Until it lands this
// counter is the only signal that a paid-for call was billed as free.
func warnZeroWalletAmount(accountID int64, totalQuota int, amountLB float64) {
	if totalQuota <= 0 || amountLB >= 0.00005 {
		return
	}
	metrics.BillingZeroAmountChargeTotal.Inc()

	now := time.Now()
	if last, loaded := zeroWalletWarnLast.LoadOrStore(accountID, now); loaded {
		if now.Sub(last.(time.Time)) < time.Minute {
			return
		}
		zeroWalletWarnLast.Store(accountID, now)
	}

	zeroWalletWarnLogf(fmt.Sprintf(
		`{"event":"wallet_zero_amount_charge","who":"account:%d","what":"positive local quota %d converts to %.6f LB","result":"will round to 0.0000 under wallet numeric(14,4), charge silently lost"}`,
		accountID, totalQuota, amountLB))
}

// reportQuotaThreshold fetches the user's current quota state from DB and
// fires checkAndPublishQuotaThresholds. Designed to be called inside an
// existing async goroutine — it must not panic or block the caller.
// consumed is the total quota consumed in this transaction (positive int).
func reportQuotaThreshold(ctx context.Context, relayInfo *relaycommon.RelayInfo, consumed int) {
	if !hubnats.Enabled() {
		return
	}
	pub := hubnats.Get()
	if pub == nil {
		return
	}
	if relayInfo.IdentityAccountID <= 0 || relayInfo.UserId == 0 || consumed <= 0 {
		return
	}

	user, err := repo.GetUserById(relayInfo.UserId)
	if err != nil {
		common.SysLog(fmt.Sprintf("quota threshold: failed to fetch user %d: %s", relayInfo.UserId, err.Error()))
		return
	}

	// user.Quota = remaining after this transaction (decremented in Phase 1).
	// user.UsedQuota = historical used quota (may or may not include this transaction
	// depending on batch-update timing; best-effort).
	usedAfter := int64(user.UsedQuota)
	limitTokens := int64(user.Quota) + usedAfter

	var rdb redisDeduper
	if common.RedisEnabled && common.RDB != nil {
		rdb = wrapRedis(common.RDB)
	}

	checkAndPublishQuotaThresholds(ctx, quotaThresholdParams{
		UserId:            relayInfo.UserId,
		IdentityAccountID: relayInfo.IdentityAccountID,
		TenantID:          user.TenantId,
		QuotaConsumed:     int64(consumed),
		UsedTokensAfter:   usedAfter,
		LimitTokens:       limitTokens,
	}, rdb, pub)
}

func checkAndSendQuotaNotify(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int) {
	AsyncGo(func() {
		userSetting := relayInfo.UserSetting
		threshold := common.QuotaRemindThreshold
		if userSetting.QuotaWarningThreshold != 0 {
			threshold = int(userSetting.QuotaWarningThreshold)
		}

		//noMoreQuota := userCache.Quota-(quota+preConsumedQuota) <= 0
		quotaTooLow := false
		consumeQuota := quota + preConsumedQuota
		if relayInfo.UserQuota-consumeQuota < threshold {
			quotaTooLow = true
		}
		if quotaTooLow {
			prompt := "您的额度即将用尽"
			topUpLink := fmt.Sprintf("%s/console/topup", system_setting.ServerAddress)

			// 根据通知方式生成不同的内容格式
			var content string
			var values []interface{}

			notifyType := userSetting.NotifyType
			if notifyType == "" {
				notifyType = dto.NotifyTypeEmail
			}

			if notifyType == dto.NotifyTypeBark {
				// Bark推送使用简短文本，不支持HTML
				content = "{{value}}，剩余额度：{{value}}，请及时充值"
				values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota)}
			} else if notifyType == dto.NotifyTypeGotify {
				content = "{{value}}，当前剩余额度为 {{value}}，请及时充值。"
				values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota)}
			} else {
				// 默认内容格式，适用于Email和Webhook（支持HTML）
				content = "{{value}}，当前剩余额度为 {{value}}，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='{{value}}'>{{value}}</a>"
				values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota), topUpLink, topUpLink}
			}

			err := NotifyUser(context.TODO(), relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values))
			if err != nil {
				common.SysError(fmt.Sprintf("failed to send quota notify to user %d: %s", relayInfo.UserId, err.Error()))
			}
		}
	})
}
