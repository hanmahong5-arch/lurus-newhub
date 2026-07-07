package zhipu_4v

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	channelconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func newTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c
}

func TestGetRequestURL(t *testing.T) {
	defaultBase := channelconstant.ChannelBaseURLs[channelconstant.ChannelTypeZhipu_v4]

	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		wantURL string
	}{
		{
			name: "claude format uses default base",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{},
			},
			wantURL: defaultBase + "/api/anthropic/v1/messages",
		},
		{
			name: "claude format uses custom channel base url",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			},
			wantURL: "https://custom.example.com/api/anthropic/v1/messages",
		},
		{
			name: "claude format with special-plan base uses special ClaudeBaseURL",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "glm-coding-plan"},
			},
			wantURL: "https://open.bigmodel.cn/api/anthropic/v1/messages",
		},
		{
			name: "embeddings mode uses default base",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{},
			},
			wantURL: defaultBase + "/api/paas/v4/embeddings",
		},
		{
			name: "embeddings mode with special-plan base uses special OpenAIBaseURL",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "glm-coding-plan"},
			},
			wantURL: "https://open.bigmodel.cn/api/coding/paas/v4/embeddings",
		},
		{
			name: "images generations mode uses default base regardless of special plan",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeImagesGenerations,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "glm-coding-plan"},
			},
			wantURL: "glm-coding-plan/api/paas/v4/images/generations",
		},
		{
			name: "default mode (chat completions) uses default base",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{},
			},
			wantURL: defaultBase + "/api/paas/v4/chat/completions",
		},
		{
			name: "default mode with special-plan base uses special OpenAIBaseURL",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "kimi-coding-plan"},
			},
			wantURL: "https://api.kimi.com/coding/v1/chat/completions",
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantURL {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c := newTestGinContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-secret-key"}}
	header := http.Header{}

	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer sk-secret-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-secret-key")
	}
}

func TestSetupRequestHeaderStreamAccept(t *testing.T) {
	a := &Adaptor{}
	c := newTestGinContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-stream"},
		IsStream:    true,
	}
	header := http.Header{}

	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept = %q, want %q", got, "text/event-stream")
	}
}

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Fatalf("err = %v, want %q", err, "request is nil")
		}
	})

	t.Run("top_p >= 1 is clamped to 0.99", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "glm-4", TopP: 1}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if gotReq.TopP != 0.99 {
			t.Errorf("TopP = %v, want 0.99", gotReq.TopP)
		}
		if req.TopP != 0.99 {
			t.Errorf("original request TopP not mutated in-place: %v", req.TopP)
		}
	})

	t.Run("top_p below 1 is left unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "glm-4", TopP: 0.5}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		if gotReq.TopP != 0.5 {
			t.Errorf("TopP = %v, want 0.5", gotReq.TopP)
		}
	})

	t.Run("stop as string is wrapped into a single-element slice", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "glm-4", Stop: "STOP"}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		wantStop, ok := gotReq.Stop.([]string)
		if !ok || len(wantStop) != 1 || wantStop[0] != "STOP" {
			t.Errorf("Stop = %#v, want []string{\"STOP\"}", gotReq.Stop)
		}
	})

	t.Run("stop as []string slice is passed through", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "glm-4", Stop: []string{"A", "B"}}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		gotStop, ok := gotReq.Stop.([]string)
		if !ok || len(gotStop) != 2 || gotStop[0] != "A" || gotStop[1] != "B" {
			t.Errorf("Stop = %#v, want []string{\"A\",\"B\"}", gotReq.Stop)
		}
	})

	t.Run("stop of unsupported type resolves to nil slice", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "glm-4", Stop: 42}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		stopSlice, ok := gotReq.Stop.([]string)
		if !ok || stopSlice != nil {
			t.Errorf("Stop = %#v, want a nil []string", gotReq.Stop)
		}
	})

	t.Run("preserves scalar fields and copies over via requestOpenAI2Zhipu", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model:               "glm-4-plus",
			Temperature:         nil,
			MaxCompletionTokens: 128,
			ToolChoice:          "auto",
			Messages: []dto.Message{
				{Role: "user", Content: "hello"},
			},
		}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		if gotReq.Model != "glm-4-plus" {
			t.Errorf("Model = %q, want %q", gotReq.Model, "glm-4-plus")
		}
		if gotReq.MaxTokens != 128 {
			t.Errorf("MaxTokens = %d, want %d (from MaxCompletionTokens)", gotReq.MaxTokens, 128)
		}
		if gotReq.ToolChoice != "auto" {
			t.Errorf("ToolChoice = %v, want %q", gotReq.ToolChoice, "auto")
		}
		if len(gotReq.Messages) != 1 || gotReq.Messages[0].Role != "user" || gotReq.Messages[0].Content != "hello" {
			t.Errorf("Messages = %#v, unexpected", gotReq.Messages)
		}
	})

	t.Run("strips base64 data URL prefix from media image content", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model: "glm-4v",
			Messages: []dto.Message{
				{
					Role: "user",
					Content: []any{
						map[string]any{
							"type": dto.ContentTypeImageURL,
							"image_url": map[string]any{
								"url": "data:image/png;base64,QUJD",
							},
						},
					},
				},
			},
		}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		if len(gotReq.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(gotReq.Messages))
		}
		mediaList, ok := gotReq.Messages[0].Content.([]dto.MediaContent)
		if !ok {
			t.Fatalf("expected []dto.MediaContent content, got %T", gotReq.Messages[0].Content)
		}
		if len(mediaList) != 1 {
			t.Fatalf("expected 1 media content item, got %d", len(mediaList))
		}
		imageURL, ok := mediaList[0].ImageUrl.(*dto.MessageImageUrl)
		if !ok {
			t.Fatalf("expected *dto.MessageImageUrl, got %T", mediaList[0].ImageUrl)
		}
		if imageURL.Url != "QUJD" {
			t.Errorf("Url = %q, want stripped base64 payload %q", imageURL.Url, "QUJD")
		}
	})

	t.Run("non-data-url image content left unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model: "glm-4v",
			Messages: []dto.Message{
				{
					Role: "user",
					Content: []any{
						map[string]any{
							"type": dto.ContentTypeImageURL,
							"image_url": map[string]any{
								"url": "https://example.com/image.png",
							},
						},
					},
				},
			},
		}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		mediaList := gotReq.Messages[0].Content.([]dto.MediaContent)
		imageURL := mediaList[0].ImageUrl.(*dto.MessageImageUrl)
		if imageURL.Url != "https://example.com/image.png" {
			t.Errorf("Url = %q, want unchanged remote url", imageURL.Url)
		}
	})
}

func TestRequestOpenAI2ZhipuDirect(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:     "glm-4",
		Stream:    true,
		Stop:      "END",
		MaxTokens: 64,
		THINKING:  []byte(`{"type":"enabled"}`),
	}
	got := requestOpenAI2Zhipu(req)
	if got.Model != "glm-4" {
		t.Errorf("Model = %q, want %q", got.Model, "glm-4")
	}
	if !got.Stream {
		t.Errorf("Stream = false, want true")
	}
	if got.MaxTokens != 64 {
		t.Errorf("MaxTokens = %d, want 64", got.MaxTokens)
	}
	stop, ok := got.Stop.([]string)
	if !ok || len(stop) != 1 || stop[0] != "END" {
		t.Errorf("Stop = %#v, want []string{\"END\"}", got.Stop)
	}
	if string(got.THINKING) != `{"type":"enabled"}` {
		t.Errorf("THINKING = %s, want passthrough", string(got.THINKING))
	}
}

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	req := &dto.ClaudeRequest{Model: "glm-4", MaxTokens: 512}
	got, err := a.ConvertClaudeRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("expected *dto.ClaudeRequest passthrough, got %T", got)
	}
	if gotReq != req {
		t.Errorf("expected the same pointer to be returned unchanged")
	}
}

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.ImageRequest{Model: "cogview-3", Prompt: "a cat"}
	got, err := a.ConvertImageRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.ImageRequest)
	if !ok {
		t.Fatalf("expected dto.ImageRequest passthrough, got %T", got)
	}
	if gotReq.Prompt != "a cat" {
		t.Errorf("Prompt = %q, want %q", gotReq.Prompt, "a cat")
	}
}

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "embedding-2"}
	got, err := a.ConvertEmbeddingRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("expected dto.EmbeddingRequest passthrough, got %T", got)
	}
	if gotReq.Model != "embedding-2" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "embedding-2")
	}
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

func TestInit(t *testing.T) {
	a := &Adaptor{}
	// Init is a documented no-op; calling it must not panic even with nil info.
	a.Init(nil)
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(ModelList))
	}
	for i, m := range ModelList {
		if got[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], m)
		}
	}
	if got[0] != "glm-4" {
		t.Errorf("GetModelList()[0] = %q, want %q", got[0], "glm-4")
	}
	if got[len(got)-1] != "glm-4.6" {
		t.Errorf("GetModelList() last = %q, want %q", got[len(got)-1], "glm-4.6")
	}

	if name := a.GetChannelName(); name != "zhipu_4v" {
		t.Errorf("GetChannelName() = %q, want %q", name, "zhipu_4v")
	}
}

// --- zhipu4vImageHandler (image.go) ---

func respWithBody(status int, body string) *http.Response {
	r := httptest.NewRecorder()
	r.Code = status
	r.Body.WriteString(body)
	return r.Result()
}

func TestZhipu4vImageHandlerUnmarshalError(t *testing.T) {
	c := newTestGinContext()
	resp := respWithBody(http.StatusOK, "not-json")
	info := &relaycommon.RelayInfo{StartTime: time.Now()}

	usage, apiErr := zhipu4vImageHandler(c, resp, info)
	if usage != nil {
		t.Errorf("expected nil usage on unmarshal error, got %v", usage)
	}
	if apiErr == nil {
		t.Fatal("expected non-nil error on invalid JSON body")
	}
}

func TestZhipu4vImageHandlerUpstreamError(t *testing.T) {
	c := newTestGinContext()
	body := `{"error":{"code":"1234","message":"upstream failed"}}`
	resp := respWithBody(http.StatusBadRequest, body)
	info := &relaycommon.RelayInfo{StartTime: time.Now()}

	usage, apiErr := zhipu4vImageHandler(c, resp, info)
	if usage != nil {
		t.Errorf("expected nil usage on upstream error, got %v", usage)
	}
	if apiErr == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(apiErr.Error(), "upstream failed") {
		t.Errorf("error = %v, want to contain %q", apiErr, "upstream failed")
	}
}

func TestZhipu4vImageHandlerB64JsonSuccess(t *testing.T) {
	c := newTestGinContext()
	body := `{"created":1234567890,"data":[{"url":"https://img.example.com/a.png","b64_json":"AAAA"}]}`
	resp := respWithBody(http.StatusOK, body)
	info := &relaycommon.RelayInfo{StartTime: time.Now()}

	usage, apiErr := zhipu4vImageHandler(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
}

func TestZhipu4vImageHandlerB64ImageSuccess(t *testing.T) {
	c := newTestGinContext()
	body := `{"data":[{"image_url":"https://img.example.com/b.png","b64_image":"BBBB"}]}`
	resp := respWithBody(http.StatusOK, body)
	start := time.Now()
	info := &relaycommon.RelayInfo{StartTime: start}

	usage, apiErr := zhipu4vImageHandler(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
}

func TestZhipu4vImageHandlerMissingURLSkipsItem(t *testing.T) {
	c := newTestGinContext()
	// item has neither url nor image_url -> logged and skipped; no download attempted.
	body := `{"data":[{"b64_json":"CCCC"}]}`
	resp := respWithBody(http.StatusOK, body)
	info := &relaycommon.RelayInfo{StartTime: time.Now()}

	usage, apiErr := zhipu4vImageHandler(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage even when all items are skipped")
	}
}

func TestZhipu4vImageHandlerEmptyDataList(t *testing.T) {
	c := newTestGinContext()
	body := `{"created":42,"data":[]}`
	resp := respWithBody(http.StatusOK, body)
	info := &relaycommon.RelayInfo{StartTime: time.Now()}

	usage, apiErr := zhipu4vImageHandler(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
}

// NOTE: DoRequest and DoResponse both delegate to code paths that perform
// live HTTP I/O or require a fully-populated relay pipeline (real upstream
// response streaming via claude/openai handlers); they are not hermetically
// testable beyond what is already exercised indirectly above. Similarly, the
// zhipu4vImageHandler branch that downloads a remote image via
// app.GetImageFromUrl (when neither b64_json nor b64_image is present) needs
// live network access and is intentionally not covered here.
