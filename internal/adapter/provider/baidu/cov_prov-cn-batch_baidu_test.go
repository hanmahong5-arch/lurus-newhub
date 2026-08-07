package baidu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	// StreamScannerHandler builds a time.NewTicker(StreamingTimeout*time.Second);
	// this must be positive or the ticker construction panics.
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

func prov_cn_batch_baiduGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, w
}

func prov_cn_batch_baiduRespFromBody(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL — URL suffix mapping + cached access token reuse
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	// Pre-seed the package-level token cache so GetRequestURL doesn't make a
	// real HTTP call to Baidu's OAuth endpoint.
	apiKey := "prov-cn-batch-key-1"
	baiduTokenStore.Store(apiKey, BaiduAccessToken{
		AccessToken: "cached-token-abc",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	})

	tests := []struct {
		name   string
		model  string
		suffix string
	}{
		{"exact match ERNIE-4.0 maps to completions_pro", "ERNIE-4.0", "chat/completions_pro"},
		{"exact match ERNIE-Bot maps to completions", "ERNIE-Bot", "chat/completions"},
		{"exact match ERNIE-Bot-turbo maps to eb-instant", "ERNIE-Bot-turbo", "chat/eb-instant"},
		{"Embedding-V1 prefix routes to embeddings/ and named suffix", "Embedding-V1", "embeddings/embedding-v1"},
		{"bge-large-zh prefix routes to embeddings/", "bge-large-zh", "embeddings/bge_large_zh"},
		{"tao-8k prefix routes to embeddings/", "tao-8k", "embeddings/tao_8k"},
		{"unknown model falls back to lowercased name", "ERNIE-Custom-Model", "chat/ernie-custom-model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ChannelBaseUrl:    "https://aip.baidubce.com",
				UpstreamModelName: tt.model,
				ApiKey:            apiKey,
			}}
			url, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantPrefix := "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/" + tt.suffix
			if !strings.HasPrefix(url, wantPrefix) {
				t.Errorf("url = %q, want prefix %q", url, wantPrefix)
			}
			if !strings.Contains(url, "access_token=cached-token-abc") {
				t.Errorf("url = %q, expected cached access_token appended (no network round-trip)", url)
			}
		})
	}

	t.Run("invalid api key format returns error before any network dial", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://aip.baidubce.com",
			UpstreamModelName: "ERNIE-Bot",
			ApiKey:            "no-pipe-separator-here",
		}}
		_, err := a.GetRequestURL(info)
		if err == nil {
			t.Fatal("expected error for malformed (non cached, non 'id|secret') api key, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduGinContext()
	req := &http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "id123|secret456"}}
	if err := a.SetupRequestHeader(c, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("Authorization") != "Bearer id123|secret456" {
		t.Errorf("Authorization = %q, want %q", req.Get("Authorization"), "Bearer id123|secret456")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest / requestOpenAI2Baidu — request translation
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest_NilRejected(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduGinContext()
	_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestAdaptor_ConvertOpenAIRequest_ValidRequestTranslated(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduGinContext()
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	out, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	baiduReq, ok := out.(*BaiduChatRequest)
	if !ok {
		t.Fatalf("expected *BaiduChatRequest, got %T", out)
	}
	if len(baiduReq.Messages) != 1 || baiduReq.Messages[0].Content != "hi" {
		t.Errorf("expected translated message content preserved, got %+v", baiduReq.Messages)
	}
}

func TestRequestOpenAI2Baidu(t *testing.T) {
	t.Run("system message extracted to System field, not included in Messages", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages: []dto.Message{
				{Role: "system", Content: "be concise"},
				{Role: "user", Content: "hello"},
			},
		}
		out := requestOpenAI2Baidu(req)
		if out.System != "be concise" {
			t.Errorf("System = %q, want %q", out.System, "be concise")
		}
		if len(out.Messages) != 1 || out.Messages[0].Role != "user" {
			t.Errorf("expected system message excluded from Messages, got %+v", out.Messages)
		}
	})

	t.Run("MaxTokens==1 is bumped to 2 (baidu rejects 1)", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{MaxTokens: 1}
		out := requestOpenAI2Baidu(req)
		if out.MaxOutputTokens == nil || *out.MaxOutputTokens != 2 {
			t.Errorf("MaxOutputTokens = %v, want pointer to 2", out.MaxOutputTokens)
		}
	})

	t.Run("MaxTokens==0 leaves MaxOutputTokens nil (no override)", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{MaxTokens: 0}
		out := requestOpenAI2Baidu(req)
		if out.MaxOutputTokens != nil {
			t.Errorf("MaxOutputTokens = %v, want nil when caller specified no limit", *out.MaxOutputTokens)
		}
	})

	t.Run("MaxTokens>1 passed through unchanged", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{MaxTokens: 500}
		out := requestOpenAI2Baidu(req)
		if out.MaxOutputTokens == nil || *out.MaxOutputTokens != 500 {
			t.Errorf("MaxOutputTokens = %v, want pointer to 500", out.MaxOutputTokens)
		}
	})

	t.Run("empty messages produce empty Messages slice, no panic", func(t *testing.T) {
		out := requestOpenAI2Baidu(dto.GeneralOpenAIRequest{Messages: []dto.Message{}})
		if len(out.Messages) != 0 {
			t.Errorf("expected zero messages, got %d", len(out.Messages))
		}
	})

	t.Run("temperature/topP/penalty/stream/user propagated", func(t *testing.T) {
		temp := 0.4
		req := dto.GeneralOpenAIRequest{
			Temperature:      &temp,
			TopP:             0.9,
			FrequencyPenalty: 1.1,
			Stream:           true,
			User:             "user-42",
		}
		out := requestOpenAI2Baidu(req)
		if out.Temperature == nil || *out.Temperature != 0.4 {
			t.Errorf("Temperature = %v, want 0.4", out.Temperature)
		}
		if out.TopP != 0.9 {
			t.Errorf("TopP = %v, want 0.9", out.TopP)
		}
		if out.PenaltyScore != 1.1 {
			t.Errorf("PenaltyScore (mapped from FrequencyPenalty) = %v, want 1.1", out.PenaltyScore)
		}
		if !out.Stream {
			t.Error("Stream flag not propagated")
		}
		if out.UserId != "user-42" {
			t.Errorf("UserId = %q, want user-42", out.UserId)
		}
	})
}

func TestAdaptor_ConvertRerankRequest_NotSupported(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduGinContext()
	out, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if err != nil || out != nil {
		t.Errorf("expected (nil, nil) passthrough for unsupported rerank, got (%v, %v)", out, err)
	}
}

func TestAdaptor_ConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduGinContext()
	req := dto.EmbeddingRequest{Input: "hello world"}
	out, err := a.ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	baiduReq, ok := out.(*BaiduEmbeddingRequest)
	if !ok {
		t.Fatalf("expected *BaiduEmbeddingRequest, got %T", out)
	}
	if len(baiduReq.Input) != 1 || baiduReq.Input[0] != "hello world" {
		t.Errorf("Input = %v, want [\"hello world\"]", baiduReq.Input)
	}
}

// ---------------------------------------------------------------------------
// responseBaidu2OpenAI / streamResponseBaidu2OpenAI / embeddingResponseBaidu2OpenAI
// ---------------------------------------------------------------------------

func TestResponseBaidu2OpenAI_UsageAndContent(t *testing.T) {
	resp := &BaiduChatResponse{
		Id:      "resp-1",
		Result:  "the answer",
		Created: 12345,
	}
	resp.Usage.PromptTokens = 7
	resp.Usage.CompletionTokens = 3
	resp.Usage.TotalTokens = 10
	out := responseBaidu2OpenAI(resp)
	if out.Choices[0].Message.Content != "the answer" {
		t.Errorf("content = %v, want 'the answer'", out.Choices[0].Message.Content)
	}
	if out.Choices[0].FinishReason != "stop" {
		t.Errorf("finish reason = %q, want stop", out.Choices[0].FinishReason)
	}
	if out.Usage.TotalTokens != 10 || out.Usage.PromptTokens != 7 {
		t.Errorf("usage not preserved for billing: got %+v", out.Usage)
	}
}

func TestStreamResponseBaidu2OpenAI_FinishReason(t *testing.T) {
	t.Run("IsEnd true sets stop finish reason", func(t *testing.T) {
		resp := &BaiduChatStreamResponse{IsEnd: true}
		resp.Result = "final chunk"
		out := streamResponseBaidu2OpenAI(resp)
		if out.Choices[0].Delta.GetContentString() != "final chunk" {
			t.Errorf("delta content = %q", out.Choices[0].Delta.GetContentString())
		}
		if out.Choices[0].FinishReason == nil || *out.Choices[0].FinishReason != "stop" {
			t.Errorf("expected stop finish reason when IsEnd, got %v", out.Choices[0].FinishReason)
		}
	})

	t.Run("IsEnd false leaves finish reason nil", func(t *testing.T) {
		resp := &BaiduChatStreamResponse{IsEnd: false}
		out := streamResponseBaidu2OpenAI(resp)
		if out.Choices[0].FinishReason != nil {
			t.Errorf("mid-stream chunk must not set finish reason, got %v", *out.Choices[0].FinishReason)
		}
	})
}

func TestEmbeddingResponseBaidu2OpenAI(t *testing.T) {
	resp := &BaiduEmbeddingResponse{
		Data: []BaiduEmbeddingData{
			{Object: "embedding", Index: 0, Embedding: []float64{0.1, 0.2}},
		},
	}
	resp.Usage.TotalTokens = 4
	out := embeddingResponseBaidu2OpenAI(resp)
	if len(out.Data) != 1 || out.Data[0].Index != 0 {
		t.Fatalf("expected 1 embedding item, got %+v", out.Data)
	}
	if out.Data[0].Embedding[0] != 0.1 {
		t.Errorf("embedding vector not preserved, got %v", out.Data[0].Embedding)
	}
	if out.Usage.TotalTokens != 4 {
		t.Errorf("usage.TotalTokens = %d, want 4 (billing must survive translation)", out.Usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// baiduHandler / baiduEmbeddingHandler / baiduStreamHandler — response path
// ---------------------------------------------------------------------------

func TestBaiduHandler_NonStreaming(t *testing.T) {
	t.Run("valid response written to client and usage extracted", func(t *testing.T) {
		c, w := prov_cn_batch_baiduGinContext()
		body := `{"id":"x1","result":"hi there","created":1,"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
		apiErr, usage := baiduHandler(c, &relaycommon.RelayInfo{}, prov_cn_batch_baiduRespFromBody(body))
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage.TotalTokens != 5 {
			t.Errorf("usage.TotalTokens = %d, want 5 (billing extraction)", usage.TotalTokens)
		}
		if !strings.Contains(w.Body.String(), "hi there") {
			t.Errorf("response body missing translated content: %s", w.Body.String())
		}
	})

	t.Run("upstream error_msg surfaces as error, not silently 200'd", func(t *testing.T) {
		c, _ := prov_cn_batch_baiduGinContext()
		body := `{"error_code":17,"error_msg":"open api daily request limit reached"}`
		apiErr, usage := baiduHandler(c, &relaycommon.RelayInfo{}, prov_cn_batch_baiduRespFromBody(body))
		if apiErr == nil {
			t.Fatal("expected error when upstream returns error_msg")
		}
		if usage != nil {
			t.Errorf("expected nil usage on upstream error, got %v", usage)
		}
	})

	t.Run("malformed JSON body classified as bad response body", func(t *testing.T) {
		c, _ := prov_cn_batch_baiduGinContext()
		apiErr, _ := baiduHandler(c, &relaycommon.RelayInfo{}, prov_cn_batch_baiduRespFromBody("{not json"))
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON body")
		}
	})
}

func TestBaiduEmbeddingHandler(t *testing.T) {
	t.Run("valid embedding response", func(t *testing.T) {
		c, w := prov_cn_batch_baiduGinContext()
		body := `{"id":"e1","data":[{"object":"embedding","index":0,"embedding":[0.5,0.6]}],"usage":{"total_tokens":8}}`
		apiErr, usage := baiduEmbeddingHandler(c, &relaycommon.RelayInfo{}, prov_cn_batch_baiduRespFromBody(body))
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage.TotalTokens != 8 {
			t.Errorf("usage.TotalTokens = %d, want 8", usage.TotalTokens)
		}
		if !strings.Contains(w.Body.String(), "0.5") {
			t.Errorf("response missing embedding vector: %s", w.Body.String())
		}
	})

	t.Run("upstream error surfaces", func(t *testing.T) {
		c, _ := prov_cn_batch_baiduGinContext()
		body := `{"error_code":1,"error_msg":"invalid request"}`
		apiErr, usage := baiduEmbeddingHandler(c, &relaycommon.RelayInfo{}, prov_cn_batch_baiduRespFromBody(body))
		if apiErr == nil {
			t.Fatal("expected error for upstream error_msg")
		}
		if usage != nil {
			t.Errorf("expected nil usage on error, got %v", usage)
		}
	})
}

func TestBaiduStreamHandler_AccumulatesUsageAndForwardsChunks(t *testing.T) {
	c, w := prov_cn_batch_baiduGinContext()
	body := "data: " + `{"id":"s1","result":"He","is_end":false}` + "\n\n" +
		"data: " + `{"id":"s1","result":"llo","is_end":true,"usage":{"prompt_tokens":1,"total_tokens":3}}` + "\n\n"
	apiErr, usage := baiduStreamHandler(c, &relaycommon.RelayInfo{}, prov_cn_batch_baiduRespFromBody(body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.TotalTokens != 3 {
		t.Errorf("usage.TotalTokens = %d, want 3 (from final chunk's usage block)", usage.TotalTokens)
	}
	if usage.CompletionTokens != 2 {
		t.Errorf("CompletionTokens = %d, want 2 (TotalTokens - PromptTokens derivation)", usage.CompletionTokens)
	}
	out := w.Body.String()
	if !strings.Contains(out, "He") || !strings.Contains(out, "llo") {
		t.Errorf("stream output missing forwarded deltas: %s", out)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse — routing between stream/embedding/chat handlers
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_Routing(t *testing.T) {
	a := &Adaptor{}

	t.Run("embedding relay mode routes to embedding handler", func(t *testing.T) {
		c, w := prov_cn_batch_baiduGinContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings}
		body := `{"id":"e1","data":[{"object":"embedding","index":0,"embedding":[0.1]}]}`
		_, apiErr := a.DoResponse(c, prov_cn_batch_baiduRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), `"object":"list"`) {
			t.Errorf("expected embedding-shaped response, got %s", w.Body.String())
		}
	})

	t.Run("default relay mode routes to chat handler", func(t *testing.T) {
		c, w := prov_cn_batch_baiduGinContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions}
		body := `{"id":"c1","result":"chat reply","usage":{"total_tokens":1}}`
		_, apiErr := a.DoResponse(c, prov_cn_batch_baiduRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "chat reply") {
			t.Errorf("expected chat-shaped response, got %s", w.Body.String())
		}
	})

	t.Run("IsStream true routes to stream handler regardless of relay mode", func(t *testing.T) {
		c, w := prov_cn_batch_baiduGinContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, IsStream: true}
		body := "data: " + `{"id":"s1","result":"stream reply","is_end":true}` + "\n\n"
		_, apiErr := a.DoResponse(c, prov_cn_batch_baiduRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "stream reply") {
			t.Errorf("expected streamed chunk in output, got %s", w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// getBaiduAccessTokenHelper — malformed key rejected before network dial
// ---------------------------------------------------------------------------

func TestGetBaiduAccessTokenHelper_InvalidKeyFormat(t *testing.T) {
	_, err := getBaiduAccessTokenHelper("no-pipe-here")
	if err == nil {
		t.Fatal("expected error for api key without exactly one '|' separator")
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	models := a.GetModelList()
	found := false
	for _, m := range models {
		if m == "ERNIE-4.0-8K" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ERNIE-4.0-8K in supported model list, got %v", models)
	}
	if a.GetChannelName() != "baidu" {
		t.Errorf("channel name = %q, want baidu", a.GetChannelName())
	}
}
