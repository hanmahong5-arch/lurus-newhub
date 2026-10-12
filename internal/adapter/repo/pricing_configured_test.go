package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// TestApplyCatalogPrice_PriceConfigured pins the catalogue's answer to "is
// this model priced": a model nobody priced must not carry the 37.5 fallback
// ratio GetModelRatio hands the relay's error path.
func TestApplyCatalogPrice_PriceConfigured(t *testing.T) {
	prevRatio := ratio_setting.ModelRatio2JSONString()
	prevPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(prevRatio)
		_ = ratio_setting.UpdateModelPriceByJSONString(prevPrice)
	})
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"model-ratio":1.5}`); err != nil {
		t.Fatal(err)
	}
	if err := ratio_setting.UpdateModelPriceByJSONString(`{"model-fixed":0.04,"model-zero":0}`); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name           string
		model          string
		wantConfigured bool
		wantRatio      float64
		wantPrice      float64
		wantQuotaType  int
	}{
		{"unpriced model has no ratio", "model-unpriced", false, 0, 0, 0},
		{"ratio priced", "model-ratio", true, 1.5, 0, 0},
		{"fixed price", "model-fixed", true, 0, 0.04, 1},
		{"explicit zero price is free, not unpriced", "model-zero", true, 0, 0, 1},
		{"openrouter free variant is free", "model-a:free", true, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p Pricing
			applyCatalogPrice(&p, tc.model)
			if p.PriceConfigured != tc.wantConfigured {
				t.Errorf("PriceConfigured = %v, want %v", p.PriceConfigured, tc.wantConfigured)
			}
			if p.ModelRatio != tc.wantRatio {
				t.Errorf("ModelRatio = %v, want %v", p.ModelRatio, tc.wantRatio)
			}
			if p.ModelPrice != tc.wantPrice {
				t.Errorf("ModelPrice = %v, want %v", p.ModelPrice, tc.wantPrice)
			}
			if p.QuotaType != tc.wantQuotaType {
				t.Errorf("QuotaType = %d, want %d", p.QuotaType, tc.wantQuotaType)
			}
		})
	}
}
