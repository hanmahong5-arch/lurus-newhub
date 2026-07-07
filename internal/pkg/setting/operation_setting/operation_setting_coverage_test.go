package operation_setting

import (
	"os"
	"testing"
)

// --- checkin_setting.go ---

func TestGetCheckinSetting_Coverage(t *testing.T) {
	orig := checkinSetting
	t.Cleanup(func() { checkinSetting = orig })

	checkinSetting = CheckinSetting{Enabled: true, MinQuota: 500, MaxQuota: 5000}

	got := GetCheckinSetting()
	if got.Enabled != true || got.MinQuota != 500 || got.MaxQuota != 5000 {
		t.Fatalf("GetCheckinSetting() = %+v, want Enabled=true MinQuota=500 MaxQuota=5000", got)
	}
	// verify pointer aliases the package-level var (not a copy)
	got.MaxQuota = 9999
	if checkinSetting.MaxQuota != 9999 {
		t.Fatalf("GetCheckinSetting() did not return a pointer to the live setting")
	}
}

func TestIsCheckinEnabled_Coverage(t *testing.T) {
	orig := checkinSetting
	t.Cleanup(func() { checkinSetting = orig })

	checkinSetting.Enabled = true
	if !IsCheckinEnabled() {
		t.Fatalf("IsCheckinEnabled() = false, want true when Enabled=true")
	}
	checkinSetting.Enabled = false
	if IsCheckinEnabled() {
		t.Fatalf("IsCheckinEnabled() = true, want false when Enabled=false")
	}
}

func TestGetCheckinQuotaRange_Coverage(t *testing.T) {
	orig := checkinSetting
	t.Cleanup(func() { checkinSetting = orig })

	checkinSetting.MinQuota = 111
	checkinSetting.MaxQuota = 222
	min, max := GetCheckinQuotaRange()
	if min != 111 || max != 222 {
		t.Fatalf("GetCheckinQuotaRange() = (%d, %d), want (111, 222)", min, max)
	}
}

// --- general_setting.go ---

func snapshotGeneralSetting(t *testing.T) {
	t.Helper()
	orig := generalSetting
	t.Cleanup(func() { generalSetting = orig })
}

func TestIsCurrencyDisplay_Coverage(t *testing.T) {
	snapshotGeneralSetting(t)

	generalSetting.QuotaDisplayType = QuotaDisplayTypeTokens
	if IsCurrencyDisplay() {
		t.Fatalf("IsCurrencyDisplay() = true for TOKENS, want false")
	}
	for _, dt := range []string{QuotaDisplayTypeUSD, QuotaDisplayTypeCNY, QuotaDisplayTypeCustom, QuotaDisplayTypeLute, "unknown"} {
		generalSetting.QuotaDisplayType = dt
		if !IsCurrencyDisplay() {
			t.Fatalf("IsCurrencyDisplay() = false for %s, want true", dt)
		}
	}
}

func TestIsCNYDisplay_Coverage(t *testing.T) {
	snapshotGeneralSetting(t)

	generalSetting.QuotaDisplayType = QuotaDisplayTypeCNY
	if !IsCNYDisplay() {
		t.Fatalf("IsCNYDisplay() = false for CNY, want true")
	}
	generalSetting.QuotaDisplayType = QuotaDisplayTypeUSD
	if IsCNYDisplay() {
		t.Fatalf("IsCNYDisplay() = true for USD, want false")
	}
}

func TestGetQuotaDisplayType_Coverage(t *testing.T) {
	snapshotGeneralSetting(t)

	generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
	if got := GetQuotaDisplayType(); got != QuotaDisplayTypeCustom {
		t.Fatalf("GetQuotaDisplayType() = %q, want %q", got, QuotaDisplayTypeCustom)
	}
}

func TestGetCurrencySymbol_Coverage(t *testing.T) {
	snapshotGeneralSetting(t)

	tests := []struct {
		displayType string
		customSym   string
		want        string
	}{
		{QuotaDisplayTypeUSD, "", "$"},
		{QuotaDisplayTypeCNY, "", "¥"},
		{QuotaDisplayTypeCustom, "€", "€"},
		{QuotaDisplayTypeCustom, "", "¤"},
		{QuotaDisplayTypeTokens, "", ""},
		{QuotaDisplayTypeLute, "", ""},
		{"unknown", "", ""},
	}
	for _, tc := range tests {
		generalSetting.QuotaDisplayType = tc.displayType
		generalSetting.CustomCurrencySymbol = tc.customSym
		if got := GetCurrencySymbol(); got != tc.want {
			t.Fatalf("GetCurrencySymbol() with type=%q custom=%q = %q, want %q", tc.displayType, tc.customSym, got, tc.want)
		}
	}
}

func TestGetUsdToCurrencyRate_Coverage(t *testing.T) {
	snapshotGeneralSetting(t)

	tests := []struct {
		displayType string
		customRate  float64
		usdToCny    float64
		want        float64
	}{
		{QuotaDisplayTypeUSD, 0, 7.3, 1},
		{QuotaDisplayTypeCNY, 0, 7.3, 7.3},
		{QuotaDisplayTypeCustom, 3.5, 7.3, 3.5},
		{QuotaDisplayTypeCustom, 0, 7.3, 1},
		{QuotaDisplayTypeCustom, -1, 7.3, 1},
		{QuotaDisplayTypeTokens, 0, 7.3, 1},
		{"unknown", 0, 7.3, 1},
	}
	for _, tc := range tests {
		generalSetting.QuotaDisplayType = tc.displayType
		generalSetting.CustomCurrencyExchangeRate = tc.customRate
		if got := GetUsdToCurrencyRate(tc.usdToCny); got != tc.want {
			t.Fatalf("GetUsdToCurrencyRate(%v) with type=%q rate=%v = %v, want %v", tc.usdToCny, tc.displayType, tc.customRate, got, tc.want)
		}
	}
}

func TestGetGeneralSetting_Coverage(t *testing.T) {
	snapshotGeneralSetting(t)

	generalSetting.DocsLink = "https://example.com/docs"
	got := GetGeneralSetting()
	if got.DocsLink != "https://example.com/docs" {
		t.Fatalf("GetGeneralSetting().DocsLink = %q, want %q", got.DocsLink, "https://example.com/docs")
	}
	got.PingIntervalSeconds = 42
	if generalSetting.PingIntervalSeconds != 42 {
		t.Fatalf("GetGeneralSetting() did not return a pointer to the live setting")
	}
}

// --- monitor_setting.go ---

func TestGetMonitorSetting_NoEnv_Coverage(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	os.Unsetenv("CHANNEL_TEST_FREQUENCY")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}

	got := GetMonitorSetting()
	if got.AutoTestChannelEnabled != false || got.AutoTestChannelMinutes != 10 {
		t.Fatalf("GetMonitorSetting() = %+v, want unchanged defaults when env unset", got)
	}
}

func TestGetMonitorSetting_ValidEnv_Coverage(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	t.Setenv("CHANNEL_TEST_FREQUENCY", "30")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}

	got := GetMonitorSetting()
	if !got.AutoTestChannelEnabled || got.AutoTestChannelMinutes != 30 {
		t.Fatalf("GetMonitorSetting() = %+v, want Enabled=true Minutes=30", got)
	}
}

func TestGetMonitorSetting_InvalidEnv_Coverage(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	t.Setenv("CHANNEL_TEST_FREQUENCY", "not-a-number")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}

	got := GetMonitorSetting()
	if got.AutoTestChannelEnabled != false || got.AutoTestChannelMinutes != 10 {
		t.Fatalf("GetMonitorSetting() = %+v, want unchanged when env unparsable", got)
	}
}

func TestGetMonitorSetting_ZeroOrNegativeEnv_Coverage(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	for _, v := range []string{"0", "-5"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("CHANNEL_TEST_FREQUENCY", v)
			monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}
			got := GetMonitorSetting()
			if got.AutoTestChannelEnabled != false || got.AutoTestChannelMinutes != 10 {
				t.Fatalf("GetMonitorSetting() with env=%q = %+v, want unchanged (frequency must be > 0)", v, got)
			}
		})
	}
}

// --- quota_setting.go ---

func TestGetQuotaSetting_Coverage(t *testing.T) {
	orig := quotaSetting
	t.Cleanup(func() { quotaSetting = orig })

	quotaSetting.EnableFreeModelPreConsume = false
	got := GetQuotaSetting()
	if got.EnableFreeModelPreConsume != false {
		t.Fatalf("GetQuotaSetting().EnableFreeModelPreConsume = true, want false")
	}
	got.EnableFreeModelPreConsume = true
	if !quotaSetting.EnableFreeModelPreConsume {
		t.Fatalf("GetQuotaSetting() did not return a pointer to the live setting")
	}
}

// --- tools.go ---

func TestAutomaticDisableKeywordsToString_Coverage(t *testing.T) {
	orig := AutomaticDisableKeywords
	t.Cleanup(func() { AutomaticDisableKeywords = orig })

	AutomaticDisableKeywords = []string{"foo", "bar", "baz"}
	want := "foo\nbar\nbaz"
	if got := AutomaticDisableKeywordsToString(); got != want {
		t.Fatalf("AutomaticDisableKeywordsToString() = %q, want %q", got, want)
	}
}

func TestAutomaticDisableKeywordsFromString_Coverage(t *testing.T) {
	orig := AutomaticDisableKeywords
	t.Cleanup(func() { AutomaticDisableKeywords = orig })

	AutomaticDisableKeywordsFromString("Foo\n  Bar  \n\nBAZ\n   \n")
	want := []string{"foo", "bar", "baz"}
	if len(AutomaticDisableKeywords) != len(want) {
		t.Fatalf("AutomaticDisableKeywordsFromString() len = %d, want %d (got %v)", len(AutomaticDisableKeywords), len(want), AutomaticDisableKeywords)
	}
	for i, w := range want {
		if AutomaticDisableKeywords[i] != w {
			t.Fatalf("AutomaticDisableKeywords[%d] = %q, want %q", i, AutomaticDisableKeywords[i], w)
		}
	}
}

func TestAutomaticDisableKeywordsFromString_Empty_Coverage(t *testing.T) {
	orig := AutomaticDisableKeywords
	t.Cleanup(func() { AutomaticDisableKeywords = orig })

	AutomaticDisableKeywords = []string{"stale"}
	AutomaticDisableKeywordsFromString("")
	if len(AutomaticDisableKeywords) != 0 {
		t.Fatalf("AutomaticDisableKeywordsFromString(\"\") left %d entries, want 0 (got %v)", len(AutomaticDisableKeywords), AutomaticDisableKeywords)
	}
}

// --- operation_setting.go (pricing helpers) ---

func TestGetClaudeWebSearchPricePerThousand_Coverage(t *testing.T) {
	if got := GetClaudeWebSearchPricePerThousand(); got != ClaudeWebSearchPrice {
		t.Fatalf("GetClaudeWebSearchPricePerThousand() = %v, want %v", got, ClaudeWebSearchPrice)
	}
}

func TestGetWebSearchPricePerThousand_Coverage(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		{"o3-mini", WebSearchPrice},
		{"o4-mini", WebSearchPrice},
		{"gpt-5", WebSearchPrice},
		{"gpt-5-mini", WebSearchPrice},
		{"gpt-4o", WebSearchPriceHigh},
		{"gpt-4.1", WebSearchPriceHigh},
		{"gpt-4o-mini", WebSearchPriceHigh},
		{"claude-3-opus", WebSearchPriceHigh},
	}
	for _, tc := range tests {
		if got := GetWebSearchPricePerThousand(tc.model, "medium"); got != tc.want {
			t.Fatalf("GetWebSearchPricePerThousand(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestGetFileSearchPricePerThousand_Coverage(t *testing.T) {
	if got := GetFileSearchPricePerThousand(); got != FileSearchPrice {
		t.Fatalf("GetFileSearchPricePerThousand() = %v, want %v", got, FileSearchPrice)
	}
}

func TestGetGeminiInputAudioPricePerMillionTokens_Coverage(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		{"gemini-2.5-flash-preview-native-audio", Gemini25FlashNativeAudioInputAudioPrice},
		{"gemini-2.5-flash-preview-lite-something", Gemini25FlashLitePreviewInputAudioPrice},
		{"gemini-2.5-flash-preview", Gemini25FlashPreviewInputAudioPrice},
		{"gemini-2.5-flash", Gemini25FlashProductionInputAudioPrice},
		{"gemini-2.0-flash", Gemini20FlashInputAudioPrice},
		{"gemini-robotics-er-1.5", GeminiRoboticsER15InputAudioPrice},
		{"gemini-1.0-pro", 0},
		{"unrelated-model", 0},
	}
	for _, tc := range tests {
		if got := GetGeminiInputAudioPricePerMillionTokens(tc.model); got != tc.want {
			t.Fatalf("GetGeminiInputAudioPricePerMillionTokens(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestGetGPTImage1PriceOnceCall_Coverage(t *testing.T) {
	tests := []struct {
		quality string
		size    string
		want    float64
	}{
		{"low", "1024x1024", GPTImage1Low1024x1024},
		{"low", "1024x1536", GPTImage1Low1024x1536},
		{"low", "1536x1024", GPTImage1Low1536x1024},
		{"medium", "1024x1024", GPTImage1Medium1024x1024},
		{"medium", "1024x1536", GPTImage1Medium1024x1536},
		{"medium", "1536x1024", GPTImage1Medium1536x1024},
		{"high", "1024x1024", GPTImage1High1024x1024},
		{"high", "1024x1536", GPTImage1High1024x1536},
		{"high", "1536x1024", GPTImage1High1536x1024},
		{"unknown-quality", "1024x1024", GPTImage1High1024x1024},
		{"low", "unknown-size", GPTImage1High1024x1024},
		{"", "", GPTImage1High1024x1024},
	}
	for _, tc := range tests {
		if got := GetGPTImage1PriceOnceCall(tc.quality, tc.size); got != tc.want {
			t.Fatalf("GetGPTImage1PriceOnceCall(%q, %q) = %v, want %v", tc.quality, tc.size, got, tc.want)
		}
	}
}
