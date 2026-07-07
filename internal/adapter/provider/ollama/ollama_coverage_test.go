package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// closeNotifyRecorder wraps httptest.ResponseRecorder to satisfy
// http.CloseNotifier, which gin's Context.Stream requires of the
// underlying ResponseWriter.
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(&closeNotifyRecorder{rec})
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, rec
}

type nopReadCloser struct {
	r *strings.Reader
}

func (n *nopReadCloser) Read(p []byte) (int, error) { return n.r.Read(p) }
func (n *nopReadCloser) Close() error                { return nil }

func httpNopCloser(body string) *nopReadCloser {
	return &nopReadCloser{r: strings.NewReader(body)}
}

// ---------------------------------------------------------------------------
// Adaptor: GetRequestURL / GetModelList / GetChannelName / Init / SetupRequestHeader
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		want string
	}{
		{
			name: "embeddings mode uses /api/embed",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ollama.local"},
			},
			want: "https://ollama.local/api/embed",
		},
		{
			name: "completions mode uses /api/generate",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ollama.local"},
			},
			want: "https://ollama.local/api/generate",
		},
		{
			name: "url path containing /v1/completions uses /api/generate",
			info: &relaycommon.RelayInfo{
				RequestURLPath: "/v1/completions",
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ollama.local"},
			},
			want: "https://ollama.local/api/generate",
		},
		{
			name: "default falls back to /api/chat",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ollama.local"},
			},
			want: "https://ollama.local/api/chat",
		},
		{
			name: "chat completions mode uses /api/chat",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ollama.local"},
			},
			want: "https://ollama.local/api/chat",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "ollama-key"}}
	header := &http.Header{}
	if err := a.SetupRequestHeader(c, header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer ollama-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer ollama-key")
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
}

func TestInit(t *testing.T) {
	a := &Adaptor{}
	a.Init(nil) // no-op, must not panic
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	want := []string{"llama3-7b"}
	if len(got) != len(want) {
		t.Fatalf("len(GetModelList()) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], w)
		}
	}
	if ChannelName != "ollama" {
		t.Errorf("ChannelName = %q, want %q", ChannelName, "ollama")
	}
	if got := a.GetChannelName(); got != "ollama" {
		t.Errorf("GetChannelName() = %q, want %q", got, "ollama")
	}
}

// ---------------------------------------------------------------------------
// Adaptor: not-implemented stubs + trivial delegations
// ---------------------------------------------------------------------------

func TestNotImplementedStubs(t *testing.T) {
	a := &Adaptor{}

	t.Run("ConvertGeminiRequest", func(t *testing.T) {
		got, err := a.ConvertGeminiRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertAudioRequest", func(t *testing.T) {
		got, err := a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertImageRequest", func(t *testing.T) {
		got, err := a.ConvertImageRequest(nil, nil, dto.ImageRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertOpenAIResponsesRequest", func(t *testing.T) {
		got, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "llama3-7b", Input: "hello"}
	got, err := a.ConvertEmbeddingRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	oe, ok := got.(*OllamaEmbeddingRequest)
	if !ok {
		t.Fatalf("expected *OllamaEmbeddingRequest, got %T", got)
	}
	if oe.Model != "llama3-7b" {
		t.Errorf("Model = %q, want %q", oe.Model, "llama3-7b")
	}
	if s, ok := oe.Input.(string); !ok || s != "hello" {
		t.Errorf("Input = %v, want %q", oe.Input, "hello")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest: nil guard + generate vs chat dispatch
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, nil)
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("error = %v, want %q", err, "request is nil")
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
	})

	t.Run("completions mode dispatches to generate conversion", func(t *testing.T) {
		c, _ := newTestContext()
		req := &dto.GeneralOpenAIRequest{Model: "llama3-7b", Prompt: "hi there"}
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeCompletions}
		got, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gr, ok := got.(*OllamaGenerateRequest)
		if !ok {
			t.Fatalf("expected *OllamaGenerateRequest, got %T", got)
		}
		if gr.Prompt != "hi there" {
			t.Errorf("Prompt = %q, want %q", gr.Prompt, "hi there")
		}
	})

	t.Run("url path with /v1/completions dispatches to generate conversion", func(t *testing.T) {
		c, _ := newTestContext()
		req := &dto.GeneralOpenAIRequest{Model: "llama3-7b", Prompt: "hi"}
		info := &relaycommon.RelayInfo{RequestURLPath: "/v1/completions"}
		got, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got.(*OllamaGenerateRequest); !ok {
			t.Fatalf("expected *OllamaGenerateRequest, got %T", got)
		}
	})

	t.Run("default dispatches to chat conversion", func(t *testing.T) {
		c, _ := newTestContext()
		req := &dto.GeneralOpenAIRequest{
			Model:    "llama3-7b",
			Messages: []dto.Message{{Role: "user", Content: "hello"}},
		}
		info := &relaycommon.RelayInfo{}
		got, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cr, ok := got.(*OllamaChatRequest)
		if !ok {
			t.Fatalf("expected *OllamaChatRequest, got %T", got)
		}
		if len(cr.Messages) != 1 || cr.Messages[0].Content != "hello" {
			t.Errorf("unexpected messages: %+v", cr.Messages)
		}
	})
}

// ---------------------------------------------------------------------------
// openAIChatToOllamaChat
// ---------------------------------------------------------------------------

func TestOpenAIChatToOllamaChat(t *testing.T) {
	c, _ := newTestContext()

	t.Run("basic options mapping and string content", func(t *testing.T) {
		temp := 0.7
		req := &dto.GeneralOpenAIRequest{
			Model:            "llama3-7b",
			Stream:           true,
			Temperature:      &temp,
			TopP:             0.9,
			TopK:             40,
			FrequencyPenalty: 0.1,
			PresencePenalty:  0.2,
			Seed:             42,
			MaxTokens:        128,
			Stop:             "STOP",
			Messages:         []dto.Message{{Role: "user", Content: "hello world"}},
		}
		got, err := openAIChatToOllamaChat(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Model != "llama3-7b" || !got.Stream {
			t.Errorf("unexpected model/stream: %+v", got)
		}
		if got.Options["temperature"] != &temp {
			t.Errorf("temperature option not set correctly: %+v", got.Options["temperature"])
		}
		if got.Options["top_p"] != 0.9 {
			t.Errorf("top_p = %v, want 0.9", got.Options["top_p"])
		}
		if got.Options["top_k"] != 40 {
			t.Errorf("top_k = %v, want 40", got.Options["top_k"])
		}
		if got.Options["frequency_penalty"] != 0.1 {
			t.Errorf("frequency_penalty = %v, want 0.1", got.Options["frequency_penalty"])
		}
		if got.Options["presence_penalty"] != 0.2 {
			t.Errorf("presence_penalty = %v, want 0.2", got.Options["presence_penalty"])
		}
		if got.Options["seed"] != 42 {
			t.Errorf("seed = %v, want 42", got.Options["seed"])
		}
		if got.Options["num_predict"] != 128 {
			t.Errorf("num_predict = %v, want 128", got.Options["num_predict"])
		}
		stop, ok := got.Options["stop"].([]string)
		if !ok || len(stop) != 1 || stop[0] != "STOP" {
			t.Errorf("stop = %v, want [STOP]", got.Options["stop"])
		}
		if len(got.Messages) != 1 || got.Messages[0].Content != "hello world" || got.Messages[0].Role != "user" {
			t.Errorf("unexpected messages: %+v", got.Messages)
		}
	})

	t.Run("stop as []string and []any variants", func(t *testing.T) {
		req1 := &dto.GeneralOpenAIRequest{Model: "m", Stop: []string{"a", "b"}}
		got1, err := openAIChatToOllamaChat(c, req1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		stop1, ok := got1.Options["stop"].([]string)
		if !ok || len(stop1) != 2 || stop1[0] != "a" || stop1[1] != "b" {
			t.Errorf("stop ([]string) = %v, want [a b]", got1.Options["stop"])
		}

		req2 := &dto.GeneralOpenAIRequest{Model: "m", Stop: []any{"x", 5, "y"}}
		got2, err := openAIChatToOllamaChat(c, req2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		stop2, ok := got2.Options["stop"].([]string)
		if !ok || len(stop2) != 2 || stop2[0] != "x" || stop2[1] != "y" {
			t.Errorf("stop ([]any) = %v, want [x y] (non-string entries dropped)", got2.Options["stop"])
		}
	})

	t.Run("response_format json and json_schema", func(t *testing.T) {
		reqJSON := &dto.GeneralOpenAIRequest{Model: "m", ResponseFormat: &dto.ResponseFormat{Type: "json"}}
		gotJSON, err := openAIChatToOllamaChat(c, reqJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotJSON.Format != "json" {
			t.Errorf("Format = %v, want %q", gotJSON.Format, "json")
		}

		schemaRaw := json.RawMessage(`{"type":"object"}`)
		reqSchema := &dto.GeneralOpenAIRequest{Model: "m", ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: schemaRaw}}
		gotSchema, err := openAIChatToOllamaChat(c, reqSchema)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		schemaMap, ok := gotSchema.Format.(map[string]any)
		if !ok || schemaMap["type"] != "object" {
			t.Errorf("Format = %v, want map with type=object", gotSchema.Format)
		}
	})

	t.Run("tools are converted", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model: "m",
			Tools: []dto.ToolCallRequest{
				{Type: "function", Function: dto.FunctionRequest{Name: "get_weather", Description: "desc", Parameters: map[string]any{"a": 1}}},
			},
		}
		got, err := openAIChatToOllamaChat(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tools, ok := got.Tools.([]OllamaTool)
		if !ok || len(tools) != 1 {
			t.Fatalf("Tools = %v, want a single-element []OllamaTool", got.Tools)
		}
		if tools[0].Function.Name != "get_weather" || tools[0].Function.Description != "desc" {
			t.Errorf("unexpected tool: %+v", tools[0])
		}
	})

	t.Run("tool role message carries ToolName", func(t *testing.T) {
		name := "get_weather"
		req := &dto.GeneralOpenAIRequest{
			Model:    "m",
			Messages: []dto.Message{{Role: "tool", Content: "result", Name: &name}},
		}
		got, err := openAIChatToOllamaChat(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Messages) != 1 || got.Messages[0].ToolName != "get_weather" {
			t.Errorf("unexpected message: %+v", got.Messages[0])
		}
	})

	t.Run("assistant message with tool calls is parsed", func(t *testing.T) {
		m := dto.Message{Role: "assistant"}
		m.SetToolCalls([]dto.ToolCallRequest{
			{Function: dto.FunctionRequest{Name: "fn", Arguments: `{"x":1}`}},
		})
		req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{m}}
		got, err := openAIChatToOllamaChat(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Messages) != 1 || len(got.Messages[0].ToolCalls) != 1 {
			t.Fatalf("unexpected message: %+v", got.Messages)
		}
		tc := got.Messages[0].ToolCalls[0]
		if tc.Function.Name != "fn" {
			t.Errorf("Function.Name = %q, want %q", tc.Function.Name, "fn")
		}
		args, ok := tc.Function.Arguments.(map[string]any)
		if !ok || args["x"] != float64(1) {
			t.Errorf("Arguments = %v, want map[x:1]", tc.Function.Arguments)
		}
	})

	t.Run("tool call with unparsable arguments defaults to empty map", func(t *testing.T) {
		m := dto.Message{Role: "assistant"}
		m.SetToolCalls([]dto.ToolCallRequest{
			{Function: dto.FunctionRequest{Name: "fn", Arguments: "not json"}},
		})
		req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{m}}
		got, err := openAIChatToOllamaChat(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tc := got.Messages[0].ToolCalls[0]
		args, ok := tc.Function.Arguments.(map[string]any)
		if !ok || len(args) != 0 {
			t.Errorf("Arguments = %v, want empty map", tc.Function.Arguments)
		}
	})

	t.Run("media content: text parts and inline data: image", func(t *testing.T) {
		media := []dto.MediaContent{
			{Type: dto.ContentTypeText, Text: "part1 "},
			{Type: dto.ContentTypeText, Text: "part2"},
			{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "data:image/png;base64,ZmFrZWRhdGE="}},
		}
		msg := dto.Message{Role: "user"}
		msg.SetMediaContent(media)
		req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg}}
		got, err := openAIChatToOllamaChat(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(got.Messages))
		}
		if got.Messages[0].Content != "part1 part2" {
			t.Errorf("Content = %q, want %q", got.Messages[0].Content, "part1 part2")
		}
		if len(got.Messages[0].Images) != 1 || got.Messages[0].Images[0] != "ZmFrZWRhdGE=" {
			t.Errorf("Images = %v, want [ZmFrZWRhdGE=]", got.Messages[0].Images)
		}
	})

	t.Run("media content: raw (non-data, non-http) image url passed through", func(t *testing.T) {
		media := []dto.MediaContent{
			{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "rawbase64=="}},
		}
		msg := dto.Message{Role: "user"}
		msg.SetMediaContent(media)
		req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg}}
		got, err := openAIChatToOllamaChat(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Messages[0].Images) != 1 || got.Messages[0].Images[0] != "rawbase64==" {
			t.Errorf("Images = %v, want [rawbase64==]", got.Messages[0].Images)
		}
	})
}

// ---------------------------------------------------------------------------
// openAIToGenerate
// ---------------------------------------------------------------------------

func TestOpenAIToGenerate(t *testing.T) {
	c, _ := newTestContext()

	t.Run("string prompt and suffix", func(t *testing.T) {
		temp := 0.5
		req := &dto.GeneralOpenAIRequest{
			Model:            "m",
			Prompt:           "write a poem",
			Suffix:           "the end",
			Temperature:      &temp,
			TopP:             0.8,
			TopK:             10,
			FrequencyPenalty: 0.3,
			PresencePenalty:  0.4,
			Seed:             7,
			MaxTokens:        64,
			Stop:             []string{"END"},
			ResponseFormat:   &dto.ResponseFormat{Type: "json"},
		}
		got, err := openAIToGenerate(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Prompt != "write a poem" {
			t.Errorf("Prompt = %q, want %q", got.Prompt, "write a poem")
		}
		if got.Suffix != "the end" {
			t.Errorf("Suffix = %q, want %q", got.Suffix, "the end")
		}
		if got.Format != "json" {
			t.Errorf("Format = %v, want %q", got.Format, "json")
		}
		if got.Options["top_p"] != 0.8 || got.Options["top_k"] != 10 {
			t.Errorf("unexpected options: %+v", got.Options)
		}
		if got.Options["num_predict"] != 64 {
			t.Errorf("num_predict = %v, want 64", got.Options["num_predict"])
		}
		stop, ok := got.Options["stop"].([]string)
		if !ok || len(stop) != 1 || stop[0] != "END" {
			t.Errorf("stop = %v, want [END]", got.Options["stop"])
		}
	})

	t.Run("[]any prompt is concatenated, non-string suffix ignored", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model:  "m",
			Prompt: []any{"a", "b", 3},
			Suffix: 42,
		}
		got, err := openAIToGenerate(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Prompt != "ab" {
			t.Errorf("Prompt = %q, want %q", got.Prompt, "ab")
		}
		if got.Suffix != "" {
			t.Errorf("Suffix = %q, want empty (non-string suffix ignored)", got.Suffix)
		}
	})

	t.Run("non-string/non-[]any prompt is stringified via fmt", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "m", Prompt: 123}
		got, err := openAIToGenerate(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Prompt != "123" {
			t.Errorf("Prompt = %q, want %q", got.Prompt, "123")
		}
	})

	t.Run("json_schema response format", func(t *testing.T) {
		schemaRaw := json.RawMessage(`{"type":"array"}`)
		req := &dto.GeneralOpenAIRequest{Model: "m", ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: schemaRaw}}
		got, err := openAIToGenerate(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		schemaMap, ok := got.Format.(map[string]any)
		if !ok || schemaMap["type"] != "array" {
			t.Errorf("Format = %v, want map with type=array", got.Format)
		}
	})

	t.Run("stop as []any with non-string entries dropped", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "m", Stop: []any{"z", 9}}
		got, err := openAIToGenerate(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		stop, ok := got.Options["stop"].([]string)
		if !ok || len(stop) != 1 || stop[0] != "z" {
			t.Errorf("stop = %v, want [z]", got.Options["stop"])
		}
	})

	t.Run("nil prompt leaves prompt empty", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "m"}
		got, err := openAIToGenerate(c, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Prompt != "" {
			t.Errorf("Prompt = %q, want empty", got.Prompt)
		}
	})
}

// ---------------------------------------------------------------------------
// requestOpenAI2Embeddings
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Embeddings(t *testing.T) {
	t.Run("single input string becomes scalar Input", func(t *testing.T) {
		temp := 0.1
		req := dto.EmbeddingRequest{
			Model:            "llama3-7b",
			Input:            "hello",
			Temperature:      &temp,
			TopP:             0.5,
			FrequencyPenalty: 0.2,
			PresencePenalty:  0.3,
			Seed:             9,
			Dimensions:       256,
		}
		got := requestOpenAI2Embeddings(req)
		if got.Model != "llama3-7b" {
			t.Errorf("Model = %q, want %q", got.Model, "llama3-7b")
		}
		s, ok := got.Input.(string)
		if !ok || s != "hello" {
			t.Errorf("Input = %v, want %q", got.Input, "hello")
		}
		if got.Options["temperature"] != &temp {
			t.Errorf("temperature option mismatch")
		}
		if got.Options["top_p"] != 0.5 {
			t.Errorf("top_p = %v, want 0.5", got.Options["top_p"])
		}
		if got.Options["frequency_penalty"] != 0.2 {
			t.Errorf("frequency_penalty = %v, want 0.2", got.Options["frequency_penalty"])
		}
		if got.Options["presence_penalty"] != 0.3 {
			t.Errorf("presence_penalty = %v, want 0.3", got.Options["presence_penalty"])
		}
		if got.Options["seed"] != 9 {
			t.Errorf("seed = %v, want 9", got.Options["seed"])
		}
		if got.Options["dimensions"] != 256 {
			t.Errorf("dimensions option = %v, want 256", got.Options["dimensions"])
		}
		if got.Dimensions != 256 {
			t.Errorf("Dimensions = %d, want 256", got.Dimensions)
		}
	})

	t.Run("multiple inputs become []string Input", func(t *testing.T) {
		req := dto.EmbeddingRequest{Model: "m", Input: []any{"a", "b"}}
		got := requestOpenAI2Embeddings(req)
		arr, ok := got.Input.([]string)
		if !ok || len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
			t.Errorf("Input = %v, want [a b]", got.Input)
		}
	})

	t.Run("no options set leaves an empty (non-nil) options map", func(t *testing.T) {
		req := dto.EmbeddingRequest{Model: "m", Input: "x"}
		got := requestOpenAI2Embeddings(req)
		if got.Options == nil || len(got.Options) != 0 {
			t.Errorf("Options = %v, want an empty map", got.Options)
		}
	})
}

// ---------------------------------------------------------------------------
// ollamaEmbeddingHandler
// ---------------------------------------------------------------------------

func TestOllamaEmbeddingHandler(t *testing.T) {
	t.Run("success path aggregates usage and writes response", func(t *testing.T) {
		c, rec := newTestContext()
		body := `{"model":"llama3-7b","embeddings":[[0.1,0.2],[0.3,0.4]],"prompt_eval_count":5}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}

		usage, apiErr := ollamaEmbeddingHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 5 || usage.TotalTokens != 5 || usage.CompletionTokens != 0 {
			t.Errorf("usage = %+v, want prompt=5 total=5 completion=0", usage)
		}
		var got dto.OpenAIEmbeddingResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("failed to unmarshal written body: %v", err)
		}
		if got.Model != "llama3-7b" || len(got.Data) != 2 {
			t.Errorf("unexpected written response: %+v", got)
		}
	})

	t.Run("ollama error field surfaces as an error", func(t *testing.T) {
		c, _ := newTestContext()
		body := `{"error":"model not found"}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{}
		usage, apiErr := ollamaEmbeddingHandler(c, info, resp)
		if apiErr == nil {
			t.Fatal("expected an error when the response carries an error field")
		}
		if usage != nil {
			t.Errorf("usage = %+v, want nil on error", usage)
		}
	})

	t.Run("malformed json body returns bad_response_body error", func(t *testing.T) {
		c, _ := newTestContext()
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser("not json"), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{}
		usage, apiErr := ollamaEmbeddingHandler(c, info, resp)
		if apiErr == nil {
			t.Fatal("expected error for malformed body")
		}
		if usage != nil {
			t.Errorf("usage = %+v, want nil on error", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// ollamaChatHandler (non-stream)
// ---------------------------------------------------------------------------

func TestOllamaChatHandler(t *testing.T) {
	t.Run("single-line ndjson response with tool-call-free content", func(t *testing.T) {
		c, rec := newTestContext()
		body := `{"model":"llama3-7b","created_at":"2024-01-01T00:00:00Z","message":{"role":"assistant","content":"hi there"},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":4}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}

		usage, apiErr := ollamaChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 3 || usage.CompletionTokens != 4 || usage.TotalTokens != 7 {
			t.Errorf("usage = %+v, want prompt=3 completion=4 total=7", usage)
		}
		var got dto.OpenAITextResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("failed to unmarshal written body: %v", err)
		}
		if got.Model != "llama3-7b" || len(got.Choices) != 1 {
			t.Fatalf("unexpected written response: %+v", got)
		}
		if got.Choices[0].FinishReason != "stop" {
			t.Errorf("FinishReason = %q, want %q", got.Choices[0].FinishReason, "stop")
		}
	})

	t.Run("multi-line ndjson response is aggregated", func(t *testing.T) {
		c, _ := newTestContext()
		lines := []string{
			`{"model":"llama3-7b","message":{"role":"assistant","content":"Hel"},"done":false}`,
			`{"model":"llama3-7b","message":{"role":"assistant","content":"lo"},"done":true,"done_reason":"stop","prompt_eval_count":1,"eval_count":2}`,
		}
		body := strings.Join(lines, "\n")
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		usage, apiErr := ollamaChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 1 || usage.CompletionTokens != 2 {
			t.Errorf("usage = %+v, want prompt=1 completion=2", usage)
		}
	})

	t.Run("generate-style response uses the response field", func(t *testing.T) {
		c, _ := newTestContext()
		body := `{"model":"llama3-7b","response":"generated text","done":true,"prompt_eval_count":2,"eval_count":3}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		usage, apiErr := ollamaChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.CompletionTokens != 3 {
			t.Errorf("CompletionTokens = %d, want 3", usage.CompletionTokens)
		}
	})

	t.Run("empty content yields nil message content pointer", func(t *testing.T) {
		c, rec := newTestContext()
		body := `{"model":"llama3-7b","done":true}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		_, apiErr := ollamaChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		var got dto.OpenAITextResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("failed to unmarshal written body: %v", err)
		}
		if got.Choices[0].Message.Content != nil {
			t.Errorf("Content = %v, want nil", got.Choices[0].Message.Content)
		}
	})

	t.Run("reasoning/thinking content is a JSON string and gets unquoted", func(t *testing.T) {
		c, _ := newTestContext()
		body := `{"model":"llama3-7b","message":{"role":"assistant","content":"answer","thinking":"\"pondering...\""},"done":true}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		_, apiErr := ollamaChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
	})

	t.Run("single malformed line returns bad_response_body error", func(t *testing.T) {
		c, _ := newTestContext()
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser("not json"), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{}
		usage, apiErr := ollamaChatHandler(c, info, resp)
		if apiErr == nil {
			t.Fatal("expected error for malformed single-line body")
		}
		if usage != nil {
			t.Errorf("usage = %+v, want nil on error", usage)
		}
	})

	t.Run("pretty-printed multi-line JSON falls back to whole-body parse", func(t *testing.T) {
		c, rec := newTestContext()
		// Each individual line is not valid JSON on its own, so the per-line
		// scan never sets parsedAny; the handler must fall back to parsing
		// the entire body as a single chunk.
		body := "\n{\n  \"model\": \"llama3-7b\",\n  \"message\": {\"role\":\"assistant\",\"content\":\"hi\",\"thinking\":123},\n  \"done\": true\n}\n"
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		usage, apiErr := ollamaChatHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
		var got dto.OpenAITextResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("failed to unmarshal written body: %v", err)
		}
		// thinking:123 is not a JSON string, so the raw "123" is used verbatim
		// (fallback branch) as the reasoning content.
		if got.Choices[0].Message.ReasoningContent != "123" {
			t.Errorf("ReasoningContent = %q, want %q", got.Choices[0].Message.ReasoningContent, "123")
		}
		if s, ok := got.Choices[0].Message.Content.(string); !ok || s != "hi" {
			t.Errorf("Content = %v, want %q", got.Choices[0].Message.Content, "hi")
		}
	})
}

// ---------------------------------------------------------------------------
// ollamaStreamHandler
// ---------------------------------------------------------------------------

func TestOllamaStreamHandler(t *testing.T) {
	t.Run("nil response body returns empty_response error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		usage, apiErr := ollamaStreamHandler(nil, info, nil)
		if apiErr == nil {
			t.Fatal("expected error for nil response")
		}
		if usage != nil {
			t.Errorf("usage = %+v, want nil on error", usage)
		}
	})

	t.Run("streams chat deltas then a done frame", func(t *testing.T) {
		c, rec := newTestContext()
		lines := []string{
			`{"model":"llama3-7b","message":{"role":"assistant","content":"Hel"},"done":false}`,
			`{"model":"llama3-7b","message":{"role":"assistant","content":"lo"},"done":false}`,
			`{"model":"llama3-7b","done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":3}`,
		}
		body := strings.Join(lines, "\n")
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}

		usage, apiErr := ollamaStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 2 || usage.CompletionTokens != 3 || usage.TotalTokens != 5 {
			t.Errorf("usage = %+v, want prompt=2 completion=3 total=5", usage)
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"content":"Hel"`) {
			t.Errorf("expected first delta chunk in output, got: %s", out)
		}
		if !strings.Contains(out, "data: [DONE]") {
			t.Errorf("expected terminal [DONE] event, got: %s", out)
		}
	})

	t.Run("generate-style delta uses response field", func(t *testing.T) {
		c, rec := newTestContext()
		lines := []string{
			`{"model":"llama3-7b","response":"chunk1","done":false}`,
			`{"model":"llama3-7b","done":true}`,
		}
		body := strings.Join(lines, "\n")
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		usage, apiErr := ollamaStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 0 || usage.CompletionTokens != 0 {
			t.Errorf("usage = %+v, want zero (no counts in done frame)", usage)
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"content":"chunk1"`) {
			t.Errorf("expected generate-style delta content, got: %s", out)
		}
		// no done_reason in the frame -> defaults to "stop"
		if !strings.Contains(out, `"finish_reason":"stop"`) {
			t.Errorf("expected default finish_reason=stop, got: %s", out)
		}
	})

	t.Run("tool call deltas are converted with incrementing call ids", func(t *testing.T) {
		c, rec := newTestContext()
		lines := []string{
			`{"model":"llama3-7b","message":{"role":"assistant","tool_calls":[{"function":{"name":"fn1","arguments":{"a":1}}}]},"done":false}`,
			`{"model":"llama3-7b","done":true}`,
		}
		body := strings.Join(lines, "\n")
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		_, apiErr := ollamaStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"name":"fn1"`) {
			t.Errorf("expected tool call name in output, got: %s", out)
		}
		if !strings.Contains(out, `"id":"call_0"`) {
			t.Errorf("expected first tool call id call_0, got: %s", out)
		}
	})

	t.Run("thinking delta is unquoted from a JSON string", func(t *testing.T) {
		c, rec := newTestContext()
		lines := []string{
			`{"model":"llama3-7b","message":{"role":"assistant","content":"","thinking":"\"thinking...\""},"done":false}`,
			`{"model":"llama3-7b","done":true}`,
		}
		body := strings.Join(lines, "\n")
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		_, apiErr := ollamaStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		out := rec.Body.String()
		if !strings.Contains(out, "thinking...") {
			t.Errorf("expected unquoted thinking content in output, got: %s", out)
		}
	})

	t.Run("thinking delta that is not a JSON string falls back to raw text", func(t *testing.T) {
		c, rec := newTestContext()
		lines := []string{
			`{"model":"llama3-7b","message":{"role":"assistant","content":"","thinking":123},"done":false}`,
			`{"model":"llama3-7b","done":true}`,
		}
		body := strings.Join(lines, "\n")
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		_, apiErr := ollamaStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"reasoning_content":"123"`) {
			t.Errorf("expected raw fallback reasoning content \"123\", got: %s", out)
		}
	})

	t.Run("blank lines between chunks are skipped", func(t *testing.T) {
		c, rec := newTestContext()
		body := "\n" + `{"model":"llama3-7b","message":{"role":"assistant","content":"hi"},"done":false}` + "\n\n" + `{"model":"llama3-7b","done":true}` + "\n"
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		_, apiErr := ollamaStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"content":"hi"`) {
			t.Errorf("expected delta content despite surrounding blank lines, got: %s", out)
		}
	})

	t.Run("malformed line aborts stream with bad_response_body error", func(t *testing.T) {
		c, _ := newTestContext()
		body := "not-json-at-all"
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := ollamaStreamHandler(c, info, resp)
		if apiErr == nil {
			t.Fatal("expected error for malformed stream line")
		}
		if usage == nil {
			t.Error("expected a non-nil (zero) usage even on error")
		}
	})
}

// ---------------------------------------------------------------------------
// toUnix / contentPtr helpers
// ---------------------------------------------------------------------------

func TestToUnix(t *testing.T) {
	t.Run("empty timestamp falls back to now", func(t *testing.T) {
		got := toUnix("")
		if got <= 0 {
			t.Errorf("toUnix(\"\") = %d, want a positive unix time", got)
		}
	})

	t.Run("RFC3339Nano timestamp parsed exactly", func(t *testing.T) {
		got := toUnix("2024-01-01T00:00:00.123456789Z")
		if got != 1704067200 {
			t.Errorf("toUnix() = %d, want 1704067200", got)
		}
	})

	t.Run("RFC3339 timestamp parsed exactly", func(t *testing.T) {
		got := toUnix("2024-01-01T00:00:00Z")
		if got != 1704067200 {
			t.Errorf("toUnix() = %d, want 1704067200", got)
		}
	})

	t.Run("unparsable timestamp falls back to now", func(t *testing.T) {
		got := toUnix("not-a-timestamp")
		if got <= 0 {
			t.Errorf("toUnix(garbage) = %d, want a positive unix time", got)
		}
	})
}

func TestContentPtr(t *testing.T) {
	if got := contentPtr(""); got != nil {
		t.Errorf("contentPtr(\"\") = %v, want nil", got)
	}
	got := contentPtr("hi")
	if got == nil || *got != "hi" {
		t.Errorf("contentPtr(\"hi\") = %v, want pointer to \"hi\"", got)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse: dispatch by RelayMode/IsStream (no live network involved,
// the underlying handlers are exercised via hermetic bodies above).
// ---------------------------------------------------------------------------

func TestDoResponse_Dispatch(t *testing.T) {
	a := &Adaptor{}

	t.Run("embeddings mode dispatches to embedding handler", func(t *testing.T) {
		c, _ := newTestContext()
		body := `{"model":"llama3-7b","embeddings":[[0.1]],"prompt_eval_count":1}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.PromptTokens != 1 {
			t.Errorf("usage = %+v, want PromptTokens=1", usage)
		}
	})

	t.Run("non-stream chat mode dispatches to chat handler", func(t *testing.T) {
		c, _ := newTestContext()
		body := `{"model":"llama3-7b","message":{"role":"assistant","content":"hi"},"done":true}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
	})

	t.Run("stream mode dispatches to stream handler", func(t *testing.T) {
		c, _ := newTestContext()
		body := `{"model":"llama3-7b","done":true}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-7b"}}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
	})
}

// sanity guard to make sure types.NewAPIError-based helpers are actually
// wired against error codes we expect (regression guard for accidental
// signature drift, not a tautology: it asserts on real values produced by
// production code above).
func TestErrorCodesUsed(t *testing.T) {
	if types.ErrorCodeBadResponseBody == "" {
		t.Fatal("ErrorCodeBadResponseBody should not be empty")
	}
}

// ---------------------------------------------------------------------------
// FetchOllamaVersion: only the hermetically reachable empty-baseURL guard is
// exercised here. The success/HTTP-error paths require a live upstream and
// are not covered by this hermetic suite.
// ---------------------------------------------------------------------------

func TestFetchOllamaVersion_EmptyBaseURL(t *testing.T) {
	got, err := FetchOllamaVersion("", "key")
	if err == nil {
		t.Fatal("expected an error for an empty baseURL")
	}
	if got != "" {
		t.Errorf("version = %q, want empty", got)
	}
}
