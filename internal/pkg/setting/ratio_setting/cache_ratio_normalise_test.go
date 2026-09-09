package ratio_setting

import "testing"

// GetCacheRatio and GetImageRatio previously looked up the raw model name
// only, so an operator-configured family entry (e.g. "<family>-thinking-*"
// or a gizmo family) was silently ignored whenever the caller passed a
// thinking-budget or gizmo variant name — the configured discount never
// applied and billing fell back to the hardcoded default. This is a money
// defect for those two getters; GetCreateCacheRatio is normalised for
// consistency but has no runtime write path, so it has no operator-visible
// billing effect (see cache_ratio.go).

func TestGetCacheRatio_NormalisesThinkingBudgetName(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)
	if err := UpdateCacheRatioByJSONString(`{"gemini-2.5-flash-thinking-*":0.25}`); err != nil {
		t.Fatalf("UpdateCacheRatioByJSONString: %v", err)
	}

	ratio, ok := GetCacheRatio("gemini-2.5-flash-thinking-1024")
	if !ok || ratio != 0.25 {
		t.Errorf("GetCacheRatio(gemini-2.5-flash-thinking-1024) = %v,%v want 0.25,true", ratio, ok)
	}
}

func TestGetCacheRatio_ExactKeyWinsOverWildcard(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)
	if err := UpdateCacheRatioByJSONString(`{"gemini-2.5-flash-thinking-1024":0.5,"gemini-2.5-flash-thinking-*":0.25}`); err != nil {
		t.Fatalf("UpdateCacheRatioByJSONString: %v", err)
	}

	ratio, ok := GetCacheRatio("gemini-2.5-flash-thinking-1024")
	if !ok || ratio != 0.5 {
		t.Errorf("GetCacheRatio(gemini-2.5-flash-thinking-1024) = %v,%v want 0.5,true (exact key must win)", ratio, ok)
	}
}

func TestGetCreateCacheRatio_NormalisesThinkingBudgetName(t *testing.T) {
	orig := defaultCreateCacheRatio
	defer func() { defaultCreateCacheRatio = orig }()
	defaultCreateCacheRatio = map[string]float64{"gemini-2.5-flash-thinking-*": 0.25}

	ratio, ok := GetCreateCacheRatio("gemini-2.5-flash-thinking-1024")
	if !ok || ratio != 0.25 {
		t.Errorf("GetCreateCacheRatio(gemini-2.5-flash-thinking-1024) = %v,%v want 0.25,true", ratio, ok)
	}
}

func TestGetCreateCacheRatio_ExactKeyWinsOverWildcard(t *testing.T) {
	orig := defaultCreateCacheRatio
	defer func() { defaultCreateCacheRatio = orig }()
	defaultCreateCacheRatio = map[string]float64{
		"gemini-2.5-flash-thinking-1024": 0.5,
		"gemini-2.5-flash-thinking-*":    0.25,
	}

	ratio, ok := GetCreateCacheRatio("gemini-2.5-flash-thinking-1024")
	if !ok || ratio != 0.5 {
		t.Errorf("GetCreateCacheRatio(gemini-2.5-flash-thinking-1024) = %v,%v want 0.5,true (exact key must win)", ratio, ok)
	}
}

func TestGetImageRatio_NormalisesGizmoName(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)
	if err := UpdateImageRatioByJSONString(`{"gpt-4-gizmo-*":2}`); err != nil {
		t.Fatalf("UpdateImageRatioByJSONString: %v", err)
	}

	ratio, ok := GetImageRatio("gpt-4-gizmo-g-abc123")
	if !ok || ratio != 2 {
		t.Errorf("GetImageRatio(gpt-4-gizmo-g-abc123) = %v,%v want 2,true", ratio, ok)
	}
}

func TestGetImageRatio_ExactKeyWinsOverWildcard(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(InitRatioSettings)
	if err := UpdateImageRatioByJSONString(`{"gpt-4-gizmo-g-abc123":3,"gpt-4-gizmo-*":2}`); err != nil {
		t.Fatalf("UpdateImageRatioByJSONString: %v", err)
	}

	ratio, ok := GetImageRatio("gpt-4-gizmo-g-abc123")
	if !ok || ratio != 3 {
		t.Errorf("GetImageRatio(gpt-4-gizmo-g-abc123) = %v,%v want 3,true (exact key must win)", ratio, ok)
	}
}
