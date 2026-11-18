package app

import (
	"fmt"
	"log"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type TokenDetails struct {
	TextTokens  int
	AudioTokens int
}

type QuotaInfo struct {
	InputDetails    TokenDetails
	OutputDetails   TokenDetails
	ModelName       string
	UsePrice        bool
	ModelPrice      float64
	ModelRatio      float64
	CompletionRatio float64
	GroupRatio      float64
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
		rounded := int(quota.Round(0).IntPart())
		if info.ModelPrice != 0 && rounded == 0 {
			rounded = 1
		}
		return rounded
	}

	completionRatio := decimal.NewFromFloat(info.CompletionRatio)
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

	if !ratio.IsZero() && quota.LessThanOrEqual(decimal.Zero) {
		quota = decimal.NewFromInt(1)
	}

	rounded := int(quota.Round(0).IntPart())
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
	groupRatio := ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
	modelRatio, _, _ := ratio_setting.GetModelRatio(modelName)

	autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
	if exists {
		groupRatio = ratio_setting.GetGroupRatio(autoGroup.(string))
		log.Printf("final group ratio: %f", groupRatio)
		relayInfo.UsingGroup = autoGroup.(string)
	}

	actualGroupRatio := groupRatio
	if userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup); ok {
		actualGroupRatio = userGroupRatio
	}

	quota := calculateAudioQuota(QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  usage.InputTokenDetails.TextTokens,
			AudioTokens: usage.InputTokenDetails.AudioTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  usage.OutputTokenDetails.TextTokens,
			AudioTokens: usage.OutputTokenDetails.AudioTokens,
		},
		ModelName:       modelName,
		UsePrice:        relayInfo.UsePrice,
		ModelRatio:      modelRatio,
		CompletionRatio: ratio_setting.GetCompletionRatio(modelName),
		GroupRatio:      actualGroupRatio,
	})

	if userQuota < quota {
		return fmt.Errorf("user quota is not enough, user quota: %s, need quota: %s", logger.FormatQuotaASCII(userQuota), logger.FormatQuotaASCII(quota))
	}

	if !token.UnlimitedQuota && token.RemainQuota < quota {
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuotaASCII(token.RemainQuota), logger.FormatQuotaASCII(quota))
	}

	if err := PostConsumeQuota(relayInfo, quota, 0, false); err != nil {
		return err
	}
	logger.LogInfo(ctx, "realtime streaming consume quota success, quota: "+fmt.Sprintf("%d", quota))
	return nil
}

// claudeCalculateQuota computes the Claude-native settlement amount in decimal.
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

func applyClaudeWebSearchFee(ctx *gin.Context, calculateQuota decimal.Decimal, groupRatio float64) (int, float64, string, decimal.Decimal) {
	callCount := ctx.GetInt("claude_web_search_requests")
	if callCount <= 0 {
		return 0, 0, "", calculateQuota
	}
	price := operation_setting.GetClaudeWebSearchPricePerThousand()
	fee := decimal.NewFromFloat(price).
		Div(decimal.NewFromInt(1000)).
		Mul(decimal.NewFromFloat(groupRatio)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Mul(decimal.NewFromInt(int64(callCount)))
	logContent := fmt.Sprintf("Claude Web Search 调用 %d 次，调用花费 %s", callCount, fee.String())
	return callCount, price, logContent, calculateQuota.Add(fee)
}

func formatAudioRateLogContent(usePrice bool, modelRatio, completionRatio, audioRatio, audioCompletionRatio, modelPrice, groupRatio float64) string {
	if !usePrice {
		return fmt.Sprintf("模型倍率 %.2f，补全倍率 %.2f，音频倍率 %.2f，音频补全倍率 %.2f，分组倍率 %.2f",
			modelRatio, completionRatio, audioRatio, audioCompletionRatio, groupRatio)
	}
	return fmt.Sprintf("模型价格 %.2f，分组倍率 %.2f", modelPrice, groupRatio)
}

func logPostConsumeQuotaDelta(ctx *gin.Context, quota, preConsumedQuota, quotaDelta int) {
	if quotaDelta > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费后补扣费：%s（实际消耗：%s，预扣费：%s）",
			logger.FormatQuota(quotaDelta),
			logger.FormatQuota(quota),
			logger.FormatQuota(preConsumedQuota),
		))
	} else if quotaDelta < 0 {
		logger.LogInfo(ctx, fmt.Sprintf("预扣费后返还扣费：%s（实际消耗：%s，预扣费：%s）",
			logger.FormatQuota(-quotaDelta),
			logger.FormatQuota(quota),
			logger.FormatQuota(preConsumedQuota),
		))
	}
}

func recordEnrichedConsumeLog(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, logParams repo.RecordConsumeLogParams) {
	governance.EnrichLogParams(ctx, relayInfo, &logParams)
	repo.RecordConsumeLog(ctx, relayInfo.UserId, logParams)
}
