package types

import "testing"

// CacheCreationRatioForWire: the 1.25 map default is Anthropic's write
// surcharge. It must keep applying on the Anthropic wire (flag false) whether
// or not the model is listed, and on the OpenAI/Gemini wire (flag true) only
// when the operator actually listed the model; an unlisted model there bills
// the write at the plain input rate. An explicitly set ratio is always honoured
// regardless of wire (Defaulted is set only by helper.ModelPriceHelper).
func TestPriceData_CacheCreationRatioForWire(t *testing.T) {
	cases := []struct {
		name                 string
		ratio                float64
		defaulted            bool
		promptIncludesCached bool
		want                 float64
	}{
		{"anthropic wire, listed", 1.25, false, false, 1.25},
		{"anthropic wire, unlisted (map default)", 1.25, true, false, 1.25},
		{"openai wire, listed (gpt-5.6 class)", 1.25, false, true, 1.25},
		{"openai wire, unlisted: plain input rate", 1.25, true, true, 1},
		{"openai wire, explicit operator ratio honoured", 3, false, true, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &PriceData{CacheCreationRatio: tc.ratio, CacheCreationRatioDefaulted: tc.defaulted}
			if got := p.CacheCreationRatioForWire(tc.promptIncludesCached); got != tc.want {
				t.Errorf("CacheCreationRatioForWire(%v) = %v, want %v", tc.promptIncludesCached, got, tc.want)
			}
		})
	}
}
