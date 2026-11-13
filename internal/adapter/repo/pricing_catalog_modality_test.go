package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

func TestCatalogUsage(t *testing.T) {
	prevSearch := ratio_setting.SearchUnitPrice2JSONString()
	prevAudio := ratio_setting.AudioSecondPrice2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateSearchUnitPriceByJSONString(prevSearch)
		_ = ratio_setting.UpdateAudioSecondPriceByJSONString(prevAudio)
	})
	_ = ratio_setting.UpdateAudioSecondPriceByJSONString(`{}`)

	cases := []struct {
		name      string
		table     string
		model     string
		wantUnit  string
		wantPrice *float64
	}{
		{"absent falls back to token", `{}`, "model-a", UsageUnitToken, nil},
		{"other model configured", `{"model-b":0.002}`, "model-a", UsageUnitToken, nil},
		{"configured price", `{"model-a":0.002}`, "model-a", UsageUnitSearchUnit, f64(0.002)},
		{"explicit zero is free, not unconfigured", `{"model-a":0}`, "model-a", UsageUnitSearchUnit, f64(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ratio_setting.UpdateSearchUnitPriceByJSONString(tc.table); err != nil {
				t.Fatal(err)
			}
			unit, search, audio := catalogUsage(tc.model)
			if unit != tc.wantUnit {
				t.Errorf("unit = %q, want %q", unit, tc.wantUnit)
			}
			if audio != nil {
				t.Errorf("audio price = %v, want nil on search-unit cases", *audio)
			}
			switch {
			case tc.wantPrice == nil && search != nil:
				t.Errorf("price = %v, want nil", *search)
			case tc.wantPrice != nil && search == nil:
				t.Errorf("price = nil, want %v", *tc.wantPrice)
			case tc.wantPrice != nil && *search != *tc.wantPrice:
				t.Errorf("price = %v, want %v", *search, *tc.wantPrice)
			}
		})
	}
}

func TestCatalogUsage_AudioSecond(t *testing.T) {
	prevSearch := ratio_setting.SearchUnitPrice2JSONString()
	prevAudio := ratio_setting.AudioSecondPrice2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateSearchUnitPriceByJSONString(prevSearch)
		_ = ratio_setting.UpdateAudioSecondPriceByJSONString(prevAudio)
	})
	_ = ratio_setting.UpdateSearchUnitPriceByJSONString(`{}`)
	if err := ratio_setting.UpdateAudioSecondPriceByJSONString(`{"tts-a":0.01,"tts-free":0}`); err != nil {
		t.Fatal(err)
	}
	unit, search, audio := catalogUsage("tts-a")
	if unit != UsageUnitAudioSecond || search != nil || audio == nil || *audio != 0.01 {
		t.Fatalf("got unit=%q search=%v audio=%v", unit, search, audio)
	}
	unit, _, audio = catalogUsage("tts-free")
	if unit != UsageUnitAudioSecond || audio == nil || *audio != 0 {
		t.Fatalf("explicit free: unit=%q audio=%v", unit, audio)
	}
	unit, search, audio = catalogUsage("tts-missing")
	if unit != UsageUnitToken || search != nil || audio != nil {
		t.Fatalf("absent: unit=%q search=%v audio=%v", unit, search, audio)
	}
}

func f64(v float64) *float64 { return &v }

func TestPickModality(t *testing.T) {
	cases := []struct {
		name     string
		override string
		abilites []string
		want     string
	}{
		{"override wins", "rerank", []string{"chat", "chat"}, "rerank"},
		{"invalid override ignored", "bogus", []string{"embedding"}, "embedding"},
		{"majority", "", []string{"chat", "embedding", "embedding"}, "embedding"},
		{"tie breaks alphabetically", "", []string{"rerank", "embedding"}, "embedding"},
		{"blanks ignored", "", []string{"", "", "chat"}, "chat"},
		{"all blank unknown", "", []string{"", ""}, ""},
		{"none", "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickModality(tc.override, tc.abilites); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
