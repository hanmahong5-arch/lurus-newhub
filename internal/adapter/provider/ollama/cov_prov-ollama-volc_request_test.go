package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL: URL construction is billing-adjacent — a wrong path
// silently routes traffic to the wrong upstream endpoint.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_GetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		want    string
		wantErr bool
	}{
		{
			name: "embeddings mode uses /api/embed",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://localhost:11434"},
			},
			want: "http://localhost:11434/api/embed",
		},
		{
			name: "completions relay mode uses /api/generate",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://localhost:11434"},
			},
			want: "http://localhost:11434/api/generate",
		},
		{
			name: "URL path contains /v1/completions uses /api/generate even if RelayMode says chat",
			info: &relaycommon.RelayInfo{
				RelayMode:      relayconstant.RelayModeChatCompletions,
				RequestURLPath: "/v1/completions",
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "http://localhost:11434"},
			},
			want: "http://localhost:11434/api/generate",
		},
		{
			name: "default falls back to /api/chat",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://localhost:11434"},
			},
			want: "http://localhost:11434/api/chat",
		},
		{
			name: "custom base_url with trailing slash is NOT normalized (double slash bug surface)",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://myhost:11434/"},
			},
			want: "http://myhost:11434//api/chat",
		},
		{
			name: "base_url with a path prefix is preserved verbatim",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://gw.example.com/ollama-proxy"},
			},
			want: "https://gw.example.com/ollama-proxy/api/chat",
		},
		{
			name: "empty base_url still appends the path (no crash)",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ""},
			},
			want: "/api/chat",
		},
	}
	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader: auth header is the money path — a wrong or
// missing Authorization header means every relayed request silently fails
// upstream auth.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_SetupRequestHeader_SetsBearerAuth(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-secret-123"}}
	header := &http.Header{}
	err := a.SetupRequestHeader(c, header, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer sk-secret-123" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-secret-123")
	}
}

func TestProvOllamaVolc_SetupRequestHeader_EmptyApiKeyStillSetsBearerPrefix(t *testing.T) {
	// FINDING: SetupRequestHeader unconditionally does
	// req.Set("Authorization", "Bearer "+info.ApiKey) with no guard for an
	// empty ApiKey (unlike FetchOllamaModels/PullOllamaModel/etc which all
	// skip the header when apiKey==""). For local/unauthenticated Ollama
	// deployments (the common case — Ollama has no native auth) this sends a
	// literal "Authorization: Bearer " header on every relayed request. Most
	// servers ignore an empty bearer token, but this is inconsistent with the
	// rest of this package's helper functions and could reject on stricter
	// upstream proxies. Locking in current behavior as a regression baseline.
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: ""}}
	header := &http.Header{}
	_ = a.SetupRequestHeader(c, header, info)
	if got := header.Get("Authorization"); got != "Bearer " {
		t.Errorf("Authorization = %q, want %q (current unconditional-Bearer-prefix behavior)", got, "Bearer ")
	}
}

// ---------------------------------------------------------------------------
// Adaptor: not-implemented / trivial passthrough methods
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_Adaptor_UnsupportedConversions(t *testing.T) {
	a := &Adaptor{}
	c := &gin.Context{}
	info := &relaycommon.RelayInfo{}

	if _, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{}); err == nil {
		t.Error("ConvertGeminiRequest should return an error (not implemented)")
	}
	if _, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{}); err == nil {
		t.Error("ConvertAudioRequest should return an error (not implemented)")
	}
	if _, err := a.ConvertImageRequest(c, info, dto.ImageRequest{}); err == nil {
		t.Error("ConvertImageRequest should return an error (not implemented)")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest should return an error (not implemented)")
	}
	if resp, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{}); resp != nil || err != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil)", resp, err)
	}
}

func TestProvOllamaVolc_Adaptor_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	list := a.GetModelList()
	if len(list) == 0 {
		t.Fatal("GetModelList() should return at least one model")
	}
	found := false
	for _, m := range list {
		if m == "llama3-7b" {
			found = true
		}
	}
	if !found {
		t.Errorf("GetModelList() = %v, want to contain %q", list, "llama3-7b")
	}
	if got := a.GetChannelName(); got != "ollama" {
		t.Errorf("GetChannelName() = %q, want %q", got, "ollama")
	}
}

func TestProvOllamaVolc_Adaptor_ConvertEmbeddingRequest_MapsModelAndInput(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "nomic-embed-text", Input: "hello world"}
	out, err := a.ConvertEmbeddingRequest(&gin.Context{}, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	embReq, ok := out.(*OllamaEmbeddingRequest)
	if !ok {
		t.Fatalf("expected *OllamaEmbeddingRequest, got %T", out)
	}
	if embReq.Model != "nomic-embed-text" {
		t.Errorf("Model = %q, want %q", embReq.Model, "nomic-embed-text")
	}
	if embReq.Input != "hello world" {
		t.Errorf("Input = %v, want %q", embReq.Input, "hello world")
	}
}

func TestProvOllamaVolc_Adaptor_ConvertOpenAIRequest_NilRequestErrors(t *testing.T) {
	a := &Adaptor{}
	out, err := a.ConvertOpenAIRequest(&gin.Context{}, &relaycommon.RelayInfo{}, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	if out != nil {
		t.Errorf("out = %v, want nil", out)
	}
}

func TestProvOllamaVolc_Adaptor_ConvertOpenAIRequest_DispatchesGenerateVsChat(t *testing.T) {
	a := &Adaptor{}

	// completions relay mode -> OllamaGenerateRequest
	genOut, err := a.ConvertOpenAIRequest(&gin.Context{}, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeCompletions}, &dto.GeneralOpenAIRequest{Model: "llama3", Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := genOut.(*OllamaGenerateRequest); !ok {
		t.Fatalf("expected *OllamaGenerateRequest for completions mode, got %T", genOut)
	}

	// chat relay mode -> OllamaChatRequest
	chatOut, err := a.ConvertOpenAIRequest(&gin.Context{}, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions}, &dto.GeneralOpenAIRequest{Model: "llama3", Messages: []dto.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := chatOut.(*OllamaChatRequest); !ok {
		t.Fatalf("expected *OllamaChatRequest for chat mode, got %T", chatOut)
	}
}

// ---------------------------------------------------------------------------
// openAIChatToOllamaChat: request-side protocol translation. This is the
// core "internal DTO -> vendor DTO" business logic.
// ---------------------------------------------------------------------------

func floatPtr(f float64) *float64 { return &f }

func TestProvOllamaVolc_OpenAIChatToOllamaChat_ParameterMapping(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model:            "llama3",
		Stream:           true,
		Temperature:      floatPtr(0.7),
		TopP:             0.9,
		TopK:             40,
		FrequencyPenalty: 0.5,
		PresencePenalty:  0.3,
		Seed:             42,
		MaxTokens:        256,
		Messages:         []dto.Message{{Role: "user", Content: "hi"}},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Model != "llama3" {
		t.Errorf("Model = %q, want %q", out.Model, "llama3")
	}
	if !out.Stream {
		t.Error("Stream should be true")
	}
	if got := out.Options["temperature"]; got == nil || *(got.(*float64)) != 0.7 {
		t.Errorf("Options[temperature] = %v, want 0.7", got)
	}
	if out.Options["top_p"] != 0.9 {
		t.Errorf("Options[top_p] = %v, want 0.9", out.Options["top_p"])
	}
	if out.Options["top_k"] != 40 {
		t.Errorf("Options[top_k] = %v, want 40", out.Options["top_k"])
	}
	if out.Options["frequency_penalty"] != 0.5 {
		t.Errorf("Options[frequency_penalty] = %v, want 0.5", out.Options["frequency_penalty"])
	}
	if out.Options["presence_penalty"] != 0.3 {
		t.Errorf("Options[presence_penalty] = %v, want 0.3", out.Options["presence_penalty"])
	}
	if out.Options["seed"] != 42 {
		t.Errorf("Options[seed] = %v, want 42", out.Options["seed"])
	}
	// GetMaxTokens() -> num_predict is the crucial billing-adjacent clamp: an
	// unmapped max_tokens means the upstream generates unbounded output.
	if out.Options["num_predict"] != 256 {
		t.Errorf("Options[num_predict] = %v, want 256", out.Options["num_predict"])
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_ZeroAndUnsetParamsAreOmitted(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model:    "llama3",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		// Temperature nil, TopP/TopK/FrequencyPenalty/PresencePenalty/Seed all
		// zero-value (the Go zero value, indistinguishable from "explicitly
		// set to 0" — this is what the source guards against).
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, key := range []string{"temperature", "top_p", "top_k", "frequency_penalty", "presence_penalty", "seed", "num_predict"} {
		if _, ok := out.Options[key]; ok {
			t.Errorf("Options[%q] should be absent when unset, got %v", key, out.Options[key])
		}
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_ResponseFormatJSON(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model:          "llama3",
		Messages:       []dto.Message{{Role: "user", Content: "hi"}},
		ResponseFormat: &dto.ResponseFormat{Type: "json"},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Format != "json" {
		t.Errorf("Format = %v, want %q", out.Format, "json")
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_ResponseFormatJSONSchema(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	r := &dto.GeneralOpenAIRequest{
		Model:          "llama3",
		Messages:       []dto.Message{{Role: "user", Content: "hi"}},
		ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: schema},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	formatMap, ok := out.Format.(map[string]any)
	if !ok {
		t.Fatalf("Format = %v (%T), want map[string]any (parsed schema)", out.Format, out.Format)
	}
	if formatMap["type"] != "object" {
		t.Errorf("Format[type] = %v, want %q", formatMap["type"], "object")
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_StopSequenceVariants(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	tests := []struct {
		name string
		stop any
		want []string
	}{
		{"single string", "STOP", []string{"STOP"}},
		{"string slice", []string{"A", "B"}, []string{"A", "B"}},
		{"any slice with strings", []any{"X", "Y"}, []string{"X", "Y"}},
		{"any slice with non-string entries filtered", []any{"X", 5, "Y"}, []string{"X", "Y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &dto.GeneralOpenAIRequest{Model: "llama3", Messages: []dto.Message{{Role: "user", Content: "hi"}}, Stop: tt.stop}
			out, err := openAIChatToOllamaChat(c, r)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, ok := out.Options["stop"].([]string)
			if !ok {
				t.Fatalf("Options[stop] = %v (%T), want []string", out.Options["stop"], out.Options["stop"])
			}
			if len(got) != len(tt.want) {
				t.Fatalf("stop = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("stop[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_AnySliceAllNonStringOmitsStop(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r := &dto.GeneralOpenAIRequest{Model: "llama3", Messages: []dto.Message{{Role: "user", Content: "hi"}}, Stop: []any{1, 2, 3}}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out.Options["stop"]; ok {
		t.Errorf("Options[stop] should be absent when all entries are non-string, got %v", out.Options["stop"])
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_ToolsMapping(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model:    "llama3",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		Tools: []dto.ToolCallRequest{
			{Type: "function", Function: dto.FunctionRequest{Name: "get_weather", Description: "fetch weather", Parameters: map[string]any{"city": "string"}}},
		},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools, ok := out.Tools.([]OllamaTool)
	if !ok {
		t.Fatalf("Tools = %v (%T), want []OllamaTool", out.Tools, out.Tools)
	}
	if len(tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(tools))
	}
	if tools[0].Function.Name != "get_weather" || tools[0].Function.Description != "fetch weather" {
		t.Errorf("tool = %+v, want Name=get_weather Description='fetch weather'", tools[0])
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_NoToolsLeavesFieldNil(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r := &dto.GeneralOpenAIRequest{Model: "llama3", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Tools != nil {
		t.Errorf("Tools = %v, want nil when no tools present", out.Tools)
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_StringContentMessages(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model: "llama3",
		Messages: []dto.Message{
			{Role: "system", Content: "you are a helpful bot"},
			{Role: "user", Content: "what is 2+2?"},
		},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(out.Messages))
	}
	if out.Messages[0].Role != "system" || out.Messages[0].Content != "you are a helpful bot" {
		t.Errorf("Messages[0] = %+v", out.Messages[0])
	}
	if out.Messages[1].Role != "user" || out.Messages[1].Content != "what is 2+2?" {
		t.Errorf("Messages[1] = %+v", out.Messages[1])
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_EmptyMessagesProducesEmptySlice(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r := &dto.GeneralOpenAIRequest{Model: "llama3", Messages: []dto.Message{}}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 0 {
		t.Errorf("len(Messages) = %d, want 0 for empty input", len(out.Messages))
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_MultiPartContent_TextAndDataImage(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model: "llava",
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": dto.ContentTypeText, "text": "describe this"},
					map[string]any{"type": dto.ContentTypeImageURL, "image_url": map[string]any{"url": "data:image/png;base64,aGVsbG8="}},
				},
			},
		},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(out.Messages))
	}
	msg := out.Messages[0]
	if msg.Content != "describe this" {
		t.Errorf("Content = %q, want %q", msg.Content, "describe this")
	}
	if len(msg.Images) != 1 || msg.Images[0] != "aGVsbG8=" {
		t.Errorf("Images = %v, want [%q] (base64 payload after the data: URI comma)", msg.Images, "aGVsbG8=")
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_RawBase64ImageURL(t *testing.T) {
	// Neither http-prefixed nor data:-prefixed -> treated as a raw base64
	// payload verbatim (the "else" branch).
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model: "llava",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": dto.ContentTypeImageURL, "image_url": map[string]any{"url": "aGVsbG8gd29ybGQ="}},
			}},
		},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages[0].Images) != 1 || out.Messages[0].Images[0] != "aGVsbG8gd29ybGQ=" {
		t.Errorf("Images = %v, want raw payload passed through verbatim", out.Messages[0].Images)
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_EmptyContentArrayProducesEmptyText(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r := &dto.GeneralOpenAIRequest{
		Model:    "llama3",
		Messages: []dto.Message{{Role: "user", Content: []any{}}},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Messages[0].Content != "" {
		t.Errorf("Content = %q, want empty string for empty content array", out.Messages[0].Content)
	}
	if out.Messages[0].Images != nil {
		t.Errorf("Images = %v, want nil for empty content array", out.Messages[0].Images)
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_HttpImageURL_BlockedBySSRFProtectionByDefault(t *testing.T) {
	// This exercises the http-prefixed branch, which calls
	// app.GetFileBase64FromUrl -> DoDownloadRequest -> SSRF validation. With
	// default settings (EnableSSRFProtection=true, AllowPrivateIp=false) a
	// loopback httptest server is rejected, and that rejection must propagate
	// as an error from openAIChatToOllamaChat rather than being swallowed or
	// panicking.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-a-real-image"))
	}))
	defer upstream.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model: "llava",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": dto.ContentTypeImageURL, "image_url": map[string]any{"url": upstream.URL + "/image.png"}},
			}},
		},
	}
	_, err := openAIChatToOllamaChat(c, r)
	if err == nil {
		t.Fatal("expected an error: fetching an http(s) image URL against a loopback address must be blocked by default SSRF protection")
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_ToolResultMessage_NameAndToolCalls(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	toolName := "get_weather"
	toolCallsJSON, _ := json.Marshal([]dto.ToolCallRequest{
		{Type: "function", Function: dto.FunctionRequest{Name: "get_weather", Arguments: `{"city":"SF"}`}},
	})
	r := &dto.GeneralOpenAIRequest{
		Model: "llama3",
		Messages: []dto.Message{
			{Role: "assistant", Content: "", ToolCalls: toolCallsJSON},
			{Role: "tool", Content: "72F and sunny", Name: &toolName},
		},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(out.Messages))
	}
	assistantMsg := out.Messages[0]
	if len(assistantMsg.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(assistantMsg.ToolCalls))
	}
	if assistantMsg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("ToolCalls[0].Function.Name = %q, want %q", assistantMsg.ToolCalls[0].Function.Name, "get_weather")
	}
	argMap, ok := assistantMsg.ToolCalls[0].Function.Arguments.(map[string]any)
	if !ok || argMap["city"] != "SF" {
		t.Errorf("ToolCalls[0].Function.Arguments = %v, want map with city=SF", assistantMsg.ToolCalls[0].Function.Arguments)
	}
	toolMsg := out.Messages[1]
	if toolMsg.ToolName != "get_weather" {
		t.Errorf("ToolName = %q, want %q (set only for role=tool with Name set)", toolMsg.ToolName, "get_weather")
	}
}

func TestProvOllamaVolc_OpenAIChatToOllamaChat_ToolCallWithEmptyArguments_DefaultsToEmptyMap(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	toolCallsJSON, _ := json.Marshal([]dto.ToolCallRequest{
		{Type: "function", Function: dto.FunctionRequest{Name: "no_args_fn", Arguments: ""}},
	})
	r := &dto.GeneralOpenAIRequest{
		Model:    "llama3",
		Messages: []dto.Message{{Role: "assistant", Content: "", ToolCalls: toolCallsJSON}},
	}
	out, err := openAIChatToOllamaChat(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args, ok := out.Messages[0].ToolCalls[0].Function.Arguments.(map[string]any)
	if !ok {
		t.Fatalf("Arguments = %v (%T), want empty map[string]any", out.Messages[0].ToolCalls[0].Function.Arguments, out.Messages[0].ToolCalls[0].Function.Arguments)
	}
	if len(args) != 0 {
		t.Errorf("Arguments = %v, want empty map", args)
	}
}

// ---------------------------------------------------------------------------
// openAIToGenerate: /v1/completions request translation
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_OpenAIToGenerate_PromptVariants(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/completions", nil)

	tests := []struct {
		name   string
		prompt any
		want   string
	}{
		{"string prompt", "complete this", "complete this"},
		{"array of strings joined", []any{"a", "b", "c"}, "abc"},
		{"array with non-string entries skipped", []any{"a", 1, "b"}, "ab"},
		{"non-string non-array falls back to %v formatting", 42, "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &dto.GeneralOpenAIRequest{Model: "llama3", Prompt: tt.prompt}
			out, err := openAIToGenerate(c, r)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Prompt != tt.want {
				t.Errorf("Prompt = %q, want %q", out.Prompt, tt.want)
			}
		})
	}
}

func TestProvOllamaVolc_OpenAIToGenerate_NilPromptLeavesEmptyString(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/completions", nil)
	out, err := openAIToGenerate(c, &dto.GeneralOpenAIRequest{Model: "llama3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Prompt != "" {
		t.Errorf("Prompt = %q, want empty string when unset", out.Prompt)
	}
}

func TestProvOllamaVolc_OpenAIToGenerate_SuffixAndOptionsAndStop(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model:       "llama3",
		Prompt:      "prefix",
		Suffix:      "suffix-text",
		Temperature: floatPtr(0.2),
		TopP:        0.5,
		MaxTokens:   100,
		Stop:        "END",
	}
	out, err := openAIToGenerate(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Suffix != "suffix-text" {
		t.Errorf("Suffix = %q, want %q", out.Suffix, "suffix-text")
	}
	if out.Options["temperature"] == nil || *(out.Options["temperature"].(*float64)) != 0.2 {
		t.Errorf("Options[temperature] = %v, want 0.2", out.Options["temperature"])
	}
	if out.Options["top_p"] != 0.5 {
		t.Errorf("Options[top_p] = %v, want 0.5", out.Options["top_p"])
	}
	if out.Options["num_predict"] != 100 {
		t.Errorf("Options[num_predict] = %v, want 100", out.Options["num_predict"])
	}
	stop, ok := out.Options["stop"].([]string)
	if !ok || len(stop) != 1 || stop[0] != "END" {
		t.Errorf("Options[stop] = %v, want [END]", out.Options["stop"])
	}
}

func TestProvOllamaVolc_OpenAIToGenerate_NonStringSuffixIgnored(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/completions", nil)
	r := &dto.GeneralOpenAIRequest{Model: "llama3", Suffix: 12345}
	out, err := openAIToGenerate(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Suffix != "" {
		t.Errorf("Suffix = %q, want empty (non-string suffix is silently dropped)", out.Suffix)
	}
}

func TestProvOllamaVolc_OpenAIToGenerate_ResponseFormatJSONAndSchema(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/completions", nil)

	r1 := &dto.GeneralOpenAIRequest{Model: "llama3", ResponseFormat: &dto.ResponseFormat{Type: "json"}}
	out1, err := openAIToGenerate(c, r1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out1.Format != "json" {
		t.Errorf("Format = %v, want %q", out1.Format, "json")
	}

	schema := json.RawMessage(`{"type":"string"}`)
	r2 := &dto.GeneralOpenAIRequest{Model: "llama3", ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: schema}}
	out2, err := openAIToGenerate(c, r2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	formatMap, ok := out2.Format.(map[string]any)
	if !ok || formatMap["type"] != "string" {
		t.Errorf("Format = %v, want parsed schema map with type=string", out2.Format)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Embeddings: embedding request translation, single vs batch
// input shape (Ollama distinguishes a single scalar input from an array).
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_RequestOpenAI2Embeddings_SingleInputUnwrapped(t *testing.T) {
	r := dto.EmbeddingRequest{Model: "nomic-embed-text", Input: "only one string"}
	out := requestOpenAI2Embeddings(r)
	if out.Input != "only one string" {
		t.Errorf("Input = %v, want the bare string (not wrapped in a slice) for a single input", out.Input)
	}
}

func TestProvOllamaVolc_RequestOpenAI2Embeddings_MultipleInputsKeptAsSlice(t *testing.T) {
	r := dto.EmbeddingRequest{Model: "nomic-embed-text", Input: []any{"first", "second"}}
	out := requestOpenAI2Embeddings(r)
	slice, ok := out.Input.([]string)
	if !ok {
		t.Fatalf("Input = %v (%T), want []string for multiple inputs", out.Input, out.Input)
	}
	if len(slice) != 2 || slice[0] != "first" || slice[1] != "second" {
		t.Errorf("Input = %v, want [first second]", slice)
	}
}

func TestProvOllamaVolc_RequestOpenAI2Embeddings_ParamsMapped(t *testing.T) {
	r := dto.EmbeddingRequest{
		Model:            "nomic-embed-text",
		Input:            "x",
		Temperature:      floatPtr(0.1),
		TopP:             0.4,
		FrequencyPenalty: 0.2,
		PresencePenalty:  0.3,
		Seed:             7,
		Dimensions:       128,
	}
	out := requestOpenAI2Embeddings(r)
	if out.Dimensions != 128 {
		t.Errorf("Dimensions = %d, want 128", out.Dimensions)
	}
	if out.Options["seed"] != 7 {
		t.Errorf("Options[seed] = %v, want 7", out.Options["seed"])
	}
	if out.Options["dimensions"] != 128 {
		t.Errorf("Options[dimensions] = %v, want 128", out.Options["dimensions"])
	}
}

func TestProvOllamaVolc_RequestOpenAI2Embeddings_ZeroInputIsEmptySlice(t *testing.T) {
	r := dto.EmbeddingRequest{Model: "nomic-embed-text"}
	out := requestOpenAI2Embeddings(r)
	slice, ok := out.Input.([]string)
	if !ok {
		t.Fatalf("Input = %v (%T), want []string when there is no input", out.Input, out.Input)
	}
	if len(slice) != 0 {
		t.Errorf("Input = %v, want empty slice", slice)
	}
}
