package siliconflow

import (
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"

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
	return c, rec
}

func httpNopCloser(body string) *nopReadCloser {
	return &nopReadCloser{r: strings.NewReader(body)}
}

type nopReadCloser struct {
	r *strings.Reader
}

func (n *nopReadCloser) Read(p []byte) (int, error) { return n.r.Read(p) }
func (n *nopReadCloser) Close() error                { return nil }

// ---------------------------------------------------------------------------
// GetChannelName / GetModelList / constants
// ---------------------------------------------------------------------------

func TestAdaptor_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "siliconflow" {
		t.Errorf("GetChannelName() = %q, want %q", got, "siliconflow")
	}
}

func TestChannelName(t *testing.T) {
	if ChannelName != "siliconflow" {
		t.Errorf("ChannelName = %q, want %q", ChannelName, "siliconflow")
	}
}

func TestAdaptor_GetModelList(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Fatalf("len(GetModelList()) = %d, want %d", len(got), len(ModelList))
	}
	for i, w := range ModelList {
		if got[i] != w {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], w)
		}
	}
	if got[0] != "THUDM/glm-4-9b-chat" {
		t.Errorf("GetModelList()[0] = %q, want %q", got[0], "THUDM/glm-4-9b-chat")
	}
}

func TestModelListNoDuplicates(t *testing.T) {
	seen := make(map[string]int, len(ModelList))
	for _, m := range ModelList {
		seen[m]++
	}
	for m, count := range seen {
		if count != 1 {
			t.Errorf("model %q appears %d times in ModelList, want 1", m, count)
		}
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	tests := []struct {
		name    string
		baseUrl string
		urlPath string
		mode    int
		want    string
	}{
		{"rerank mode ignores RequestURLPath", "https://api.siliconflow.cn", "/v1/whatever", constant.RelayModeRerank, "https://api.siliconflow.cn/v1/rerank"},
		{"chat completions uses request url path", "https://api.siliconflow.cn", "/v1/chat/completions", constant.RelayModeChatCompletions, "https://api.siliconflow.cn/v1/chat/completions"},
		{"embeddings mode falls through to path join", "https://api.siliconflow.cn", "/v1/embeddings", constant.RelayModeEmbeddings, "https://api.siliconflow.cn/v1/embeddings"},
		{"empty base url with rerank", "", "/ignored", constant.RelayModeRerank, "/v1/rerank"},
		{"trailing slash base url preserved as-is", "https://api.siliconflow.cn/", "/v1/chat/completions", constant.RelayModeChatCompletions, "https://api.siliconflow.cn//v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayMode:      tt.mode,
				RequestURLPath: tt.urlPath,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: tt.baseUrl},
			}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}

	t.Run("sets bearer auth and forwards content-type/accept", func(t *testing.T) {
		c, _ := newTestContext()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		c.Request = req

		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-key-123"}}
		header := &http.Header{}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer test-key-123" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key-123")
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
		if got := header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want %q", got, "application/json")
		}
	})

	t.Run("stream defaults Accept to text/event-stream when absent", func(t *testing.T) {
		c, _ := newTestContext()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Request = req

		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "streaming-key"}}
		header := &http.Header{}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want %q", got, "text/event-stream")
		}
		if got := header.Get("Authorization"); got != "Bearer streaming-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer streaming-key")
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.Init (no-op)
// ---------------------------------------------------------------------------

func TestAdaptor_Init(t *testing.T) {
	a := &Adaptor{}
	// Init is a documented no-op; calling it with a nil-safe info must not panic.
	a.Init(&relaycommon.RelayInfo{})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()

	t.Run("FIM request with prefix and no messages gets synthetic empty user message", func(t *testing.T) {
		prefix := "def foo("
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-ai/deepseek-llm-67b-chat", Prefix: &prefix}
		result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, ok := result.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest", result)
		}
		if len(got.Messages) != 1 || got.Messages[0].Role != "user" || got.Messages[0].Content != "" {
			t.Errorf("Messages = %+v, want single empty user message", got.Messages)
		}
	})

	t.Run("FIM request with suffix and no messages gets synthetic empty user message", func(t *testing.T) {
		suffix := ")"
		req := &dto.GeneralOpenAIRequest{Model: "m", Suffix: &suffix}
		result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := result.(*dto.GeneralOpenAIRequest)
		if len(got.Messages) != 1 {
			t.Errorf("len(Messages) = %d, want 1", len(got.Messages))
		}
	})

	t.Run("non-FIM request with existing messages is left untouched", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model:    "m",
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		}
		result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := result.(*dto.GeneralOpenAIRequest)
		if len(got.Messages) != 1 || got.Messages[0].Content != "hi" {
			t.Errorf("Messages = %+v, want unchanged single message", got.Messages)
		}
	})

	t.Run("FIM request with prefix but existing messages is left untouched", func(t *testing.T) {
		prefix := "x"
		req := &dto.GeneralOpenAIRequest{
			Model:    "m",
			Prefix:   &prefix,
			Messages: []dto.Message{{Role: "user", Content: "already here"}},
		}
		result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := result.(*dto.GeneralOpenAIRequest)
		if len(got.Messages) != 1 || got.Messages[0].Content != "already here" {
			t.Errorf("Messages = %+v, want unchanged", got.Messages)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertImageRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()

	t.Run("nil Extra falls back to standard size/n fields", func(t *testing.T) {
		req := dto.ImageRequest{Model: "black-forest-labs/FLUX.1-schnell", Prompt: "a cat", Size: "1024x1024", N: 2}
		result, err := a.ConvertImageRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, ok := result.(*SFImageRequest)
		if !ok {
			t.Fatalf("result type = %T, want *SFImageRequest", result)
		}
		if got.Model != "black-forest-labs/FLUX.1-schnell" || got.Prompt != "a cat" {
			t.Errorf("unexpected model/prompt: %+v", got)
		}
		if got.ImageSize != "1024x1024" {
			t.Errorf("ImageSize = %q, want %q (fallback from Size)", got.ImageSize, "1024x1024")
		}
		if got.BatchSize != 2 {
			t.Errorf("BatchSize = %d, want 2 (fallback from N)", got.BatchSize)
		}
	})

	t.Run("Extra with SiliconFlow-specific image_size takes precedence over Size", func(t *testing.T) {
		req := dto.ImageRequest{
			Model: "black-forest-labs/FLUX.1-schnell",
			Prompt: "a dog",
			Size:  "512x512",
			N:     1,
			Extra: map[string]json.RawMessage{
				"image_size": json.RawMessage(`"1536x1024"`),
				"batch_size": json.RawMessage(`4`),
				"seed":       json.RawMessage(`42`),
			},
		}
		result, err := a.ConvertImageRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := result.(*SFImageRequest)
		if got.ImageSize != "1536x1024" {
			t.Errorf("ImageSize = %q, want %q (from Extra, not Size)", got.ImageSize, "1536x1024")
		}
		if got.BatchSize != 4 {
			t.Errorf("BatchSize = %d, want 4 (from Extra, not N)", got.BatchSize)
		}
		if got.Seed != 42 {
			t.Errorf("Seed = %d, want 42", got.Seed)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertRerankRequest / ConvertEmbeddingRequest (pass-through)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	req := dto.RerankRequest{Query: "q", Model: "BAAI/bge-reranker-v2-m3", Documents: []any{"a", "b"}}
	result, err := a.ConvertRerankRequest(c, constant.RelayModeRerank, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := result.(dto.RerankRequest)
	if !ok {
		t.Fatalf("result type = %T, want dto.RerankRequest", result)
	}
	if got.Query != "q" || got.Model != "BAAI/bge-reranker-v2-m3" || len(got.Documents) != 2 {
		t.Errorf("unexpected passthrough result: %+v", got)
	}
}

func TestAdaptor_ConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	req := dto.EmbeddingRequest{Model: "BAAI/bge-m3", Input: "hello"}
	result, err := a.ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := result.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("result type = %T, want dto.EmbeddingRequest", result)
	}
	if got.Model != "BAAI/bge-m3" {
		t.Errorf("Model = %q, want %q", got.Model, "BAAI/bge-m3")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertClaudeRequest / ConvertAudioRequest (delegate to openai.Adaptor)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-5-sonnet",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       pkgconstant.ChannelTypeOpenAI,
			UpstreamModelName: "deepseek-ai/deepseek-llm-67b-chat",
		},
	}
	req := &dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	}
	result, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := result.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest", result)
	}
	if got.Model != "claude-3-5-sonnet" {
		t.Errorf("Model = %q, want %q (converted from ClaudeRequest.Model)", got.Model, "claude-3-5-sonnet")
	}
	if len(got.Messages) != 1 || got.Messages[0].StringContent() != "hi" {
		t.Errorf("Messages = %+v, want single user message with content %q", got.Messages, "hi")
	}
}

func TestAdaptor_ConvertAudioRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("speech mode marshals request body to JSON", func(t *testing.T) {
		c, _ := newTestContext()
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
		req := dto.AudioRequest{Model: "FunAudioLLM/SenseVoiceSmall", Input: "hi", Voice: "alloy", ResponseFormat: "json"}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech}
		reader, err := a.ConvertAudioRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reader == nil {
			t.Fatal("reader = nil, want non-nil JSON body")
		}
	})

	t.Run("transcription mode requires a real multipart request to build form body", func(t *testing.T) {
		origMax := pkgconstant.MaxRequestBodyMB
		pkgconstant.MaxRequestBodyMB = 10
		t.Cleanup(func() { pkgconstant.MaxRequestBodyMB = origMax })

		c, _ := newTestContext()
		var buf strings.Builder
		writer := multipart.NewWriter(&buf)
		_ = writer.WriteField("model", "FunAudioLLM/SenseVoiceSmall")
		fw, _ := writer.CreateFormFile("file", "audio.mp3")
		_, _ = fw.Write([]byte("fake-audio-bytes"))
		_ = writer.Close()

		httpReq := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(buf.String()))
		httpReq.Header.Set("Content-Type", writer.FormDataContentType())
		c.Request = httpReq

		req := dto.AudioRequest{Model: "FunAudioLLM/SenseVoiceSmall", ResponseFormat: "json"}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioTranscription}
		reader, err := a.ConvertAudioRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reader == nil {
			t.Fatal("reader = nil, want non-nil multipart body")
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse(t *testing.T) {
	a := &Adaptor{}

	t.Run("rerank mode dispatches to siliconflowRerankHandler", func(t *testing.T) {
		c, rec := newTestContext()
		body := `{"results":[{"index":0,"relevance_score":0.5}],"meta":{"tokens":{"input_tokens":5,"output_tokens":0}}}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}

		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		u, ok := usage.(*dto.Usage)
		if !ok {
			t.Fatalf("usage type = %T, want *dto.Usage", usage)
		}
		if u.PromptTokens != 5 {
			t.Errorf("PromptTokens = %d, want 5", u.PromptTokens)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("recorded status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("default mode delegates to openai.Adaptor.DoResponse for chat completion", func(t *testing.T) {
		c, _ := newTestContext()
		body := `{"id":"chatcmpl-1","object":"chat.completion","model":"deepseek-ai/deepseek-llm-67b-chat","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: httpNopCloser(body), Header: make(http.Header)}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}

		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage == nil {
			t.Fatal("usage = nil, want non-nil for successful chat completion")
		}
	})
}

// ---------------------------------------------------------------------------
// Not-implemented stubs
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertGeminiRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	result, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, &dto.GeminiChatRequest{})
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Errorf("err = %v, want %q", err, "not implemented")
	}
}

func TestAdaptor_ConvertOpenAIResponsesRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	result, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, dto.OpenAIResponsesRequest{})
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Errorf("err = %v, want %q", err, "not implemented")
	}
}

// ---------------------------------------------------------------------------
// siliconflowRerankHandler
// ---------------------------------------------------------------------------

func TestSiliconflowRerankHandler(t *testing.T) {
	t.Run("success path converts usage and writes rerank response body", func(t *testing.T) {
		c, rec := newTestContext()
		body := `{"results":[{"index":0,"relevance_score":0.87},{"index":1,"relevance_score":0.12}],"meta":{"tokens":{"input_tokens":15,"output_tokens":3}}}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser(body),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

		usage, apiErr := siliconflowRerankHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 15 || usage.CompletionTokens != 3 || usage.TotalTokens != 18 {
			t.Errorf("usage = %+v, want prompt=15 completion=3 total=18", usage)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("recorded status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
		var got dto.RerankResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("failed to unmarshal written body: %v", err)
		}
		if len(got.Results) != 2 || got.Results[0].RelevanceScore != 0.87 || got.Results[1].Index != 1 {
			t.Errorf("unexpected results: %+v", got.Results)
		}
		if got.Usage.TotalTokens != 18 {
			t.Errorf("written Usage.TotalTokens = %d, want 18", got.Usage.TotalTokens)
		}
	})

	t.Run("propagates non-200 status code to the recorder", func(t *testing.T) {
		c, rec := newTestContext()
		body := `{"results":[],"meta":{"tokens":{"input_tokens":0,"output_tokens":0}}}`
		resp := &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       httpNopCloser(body),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := siliconflowRerankHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.TotalTokens != 0 {
			t.Errorf("TotalTokens = %d, want 0", usage.TotalTokens)
		}
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("recorded status = %d, want %d", rec.Code, http.StatusTooManyRequests)
		}
	})

	t.Run("malformed json body returns bad_response_body error", func(t *testing.T) {
		c, _ := newTestContext()
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser("not json"),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := siliconflowRerankHandler(c, info, resp)
		if apiErr == nil {
			t.Fatal("expected error for malformed body")
		}
		if usage != nil {
			t.Errorf("usage = %+v, want nil on error", usage)
		}
	})
}
