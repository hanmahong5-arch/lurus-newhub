package helper

import (
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
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

	// Declarative context-length pricing tier (billing-pricing-14): the
	// perception path (this function and ComputeLurusExtension, which reads
	// relayInfo.PriceData.ModelRatio right after this call in every caller)
	// runs strictly before postConsumeQuota settles the same request
	// (adaptor.DoResponse precedes it in compatible_handler.go's TextHelper),
	// so without this the X-Request-Cost header and x_lurus extension would
	// report the stale pre-consume tier while the wallet debit uses the
	// resettled one — half the real charge on a tier-crossing call (cycle-8
	// plan §8 L5 B-F2). Idempotent and a no-op for UsePrice models and for
	// any model with no tiers configured (helper.ResettleContextTier); the
	// later postConsumeQuota call re-runs this with the same inputs.
	ResettleContextTier(&relayInfo.PriceData, relayInfo.OriginModelName, usage.AsOpenAIWire().PromptTokens)

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
	dCachedCreationRatio := decimal.NewFromFloat(relayInfo.PriceData.CacheCreationRatioForWire(usage.PromptTokensIncludeCached))

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

	costLB := currency.QuotaToCNY(totalQuota)

	var billingMode string
	switch {
	case info.PlatformPreAuthID != 0:
		billingMode = "pre_auth"
	case info.IdentityAccountID != 0:
		billingMode = "trust_cache"
	default:
		billingMode = "legacy"
	}

	balanceRemaining := currency.QuotaToCNY(info.UserQuota - totalQuota)
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

// Response header names this package emits, and the two values
// CostReportingHeader can take.
//
// RelayAdapterHeader replaced X-Model-Provider (cycle-14 L6). The value was
// always constant.GetChannelTypeName(info.ChannelType) — the channel's
// configured adapter family, i.e. the wire dialect we spoke upstream. It is
// NOT a measurement of who serves the model: an operator can point an
// OpenAI-typed channel at any OpenAI-compatible endpoint, and a live
// instance was answering a deepseek-chat call with "OpenAI". The gateway has
// no way to learn the real provider — the channel's base URL is a
// configuration value, and an aggregator's endpoint would only name the
// aggregator — so the honest fix is to report what we do know under a name
// that says what it is, rather than keep a documented header that lies.
// Renaming rather than deleting keeps the genuinely useful part: the
// adapter family tells a caller which response dialect to expect.
//
// CostReportingHeader exists because the two response shapes cannot carry
// the cost the same way; see SetEventStreamHeaders.
const (
	RelayAdapterHeader             = "X-Relay-Adapter"
	CostReportingHeader            = "X-Cost-Reporting"
	CostReportingInHeaders         = "headers"
	CostReportingInFinalUsageFrame = "final-usage-frame"
)

// SetPerceptionHeaders writes the cost/adapter/request-id headers onto a
// BUFFERED (non-streamed) response, where the whole upstream reply — and so
// the settled cost — is already in hand before anything is flushed.
func SetPerceptionHeaders(c *gin.Context, info *relaycommon.RelayInfo, ext *types.LurusUsageExtension) {
	if c == nil || info == nil {
		return
	}

	if ext != nil {
		c.Writer.Header().Set("X-Request-Cost", perceptionFormatFloat(ext.CostLB))
		c.Writer.Header().Set("X-Quota-Remaining", perceptionFormatFloat(ext.BalanceRemaining))
		// Only claim the cost is in the headers when it actually is.
		c.Writer.Header().Set(CostReportingHeader, CostReportingInHeaders)
	}

	if info.ChannelMeta != nil {
		c.Writer.Header().Set(RelayAdapterHeader, constant.GetChannelTypeName(info.ChannelType))
	}

	if reqID := c.GetString(common.RequestIdKey); reqID != "" {
		c.Writer.Header().Set("X-Request-Id", reqID)
	}
}

func perceptionFormatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
