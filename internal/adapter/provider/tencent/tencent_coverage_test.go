package tencent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func newTestContext() (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return w, c
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://hunyuan.tencentcloudapi.com"}}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://hunyuan.tencentcloudapi.com/"
	if got != want {
		t.Errorf("GetRequestURL() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestInit(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{})
	if a.Action != "ChatCompletions" {
		t.Errorf("Action = %q, want %q", a.Action, "ChatCompletions")
	}
	if a.Version != "2023-09-01" {
		t.Errorf("Version = %q, want %q", a.Version, "2023-09-01")
	}
	if a.Timestamp == 0 {
		t.Error("Timestamp = 0, want non-zero unix timestamp")
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	_, c := newTestContext()
	a := &Adaptor{
		Sign:      "TC3-HMAC-SHA256 Credential=abc",
		Action:    "ChatCompletions",
		Version:   "2023-09-01",
		Timestamp: 1700000000,
	}
	header := http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "TC3-HMAC-SHA256 Credential=abc" {
		t.Errorf("Authorization = %q, want %q", got, "TC3-HMAC-SHA256 Credential=abc")
	}
	if got := header.Get("X-TC-Action"); got != "ChatCompletions" {
		t.Errorf("X-TC-Action = %q, want %q", got, "ChatCompletions")
	}
	if got := header.Get("X-TC-Version"); got != "2023-09-01" {
		t.Errorf("X-TC-Version = %q, want %q", got, "2023-09-01")
	}
	if got := header.Get("X-TC-Timestamp"); got != "1700000000" {
		t.Errorf("X-TC-Timestamp = %q, want %q", got, "1700000000")
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

	t.Run("malformed channel key propagates parseTencentConfig error", func(t *testing.T) {
		_, c := newTestContext()
		c.Set(string(constant.ContextKeyChannelKey), "no-pipes-here")
		req := &dto.GeneralOpenAIRequest{Model: "hunyuan-lite"}
		got, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err == nil {
			t.Fatal("expected error for malformed channel key, got nil")
		}
		if err.Error() != "invalid tencent config" {
			t.Errorf("error = %q, want %q", err.Error(), "invalid tencent config")
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		// AppID must have been reset to zero-value even on the error path
		// (it is assigned before the error check).
		if a.AppID != 0 {
			t.Errorf("AppID = %d, want 0 on parse error with non-numeric appid", a.AppID)
		}
	})

	t.Run("well-formed channel key produces signed TencentChatRequest", func(t *testing.T) {
		aa := &Adaptor{Action: "ChatCompletions", Timestamp: 1700000000}
		_, c := newTestContext()
		c.Set(string(constant.ContextKeyChannelKey), "Bearer 12345|secret-id-1|secret-key-1")
		req := &dto.GeneralOpenAIRequest{
			Model:  "hunyuan-lite",
			Stream: true,
			TopP:   0.5,
			Messages: []dto.Message{
				{Role: "user", Content: "hello"},
			},
		}
		got, err := aa.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tencentReq, ok := got.(*TencentChatRequest)
		if !ok {
			t.Fatalf("expected *TencentChatRequest, got %T", got)
		}
		if tencentReq.Model == nil || *tencentReq.Model != "hunyuan-lite" {
			t.Errorf("Model = %v, want hunyuan-lite", tencentReq.Model)
		}
		if tencentReq.Stream == nil || !*tencentReq.Stream {
			t.Errorf("Stream = %v, want true", tencentReq.Stream)
		}
		if tencentReq.TopP == nil || *tencentReq.TopP != 0.5 {
			t.Errorf("TopP = %v, want 0.5", tencentReq.TopP)
		}
		if len(tencentReq.Messages) != 1 || tencentReq.Messages[0].Content != "hello" {
			t.Errorf("Messages = %+v, want single message with content 'hello'", tencentReq.Messages)
		}
		if aa.AppID != 12345 {
			t.Errorf("AppID = %d, want 12345", aa.AppID)
		}
		if aa.Sign == "" {
			t.Error("Sign was not populated")
		}
		if !strings.HasPrefix(aa.Sign, "TC3-HMAC-SHA256 Credential=secret-id-1/") {
			t.Errorf("Sign = %q, want it to start with TC3-HMAC-SHA256 Credential=secret-id-1/", aa.Sign)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertRerankRequest
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

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
	if a.GetChannelName() != "tencent" {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), "tencent")
	}
}

// ---------------------------------------------------------------------------
// DoRequest — the only hermetically reachable branch: an invalid
// ChannelBaseUrl (containing a raw control character) makes
// http.NewRequestWithContext fail inside provider.DoApiRequest before any
// network connection is attempted. The success path requires a live POST
// to the Tencent hunyuan endpoint and is not exercised here.
// ---------------------------------------------------------------------------

func TestDoRequest_NewRequestErrorPropagates(t *testing.T) {
	_, c := newTestContext()
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://example.com\n"}}

	got, err := a.DoRequest(c, info, strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "new request failed") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "new request failed")
	}
	if resp, ok := got.(*http.Response); ok && resp != nil {
		t.Errorf("expected nil *http.Response, got %v", resp)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Tencent
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Tencent(t *testing.T) {
	temp := 0.7
	req := dto.GeneralOpenAIRequest{
		Model:       "hunyuan-pro",
		Stream:      false,
		TopP:        0, // zero TopP must not be carried over (pointer left nil)
		Temperature: &temp,
		Messages: []dto.Message{
			{Role: "system", Content: "be nice"},
			{Role: "user", Content: "hi"},
		},
	}
	got := requestOpenAI2Tencent(&Adaptor{}, req)
	if got.Model == nil || *got.Model != "hunyuan-pro" {
		t.Errorf("Model = %v, want hunyuan-pro", got.Model)
	}
	if got.Stream == nil || *got.Stream {
		t.Errorf("Stream = %v, want false", got.Stream)
	}
	if got.TopP != nil {
		t.Errorf("TopP = %v, want nil (zero-value TopP must not be set)", *got.TopP)
	}
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", got.Temperature)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Content != "hi" {
		t.Errorf("Messages = %+v, want [system:be nice, user:hi]", got.Messages)
	}
}

func TestRequestOpenAI2Tencent_NonZeroTopP(t *testing.T) {
	req := dto.GeneralOpenAIRequest{Model: "hunyuan-lite", TopP: 0.3}
	got := requestOpenAI2Tencent(&Adaptor{}, req)
	if got.TopP == nil || *got.TopP != 0.3 {
		t.Errorf("TopP = %v, want 0.3", got.TopP)
	}
}

// ---------------------------------------------------------------------------
// responseTencent2OpenAI / streamResponseTencent2OpenAI
// ---------------------------------------------------------------------------

func TestResponseTencent2OpenAI(t *testing.T) {
	t.Run("with choices", func(t *testing.T) {
		resp := &TencentChatResponse{
			Id: "resp-1",
			Usage: TencentUsage{
				PromptTokens:     3,
				CompletionTokens: 5,
				TotalTokens:      8,
			},
			Choices: []TencentResponseChoices{
				{
					FinishReason: "stop",
					Messages:     TencentMessage{Role: "assistant", Content: "the reply"},
				},
			},
		}
		got := responseTencent2OpenAI(resp)
		if got.Id != "resp-1" || got.Object != "chat.completion" {
			t.Errorf("unexpected envelope: %+v", got)
		}
		if got.Usage.TotalTokens != 8 || got.Usage.PromptTokens != 3 || got.Usage.CompletionTokens != 5 {
			t.Errorf("Usage = %+v, want 3/5/8", got.Usage)
		}
		if len(got.Choices) != 1 {
			t.Fatalf("Choices length = %d, want 1", len(got.Choices))
		}
		choice := got.Choices[0]
		if choice.Message.Role != "assistant" || choice.Message.Content != "the reply" || choice.FinishReason != "stop" {
			t.Errorf("choice = %+v, want assistant/the reply/stop", choice)
		}
	})

	t.Run("no choices", func(t *testing.T) {
		resp := &TencentChatResponse{Id: "resp-2"}
		got := responseTencent2OpenAI(resp)
		if len(got.Choices) != 0 {
			t.Errorf("Choices length = %d, want 0", len(got.Choices))
		}
	})
}

func TestStreamResponseTencent2OpenAI(t *testing.T) {
	t.Run("finish reason stop", func(t *testing.T) {
		resp := &TencentChatResponse{
			Choices: []TencentResponseChoices{
				{FinishReason: "stop", Delta: TencentMessage{Content: "chunk"}},
			},
		}
		got := streamResponseTencent2OpenAI(resp)
		if got.Object != "chat.completion.chunk" || got.Model != "tencent-hunyuan" {
			t.Errorf("unexpected envelope: %+v", got)
		}
		if len(got.Choices) != 1 {
			t.Fatalf("Choices length = %d, want 1", len(got.Choices))
		}
		if got.Choices[0].Delta.GetContentString() != "chunk" {
			t.Errorf("Delta content = %q, want %q", got.Choices[0].Delta.GetContentString(), "chunk")
		}
		if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != constant.FinishReasonStop {
			t.Errorf("FinishReason = %v, want %q", got.Choices[0].FinishReason, constant.FinishReasonStop)
		}
	})

	t.Run("not finished: no finish reason", func(t *testing.T) {
		resp := &TencentChatResponse{
			Choices: []TencentResponseChoices{
				{FinishReason: "", Delta: TencentMessage{Content: "partial"}},
			},
		}
		got := streamResponseTencent2OpenAI(resp)
		if got.Choices[0].FinishReason != nil {
			t.Errorf("FinishReason = %v, want nil", *got.Choices[0].FinishReason)
		}
	})

	t.Run("no choices", func(t *testing.T) {
		resp := &TencentChatResponse{}
		got := streamResponseTencent2OpenAI(resp)
		if len(got.Choices) != 0 {
			t.Errorf("Choices length = %d, want 0", len(got.Choices))
		}
	})
}

// ---------------------------------------------------------------------------
// tencentHandler — hermetic (only requires an io.Reader body, no network).
// ---------------------------------------------------------------------------

func TestTencentHandler_Success(t *testing.T) {
	w, c := newTestContext()
	body := `{"Response":{"Id":"cmpl-1","Choices":[{"FinishReason":"stop","Message":{"Role":"assistant","Content":"hi there"}}],"Usage":{"TotalTokens":9,"PromptTokens":3,"CompletionTokens":6}}}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := tencentHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.TotalTokens != 9 {
		t.Errorf("TotalTokens = %d, want 9", usage.TotalTokens)
	}
	if w.Code != http.StatusOK {
		t.Errorf("http status written = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "hi there") {
		t.Errorf("response body = %q, want it to contain %q", w.Body.String(), "hi there")
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "application/json")
	}
}

func TestTencentHandler_UpstreamError(t *testing.T) {
	_, c := newTestContext()
	body := `{"Response":{"Error":{"Code":123,"Message":"quota exceeded"}}}`
	resp := &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := tencentHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for non-zero Error.Code, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestTencentHandler_MalformedJSON(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{not-json"))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := tencentHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// errReader always fails on Read, to exercise the io.ReadAll error branch.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReader) Close() error               { return nil }

func TestTencentHandler_BodyReadError(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: errReader{}}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := tencentHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when body read fails, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// ---------------------------------------------------------------------------
// tencentStreamHandler — SSE scan loop against an in-memory reader.
// ---------------------------------------------------------------------------

func TestTencentStreamHandler(t *testing.T) {
	sse := "data:{\"Choices\":[{\"Delta\":{\"Content\":\"chunk one\"}}]}\n" +
		"data:{\"Choices\":[{\"FinishReason\":\"stop\",\"Delta\":{\"Content\":\" chunk two\"}}]}\n" +
		"short\n" +
		"data:{not-json}\n"
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "hunyuan-lite"},
		DisablePing: true,
	}

	usage, apiErr := tencentStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
}

// ---------------------------------------------------------------------------
// DoResponse — dispatch across IsStream branches.
// ---------------------------------------------------------------------------

func TestDoResponse_Dispatch(t *testing.T) {
	a := &Adaptor{}

	t.Run("stream", func(t *testing.T) {
		_, c := newTestContext()
		sse := "data:{\"Choices\":[{\"FinishReason\":\"stop\",\"Delta\":{\"Content\":\"hi\"}}]}\n"
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}, DisablePing: true}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
	})

	t.Run("non-stream", func(t *testing.T) {
		_, c := newTestContext()
		body := `{"Response":{"Id":"c-1","Choices":[{"FinishReason":"stop","Message":{"Role":"assistant","Content":"hi"}}],"Usage":{"TotalTokens":2}}}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 2 {
			t.Errorf("usage = %+v, want TotalTokens=2", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// parseTencentConfig
// ---------------------------------------------------------------------------

func TestParseTencentConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		appId, secretId, secretKey, err := parseTencentConfig("999|sid|skey")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if appId != 999 {
			t.Errorf("appId = %d, want 999", appId)
		}
		if secretId != "sid" || secretKey != "skey" {
			t.Errorf("secretId/secretKey = %q/%q, want sid/skey", secretId, secretKey)
		}
	})

	tests := []struct {
		name   string
		config string
	}{
		{"no pipe", "abcdefg"},
		{"too many parts", "a|b|c|d"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := parseTencentConfig(tt.config)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != "invalid tencent config" {
				t.Errorf("error = %q, want %q", err.Error(), "invalid tencent config")
			}
		})
	}

	t.Run("non-numeric appid", func(t *testing.T) {
		appId, _, _, err := parseTencentConfig("notanumber|sid|skey")
		if err == nil {
			t.Fatal("expected error for non-numeric appid, got nil")
		}
		if appId != 0 {
			t.Errorf("appId = %d, want 0", appId)
		}
	})
}

// ---------------------------------------------------------------------------
// sha256hex / hmacSha256 / getTencentSign
// ---------------------------------------------------------------------------

func TestSha256Hex(t *testing.T) {
	got := sha256hex("")
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("sha256hex(\"\") = %q, want %q", got, want)
	}
}

func TestGetTencentSign_Deterministic(t *testing.T) {
	model := "hunyuan-lite"
	streamVal := false
	req := TencentChatRequest{Model: &model, Stream: &streamVal}
	a := &Adaptor{Action: "ChatCompletions", Timestamp: 1700000000}

	sig1 := getTencentSign(req, a, "secret-id", "secret-key")
	sig2 := getTencentSign(req, a, "secret-id", "secret-key")
	if sig1 != sig2 {
		t.Errorf("signature not deterministic: %q vs %q", sig1, sig2)
	}
	if !strings.HasPrefix(sig1, "TC3-HMAC-SHA256 Credential=secret-id/") {
		t.Errorf("signature = %q, want prefix %q", sig1, "TC3-HMAC-SHA256 Credential=secret-id/")
	}
	if !strings.Contains(sig1, "SignedHeaders=content-type;host;x-tc-action, Signature=") {
		t.Errorf("signature = %q, missing expected SignedHeaders/Signature markers", sig1)
	}

	// Different secretKey must yield a different signature.
	sig3 := getTencentSign(req, a, "secret-id", "different-key")
	if sig3 == sig1 {
		t.Error("expected different signature for different secretKey")
	}
}
