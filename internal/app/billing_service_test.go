package app

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// ---------------------------------------------------------------------------
// CalculateDisplayAmount
// ---------------------------------------------------------------------------

func setQuotaDisplayType(t *testing.T, displayType string) {
	t.Helper()
	gs := operation_setting.GetGeneralSetting()
	orig := gs.QuotaDisplayType
	gs.QuotaDisplayType = displayType
	t.Cleanup(func() {
		gs.QuotaDisplayType = orig
	})
}

func setUSDExchangeRate(t *testing.T, rate float64) {
	t.Helper()
	orig := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = rate
	t.Cleanup(func() {
		operation_setting.USDExchangeRate = orig
	})
}

func TestCalculateDisplayAmount_Lute_ReturnsLucEquivalent(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeLute)

	setUSDExchangeRate(t, 5)
	quota := 1_000_000
	// LUT → LUC (= CNY) display: quota is USD-priced, so 1,000,000 quota is
	// $2, which is CNY 10 at 5 CNY/USD (currency.LucToLut).
	want := 10.0
	got := CalculateDisplayAmount(quota)
	if got != want {
		t.Errorf("CalculateDisplayAmount(%d) with LUTE = %f, want %f", quota, got, want)
	}
}

func TestCalculateDisplayAmount_USD_ReturnsQuotaDividedByUnit(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)

	quota := int(common.QuotaPerUnit) // exactly 1 USD
	got := CalculateDisplayAmount(quota)
	want := 1.0

	if got != want {
		t.Errorf("CalculateDisplayAmount(%d) with USD = %f, want %f", quota, got, want)
	}
}

func TestCalculateDisplayAmount_USD_ZeroQuota(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)

	got := CalculateDisplayAmount(0)
	if got != 0 {
		t.Errorf("CalculateDisplayAmount(0) with USD = %f, want 0", got)
	}
}

func TestCalculateDisplayAmount_USD_LargeQuota(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)

	quota := int(common.QuotaPerUnit) * 100 // 100 USD
	got := CalculateDisplayAmount(quota)
	want := 100.0

	if got != want {
		t.Errorf("CalculateDisplayAmount(%d) with USD = %f, want %f", quota, got, want)
	}
}

func TestCalculateDisplayAmount_CNY_ReturnsQuotaMultipliedByExchangeRate(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeCNY)
	setUSDExchangeRate(t, 7.3)

	quota := int(common.QuotaPerUnit) // 1 USD worth of quota
	got := CalculateDisplayAmount(quota)
	want := 7.3 // 1 * 7.3

	if got != want {
		t.Errorf("CalculateDisplayAmount(%d) with CNY = %f, want %f", quota, got, want)
	}
}

func TestCalculateDisplayAmount_CNY_ZeroQuota(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeCNY)
	setUSDExchangeRate(t, 7.3)

	got := CalculateDisplayAmount(0)
	if got != 0 {
		t.Errorf("CalculateDisplayAmount(0) with CNY = %f, want 0", got)
	}
}

func TestCalculateDisplayAmount_CNY_CustomExchangeRate(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeCNY)
	setUSDExchangeRate(t, 8.0)

	quota := int(common.QuotaPerUnit) * 10 // 10 USD worth
	got := CalculateDisplayAmount(quota)
	want := 80.0 // 10 * 8.0

	if got != want {
		t.Errorf("CalculateDisplayAmount(%d) with CNY rate=8.0 = %f, want %f", quota, got, want)
	}
}

func TestCalculateDisplayAmount_Tokens_ReturnsRawQuotaValue(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeTokens)

	quota := 12345
	got := CalculateDisplayAmount(quota)
	want := float64(quota)

	if got != want {
		t.Errorf("CalculateDisplayAmount(%d) with TOKENS = %f, want %f", quota, got, want)
	}
}

func TestCalculateDisplayAmount_Tokens_ZeroQuota(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeTokens)

	got := CalculateDisplayAmount(0)
	if got != 0 {
		t.Errorf("CalculateDisplayAmount(0) with TOKENS = %f, want 0", got)
	}
}

func TestCalculateDisplayAmount_Tokens_NegativeQuota(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeTokens)

	got := CalculateDisplayAmount(-500)
	want := -500.0

	if got != want {
		t.Errorf("CalculateDisplayAmount(-500) with TOKENS = %f, want %f", got, want)
	}
}

func TestCalculateDisplayAmount_DefaultFallsToUSD(t *testing.T) {
	// Unknown display type should use the default (USD) branch
	setQuotaDisplayType(t, "UNKNOWN_TYPE")

	quota := int(common.QuotaPerUnit) * 5 // 5 USD
	got := CalculateDisplayAmount(quota)
	want := 5.0

	if got != want {
		t.Errorf("CalculateDisplayAmount(%d) with unknown type = %f, want %f (USD fallback)", quota, got, want)
	}
}

// ---------------------------------------------------------------------------
// Table-driven: all display types in one test
// ---------------------------------------------------------------------------

func TestCalculateDisplayAmount_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		displayType  string
		exchangeRate float64
		quota        int
		want         float64
	}{
		{
			name:        "USD small quota",
			displayType: operation_setting.QuotaDisplayTypeUSD,
			quota:       250000, // 0.5 USD
			want:        0.5,
		},
		{
			name:         "CNY with rate 7.0",
			displayType:  operation_setting.QuotaDisplayTypeCNY,
			exchangeRate: 7.0,
			quota:        int(common.QuotaPerUnit) * 2, // 2 USD
			want:         14.0,                         // 2 * 7.0
		},
		{
			name:        "TOKENS preserves raw value",
			displayType: operation_setting.QuotaDisplayTypeTokens,
			quota:       999999,
			want:        999999.0,
		},
		{
			name:        "negative quota USD",
			displayType: operation_setting.QuotaDisplayTypeUSD,
			quota:       -int(common.QuotaPerUnit),
			want:        -1.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setQuotaDisplayType(t, tc.displayType)
			if tc.exchangeRate > 0 {
				setUSDExchangeRate(t, tc.exchangeRate)
			}

			got := CalculateDisplayAmount(tc.quota)
			if got != tc.want {
				t.Errorf("CalculateDisplayAmount(%d) [type=%s] = %f, want %f",
					tc.quota, tc.displayType, got, tc.want)
			}
		})
	}
}
