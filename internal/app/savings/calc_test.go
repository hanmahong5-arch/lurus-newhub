package savings

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

func TestRepriceModel(t *testing.T) {
	cases := []struct {
		name                                                 string
		spend                                                ModelSpend
		modelRatio, completionRatio, altRatio, altCompletion float64
		want                                                 int64
	}{
		{
			// Equal completion ratios → effAlt/effOrig = altRatio/modelRatio = 0.5.
			// savings = 1000 * (1 - 0.5) = 500.
			name:            "normal saving half-price",
			spend:           ModelSpend{TotalQuota: 1000, PromptTokens: 100, CompletionTokens: 200},
			modelRatio:      1.0,
			completionRatio: 2.0,
			altRatio:        0.5,
			altCompletion:   2.0,
			want:            500,
		},
		{
			// Alt is MORE expensive → floored at 0, never negative.
			name:            "zero floor when alt dearer",
			spend:           ModelSpend{TotalQuota: 1000, PromptTokens: 100, CompletionTokens: 200},
			modelRatio:      0.5,
			completionRatio: 2.0,
			altRatio:        1.0,
			altCompletion:   2.0,
			want:            0,
		},
		{
			// effOrig == 0 (zero tokens) → guard returns 0, no divide-by-zero.
			name:            "zero tokens guarded",
			spend:           ModelSpend{TotalQuota: 1000, PromptTokens: 0, CompletionTokens: 0},
			modelRatio:      1.0,
			completionRatio: 2.0,
			altRatio:        0.1,
			altCompletion:   2.0,
			want:            0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := repriceModel(tc.spend, tc.modelRatio, tc.completionRatio, tc.altRatio, tc.altCompletion)
			if got != tc.want {
				t.Errorf("repriceModel = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRepriceModel_AggregateEqualsPerRow proves the aggregate strategy is exact
// for a uniform token mix: re-pricing the summed aggregate equals the sum of
// per-row re-pricing (the formula is linear in quota and token counts).
func TestRepriceModel_AggregateEqualsPerRow(t *testing.T) {
	// Two identical rows; ratios chosen so the per-row result divides evenly,
	// removing int-truncation as a confounder.
	row := ModelSpend{TotalQuota: 600, PromptTokens: 50, CompletionTokens: 100}
	modelRatio, completionRatio, altRatio, altCompletion := 1.0, 2.0, 0.25, 2.0

	perRow := repriceModel(row, modelRatio, completionRatio, altRatio, altCompletion)
	aggregate := repriceModel(
		ModelSpend{TotalQuota: row.TotalQuota * 2, PromptTokens: row.PromptTokens * 2, CompletionTokens: row.CompletionTokens * 2},
		modelRatio, completionRatio, altRatio, altCompletion,
	)
	if aggregate != perRow*2 {
		t.Errorf("aggregate %d != 2*perRow %d", aggregate, perRow*2)
	}
}

func TestCompute_Exclusions(t *testing.T) {
	ratio_setting.InitRatioSettings()
	// Exercise the free_or_zero guard for a MAPPED model whose ratio was driven
	// to 0 (the only way it triggers — an unmapped model is "unmapped" first).
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o":0}`); err != nil {
		t.Fatalf("override ratio: %v", err)
	}
	defer ratio_setting.InitRatioSettings() // restore for other tests in this package

	spends := []ModelSpend{
		{Model: "dall-e-3", TotalQuota: 100, PromptTokens: 0, CompletionTokens: 0, Count: 1},    // per-call priced
		{Model: "o1", TotalQuota: 200, PromptTokens: 100, CompletionTokens: 200, Count: 1},      // reasoning
		{Model: "some-unmapped-model", TotalQuota: 300, PromptTokens: 100, CompletionTokens: 1}, // unmapped
		{Model: "gpt-4o", TotalQuota: 400, PromptTokens: 100, CompletionTokens: 200, Count: 1},  // mapped, ratio→0
	}
	res := Compute(spends)

	want := map[string]string{
		"dall-e-3":            "per_call_priced",
		"o1":                  "reasoning_model",
		"some-unmapped-model": "unmapped",
		"gpt-4o":              "free_or_zero",
	}
	got := map[string]string{}
	for _, e := range res.Excluded {
		got[e.Model] = e.Reason
	}
	for model, reason := range want {
		if got[model] != reason {
			t.Errorf("excluded[%s] = %q, want %q", model, got[model], reason)
		}
	}
	// No fabricated savings from excluded-only input.
	if res.Conservative.TotalSavingsQuota != 0 || res.Aggressive.TotalSavingsQuota != 0 {
		t.Errorf("expected zero savings from all-excluded input, got cons=%d aggr=%d",
			res.Conservative.TotalSavingsQuota, res.Aggressive.TotalSavingsQuota)
	}
	// Total spend is the real sum, never dropped.
	if res.TotalSpendQuota != 1000 {
		t.Errorf("total_spend_quota = %d, want 1000", res.TotalSpendQuota)
	}
}

// TestCompute_NormalSaving re-prices a real mapped model with live ratios and
// checks the math against a hand computation, so the headline can never drift
// from the documented formula.
func TestCompute_NormalSaving(t *testing.T) {
	ratio_setting.InitRatioSettings()

	// gpt-4o: modelRatio 1.25, completionRatio 4.
	// conservative sub gpt-4o-mini: ratio 0.075, completionRatio 4.
	//   effOrig = (100 + 200*4)*1.25 = 1125 ; effAlt = 900*0.075 = 67.5
	//   savings = 1000*(1 - 67.5/1125) = 1000*0.94 = 940.
	res := Compute([]ModelSpend{
		{Model: "gpt-4o", TotalQuota: 1000, PromptTokens: 100, CompletionTokens: 200, Count: 1},
	})
	if res.Conservative.TotalSavingsQuota != 940 {
		t.Errorf("conservative savings = %d, want 940", res.Conservative.TotalSavingsQuota)
	}
	if len(res.Conservative.TopOpportunities) != 1 ||
		res.Conservative.TopOpportunities[0].AltModel != "gpt-4o-mini" {
		t.Errorf("expected one conservative opportunity → gpt-4o-mini, got %+v",
			res.Conservative.TopOpportunities)
	}
	// Aggressive sub deepseek-chat at DeepSeek's list price (ratio 0.15,
	// completionRatio 4): effAlt = (100 + 200*4)*0.15 = 135,
	// savings = 1000*(1 - 135/1125) = 880. At list prices it saves LESS than
	// gpt-4o-mini here — this used to assert the opposite, which only held
	// while deepseek output was billed at its input price.
	if res.Aggressive.TotalSavingsQuota != 880 {
		t.Errorf("aggressive savings = %d, want 880", res.Aggressive.TotalSavingsQuota)
	}
}
