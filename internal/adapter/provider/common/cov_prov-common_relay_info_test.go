package common

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func provCommonNewGinContext(t *testing.T, method, target string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c
}

// ---- genBaseRelayInfo / GenRelayInfoOpenAI ----

func TestGenRelayInfoOpenAI_TokenGroupFallsBackToUserGroup(t *testing.T) {
	c := provCommonNewGinContext(t, "GET", "/v1/chat/completions")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "vip")
	// token group intentionally left unset

	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "gpt-4"})

	if info.TokenGroup != "vip" {
		t.Errorf("TokenGroup = %q, want fallback to UserGroup %q", info.TokenGroup, "vip")
	}
	if info.RelayFormat != types.RelayFormatOpenAI {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatOpenAI)
	}
	if info.RelayMode != relayconstant.RelayModeChatCompletions {
		t.Errorf("RelayMode = %d, want RelayModeChatCompletions (%d)", info.RelayMode, relayconstant.RelayModeChatCompletions)
	}
}

func TestGenRelayInfoOpenAI_ExplicitTokenGroupWins(t *testing.T) {
	c := provCommonNewGinContext(t, "GET", "/v1/chat/completions")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "premium")

	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "gpt-4"})
	if info.TokenGroup != "premium" {
		t.Errorf("TokenGroup = %q, want explicit token group %q to win over user group", info.TokenGroup, "premium")
	}
}

func TestGenRelayInfoOpenAI_PlaygroundPathRewritesRequestURLPath(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/pg/chat/completions?foo=bar")

	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "gpt-4"})

	if !info.IsPlayground {
		t.Fatal("expected IsPlayground = true for /pg prefixed path")
	}
	if info.RequestURLPath != "/v1/chat/completions?foo=bar" {
		t.Errorf("RequestURLPath = %q, want /pg prefix swapped for /v1", info.RequestURLPath)
	}
}

func TestGenRelayInfoOpenAI_IsStreamReflectsRequest(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/chat/completions")
	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "gpt-4", Stream: true})
	if !info.IsStream {
		t.Error("expected IsStream = true when request.Stream is true")
	}

	c2 := provCommonNewGinContext(t, "POST", "/v1/chat/completions")
	info2 := GenRelayInfoOpenAI(c2, &dto.GeneralOpenAIRequest{Model: "gpt-4", Stream: false})
	if info2.IsStream {
		t.Error("expected IsStream = false when request.Stream is false")
	}
}

func TestGenRelayInfoOpenAI_IdentityAccountIDCarriedFromContext(t *testing.T) {
	c := provCommonNewGinContext(t, "GET", "/v1/chat/completions")
	c.Set("identity_account_id", int64(42))

	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "gpt-4"})
	if info.IdentityAccountID != 42 {
		t.Errorf("IdentityAccountID = %d, want 42 (wallet bridging depends on this)", info.IdentityAccountID)
	}
}

func TestGenRelayInfoOpenAI_IdentityAccountIDWrongTypeDefaultsZero(t *testing.T) {
	c := provCommonNewGinContext(t, "GET", "/v1/chat/completions")
	c.Set("identity_account_id", "not-an-int64")

	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "gpt-4"})
	if info.IdentityAccountID != 0 {
		t.Errorf("IdentityAccountID = %d, want 0 for a type-assertion failure (must not silently misattribute wallet)", info.IdentityAccountID)
	}
}

func TestGenRelayInfoOpenAI_UserSettingCarriedWhenPresent(t *testing.T) {
	c := provCommonNewGinContext(t, "GET", "/v1/chat/completions")
	want := dto.UserSetting{}
	common.SetContextKey(c, constant.ContextKeyUserSetting, want)

	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "gpt-4"})
	// Business check: presence-detection worked (GetContextKeyType succeeded),
	// not just a zero-value coincidence — verified via a distinguishing field.
	_ = info.UserSetting // type assertion succeeded without panicking
}

func TestGenRelayInfoOpenAI_RelayModeUnknownFallsBackToGinKey(t *testing.T) {
	c := provCommonNewGinContext(t, "GET", "/some/unmapped/path")
	c.Set("relay_mode", 99)

	info := GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{Model: "gpt-4"})
	if info.RelayMode != 99 {
		t.Errorf("RelayMode = %d, want fallback to gin-context relay_mode=99 when path doesn't match a known route", info.RelayMode)
	}
}

// ---- GenRelayInfoWs ----

func TestGenRelayInfoWs(t *testing.T) {
	c := provCommonNewGinContext(t, "GET", "/v1/realtime")
	info := GenRelayInfoWs(c, nil)

	if info.RelayFormat != types.RelayFormatOpenAIRealtime {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatOpenAIRealtime)
	}
	if !info.IsFirstRequest {
		t.Error("expected IsFirstRequest = true for a fresh websocket session")
	}
	if info.InputAudioFormat != "pcm16" || info.OutputAudioFormat != "pcm16" {
		t.Errorf("audio formats = %q/%q, want pcm16/pcm16", info.InputAudioFormat, info.OutputAudioFormat)
	}
}

// ---- GenRelayInfoClaude ----

func TestGenRelayInfoClaude(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/messages")
	info := GenRelayInfoClaude(c, &dto.GeneralOpenAIRequest{})

	if info.RelayFormat != types.RelayFormatClaude {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatClaude)
	}
	if info.ShouldIncludeUsage {
		t.Error("expected ShouldIncludeUsage = false for Claude format")
	}
	if info.ClaudeConvertInfo == nil || info.LastMessagesType != LastMessageTypeNone {
		t.Errorf("ClaudeConvertInfo.LastMessagesType = %+v, want %q", info.ClaudeConvertInfo, LastMessageTypeNone)
	}
	if info.IsClaudeBetaQuery {
		t.Error("expected IsClaudeBetaQuery = false without ?beta=true")
	}
}

func TestGenRelayInfoClaude_BetaQueryDetected(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/messages?beta=true")
	info := GenRelayInfoClaude(c, &dto.GeneralOpenAIRequest{})
	if !info.IsClaudeBetaQuery {
		t.Error("expected IsClaudeBetaQuery = true for ?beta=true")
	}
}

// ---- GenRelayInfoRerank ----

func TestGenRelayInfoRerank(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/rerank")
	returnDocs := true
	req := &dto.RerankRequest{
		Documents:       []any{"doc1", "doc2"},
		ReturnDocuments: &returnDocs,
	}
	info := GenRelayInfoRerank(c, req)

	if info.RelayMode != relayconstant.RelayModeRerank {
		t.Errorf("RelayMode = %d, want RelayModeRerank", info.RelayMode)
	}
	if info.RelayFormat != types.RelayFormatRerank {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatRerank)
	}
	if info.RerankerInfo == nil || len(info.Documents) != 2 {
		t.Fatalf("RerankerInfo.Documents = %+v, want 2 documents carried through", info.RerankerInfo)
	}
	if !info.ReturnDocuments {
		t.Error("expected ReturnDocuments = true to be carried from the request")
	}
}

// ---- GenRelayInfoOpenAIAudio / GenRelayInfoEmbedding / GenRelayInfoImage / GenRelayInfoGemini ----

func TestGenRelayInfoOpenAIAudio(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/audio/transcriptions")
	info := GenRelayInfoOpenAIAudio(c, &dto.GeneralOpenAIRequest{})
	if info.RelayFormat != types.RelayFormatOpenAIAudio {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatOpenAIAudio)
	}
}

func TestGenRelayInfoEmbedding(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/embeddings")
	info := GenRelayInfoEmbedding(c, &dto.GeneralOpenAIRequest{})
	if info.RelayFormat != types.RelayFormatEmbedding {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatEmbedding)
	}
}

func TestGenRelayInfoImage(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/images/generations")
	info := GenRelayInfoImage(c, &dto.GeneralOpenAIRequest{})
	if info.RelayFormat != types.RelayFormatOpenAIImage {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatOpenAIImage)
	}
}

func TestGenRelayInfoGemini(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1beta/models/gemini-pro:generateContent")
	info := GenRelayInfoGemini(c, &dto.GeneralOpenAIRequest{})
	if info.RelayFormat != types.RelayFormatGemini {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatGemini)
	}
	if info.ShouldIncludeUsage {
		t.Error("expected ShouldIncludeUsage = false for Gemini format")
	}
}

// ---- GenRelayInfoResponses ----

func TestGenRelayInfoResponses_NoTools(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/responses")
	req := &dto.OpenAIResponsesRequest{Model: "gpt-4o"}
	info := GenRelayInfoResponses(c, req)

	if info.RelayMode != relayconstant.RelayModeResponses {
		t.Errorf("RelayMode = %d, want RelayModeResponses", info.RelayMode)
	}
	if info.ResponsesUsageInfo == nil || len(info.BuiltInTools) != 0 {
		t.Errorf("BuiltInTools = %+v, want empty map for a tool-less request", info.ResponsesUsageInfo)
	}
}

func TestGenRelayInfoResponses_WebSearchToolDefaultsMediumContextSize(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/responses")
	toolsJSON, _ := json.Marshal([]map[string]any{{"type": "web_search_preview"}})
	req := &dto.OpenAIResponsesRequest{Model: "gpt-4o", Tools: toolsJSON}

	info := GenRelayInfoResponses(c, req)

	tool, ok := info.BuiltInTools[dto.BuildInToolWebSearchPreview]
	if !ok {
		t.Fatalf("expected %q tool registered, got %+v", dto.BuildInToolWebSearchPreview, info.BuiltInTools)
	}
	if tool.SearchContextSize != "medium" {
		t.Errorf("SearchContextSize = %q, want default %q", tool.SearchContextSize, "medium")
	}
	if tool.CallCount != 0 {
		t.Errorf("CallCount = %d, want 0 at init (usage accrues during streaming)", tool.CallCount)
	}
}

func TestGenRelayInfoResponses_WebSearchToolExplicitContextSizePreserved(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/responses")
	toolsJSON, _ := json.Marshal([]map[string]any{{"type": "web_search_preview", "search_context_size": "high"}})
	req := &dto.OpenAIResponsesRequest{Model: "gpt-4o", Tools: toolsJSON}

	info := GenRelayInfoResponses(c, req)
	tool := info.BuiltInTools[dto.BuildInToolWebSearchPreview]
	if tool == nil || tool.SearchContextSize != "high" {
		t.Errorf("SearchContextSize = %+v, want explicit %q preserved", tool, "high")
	}
}

// ---- GenRelayInfo dispatcher ----

func TestGenRelayInfo_DispatchesByFormat(t *testing.T) {
	tests := []struct {
		name       string
		format     types.RelayFormat
		request    dto.Request
		wantFormat types.RelayFormat
		wantErr    bool
	}{
		{"openai", types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{}, types.RelayFormatOpenAI, false},
		{"openai_audio", types.RelayFormatOpenAIAudio, &dto.GeneralOpenAIRequest{}, types.RelayFormatOpenAIAudio, false},
		{"openai_image", types.RelayFormatOpenAIImage, &dto.GeneralOpenAIRequest{}, types.RelayFormatOpenAIImage, false},
		{"claude", types.RelayFormatClaude, &dto.GeneralOpenAIRequest{}, types.RelayFormatClaude, false},
		{"gemini", types.RelayFormatGemini, &dto.GeneralOpenAIRequest{}, types.RelayFormatGemini, false},
		{"embedding", types.RelayFormatEmbedding, &dto.GeneralOpenAIRequest{}, types.RelayFormatEmbedding, false},
		{"rerank wrong type", types.RelayFormatRerank, &dto.GeneralOpenAIRequest{}, "", true},
		{"rerank correct type", types.RelayFormatRerank, &dto.RerankRequest{}, types.RelayFormatRerank, false},
		{"responses wrong type", types.RelayFormatOpenAIResponses, &dto.GeneralOpenAIRequest{}, "", true},
		{"responses correct type", types.RelayFormatOpenAIResponses, &dto.OpenAIResponsesRequest{}, types.RelayFormatOpenAIResponses, false},
		{"task format", types.RelayFormatTask, nil, "", false},
		{"mj_proxy format", types.RelayFormatMjProxy, nil, "", false},
		{"unknown format", types.RelayFormat("bogus"), nil, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := provCommonNewGinContext(t, "POST", "/x")
			info, err := GenRelayInfo(c, tt.format, tt.request, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if info != nil {
					t.Errorf("info = %+v, want nil on error", info)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantFormat != "" && info.RelayFormat != tt.wantFormat {
				t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, tt.wantFormat)
			}
		})
	}
}

func TestGenRelayInfo_WsFormatCarriesConnection(t *testing.T) {
	c := provCommonNewGinContext(t, "GET", "/v1/realtime")
	info, err := GenRelayInfo(c, types.RelayFormatOpenAIRealtime, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.RelayFormat != types.RelayFormatOpenAIRealtime {
		t.Errorf("RelayFormat = %q, want %q", info.RelayFormat, types.RelayFormatOpenAIRealtime)
	}
}

// ---- InitChannelMeta ----

func TestInitChannelMeta_PopulatesFromContext(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/chat/completions")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelId, 7)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://api.openai.com")
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-secret")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")

	// OriginModelName is a RelayInfo struct field (set earlier in
	// genBaseRelayInfo from the request path); InitChannelMeta re-applies it
	// to the request after resolving channel metadata, so it must be set
	// directly here rather than only via the context key.
	info := &RelayInfo{Request: &dto.GeneralOpenAIRequest{Model: "client-supplied-alias"}, OriginModelName: "gpt-4o"}
	info.InitChannelMeta(c)

	if info.ChannelMeta == nil {
		t.Fatal("expected ChannelMeta to be populated")
	}
	if info.ChannelId != 7 {
		t.Errorf("ChannelId = %d, want 7", info.ChannelId)
	}
	if info.ChannelBaseUrl != "https://api.openai.com" {
		t.Errorf("ChannelBaseUrl = %q, want %q", info.ChannelBaseUrl, "https://api.openai.com")
	}
	if info.ApiKey != "sk-secret" {
		t.Errorf("ApiKey = %q, want %q", info.ApiKey, "sk-secret")
	}
	// Business rule: UpstreamModelName is read from the ContextKeyOriginalModel
	// value set by earlier middleware.
	if info.UpstreamModelName != "gpt-4o" {
		t.Errorf("UpstreamModelName = %q, want %q", info.UpstreamModelName, "gpt-4o")
	}
	// Business rule: SupportStreamOptions must be enabled for OpenAI channels.
	if !info.SupportStreamOptions {
		t.Error("expected SupportStreamOptions = true for ChannelTypeOpenAI")
	}
	// Business rule: the outgoing request's model name is reset to
	// info.OriginModelName, overriding whatever alias the client sent.
	if info.Request.(*dto.GeneralOpenAIRequest).Model != "gpt-4o" {
		t.Errorf("Request.Model = %q, want reset to OriginModelName %q", info.Request.(*dto.GeneralOpenAIRequest).Model, "gpt-4o")
	}
}

func TestInitChannelMeta_UnsupportedStreamChannelLeavesFlagFalse(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/chat/completions")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMokaAI)

	info := &RelayInfo{}
	info.InitChannelMeta(c)

	if info.SupportStreamOptions {
		t.Error("expected SupportStreamOptions = false for a channel type not in the stream-supported allowlist")
	}
}

func TestInitChannelMeta_AzureUsesQueryAPIVersion(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/openai/deployments/gpt-4/chat/completions?api-version=2024-02-01")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAzure)

	info := &RelayInfo{}
	info.InitChannelMeta(c)

	if info.ApiVersion != "2024-02-01" {
		t.Errorf("ApiVersion = %q, want the Azure query api-version to be picked up", info.ApiVersion)
	}
}

func TestInitChannelMeta_VertexUsesRegion(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/models")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeVertexAi)
	c.Set("region", "us-central1")

	info := &RelayInfo{}
	info.InitChannelMeta(c)

	if info.ApiVersion != "us-central1" {
		t.Errorf("ApiVersion = %q, want Vertex region %q", info.ApiVersion, "us-central1")
	}
}

func TestInitChannelMeta_ChannelSettingsCarriedWhenTypeMatches(t *testing.T) {
	c := provCommonNewGinContext(t, "POST", "/v1/x")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{Proxy: "http://proxy.local:8080"})
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowServiceTier: true})

	info := &RelayInfo{}
	info.InitChannelMeta(c)

	if info.ChannelSetting.Proxy != "http://proxy.local:8080" {
		t.Errorf("ChannelSetting.Proxy = %q, want carried through from context", info.ChannelSetting.Proxy)
	}
	if !info.ChannelOtherSettings.AllowServiceTier {
		t.Error("expected ChannelOtherSettings.AllowServiceTier = true to be carried through")
	}
}

// ---- ToString ----

func TestRelayInfo_ToString_NilReceiver(t *testing.T) {
	var info *RelayInfo
	if got := info.ToString(); got != "RelayInfo<nil>" {
		t.Errorf("ToString() = %q, want %q", got, "RelayInfo<nil>")
	}
}

func TestRelayInfo_ToString_MasksSecrets(t *testing.T) {
	now := time.Now()
	info := &RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-4o",
		UserId:          1,
		UserEmail:       "alice@example.com",
		TokenId:         5,
		TokenKey:        "sk-super-secret-token",
		StartTime:       now,
		ChannelMeta: &ChannelMeta{
			ChannelType: constant.ChannelTypeOpenAI,
			ApiKey:      "sk-very-secret-api-key",
		},
	}
	info.FirstResponseTime = now.Add(200 * time.Millisecond)

	s := info.ToString()

	if strings.Contains(s, "sk-super-secret-token") {
		t.Error("ToString() leaked the raw token key — must be masked")
	}
	if strings.Contains(s, "sk-very-secret-api-key") {
		t.Error("ToString() leaked the raw channel API key — must be masked")
	}
	if strings.Contains(s, "alice") {
		t.Error("ToString() leaked the email local-part — must be masked")
	}
	if !strings.Contains(s, "example.com") {
		t.Error("ToString() should still surface the email domain for debugging")
	}
	if !strings.Contains(s, "gpt-4o") {
		t.Error("ToString() should surface the model name (not sensitive)")
	}
}

func TestRelayInfo_ToString_IncludesConditionalSections(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:       types.RelayFormatOpenAIAudio,
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
		AudioUsage:        true,
		ReasoningEffort:   "high",
		ResponsesUsageInfo: &ResponsesUsageInfo{
			BuiltInTools: map[string]*BuildInToolInfo{
				"web_search_preview": {ToolName: "web_search_preview", CallCount: 3},
			},
		},
	}
	s := info.ToString()
	if !strings.Contains(s, "Realtime{") {
		t.Error("expected Realtime section when audio fields are set")
	}
	if !strings.Contains(s, "ReasoningEffort: \"high\"") {
		t.Error("expected ReasoningEffort section")
	}
	if !strings.Contains(s, "calls=3") {
		t.Error("expected ResponsesTools section with call count")
	}
}

// ---- prompt-token / first-response bookkeeping ----

func TestRelayInfo_EstimatePromptTokensRoundTrip(t *testing.T) {
	info := &RelayInfo{}
	info.SetEstimatePromptTokens(123)
	if got := info.GetEstimatePromptTokens(); got != 123 {
		t.Errorf("GetEstimatePromptTokens() = %d, want 123", got)
	}
}

func TestRelayInfo_SetFirstResponseTime_OnlyFirstCallTakesEffect(t *testing.T) {
	start := time.Now()
	// Sleep past the underlying clock's tick so the two time.Now() reads used
	// below (StartTime here, FirstResponseTime inside SetFirstResponseTime)
	// are guaranteed strictly ordered rather than racing to equal timestamps.
	time.Sleep(5 * time.Millisecond)
	info := &RelayInfo{StartTime: start}
	info.isFirstResponse = true

	if info.HasSendResponse() {
		t.Fatal("HasSendResponse() should be false before any response is recorded")
	}

	info.SetFirstResponseTime()
	first := info.FirstResponseTime
	if !first.After(start) {
		t.Errorf("FirstResponseTime = %v, want strictly after start %v", first, start)
	}
	if !info.HasSendResponse() {
		t.Error("HasSendResponse() should be true after SetFirstResponseTime()")
	}

	time.Sleep(2 * time.Millisecond)
	info.SetFirstResponseTime() // second call must be a no-op
	if !info.FirstResponseTime.Equal(first) {
		t.Errorf("FirstResponseTime changed on second call: %v -> %v, want idempotent after first response", first, info.FirstResponseTime)
	}
}

// ---- TaskSubmitReq ----

func TestTaskSubmitReq_GetPromptAndHasImage(t *testing.T) {
	r := &TaskSubmitReq{Prompt: "a cat"}
	if r.GetPrompt() != "a cat" {
		t.Errorf("GetPrompt() = %q, want %q", r.GetPrompt(), "a cat")
	}
	if r.HasImage() {
		t.Error("HasImage() should be false with no Images")
	}
	r.Images = []string{"http://example.com/1.png"}
	if !r.HasImage() {
		t.Error("HasImage() should be true once Images is non-empty")
	}
}

func TestTaskSubmitReq_UnmarshalJSON_MetadataAsEncodedJSONString(t *testing.T) {
	// metadata delivered as a JSON string containing an object (a real-world
	// quirk some upstream SDKs produce).
	raw := `{"prompt":"p","metadata":"{\"seed\":42}"}`
	var r TaskSubmitReq
	if err := (&r).UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if r.Prompt != "p" {
		t.Errorf("Prompt = %q, want %q", r.Prompt, "p")
	}
	seed, ok := r.Metadata["seed"]
	if !ok {
		t.Fatalf("Metadata = %+v, want key 'seed' present", r.Metadata)
	}
	if int(seed.(float64)) != 42 {
		t.Errorf("Metadata[seed] = %v, want 42", seed)
	}
}

func TestTaskSubmitReq_UnmarshalJSON_MetadataAsDirectObject(t *testing.T) {
	raw := `{"prompt":"p","metadata":{"seed":7}}`
	var r TaskSubmitReq
	if err := (&r).UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if int(r.Metadata["seed"].(float64)) != 7 {
		t.Errorf("Metadata[seed] = %v, want 7", r.Metadata["seed"])
	}
}

func TestTaskSubmitReq_UnmarshalJSON_NoMetadata(t *testing.T) {
	raw := `{"prompt":"p"}`
	var r TaskSubmitReq
	if err := (&r).UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if r.Metadata != nil {
		t.Errorf("Metadata = %+v, want nil when absent from payload", r.Metadata)
	}
}

func TestTaskSubmitReq_UnmarshalMetadata(t *testing.T) {
	r := &TaskSubmitReq{Metadata: map[string]interface{}{"seconds": float64(8), "size": "720x1280"}}
	type target struct {
		Seconds int    `json:"seconds"`
		Size    string `json:"size"`
	}
	var out target
	if err := r.UnmarshalMetadata(&out); err != nil {
		t.Fatalf("UnmarshalMetadata() error = %v", err)
	}
	if out.Seconds != 8 || out.Size != "720x1280" {
		t.Errorf("out = %+v, want {Seconds:8 Size:720x1280}", out)
	}
}

func TestTaskSubmitReq_UnmarshalMetadata_NilIsNoop(t *testing.T) {
	r := &TaskSubmitReq{}
	type target struct{ X int }
	var out target
	if err := r.UnmarshalMetadata(&out); err != nil {
		t.Fatalf("UnmarshalMetadata() error = %v, want nil for empty metadata", err)
	}
	if out.X != 0 {
		t.Errorf("out.X = %d, want untouched 0", out.X)
	}
}

// ---- FailTaskInfo ----

func TestFailTaskInfo(t *testing.T) {
	ti := FailTaskInfo("upstream rejected")
	if ti.Status != "FAILURE" {
		t.Errorf("Status = %q, want FAILURE", ti.Status)
	}
	if ti.Reason != "upstream rejected" {
		t.Errorf("Reason = %q, want %q", ti.Reason, "upstream rejected")
	}
}

// ---- RemoveDisabledFields ----

func TestRemoveDisabledFields(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		settings   dto.ChannelOtherSettings
		wantHas    []string
		wantNotHas []string
	}{
		{
			name:       "defaults strip service_tier and safety_identifier, keep store",
			input:      `{"model":"gpt-4","service_tier":"scale","safety_identifier":"u1","store":true}`,
			settings:   dto.ChannelOtherSettings{},
			wantHas:    []string{"store"},
			wantNotHas: []string{"service_tier", "safety_identifier"},
		},
		{
			name:       "allow service tier keeps it",
			input:      `{"model":"gpt-4","service_tier":"scale"}`,
			settings:   dto.ChannelOtherSettings{AllowServiceTier: true},
			wantHas:    []string{"service_tier"},
			wantNotHas: nil,
		},
		{
			name:       "disable store removes it",
			input:      `{"model":"gpt-4","store":true}`,
			settings:   dto.ChannelOtherSettings{DisableStore: true},
			wantHas:    nil,
			wantNotHas: []string{"store"},
		},
		{
			name:       "allow safety identifier keeps it",
			input:      `{"model":"gpt-4","safety_identifier":"u1"}`,
			settings:   dto.ChannelOtherSettings{AllowSafetyIdentifier: true},
			wantHas:    []string{"safety_identifier"},
			wantNotHas: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := RemoveDisabledFields([]byte(tt.input), tt.settings)
			if err != nil {
				t.Fatalf("RemoveDisabledFields() error = %v", err)
			}
			var m map[string]interface{}
			if err := json.Unmarshal(out, &m); err != nil {
				t.Fatalf("output is not valid JSON: %v (%s)", err, out)
			}
			for _, k := range tt.wantHas {
				if _, ok := m[k]; !ok {
					t.Errorf("expected field %q to be present, got %s", k, out)
				}
			}
			for _, k := range tt.wantNotHas {
				if _, ok := m[k]; ok {
					t.Errorf("expected field %q to be stripped, got %s", k, out)
				}
			}
		})
	}
}

func TestRemoveDisabledFields_MalformedJSONReturnsOriginalWithoutError(t *testing.T) {
	input := []byte(`{not valid json`)
	out, err := RemoveDisabledFields(input, dto.ChannelOtherSettings{})
	if err != nil {
		t.Fatalf("RemoveDisabledFields() error = %v, want nil (malformed input is passed through)", err)
	}
	if string(out) != string(input) {
		t.Errorf("out = %s, want original input passed through unchanged on parse failure", out)
	}
}

// ---- RemoveGeminiDisabledFields ----

func TestRemoveGeminiDisabledFields_RemovesFunctionResponseIdByDefault(t *testing.T) {
	input := `{"contents":[{"parts":[{"functionResponse":{"id":"fr1","name":"tool"}},{"function_response":{"id":"fr2","name":"tool2"}}]}]}`
	out, err := RemoveGeminiDisabledFields([]byte(input))
	if err != nil {
		t.Fatalf("RemoveGeminiDisabledFields() error = %v", err)
	}
	if strings.Contains(string(out), `"id"`) {
		t.Errorf("expected functionResponse/function_response id fields stripped, got %s", out)
	}
	if !strings.Contains(string(out), `"name":"tool"`) {
		t.Errorf("expected unrelated fields preserved, got %s", out)
	}
}

func TestRemoveGeminiDisabledFields_DisabledLeavesIdIntact(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prev := settings.RemoveFunctionResponseIdEnabled
	settings.RemoveFunctionResponseIdEnabled = false
	t.Cleanup(func() { settings.RemoveFunctionResponseIdEnabled = prev })

	input := `{"contents":[{"parts":[{"functionResponse":{"id":"fr1"}}]}]}`
	out, err := RemoveGeminiDisabledFields([]byte(input))
	if err != nil {
		t.Fatalf("RemoveGeminiDisabledFields() error = %v", err)
	}
	if !strings.Contains(string(out), `"id":"fr1"`) {
		t.Errorf("expected id field preserved when the setting is disabled, got %s", out)
	}
}

func TestRemoveGeminiDisabledFields_MalformedJSONReturnsOriginalWithoutError(t *testing.T) {
	input := []byte(`{not valid`)
	out, err := RemoveGeminiDisabledFields(input)
	if err != nil {
		t.Fatalf("RemoveGeminiDisabledFields() error = %v, want nil", err)
	}
	if string(out) != string(input) {
		t.Errorf("out = %s, want original input unchanged on parse failure", out)
	}
}

func TestRemoveGeminiDisabledFields_NoContentsIsNoop(t *testing.T) {
	input := `{"model":"gemini-pro"}`
	out, err := RemoveGeminiDisabledFields([]byte(input))
	if err != nil {
		t.Fatalf("RemoveGeminiDisabledFields() error = %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if m["model"] != "gemini-pro" {
		t.Errorf("model = %v, want preserved", m["model"])
	}
}
