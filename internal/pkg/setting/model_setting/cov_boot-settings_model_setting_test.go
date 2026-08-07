package model_setting

import (
	"net/http"
	"testing"
)

// --- claude.go ---

func boot_settings_resetClaudeSettings(t *testing.T) {
	t.Helper()
	orig := claudeSettings
	t.Cleanup(func() { claudeSettings = orig })
}

func TestGetClaudeSettings_BackfillsMissingDefaultKey(t *testing.T) {
	boot_settings_resetClaudeSettings(t)
	// Simulate a config load that dropped the "default" key entirely — the
	// getter must self-heal it so GetDefaultMaxTokens never panics on a
	// missing fallback.
	claudeSettings.DefaultMaxTokens = map[string]int{"gpt-4o": 4096}

	s := GetClaudeSettings()
	if got, ok := s.DefaultMaxTokens["default"]; !ok || got != 8192 {
		t.Fatalf("expected self-healed default=8192, got %v (present=%v)", got, ok)
	}
	if s.DefaultMaxTokens["gpt-4o"] != 4096 {
		t.Fatalf("expected existing entries preserved, got %v", s.DefaultMaxTokens)
	}
}

func TestClaudeSettings_GetDefaultMaxTokens(t *testing.T) {
	boot_settings_resetClaudeSettings(t)
	claudeSettings.DefaultMaxTokens = map[string]int{
		"default":           8192,
		"claude-3-5-sonnet": 4096,
	}
	c := GetClaudeSettings()

	if got := c.GetDefaultMaxTokens("claude-3-5-sonnet"); got != 4096 {
		t.Fatalf("expected model-specific override 4096, got %d", got)
	}
	if got := c.GetDefaultMaxTokens("unknown-model"); got != 8192 {
		t.Fatalf("expected fallback to default 8192 for unknown model, got %d", got)
	}
}

func TestClaudeSettings_WriteHeaders_MergesAndDedupes(t *testing.T) {
	boot_settings_resetClaudeSettings(t)
	claudeSettings.HeadersSettings = map[string]map[string][]string{
		"claude-3-5-sonnet": {
			"anthropic-beta": {"beta-a", "beta-b"},
		},
	}
	c := GetClaudeSettings()

	h := &http.Header{}
	h.Add("anthropic-beta", "beta-a") // pre-existing value that must not be duplicated
	c.WriteHeaders("claude-3-5-sonnet", h)

	values := h.Values("anthropic-beta")
	if len(values) != 2 {
		t.Fatalf("expected deduped header values (beta-a kept once, beta-b appended), got %v", values)
	}
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	if !seen["beta-a"] || !seen["beta-b"] {
		t.Fatalf("expected both beta-a and beta-b present, got %v", values)
	}
}

func TestClaudeSettings_WriteHeaders_UnknownModelNoOp(t *testing.T) {
	boot_settings_resetClaudeSettings(t)
	claudeSettings.HeadersSettings = map[string]map[string][]string{
		"claude-3-5-sonnet": {"x": {"1"}},
	}
	c := GetClaudeSettings()

	h := &http.Header{}
	c.WriteHeaders("some-other-model", h)
	if len(*h) != 0 {
		t.Fatalf("expected no headers written for unconfigured model, got %v", h)
	}
}

// --- gemini.go ---

func boot_settings_resetGeminiSettings(t *testing.T) {
	t.Helper()
	orig := geminiSettings
	t.Cleanup(func() { geminiSettings = orig })
}

func TestGetGeminiSafetySetting_FallsBackToDefault(t *testing.T) {
	boot_settings_resetGeminiSettings(t)
	geminiSettings.SafetySettings = map[string]string{
		"default":        "OFF",
		"gemini-1.5-pro": "BLOCK_HIGH",
	}
	if got := GetGeminiSafetySetting("gemini-1.5-pro"); got != "BLOCK_HIGH" {
		t.Fatalf("expected model-specific safety setting, got %q", got)
	}
	if got := GetGeminiSafetySetting("unregistered-model"); got != "OFF" {
		t.Fatalf("expected fallback to default safety setting, got %q", got)
	}
}

func TestGetGeminiVersionSetting_FallsBackToDefault(t *testing.T) {
	boot_settings_resetGeminiSettings(t)
	geminiSettings.VersionSettings = map[string]string{
		"default":        "v1beta",
		"gemini-1.0-pro": "v1",
	}
	if got := GetGeminiVersionSetting("gemini-1.0-pro"); got != "v1" {
		t.Fatalf("expected v1 for gemini-1.0-pro, got %q", got)
	}
	if got := GetGeminiVersionSetting("gemini-2.0-flash"); got != "v1beta" {
		t.Fatalf("expected fallback to default version v1beta, got %q", got)
	}
}

func TestIsGeminiModelSupportImagine(t *testing.T) {
	boot_settings_resetGeminiSettings(t)
	geminiSettings.SupportedImagineModels = []string{"gemini-2.0-flash-exp", "gemini-2.0-flash-exp-image-generation"}

	if !IsGeminiModelSupportImagine("gemini-2.0-flash-exp") {
		t.Fatalf("expected gemini-2.0-flash-exp to support imagine")
	}
	if IsGeminiModelSupportImagine("gemini-1.5-pro") {
		t.Fatalf("expected gemini-1.5-pro to NOT support imagine")
	}
	if IsGeminiModelSupportImagine("") {
		t.Fatalf("expected empty model name to NOT support imagine")
	}
}

// --- global.go ---

func boot_settings_resetGlobalSettings(t *testing.T) {
	t.Helper()
	orig := globalSettings
	t.Cleanup(func() { globalSettings = orig })
}

func TestShouldPreserveThinkingSuffix(t *testing.T) {
	boot_settings_resetGlobalSettings(t)
	globalSettings.ThinkingModelBlacklist = []string{"moonshotai/kimi-k2-thinking", "kimi-k2-thinking"}

	if !ShouldPreserveThinkingSuffix("kimi-k2-thinking") {
		t.Fatalf("expected blacklisted model to preserve thinking suffix")
	}
	if !ShouldPreserveThinkingSuffix("  kimi-k2-thinking  ") {
		t.Fatalf("expected surrounding whitespace to be trimmed before matching")
	}
	if ShouldPreserveThinkingSuffix("gpt-4o") {
		t.Fatalf("expected non-blacklisted model to NOT preserve thinking suffix")
	}
	// Edge case: an empty/whitespace-only model name must short-circuit to
	// false rather than matching an empty blacklist entry.
	if ShouldPreserveThinkingSuffix("") {
		t.Fatalf("expected empty model name to return false")
	}
	if ShouldPreserveThinkingSuffix("   ") {
		t.Fatalf("expected whitespace-only model name to return false")
	}
}

func TestGetGlobalSettings_ReturnsLiveMutablePointer(t *testing.T) {
	boot_settings_resetGlobalSettings(t)
	GetGlobalSettings().PassThroughRequestEnabled = true
	if !globalSettings.PassThroughRequestEnabled {
		t.Fatalf("expected mutation via GetGlobalSettings() to affect package state")
	}
}
