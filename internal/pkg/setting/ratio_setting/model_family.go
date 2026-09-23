package ratio_setting

import "strings"

// familyRule defines a model family matching rule.
// prefix must match, and if tier is non-empty it must appear in the name.
type familyRule struct {
	prefix string
	tier   string
	ratio  float64
}

// modelFamilyRules is ordered from most specific to least specific.
// When a model is not in the explicit ratio map, these rules provide
// a reasonable base ratio derived from the family's known pricing.
//
// Base ratio unit: 1 = $0.002 / 1K tokens (same as defaultModelRatio).
var modelFamilyRules = []familyRule{
	// --- Gemini ---
	{"gemini-", "flash-lite", 0.05},
	{"gemini-", "flash-image", 0.075},
	{"gemini-", "flash", 0.075},
	{"gemini-", "pro-image", 0.625},
	{"gemini-", "pro", 0.625},
	{"gemini-", "embedding", 0.075},
	{"gemini-", "", 0.075}, // fallback for any unknown gemini tier

	// --- Claude ---
	{"claude-", "haiku", 0.5},
	{"claude-", "sonnet", 1.5},
	{"claude-", "opus", 7.5},
	{"claude-", "", 1.5},

	// --- GPT ---
	{"gpt-", "nano", 0.05},
	{"gpt-", "mini", 0.2},
	{"gpt-", "turbo", 5.0},
	{"gpt-", "", 1.25},

	// --- O-series (o1, o3, o4 ...) ---
	{"o1-", "pro", 75.0},
	{"o1-", "mini", 0.55},
	{"o1-", "", 7.5},
	{"o3-", "pro", 10.0},
	{"o3-", "mini", 0.55},
	{"o3-", "", 1.0},
	{"o4-", "mini", 0.55},
	{"o4-", "", 1.0},

	// --- DeepSeek ---
	// (list prices 2026-09-23; see defaultModelRatio — every non-pro name
	// DeepSeek accepts is answered by deepseek-flash)
	{"deepseek-", "pro", 0.66},
	{"deepseek-", "chat", 0.15},
	{"deepseek-", "coder", 0.15},
	{"deepseek-", "reasoner", 0.15},
	{"deepseek-", "", 0.15},

	// --- Qwen ---
	{"qwen-", "turbo", 0.86},
	{"qwen-", "plus", 10.0},
	{"qwen-", "max", 10.0},
	{"qwen-", "", 0.86},

	// --- GLM ---
	{"glm-", "4-flash", 0.0},
	{"glm-", "4-air", 0.07},
	{"glm-", "4", 3.57},
	{"glm-", "", 0.36},

	// --- Moonshot ---
	{"moonshot-", "", 0.86},

	// --- Doubao ---
	{"doubao-", "lite", 0.014},
	{"doubao-", "pro", 0.057},
	{"doubao-", "", 0.057},
}

// GetModelFamilyName returns the matched family name for a model (e.g. "gemini-flash-lite"),
// or empty string if no family matched.
func GetModelFamilyName(name string) string {
	lower := strings.ToLower(name)
	for _, rule := range modelFamilyRules {
		if !strings.HasPrefix(lower, rule.prefix) {
			continue
		}
		if rule.tier == "" {
			return strings.TrimSuffix(rule.prefix, "-")
		}
		rest := lower[len(rule.prefix):]
		if strings.Contains(rest, rule.tier) {
			return strings.TrimSuffix(rule.prefix, "-") + "-" + rule.tier
		}
	}
	return ""
}

// ModelFamilyFallback returns a base ratio for models not in the explicit ratio map.
// It matches by provider prefix + tier keyword in the model name.
// Returns (baseRatio, true) if matched, (0, false) if no family recognized.
func ModelFamilyFallback(name string) (float64, bool) {
	lower := strings.ToLower(name)
	for _, rule := range modelFamilyRules {
		if !strings.HasPrefix(lower, rule.prefix) {
			continue
		}
		// Prefix matched. If tier is empty, it's the catch-all for this prefix.
		if rule.tier == "" {
			return rule.ratio, true
		}
		// Check if tier keyword appears in the name (after prefix)
		rest := lower[len(rule.prefix):]
		if strings.Contains(rest, rule.tier) {
			return rule.ratio, true
		}
	}
	return 0, false
}
