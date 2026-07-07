package xunfei

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestGinContext(t *testing.T, method, path string) *gin.Context {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
	return c
}

// ---------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	want := []string{
		"SparkDesk",
		"SparkDesk-v1.1",
		"SparkDesk-v2.1",
		"SparkDesk-v3.1",
		"SparkDesk-v3.5",
		"SparkDesk-v4.0",
	}
	got := a.GetModelList()
	if len(got) != len(want) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], w)
		}
	}

	if name := a.GetChannelName(); name != "xunfei" {
		t.Errorf("GetChannelName() = %q, want %q", name, "xunfei")
	}
}

// ---------------------------------------------------------------------
// GetRequestURL - always returns empty string, nil error (xunfei relay
// does not use a regular HTTP URL; the real endpoint is derived per-request
// inside getXunfeiAuthUrl instead).
// ---------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil info", func(t *testing.T) {
		got, err := a.GetRequestURL(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("GetRequestURL(nil) = %q, want empty string", got)
		}
	})

	t.Run("populated info", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://example.com"},
		}
		got, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("GetRequestURL(info) = %q, want empty string", got)
		}
	})
}

// ---------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}

	t.Run("copies content-type/accept from request", func(t *testing.T) {
		c := newTestGinContext(t, http.MethodPost, "/")
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{}
		header := http.Header{}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
	})

	t.Run("stream without accept sets text/event-stream", func(t *testing.T) {
		c := newTestGinContext(t, http.MethodPost, "/")
		info := &relaycommon.RelayInfo{IsStream: true}
		header := http.Header{}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want %q", got, "text/event-stream")
		}
	})
}

// ---------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	t.Run("nil request errors", func(t *testing.T) {
		a := &Adaptor{}
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("error = %v, want %q", err, "request is nil")
		}
		if a.request != nil {
			t.Errorf("expected a.request to remain nil, got %v", a.request)
		}
	})

	t.Run("non-nil request is stashed and returned as-is", func(t *testing.T) {
		a := &Adaptor{}
		req := &dto.GeneralOpenAIRequest{Model: "SparkDesk-v3.5"}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != req {
			t.Errorf("expected same pointer returned, got %v", got)
		}
		if a.request != req {
			t.Errorf("expected a.request to be stashed, got %v", a.request)
		}
	})
}

// ---------------------------------------------------------------------
// ConvertRerankRequest - unconditional nil,nil stub
// ---------------------------------------------------------------------

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{Model: "x"})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// ---------------------------------------------------------------------
// DoRequest - xunfei is not a regular HTTP relay; it always returns a
// dummy 200 response without doing any I/O.
// ---------------------------------------------------------------------

func TestDoRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.DoRequest(nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := got.(*http.Response)
	if !ok {
		t.Fatalf("expected *http.Response, got %T", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// ---------------------------------------------------------------------
// DoResponse - only the hermetically reachable early-return guard
// branches are exercised here. Once past those guards, DoResponse
// dispatches to xunfeiHandler/xunfeiStreamHandler, which dial a live
// wss:// websocket via xunfeiMakeRequest -- that requires a live upstream
// and is NOT exercised by this hermetic test.
// ---------------------------------------------------------------------

func TestDoResponse_InvalidKeyFormat(t *testing.T) {
	a := &Adaptor{}
	tests := []struct {
		name   string
		apiKey string
	}{
		{"empty key", ""},
		{"missing pipe", "onlyonepart"},
		{"too few parts", "a|b"},
		{"too many parts", "a|b|c|d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: tt.apiKey}}
			usage, err := a.DoResponse(nil, nil, info)
			if usage != nil {
				t.Errorf("expected nil usage, got %v", usage)
			}
			if err == nil {
				t.Fatal("expected error for invalid key format")
			}
			if err.Err == nil || err.Err.Error() != "invalid auth" {
				t.Errorf("Err = %v, want %q", err.Err, "invalid auth")
			}
		})
	}
}

func TestDoResponse_NilRequest(t *testing.T) {
	a := &Adaptor{} // a.request left nil
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app|secret|key"}}
	usage, err := a.DoResponse(nil, nil, info)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	if err.Err == nil || err.Err.Error() != "request is nil" {
		t.Errorf("Err = %v, want %q", err.Err, "request is nil")
	}
}

// ---------------------------------------------------------------------
// requestOpenAI2Xunfei
// ---------------------------------------------------------------------

func TestRequestOpenAI2Xunfei(t *testing.T) {
	t.Run("non-3.5 model converts system message to user+assistant pair", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model: "SparkDesk-v3.1",
			N:     3,
			Messages: []dto.Message{
				{Role: "system", Content: "be nice"},
				{Role: "user", Content: "hi"},
			},
		}
		got := requestOpenAI2Xunfei(req, "app-1", "generalv3")
		if got.Header.AppId != "app-1" {
			t.Errorf("AppId = %q, want %q", got.Header.AppId, "app-1")
		}
		if got.Parameter.Chat.Domain != "generalv3" {
			t.Errorf("Domain = %q, want %q", got.Parameter.Chat.Domain, "generalv3")
		}
		if got.Parameter.Chat.TopK != 3 {
			t.Errorf("TopK = %d, want 3", got.Parameter.Chat.TopK)
		}
		texts := got.Payload.Message.Text
		if len(texts) != 3 {
			t.Fatalf("Text len = %d, want 3 (system->user+assistant, then user)", len(texts))
		}
		if texts[0].Role != "user" || texts[0].Content != "be nice" {
			t.Errorf("texts[0] = %+v, want role=user content=%q", texts[0], "be nice")
		}
		if texts[1].Role != "assistant" || texts[1].Content != "Okay" {
			t.Errorf("texts[1] = %+v, want role=assistant content=%q", texts[1], "Okay")
		}
		if texts[2].Role != "user" || texts[2].Content != "hi" {
			t.Errorf("texts[2] = %+v, want role=user content=%q", texts[2], "hi")
		}
	})

	t.Run("3.5 model keeps system message as-is", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model: "SparkDesk-v3.5",
			Messages: []dto.Message{
				{Role: "system", Content: "be nice"},
			},
		}
		got := requestOpenAI2Xunfei(req, "app-2", "generalv3.5")
		texts := got.Payload.Message.Text
		if len(texts) != 1 {
			t.Fatalf("Text len = %d, want 1 (system message untouched)", len(texts))
		}
		if texts[0].Role != "system" || texts[0].Content != "be nice" {
			t.Errorf("texts[0] = %+v, want role=system content=%q", texts[0], "be nice")
		}
	})

	t.Run("temperature and max tokens carried through", func(t *testing.T) {
		temp := 0.7
		req := dto.GeneralOpenAIRequest{
			Model:       "SparkDesk-v1.1",
			Temperature: &temp,
			MaxTokens:   128,
			Messages:    []dto.Message{{Role: "user", Content: "hi"}},
		}
		got := requestOpenAI2Xunfei(req, "app-3", "lite")
		if got.Parameter.Chat.Temperature == nil || *got.Parameter.Chat.Temperature != 0.7 {
			t.Errorf("Temperature = %v, want 0.7", got.Parameter.Chat.Temperature)
		}
		if got.Parameter.Chat.MaxTokens != req.GetMaxTokens() {
			t.Errorf("MaxTokens = %d, want %d", got.Parameter.Chat.MaxTokens, req.GetMaxTokens())
		}
	})
}

// ---------------------------------------------------------------------
// responseXunfei2OpenAI
// ---------------------------------------------------------------------

func TestResponseXunfei2OpenAI(t *testing.T) {
	t.Run("empty choices synthesizes empty content", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		got := responseXunfei2OpenAI(resp)
		if len(got.Choices) != 1 {
			t.Fatalf("Choices len = %d, want 1", len(got.Choices))
		}
		if got.Choices[0].Content != "" {
			t.Errorf("Content = %v, want empty", got.Choices[0].Content)
		}
		if got.Choices[0].Role != "assistant" {
			t.Errorf("Role = %q, want %q", got.Choices[0].Role, "assistant")
		}
		if got.Object != "chat.completion" {
			t.Errorf("Object = %q, want %q", got.Object, "chat.completion")
		}
	})

	t.Run("non-empty choices use first text item", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		resp.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{Content: "hello world", Role: "assistant"},
		}
		got := responseXunfei2OpenAI(resp)
		if got.Choices[0].Content != "hello world" {
			t.Errorf("Content = %v, want %q", got.Choices[0].Content, "hello world")
		}
		if got.Choices[0].FinishReason != "stop" {
			t.Errorf("FinishReason = %q, want %q", got.Choices[0].FinishReason, "stop")
		}
	})
}

// ---------------------------------------------------------------------
// streamResponseXunfei2OpenAI
// ---------------------------------------------------------------------

func TestStreamResponseXunfei2OpenAI(t *testing.T) {
	t.Run("empty choices synthesizes empty content, no finish reason", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		got := streamResponseXunfei2OpenAI(resp)
		if got.Model != "SparkDesk" {
			t.Errorf("Model = %q, want %q", got.Model, "SparkDesk")
		}
		if len(got.Choices) != 1 {
			t.Fatalf("Choices len = %d, want 1", len(got.Choices))
		}
		if got.Choices[0].FinishReason != nil {
			t.Errorf("FinishReason = %v, want nil (status != 2)", got.Choices[0].FinishReason)
		}
	})

	t.Run("status 2 sets finish reason to stop", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		resp.Payload.Choices.Status = 2
		resp.Payload.Choices.Text = []XunfeiChatResponseTextItem{{Content: "done"}}
		got := streamResponseXunfei2OpenAI(resp)
		if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != "stop" {
			t.Errorf("FinishReason = %v, want %q", got.Choices[0].FinishReason, "stop")
		}
	})

	t.Run("status != 2 leaves finish reason nil", func(t *testing.T) {
		resp := &XunfeiChatResponse{}
		resp.Payload.Choices.Status = 1
		resp.Payload.Choices.Text = []XunfeiChatResponseTextItem{{Content: "partial"}}
		got := streamResponseXunfei2OpenAI(resp)
		if got.Choices[0].FinishReason != nil {
			t.Errorf("FinishReason = %v, want nil", got.Choices[0].FinishReason)
		}
	})
}

// ---------------------------------------------------------------------
// buildXunfeiAuthUrl - pure string construction, no network. Signature
// values are non-deterministic across runs (time.Now()), so we assert
// on structural properties rather than exact bytes.
// ---------------------------------------------------------------------

func TestBuildXunfeiAuthUrl(t *testing.T) {
	got := buildXunfeiAuthUrl("wss://spark-api.xf-yun.com/v1.1/chat", "my-api-key", "my-api-secret")
	prefix := "wss://spark-api.xf-yun.com/v1.1/chat?"
	if len(got) <= len(prefix) || got[:len(prefix)] != prefix {
		t.Fatalf("buildXunfeiAuthUrl() = %q, want prefix %q", got, prefix)
	}
	for _, param := range []string{"host=", "date=", "authorization="} {
		if !containsSubstring(got, param) {
			t.Errorf("expected query to contain %q, got %q", param, got)
		}
	}
}

func containsSubstring(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// ---------------------------------------------------------------------
// apiVersion2domain
// ---------------------------------------------------------------------

func TestApiVersion2domain(t *testing.T) {
	tests := []struct {
		apiVersion string
		want       string
	}{
		{"v1.1", "lite"},
		{"v2.1", "generalv2"},
		{"v3.1", "generalv3"},
		{"v3.5", "generalv3.5"},
		{"v4.0", "4.0Ultra"},
		{"v9.9", "generalv9.9"}, // default fallback: "general" + apiVersion
	}
	for _, tt := range tests {
		t.Run(tt.apiVersion, func(t *testing.T) {
			if got := apiVersion2domain(tt.apiVersion); got != tt.want {
				t.Errorf("apiVersion2domain(%q) = %q, want %q", tt.apiVersion, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// getAPIVersion
// ---------------------------------------------------------------------

func TestGetAPIVersion(t *testing.T) {
	t.Run("query param takes precedence", func(t *testing.T) {
		c := newTestGinContext(t, http.MethodGet, "/?api-version=v4.0")
		if got := getAPIVersion(c, "SparkDesk-v1.1"); got != "v4.0" {
			t.Errorf("getAPIVersion() = %q, want %q", got, "v4.0")
		}
	})

	t.Run("model name suffix used when no query param", func(t *testing.T) {
		c := newTestGinContext(t, http.MethodGet, "/")
		if got := getAPIVersion(c, "SparkDesk-v3.1"); got != "v3.1" {
			t.Errorf("getAPIVersion() = %q, want %q", got, "v3.1")
		}
	})

	t.Run("context api_version used when model has no version suffix", func(t *testing.T) {
		c := newTestGinContext(t, http.MethodGet, "/")
		c.Set("api_version", "v2.1")
		if got := getAPIVersion(c, "SparkDesk"); got != "v2.1" {
			t.Errorf("getAPIVersion() = %q, want %q", got, "v2.1")
		}
	})

	t.Run("falls back to default v1.1", func(t *testing.T) {
		c := newTestGinContext(t, http.MethodGet, "/")
		if got := getAPIVersion(c, "SparkDesk"); got != "v1.1" {
			t.Errorf("getAPIVersion() = %q, want %q", got, "v1.1")
		}
	})

	t.Run("model name with more than 2 hyphen-parts falls through to default", func(t *testing.T) {
		c := newTestGinContext(t, http.MethodGet, "/")
		if got := getAPIVersion(c, "Spark-Desk-v3.1"); got != "v1.1" {
			t.Errorf("getAPIVersion() = %q, want %q", got, "v1.1")
		}
	})
}

// ---------------------------------------------------------------------
// getXunfeiAuthUrl - composes apiVersion2domain + buildXunfeiAuthUrl; no
// network I/O (only pure computation + URL string building).
// ---------------------------------------------------------------------

func TestGetXunfeiAuthUrl(t *testing.T) {
	c := newTestGinContext(t, http.MethodGet, "/?api-version=v3.5")
	domain, authURL := getXunfeiAuthUrl(c, "key", "secret", "SparkDesk-v3.5")
	if domain != "generalv3.5" {
		t.Errorf("domain = %q, want %q", domain, "generalv3.5")
	}
	wantPrefix := "wss://spark-api.xf-yun.com/v3.5/chat?"
	if len(authURL) <= len(wantPrefix) || authURL[:len(wantPrefix)] != wantPrefix {
		t.Errorf("authURL = %q, want prefix %q", authURL, wantPrefix)
	}
}
