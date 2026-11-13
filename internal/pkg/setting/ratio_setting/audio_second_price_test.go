package ratio_setting

import "testing"

func TestAudioBillableSeconds(t *testing.T) {
	cases := []struct {
		in   float64
		want int64
	}{
		{0, 0},
		{-1, 0},
		{0.1, 1},
		{1.0, 1},
		{1.01, 2},
		{59.1, 60},
	}
	for _, tc := range cases {
		if got := AudioBillableSeconds(tc.in); got != tc.want {
			t.Errorf("AudioBillableSeconds(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestAudioSecondsFromLegacyTokens(t *testing.T) {
	// 33 tokens ≈ ceil(2)/60*1000 from a 2s clip.
	if got := AudioSecondsFromLegacyTokens(33); got != 2 {
		t.Errorf("AudioSecondsFromLegacyTokens(33) = %d, want 2", got)
	}
	if got := AudioSecondsFromLegacyTokens(0); got != 0 {
		t.Errorf("AudioSecondsFromLegacyTokens(0) = %d, want 0", got)
	}
}

func TestAudioSecondPrice_AbsentVsExplicitZero(t *testing.T) {
	prev := AudioSecondPrice2JSONString()
	t.Cleanup(func() { _ = UpdateAudioSecondPriceByJSONString(prev) })

	if err := UpdateAudioSecondPriceByJSONString(`{"model-a":0,"model-b":0.002}`); err != nil {
		t.Fatal(err)
	}
	if p, ok := GetAudioSecondPrice("model-a"); !ok || p != 0 {
		t.Fatalf("explicit 0: got %v,%v", p, ok)
	}
	if p, ok := GetAudioSecondPrice("model-b"); !ok || p != 0.002 {
		t.Fatalf("priced: got %v,%v", p, ok)
	}
	if _, ok := GetAudioSecondPrice("model-c"); ok {
		t.Fatal("absent must not report ok")
	}
}

func TestAudioSecondPrice_InvalidPayloadKeepsLiveTable(t *testing.T) {
	prev := AudioSecondPrice2JSONString()
	t.Cleanup(func() { _ = UpdateAudioSecondPriceByJSONString(prev) })
	if err := UpdateAudioSecondPriceByJSONString(`{"model-a":0.5}`); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`[1]`, `{"model-a":-1}`, `{"model-a":"x"}`} {
		if err := UpdateAudioSecondPriceByJSONString(bad); err == nil {
			t.Fatalf("expected error for %s", bad)
		}
	}
	if p, ok := GetAudioSecondPrice("model-a"); !ok || p != 0.5 {
		t.Fatalf("live table cleared: %v,%v", p, ok)
	}
}
