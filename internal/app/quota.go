package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
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
		return int(quota.IntPart())
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

	return int(quota.Round(0).IntPart())
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

	if relayInfo.ChannelType == constant.ChannelTypeOpenRouter {
		promptTokens -= cacheTokens
		isUsingCustomSettings := relayInfo.PriceData.UsePrice || hasCustomModelRatio(modelName, relayInfo.PriceData.ModelRatio)
		if cacheCreationTokens == 0 && relayInfo.PriceData.CacheCreationRatio != 1 && usage.Cost != 0 && !isUsingCustomSettings {
			maybeCacheCreationTokens := CalcOpenRouterCacheCreateTokens(*usage, relayInfo.PriceData)
			if maybeCacheCreationTokens >= 0 && promptTokens >= maybeCacheCreationTokens {
				cacheCreationTokens = maybeCacheCreationTokens
			}
		}
		promptTokens -= cacheCreationTokens
	}

	calculateQuota := 0.0
	if !relayInfo.PriceData.UsePrice {
		calculateQuota = float64(promptTokens)
		calculateQuota += float64(cacheTokens) * cacheRatio
		calculateQuota += float64(cacheCreationTokens5m) * cacheCreationRatio5m
		calculateQuota += float64(cacheCreationTokens1h) * cacheCreationRatio1h
		remainingCacheCreationTokens := cacheCreationTokens - cacheCreationTokens5m - cacheCreationTokens1h
		if remainingCacheCreationTokens > 0 {
			calculateQuota += float64(remainingCacheCreationTokens) * cacheCreationRatio
		}
		calculateQuota += float64(completionTokens) * completionRatio
		calculateQuota = calculateQuota * groupRatio * modelRatio
	} else {
		calculateQuota = modelPrice * common.QuotaPerUnit * groupRatio
	}

	if modelRatio != 0 && calculateQuota <= 0 {
		calculateQuota = 1
	}

	quota := int(calculateQuota)

	totalTokens := promptTokens + completionTokens

	var logContent string
	// record all the consume log even if quota is 0
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += fmt.Sprintf("（可能是上游出错）")
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
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

	if relayInfo.IdentityAccountID > 0 && totalQuota > 0 {
		accountID := relayInfo.IdentityAccountID
		amountLB := float64(totalQuota) / common.QuotaPerUnit

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
				RecordCostSpikeWindow(relayInfo.UserId, totalQuota)
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
				RecordCostSpikeWindow(relayInfo.UserId, totalQuota)
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
