package ratio_setting

import (
	"math"
	"testing"
)

// The hosted TypeSafe list price is $0.042 per 1M input tokens, output free.
// Ratio 1 = $2 / 1M input tokens, so the default ratio is 0.021 for every
// name a System One channel serves; without a default the gateway would
// refuse the model (or price it at the unknown-model fallback).
func TestSystemOneDefaultPrices(t *testing.T) {
	InitRatioSettings()
	names := []string{
		"jev-latest", "jev-preview", "jev-1.13.0",
		"laya-auto", "laya-english", "laya-multilingual", "laya-typed-decisions",
	}
	for _, name := range names {
		ratio, ok, _ := GetModelRatio(name)
		if !ok {
			t.Errorf("%s: no default model ratio", name)
			continue
		}
		if math.Abs(ratio*2-0.042) > 1e-9 {
			t.Errorf("%s: $%.6f per 1M input tokens, want $0.042", name, ratio*2)
		}
	}
}

// The defaults must be in the exported default table too, which is what the
// admin pricing page and the options seeding read.
func TestSystemOneDefaultPrices_InDefaultTable(t *testing.T) {
	if got := GetDefaultModelRatioMap()["jev-latest"]; math.Abs(got*2-0.042) > 1e-9 {
		t.Errorf("default table jev-latest = %v, want ratio 0.021", got)
	}
}

// A persisted ModelRatio/CompletionRatio option replaces the live tables
// wholesale (every deployment where an operator ever saved prices). The System
// One defaults must survive that, an operator's own row must still win, and
// output must stay free whatever the completion table says.
func TestSystemOnePrices_SurviveAPersistedTable(t *testing.T) {
	InitRatioSettings()
	origModel := ModelRatio2JSONString()
	origCompletion := CompletionRatio2JSONString()
	t.Cleanup(func() {
		_ = UpdateModelRatioByJSONString(origModel)
		_ = UpdateCompletionRatioByJSONString(origCompletion)
	})

	if err := UpdateModelRatioByJSONString(`{"model-a":1,"laya-english":0.05}`); err != nil {
		t.Fatal(err)
	}
	if err := UpdateCompletionRatioByJSONString(`{"model-a":2,"laya-english":3}`); err != nil {
		t.Fatal(err)
	}

	if got, ok, _ := GetModelRatio("jev-latest"); !ok || math.Abs(got-0.021) > 1e-9 {
		t.Errorf("jev-latest after a persisted table without it = %v (ok=%v), want the 0.021 list price", got, ok)
	}
	if got, _, _ := GetModelRatio("laya-english"); math.Abs(got-0.05) > 1e-9 {
		t.Errorf("laya-english = %v, want the operator's own 0.05", got)
	}
	for _, name := range []string{"jev-latest", "laya-english"} {
		if got := GetCompletionRatio(name); got != 0 {
			t.Errorf("completion ratio of %s = %v, want 0 (output is free)", name, got)
		}
	}
	if got := GetCompletionRatio("model-a"); got != 2 {
		t.Errorf("completion ratio of an unrelated model = %v, want its own 2", got)
	}
}
