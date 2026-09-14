package helper

import (
	"net/http/httptest"
	"reflect"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func priceCtx() *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c
}

// seedRatios installs deterministic ratio/price/group tables for the duration of a
// test, restoring the previous ones afterwards so other tests are unaffected.
func seedRatios(t *testing.T, modelRatio, modelPrice, groupRatio string, groupGroup map[string]map[string]float64) {
	t.Helper()
	prevRatio := ratio_setting.ModelRatio2JSONString()
	prevPrice := ratio_setting.ModelPrice2JSONString()
	prevGroup := ratio_setting.GroupRatio2JSONString()
	prevGroupGroup := ratio_setting.GroupGroupRatio

	if err := ratio_setting.UpdateModelRatioByJSONString(modelRatio); err != nil {
		t.Fatalf("seed model ratio: %v", err)
	}
	if err := ratio_setting.UpdateModelPriceByJSONString(modelPrice); err != nil {
		t.Fatalf("seed model price: %v", err)
	}
	if err := ratio_setting.UpdateGroupRatioByJSONString(groupRatio); err != nil {
		t.Fatalf("seed group ratio: %v", err)
	}
	ratio_setting.GroupGroupRatio = groupGroup

	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(prevRatio)
		_ = ratio_setting.UpdateModelPriceByJSONString(prevPrice)
		_ = ratio_setting.UpdateGroupRatioByJSONString(prevGroup)
		ratio_setting.GroupGroupRatio = prevGroupGroup
	})
}

func TestHandleGroupRatio(t *testing.T) {
	t.Run("normal group ratio", func(t *testing.T) {
		seedRatios(t, `{}`, `{}`, `{"default":1.5}`, map[string]map[string]float64{})
		info := &relaycommon.RelayInfo{UsingGroup: "default", UserGroup: "u"}
		gri := HandleGroupRatio(priceCtx(), info)
		if gri.GroupRatio != 1.5 {
			t.Errorf("GroupRatio = %v, want 1.5", gri.GroupRatio)
		}
		if gri.HasSpecialRatio {
			t.Error("HasSpecialRatio should be false")
		}
	})

	t.Run("user special group-group ratio wins", func(t *testing.T) {
		seedRatios(t, `{}`, `{}`, `{"default":1.5}`,
			map[string]map[string]float64{"vip": {"default": 0.5}})
		info := &relaycommon.RelayInfo{UsingGroup: "default", UserGroup: "vip"}
		gri := HandleGroupRatio(priceCtx(), info)
		if !gri.HasSpecialRatio {
			t.Fatal("HasSpecialRatio should be true")
		}
		if gri.GroupRatio != 0.5 || gri.GroupSpecialRatio != 0.5 {
			t.Errorf("special ratio = %v/%v, want 0.5", gri.GroupRatio, gri.GroupSpecialRatio)
		}
	})

	t.Run("auto_group overrides using group", func(t *testing.T) {
		seedRatios(t, `{}`, `{}`, `{"auto":2.0,"default":1.0}`, map[string]map[string]float64{})
		c := priceCtx()
		c.Set("auto_group", "auto")
		info := &relaycommon.RelayInfo{UsingGroup: "default", UserGroup: "u"}
		gri := HandleGroupRatio(c, info)
		if info.UsingGroup != "auto" {
			t.Errorf("UsingGroup = %q, want auto", info.UsingGroup)
		}
		if gri.GroupRatio != 2.0 {
			t.Errorf("GroupRatio = %v, want 2.0", gri.GroupRatio)
		}
	})
}

func TestModelPriceHelper_RatioBased(t *testing.T) {
	seedRatios(t, `{"ratio-model":2.0}`, `{}`, `{"default":1.5}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "ratio-model", UsingGroup: "default"}
	pd, err := ModelPriceHelper(priceCtx(), info, 1000, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if pd.UsePrice {
		t.Error("UsePrice should be false for ratio-based model")
	}
	if pd.ModelRatio != 2.0 {
		t.Errorf("ModelRatio = %v, want 2.0", pd.ModelRatio)
	}
	if pd.GroupRatioInfo.GroupRatio != 1.5 {
		t.Errorf("GroupRatio = %v, want 1.5", pd.GroupRatioInfo.GroupRatio)
	}
	// preConsumedTokens = max(1000, 500) = 1000; ratio = 2.0*1.5 = 3.0 -> 3000
	if pd.QuotaToPreConsume != 3000 {
		t.Errorf("QuotaToPreConsume = %d, want 3000", pd.QuotaToPreConsume)
	}
	// relayInfo.PriceData is populated as a side effect
	if info.PriceData.ModelRatio != 2.0 {
		t.Errorf("info.PriceData not populated: %v", info.PriceData.ModelRatio)
	}
}

func TestModelPriceHelper_RatioMaxTokens(t *testing.T) {
	seedRatios(t, `{"ratio-model":1.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "ratio-model", UsingGroup: "default"}
	// promptTokens 100 -> max(100,500)=500; +MaxTokens 200 = 700; ratio 1.0 -> 700
	pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{MaxTokens: 200})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if pd.QuotaToPreConsume != 700 {
		t.Errorf("QuotaToPreConsume = %d, want 700", pd.QuotaToPreConsume)
	}
}

func TestModelPriceHelper_PriceBased(t *testing.T) {
	seedRatios(t, `{}`, `{"price-model":0.5}`, `{"default":1.5}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "price-model", UsingGroup: "default"}
	pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !pd.UsePrice {
		t.Error("UsePrice should be true")
	}
	if pd.ModelPrice != 0.5 {
		t.Errorf("ModelPrice = %v, want 0.5", pd.ModelPrice)
	}
	// 0.5 * QuotaPerUnit(500000) * 1.5 = 375000
	if pd.QuotaToPreConsume != 375000 {
		t.Errorf("QuotaToPreConsume = %d, want 375000", pd.QuotaToPreConsume)
	}
}

func TestModelPriceHelper_PriceWithImageRatio(t *testing.T) {
	seedRatios(t, `{}`, `{"price-model":0.5}`, `{"default":1.0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "price-model", UsingGroup: "default"}
	pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{ImagePriceRatio: 2.0})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// modelPrice 0.5 * imageRatio 2 = 1.0 -> 1.0*500000*1.0 = 500000
	if pd.ModelPrice != 1.0 {
		t.Errorf("ModelPrice = %v, want 1.0", pd.ModelPrice)
	}
	if pd.QuotaToPreConsume != 500000 {
		t.Errorf("QuotaToPreConsume = %d, want 500000", pd.QuotaToPreConsume)
	}
}

func TestModelPriceHelper_UnsetRatioRejected(t *testing.T) {
	seedRatios(t, `{}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "unknown-family-zzz-9999", UsingGroup: "default"}
	_, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
	if err == nil {
		t.Fatal("expected unset-ratio error")
	}
}

func TestModelPriceHelper_UnsetRatioAcceptedInSelfUse(t *testing.T) {
	seedRatios(t, `{}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{
		OriginModelName: "unknown-family-zzz-9999",
		UsingGroup:      "default",
		UserSetting:     dto.UserSetting{AcceptUnsetRatioModel: true},
	}
	if _, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{}); err != nil {
		t.Fatalf("self-use should accept unset ratio, got %v", err)
	}
}

func TestModelPriceHelper_FreeModel(t *testing.T) {
	// disable free-model pre-consume so a zero group ratio zeroes the quota
	qs := operation_setting.GetQuotaSetting()
	prev := qs.EnableFreeModelPreConsume
	qs.EnableFreeModelPreConsume = false
	t.Cleanup(func() { qs.EnableFreeModelPreConsume = prev })

	seedRatios(t, `{"ratio-model":2.0}`, `{}`, `{"free":0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "ratio-model", UsingGroup: "free"}
	pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !pd.FreeModel {
		t.Error("FreeModel should be true when group ratio is 0")
	}
	if pd.QuotaToPreConsume != 0 {
		t.Errorf("QuotaToPreConsume = %d, want 0", pd.QuotaToPreConsume)
	}
}

func TestModelPriceHelper_FreeByZeroPrice(t *testing.T) {
	qs := operation_setting.GetQuotaSetting()
	prev := qs.EnableFreeModelPreConsume
	qs.EnableFreeModelPreConsume = false
	t.Cleanup(func() { qs.EnableFreeModelPreConsume = prev })

	seedRatios(t, `{}`, `{"price-model":0}`, `{"default":1.0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "price-model", UsingGroup: "default"}
	pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !pd.FreeModel {
		t.Error("FreeModel should be true when price is 0")
	}
}

func TestModelPriceHelperPerCall(t *testing.T) {
	t.Run("configured per-call price", func(t *testing.T) {
		seedRatios(t, `{}`, `{"mj-model":0.2}`, `{"default":1.0}`, map[string]map[string]float64{})
		info := &relaycommon.RelayInfo{OriginModelName: "mj-model", UsingGroup: "default"}
		pd := ModelPriceHelperPerCall(priceCtx(), info)
		if pd.ModelPrice != 0.2 {
			t.Errorf("ModelPrice = %v, want 0.2", pd.ModelPrice)
		}
		// 0.2 * 500000 * 1.0 = 100000
		if pd.Quota != 100000 {
			t.Errorf("Quota = %d, want 100000", pd.Quota)
		}
	})

	t.Run("fallback to hardcoded default when unconfigured", func(t *testing.T) {
		seedRatios(t, `{}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
		info := &relaycommon.RelayInfo{OriginModelName: "totally-unknown-percall-xyz", UsingGroup: "default"}
		pd := ModelPriceHelperPerCall(priceCtx(), info)
		if pd.ModelPrice != 0.1 {
			t.Errorf("ModelPrice = %v, want 0.1 default", pd.ModelPrice)
		}
		if pd.Quota != 50000 {
			t.Errorf("Quota = %d, want 50000", pd.Quota)
		}
	})
}

// seedContextTiers installs a context-tier map for the duration of a test,
// restoring the previous one afterwards (same convention as seedRatios).
func seedContextTiers(t *testing.T, tiersJSON string) {
	t.Helper()
	prev := ratio_setting.ContextLengthTiers2JSONString()
	if err := ratio_setting.UpdateContextLengthTiersByJSONString(tiersJSON); err != nil {
		t.Fatalf("seed context tiers: %v", err)
	}
	t.Cleanup(func() {
		_ = ratio_setting.UpdateContextLengthTiersByJSONString(prev)
	})
}

// TestModelPriceHelper_NoTiers_ByteIdenticalToFlat is the cycle-8 merge gate
// (operator_decisions): a golden PriceData captured on HEAD before this lane,
// reproduced with the empty default context-tier map, must be untouched by
// the new field/branch.
func TestModelPriceHelper_NoTiers_ByteIdenticalToFlat(t *testing.T) {
	seedRatios(t, `{"golden-model":2.0}`, `{}`, `{"default":1.5}`, map[string]map[string]float64{})
	seedContextTiers(t, `{}`)
	info := &relaycommon.RelayInfo{OriginModelName: "golden-model", UsingGroup: "default"}
	pd, err := ModelPriceHelper(priceCtx(), info, 1000, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Golden values captured against the pre-lane (HEAD) formula:
	// preConsumedTokens = max(1000,500) = 1000; ratio = 2.0*1.5 = 3.0 -> 3000.
	// Non-ratio fields are pulled from the same getters ModelPriceHelper
	// itself calls (GetModelPrice's -1 "not found" sentinel,
	// GetImageRatio/GetAudioRatio/GetAudioCompletionRatio's 1 default) rather
	// than hand-guessed, so this golden only pins the one thing this lane can
	// change (the tier branch), not every unrelated default.
	modelPrice, _ := ratio_setting.GetModelPrice("golden-model", false)
	want := types.PriceData{
		ModelPrice:                  modelPrice,
		ModelRatio:                  2.0,
		CompletionRatio:             ratio_setting.GetCompletionRatio("golden-model"),
		CacheRatio:                  1, // GetCacheRatio default when unconfigured
		CacheCreationRatio:          1.25,
		CacheCreation5mRatio:        1.25,
		CacheCreation1hRatio:        1.25 * claudeCacheCreation1hMultiplier,
		CacheCreationRatioDefaulted: true,
		ImageRatio:                  1,
		AudioRatio:                  1,
		AudioCompletionRatio:        1,
		GroupRatioInfo:              types.GroupRatioInfo{GroupRatio: 1.5, GroupSpecialRatio: -1},
		QuotaToPreConsume:           3000,
		ContextTierThreshold:        0,
		BaseModelRatio:              2.0,
		BaseCompletionRatio:         ratio_setting.GetCompletionRatio("golden-model"),
		BaseCacheRatio:              1,
		BaseRatiosSet:               true,
	}
	if !reflect.DeepEqual(pd, want) {
		t.Errorf("PriceData with empty context tiers = %+v, want byte-identical golden %+v", pd, want)
	}
}

func TestModelPriceHelper_TierOverridesModelRatio(t *testing.T) {
	seedRatios(t, `{"tier-model":1.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	seedContextTiers(t, `{"tier-model":[
		{"threshold_tokens":0,"model_ratio":1.0},
		{"threshold_tokens":3000,"model_ratio":2.0}
	]}`)

	shortInfo := &relaycommon.RelayInfo{OriginModelName: "tier-model", UsingGroup: "default"}
	shortPD, err := ModelPriceHelper(priceCtx(), shortInfo, 100, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err (short): %v", err)
	}
	if shortPD.ModelRatio != 1.0 {
		t.Errorf("short-prompt ModelRatio = %v, want 1.0 (base tier)", shortPD.ModelRatio)
	}
	if shortPD.ContextTierThreshold != 0 {
		t.Errorf("short-prompt ContextTierThreshold = %d, want 0", shortPD.ContextTierThreshold)
	}

	longInfo := &relaycommon.RelayInfo{OriginModelName: "tier-model", UsingGroup: "default"}
	longPD, err := ModelPriceHelper(priceCtx(), longInfo, 3500, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err (long): %v", err)
	}
	if longPD.ModelRatio != 2.0 {
		t.Errorf("long-prompt ModelRatio = %v, want 2.0 (tiered)", longPD.ModelRatio)
	}
	if longPD.ContextTierThreshold != 3000 {
		t.Errorf("long-prompt ContextTierThreshold = %d, want 3000", longPD.ContextTierThreshold)
	}
	// short: preConsumedTokens = max(100, common.PreConsumedQuota=500) = 500;
	// ratio 1.0*1.0 -> 500. long: preConsumedTokens = max(3500,500) = 3500;
	// tiered ratio 2.0*1.0 -> 7000.
	if shortPD.QuotaToPreConsume != 500 {
		t.Errorf("short-prompt QuotaToPreConsume = %d, want 500", shortPD.QuotaToPreConsume)
	}
	if longPD.QuotaToPreConsume != 7000 {
		t.Errorf("long-prompt QuotaToPreConsume = %d, want 7000 (tiered ratio 2x applied to the actual 3500 prompt tokens)", longPD.QuotaToPreConsume)
	}
}

func TestModelPriceHelper_PerCallModelIgnoresTiers(t *testing.T) {
	seedRatios(t, `{}`, `{"percall-tier-model":0.5}`, `{"default":1.0}`, map[string]map[string]float64{})
	seedContextTiers(t, `{"percall-tier-model":[{"threshold_tokens":0,"model_ratio":99}]}`)

	info := &relaycommon.RelayInfo{OriginModelName: "percall-tier-model", UsingGroup: "default"}
	pd, err := ModelPriceHelper(priceCtx(), info, 5000, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !pd.UsePrice {
		t.Fatal("UsePrice should be true (per-call priced model)")
	}
	if pd.ModelPrice != 0.5 {
		t.Errorf("ModelPrice = %v, want 0.5 (untouched by the context tier)", pd.ModelPrice)
	}
	if pd.ContextTierThreshold != 0 {
		t.Errorf("ContextTierThreshold = %d, want 0 (per-call models ignore tiers)", pd.ContextTierThreshold)
	}
	// 0.5 * QuotaPerUnit(500000) * 1.0 = 250000, unaffected by the tier's
	// model_ratio:99 which only applies on the !UsePrice branch.
	if pd.QuotaToPreConsume != 250000 {
		t.Errorf("QuotaToPreConsume = %d, want 250000", pd.QuotaToPreConsume)
	}
}

func TestResettleContextTier_ActualAboveThresholdUsesHigherTier(t *testing.T) {
	seedRatios(t, `{"resettle-model":1.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	seedContextTiers(t, `{"resettle-model":[
		{"threshold_tokens":0,"model_ratio":1.0},
		{"threshold_tokens":3000,"model_ratio":2.0}
	]}`)

	// Simulate the pre-consume estimate having landed below the threshold
	// (ModelRatio 1.0, ContextTierThreshold 0) while the upstream-reported
	// actual prompt tokens land above it. BaseModelRatio/BaseCompletionRatio/
	// BaseCacheRatio are what helper.ModelPriceHelper would have snapshotted
	// pre-consume — the flat, non-tiered ratios — before either tier
	// override.
	pd := types.PriceData{
		ModelRatio: 1.0, CompletionRatio: 1.0, CacheRatio: 1.0,
		BaseModelRatio: 1.0, BaseCompletionRatio: 1.0, BaseCacheRatio: 1.0, BaseRatiosSet: true,
	}
	ResettleContextTier(&pd, "resettle-model", 4000)
	if pd.ModelRatio != 2.0 {
		t.Errorf("ModelRatio after resettle = %v, want 2.0 (actual tokens above threshold)", pd.ModelRatio)
	}
	if pd.ContextTierThreshold != 3000 {
		t.Errorf("ContextTierThreshold after resettle = %d, want 3000", pd.ContextTierThreshold)
	}
}

func TestResettleContextTier_ActualBelowThresholdRevertsToBase(t *testing.T) {
	seedRatios(t, `{"resettle-model2":1.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	seedContextTiers(t, `{"resettle-model2":[
		{"threshold_tokens":0,"model_ratio":1.0},
		{"threshold_tokens":3000,"model_ratio":2.0}
	]}`)

	// Simulate the pre-consume estimate having (wrongly) landed above the
	// threshold; the actual usage comes in below it.
	pd := types.PriceData{
		ModelRatio: 2.0, CompletionRatio: 1.0, CacheRatio: 1.0, ContextTierThreshold: 3000,
		BaseModelRatio: 1.0, BaseCompletionRatio: 1.0, BaseCacheRatio: 1.0, BaseRatiosSet: true,
	}
	ResettleContextTier(&pd, "resettle-model2", 500)
	if pd.ModelRatio != 1.0 {
		t.Errorf("ModelRatio after resettle = %v, want 1.0 (actual tokens below the 3000 threshold, base tier)", pd.ModelRatio)
	}
	if pd.ContextTierThreshold != 0 {
		t.Errorf("ContextTierThreshold after resettle = %d, want 0", pd.ContextTierThreshold)
	}
}

func TestResettleContextTier_NoTiersConfiguredIsNoOp(t *testing.T) {
	seedContextTiers(t, `{}`)
	pd := types.PriceData{ModelRatio: 1.0, CompletionRatio: 1.0, CacheRatio: 1.0, ContextTierThreshold: 0}
	before := pd
	ResettleContextTier(&pd, "no-tiers-model-zzz", 999999)
	if !reflect.DeepEqual(pd, before) {
		t.Errorf("ResettleContextTier with no tiers configured mutated PriceData: before=%+v after=%+v", before, pd)
	}
}

// TestResettleContextTier_NoBaseSnapshotIsNoOp covers a PriceData that never
// went through ModelPriceHelper's token-based branch, so its Base* snapshot is
// still zero. Overwriting the live ratios from that snapshot would charge the
// call at ratio 0; the guard leaves such a value untouched.
func TestResettleContextTier_NoBaseSnapshotIsNoOp(t *testing.T) {
	seedContextTiers(t, `{"no-base-snapshot-model":[{"threshold_tokens":5000,"model_ratio":4.0}]}`)
	// A hand-built PriceData: ratios set, Base* left at zero, and a token count
	// below the only rung so no tier qualifies either.
	pd := types.PriceData{ModelRatio: 2.0, CompletionRatio: 3.0, CacheRatio: 1.0}
	before := pd
	ResettleContextTier(&pd, "no-base-snapshot-model", 10)
	if !reflect.DeepEqual(pd, before) {
		t.Errorf("ResettleContextTier zeroed a PriceData with no pre-consume snapshot: before=%+v after=%+v", before, pd)
	}
}

// TestResettleContextTier_FrozenBaseSurvivesLiveRatioEdit is the cycle-8
// plan §8 L5 B-F3 oracle: the settled ratio must come from the pre-consume
// BaseModelRatio snapshot, not a fresh read of the live ratio_setting map —
// an admin edit (or a routine repo.SyncOptions tick pulling another
// replica's write) landing between pre-consume and settlement must not
// re-price a request that is already in flight. The configured tier only
// overrides CompletionRatio, so ModelRatio falls through to the base on
// every actualPromptTokens value; if ResettleContextTier re-read the live
// map instead of the snapshot, the post-pre-consume edit below would leak
// into the settled ModelRatio.
func TestResettleContextTier_FrozenBaseSurvivesLiveRatioEdit(t *testing.T) {
	seedRatios(t, `{"frozen-tier-model":1.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	seedContextTiers(t, `{"frozen-tier-model":[{"threshold_tokens":0,"completion_ratio":5.0}]}`)

	info := &relaycommon.RelayInfo{OriginModelName: "frozen-tier-model", UsingGroup: "default"}
	pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if pd.ModelRatio != 1.0 || pd.BaseModelRatio != 1.0 {
		t.Fatalf("pre-consume ModelRatio/BaseModelRatio = %v/%v, want 1.0/1.0", pd.ModelRatio, pd.BaseModelRatio)
	}

	// Simulate a concurrent admin edit (or a replica's SyncOptions write)
	// landing on the live flat ratio while this request is still in flight,
	// i.e. after pre-consume but before settlement.
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"frozen-tier-model":99.0}`); err != nil {
		t.Fatalf("mutate live model ratio: %v", err)
	}

	ResettleContextTier(&pd, "frozen-tier-model", 1000)
	if pd.ModelRatio != 1.0 {
		t.Errorf("ModelRatio after settlement = %v, want 1.0 (frozen pre-consume base, not the live-edited 99.0)", pd.ModelRatio)
	}
	if pd.CompletionRatio != 5.0 {
		t.Errorf("CompletionRatio after settlement = %v, want 5.0 (tier override still applies)", pd.CompletionRatio)
	}
}

func TestResettleContextTier_UsePriceIsNoOp(t *testing.T) {
	seedContextTiers(t, `{"percall-resettle-model":[{"threshold_tokens":0,"model_ratio":99}]}`)
	pd := types.PriceData{UsePrice: true, ModelPrice: 0.5}
	before := pd
	ResettleContextTier(&pd, "percall-resettle-model", 999999)
	if !reflect.DeepEqual(pd, before) {
		t.Errorf("ResettleContextTier on a UsePrice PriceData mutated it: before=%+v after=%+v", before, pd)
	}
}

func TestContainPriceOrRatio(t *testing.T) {
	seedRatios(t, `{"has-ratio":1.0}`, `{"has-price":0.3}`, `{"default":1.0}`, map[string]map[string]float64{})
	if !ContainPriceOrRatio("has-price") {
		t.Error("has-price should be found")
	}
	if !ContainPriceOrRatio("has-ratio") {
		t.Error("has-ratio should be found")
	}
	if ContainPriceOrRatio("neither-configured-zzz-9999") {
		t.Error("unconfigured model should not be found")
	}
}
