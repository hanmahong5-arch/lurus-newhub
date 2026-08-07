package ratio_setting

import "testing"

// Regression: every Update*ByJSONString used to reset its live map BEFORE
// parsing the caller-supplied JSON. A single malformed admin payload therefore
// wiped the in-memory pricing tables and only then returned the parse error —
// leaving every model without a ratio until a successful update or a restart.
// The contract asserted here: a failed update is a no-op.

const fixRatioBadJSON = `{"broken":`

func TestFixRatioBadJSONKeepsModelRatio(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateModelRatioByJSONString(`{"fix-ratio-probe":3.5}`); err != nil {
		t.Fatalf("seed model ratio: %v", err)
	}
	if err := UpdateModelRatioByJSONString(fixRatioBadJSON); err == nil {
		t.Fatal("UpdateModelRatioByJSONString(malformed) = nil, want error")
	}
	if got := GetModelRatioCopy()["fix-ratio-probe"]; got != 3.5 {
		t.Errorf("model ratio after failed update = %v, want 3.5 (previous value preserved)", got)
	}
	if ratio, ok, _ := GetModelRatio("fix-ratio-probe"); !ok || ratio != 3.5 {
		t.Errorf("GetModelRatio = %v,%v want 3.5,true", ratio, ok)
	}
}

func TestFixRatioBadJSONKeepsModelPrice(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateModelPriceByJSONString(`{"fix-price-probe":0.25}`); err != nil {
		t.Fatalf("seed model price: %v", err)
	}
	if err := UpdateModelPriceByJSONString(fixRatioBadJSON); err == nil {
		t.Fatal("UpdateModelPriceByJSONString(malformed) = nil, want error")
	}
	if price, ok := GetModelPrice("fix-price-probe", false); !ok || price != 0.25 {
		t.Errorf("GetModelPrice = %v,%v want 0.25,true", price, ok)
	}
}

func TestFixRatioBadJSONKeepsCompletionRatio(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateCompletionRatioByJSONString(`{"vendor/fix-probe":9}`); err != nil {
		t.Fatalf("seed completion ratio: %v", err)
	}
	if err := UpdateCompletionRatioByJSONString(fixRatioBadJSON); err == nil {
		t.Fatal("UpdateCompletionRatioByJSONString(malformed) = nil, want error")
	}
	if got := GetCompletionRatio("vendor/fix-probe"); got != 9 {
		t.Errorf("completion ratio after failed update = %v, want 9", got)
	}
}

func TestFixRatioBadJSONKeepsImageRatio(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateImageRatioByJSONString(`{"fix-image-probe":2}`); err != nil {
		t.Fatalf("seed image ratio: %v", err)
	}
	if err := UpdateImageRatioByJSONString(fixRatioBadJSON); err == nil {
		t.Fatal("UpdateImageRatioByJSONString(malformed) = nil, want error")
	}
	if ratio, ok := GetImageRatio("fix-image-probe"); !ok || ratio != 2 {
		t.Errorf("GetImageRatio = %v,%v want 2,true", ratio, ok)
	}
}

func TestFixRatioBadJSONKeepsCacheRatio(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)

	if err := UpdateCacheRatioByJSONString(`{"fix-cache-probe":0.5}`); err != nil {
		t.Fatalf("seed cache ratio: %v", err)
	}
	if err := UpdateCacheRatioByJSONString(fixRatioBadJSON); err == nil {
		t.Fatal("UpdateCacheRatioByJSONString(malformed) = nil, want error")
	}
	if ratio, ok := GetCacheRatio("fix-cache-probe"); !ok || ratio != 0.5 {
		t.Errorf("GetCacheRatio = %v,%v want 0.5,true", ratio, ok)
	}
}

func TestFixRatioBadJSONKeepsGroupRatio(t *testing.T) {
	origGroup := GroupRatio2JSONString()
	origGroupGroup := GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		_ = UpdateGroupRatioByJSONString(origGroup)
		_ = UpdateGroupGroupRatioByJSONString(origGroupGroup)
	})

	if err := UpdateGroupRatioByJSONString(`{"default":1,"fix-group-probe":0.9}`); err != nil {
		t.Fatalf("seed group ratio: %v", err)
	}
	if err := UpdateGroupRatioByJSONString(fixRatioBadJSON); err == nil {
		t.Fatal("UpdateGroupRatioByJSONString(malformed) = nil, want error")
	}
	if got := GetGroupRatio("fix-group-probe"); got != 0.9 {
		t.Errorf("group ratio after failed update = %v, want 0.9", got)
	}

	if err := UpdateGroupGroupRatioByJSONString(`{"fix-group-probe":{"default":0.8}}`); err != nil {
		t.Fatalf("seed group-group ratio: %v", err)
	}
	if err := UpdateGroupGroupRatioByJSONString(fixRatioBadJSON); err == nil {
		t.Fatal("UpdateGroupGroupRatioByJSONString(malformed) = nil, want error")
	}
	if ratio, ok := GetGroupGroupRatio("fix-group-probe", "default"); !ok || ratio != 0.8 {
		t.Errorf("GetGroupGroupRatio = %v,%v want 0.8,true", ratio, ok)
	}
}
