package constant

import (
	"fmt"
	"testing"
)

// TestGetChannelTypeName covers the known/unknown lookup branches.
func TestGetChannelTypeName(t *testing.T) {
	cases := []struct {
		name        string
		channelType int
		want        string
	}{
		{"openai", ChannelTypeOpenAI, "OpenAI"},
		{"anthropic", ChannelTypeAnthropic, "Anthropic"},
		{"deepseek", ChannelTypeDeepSeek, "DeepSeek"},
		{"replicate", ChannelTypeReplicate, "Replicate"},
		{"zero_unknown", ChannelTypeUnknown, "Unknown"},
		{"negative_unmapped", -1, "Unknown"},
		{"large_unmapped", 99999, "Unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := GetChannelTypeName(c.channelType)
			if got != c.want {
				t.Errorf("GetChannelTypeName(%d) = %q, want %q", c.channelType, got, c.want)
			}
		})
	}
}

// TestChannelBaseURLsIndexAlignment verifies the base-URL slice is indexed
// consistently with the channel-type constants it documents (spot checks).
func TestChannelBaseURLsIndexAlignment(t *testing.T) {
	cases := []struct {
		channelType int
		want        string
	}{
		{ChannelTypeUnknown, ""},
		{ChannelTypeOpenAI, "https://api.openai.com"},
		{ChannelTypeAnthropic, "https://api.anthropic.com"},
		{ChannelTypeGemini, "https://generativelanguage.googleapis.com"},
		{ChannelTypeDeepSeek, "https://api.deepseek.com"},
		{ChannelTypeReplicate, "https://api.replicate.com"},
	}
	for _, c := range cases {
		if c.channelType >= len(ChannelBaseURLs) {
			t.Fatalf("channelType %d out of range of ChannelBaseURLs (len %d)", c.channelType, len(ChannelBaseURLs))
		}
		got := ChannelBaseURLs[c.channelType]
		if got != c.want {
			t.Errorf("ChannelBaseURLs[%d] = %q, want %q", c.channelType, got, c.want)
		}
	}
}

// TestChannelSpecialBases asserts the exact pairing of Claude/OpenAI base
// URLs for each special-cased coding-plan key.
func TestChannelSpecialBases(t *testing.T) {
	cases := []struct {
		key           string
		claudeBaseURL string
		openAIBaseURL string
	}{
		{"glm-coding-plan", "https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn/api/coding/paas/v4"},
		{"glm-coding-plan-international", "https://api.z.ai/api/anthropic", "https://api.z.ai/api/coding/paas/v4"},
		{"kimi-coding-plan", "https://api.kimi.com/coding", "https://api.kimi.com/coding/v1"},
		{"doubao-coding-plan", "https://ark.cn-beijing.volces.com/api/coding", "https://ark.cn-beijing.volces.com/api/coding/v3"},
	}
	for _, c := range cases {
		got, ok := ChannelSpecialBases[c.key]
		if !ok {
			t.Fatalf("ChannelSpecialBases missing key %q", c.key)
		}
		if got.ClaudeBaseURL != c.claudeBaseURL || got.OpenAIBaseURL != c.openAIBaseURL {
			t.Errorf("ChannelSpecialBases[%q] = %+v, want {%q %q}", c.key, got, c.claudeBaseURL, c.openAIBaseURL)
		}
	}
	if _, ok := ChannelSpecialBases["does-not-exist"]; ok {
		t.Error("ChannelSpecialBases[\"does-not-exist\"] unexpectedly found")
	}
}

// TestSetupAccessors exercises IsSetup/SetSetup/TryClaimSetup, restoring the
// package-level atomic to its original value afterward so other tests in
// this package (and -count>1 reruns) are unaffected.
func TestSetupAccessors(t *testing.T) {
	original := IsSetup()
	t.Cleanup(func() {
		SetSetup(original)
	})

	SetSetup(false)
	if IsSetup() {
		t.Fatal("IsSetup() = true after SetSetup(false)")
	}

	// First claim from false should win.
	if !TryClaimSetup() {
		t.Fatal("TryClaimSetup() = false on first claim, want true")
	}
	if !IsSetup() {
		t.Fatal("IsSetup() = false after successful TryClaimSetup")
	}

	// Second claim should lose since setup is already true.
	if TryClaimSetup() {
		t.Fatal("TryClaimSetup() = true on second claim, want false")
	}

	SetSetup(true)
	if !IsSetup() {
		t.Fatal("IsSetup() = false after SetSetup(true)")
	}

	SetSetup(false)
	if IsSetup() {
		t.Fatal("IsSetup() = true after SetSetup(false)")
	}
}

// TestMidjourneyModel2Action asserts the model->action string mapping used
// for MJ task dispatch.
func TestMidjourneyModel2Action(t *testing.T) {
	cases := map[string]string{
		"mj_imagine":        MjActionImagine,
		"mj_describe":       MjActionDescribe,
		"mj_blend":          MjActionBlend,
		"mj_upscale":        MjActionUpscale,
		"mj_variation":      MjActionVariation,
		"mj_reroll":         MjActionReRoll,
		"mj_modal":          MjActionModal,
		"mj_inpaint":        MjActionInPaint,
		"mj_zoom":           MjActionZoom,
		"mj_custom_zoom":    MjActionCustomZoom,
		"mj_shorten":        MjActionShorten,
		"mj_high_variation": MjActionHighVariation,
		"mj_low_variation":  MjActionLowVariation,
		"mj_pan":            MjActionPan,
		"swap_face":         MjActionSwapFace,
		"mj_upload":         MjActionUpload,
		"mj_video":          MjActionVideo,
		"mj_edits":          MjActionEdits,
	}
	if len(MidjourneyModel2Action) != len(cases) {
		t.Fatalf("MidjourneyModel2Action has %d entries, want %d", len(MidjourneyModel2Action), len(cases))
	}
	for model, want := range cases {
		got, ok := MidjourneyModel2Action[model]
		if !ok {
			t.Errorf("MidjourneyModel2Action missing key %q", model)
			continue
		}
		if got != want {
			t.Errorf("MidjourneyModel2Action[%q] = %q, want %q", model, got, want)
		}
	}
}

// TestSunoModel2Action asserts the Suno model->action string mapping.
func TestSunoModel2Action(t *testing.T) {
	want := map[string]string{
		"suno_music":  SunoActionMusic,
		"suno_lyrics": SunoActionLyrics,
	}
	if len(SunoModel2Action) != len(want) {
		t.Fatalf("SunoModel2Action has %d entries, want %d", len(SunoModel2Action), len(want))
	}
	for model, action := range want {
		got, ok := SunoModel2Action[model]
		if !ok {
			t.Errorf("SunoModel2Action missing key %q", model)
			continue
		}
		if got != action {
			t.Errorf("SunoModel2Action[%q] = %q, want %q", model, got, action)
		}
	}
}

// TestContextKeyValues locks in the exact string values used as context map
// keys, since a stray rename here would silently break value propagation.
func TestContextKeyValues(t *testing.T) {
	cases := map[ContextKey]string{
		ContextKeyTokenCountMeta:           "token_count_meta",
		ContextKeyPromptTokens:             "prompt_tokens",
		ContextKeyEstimatedTokens:          "estimated_tokens",
		ContextKeyOriginalModel:            "original_model",
		ContextKeyRequestStartTime:         "request_start_time",
		ContextKeyTokenUnlimited:           "token_unlimited_quota",
		ContextKeyTokenKey:                 "token_key",
		ContextKeyTokenId:                  "token_id",
		ContextKeyTokenGroup:               "token_group",
		ContextKeyTokenSpecificChannelId:   "specific_channel_id",
		ContextKeyTokenModelLimitEnabled:   "token_model_limit_enabled",
		ContextKeyTokenModelLimit:          "token_model_limit",
		ContextKeyTokenCrossGroupRetry:     "token_cross_group_retry",
		ContextKeyChannelId:                "channel_id",
		ContextKeyChannelName:              "channel_name",
		ContextKeyChannelCreateTime:        "channel_create_time",
		ContextKeyChannelBaseUrl:           "base_url",
		ContextKeyChannelType:              "channel_type",
		ContextKeyChannelSetting:           "channel_setting",
		ContextKeyChannelOtherSetting:      "channel_other_setting",
		ContextKeyChannelParamOverride:     "param_override",
		ContextKeyChannelHeaderOverride:    "header_override",
		ContextKeyChannelOrganization:      "channel_organization",
		ContextKeyChannelAutoBan:           "auto_ban",
		ContextKeyChannelModelMapping:      "model_mapping",
		ContextKeyChannelStatusCodeMapping: "status_code_mapping",
		ContextKeyChannelIsMultiKey:        "channel_is_multi_key",
		ContextKeyChannelMultiKeyIndex:     "channel_multi_key_index",
		ContextKeyChannelKey:               "channel_key",
		ContextKeyAutoGroup:                "auto_group",
		ContextKeyAutoGroupIndex:           "auto_group_index",
		ContextKeyAutoGroupRetryIndex:      "auto_group_retry_index",
		ContextKeyUserId:                   "id",
		ContextKeyUserSetting:              "user_setting",
		ContextKeyUserQuota:                "user_quota",
		ContextKeyUserStatus:               "user_status",
		ContextKeyUserEmail:                "user_email",
		ContextKeyUserGroup:                "user_group",
		ContextKeyUsingGroup:               "group",
		ContextKeyUserName:                 "username",
		ContextKeyLocalCountTokens:         "local_count_tokens",
		ContextKeySystemPromptOverride:     "system_prompt_override",
	}
	for key, want := range cases {
		if string(key) != want {
			t.Errorf("ContextKey %v = %q, want %q", key, string(key), want)
		}
	}
}

// TestEndpointTypeValues locks in the exact string values used for endpoint
// dispatch/config comparisons.
func TestEndpointTypeValues(t *testing.T) {
	cases := map[EndpointType]string{
		EndpointTypeOpenAI:          "openai",
		EndpointTypeOpenAIResponse:  "openai-response",
		EndpointTypeAnthropic:       "anthropic",
		EndpointTypeGemini:          "gemini",
		EndpointTypeJinaRerank:      "jina-rerank",
		EndpointTypeImageGeneration: "image-generation",
		EndpointTypeEmbeddings:      "embeddings",
		EndpointTypeOpenAIVideo:     "openai-video",
	}
	for key, want := range cases {
		if string(key) != want {
			t.Errorf("EndpointType %v = %q, want %q", key, string(key), want)
		}
	}
}

// TestMultiKeyModeValues locks in the exact string values for multi-key
// selection mode.
func TestMultiKeyModeValues(t *testing.T) {
	if MultiKeyModeRandom != "random" {
		t.Errorf("MultiKeyModeRandom = %q, want %q", MultiKeyModeRandom, "random")
	}
	if MultiKeyModePolling != "polling" {
		t.Errorf("MultiKeyModePolling = %q, want %q", MultiKeyModePolling, "polling")
	}
}

// TestTaskPlatformAndActionValues locks in the exact string values used for
// task platform/action dispatch.
func TestTaskPlatformAndActionValues(t *testing.T) {
	if TaskPlatformSuno != "suno" {
		t.Errorf("TaskPlatformSuno = %q, want %q", TaskPlatformSuno, "suno")
	}
	if TaskPlatformMusic != "music" {
		t.Errorf("TaskPlatformMusic = %q, want %q", TaskPlatformMusic, "music")
	}
	if TaskPlatformMidjourney != "mj" {
		t.Errorf("TaskPlatformMidjourney = %q, want %q", TaskPlatformMidjourney, "mj")
	}

	actions := map[string]string{
		"SunoActionMusic":             SunoActionMusic,
		"SunoActionLyrics":            SunoActionLyrics,
		"MusicActionGenerate":         MusicActionGenerate,
		"TaskActionGenerate":          TaskActionGenerate,
		"TaskActionTextGenerate":      TaskActionTextGenerate,
		"TaskActionFirstTailGenerate": TaskActionFirstTailGenerate,
		"TaskActionReferenceGenerate": TaskActionReferenceGenerate,
		"TaskActionRemix":             TaskActionRemix,
	}
	want := map[string]string{
		"SunoActionMusic":             "MUSIC",
		"SunoActionLyrics":            "LYRICS",
		"MusicActionGenerate":         "GENERATE",
		"TaskActionGenerate":          "generate",
		"TaskActionTextGenerate":      "textGenerate",
		"TaskActionFirstTailGenerate": "firstTailGenerate",
		"TaskActionReferenceGenerate": "referenceGenerate",
		"TaskActionRemix":             "remixGenerate",
	}
	for name, got := range actions {
		if got != want[name] {
			t.Errorf("%s = %q, want %q", name, got, want[name])
		}
	}
}

// TestFinishReasonValues locks in the exact finish-reason strings.
func TestFinishReasonValues(t *testing.T) {
	cases := map[string]string{
		FinishReasonStop:          "stop",
		FinishReasonToolCalls:     "tool_calls",
		FinishReasonLength:        "length",
		FinishReasonFunctionCall:  "function_call",
		FinishReasonContentFilter: "content_filter",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("finish reason value = %q, want %q", got, want)
		}
	}
}

// TestCacheKeyFormats verifies the printf-style cache key format strings
// produce the exact expected keys when applied.
func TestCacheKeyFormats(t *testing.T) {
	type fmtCase struct {
		format string
		id     int
		want   string
	}
	for _, c := range []fmtCase{
		{UserGroupKeyFmt, 42, "user_group:42"},
		{UserQuotaKeyFmt, 42, "user_quota:42"},
		{UserEnabledKeyFmt, 42, "user_enabled:42"},
		{UserUsernameKeyFmt, 42, "user_name:42"},
	} {
		got := fmt.Sprintf(c.format, c.id)
		if got != c.want {
			t.Errorf("fmt.Sprintf(%q, %d) = %q, want %q", c.format, c.id, got, c.want)
		}
	}

	if TokenFiledRemainQuota != "RemainQuota" {
		t.Errorf("TokenFiledRemainQuota = %q, want %q", TokenFiledRemainQuota, "RemainQuota")
	}
	if TokenFieldGroup != "Group" {
		t.Errorf("TokenFieldGroup = %q, want %q", TokenFieldGroup, "Group")
	}
}

// TestMidjourneyErrorAndActionConstants locks in the numeric/string
// midjourney constants used for error classification and action dispatch.
func TestMidjourneyErrorAndActionConstants(t *testing.T) {
	if MjErrorUnknown != 5 {
		t.Errorf("MjErrorUnknown = %d, want 5", MjErrorUnknown)
	}
	if MjRequestError != 4 {
		t.Errorf("MjRequestError = %d, want 4", MjRequestError)
	}
	if MjActionImagine != "IMAGINE" {
		t.Errorf("MjActionImagine = %q, want %q", MjActionImagine, "IMAGINE")
	}
	if MjActionEdits != "EDITS" {
		t.Errorf("MjActionEdits = %q, want %q", MjActionEdits, "EDITS")
	}
}

// TestAzureNoRemoveDotTime asserts the exact epoch value derived from the
// hard-coded cutover date.
func TestAzureNoRemoveDotTime(t *testing.T) {
	const want = int64(1746835200) // 2025-05-10T00:00:00Z
	if AzureNoRemoveDotTime != want {
		t.Errorf("AzureNoRemoveDotTime = %d, want %d", AzureNoRemoveDotTime, want)
	}
}
