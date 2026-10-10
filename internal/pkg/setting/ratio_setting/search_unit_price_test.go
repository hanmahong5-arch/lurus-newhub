package ratio_setting

import "testing"

func TestSearchUnits_CohereConvention(t *testing.T) {
	for _, tc := range []struct {
		docs, queries int
		want          int64
	}{
		{0, 1, 0}, {1, 1, 1}, {99, 1, 1}, {100, 1, 1}, {101, 1, 2}, {200, 1, 2}, {201, 1, 3},
		{101, 3, 6}, {10, 0, 0}, {-5, 1, 0},
	} {
		if got := SearchUnits(tc.docs, tc.queries); got != tc.want {
			t.Errorf("SearchUnits(%d,%d) = %d, want %d", tc.docs, tc.queries, got, tc.want)
		}
	}
}

func TestSearchUnitPrice_AbsentVsExplicitZero(t *testing.T) {
	prev := SearchUnitPrice2JSONString()
	t.Cleanup(func() { _ = UpdateSearchUnitPriceByJSONString(prev) })

	if err := UpdateSearchUnitPriceByJSONString(`{"model-a":0,"model-b":0.002}`); err != nil {
		t.Fatal(err)
	}
	if p, ok := GetSearchUnitPrice("model-a"); !ok || p != 0 {
		t.Errorf("explicit zero: got %v,%v want 0,true", p, ok)
	}
	if p, ok := GetSearchUnitPrice("model-b"); !ok || p != 0.002 {
		t.Errorf("model-b: got %v,%v", p, ok)
	}
	if _, ok := GetSearchUnitPrice("model-c"); ok {
		t.Error("unlisted model must report absent, not free")
	}
}

func TestSearchUnitPrice_InvalidPayloadKeepsLiveTable(t *testing.T) {
	prev := SearchUnitPrice2JSONString()
	t.Cleanup(func() { _ = UpdateSearchUnitPriceByJSONString(prev) })
	if err := UpdateSearchUnitPriceByJSONString(`{"model-a":0.5}`); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"model-a":-1}`, `not json`, `{"model-a":"x"}`} {
		if err := UpdateSearchUnitPriceByJSONString(bad); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
	if p, ok := GetSearchUnitPrice("model-a"); !ok || p != 0.5 {
		t.Errorf("a rejected update must not clear the live table, got %v,%v", p, ok)
	}
}
