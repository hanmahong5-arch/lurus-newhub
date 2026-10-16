package app

import (
	"math"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// RecordUpstreamCost stores on relayInfo what the upstream itself billed for
// the request, when it reports it. Today only OpenRouter does (its
// proprietary usage.cost, USD); every other channel leaves the cost unknown,
// and the platform usage mirror then omits upstream_cost_cny instead of
// claiming the call cost nothing.
func RecordUpstreamCost(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) {
	if relayInfo == nil || usage == nil || relayInfo.ChannelMeta == nil {
		return
	}
	if relayInfo.ChannelType != constant.ChannelTypeOpenRouter {
		return
	}
	cost, ok := usage.Cost.(float64)
	if !ok || cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return
	}
	relayInfo.UpstreamCostUSD = cost
	relayInfo.UpstreamCostKnown = true
}

// upstreamCostCNY converts an upstream USD cost at the same exchange rate the
// wallet debit uses (currency.LucToLut), rounded to the wallet's 4 decimals'
// finer neighbour (6) so sub-fen costs are not lost before aggregation.
func upstreamCostCNY(usd float64) float64 {
	rate := operation_setting.USDExchangeRate
	if rate <= 0 {
		rate = operation_setting.DefaultUSDExchangeRate
	}
	return math.Round(usd*rate*1e6) / 1e6
}
