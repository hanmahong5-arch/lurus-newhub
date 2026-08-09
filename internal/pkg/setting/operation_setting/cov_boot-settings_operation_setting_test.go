package operation_setting

import (
	"os"
	"strings"
	"testing"
)

// --- checkin_setting.go ---

func boot_settings_resetCheckinSetting(t *testing.T) {
	t.Helper()
	orig := checkinSetting
	t.Cleanup(func() { checkinSetting = orig })
}

func TestCheckinSetting_Defaults(t *testing.T) {
	boot_settings_resetCheckinSetting(t)
	checkinSetting = CheckinSetting{Enabled: false, MinQuota: 1000, MaxQuota: 10000}

	if IsCheckinEnabled() {
		t.Fatalf("expected check-in disabled by default")
	}
	min, max := GetCheckinQuotaRange()
	if min != 1000 || max != 10000 {
		t.Fatalf("expected default range [1000,10000], got [%d,%d]", min, max)
	}
}

func TestCheckinSetting_EnabledReflectsMutation(t *testing.T) {
	boot_settings_resetCheckinSetting(t)
	GetCheckinSetting().Enabled = true
	if !IsCheckinEnabled() {
		t.Fatalf("expected IsCheckinEnabled to reflect mutation via GetCheckinSetting pointer")
	}
}

// --- general_setting.go ---

func boot_settings_resetGeneralSetting(t *testing.T) {
	t.Helper()
	orig := generalSetting
	t.Cleanup(func() { generalSetting = orig })
}

func TestGeneralSetting_CurrencyDisplayBranches(t *testing.T) {
	boot_settings_resetGeneralSetting(t)

	tests := []struct {
		displayType  string
		wantCurrency bool
		wantCNY      bool
		wantSymbol   string
	}{
		{QuotaDisplayTypeUSD, true, false, "$"},
		{QuotaDisplayTypeCNY, true, true, "¥"},
		{QuotaDisplayTypeTokens, false, false, ""},
		{QuotaDisplayTypeCustom, true, false, "€"},
	}
	for _, tc := range tests {
		t.Run(tc.displayType, func(t *testing.T) {
			generalSetting.QuotaDisplayType = tc.displayType
			generalSetting.CustomCurrencySymbol = "€"
			if got := IsCurrencyDisplay(); got != tc.wantCurrency {
				t.Fatalf("IsCurrencyDisplay() = %v, want %v", got, tc.wantCurrency)
			}
			if got := IsCNYDisplay(); got != tc.wantCNY {
				t.Fatalf("IsCNYDisplay() = %v, want %v", got, tc.wantCNY)
			}
			if got := GetQuotaDisplayType(); got != tc.displayType {
				t.Fatalf("GetQuotaDisplayType() = %v, want %v", got, tc.displayType)
			}
			if got := GetCurrencySymbol(); got != tc.wantSymbol {
				t.Fatalf("GetCurrencySymbol() = %q, want %q", got, tc.wantSymbol)
			}
		})
	}
}

func TestGetCurrencySymbol_CustomEmptySymbolFallsBackToGeneric(t *testing.T) {
	boot_settings_resetGeneralSetting(t)
	generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
	generalSetting.CustomCurrencySymbol = ""

	if got := GetCurrencySymbol(); got != "¤" {
		t.Fatalf("expected generic currency symbol ¤ fallback, got %q", got)
	}
}

func TestGetCurrencySymbol_UnknownTypeReturnsEmpty(t *testing.T) {
	boot_settings_resetGeneralSetting(t)
	generalSetting.QuotaDisplayType = "SOMETHING_UNKNOWN"

	if got := GetCurrencySymbol(); got != "" {
		t.Fatalf("expected empty symbol for unknown display type, got %q", got)
	}
}

func TestGetUsdToCurrencyRate_Branches(t *testing.T) {
	boot_settings_resetGeneralSetting(t)

	generalSetting.QuotaDisplayType = QuotaDisplayTypeUSD
	if got := GetUsdToCurrencyRate(7.3); got != 1 {
		t.Fatalf("USD rate should always be 1, got %v", got)
	}

	generalSetting.QuotaDisplayType = QuotaDisplayTypeCNY
	if got := GetUsdToCurrencyRate(7.3); got != 7.3 {
		t.Fatalf("CNY rate should pass through usdToCny, got %v", got)
	}

	generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
	generalSetting.CustomCurrencyExchangeRate = 2.5
	if got := GetUsdToCurrencyRate(7.3); got != 2.5 {
		t.Fatalf("expected custom rate 2.5, got %v", got)
	}

	// Edge case: a non-positive custom rate must not divide-by-zero or return
	// a negative price downstream — it falls back to 1 (par with USD).
	generalSetting.CustomCurrencyExchangeRate = 0
	if got := GetUsdToCurrencyRate(7.3); got != 1 {
		t.Fatalf("expected non-positive custom rate to fall back to 1, got %v", got)
	}
	generalSetting.CustomCurrencyExchangeRate = -3
	if got := GetUsdToCurrencyRate(7.3); got != 1 {
		t.Fatalf("expected negative custom rate to fall back to 1, got %v", got)
	}

	generalSetting.QuotaDisplayType = QuotaDisplayTypeTokens
	if got := GetUsdToCurrencyRate(7.3); got != 1 {
		t.Fatalf("expected TOKENS (unhandled) branch to default to 1, got %v", got)
	}
}

// --- monitor_setting.go ---

func boot_settings_resetMonitorSetting(t *testing.T) {
	t.Helper()
	orig := monitorSetting
	origEnv, hadEnv := os.LookupEnv("CHANNEL_TEST_FREQUENCY")
	t.Cleanup(func() {
		monitorSetting = orig
		if hadEnv {
			_ = os.Setenv("CHANNEL_TEST_FREQUENCY", origEnv)
		} else {
			_ = os.Unsetenv("CHANNEL_TEST_FREQUENCY")
		}
	})
}

func TestGetMonitorSetting_EnvOverridesEnableAutoTest(t *testing.T) {
	boot_settings_resetMonitorSetting(t)
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}
	_ = os.Setenv("CHANNEL_TEST_FREQUENCY", "30")

	s := GetMonitorSetting()
	if !s.AutoTestChannelEnabled || s.AutoTestChannelMinutes != 30 {
		t.Fatalf("expected env override to enable auto-test at 30 minutes, got %+v", s)
	}
}

func TestGetMonitorSetting_EmptyEnvLeavesDefaults(t *testing.T) {
	boot_settings_resetMonitorSetting(t)
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}
	_ = os.Unsetenv("CHANNEL_TEST_FREQUENCY")

	s := GetMonitorSetting()
	if s.AutoTestChannelEnabled || s.AutoTestChannelMinutes != 10 {
		t.Fatalf("expected defaults preserved when env unset, got %+v", s)
	}
}

func TestGetMonitorSetting_InvalidEnvIgnored(t *testing.T) {
	boot_settings_resetMonitorSetting(t)
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}
	_ = os.Setenv("CHANNEL_TEST_FREQUENCY", "not-a-number")

	s := GetMonitorSetting()
	if s.AutoTestChannelEnabled || s.AutoTestChannelMinutes != 10 {
		t.Fatalf("expected non-numeric env var to be ignored, got %+v", s)
	}
}

// NOTE: CHANNEL_TEST_FREQUENCY=0 (and negative values) used to be silently
// ignored instead of disabling the automatic channel test, so a deployment that
// had ever enabled it could not turn it off through the environment. Covered by
// fix_monitor_setting_test.go, which pins the zero, negative, positive,
// unparsable and unset cases and also asserts the period is left alone when
// disabling.

// --- operation_setting.go ---

func boot_settings_resetAutomaticDisableKeywords(t *testing.T) {
	t.Helper()
	orig := AutomaticDisableKeywords
	t.Cleanup(func() { AutomaticDisableKeywords = orig })
}

func TestAutomaticDisableKeywordsToString(t *testing.T) {
	boot_settings_resetAutomaticDisableKeywords(t)
	AutomaticDisableKeywords = []string{"foo", "bar"}
	if got := AutomaticDisableKeywordsToString(); got != "foo\nbar" {
		t.Fatalf("expected 'foo\\nbar', got %q", got)
	}
}

func TestAutomaticDisableKeywordsFromString_LowercasesAndTrims(t *testing.T) {
	boot_settings_resetAutomaticDisableKeywords(t)
	// Business rule: disable-keyword matching against upstream error text is
	// meant to be case-insensitive, so entries are normalized to lowercase on
	// ingest, and blank/whitespace-only lines are dropped.
	AutomaticDisableKeywordsFromString("Quota Exceeded\n\n  Permission DENIED  \n")
	want := []string{"quota exceeded", "permission denied"}
	if len(AutomaticDisableKeywords) != len(want) {
		t.Fatalf("expected %v, got %v", want, AutomaticDisableKeywords)
	}
	for i := range want {
		if AutomaticDisableKeywords[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, AutomaticDisableKeywords)
		}
	}
}

// --- quota_setting.go ---

func TestGetQuotaSetting_Default(t *testing.T) {
	orig := quotaSetting
	t.Cleanup(func() { quotaSetting = orig })
	quotaSetting = QuotaSetting{EnableFreeModelPreConsume: true}

	if !GetQuotaSetting().EnableFreeModelPreConsume {
		t.Fatalf("expected free-model pre-consume enabled by default")
	}
}

// --- tools.go ---

func TestGetWebSearchPricePerThousand_ModelBranches(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		{"o3-mini", WebSearchPrice},
		{"o4-mini", WebSearchPrice},
		{"gpt-5", WebSearchPrice},
		{"gpt-5-mini", WebSearchPrice},
		{"gpt-4o", WebSearchPriceHigh},
		{"gpt-4.1-mini", WebSearchPriceHigh},
		{"claude-3-opus", WebSearchPriceHigh},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			if got := GetWebSearchPricePerThousand(tc.model, "medium"); got != tc.want {
				t.Fatalf("GetWebSearchPricePerThousand(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

func TestGetFileSearchPricePerThousand(t *testing.T) {
	if got := GetFileSearchPricePerThousand(); got != FileSearchPrice {
		t.Fatalf("expected %v, got %v", FileSearchPrice, got)
	}
}

func TestGetClaudeWebSearchPricePerThousand(t *testing.T) {
	if got := GetClaudeWebSearchPricePerThousand(); got != ClaudeWebSearchPrice {
		t.Fatalf("expected %v, got %v", ClaudeWebSearchPrice, got)
	}
}

func TestGetGeminiInputAudioPricePerMillionTokens_PrefixPriorityOrder(t *testing.T) {
	// The prefix checks are ordered most-specific-first; a naive
	// alphabetical/unordered check could misroute e.g. the "-lite" variant
	// into the generic "-preview" bucket. This locks the intended precedence.
	tests := []struct {
		model string
		want  float64
	}{
		{"gemini-2.5-flash-preview-native-audio-dialog", Gemini25FlashNativeAudioInputAudioPrice},
		{"gemini-2.5-flash-preview-lite-05-20", Gemini25FlashLitePreviewInputAudioPrice},
		{"gemini-2.5-flash-preview-05-20", Gemini25FlashPreviewInputAudioPrice},
		{"gemini-2.5-flash", Gemini25FlashProductionInputAudioPrice},
		{"gemini-2.0-flash-001", Gemini20FlashInputAudioPrice},
		{"gemini-robotics-er-1.5-preview", GeminiRoboticsER15InputAudioPrice},
		{"gpt-4o", 0}, // unrelated model family: no audio price
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			if got := GetGeminiInputAudioPricePerMillionTokens(tc.model); got != tc.want {
				t.Fatalf("GetGeminiInputAudioPricePerMillionTokens(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

func TestGetGPTImage1PriceOnceCall(t *testing.T) {
	tests := []struct {
		quality string
		size    string
		want    float64
	}{
		{"low", "1024x1024", GPTImage1Low1024x1024},
		{"medium", "1024x1536", GPTImage1Medium1024x1536},
		{"high", "1536x1024", GPTImage1High1536x1024},
		// Edge case: unknown quality/size combos fall back to the highest
		// price tier rather than erroring or returning 0 (fail-safe billing:
		// never undercharge for an unrecognized image spec).
		{"ultra", "1024x1024", GPTImage1High1024x1024},
		{"low", "2048x2048", GPTImage1High1024x1024},
	}
	for _, tc := range tests {
		t.Run(tc.quality+"_"+tc.size, func(t *testing.T) {
			if got := GetGPTImage1PriceOnceCall(tc.quality, tc.size); got != tc.want {
				t.Fatalf("GetGPTImage1PriceOnceCall(%q,%q) = %v, want %v", tc.quality, tc.size, got, tc.want)
			}
		})
	}
}

func TestAutomaticDisableKeywordsFromString_AllBlankProducesEmptySlice(t *testing.T) {
	boot_settings_resetAutomaticDisableKeywords(t)
	AutomaticDisableKeywordsFromString("   \n\n  ")
	if len(AutomaticDisableKeywords) != 0 {
		t.Fatalf("expected zero keywords for all-blank input, got %v", AutomaticDisableKeywords)
	}
}

func TestOperationSettingModuleVars_Defaults(t *testing.T) {
	// Guard against an accidental flip of these module-wide feature flags,
	// which gate demo-mode restrictions and self-hosting cost markup.
	if DemoSiteEnabled {
		t.Fatalf("expected DemoSiteEnabled=false by default")
	}
	if SelfUseModeEnabled {
		t.Fatalf("expected SelfUseModeEnabled=false by default")
	}
	if ModelFallbackMarkup != 1.25 {
		t.Fatalf("expected ModelFallbackMarkup=1.25 by default, got %v", ModelFallbackMarkup)
	}
	if !strings.Contains(strings.Join(AutomaticDisableKeywords, "|"), "credit balance") {
		t.Fatalf("expected default AutomaticDisableKeywords to include a credit-balance keyword, got %v", AutomaticDisableKeywords)
	}
}
