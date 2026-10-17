package app

import (
	"math"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
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
	relayInfo.UpstreamCostUSD = &cost
}

// addMirrorCostMeta adds the cost side of the platform's per-product /
// per-model margin report to a usage-mirror event: which channel served the
// call, and what the upstream charged, only when the upstream reported it
// (an unknown cost is omitted, never 0).
func addMirrorCostMeta(meta map[string]interface{}, relayInfo *relaycommon.RelayInfo) {
	if relayInfo.ChannelMeta != nil && relayInfo.ChannelId > 0 {
		meta["channel_id"] = relayInfo.ChannelId
	}
	if relayInfo.UpstreamCostUSD != nil {
		meta["upstream_cost_cny"] = upstreamCostCNY(*relayInfo.UpstreamCostUSD)
	}
}

// upstreamCostCNY converts an upstream USD cost at the same exchange rate the
// wallet debit uses, rounded to 6 decimals so sub-fen costs are not lost
// before aggregation.
func upstreamCostCNY(usd float64) float64 {
	return math.Round(currency.USDToCNY(usd)*1e6) / 1e6
}
