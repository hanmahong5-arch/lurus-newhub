package bifrosteng

import (
	"context"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestShouldHandleChat_Gates(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "legacy")
	ResetConfigForTest()
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	if ShouldHandleChat(constant.APITypeOpenAI, req, false) {
		t.Fatal("legacy engine must not handle")
	}

	t.Setenv("RELAY_ENGINE", "bifrost")
	t.Setenv("RELAY_BIFROST_MODES", "chat")
	ResetConfigForTest()
	// Ready() is false without Init — still must not handle.
	if ShouldHandleChat(constant.APITypeOpenAI, req, false) {
		t.Fatal("uninitialized bifrost must not handle")
	}
	if ShouldHandleChat(constant.APITypeOpenAI, req, true) {
		t.Fatal("pass-through must not handle")
	}
	if ShouldHandleChat(constant.APITypeBaidu, req, false) {
		t.Fatal("deferred apiType must not handle")
	}
}

func TestSnapshot_Legacy(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "")
	ResetConfigForTest()
	ResetTotalsForTest()
	ObserveEngine(EngineLegacy, ModeChat, "ok")
	snap := Snapshot()
	if snap["engine"] != EngineLegacy {
		t.Fatalf("engine=%v", snap["engine"])
	}
	if snap["ready"] != false {
		t.Fatalf("ready=%v", snap["ready"])
	}
	if snap["replica_scoped"] != true {
		t.Fatal("expected replica_scoped")
	}
	totals, ok := snap["totals"].([]EngineTotalRow)
	if !ok || len(totals) == 0 {
		t.Fatalf("totals=%v", snap["totals"])
	}
}

func TestMappingCoverage(t *testing.T) {
	if !IsMapped(constant.APITypeOpenAI) || CoverageOf(constant.APITypeOpenAI) != CoverageOwned {
		t.Fatal("openai should be owned")
	}
	if CoverageOf(constant.APITypeAws) != CoverageOwned {
		t.Fatal("aws should be owned after account extras")
	}
	if CoverageOf(constant.APITypeBaidu) != CoverageDeferred {
		t.Fatal("baidu deferred")
	}
	if CoverageOf(99999) != CoverageLegacy {
		t.Fatal("unknown legacy")
	}
	if key := ProviderKey(42); key != schemas.ModelProvider("nh-ch-42") {
		t.Fatalf("key=%q", key)
	}
}

func TestToStreamChunk_AndEmbedding(t *testing.T) {
	role := "assistant"
	content := "hi"
	resp := &schemas.BifrostChatResponse{
		ID:                "chunk-1",
		Created:           2,
		Model:             "",
		SystemFingerprint: "fp",
		Choices: []schemas.BifrostResponseChoice{{
			Index: 0,
			ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
				Delta: &schemas.ChatStreamResponseChoiceDelta{
					Role:    &role,
					Content: &content,
				},
			},
		}},
	}
	chunk := toStreamChunk(resp, "gpt-test")
	if chunk.Model != "gpt-test" || chunk.SystemFingerprint == nil || len(chunk.Choices) != 1 {
		t.Fatalf("chunk=%+v", chunk)
	}
	if chunk.Choices[0].Delta.Content == nil || *chunk.Choices[0].Delta.Content != "hi" {
		t.Fatalf("delta=%+v", chunk.Choices[0].Delta)
	}

	emb := toOpenAIEmbeddingResponse(&schemas.BifrostEmbeddingResponse{
		Model: "",
		Data: []schemas.EmbeddingData{
			{Index: 0, Embedding: schemas.EmbeddingStruct{EmbeddingArray: []float64{0.1, 0.2}}},
		},
		Usage: &schemas.BifrostLLMUsage{PromptTokens: 1, TotalTokens: 1},
	}, "text-embedding-3")
	if emb.Model != "text-embedding-3" || len(emb.Data) != 1 {
		t.Fatalf("emb=%+v", emb)
	}
}

func TestObserveBifrostAndSplit3(t *testing.T) {
	ResetTotalsForTest()
	ObserveEngine("bifrost", "chat", "error")
	observeBifrost("chat", "error", time.Now().Add(-50*time.Millisecond))
	if split3("a|b") != nil {
		t.Fatal("want nil for two parts")
	}
	if got := split3("a|b|c"); got == nil || got[2] != "c" {
		t.Fatalf("got=%v", got)
	}
	if Client() != nil || Account() != nil || Ready() {
		t.Fatal("expected nil client when not Init'd")
	}
	Shutdown() // safe no-op
}

func TestToBifrostMessages_Reasoning(t *testing.T) {
	msgs, err := toBifrostMessages([]dto.Message{{
		Role:             "assistant",
		Content:          "ok",
		ReasoningContent: "think",
	}})
	if err != nil || len(msgs) != 1 || msgs[0].Reasoning == nil || *msgs[0].Reasoning != "think" {
		t.Fatalf("msgs=%v err=%v", msgs, err)
	}
}

func TestConvertBranches_ContentParamsErrors(t *testing.T) {
	// multimodal convertible + content blocks
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user"}}}
	req.Messages[0].SetMediaContent([]dto.MediaContent{
		{Type: "text", Text: "hi"},
		{Type: "input_audio", InputAudio: map[string]any{"data": "abc", "format": "wav"}},
		{Type: "refusal", Text: "no"},
		{Type: "file", File: map[string]any{"file_id": "file-1", "filename": "a.pdf"}},
	})
	if !ChatConvertible(req) {
		t.Fatal("expected convertible multimodal")
	}
	msgs, err := toBifrostMessages(req.Messages)
	if err != nil || len(msgs) != 1 || msgs[0].Content == nil || len(msgs[0].Content.ContentBlocks) != 4 {
		t.Fatalf("msgs=%v err=%v", msgs, err)
	}

	bad := dto.MediaContent{Type: "image_url"}
	if _, err := toContentBlock(bad); err == nil {
		t.Fatal("image_url without url must error")
	}
	if _, err := toContentBlock(dto.MediaContent{Type: "input_audio"}); err == nil {
		t.Fatal("input_audio without data must error")
	}
	if _, err := toContentBlock(dto.MediaContent{Type: "file"}); err == nil {
		t.Fatal("empty file must error")
	}
	if _, err := toContentBlock(dto.MediaContent{Type: "video_url"}); err == nil {
		t.Fatal("video_url must error")
	}
	if _, err := toContentBlock(dto.MediaContent{Type: "weird"}); err == nil {
		t.Fatal("unknown type must error")
	}
	if toInputFile(nil) != nil || toInputFile(map[string]any{}) != nil {
		t.Fatal("empty file raw must be nil")
	}
	if f := toInputFile(map[string]any{"file_url": "https://x"}); f == nil || f.FileURL == nil {
		t.Fatal("file_url map")
	}

	temp := 0.2
	paramsReq := &dto.GeneralOpenAIRequest{
		Temperature:          &temp,
		TopP:                 0.9,
		MaxTokens:            10,
		FrequencyPenalty:     0.1,
		PresencePenalty:      0.2,
		User:                 "u1",
		Seed:                 7,
		Stop:                []string{"END"},
		ToolChoice:          "auto",
		Tools:               []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "f", Parameters: map[string]any{"type": "object"}}}},
		ResponseFormat:      &dto.ResponseFormat{Type: "json_object"},
		MaxCompletionTokens: 0,
	}
	p := toChatParams(paramsReq)
	if p == nil || p.Temperature == nil || len(p.Tools) != 1 || p.ToolChoice == nil {
		t.Fatalf("params=%+v", p)
	}
	if normalizeStop("x") == nil || normalizeStop([]any{"a", "b"}) == nil || normalizeStop(1) != nil {
		t.Fatal("normalizeStop branches")
	}
	if toToolChoice(nil) != nil {
		t.Fatal("nil tool choice")
	}
	if toToolChoice("required") == nil {
		t.Fatal("string tool choice")
	}
	if toToolChoice(map[string]any{"type": "function", "function": map[string]any{"name": "f"}}) == nil {
		t.Fatal("struct tool choice")
	}

	u := fromBifrostUsage(nil)
	if u == nil || u.TotalTokens != 0 {
		t.Fatalf("nil usage=%v", u)
	}
	u = fromBifrostUsage(&schemas.BifrostLLMUsage{
		CompletionTokensDetails: &schemas.ChatCompletionTokensDetails{ReasoningTokens: 2, AudioTokens: 1, TextTokens: 3},
	})
	if u.CompletionTokenDetails.ReasoningTokens != 2 {
		t.Fatalf("completion details=%+v", u.CompletionTokenDetails)
	}

	if bifrostMessageText(nil) != "" || bifrostMessageText(&schemas.ChatMessage{}) != "" {
		t.Fatal("empty message text")
	}
	s := "hello"
	if bifrostMessageText(&schemas.ChatMessage{Content: &schemas.ChatMessageContent{ContentStr: &s}}) != "hello" {
		t.Fatal("string content")
	}
	if bifrostErrMessage(nil) == "" {
		t.Fatal("nil err message fallback")
	}
	if bifrostErrStatus(nil) != 500 {
		t.Fatal("nil err status")
	}
	st := 429
	be := &schemas.BifrostError{Error: &schemas.ErrorField{Message: "boom"}, StatusCode: &st}
	if bifrostErrMessage(be) != "boom" || bifrostErrStatus(be) != 429 {
		t.Fatalf("err map msg=%s st=%d", bifrostErrMessage(be), bifrostErrStatus(be))
	}
}

func TestAccountRegisterAndLookup(t *testing.T) {
	acc := NewChannelAccount()
	if _, err := acc.RegisterChannel(nil); err == nil {
		t.Fatal("nil info")
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeOpenAI,
			ChannelId:         9,
			ApiKey:            "sk-test",
			ChannelBaseUrl:    "https://example.com/v1",
			UpstreamModelName: "gpt-4o-mini",
		},
	}
	key, err := acc.RegisterChannel(info)
	if err != nil || key == "" {
		t.Fatalf("register: %v %q", err, key)
	}
	providers, err := acc.GetConfiguredProviders()
	if err != nil || len(providers) != 1 {
		t.Fatalf("providers=%v err=%v", providers, err)
	}
	keys, err := acc.GetKeysForProvider(context.Background(), key)
	if err != nil || len(keys) != 1 || keys[0].Value.GetValue() == "" {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	cfg, err := acc.GetConfigForProvider(key)
	if err != nil || cfg == nil || cfg.NetworkConfig.BaseURL == "" {
		t.Fatalf("cfg=%v err=%v", cfg, err)
	}
	if _, err := acc.GetKeysForProvider(context.Background(), "missing"); err == nil {
		t.Fatal("missing provider")
	}
}

func TestInitLegacyNoopAndBifrost(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "legacy")
	ResetConfigForTest()
	if err := Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if Ready() {
		t.Fatal("legacy must not be ready")
	}

	t.Setenv("RELAY_ENGINE", "bifrost")
	t.Setenv("RELAY_BIFROST_MODES", "chat,embeddings")
	ResetConfigForTest()
	if err := Init(context.Background()); err != nil {
		t.Fatalf("bifrost Init: %v", err)
	}
	t.Cleanup(Shutdown)
	if !Ready() || Client() == nil || Account() == nil {
		t.Fatal("expected ready after Init")
	}
	if !ShouldHandleEmbeddings(constant.APITypeOpenAI) {
		// Ready + embeddings mode + mapped — should be true
		t.Fatal("expected embeddings handleable")
	}
	// second Init is idempotent
	if err := Init(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBaseProviderVariants(t *testing.T) {
	for _, api := range []int{
		constant.APITypeAnthropic, constant.APITypeGemini, constant.APITypeOllama,
		constant.APITypeCohere, constant.APITypeOpenRouter, constant.APITypeDeepSeek,
	} {
		if !IsMapped(api) {
			t.Fatalf("api %d should be mapped", api)
		}
	}
}
