package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

func TestCatalogUsage(t *testing.T) {
	prev := ratio_setting.SearchUnitPrice2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateSearchUnitPriceByJSONString(prev) })

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
			unit, price := catalogUsage(tc.model)
			if unit != tc.wantUnit {
				t.Errorf("unit = %q, want %q", unit, tc.wantUnit)
			}
			switch {
			case tc.wantPrice == nil && price != nil:
				t.Errorf("price = %v, want nil", *price)
			case tc.wantPrice != nil && price == nil:
				t.Errorf("price = nil, want %v", *tc.wantPrice)
			case tc.wantPrice != nil && *price != *tc.wantPrice:
				t.Errorf("price = %v, want %v", *price, *tc.wantPrice)
			}
		})
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
