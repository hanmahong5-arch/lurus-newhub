package model_setting

import (
	"net/http"
	"testing"
)

// snapshotClaudeSettings saves and restores the global claudeSettings so tests remain -count=1 safe.
func snapshotClaudeSettings(t *testing.T) {
	t.Helper()
	orig := claudeSettings
	origHeaders := make(map[string]map[string][]string, len(orig.HeadersSettings))
	for k, v := range orig.HeadersSettings {
		inner := make(map[string][]string, len(v))
		for hk, hv := range v {
			cp := make([]string, len(hv))
			copy(cp, hv)
			inner[hk] = cp
		}
		origHeaders[k] = inner
	}
	origMaxTokens := make(map[string]int, len(orig.DefaultMaxTokens))
	for k, v := range orig.DefaultMaxTokens {
		origMaxTokens[k] = v
	}
	t.Cleanup(func() {
		claudeSettings = orig
		claudeSettings.HeadersSettings = origHeaders
		claudeSettings.DefaultMaxTokens = origMaxTokens
	})
}

func snapshotGeminiSettings(t *testing.T) {
	t.Helper()
	orig := geminiSettings
	t.Cleanup(func() {
		geminiSettings = orig
	})
}

func snapshotGlobalSettings(t *testing.T) {
	t.Helper()
	orig := globalSettings
	t.Cleanup(func() {
		globalSettings = orig
	})
}

func TestGetClaudeSettings_BackfillsDefaultMaxTokens(t *testing.T) {
	snapshotClaudeSettings(t)

	// Remove the "default" key to force GetClaudeSettings to backfill it.
	claudeSettings.DefaultMaxTokens = map[string]int{}

	got := GetClaudeSettings()
	if got.DefaultMaxTokens["default"] != 8192 {
		t.Fatalf("expected backfilled default max tokens 8192, got %d", got.DefaultMaxTokens["default"])
	}

	// Calling again when "default" already present should not overwrite an existing custom value.
	claudeSettings.DefaultMaxTokens["default"] = 4096
	got2 := GetClaudeSettings()
	if got2.DefaultMaxTokens["default"] != 4096 {
		t.Fatalf("expected preserved default max tokens 4096, got %d", got2.DefaultMaxTokens["default"])
	}
}

func TestClaudeSettings_GetDefaultMaxTokens(t *testing.T) {
	snapshotClaudeSettings(t)

	c := &ClaudeSettings{
		DefaultMaxTokens: map[string]int{
			"default":        8192,
			"claude-3-opus":  4096,
			"claude-3-sonnet": 2048,
		},
	}

	tests := []struct {
		name  string
		model string
		want  int
	}{
		{"known model returns its own value", "claude-3-opus", 4096},
		{"another known model returns its own value", "claude-3-sonnet", 2048},
		{"unknown model falls back to default", "claude-unknown", 8192},
		{"empty model falls back to default", "", 8192},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.GetDefaultMaxTokens(tt.model); got != tt.want {
				t.Errorf("GetDefaultMaxTokens(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

func TestClaudeSettings_WriteHeaders_NoMatchingModel(t *testing.T) {
	c := &ClaudeSettings{
		HeadersSettings: map[string]map[string][]string{
			"claude-3-opus": {
				"X-Custom": {"value1"},
			},
		},
	}

	h := &http.Header{}
	c.WriteHeaders("unknown-model", h)

	if len(*h) != 0 {
		t.Fatalf("expected no headers written for unknown model, got %v", h)
	}
}

func TestClaudeSettings_WriteHeaders_AddsNewValues(t *testing.T) {
	c := &ClaudeSettings{
		HeadersSettings: map[string]map[string][]string{
			"claude-3-opus": {
				"X-Custom": {"value1", "value2"},
			},
		},
	}

	h := &http.Header{}
	c.WriteHeaders("claude-3-opus", h)

	got := h.Values("X-Custom")
	if len(got) != 2 || got[0] != "value1" || got[1] != "value2" {
		t.Fatalf("expected [value1 value2], got %v", got)
	}
}

func TestClaudeSettings_WriteHeaders_DedupesExistingValues(t *testing.T) {
	c := &ClaudeSettings{
		HeadersSettings: map[string]map[string][]string{
			"claude-3-opus": {
				"X-Custom": {"value1", "value2"},
			},
		},
	}

	h := &http.Header{}
	h.Add("X-Custom", "value1") // pre-existing value should not be duplicated

	c.WriteHeaders("claude-3-opus", h)

	got := h.Values("X-Custom")
	if len(got) != 2 {
		t.Fatalf("expected 2 values (no duplicate of value1), got %v", got)
	}
	if got[0] != "value1" || got[1] != "value2" {
		t.Fatalf("expected [value1 value2], got %v", got)
	}
}

func TestClaudeSettings_WriteHeaders_MultipleHeaderKeys(t *testing.T) {
	c := &ClaudeSettings{
		HeadersSettings: map[string]map[string][]string{
			"claude-3-opus": {
				"X-A": {"a1"},
				"X-B": {"b1", "b2"},
			},
		},
	}

	h := &http.Header{}
	c.WriteHeaders("claude-3-opus", h)

	if got := h.Values("X-A"); len(got) != 1 || got[0] != "a1" {
		t.Fatalf("X-A: expected [a1], got %v", got)
	}
	if got := h.Values("X-B"); len(got) != 2 || got[0] != "b1" || got[1] != "b2" {
		t.Fatalf("X-B: expected [b1 b2], got %v", got)
	}
}

func TestGetGeminiSettings_ReturnsGlobalInstance(t *testing.T) {
	snapshotGeminiSettings(t)

	geminiSettings.ThinkingAdapterEnabled = true
	got := GetGeminiSettings()
	if !got.ThinkingAdapterEnabled {
		t.Fatalf("expected GetGeminiSettings to return the mutated global instance")
	}
}

func TestGetGeminiSafetySetting(t *testing.T) {
	snapshotGeminiSettings(t)

	geminiSettings.SafetySettings = map[string]string{
		"default":        "OFF",
		"gemini-1.5-pro": "BLOCK_NONE",
	}

	tests := []struct {
		name string
		key  string
		want string
	}{
		{"known key returns its value", "gemini-1.5-pro", "BLOCK_NONE"},
		{"unknown key falls back to default", "gemini-unknown", "OFF"},
		{"empty key falls back to default", "", "OFF"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetGeminiSafetySetting(tt.key); got != tt.want {
				t.Errorf("GetGeminiSafetySetting(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestGetGeminiVersionSetting(t *testing.T) {
	snapshotGeminiSettings(t)

	geminiSettings.VersionSettings = map[string]string{
		"default":        "v1beta",
		"gemini-1.0-pro": "v1",
	}

	tests := []struct {
		name string
		key  string
		want string
	}{
		{"known key returns its value", "gemini-1.0-pro", "v1"},
		{"unknown key falls back to default", "gemini-2.0-flash", "v1beta"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetGeminiVersionSetting(tt.key); got != tt.want {
				t.Errorf("GetGeminiVersionSetting(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestIsGeminiModelSupportImagine(t *testing.T) {
	snapshotGeminiSettings(t)

	geminiSettings.SupportedImagineModels = []string{
		"gemini-2.0-flash-exp-image-generation",
		"gemini-2.0-flash-exp",
	}

	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{"supported model matches", "gemini-2.0-flash-exp-image-generation", true},
		{"another supported model matches", "gemini-2.0-flash-exp", true},
		{"unsupported model does not match", "gemini-1.5-pro", false},
		{"empty model does not match", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsGeminiModelSupportImagine(tt.model); got != tt.want {
				t.Errorf("IsGeminiModelSupportImagine(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestGetGlobalSettings_ReturnsGlobalInstance(t *testing.T) {
	snapshotGlobalSettings(t)

	globalSettings.PassThroughRequestEnabled = true
	got := GetGlobalSettings()
	if !got.PassThroughRequestEnabled {
		t.Fatalf("expected GetGlobalSettings to return the mutated global instance")
	}
}

func TestShouldPreserveThinkingSuffix(t *testing.T) {
	snapshotGlobalSettings(t)

	globalSettings.ThinkingModelBlacklist = []string{
		"moonshotai/kimi-k2-thinking",
		"kimi-k2-thinking",
	}

	tests := []struct {
		name      string
		modelName string
		want      bool
	}{
		{"exact match in blacklist", "kimi-k2-thinking", true},
		{"exact match with namespace prefix", "moonshotai/kimi-k2-thinking", true},
		{"match with surrounding whitespace is trimmed", "  kimi-k2-thinking  ", true},
		{"non-matching model", "gpt-4", false},
		{"empty string returns false", "", false},
		{"whitespace-only string returns false", "   ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldPreserveThinkingSuffix(tt.modelName); got != tt.want {
				t.Errorf("ShouldPreserveThinkingSuffix(%q) = %v, want %v", tt.modelName, got, tt.want)
			}
		})
	}
}
