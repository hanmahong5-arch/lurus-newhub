package ratio_setting

import (
	"math"
	"testing"
)

// DeepSeek list prices (api-docs.deepseek.com/quick_start/pricing, read
// 2026-09-23, peak): deepseek-flash $0.30 in / $1.20 out / $0.006 cache hit
// per 1M; deepseek-v4-pro $1.32 / $3.96 / $0.044. deepseek-chat, -reasoner
// and -v4-flash are legacy names answered by deepseek-flash (verified live
// against the upstream the same day). Ratio 1 = $2 / 1M input tokens.
//
// Before this, output was billed at the input price (completion ratio 1)
// and v4-pro input at a third of its price.
func TestDeepSeekListPrices(t *testing.T) {
	InitRatioSettings()
	type price struct{ in, out, cacheHit float64 } // $ per 1M tokens
	want := map[string]price{
		"deepseek-flash":    {0.30, 1.20, 0.006},
		"deepseek-chat":     {0.30, 1.20, 0.006},
		"deepseek-reasoner": {0.30, 1.20, 0.006},
		"deepseek-v4-flash": {0.30, 1.20, 0.006},
		"deepseek-v4-pro":   {1.32, 3.96, 0.044},
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
		cr, _ := GetCacheRatio(model)
		if !near(in, p.in) || !near(out, p.out) || !near(in*cr, p.cacheHit) {
			t.Errorf("%s: in/out/cache = $%.4f / $%.4f / $%.4f per 1M, want $%.4f / $%.4f / $%.4f",
				model, in, out, in*cr, p.in, p.out, p.cacheHit)
		}
	}
}

// Other hosts' DeepSeek builds ("deepseek-ai/...") are priced by those hosts,
// not by DeepSeek's own output multiplier.
func TestDeepSeekCompletionRule_SkipsOtherHosts(t *testing.T) {
	InitRatioSettings()
	if got := GetCompletionRatio("deepseek-ai/DeepSeek-V3.1"); got == 4 {
		t.Errorf("deepseek-ai/DeepSeek-V3.1 completion ratio = 4: DeepSeek's own rule leaked onto another host's model")
	}
}
