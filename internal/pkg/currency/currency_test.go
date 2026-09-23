package currency

import (
	"math"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// withUSDRate pins USDExchangeRate for one test. At 5.0, CNY 1 buys
// QuotaPerUnit/5 = 100,000 quota, which keeps the arithmetic below exact.
func withUSDRate(t *testing.T, rate float64) {
	t.Helper()
	prev := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = rate
	t.Cleanup(func() { operation_setting.USDExchangeRate = prev })
}

// Quota is priced in USD (QuotaPerUnit quota = $1); the wallet is in CNY.
// One yuan therefore buys QuotaPerUnit / USDExchangeRate quota — not
// QuotaPerUnit, which was the old bug (CNY 1 treated as $1).
func TestLucToLut(t *testing.T) {
	withUSDRate(t, 5)
	if got := LucToLut(); got != 100_000 {
		t.Errorf("LucToLut() at 5 CNY/USD = %f, want 100000", got)
	}
	withUSDRate(t, operation_setting.DefaultUSDExchangeRate)
	if got, want := LucToLut(), common.QuotaPerUnit/7.3; got != want {
		t.Errorf("LucToLut() at the default rate = %f, want QuotaPerUnit/7.3 = %f", got, want)
	}
}

// A rate <= 0 cannot be written through the option API, but if one is ever
// set directly the fallback must be the default rate, never 1 (the old bug).
func TestLucToLut_NonPositiveRateFallsBackToDefault(t *testing.T) {
	for _, r := range []float64{0, -1} {
		withUSDRate(t, r)
		if got, want := LucToLut(), common.QuotaPerUnit/operation_setting.DefaultUSDExchangeRate; got != want {
			t.Errorf("rate %v: LucToLut() = %f, want %f", r, got, want)
		}
	}
}

// The economics this exists for: deepseek-chat's ratio 0.135 is DeepSeek's
// $0.27 / 1M input tokens. 1M input tokens = 135,000 quota, which must cost
// 0.27 USD = 1.971 CNY at 7.3 — not CNY 0.27.
func TestQuotaToCNY_ChargesTheRealPrice(t *testing.T) {
	withUSDRate(t, 7.3)
	quota := int(1_000_000 / 1000 * 0.135 * 1000) // 1M tokens at ratio 0.135
	if got := QuotaToCNY(quota); math.Abs(got-1.971) > 1e-9 {
		t.Errorf("QuotaToCNY(%d) = %f CNY, want 1.971 (= $0.27 at 7.3)", quota, got)
	}
}

func TestCNYToQuota(t *testing.T) {
	withUSDRate(t, 5)
	cases := map[float64]int{1: 100_000, 12.5: 1_250_000, 0.000001: 0, 0: 0, -3: 0}
	for cny, want := range cases {
		if got := CNYToQuota(cny); got != want {
			t.Errorf("CNYToQuota(%v) = %d, want %d", cny, got, want)
		}
	}
	// Round trip: what a yuan buys converts back to that yuan.
	if got := QuotaToCNY(CNYToQuota(3)); got != 3 {
		t.Errorf("QuotaToCNY(CNYToQuota(3)) = %f, want 3", got)
	}
}

func TestLugToLut(t *testing.T) {
	withUSDRate(t, 5)
	expected := float64(LugToLuc) * LucToLut()
	if LugToLut() != expected {
		t.Errorf("LugToLut()=%f, expected %f", LugToLut(), expected)
	}
	// 1 LUG = 100 LUC * 100,000 = 10,000,000
	if LugToLut() != 10_000_000 {
		t.Errorf("expected 10000000, got %f", LugToLut())
	}
}

func TestLucToLutAmount(t *testing.T) {
	withUSDRate(t, 5)
	tests := []struct {
		name       string
		luc        float64
		multiplier float64
		expected   int
	}{
		{"1 LUC no bonus", 1.0, 1.0, 100_000},
		{"10 LUC no bonus", 10.0, 1.0, 1_000_000},
		{"1 LUC silver bonus", 1.0, 1.05, 105_000},
		{"1 LUC diamond bonus", 1.0, 1.20, 120_000},
		{"0 LUC", 0, 1.0, 0},
		{"negative LUC", -5, 1.0, 0},
		{"zero multiplier defaults to 1.0", 1.0, 0, 100_000},
		{"fractional LUC", 0.5, 1.0, 50_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := LucToLutAmount(tt.luc, tt.multiplier)
			if result != tt.expected {
				t.Errorf("LucToLutAmount(%f, %f) = %d, want %d", tt.luc, tt.multiplier, result, tt.expected)
			}
		})
	}
}

func TestLugToLucAmount(t *testing.T) {
	if LugToLucAmount(1) != 100 {
		t.Errorf("1 LUG should = 100 LUC, got %f", LugToLucAmount(1))
	}
	if LugToLucAmount(0.5) != 50 {
		t.Errorf("0.5 LUG should = 50 LUC, got %f", LugToLucAmount(0.5))
	}
}

func TestLutToLucDisplay(t *testing.T) {
	withUSDRate(t, 5)
	if result := LutToLucDisplay(100_000); result != 1.0 {
		t.Errorf("100,000 LUT should display as 1.0 LUC at 5 CNY/USD, got %f", result)
	}
	if result := LutToLucDisplay(0); result != 0 {
		t.Errorf("0 LUT should display as 0 LUC, got %f", result)
	}
	if result := LutToLucDisplay(1_000_000); result != 10.0 {
		t.Errorf("1,000,000 LUT should display as 10.0 LUC, got %f", result)
	}
}

func TestLutToLugDisplay(t *testing.T) {
	withUSDRate(t, 5)
	if result := LutToLugDisplay(10_000_000); result != 1.0 {
		t.Errorf("10,000,000 LUT should display as 1.0 LUG, got %f", result)
	}
}

func TestFormatLut(t *testing.T) {
	tests := []struct {
		amount   int
		expected string
	}{
		{500, "500 LUT"},
		{9999, "9999 LUT"},
		{10_000, "1.0 wan LUT"},
		{500_000, "50.0 wan LUT"},
		{5_000_000, "500.0 wan LUT"},
		{100_000_000, "1.00 yi LUT"},
		{250_000_000, "2.50 yi LUT"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := FormatLut(tt.amount)
			if result != tt.expected {
				t.Errorf("FormatLut(%d) = %q, want %q", tt.amount, result, tt.expected)
			}
		})
	}
}

func TestFormatLutCN(t *testing.T) {
	tests := []struct {
		amount   int
		expected string
	}{
		{1250, "1250路特"},
		{500_000, "50.0万路特"},
		{50_000_000, "5000.0万路特"},
		{100_000_000, "1.00亿路特"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := FormatLutCN(tt.amount)
			if result != tt.expected {
				t.Errorf("FormatLutCN(%d) = %q, want %q", tt.amount, result, tt.expected)
			}
		})
	}
}

func TestModelPriceLut(t *testing.T) {
	// GPT-4o: ratio 1.25, completion 3, group 1
	input, output := ModelPriceLut(1.25, 3.0, 1.0)
	if input != 1250 {
		t.Errorf("GPT-4o input = %f, want 1250", input)
	}
	if output != 3750 {
		t.Errorf("GPT-4o output = %f, want 3750", output)
	}

	// GPT-4o-mini: ratio 0.075, completion 3, group 1
	input, output = ModelPriceLut(0.075, 3.0, 1.0)
	if input != 75 {
		t.Errorf("mini input = %f, want 75", input)
	}
	if output != 225 {
		t.Errorf("mini output = %f, want 225", output)
	}

	// With group discount
	input, _ = ModelPriceLut(1.25, 3.0, 0.8)
	if input != 1000 {
		t.Errorf("GPT-4o input with 0.8 group = %f, want 1000", input)
	}
}

func TestVIPBonusRate(t *testing.T) {
	tests := []struct {
		level    int
		expected float64
	}{
		{0, 1.00},
		{1, 1.05},
		{2, 1.10},
		{3, 1.15},
		{4, 1.20},
		{5, 1.20},  // capped at diamond
		{-1, 1.00}, // below 0 = standard
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := VIPBonusRate(tt.level)
			if math.Abs(result-tt.expected) > 0.001 {
				t.Errorf("VIPBonusRate(%d) = %f, want %f", tt.level, result, tt.expected)
			}
		})
	}
}

func TestCalculateExchange(t *testing.T) {
	withUSDRate(t, 5)
	info := CalculateExchange(10.0, 0)
	if info.SourceCurrency != CodeLuCoin {
		t.Error("source should be LUC")
	}
	if info.TargetCurrency != CodeLute {
		t.Error("target should be LUT")
	}
	if info.TargetAmount != 1_000_000 {
		t.Errorf("10 LUC standard = %d LUT, want 1000000", info.TargetAmount)
	}

	// With VIP diamond bonus (1.2x)
	info = CalculateExchange(10.0, 4)
	if info.TargetAmount != 1_200_000 {
		t.Errorf("10 LUC diamond = %d LUT, want 1200000", info.TargetAmount)
	}
	if math.Abs(info.VIPBonus-1.20) > 0.001 {
		t.Errorf("VIPBonus = %f, want 1.20", info.VIPBonus)
	}

	// Zero amount
	info = CalculateExchange(0, 0)
	if info.TargetAmount != 0 {
		t.Errorf("0 LUC = %d LUT, want 0", info.TargetAmount)
	}
}

// TestLutToLucDisplayZeroRate exercises the defensive rate==0 branch by
// temporarily zeroing the live QuotaPerUnit var (restored via t.Cleanup).
// A zero rate must never produce a divide-by-zero Inf/NaN; it returns 0 so a
// misconfigured base unit never mis-states a displayed LUC balance.
func TestLutToLucDisplayZeroRate(t *testing.T) {
	orig := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = orig })

	common.QuotaPerUnit = 0
	if got := LutToLucDisplay(5_000_000); got != 0 {
		t.Errorf("LutToLucDisplay with zero rate = %v, want 0", got)
	}
	if got := LutToLugDisplay(5_000_000); got != 0 {
		t.Errorf("LutToLugDisplay with zero rate = %v, want 0", got)
	}
}

// TestFormatLutNegative covers the abs = -abs sign-normalization branch across
// all three magnitude bands. Negative amounts (e.g. a refund/adjustment) must
// pick the same wan/yi band as their positive counterpart and keep the sign.
func TestFormatLutNegative(t *testing.T) {
	tests := []struct {
		amount int
		wantEN string
		wantCN string
	}{
		{-500, "-500 LUT", "-500路特"},
		{-9999, "-9999 LUT", "-9999路特"},
		{-10_000, "-1.0 wan LUT", "-1.0万路特"},
		{-500_000, "-50.0 wan LUT", "-50.0万路特"},
		{-100_000_000, "-1.00 yi LUT", "-1.00亿路特"},
		{-250_000_000, "-2.50 yi LUT", "-2.50亿路特"},
	}
	for _, tt := range tests {
		if got := FormatLut(tt.amount); got != tt.wantEN {
			t.Errorf("FormatLut(%d) = %q, want %q", tt.amount, got, tt.wantEN)
		}
		if got := FormatLutCN(tt.amount); got != tt.wantCN {
			t.Errorf("FormatLutCN(%d) = %q, want %q", tt.amount, got, tt.wantCN)
		}
	}
}

// TestModelPriceLutDefaults covers the two guard branches that clamp a
// non-positive completionRatio or groupRatio up to 1.0, so an unset/garbage
// ratio never zeroes out a model's billed price.
func TestModelPriceLutDefaults(t *testing.T) {
	// groupRatio <= 0 defaults to 1.0.
	in, out := ModelPriceLut(1.25, 3.0, 0)
	if in != 1250 || out != 3750 {
		t.Errorf("zero groupRatio: in=%v out=%v, want 1250/3750", in, out)
	}
	in, out = ModelPriceLut(1.25, 3.0, -2)
	if in != 1250 || out != 3750 {
		t.Errorf("negative groupRatio: in=%v out=%v, want 1250/3750", in, out)
	}

	// completionRatio <= 0 defaults to 1.0 → output equals input price.
	in, out = ModelPriceLut(1.25, 0, 1.0)
	if in != 1250 || out != 1250 {
		t.Errorf("zero completionRatio: in=%v out=%v, want 1250/1250", in, out)
	}
	in, out = ModelPriceLut(1.25, -1, 1.0)
	if in != 1250 || out != 1250 {
		t.Errorf("negative completionRatio: in=%v out=%v, want 1250/1250", in, out)
	}

	// Both guards fire at once.
	in, out = ModelPriceLut(2.0, 0, 0)
	if in != 2000 || out != 2000 {
		t.Errorf("both defaults: in=%v out=%v, want 2000/2000", in, out)
	}
}

func TestOneWayConversion(t *testing.T) {
	// Verify roundtrip is NOT lossless (by design — one-way conversion)
	luc := 10.0
	lut := LucToLutAmount(luc, 1.0)
	backToLuc := LutToLucDisplay(lut)

	// Should be equal since we used floor and the rate is exact
	if backToLuc != luc {
		t.Logf("Note: %f LUC -> %d LUT -> %f LUC display (this is display only, NOT real conversion)", luc, lut, backToLuc)
	}
}
