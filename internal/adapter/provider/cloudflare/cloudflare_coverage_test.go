package cloudflare

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newGinContext(method, target string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, body)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		want string
	}{
		{
			name: "chat completions mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cloudflare.com", ApiVersion: "acct-1"},
			},
			want: "https://api.cloudflare.com/client/v4/accounts/acct-1/ai/v1/chat/completions",
		},
		{
			name: "embeddings mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cloudflare.com", ApiVersion: "acct-2"},
			},
			want: "https://api.cloudflare.com/client/v4/accounts/acct-2/ai/v1/embeddings",
		},
		{
			name: "responses mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeResponses,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cloudflare.com", ApiVersion: "acct-3"},
			},
			want: "https://api.cloudflare.com/client/v4/accounts/acct-3/ai/v1/responses",
		},
		{
			name: "default mode falls back to run/<model>",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cloudflare.com", ApiVersion: "acct-4", UpstreamModelName: "@cf/meta/llama-3.1-8b-instruct"},
			},
			want: "https://api.cloudflare.com/client/v4/accounts/acct-4/ai/run/@cf/meta/llama-3.1-8b-instruct",
		},
		{
			name: "zero-value relay mode also falls into default branch",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cloudflare.com", ApiVersion: "acct-5", UpstreamModelName: "@cf/microsoft/phi-2"},
			},
			want: "https://api.cloudflare.com/client/v4/accounts/acct-5/ai/run/@cf/microsoft/phi-2",
		},
	}

	a := &Adaptor{}
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

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	c, _ := newGinContext(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "cf-test-key"}}
	header := http.Header{}

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := header.Get("Authorization"); got != "Bearer cf-test-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer cf-test-key")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, nil)
		if err == nil {
			t.Fatal("expected error for nil request, got nil")
		}
		if err.Error() != "request is nil" {
			t.Errorf("error = %q, want %q", err.Error(), "request is nil")
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
	})

	t.Run("completions mode converts to CfRequest", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Prompt:      "hello world",
			MaxTokens:   42,
			Stream:      true,
			Temperature: common.GetPointer(0.5),
		}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeCompletions}
		got, err := a.ConvertOpenAIRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfReq, ok := got.(*CfRequest)
		if !ok {
			t.Fatalf("expected *CfRequest, got %T", got)
		}
		if cfReq.Prompt != "hello world" {
			t.Errorf("Prompt = %q, want %q", cfReq.Prompt, "hello world")
		}
		if cfReq.MaxTokens != 42 {
			t.Errorf("MaxTokens = %d, want 42", cfReq.MaxTokens)
		}
		if !cfReq.Stream {
			t.Error("Stream = false, want true")
		}
		if cfReq.Temperature == nil || *cfReq.Temperature != 0.5 {
			t.Errorf("Temperature = %v, want 0.5", cfReq.Temperature)
		}
	})

	t.Run("non-completions mode is passed through unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "@cf/meta/llama-3-8b-instruct"}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions}
		got, err := a.ConvertOpenAIRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if gotReq != req {
			t.Error("expected the same pointer to be returned")
		}
	})
}

// ---------------------------------------------------------------------------
// convertCf2CompletionsRequest (unexported helper)
// ---------------------------------------------------------------------------

func TestConvertCf2CompletionsRequest(t *testing.T) {
	t.Run("prompt as string", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Prompt:              "test prompt",
			MaxTokens:           10,
			MaxCompletionTokens: 20,
			Stream:              false,
			Temperature:         common.GetPointer(1.2),
		}
		got := convertCf2CompletionsRequest(req)
		if got.Prompt != "test prompt" {
			t.Errorf("Prompt = %q, want %q", got.Prompt, "test prompt")
		}
		// GetMaxTokens prefers MaxCompletionTokens when non-zero.
		if got.MaxTokens != 20 {
			t.Errorf("MaxTokens = %d, want 20 (MaxCompletionTokens should win)", got.MaxTokens)
		}
		if got.Stream {
			t.Error("Stream = true, want false")
		}
		if got.Temperature == nil || *got.Temperature != 1.2 {
			t.Errorf("Temperature = %v, want 1.2", got.Temperature)
		}
	})

	t.Run("prompt is non-string type becomes empty string", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Prompt: 12345, // not a string -> type assertion fails silently
		}
		got := convertCf2CompletionsRequest(req)
		if got.Prompt != "" {
			t.Errorf("Prompt = %q, want empty string for non-string prompt", got.Prompt)
		}
	})

	t.Run("nil prompt becomes empty string", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{}
		got := convertCf2CompletionsRequest(req)
		if got.Prompt != "" {
			t.Errorf("Prompt = %q, want empty string", got.Prompt)
		}
		if got.MaxTokens != 0 {
			t.Errorf("MaxTokens = %d, want 0", got.MaxTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertOpenAIResponsesRequest / ConvertRerankRequest / ConvertEmbeddingRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIResponsesRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.OpenAIResponsesRequest{Model: "@cf/meta/llama-3-8b-instruct"}
	got, err := a.ConvertOpenAIResponsesRequest(nil, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.OpenAIResponsesRequest)
	if !ok {
		t.Fatalf("expected dto.OpenAIResponsesRequest, got %T", got)
	}
	if gotReq.Model != "@cf/meta/llama-3-8b-instruct" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "@cf/meta/llama-3-8b-instruct")
	}
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.RerankRequest{Model: "@cf/rerank"}
	got, err := a.ConvertRerankRequest(nil, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.RerankRequest)
	if !ok {
		t.Fatalf("expected dto.RerankRequest, got %T", got)
	}
	if gotReq.Model != "@cf/rerank" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "@cf/rerank")
	}
}

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "@cf/embed"}
	got, err := a.ConvertEmbeddingRequest(nil, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("expected dto.EmbeddingRequest, got %T", got)
	}
	if gotReq.Model != "@cf/embed" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "@cf/embed")
	}
}

// ---------------------------------------------------------------------------
// ConvertAudioRequest — only the hermetically reachable error path (no file
// uploaded). The success path performs no network I/O either, but requires a
// real multipart form file which is exercised here too.
// ---------------------------------------------------------------------------

func TestConvertAudioRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("missing file returns error", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", strings.NewReader(""))
		c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=x")

		got, err := a.ConvertAudioRequest(c, &relaycommon.RelayInfo{}, dto.AudioRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "file is required" {
			t.Errorf("error = %v, want %q", err, "file is required")
		}
	})

	t.Run("uploaded file is copied into the request body", func(t *testing.T) {
		buf := &bytes.Buffer{}
		mw := multipart.NewWriter(buf)
		fw, err := mw.CreateFormFile("file", "audio.wav")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write([]byte("fake-audio-bytes")); err != nil {
			t.Fatalf("write form file: %v", err)
		}
		if err := mw.Close(); err != nil {
			t.Fatalf("close writer: %v", err)
		}

		c, _ := newGinContext(http.MethodPost, "/", buf)
		c.Request.Header.Set("Content-Type", mw.FormDataContentType())

		got, err := a.ConvertAudioRequest(c, &relaycommon.RelayInfo{}, dto.AudioRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotBody, err := io.ReadAll(got)
		if err != nil {
			t.Fatalf("read returned body: %v", err)
		}
		if string(gotBody) != "fake-audio-bytes" {
			t.Errorf("body = %q, want %q", string(gotBody), "fake-audio-bytes")
		}
	})
}

// ---------------------------------------------------------------------------
// DoResponse — exercised purely in-memory: hand-built *http.Response values,
// no network. Only the branches reachable this way are covered.
// ---------------------------------------------------------------------------

func TestDoResponseUnmatchedModeReturnsNil(t *testing.T) {
	c, _ := newGinContext(http.MethodPost, "/", nil)
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesGenerations}

	usage, err := a.DoResponse(c, nil, info)
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestDoResponseChatCompletionsNonStream(t *testing.T) {
	c, _ := newGinContext(http.MethodPost, "/", nil)
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "@cf/meta/llama-3-8b-instruct"}}

	body := `{"id":"resp-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}]}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}

	usage, err := a.DoResponse(c, resp, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
}

func TestDoResponseChatCompletionsStream(t *testing.T) {
	c, w := newGinContext(http.MethodPost, "/", nil)
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode: constant.RelayModeChatCompletions,
		IsStream:  true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "@cf/meta/llama-3-8b-instruct"},
	}
	sseBody := `data: {"choices":[{"delta":{"content":"hi"}}]}` + "\ndata: [DONE]\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sseBody)),
		Header:     http.Header{},
	}

	usage, err := a.DoResponse(c, resp, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want it to contain %q", ct, "text/event-stream")
	}
}

func TestDoResponseEmbeddingsBadBodyIsError(t *testing.T) {
	c, _ := newGinContext(http.MethodPost, "/", nil)
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("not-json")),
		Header:     http.Header{},
	}

	_, err := a.DoResponse(c, resp, info)
	if err == nil {
		t.Fatal("expected non-nil error for malformed embeddings response body")
	}
}

func TestDoResponseResponsesBadBodyIsError(t *testing.T) {
	c, _ := newGinContext(http.MethodPost, "/", nil)
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeResponses}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("not-json")),
		Header:     http.Header{},
	}

	_, err := a.DoResponse(c, resp, info)
	if err == nil {
		t.Fatal("expected non-nil error for malformed responses response body")
	}
}

func TestDoResponseAudioTranscriptionAndTranslation(t *testing.T) {
	a := &Adaptor{}
	body := `{"result":{"text":"transcribed text"}}`

	for _, mode := range []int{constant.RelayModeAudioTranscription, constant.RelayModeAudioTranslation} {
		c, _ := newGinContext(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{RelayMode: mode, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "@cf/openai/whisper"}}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{},
		}
		usage, err := a.DoResponse(c, resp, info)
		if err != nil {
			t.Fatalf("mode %d: unexpected error: %v", mode, err)
		}
		if usage == nil {
			t.Fatalf("mode %d: expected non-nil usage", mode)
		}
	}
}

// ---------------------------------------------------------------------------
// cfHandler (unexported)
// ---------------------------------------------------------------------------

func TestCfHandler(t *testing.T) {
	t.Run("valid body produces usage and writes JSON response", func(t *testing.T) {
		c, w := newGinContext(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "@cf/meta/llama-3-8b-instruct"}}
		body := `{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{},
		}

		apiErr, usage := cfHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
		if w.Code != http.StatusOK {
			t.Errorf("recorded status = %d, want %d", w.Code, http.StatusOK)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}
		if !strings.Contains(w.Body.String(), "hello") {
			t.Errorf("body = %q, want it to contain %q", w.Body.String(), "hello")
		}
	})

	t.Run("malformed body returns bad-response error", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "@cf/meta/llama-3-8b-instruct"}}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("not-json")),
			Header:     http.Header{},
		}

		apiErr, usage := cfHandler(c, info, resp)
		if apiErr == nil {
			t.Fatal("expected non-nil error for malformed body")
		}
		if usage != nil {
			t.Errorf("usage = %v, want nil", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// cfSTTHandler (unexported)
// ---------------------------------------------------------------------------

func TestCfSTTHandler(t *testing.T) {
	t.Run("valid body produces usage and writes audio response", func(t *testing.T) {
		c, w := newGinContext(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "@cf/openai/whisper"}}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"result":{"text":"transcribed"}}`)),
			Header:     http.Header{},
		}

		apiErr, usage := cfSTTHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
		if !strings.Contains(w.Body.String(), "transcribed") {
			t.Errorf("body = %q, want it to contain %q", w.Body.String(), "transcribed")
		}
	})

	t.Run("malformed body returns bad-response error", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "@cf/openai/whisper"}}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("not-json")),
			Header:     http.Header{},
		}

		apiErr, usage := cfSTTHandler(c, info, resp)
		if apiErr == nil {
			t.Fatal("expected non-nil error for malformed body")
		}
		if usage != nil {
			t.Errorf("usage = %v, want nil", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// cfStreamHandler (unexported)
// ---------------------------------------------------------------------------

func TestCfStreamHandler(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"He"}}]}`,
		"", // blank line separator
		`data: not-json`, // malformed line should be skipped, not fatal
		`data: {"choices":[{"delta":{"content":"llo"}}]}`,
		`data: [DONE]`,
	}, "\n")

	c, w := newGinContext(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{ShouldIncludeUsage: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "@cf/meta/llama-3-8b-instruct"}}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sseBody)),
		Header:     http.Header{},
	}

	apiErr, usage := cfStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.CompletionTokens == 0 {
		t.Error("expected non-zero CompletionTokens from streamed content")
	}
	// SSE headers must be set for a streaming response.
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want it to contain %q", ct, "text/event-stream")
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	gotModels := a.GetModelList()
	if len(gotModels) != len(ModelList) {
		t.Fatalf("GetModelList() length = %d, want %d", len(gotModels), len(ModelList))
	}
	for i, m := range ModelList {
		if gotModels[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, gotModels[i], m)
		}
	}
	if len(gotModels) != 33 {
		t.Errorf("len(ModelList) = %d, want 33", len(gotModels))
	}
	if gotModels[0] != "@cf/meta/llama-3.1-8b-instruct" {
		t.Errorf("ModelList[0] = %q, want %q", gotModels[0], "@cf/meta/llama-3.1-8b-instruct")
	}
	if gotModels[len(gotModels)-1] != "@hf/thebloke/zephyr-7b-beta-awq" {
		t.Errorf("last model = %q, want %q", gotModels[len(gotModels)-1], "@hf/thebloke/zephyr-7b-beta-awq")
	}

	if got := a.GetChannelName(); got != "cloudflare" {
		t.Errorf("GetChannelName() = %q, want %q", got, "cloudflare")
	}
	if ChannelName != "cloudflare" {
		t.Errorf("ChannelName = %q, want %q", ChannelName, "cloudflare")
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
