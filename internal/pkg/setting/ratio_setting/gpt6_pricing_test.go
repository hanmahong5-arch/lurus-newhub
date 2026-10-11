package ratio_setting

import (
	"math"
	"testing"
)

// GPT-6 Sol / Luna (2026-09-22) and GPT-6.1 Sol (2026-09-29) Standard-tier
// list prices, $ per 1M tokens: Sol 2 in / 0.20 cached / 2.50 cache write /
// 10 out; Luna 0.10 / 0.01 / 0.125 / 0.50; 6.1 Sol priced as Sol. Ratio
// 1 = $2 / 1M input tokens. Before this table the models fell through to the
// "gpt-" family fallback (1.25, completion 2), which overcharged Sol input by
// 25% and Luna input by 25x while undercharging every output token.
func TestGPT6ListPrices(t *testing.T) {
	InitRatioSettings()
	type price struct{ in, cacheHit, cacheWrite, out float64 }
	want := map[string]price{
		"gpt-6-sol":   {2.00, 0.20, 2.50, 10.00},
		"gpt-6-luna":  {0.10, 0.01, 0.125, 0.50},
		"gpt-6.1-sol": {2.00, 0.20, 2.50, 10.00},
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	for model, p := range want {
		ratio, ok, _ := GetModelRatio(model)
		if !ok {
			t.Errorf("%s: no model ratio", model)
			continue
		}
		in := ratio * 2
		out := in * GetCompletionRatio(model)
		cr, crOK := GetCacheRatio(model)
		ccr, ccrOK := GetCreateCacheRatio(model)
		if !crOK || !ccrOK {
			t.Errorf("%s: cache read listed=%v, cache write listed=%v; both must be explicit", model, crOK, ccrOK)
		}
		if !near(in, p.in) || !near(in*cr, p.cacheHit) || !near(in*ccr, p.cacheWrite) || !near(out, p.out) {
			t.Errorf("%s: in/cached/write/out = $%.4f / $%.4f / $%.4f / $%.4f per 1M, want $%.4f / $%.4f / $%.4f / $%.4f",
				model, in, in*cr, in*ccr, out, p.in, p.cacheHit, p.cacheWrite, p.out)
		}
	}
}

// The family-level output multiplier covers dated or tiered ids the explicit
// table does not list (e.g. a future gpt-6-sol-2026-09-22), so a new Sol
// snapshot is never billed at the generic gpt-4 era 2x.
func TestGPT6CompletionRule_CoversUnlistedIds(t *testing.T) {
	InitRatioSettings()
	if got := GetCompletionRatio("gpt-6-sol-2026-09-22"); got != 5 {
		t.Errorf("gpt-6-sol-2026-09-22 completion ratio = %v, want 5", got)
	}
	// The rule must not leak onto older generations.
	if got := GetCompletionRatio("gpt-5-nano"); got != 8 {
		t.Errorf("gpt-5-nano completion ratio = %v, want 8 (gpt-5 rule)", got)
	}
}
