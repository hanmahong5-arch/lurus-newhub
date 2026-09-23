package currency

import (
	"fmt"
	"math"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// Currency codes for the three-tier monetary system.
//
//	Tier 1: LuGold (LUG) — subscription/package unit, 1 LUG = 100 LUC
//	Tier 2: LuCoin (LUC) — platform-wide credit, 1 LUC = CNY 1 (wallet unit)
//	Tier 3: Lute   (LUT) — API product usage credit, 1 LUT = 1 internal quota unit
//
// Conversion is ONE-WAY only: LUG -> LUC -> LUT.
// Reverse conversion is NOT allowed.
const (
	CodeLuGold = "LUG" // Tier 1: subscription unit
	CodeLuCoin = "LUC" // Tier 2: platform credit
	CodeLute   = "LUT" // Tier 3: API usage credit
)

// Base exchange rates.
// These define the canonical conversion ratios between currency tiers.
const (
	// 1 LUG = 100 LUC (a LuGold is like a 100-yuan bill)
	LugToLuc = 100

	// 1 LUC (= CNY 1 in the platform wallet) buys LucToLut() LUT; see there.
	// 1 LUG = LugToLuc * LucToLut() LUT (derived, not hardcoded)
)

// LucToLut returns how much quota 1 LUC — one yuan of platform wallet
// balance — buys.
//
// Quota is priced in US dollars: model ratios are the upstream USD presets
// (ratio 1 = $0.002 / 1K tokens, deepseek-chat's 0.135 = $0.27 / 1M), so
// QuotaPerUnit quota is worth $1, not CNY 1. The wallet is in yuan (a CNY 1
// topup credits 1.0), so one yuan buys QuotaPerUnit / USDExchangeRate quota.
//
// This used to return QuotaPerUnit itself, i.e. it treated CNY 1 as $1:
// every wallet debit, pre-authorisation, wallet-to-quota transfer and
// invoice amount_cny was 1/USDExchangeRate of the real cost (at 7.3, a
// customer paid CNY 0.27 for what the upstream charged CNY 1.97).
//
// Every yuan<->quota conversion must go through here (QuotaToCNY /
// CNYToQuota); TestNoRawQuotaPerUnitAtCNYBoundaries pins that.
func LucToLut() float64 {
	rate := operation_setting.USDExchangeRate
	if rate <= 0 {
		// Unreachable through the option API (positiveRangeOptionKinds
		// rejects <= 0); never fall back to 1, which is the old bug.
		rate = operation_setting.DefaultUSDExchangeRate
	}
	return common.QuotaPerUnit / rate
}

// QuotaToCNY converts a quota amount to yuan (LUC) — the amount to debit
// from, pre-authorise on, or report against the platform wallet.
func QuotaToCNY(quota int) float64 {
	return float64(quota) / LucToLut()
}

// CNYToQuota converts yuan (LUC) to quota, truncated — the quota a CNY
// amount taken from the wallet buys.
func CNYToQuota(cny float64) int {
	if cny <= 0 {
		return 0
	}
	return int(math.Floor(cny * LucToLut()))
}

// LugToLut returns the LUG -> LUT exchange rate (via LUC).
func LugToLut() float64 {
	return float64(LugToLuc) * LucToLut()
}

// --- Conversion functions (one-way: down only) ---

// LucToLutAmount converts LUC amount to LUT (integer, truncated).
// discountMultiplier applies VIP bonus (1.0 = no bonus, 1.1 = +10%).
func LucToLutAmount(lucAmount float64, discountMultiplier float64) int {
	if lucAmount <= 0 {
		return 0
	}
	if discountMultiplier <= 0 {
		discountMultiplier = 1.0
	}
	raw := lucAmount * LucToLut() * discountMultiplier
	return int(math.Floor(raw))
}

// LugToLucAmount converts LUG to LUC.
func LugToLucAmount(lugAmount float64) float64 {
	return lugAmount * float64(LugToLuc)
}

// --- Reverse display (read-only, for UI, NOT for actual conversion) ---

// LutToLucDisplay converts LUT amount to LUC equivalent for display purposes.
func LutToLucDisplay(lutAmount int) float64 {
	rate := LucToLut()
	if rate == 0 {
		return 0
	}
	return float64(lutAmount) / rate
}

// LutToLugDisplay converts LUT amount to LUG equivalent for display purposes.
func LutToLugDisplay(lutAmount int) float64 {
	rate := LugToLut()
	if rate == 0 {
		return 0
	}
	return float64(lutAmount) / rate
}

// --- Formatting ---

// FormatLut formats a LUT amount for display.
// Uses Chinese wan (10K) grouping for large numbers.
//
//	< 10,000:       "1,250 LUT"
//	10,000+:        "50 wan LUT"  (displayed as "50万路特" in UI)
//	100,000,000+:   "1.5 yi LUT" (displayed as "1.5亿路特" in UI)
func FormatLut(amount int) string {
	abs := amount
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 100_000_000:
		yi := float64(amount) / 100_000_000
		return fmt.Sprintf("%.2f yi LUT", yi)
	case abs >= 10_000:
		wan := float64(amount) / 10_000
		return fmt.Sprintf("%.1f wan LUT", wan)
	default:
		return fmt.Sprintf("%d LUT", amount)
	}
}

// FormatLutCN formats a LUT amount in Chinese for end-user display.
func FormatLutCN(amount int) string {
	abs := amount
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 100_000_000:
		yi := float64(amount) / 100_000_000
		return fmt.Sprintf("%.2f亿路特", yi)
	case abs >= 10_000:
		wan := float64(amount) / 10_000
		return fmt.Sprintf("%.1f万路特", wan)
	default:
		return fmt.Sprintf("%d路特", amount)
	}
}

// --- Model pricing helper ---

// ModelPriceLut returns the LUT cost per 1K tokens for a model.
//
//	inputLutPer1K  = 1000 * modelRatio * groupRatio
//	outputLutPer1K = 1000 * modelRatio * completionRatio * groupRatio
func ModelPriceLut(modelRatio, completionRatio, groupRatio float64) (inputPer1K, outputPer1K float64) {
	if groupRatio <= 0 {
		groupRatio = 1.0
	}
	if completionRatio <= 0 {
		completionRatio = 1.0
	}
	inputPer1K = 1000 * modelRatio * groupRatio
	outputPer1K = 1000 * modelRatio * completionRatio * groupRatio
	return
}

// --- VIP exchange rate bonuses ---

// VIPBonusRate returns the exchange rate multiplier for a VIP level.
// Level 0 = standard (1.0x), up to level 4 = diamond (1.2x).
func VIPBonusRate(vipLevel int) float64 {
	switch {
	case vipLevel >= 4:
		return 1.20 // Diamond
	case vipLevel >= 3:
		return 1.15 // Platinum
	case vipLevel >= 2:
		return 1.10 // Gold
	case vipLevel >= 1:
		return 1.05 // Silver
	default:
		return 1.00 // Standard
	}
}

// ExchangeInfo holds the details of a LUC -> LUT exchange for audit/display.
type ExchangeInfo struct {
	SourceCurrency string  `json:"source_currency"` // "LUC"
	SourceAmount   float64 `json:"source_amount"`
	TargetCurrency string  `json:"target_currency"` // "LUT"
	TargetAmount   int     `json:"target_amount"`
	ExchangeRate   float64 `json:"exchange_rate"` // effective rate (base * VIP bonus)
	VIPLevel       int     `json:"vip_level"`
	VIPBonus       float64 `json:"vip_bonus"` // multiplier applied
}

// CalculateExchange computes the exchange result for a LUC -> LUT conversion.
func CalculateExchange(lucAmount float64, vipLevel int) *ExchangeInfo {
	bonus := VIPBonusRate(vipLevel)
	lutAmount := LucToLutAmount(lucAmount, bonus)
	effectiveRate := LucToLut() * bonus

	return &ExchangeInfo{
		SourceCurrency: CodeLuCoin,
		SourceAmount:   lucAmount,
		TargetCurrency: CodeLute,
		TargetAmount:   lutAmount,
		ExchangeRate:   effectiveRate,
		VIPLevel:       vipLevel,
		VIPBonus:       bonus,
	}
}
