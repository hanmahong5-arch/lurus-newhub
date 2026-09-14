package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// TestSetOptionMapValue_ContextLengthTiers exercises the option-loader
// dispatch this lane adds (repo/option.go's updateOptionMap switch case),
// the same round trip UpdateOption/SetOptionMapValue drive for the other
// four pricing maps (ModelRatio/CompletionRatio/ModelPrice/CacheRatio).
func TestSetOptionMapValue_ContextLengthTiers(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	prev := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prev) })

	if err := UpdateOption("ContextLengthTiers", `{"opt-tier-model":[{"threshold_tokens":0,"model_ratio":1.5}]}`); err != nil {
		t.Fatalf("UpdateOption(ContextLengthTiers) = %v, want nil", err)
	}

	got := ratio_setting.GetContextLengthTier("opt-tier-model", 0)
	if got == nil || got.ModelRatio == nil || *got.ModelRatio != 1.5 {
		t.Errorf("GetContextLengthTier after UpdateOption = %+v, want tier with ModelRatio=1.5", got)
	}

	var persisted Option
	if err := DB.First(&persisted, "key = ?", "ContextLengthTiers").Error; err != nil {
		t.Fatalf("reload persisted option row: %v", err)
	}
	if persisted.Value != `{"opt-tier-model":[{"threshold_tokens":0,"model_ratio":1.5}]}` {
		t.Errorf("persisted option value = %q, want the raw JSON UpdateOption was given", persisted.Value)
	}
}

// TestSetOptionMapValue_ContextLengthTiers_MalformedRejectedLeavesMemoryIntact
// mirrors the other pricing keys' "malformed write never applies" contract
// (fix_ratio_bad_json_test.go's pattern, extended through the option-map
// dispatch layer this lane's case statement adds).
func TestSetOptionMapValue_ContextLengthTiers_MalformedRejectedLeavesMemoryIntact(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	prev := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prev) })

	if err := UpdateOption("ContextLengthTiers", `{"opt-tier-probe":[{"threshold_tokens":0,"model_ratio":3}]}`); err != nil {
		t.Fatalf("seed via UpdateOption: %v", err)
	}

	// Non-ascending thresholds: rejected by ratio_setting's validation, which
	// SetOptionMapValue surfaces as an error from updateOptionMap's switch.
	if err := SetOptionMapValue("ContextLengthTiers", `{"opt-tier-probe":[{"threshold_tokens":3000,"model_ratio":1},{"threshold_tokens":1000,"model_ratio":2}]}`); err == nil {
		t.Fatal("SetOptionMapValue(non-ascending tiers) = nil, want error")
	}

	got := ratio_setting.GetContextLengthTier("opt-tier-probe", 0)
	if got == nil || got.ModelRatio == nil || *got.ModelRatio != 3 {
		t.Errorf("after rejected write, GetContextLengthTier = %+v, want the previously-seeded tier (ModelRatio=3)", got)
	}
}
