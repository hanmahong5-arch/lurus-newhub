package tencent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	pkgcommon "github.com/LurusTech/lurus-hub/internal/pkg/common"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

func prov_cn_batch_tencentGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, w
}

func prov_cn_batch_tencentRespFromBody(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// ---------------------------------------------------------------------------
// Adaptor.Init
// ---------------------------------------------------------------------------

func TestAdaptor_Init(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{})
	if a.Action != "ChatCompletions" {
		t.Errorf("Action = %q, want ChatCompletions", a.Action)
	}
	if a.Version != "2023-09-01" {
		t.Errorf("Version = %q, want 2023-09-01", a.Version)
	}
	if a.Timestamp == 0 {
		t.Error("expected Timestamp to be populated with current unix time")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://hunyuan.tencentcloudapi.com"}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://hunyuan.tencentcloudapi.com/" {
		t.Errorf("url = %q, want trailing-slash root path", url)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader — Tencent TC3 signature headers
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{Sign: "TC3-HMAC-SHA256 Credential=...", Action: "ChatCompletions", Version: "2023-09-01", Timestamp: 1700000000}
	c, _ := prov_cn_batch_tencentGinContext()
	req := &http.Header{}
	if err := a.SetupRequestHeader(c, req, &relaycommon.RelayInfo{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("Authorization") != a.Sign {
		t.Errorf("Authorization = %q, want %q", req.Get("Authorization"), a.Sign)
	}
	if req.Get("X-TC-Action") != "ChatCompletions" {
		t.Errorf("X-TC-Action = %q, want ChatCompletions", req.Get("X-TC-Action"))
	}
	if req.Get("X-TC-Version") != "2023-09-01" {
		t.Errorf("X-TC-Version = %q, want 2023-09-01", req.Get("X-TC-Version"))
	}
	if req.Get("X-TC-Timestamp") != "1700000000" {
		t.Errorf("X-TC-Timestamp = %q, want 1700000000", req.Get("X-TC-Timestamp"))
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest — config parsing + signature generation
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest(t *testing.T) {
	t.Run("nil request rejected", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_tencentGinContext()
		_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
		if err == nil {
			t.Fatal("expected error for nil request")
		}
	})

	t.Run("malformed api key (not appid|secretid|secretkey) rejected", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_tencentGinContext()
		pkgcommon.SetContextKey(c, pkgconstant.ContextKeyChannelKey, "bad-key-format")
		req := &dto.GeneralOpenAIRequest{Model: "hunyuan-lite"}
		_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err == nil {
			t.Fatal("expected error for malformed tencent config key")
		}
	})

	t.Run("non-numeric appid rejected", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_tencentGinContext()
		pkgcommon.SetContextKey(c, pkgconstant.ContextKeyChannelKey, "not-a-number|secretid|secretkey")
		req := &dto.GeneralOpenAIRequest{Model: "hunyuan-lite"}
		_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err == nil {
			t.Fatal("expected error when appid segment is not a valid int64")
		}
	})

	t.Run("valid key parses appid, strips Bearer prefix, computes signature and stores it on the adaptor", func(t *testing.T) {
		a := &Adaptor{Action: "ChatCompletions", Timestamp: 1700000000}
		c, _ := prov_cn_batch_tencentGinContext()
		pkgcommon.SetContextKey(c, pkgconstant.ContextKeyChannelKey, "Bearer 12345|secretIdXYZ|secretKeyXYZ")
		req := &dto.GeneralOpenAIRequest{
			Model: "hunyuan-lite",
			Messages: []dto.Message{
				{Role: "user", Content: "hi"},
			},
		}
		out, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.AppID != 12345 {
			t.Errorf("AppID = %d, want 12345 (parsed from Bearer-prefixed config)", a.AppID)
		}
		if a.Sign == "" {
			t.Error("expected a non-empty TC3 signature to be computed and stashed for SetupRequestHeader")
		}
		if !strings.HasPrefix(a.Sign, "TC3-HMAC-SHA256 Credential=secretIdXYZ/") {
			t.Errorf("Sign = %q, want TC3-HMAC-SHA256 credential scope keyed by secretIdXYZ", a.Sign)
		}
		tcReq, ok := out.(*TencentChatRequest)
		if !ok {
			t.Fatalf("expected *TencentChatRequest, got %T", out)
		}
		if len(tcReq.Messages) != 1 || tcReq.Messages[0].Content != "hi" {
			t.Errorf("expected translated message content preserved, got %+v", tcReq.Messages)
		}
	})
}

func TestAdaptor_ConvertRerankRequest_NotSupported(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_tencentGinContext()
	out, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if out != nil || err != nil {
		t.Errorf("expected (nil, nil) for unsupported rerank, got (%v, %v)", out, err)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Tencent — request translation
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Tencent(t *testing.T) {
	t.Run("messages, model, stream propagated", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model:  "hunyuan-pro",
			Stream: true,
			Messages: []dto.Message{
				{Role: "system", Content: "be terse"},
				{Role: "user", Content: "hi"},
			},
		}
		out := requestOpenAI2Tencent(&Adaptor{}, req)
		if out.Model == nil || *out.Model != "hunyuan-pro" {
			t.Errorf("Model = %v, want hunyuan-pro", out.Model)
		}
		if out.Stream == nil || !*out.Stream {
			t.Error("expected Stream=true propagated")
		}
		if len(out.Messages) != 2 {
			t.Fatalf("expected system message preserved as a Tencent message (unlike baidu), got %d messages", len(out.Messages))
		}
		if out.Messages[0].Role != "system" || out.Messages[0].Content != "be terse" {
			t.Errorf("system message not preserved verbatim: %+v", out.Messages[0])
		}
	})

	t.Run("TopP==0 omitted (nil), TopP!=0 included", func(t *testing.T) {
		out := requestOpenAI2Tencent(&Adaptor{}, dto.GeneralOpenAIRequest{TopP: 0})
		if out.TopP != nil {
			t.Errorf("expected nil TopP when caller specified 0, got %v", *out.TopP)
		}
		out2 := requestOpenAI2Tencent(&Adaptor{}, dto.GeneralOpenAIRequest{TopP: 0.8})
		if out2.TopP == nil || *out2.TopP != 0.8 {
			t.Errorf("TopP = %v, want pointer to 0.8", out2.TopP)
		}
	})

	t.Run("empty messages yields empty slice, not nil-panic", func(t *testing.T) {
		out := requestOpenAI2Tencent(&Adaptor{}, dto.GeneralOpenAIRequest{Messages: []dto.Message{}})
		if len(out.Messages) != 0 {
			t.Errorf("expected zero messages, got %d", len(out.Messages))
		}
	})
}

// ---------------------------------------------------------------------------
// responseTencent2OpenAI / streamResponseTencent2OpenAI
// ---------------------------------------------------------------------------

func TestResponseTencent2OpenAI(t *testing.T) {
	t.Run("normal response maps content, finish reason, usage", func(t *testing.T) {
		resp := &TencentChatResponse{
			Id: "req-1",
			Choices: []TencentResponseChoices{
				{FinishReason: "stop", Messages: TencentMessage{Role: "assistant", Content: "hello"}},
			},
		}
		resp.Usage = TencentUsage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}
		out := responseTencent2OpenAI(resp)
		if out.Choices[0].Content != "hello" {
			t.Errorf("content = %v, want hello", out.Choices[0].Content)
		}
		if out.Choices[0].FinishReason != "stop" {
			t.Errorf("finish reason = %q, want stop", out.Choices[0].FinishReason)
		}
		if out.TotalTokens != 6 {
			t.Errorf("usage.TotalTokens = %d, want 6 (billing extraction)", out.TotalTokens)
		}
	})

	t.Run("empty choices produces zero choices, no panic (index-0 access guarded)", func(t *testing.T) {
		resp := &TencentChatResponse{}
		out := responseTencent2OpenAI(resp)
		if len(out.Choices) != 0 {
			t.Errorf("expected zero choices for empty upstream response, got %d", len(out.Choices))
		}
	})
}

func TestStreamResponseTencent2OpenAI(t *testing.T) {
	t.Run("stop finish reason mapped from string literal", func(t *testing.T) {
		resp := &TencentChatResponse{
			Choices: []TencentResponseChoices{
				{FinishReason: "stop", Delta: TencentMessage{Content: "chunk"}},
			},
		}
		out := streamResponseTencent2OpenAI(resp)
		if out.Choices[0].Delta.GetContentString() != "chunk" {
			t.Errorf("delta content = %q, want chunk", out.Choices[0].Delta.GetContentString())
		}
		if out.Choices[0].FinishReason == nil || *out.Choices[0].FinishReason != "stop" {
			t.Errorf("expected stop finish reason, got %v", out.Choices[0].FinishReason)
		}
	})

	t.Run("non-stop finish reason string leaves FinishReason nil", func(t *testing.T) {
		resp := &TencentChatResponse{
			Choices: []TencentResponseChoices{
				{FinishReason: "", Delta: TencentMessage{Content: "partial"}},
			},
		}
		out := streamResponseTencent2OpenAI(resp)
		if out.Choices[0].FinishReason != nil {
			t.Errorf("mid-stream chunk must not set finish reason, got %v", *out.Choices[0].FinishReason)
		}
	})

	t.Run("empty choices produces no choices, no index-0 panic", func(t *testing.T) {
		resp := &TencentChatResponse{}
		out := streamResponseTencent2OpenAI(resp)
		if len(out.Choices) != 0 {
			t.Errorf("expected zero choices, got %d", len(out.Choices))
		}
	})
}

// ---------------------------------------------------------------------------
// tencentHandler / tencentStreamHandler / Adaptor.DoResponse — response path
// ---------------------------------------------------------------------------

func TestTencentHandler(t *testing.T) {
	t.Run("valid response extracts usage and writes translated body", func(t *testing.T) {
		c, w := prov_cn_batch_tencentGinContext()
		body := `{"Response":{"Id":"r1","Choices":[{"FinishReason":"stop","Message":{"Role":"assistant","Content":"hi there"}}],"Usage":{"PromptTokens":3,"CompletionTokens":2,"TotalTokens":5}}}`
		bcResp5 := prov_cn_batch_tencentRespFromBody(body)
		defer func() {
			if bcResp5 != nil {
				_ = bcResp5.Body.Close()
			}
		}()
		usage, apiErr := tencentHandler(c, &relaycommon.RelayInfo{}, bcResp5)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage.TotalTokens != 5 {
			t.Errorf("usage.TotalTokens = %d, want 5", usage.TotalTokens)
		}
		if !strings.Contains(w.Body.String(), "hi there") {
			t.Errorf("response body missing translated content: %s", w.Body.String())
		}
	})

	t.Run("upstream error code surfaces as classified error, not silent 200", func(t *testing.T) {
		c, _ := prov_cn_batch_tencentGinContext()
		body := `{"Response":{"Error":{"Code":1002,"Message":"InvalidParameter"}}}`
		bcResp4 := prov_cn_batch_tencentRespFromBody(body)
		defer func() {
			if bcResp4 != nil {
				_ = bcResp4.Body.Close()
			}
		}()
		usage, apiErr := tencentHandler(c, &relaycommon.RelayInfo{}, bcResp4)
		if apiErr == nil {
			t.Fatal("expected error for non-zero upstream error code")
		}
		if usage != nil {
			t.Errorf("expected nil usage on upstream error, got %v", usage)
		}
	})

	t.Run("malformed JSON body classified as bad response body", func(t *testing.T) {
		c, _ := prov_cn_batch_tencentGinContext()
		bcResp3 := prov_cn_batch_tencentRespFromBody("{not json")
		defer func() {
			if bcResp3 != nil {
				_ = bcResp3.Body.Close()
			}
		}()
		_, apiErr := tencentHandler(c, &relaycommon.RelayInfo{}, bcResp3)
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

func TestTencentStreamHandler(t *testing.T) {
	c, w := prov_cn_batch_tencentGinContext()
	body := "data:" + `{"Choices":[{"Delta":{"Content":"He"}}]}` + "\n" +
		"data:" + `{"Choices":[{"Delta":{"Content":"llo"},"FinishReason":"stop"}]}` + "\n" +
		"data:[DONE]\n"
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "hunyuan-lite"}}
	bcResp2 := prov_cn_batch_tencentRespFromBody(body)
	defer func() {
		if bcResp2 != nil {
			_ = bcResp2.Body.Close()
		}
	}()
	usage, apiErr := tencentStreamHandler(c, info, bcResp2)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.CompletionTokens <= 0 {
		t.Errorf("expected positive estimated CompletionTokens for accumulated 'Hello' text, got %d", usage.CompletionTokens)
	}
	out := w.Body.String()
	if !strings.Contains(out, "He") || !strings.Contains(out, "llo") {
		t.Errorf("stream output missing forwarded deltas: %s", out)
	}
}

func TestAdaptor_DoResponse_Routing(t *testing.T) {
	a := &Adaptor{}
	t.Run("non-stream routes to tencentHandler", func(t *testing.T) {
		c, w := prov_cn_batch_tencentGinContext()
		body := `{"Response":{"Id":"r1","Choices":[{"FinishReason":"stop","Message":{"Role":"assistant","Content":"non-stream reply"}}]}}`
		bcResp1 := prov_cn_batch_tencentRespFromBody(body)
		defer func() {
			if bcResp1 != nil {
				_ = bcResp1.Body.Close()
			}
		}()
		_, apiErr := a.DoResponse(c, bcResp1, &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "non-stream reply") {
			t.Errorf("expected non-stream response body, got %s", w.Body.String())
		}
	})

	t.Run("stream routes to tencentStreamHandler", func(t *testing.T) {
		c, w := prov_cn_batch_tencentGinContext()
		body := "data:" + `{"Choices":[{"Delta":{"Content":"stream reply"},"FinishReason":"stop"}]}` + "\n"
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
		bcResp0 := prov_cn_batch_tencentRespFromBody(body)
		defer func() {
			if bcResp0 != nil {
				_ = bcResp0.Body.Close()
			}
		}()
		_, apiErr := a.DoResponse(c, bcResp0, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "stream reply") {
			t.Errorf("expected streamed chunk, got %s", w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// parseTencentConfig
// ---------------------------------------------------------------------------

func TestParseTencentConfig(t *testing.T) {
	t.Run("valid config parses all three parts", func(t *testing.T) {
		appId, secretId, secretKey, err := parseTencentConfig("99|sid|skey")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if appId != 99 || secretId != "sid" || secretKey != "skey" {
			t.Errorf("got (%d, %q, %q), want (99, sid, skey)", appId, secretId, secretKey)
		}
	})

	t.Run("wrong part count rejected", func(t *testing.T) {
		_, _, _, err := parseTencentConfig("only|two")
		if err == nil {
			t.Fatal("expected error for wrong part count")
		}
	})
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	models := a.GetModelList()
	if len(models) == 0 {
		t.Fatal("expected non-empty model list")
	}
	if a.GetChannelName() != ChannelName {
		t.Errorf("channel name = %q, want %q", a.GetChannelName(), ChannelName)
	}
}
