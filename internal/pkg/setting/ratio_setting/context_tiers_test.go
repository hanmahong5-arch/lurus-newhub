package ratio_setting

import (
	"strconv"
	"testing"
)

func TestGetContextLengthTier_EmptyIsNil(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if got := GetContextLengthTier("no-such-model", 999999); got != nil {
		t.Errorf("GetContextLengthTier(no tiers configured) = %+v, want nil", got)
	}
}

func TestGetContextLengthTier_BoundaryAtThreshold(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":3000,"model_ratio":2}]}`); err != nil {
		t.Fatalf("seed tiers: %v", err)
	}

	if got := GetContextLengthTier("m1", 2999); got != nil {
		t.Errorf("threshold-1: GetContextLengthTier = %+v, want nil", got)
	}
	got := GetContextLengthTier("m1", 3000)
	if got == nil || got.ModelRatio == nil || *got.ModelRatio != 2 {
		t.Fatalf("threshold: GetContextLengthTier = %+v, want tier with ModelRatio=2", got)
	}
	got = GetContextLengthTier("m1", 3001)
	if got == nil || got.ModelRatio == nil || *got.ModelRatio != 2 {
		t.Fatalf("threshold+1: GetContextLengthTier = %+v, want tier with ModelRatio=2", got)
	}
}

func TestGetContextLengthTier_HighestQualifyingWins(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateContextLengthTiersByJSONString(`{"m1":[
		{"threshold_tokens":0,"model_ratio":1},
		{"threshold_tokens":3000,"model_ratio":2},
		{"threshold_tokens":10000,"model_ratio":4}
	]}`); err != nil {
		t.Fatalf("seed tiers: %v", err)
	}

	cases := []struct {
		prompt int
		want   float64
	}{
		{0, 1},
		{2999, 1},
		{3000, 2},
		{9999, 2},
		{10000, 4},
		{50000, 4},
	}
	for _, tc := range cases {
		got := GetContextLengthTier("m1", tc.prompt)
		if got == nil || got.ModelRatio == nil || *got.ModelRatio != tc.want {
			t.Errorf("prompt=%d: GetContextLengthTier = %+v, want ModelRatio=%v", tc.prompt, got, tc.want)
		}
	}
}

func TestUpdateContextLengthTiers_RejectsNonAscending(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	err := UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":3000,"model_ratio":2},{"threshold_tokens":1000,"model_ratio":4}]}`)
	if err == nil {
		t.Fatal("UpdateContextLengthTiersByJSONString(non-ascending) = nil, want error")
	}

	err = UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":3000,"model_ratio":2},{"threshold_tokens":3000,"model_ratio":4}]}`)
	if err == nil {
		t.Fatal("UpdateContextLengthTiersByJSONString(duplicate threshold) = nil, want error")
	}
}

func TestUpdateContextLengthTiers_RejectsNegativeRatio(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	err := UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":0,"model_ratio":-1}]}`)
	if err == nil {
		t.Fatal("UpdateContextLengthTiersByJSONString(negative model_ratio) = nil, want error")
	}
	err = UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":0,"model_ratio":0}]}`)
	if err == nil {
		t.Fatal("UpdateContextLengthTiersByJSONString(zero model_ratio) = nil, want error")
	}
	err = UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":-1,"model_ratio":1}]}`)
	if err == nil {
		t.Fatal("UpdateContextLengthTiersByJSONString(negative threshold) = nil, want error")
	}
}

func TestUpdateContextLengthTiers_InvalidLeavesMemoryUntouched(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":3000,"model_ratio":2}]}`); err != nil {
		t.Fatalf("seed tiers: %v", err)
	}

	if err := UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":3000,"model_ratio":-1}]}`); err == nil {
		t.Fatal("UpdateContextLengthTiersByJSONString(invalid) = nil, want error")
	}

	got := GetContextLengthTier("m1", 3000)
	if got == nil || got.ModelRatio == nil || *got.ModelRatio != 2 {
		t.Errorf("after failed update, GetContextLengthTier(m1,3000) = %+v, want the previously-seeded tier (ModelRatio=2)", got)
	}

	if err := UpdateContextLengthTiersByJSONString(`{broken`); err == nil {
		t.Fatal("UpdateContextLengthTiersByJSONString(malformed JSON) = nil, want error")
	}
	got = GetContextLengthTier("m1", 3000)
	if got == nil || got.ModelRatio == nil || *got.ModelRatio != 2 {
		t.Errorf("after malformed update, GetContextLengthTier(m1,3000) = %+v, want the previously-seeded tier (ModelRatio=2)", got)
	}
}

func TestUpdateContextLengthTiers_RejectsTooManyTiers(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	list := `[`
	for i := 0; i < 9; i++ {
		if i > 0 {
			list += ","
		}
		list += `{"threshold_tokens":` + strconv.Itoa(i*1000) + `,"model_ratio":1}`
	}
	list += `]`
	if err := UpdateContextLengthTiersByJSONString(`{"m1":` + list + `}`); err == nil {
		t.Fatal("UpdateContextLengthTiersByJSONString(9 tiers) = nil, want error (max 8)")
	}
}

func TestUpdateContextLengthTiers_ExplicitEmptyListClears(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateContextLengthTiersByJSONString(`{"m1":[{"threshold_tokens":0,"model_ratio":1}]}`); err != nil {
		t.Fatalf("seed tiers: %v", err)
	}
	if got := GetContextLengthTier("m1", 0); got == nil {
		t.Fatal("precondition: expected a tier before clearing")
	}

	if err := UpdateContextLengthTiersByJSONString(`{"m1":[]}`); err != nil {
		t.Fatalf("clear tiers: %v", err)
	}
	if got := GetContextLengthTier("m1", 0); got != nil {
		t.Errorf("after explicit empty list, GetContextLengthTier(m1,0) = %+v, want nil", got)
	}
}
