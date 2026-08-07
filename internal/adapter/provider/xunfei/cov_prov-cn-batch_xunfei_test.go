package xunfei

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func prov_cn_batch_xunfeiGinContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

func ptrFloat64(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// requestOpenAI2Xunfei — request translation: system role handling, params
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Xunfei_SystemMessageConversion(t *testing.T) {
	t.Run("non-3.5 model splits system into user+assistant Okay", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model: "SparkDesk-v3.1",
			Messages: []dto.Message{
				{Role: "system", Content: "you are a helpful bot"},
				{Role: "user", Content: "hi"},
			},
		}
		out := requestOpenAI2Xunfei(req, "app1", "generalv3")
		if len(out.Payload.Message.Text) != 3 {
			t.Fatalf("expected 3 messages (system split into 2 + user), got %d: %+v", len(out.Payload.Message.Text), out.Payload.Message.Text)
		}
		if out.Payload.Message.Text[0].Role != "user" || out.Payload.Message.Text[0].Content != "you are a helpful bot" {
			t.Errorf("system message should become user message with original content, got %+v", out.Payload.Message.Text[0])
		}
		if out.Payload.Message.Text[1].Role != "assistant" || out.Payload.Message.Text[1].Content != "Okay" {
			t.Errorf("expected synthetic assistant Okay ack, got %+v", out.Payload.Message.Text[1])
		}
		if out.Payload.Message.Text[2].Role != "user" || out.Payload.Message.Text[2].Content != "hi" {
			t.Errorf("expected original user message preserved, got %+v", out.Payload.Message.Text[2])
		}
	})

	t.Run("3.5 model keeps system role verbatim", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model: "SparkDesk-v3.5",
			Messages: []dto.Message{
				{Role: "system", Content: "sys prompt"},
			},
		}
		out := requestOpenAI2Xunfei(req, "app1", "generalv3.5")
		if len(out.Payload.Message.Text) != 1 {
			t.Fatalf("3.5 models must NOT split system message, got %d messages: %+v", len(out.Payload.Message.Text), out.Payload.Message.Text)
		}
		if out.Payload.Message.Text[0].Role != "system" {
			t.Errorf("expected system role preserved for 3.5 models, got %q", out.Payload.Message.Text[0].Role)
		}
	})

	t.Run("multimodal content array reduces to text-only parts", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model: "SparkDesk-v3.1",
			Messages: []dto.Message{
				{Role: "user", Content: []any{
					map[string]any{"type": "text", "text": "look at this"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/img.png"}},
				}},
			},
		}
		out := requestOpenAI2Xunfei(req, "app1", "generalv3")
		if len(out.Payload.Message.Text) != 1 {
			t.Fatalf("expected single message, got %d", len(out.Payload.Message.Text))
		}
		if out.Payload.Message.Text[0].Content != "look at this" {
			t.Errorf("StringContent should extract only text parts and drop image blocks, got %q", out.Payload.Message.Text[0].Content)
		}
	})

	t.Run("empty messages produces empty text slice, not nil-panic", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{Model: "SparkDesk-v3.1", Messages: []dto.Message{}}
		out := requestOpenAI2Xunfei(req, "app1", "generalv3")
		if out.Payload.Message.Text == nil {
			t.Errorf("expected non-nil (possibly empty) message slice, got nil")
		}
		if len(out.Payload.Message.Text) != 0 {
			t.Errorf("expected zero messages for empty input, got %d", len(out.Payload.Message.Text))
		}
	})

	t.Run("header/parameter mapping: appid, domain, temperature, topK, maxTokens", func(t *testing.T) {
		temp := 0.7
		req := dto.GeneralOpenAIRequest{
			Model:       "SparkDesk-v3.1",
			Temperature: &temp,
			N:           3,
			MaxTokens:   256,
		}
		out := requestOpenAI2Xunfei(req, "my-app-id", "generalv3")
		if out.Header.AppId != "my-app-id" {
			t.Errorf("AppId = %q, want my-app-id", out.Header.AppId)
		}
		if out.Parameter.Chat.Domain != "generalv3" {
			t.Errorf("Domain = %q, want generalv3", out.Parameter.Chat.Domain)
		}
		if out.Parameter.Chat.Temperature == nil || *out.Parameter.Chat.Temperature != 0.7 {
			t.Errorf("Temperature not propagated, got %v", out.Parameter.Chat.Temperature)
		}
		if out.Parameter.Chat.TopK != 3 {
			t.Errorf("TopK (mapped from N) = %d, want 3", out.Parameter.Chat.TopK)
		}
		if out.Parameter.Chat.MaxTokens != 256 {
			t.Errorf("MaxTokens = %d, want 256 (GetMaxTokens fallback to MaxTokens)", out.Parameter.Chat.MaxTokens)
		}
	})

	t.Run("MaxCompletionTokens takes priority over MaxTokens", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model:               "SparkDesk-v3.1",
			MaxTokens:           100,
			MaxCompletionTokens: 50,
		}
		out := requestOpenAI2Xunfei(req, "app1", "generalv3")
		if out.Parameter.Chat.MaxTokens != 50 {
			t.Errorf("MaxTokens = %d, want 50 (MaxCompletionTokens must win per GetMaxTokens)", out.Parameter.Chat.MaxTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// responseXunfei2OpenAI — response translation + usage extraction (billing)
// ---------------------------------------------------------------------------

func TestResponseXunfei2OpenAI(t *testing.T) {
	t.Run("normal response maps content, role, finish reason and usage", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		resp.Payload.Choices.Text = []XunfeiChatResponseTextItem{{Content: "hello world"}}
		resp.Payload.Usage.Text.PromptTokens = 10
		resp.Payload.Usage.Text.CompletionTokens = 5
		resp.Payload.Usage.Text.TotalTokens = 15

		out := responseXunfei2OpenAI(resp)
		if len(out.Choices) != 1 {
			t.Fatalf("expected 1 choice, got %d", len(out.Choices))
		}
		if out.Choices[0].Message.Content != "hello world" {
			t.Errorf("content = %v, want hello world", out.Choices[0].Message.Content)
		}
		if out.Choices[0].Message.Role != "assistant" {
			t.Errorf("role = %q, want assistant", out.Choices[0].Message.Role)
		}
		if out.Choices[0].FinishReason != constant.FinishReasonStop {
			t.Errorf("finish reason = %q, want %q", out.Choices[0].FinishReason, constant.FinishReasonStop)
		}
		if out.Usage.PromptTokens != 10 || out.Usage.CompletionTokens != 5 || out.Usage.TotalTokens != 15 {
			t.Errorf("usage not preserved for billing: got %+v", out.Usage)
		}
	})

	t.Run("empty choices array degrades to empty content, no panic", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		out := responseXunfei2OpenAI(resp)
		if len(out.Choices) != 1 {
			t.Fatalf("expected default single choice, got %d", len(out.Choices))
		}
		if out.Choices[0].Message.Content != "" {
			t.Errorf("expected empty content fallback, got %v", out.Choices[0].Message.Content)
		}
	})
}

// ---------------------------------------------------------------------------
// streamResponseXunfei2OpenAI — streaming delta translation
// ---------------------------------------------------------------------------

func TestStreamResponseXunfei2OpenAI(t *testing.T) {
	t.Run("status 2 marks stop finish reason", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		resp.Payload.Choices.Text = []XunfeiChatResponseTextItem{{Content: "chunk"}}
		resp.Payload.Choices.Status = 2
		out := streamResponseXunfei2OpenAI(resp)
		if out.Choices[0].Delta.GetContentString() != "chunk" {
			t.Errorf("delta content = %q, want chunk", out.Choices[0].Delta.GetContentString())
		}
		if out.Choices[0].FinishReason == nil || *out.Choices[0].FinishReason != constant.FinishReasonStop {
			t.Errorf("expected stop finish reason at status 2, got %v", out.Choices[0].FinishReason)
		}
	})

	t.Run("mid-stream status leaves finish reason nil", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		resp.Payload.Choices.Text = []XunfeiChatResponseTextItem{{Content: "partial"}}
		resp.Payload.Choices.Status = 0
		out := streamResponseXunfei2OpenAI(resp)
		if out.Choices[0].FinishReason != nil {
			t.Errorf("mid-stream chunk must not set finish reason, got %v", *out.Choices[0].FinishReason)
		}
	})

	t.Run("empty text array defaults to empty content without panic", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		out := streamResponseXunfei2OpenAI(resp)
		if out.Choices[0].Delta.GetContentString() != "" {
			t.Errorf("expected empty content default, got %q", out.Choices[0].Delta.GetContentString())
		}
	})
}

// ---------------------------------------------------------------------------
// apiVersion2domain / getAPIVersion — model/version resolution
// ---------------------------------------------------------------------------

func TestApiVersion2Domain(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{"v1.1", "lite"},
		{"v2.1", "generalv2"},
		{"v3.1", "generalv3"},
		{"v3.5", "generalv3.5"},
		{"v4.0", "4.0Ultra"},
		{"v9.9", "generalv9.9"}, // unknown version falls back to "general"+version
	}
	for _, tt := range tests {
		if got := apiVersion2domain(tt.version); got != tt.want {
			t.Errorf("apiVersion2domain(%q) = %q, want %q", tt.version, got, tt.want)
		}
	}
}

func TestGetAPIVersion(t *testing.T) {
	t.Run("query param takes precedence", func(t *testing.T) {
		c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions?api-version=v4.0")
		got := getAPIVersion(c, "SparkDesk-v3.1")
		if got != "v4.0" {
			t.Errorf("got %q, want v4.0 (query param must win)", got)
		}
	})

	t.Run("derived from two-part model name when no query param", func(t *testing.T) {
		c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
		got := getAPIVersion(c, "SparkDesk-v3.5")
		if got != "v3.5" {
			t.Errorf("got %q, want v3.5 (model suffix)", got)
		}
	})

	t.Run("falls back to gin context api_version key", func(t *testing.T) {
		c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
		c.Set("api_version", "v2.1")
		got := getAPIVersion(c, "SparkDesk") // single-part model name, no split
		if got != "v2.1" {
			t.Errorf("got %q, want v2.1 (context fallback)", got)
		}
	})

	t.Run("defaults to v1.1 when nothing else resolves", func(t *testing.T) {
		c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
		got := getAPIVersion(c, "SparkDesk")
		if got != "v1.1" {
			t.Errorf("got %q, want v1.1 default", got)
		}
	})
}

// ---------------------------------------------------------------------------
// buildXunfeiAuthUrl / getXunfeiAuthUrl — signed URL construction
// ---------------------------------------------------------------------------

func TestBuildXunfeiAuthUrl(t *testing.T) {
	url := buildXunfeiAuthUrl("wss://spark-api.xf-yun.com/v3.1/chat", "key123", "secret456")
	if !strings.HasPrefix(url, "wss://spark-api.xf-yun.com/v3.1/chat?") {
		t.Fatalf("expected base URL preserved with query suffix, got %q", url)
	}
	for _, param := range []string{"host=", "date=", "authorization="} {
		if !strings.Contains(url, param) {
			t.Errorf("signed URL missing required query param %q: %s", param, url)
		}
	}
}

func TestGetXunfeiAuthUrl(t *testing.T) {
	c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions?api-version=v3.5")
	domain, authURL := getXunfeiAuthUrl(c, "apikey", "apisecret", "SparkDesk-v3.5")
	if domain != "generalv3.5" {
		t.Errorf("domain = %q, want generalv3.5", domain)
	}
	if !strings.HasPrefix(authURL, "wss://spark-api.xf-yun.com/v3.5/chat?") {
		t.Errorf("authURL host/path wrong: %s", authURL)
	}
}

// ---------------------------------------------------------------------------
// Adaptor — request/response glue, error classification
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL_AlwaysEmpty(t *testing.T) {
	a := &Adaptor{}
	url, err := a.GetRequestURL(&relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("xunfei uses websocket dialing, not HTTP GetRequestURL; expected empty string, got %q", url)
	}
}

func TestAdaptor_SetupRequestHeader_StreamAcceptHeader(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
	info := &relaycommon.RelayInfo{IsStream: true}
	req := &http.Header{}
	if err := a.SetupRequestHeader(c, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("Accept") != "text/event-stream" {
		t.Errorf("Accept header = %q, want text/event-stream for streaming requests with no client Accept header", req.Get("Accept"))
	}
}

func TestAdaptor_ConvertOpenAIRequest(t *testing.T) {
	t.Run("nil request rejected", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
		_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
		if err == nil {
			t.Fatal("expected error for nil request, got nil")
		}
	})

	t.Run("valid request stored on adaptor and passed through unchanged", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
		req := &dto.GeneralOpenAIRequest{Model: "SparkDesk-v3.1"}
		out, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != req {
			t.Errorf("expected same request pointer to pass through, got %+v", out)
		}
		if a.request != req {
			t.Error("adaptor must retain the converted request for DoResponse to use (websocket relay has no HTTP body)")
		}
	})
}

func TestAdaptor_ConvertRerankRequest_NotSupported(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/rerank")
	out, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nil {
		t.Errorf("xunfei has no rerank support, expected nil passthrough, got %v", out)
	}
}

func TestAdaptor_DoRequest_ReturnsDummyOKResponse(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
	result, err := a.DoRequest(c, &relaycommon.RelayInfo{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("expected *http.Response, got %T", result)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("dummy response status = %d, want 200 (real work happens in DoResponse via websocket)", resp.StatusCode)
	}
}

func TestAdaptor_DoResponse_ErrorClassification(t *testing.T) {
	t.Run("malformed api key (not app|secret|key) rejected before any dial attempt", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "onlyonepart"}}
		usage, apiErr := a.DoResponse(c, &http.Response{}, info)
		if apiErr == nil {
			t.Fatal("expected error for malformed API key, got nil")
		}
		if usage != nil {
			t.Errorf("expected nil usage on auth error, got %v", usage)
		}
	})

	t.Run("valid key format but no prior ConvertOpenAIRequest call rejected as nil request", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_xunfeiGinContext("POST", "/v1/chat/completions")
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "appid|secret|key"}}
		usage, apiErr := a.DoResponse(c, &http.Response{}, info)
		if apiErr == nil {
			t.Fatal("expected error when request was never converted (a.request is nil), got nil")
		}
		if usage != nil {
			t.Errorf("expected nil usage, got %v", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	models := a.GetModelList()
	found := false
	for _, m := range models {
		if m == "SparkDesk-v3.5" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected SparkDesk-v3.5 in supported model list, got %v", models)
	}
	if a.GetChannelName() != "xunfei" {
		t.Errorf("channel name = %q, want xunfei", a.GetChannelName())
	}
}
