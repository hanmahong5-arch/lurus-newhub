package helper

import (
	"fmt"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/shopspring/decimal"

	"github.com/gin-gonic/gin"
)

// EstimateQuotaFromUsage computes an estimated quota for perception display.
// Mirrors the core token-based billing in postConsumeQuota (prompt + completion + cache
// adjustments). Tool-specific pricing (web search, file search, image generation) is
// excluded — the exact billing is handled by postConsumeQuota.
func EstimateQuotaFromUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) int {
	if relayInfo == nil || usage == nil {
		return 0
	}

	if relayInfo.PriceData.UsePrice {
		q := decimal.NewFromFloat(relayInfo.PriceData.ModelPrice).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
			Mul(decimal.NewFromFloat(relayInfo.PriceData.GroupRatioInfo.GroupRatio))
		result := int(q.Round(0).IntPart())
		if result <= 0 {
			result = 1
		}
		return result
	}

	dPromptTokens := decimal.NewFromInt(int64(usage.PromptTokens))
	dCompletionTokens := decimal.NewFromInt(int64(usage.CompletionTokens))
	dCacheTokens := decimal.NewFromInt(int64(usage.PromptTokensDetails.CachedTokens))
	dImageTokens := decimal.NewFromInt(int64(usage.PromptTokensDetails.ImageTokens))
	dCachedCreationTokens := decimal.NewFromInt(int64(usage.PromptTokensDetails.CachedCreationTokens))

	dModelRatio := decimal.NewFromFloat(relayInfo.PriceData.ModelRatio)
	dGroupRatio := decimal.NewFromFloat(relayInfo.PriceData.GroupRatioInfo.GroupRatio)
	dCompletionRatio := decimal.NewFromFloat(relayInfo.PriceData.CompletionRatio)
	dCacheRatio := decimal.NewFromFloat(relayInfo.PriceData.CacheRatio)
	dImageRatio := decimal.NewFromFloat(relayInfo.PriceData.ImageRatio)
	dCachedCreationRatio := decimal.NewFromFloat(relayInfo.PriceData.CacheCreationRatio)

	ratio := dModelRatio.Mul(dGroupRatio)

	// Prompt-base deduction keyed on the wire flag stamped at the usage parse
	// site (dto.Usage.PromptTokensIncludeCached), matching the real billing in
	// compatible_handler.go/postConsumeQuota — this estimate used to guess
	// from ChannelType and disagreed with the actual charge on exactly the
	// combinations where that guess is wrong (Claude wire on non-Anthropic
	// channels, OpenAI wire billed via the Claude path).
	baseTokens := dPromptTokens
	var cachedTokensWithRatio decimal.Decimal
	if !dCacheTokens.IsZero() {
		if usage.PromptTokensIncludeCached {
			baseTokens = baseTokens.Sub(dCacheTokens)
		}
		cachedTokensWithRatio = dCacheTokens.Mul(dCacheRatio)
	}

	var cachedCreationWithRatio decimal.Decimal
	if !dCachedCreationTokens.IsZero() {
		if usage.PromptTokensIncludeCached {
			baseTokens = baseTokens.Sub(dCachedCreationTokens)
		}
		cachedCreationWithRatio = dCachedCreationTokens.Mul(dCachedCreationRatio)
	}

	var imageTokensWithRatio decimal.Decimal
	if !dImageTokens.IsZero() {
		baseTokens = baseTokens.Sub(dImageTokens)
		imageTokensWithRatio = dImageTokens.Mul(dImageRatio)
	}

	promptQuota := baseTokens.Add(cachedTokensWithRatio).
		Add(imageTokensWithRatio).
		Add(cachedCreationWithRatio)
	completionQuota := dCompletionTokens.Mul(dCompletionRatio)

	quotaDecimal := promptQuota.Add(completionQuota).Mul(ratio)
	if !ratio.IsZero() && quotaDecimal.LessThanOrEqual(decimal.Zero) {
		quotaDecimal = decimal.NewFromInt(1)
	}

	return int(quotaDecimal.Round(0).IntPart())
}

// ComputeLurusExtension builds a LurusUsageExtension from relay context.
func ComputeLurusExtension(info *relaycommon.RelayInfo, usage *dto.Usage, totalQuota int) *types.LurusUsageExtension {
	if info == nil {
		return nil
	}

	costLB := float64(totalQuota) / common.QuotaPerUnit

	var billingMode string
	switch {
	case info.PlatformPreAuthID != 0:
		billingMode = "pre_auth"
	case info.IdentityAccountID != 0:
		billingMode = "trust_cache"
	default:
		billingMode = "legacy"
	}

	balanceRemaining := float64(info.UserQuota-totalQuota) / common.QuotaPerUnit
	if balanceRemaining < 0 {
		balanceRemaining = 0
	}

	var cachedTokens int
	if usage != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
	}

	return &types.LurusUsageExtension{
		CostLB:           costLB,
		ModelRatio:       info.PriceData.ModelRatio,
		GroupRatio:       info.PriceData.GroupRatioInfo.GroupRatio,
		CachedTokens:     cachedTokens,
		BalanceRemaining: balanceRemaining,
		BillingMode:      billingMode,
	}
}

// SetPerceptionHeaders writes cost/provider/request-id headers onto the response.
func SetPerceptionHeaders(c *gin.Context, info *relaycommon.RelayInfo, ext *types.LurusUsageExtension) {
	if c == nil || info == nil {
		return
	}

	if ext != nil {
		c.Writer.Header().Set("X-Request-Cost", perceptionFormatFloat(ext.CostLB))
		c.Writer.Header().Set("X-Quota-Remaining", perceptionFormatFloat(ext.BalanceRemaining))
	}

	if info.ChannelMeta != nil {
		c.Writer.Header().Set("X-Model-Provider", constant.GetChannelTypeName(info.ChannelType))
	}

	if reqID := c.GetString(common.RequestIdKey); reqID != "" {
		c.Writer.Header().Set("X-Request-Id", reqID)
	}
}

func perceptionFormatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// SetRateLimitHeaders writes standard rate-limit headers on 429 responses.
func SetRateLimitHeaders(c *gin.Context, limit int, remaining int, retryAfterSec int64) {
	if c == nil {
		return
	}
	c.Writer.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSec))
	c.Writer.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	c.Writer.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
}
