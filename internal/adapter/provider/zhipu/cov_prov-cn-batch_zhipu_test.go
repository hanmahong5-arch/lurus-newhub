package zhipu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

// prov_cn_batch_zhipuCloseNotifyRecorder adds http.CloseNotifier to
// httptest.ResponseRecorder, which gin.Context.Stream() requires (zhipu's
// streaming handler uses c.Stream internally).
type prov_cn_batch_zhipuCloseNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (r *prov_cn_batch_zhipuCloseNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func prov_cn_batch_zhipuGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	w := &prov_cn_batch_zhipuCloseNotifyRecorder{rec}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, rec
}

func prov_cn_batch_zhipuRespFromBody(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	t.Run("non-stream uses invoke endpoint", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://open.bigmodel.cn", UpstreamModelName: "chatglm_pro"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://open.bigmodel.cn/api/paas/v3/model-api/chatglm_pro/invoke" {
			t.Errorf("url = %q", url)
		}
	})

	t.Run("stream uses sse-invoke endpoint", func(t *testing.T) {
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://open.bigmodel.cn", UpstreamModelName: "chatglm_pro"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://open.bigmodel.cn/api/paas/v3/model-api/chatglm_pro/sse-invoke" {
			t.Errorf("url = %q", url)
		}
	})
}

// ---------------------------------------------------------------------------
// getZhipuToken — local JWT signing (no network), cached, format-validated
// ---------------------------------------------------------------------------

func TestGetZhipuToken(t *testing.T) {
	t.Run("valid id.secret key produces a non-empty signed token and caches it", func(t *testing.T) {
		key := "prov-cn-batch-zhipu-id-1.prov-cn-batch-zhipu-secret-1"
		token1 := getZhipuToken(key)
		if token1 == "" {
			t.Fatal("expected a non-empty JWT for a valid two-part key")
		}
		if strings.Count(token1, ".") != 2 {
			t.Errorf("expected a JWT with 3 dot-separated segments, got %q", token1)
		}
		token2 := getZhipuToken(key)
		if token2 != token1 {
			t.Errorf("expected cached token to be reused within its expiry window, got different tokens")
		}
	})

	t.Run("malformed key (not exactly one dot) yields empty token, not a panic", func(t *testing.T) {
		if got := getZhipuToken("no-dot-here"); got != "" {
			t.Errorf("expected empty token for malformed key, got %q", got)
		}
		if got := getZhipuToken("a.b.c"); got != "" {
			t.Errorf("expected empty token for a key with more than one dot, got %q", got)
		}
	})
}

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipuGinContext()
	req := &http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "prov-cn-batch-zhipu-hdr-id.prov-cn-batch-zhipu-hdr-secret"}}
	if err := a.SetupRequestHeader(c, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("Authorization") == "" {
		t.Error("expected a non-empty Authorization token for a well-formed api key")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest — TopP clamp + request nil-check
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest(t *testing.T) {
	t.Run("nil request rejected", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_zhipuGinContext()
		_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
		if err == nil {
			t.Fatal("expected error for nil request")
		}
	})

	t.Run("TopP >= 1 clamped to 0.99 (zhipu rejects TopP==1)", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_zhipuGinContext()
		req := &dto.GeneralOpenAIRequest{TopP: 1.0}
		out, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		zr := out.(*ZhipuRequest)
		if zr.TopP != 0.99 {
			t.Errorf("TopP = %v, want clamped to 0.99", zr.TopP)
		}
		if req.TopP != 0.99 {
			t.Errorf("caller's request.TopP should also be mutated to 0.99, got %v", req.TopP)
		}
	})

	t.Run("TopP above 1 also clamped", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_zhipuGinContext()
		req := &dto.GeneralOpenAIRequest{TopP: 1.5}
		out, _ := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		zr := out.(*ZhipuRequest)
		if zr.TopP != 0.99 {
			t.Errorf("TopP = %v, want clamped to 0.99", zr.TopP)
		}
	})

	t.Run("TopP below 1 left untouched", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_zhipuGinContext()
		req := &dto.GeneralOpenAIRequest{TopP: 0.42}
		out, _ := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		zr := out.(*ZhipuRequest)
		if zr.TopP != 0.42 {
			t.Errorf("TopP = %v, want 0.42 unmodified", zr.TopP)
		}
	})
}

func TestAdaptor_ConvertRerankRequest_NotSupported(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipuGinContext()
	out, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if out != nil || err != nil {
		t.Errorf("expected (nil, nil) for unsupported rerank, got (%v, %v)", out, err)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Zhipu — system message split into system+user "Okay" ack
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Zhipu(t *testing.T) {
	t.Run("system message splits into system + synthetic user Okay ack", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages: []dto.Message{
				{Role: "system", Content: "be helpful"},
				{Role: "user", Content: "hi"},
			},
		}
		out := requestOpenAI2Zhipu(req)
		if len(out.Prompt) != 3 {
			t.Fatalf("expected 3 prompt entries (system split into 2 + user), got %d: %+v", len(out.Prompt), out.Prompt)
		}
		if out.Prompt[0].Role != "system" || out.Prompt[0].Content != "be helpful" {
			t.Errorf("prompt[0] = %+v, want system role with original content", out.Prompt[0])
		}
		if out.Prompt[1].Role != "user" || out.Prompt[1].Content != "Okay" {
			t.Errorf("prompt[1] = %+v, want synthetic user Okay ack", out.Prompt[1])
		}
		if out.Prompt[2].Role != "user" || out.Prompt[2].Content != "hi" {
			t.Errorf("prompt[2] = %+v, want original user message", out.Prompt[2])
		}
	})

	t.Run("empty messages produce empty prompt, no panic", func(t *testing.T) {
		out := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{Messages: []dto.Message{}})
		if len(out.Prompt) != 0 {
			t.Errorf("expected zero prompt entries, got %d", len(out.Prompt))
		}
	})

	t.Run("temperature/topP propagated, incremental always false", func(t *testing.T) {
		temp := 0.6
		out := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{Temperature: &temp, TopP: 0.8})
		if out.Temperature == nil || *out.Temperature != 0.6 {
			t.Errorf("Temperature = %v, want 0.6", out.Temperature)
		}
		if out.TopP != 0.8 {
			t.Errorf("TopP = %v, want 0.8", out.TopP)
		}
		if out.Incremental {
			t.Error("expected Incremental=false always (non-incremental prompt mode)")
		}
	})
}

// ---------------------------------------------------------------------------
// responseZhipu2OpenAI — only the LAST choice gets finish_reason=stop
// ---------------------------------------------------------------------------

func TestResponseZhipu2OpenAI(t *testing.T) {
	resp := &ZhipuResponse{
		Data: ZhipuResponseData{
			TaskId: "t1",
			Choices: []ZhipuMessage{
				{Role: "assistant", Content: "\"first\""},
				{Role: "assistant", Content: "\"second\""},
			},
		},
	}
	resp.Data.TotalTokens = 9
	out := responseZhipu2OpenAI(resp)
	if len(out.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(out.Choices))
	}
	if out.Choices[0].Content != "first" {
		t.Errorf("choice[0] content = %v, want quotes stripped -> first", out.Choices[0].Content)
	}
	if out.Choices[0].FinishReason != "" {
		t.Errorf("only the LAST choice should carry finish_reason=stop; choice[0] got %q", out.Choices[0].FinishReason)
	}
	if out.Choices[1].FinishReason != "stop" {
		t.Errorf("last choice finish_reason = %q, want stop", out.Choices[1].FinishReason)
	}
	if out.TotalTokens != 9 {
		t.Errorf("usage.TotalTokens = %d, want 9 (billing extraction)", out.TotalTokens)
	}
}

func TestResponseZhipu2OpenAI_NoChoices(t *testing.T) {
	resp := &ZhipuResponse{}
	out := responseZhipu2OpenAI(resp)
	if len(out.Choices) != 0 {
		t.Errorf("expected zero choices for empty upstream data, got %d", len(out.Choices))
	}
}

// ---------------------------------------------------------------------------
// streamResponseZhipu2OpenAI / streamMetaResponseZhipu2OpenAI
// ---------------------------------------------------------------------------

func TestStreamResponseZhipu2OpenAI(t *testing.T) {
	out := streamResponseZhipu2OpenAI("partial text")
	if out.Choices[0].Delta.GetContentString() != "partial text" {
		t.Errorf("delta content = %q, want 'partial text'", out.Choices[0].Delta.GetContentString())
	}
	if out.Choices[0].FinishReason != nil {
		t.Errorf("data chunks must not carry finish_reason, got %v", *out.Choices[0].FinishReason)
	}
}

func TestStreamMetaResponseZhipu2OpenAI(t *testing.T) {
	meta := &ZhipuStreamMetaResponse{RequestId: "req-9"}
	meta.PromptTokens = 3
	meta.CompletionTokens = 7
	meta.TotalTokens = 10
	resp, usage := streamMetaResponseZhipu2OpenAI(meta)
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Errorf("meta chunk must always carry finish_reason=stop, got %v", resp.Choices[0].FinishReason)
	}
	if resp.Id != "req-9" {
		t.Errorf("Id = %q, want req-9", resp.Id)
	}
	if usage.TotalTokens != 10 {
		t.Errorf("usage.TotalTokens = %d, want 10 (final billing figure comes from meta frame)", usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// zhipuHandler / zhipuStreamHandler / Adaptor.DoResponse
// ---------------------------------------------------------------------------

func TestZhipuHandler(t *testing.T) {
	t.Run("success response translated with usage extracted", func(t *testing.T) {
		c, w := prov_cn_batch_zhipuGinContext()
		body := `{"code":200,"msg":"ok","success":true,"data":{"task_id":"t1","choices":[{"role":"assistant","content":"\"hi there\""}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}}`
		bcResp6 := prov_cn_batch_zhipuRespFromBody(body)
		defer func() {
			if bcResp6 != nil {
				_ = bcResp6.Body.Close()
			}
		}()
		usage, apiErr := zhipuHandler(c, &relaycommon.RelayInfo{}, bcResp6)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage.TotalTokens != 5 {
			t.Errorf("usage.TotalTokens = %d, want 5", usage.TotalTokens)
		}
		if !strings.Contains(w.Body.String(), "hi there") {
			t.Errorf("response body missing translated (unquoted) content: %s", w.Body.String())
		}
	})

	t.Run("success=false surfaces classified upstream error, not silent 200", func(t *testing.T) {
		c, _ := prov_cn_batch_zhipuGinContext()
		body := `{"code":1234,"msg":"invalid parameter","success":false,"data":{}}`
		bcResp5 := prov_cn_batch_zhipuRespFromBody(body)
		defer func() {
			if bcResp5 != nil {
				_ = bcResp5.Body.Close()
			}
		}()
		usage, apiErr := zhipuHandler(c, &relaycommon.RelayInfo{}, bcResp5)
		if apiErr == nil {
			t.Fatal("expected error when success=false")
		}
		if usage != nil {
			t.Errorf("expected nil usage on error, got %v", usage)
		}
	})

	t.Run("malformed JSON classified as bad response body", func(t *testing.T) {
		c, _ := prov_cn_batch_zhipuGinContext()
		bcResp4 := prov_cn_batch_zhipuRespFromBody("{not json")
		defer func() {
			if bcResp4 != nil {
				_ = bcResp4.Body.Close()
			}
		}()
		_, apiErr := zhipuHandler(c, &relaycommon.RelayInfo{}, bcResp4)
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

func TestZhipuStreamHandler_DataAndMetaFrames(t *testing.T) {
	c, w := prov_cn_batch_zhipuGinContext()
	body := "data:He\n" + "data:llo\n" + `meta:{"request_id":"r1","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}` + "\n"
	bcResp3 := prov_cn_batch_zhipuRespFromBody(body)
	defer func() {
		if bcResp3 != nil {
			_ = bcResp3.Body.Close()
		}
	}()
	usage, apiErr := zhipuStreamHandler(c, &relaycommon.RelayInfo{}, bcResp3)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage extracted from the meta frame")
	}
	if usage.TotalTokens != 3 {
		t.Errorf("usage.TotalTokens = %d, want 3 (from meta frame)", usage.TotalTokens)
	}
	out := w.Body.String()
	if !strings.Contains(out, "He") || !strings.Contains(out, "llo") {
		t.Errorf("stream output missing forwarded data chunks: %s", out)
	}
	if !strings.Contains(out, "[DONE]") {
		t.Errorf("expected terminal [DONE] marker: %s", out)
	}
}

func TestZhipuStreamHandler_NoMetaFrame_UsageStaysNil(t *testing.T) {
	// FINDING: if the upstream stream ends without ever sending a "meta:"
	// frame (e.g. connection cut short), zhipuStreamHandler returns a nil
	// *dto.Usage with no error. Callers that assume DoResponse's usage return
	// is always non-nil on a nil error would nil-pointer-panic on this path.
	// Locking in the current behavior as a regression baseline.
	c, w := prov_cn_batch_zhipuGinContext()
	body := "data:only data, no meta\n"
	bcResp2 := prov_cn_batch_zhipuRespFromBody(body)
	defer func() {
		if bcResp2 != nil {
			_ = bcResp2.Body.Close()
		}
	}()
	usage, apiErr := zhipuStreamHandler(c, &relaycommon.RelayInfo{}, bcResp2)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage != nil {
		t.Errorf("expected nil usage when no meta frame arrives, got %+v", usage)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("expected terminal [DONE] marker even without usage: %s", w.Body.String())
	}
}

func TestAdaptor_DoResponse_Routing(t *testing.T) {
	a := &Adaptor{}
	t.Run("non-stream routes to zhipuHandler", func(t *testing.T) {
		c, w := prov_cn_batch_zhipuGinContext()
		body := `{"code":200,"msg":"ok","success":true,"data":{"choices":[{"role":"assistant","content":"non-stream"}]}}`
		bcResp1 := prov_cn_batch_zhipuRespFromBody(body)
		defer func() {
			if bcResp1 != nil {
				_ = bcResp1.Body.Close()
			}
		}()
		_, apiErr := a.DoResponse(c, bcResp1, &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "non-stream") {
			t.Errorf("expected non-stream response, got %s", w.Body.String())
		}
	})

	t.Run("stream routes to zhipuStreamHandler", func(t *testing.T) {
		c, w := prov_cn_batch_zhipuGinContext()
		body := "data:stream-chunk\n"
		info := &relaycommon.RelayInfo{IsStream: true}
		bcResp0 := prov_cn_batch_zhipuRespFromBody(body)
		defer func() {
			if bcResp0 != nil {
				_ = bcResp0.Body.Close()
			}
		}()
		_, apiErr := a.DoResponse(c, bcResp0, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "stream-chunk") {
			t.Errorf("expected streamed chunk, got %s", w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if len(a.GetModelList()) == 0 {
		t.Fatal("expected non-empty model list")
	}
	if a.GetChannelName() != ChannelName {
		t.Errorf("channel name = %q, want %q", a.GetChannelName(), ChannelName)
	}
}
