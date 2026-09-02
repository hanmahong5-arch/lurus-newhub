package helper

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ModelPriceHelper must say whether the cache-creation ratio it hands out is
// the map default or a per-model entry: settlement bills an OpenAI-wire cache
// write at the plain input rate when it is the default (types.PriceData.
// CacheCreationRatioForWire). gpt-5.6 is listed (vendor: 1.25x writes); an
// arbitrary model is not, and gets the same 1.25 only as the Anthropic default.
func TestModelPriceHelper_CacheCreationRatioDefaultedFlag(t *testing.T) {
	seedRatios(t, `{"ratio-model":2.0,"gpt-5.6":2.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})

	for _, tc := range []struct {
		model     string
		defaulted bool
	}{
		{"ratio-model", true},
		{"gpt-5.6", false},
	} {
		info := &relaycommon.RelayInfo{OriginModelName: tc.model, UsingGroup: "default"}
		pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
		if err != nil {
			t.Fatalf("%s: %v", tc.model, err)
		}
		if pd.CacheCreationRatio != 1.25 {
			t.Errorf("%s: CacheCreationRatio = %v, want 1.25", tc.model, pd.CacheCreationRatio)
		}
		if pd.CacheCreationRatioDefaulted != tc.defaulted {
			t.Errorf("%s: CacheCreationRatioDefaulted = %v, want %v", tc.model, pd.CacheCreationRatioDefaulted, tc.defaulted)
		}
		wantWire := 1.25
		if tc.defaulted {
			wantWire = 1
		}
		if got := pd.CacheCreationRatioForWire(true); got != wantWire {
			t.Errorf("%s: OpenAI-wire write ratio = %v, want %v", tc.model, got, wantWire)
		}
		if got := pd.CacheCreationRatioForWire(false); got != 1.25 {
			t.Errorf("%s: Anthropic-wire write ratio = %v, want 1.25", tc.model, got)
		}
	}
}
