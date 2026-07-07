package baidu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
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

// seedToken stores a not-expiring cached access token under apiKey so that
// GetRequestURL can resolve it without any network call.
func seedToken(t *testing.T, apiKey, token string) {
	t.Helper()
	baiduTokenStore.Store(apiKey, BaiduAccessToken{
		AccessToken: token,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	})
	t.Cleanup(func() { baiduTokenStore.Delete(apiKey) })
}

// ---------------------------------------------------------------------------
// GetRequestURL — every suffix/case branch, using cached tokens to stay
// hermetic (no live call to the Baidu OAuth endpoint).
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		wantURL string
	}{
		{"ERNIE-4.0", "ERNIE-4.0", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions_pro"},
		{"ERNIE-Bot-4", "ERNIE-Bot-4", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions_pro"},
		{"ERNIE-Bot", "ERNIE-Bot", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions"},
		{"ERNIE-Bot-turbo", "ERNIE-Bot-turbo", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/eb-instant"},
		{"ERNIE-Speed", "ERNIE-Speed", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie_speed"},
		{"ERNIE-4.0-8K", "ERNIE-4.0-8K", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions_pro"},
		{"ERNIE-3.5-8K", "ERNIE-3.5-8K", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions"},
		{"ERNIE-3.5-8K-0205", "ERNIE-3.5-8K-0205", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie-3.5-8k-0205"},
		{"ERNIE-3.5-8K-1222", "ERNIE-3.5-8K-1222", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie-3.5-8k-1222"},
		{"ERNIE-Bot-8K", "ERNIE-Bot-8K", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie_bot_8k"},
		{"ERNIE-3.5-4K-0205", "ERNIE-3.5-4K-0205", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie-3.5-4k-0205"},
		{"ERNIE-Speed-8K", "ERNIE-Speed-8K", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie_speed"},
		{"ERNIE-Speed-128K", "ERNIE-Speed-128K", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie-speed-128k"},
		{"ERNIE-Lite-8K-0922", "ERNIE-Lite-8K-0922", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/eb-instant"},
		{"ERNIE-Lite-8K-0308", "ERNIE-Lite-8K-0308", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie-lite-8k"},
		{"ERNIE-Tiny-8K", "ERNIE-Tiny-8K", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/ernie-tiny-8k"},
		{"BLOOMZ-7B", "BLOOMZ-7B", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/bloomz_7b1"},
		{"Embedding-V1 (embeddings prefix)", "Embedding-V1", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/embedding-v1"},
		{"bge-large-zh (embeddings prefix)", "bge-large-zh", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/bge_large_zh"},
		{"bge-large-en (embeddings prefix)", "bge-large-en", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/bge_large_en"},
		{"tao-8k (embeddings prefix)", "tao-8k", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/tao_8k"},
		{"unknown model falls to default lowercased suffix", "Some-Custom-Model", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/some-custom-model"},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiKey := "key-for-" + tt.model
			seedToken(t, apiKey, "cached-tok")
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://aip.baidubce.com",
					ApiKey:            apiKey,
					UpstreamModelName: tt.model,
				},
			}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := tt.wantURL + "?access_token=cached-tok"
			if got != want {
				t.Errorf("GetRequestURL() = %q, want %q", got, want)
			}
		})
	}
}

func TestGetRequestURL_TokenError(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://aip.baidubce.com",
			ApiKey:            "no-pipe-in-this-key",
			UpstreamModelName: "ERNIE-Bot",
		},
	}
	got, err := a.GetRequestURL(info)
	if err == nil {
		t.Fatal("expected error for malformed apikey, got nil")
	}
	if err.Error() != "invalid baidu apikey" {
		t.Errorf("error = %q, want %q", err.Error(), "invalid baidu apikey")
	}
	if got != "" {
		t.Errorf("expected empty URL on error, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// getBaiduAccessToken / getBaiduAccessTokenHelper — token cache branches.
// ---------------------------------------------------------------------------

func TestGetBaiduAccessToken_CacheHitNotExpiring(t *testing.T) {
	apiKey := "cache-hit-fresh"
	seedToken(t, apiKey, "fresh-tok")
	tok, err := getBaiduAccessToken(apiKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "fresh-tok" {
		t.Errorf("token = %q, want %q", tok, "fresh-tok")
	}
}

func TestGetBaiduAccessToken_CacheHitSoonToExpire(t *testing.T) {
	apiKey := "cache-hit-expiring-no-pipe"
	// ExpiresAt already in the past triggers the async-refresh goroutine.
	// The apiKey has no "|" so the refresh helper fails immediately without
	// reaching the network; the synchronous return path is what's under test.
	baiduTokenStore.Store(apiKey, BaiduAccessToken{
		AccessToken: "stale-but-returned-tok",
		ExpiresAt:   time.Now().Add(-time.Hour),
	})
	t.Cleanup(func() { baiduTokenStore.Delete(apiKey) })

	tok, err := getBaiduAccessToken(apiKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "stale-but-returned-tok" {
		t.Errorf("token = %q, want %q", tok, "stale-but-returned-tok")
	}
}

func TestGetBaiduAccessToken_CacheTypeAssertionFailsFallsThrough(t *testing.T) {
	apiKey := "cache-wrong-type-no-pipe"
	// Store a value of the wrong type under the key: the `val.(BaiduAccessToken)`
	// assertion fails, so the code falls through to getBaiduAccessTokenHelper.
	baiduTokenStore.Store(apiKey, "not-a-BaiduAccessToken")
	t.Cleanup(func() { baiduTokenStore.Delete(apiKey) })

	tok, err := getBaiduAccessToken(apiKey)
	if err == nil {
		t.Fatal("expected error from fallthrough helper call, got nil")
	}
	if tok != "" {
		t.Errorf("token = %q, want empty", tok)
	}
	if err.Error() != "invalid baidu apikey" {
		t.Errorf("error = %q, want %q", err.Error(), "invalid baidu apikey")
	}
}

func TestGetBaiduAccessToken_CacheMissHelperError(t *testing.T) {
	apiKey := "not-cached-and-malformed"
	tok, err := getBaiduAccessToken(apiKey)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if tok != "" {
		t.Errorf("token = %q, want empty", tok)
	}
}

func TestGetBaiduAccessTokenHelper_InvalidFormats(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
	}{
		{"no pipe", "abcdefg"},
		{"too many parts", "a|b|c"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok, err := getBaiduAccessTokenHelper(tt.apiKey)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != "invalid baidu apikey" {
				t.Errorf("error = %q, want %q", err.Error(), "invalid baidu apikey")
			}
			if tok != nil {
				t.Errorf("token = %v, want nil", tok)
			}
		})
	}
}

// Note: getBaiduAccessTokenHelper's success/HTTP-response branches (valid
// "id|secret" apiKey format) require a live call to
// https://aip.baidubce.com/oauth/2.0/token and are not exercised here — no
// network I/O is permitted in this hermetic test file.

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test-key"}}
	header := http.Header{}

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer sk-test-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer sk-test-key")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest / ConvertRerankRequest / ConvertEmbeddingRequest
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

	t.Run("non-nil request is converted to BaiduChatRequest", func(t *testing.T) {
		maxTokens := uint(100)
		req := &dto.GeneralOpenAIRequest{
			Model:     "ERNIE-Bot",
			MaxTokens: maxTokens,
			User:      "user-1",
			Messages: []dto.Message{
				{Role: "system", Content: "be nice"},
				{Role: "user", Content: "hello"},
			},
		}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		baiduReq, ok := got.(*BaiduChatRequest)
		if !ok {
			t.Fatalf("expected *BaiduChatRequest, got %T", got)
		}
		if baiduReq.System != "be nice" {
			t.Errorf("System = %q, want %q", baiduReq.System, "be nice")
		}
		if len(baiduReq.Messages) != 1 || baiduReq.Messages[0].Content != "hello" {
			t.Errorf("Messages = %+v, want single user message with content %q", baiduReq.Messages, "hello")
		}
		if baiduReq.MaxOutputTokens == nil || *baiduReq.MaxOutputTokens != 100 {
			t.Errorf("MaxOutputTokens = %v, want 100", baiduReq.MaxOutputTokens)
		}
		if baiduReq.UserId != "user-1" {
			t.Errorf("UserId = %q, want %q", baiduReq.UserId, "user-1")
		}
	})

	t.Run("MaxTokens of exactly 1 is bumped to 2", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{MaxTokens: 1}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		baiduReq := got.(*BaiduChatRequest)
		if baiduReq.MaxOutputTokens == nil || *baiduReq.MaxOutputTokens != 2 {
			t.Errorf("MaxOutputTokens = %v, want 2", baiduReq.MaxOutputTokens)
		}
	})

	t.Run("MaxTokens of 0 leaves MaxOutputTokens nil", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		baiduReq := got.(*BaiduChatRequest)
		if baiduReq.MaxOutputTokens != nil {
			t.Errorf("MaxOutputTokens = %v, want nil", *baiduReq.MaxOutputTokens)
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
	req := dto.EmbeddingRequest{Input: []any{"foo", "bar"}}
	got, err := a.ConvertEmbeddingRequest(nil, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	baiduReq, ok := got.(*BaiduEmbeddingRequest)
	if !ok {
		t.Fatalf("expected *BaiduEmbeddingRequest, got %T", got)
	}
	if len(baiduReq.Input) != 2 || baiduReq.Input[0] != "foo" || baiduReq.Input[1] != "bar" {
		t.Errorf("Input = %+v, want [foo bar]", baiduReq.Input)
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
	if a.GetChannelName() != "baidu" {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), "baidu")
	}
}

// ---------------------------------------------------------------------------
// DoRequest — only the hermetically reachable early-error branch: a
// malformed ApiKey makes GetRequestURL fail before any HTTP call is issued.
// The success path (a real upstream POST) needs live network and is not
// exercised here.
// ---------------------------------------------------------------------------

func TestDoRequest_GetRequestURLErrorPropagates(t *testing.T) {
	_, c := newTestContext()
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://aip.baidubce.com",
			ApiKey:            "malformed-no-pipe",
			UpstreamModelName: "ERNIE-Bot",
		},
	}
	got, err := a.DoRequest(c, info, strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "get request url failed") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "get request url failed")
	}
	// NOTE: `got` is `any` wrapping a typed-nil *http.Response, so a plain
	// `got != nil` comparison would be true (classic Go interface-nil
	// gotcha); the meaningful assertion is the error above.
	if resp, ok := got.(*http.Response); ok && resp != nil {
		t.Errorf("expected nil *http.Response, got %v", resp)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Baidu / responseBaidu2OpenAI / embedding conversion helpers
// ---------------------------------------------------------------------------

func TestResponseBaidu2OpenAI(t *testing.T) {
	resp := &BaiduChatResponse{
		Id:      "resp-1",
		Object:  "chat",
		Created: 123,
		Result:  "the reply",
		Usage:   dto.Usage{TotalTokens: 10, PromptTokens: 4, CompletionTokens: 6},
	}
	got := responseBaidu2OpenAI(resp)
	if got.Id != "resp-1" || got.Object != "chat.completion" || got.Created != int64(123) {
		t.Errorf("unexpected envelope: %+v", got)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("Choices length = %d, want 1", len(got.Choices))
	}
	choice := got.Choices[0]
	if choice.Message.Role != "assistant" || choice.Message.Content != "the reply" || choice.FinishReason != "stop" {
		t.Errorf("choice = %+v, want assistant/the reply/stop", choice)
	}
	if got.Usage.TotalTokens != 10 {
		t.Errorf("Usage.TotalTokens = %d, want 10", got.Usage.TotalTokens)
	}
}

func TestStreamResponseBaidu2OpenAI(t *testing.T) {
	t.Run("not end of stream: no finish reason", func(t *testing.T) {
		baiduResp := &BaiduChatStreamResponse{
			BaiduChatResponse: BaiduChatResponse{Id: "s-1", Created: 1, Result: "partial"},
			IsEnd:             false,
		}
		got := streamResponseBaidu2OpenAI(baiduResp)
		if got.Id != "s-1" || got.Object != "chat.completion.chunk" || got.Model != "ernie-bot" {
			t.Errorf("unexpected envelope: %+v", got)
		}
		if len(got.Choices) != 1 {
			t.Fatalf("Choices length = %d, want 1", len(got.Choices))
		}
		if got.Choices[0].FinishReason != nil {
			t.Errorf("FinishReason = %v, want nil", *got.Choices[0].FinishReason)
		}
	})

	t.Run("end of stream: finish reason stop", func(t *testing.T) {
		baiduResp := &BaiduChatStreamResponse{
			BaiduChatResponse: BaiduChatResponse{Id: "s-2", Created: 2, Result: "final"},
			IsEnd:             true,
		}
		got := streamResponseBaidu2OpenAI(baiduResp)
		if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != pkgconstant.FinishReasonStop {
			t.Errorf("FinishReason = %v, want %q", got.Choices[0].FinishReason, pkgconstant.FinishReasonStop)
		}
	})
}

func TestEmbeddingResponseBaidu2OpenAI(t *testing.T) {
	resp := &BaiduEmbeddingResponse{
		Id: "e-1",
		Data: []BaiduEmbeddingData{
			{Object: "embedding", Embedding: []float64{0.1, 0.2}, Index: 0},
			{Object: "embedding", Embedding: []float64{0.3, 0.4}, Index: 1},
		},
		Usage: dto.Usage{TotalTokens: 5},
	}
	got := embeddingResponseBaidu2OpenAI(resp)
	if got.Object != "list" || got.Model != "baidu-embedding" {
		t.Errorf("unexpected envelope: %+v", got)
	}
	if len(got.Data) != 2 {
		t.Fatalf("Data length = %d, want 2", len(got.Data))
	}
	if got.Data[1].Index != 1 || got.Data[1].Embedding[0] != 0.3 {
		t.Errorf("Data[1] = %+v, want Index=1 Embedding[0]=0.3", got.Data[1])
	}
	if got.Usage.TotalTokens != 5 {
		t.Errorf("Usage.TotalTokens = %d, want 5", got.Usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// baiduHandler / baiduEmbeddingHandler — hermetic (only requires an
// io.Reader body, no network).
// ---------------------------------------------------------------------------

func TestBaiduHandler_Success(t *testing.T) {
	w, c := newTestContext()
	body := `{"id":"cmpl-1","object":"chat","created":100,"result":"hi there","usage":{"total_tokens":9,"prompt_tokens":3,"completion_tokens":6}}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	apiErr, usage := baiduHandler(c, info, resp)
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

func TestBaiduHandler_UpstreamErrorMsg(t *testing.T) {
	_, c := newTestContext()
	body := `{"error_code":17,"error_msg":"quota exceeded"}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	apiErr, usage := baiduHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for non-empty error_msg, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestBaiduHandler_MalformedJSON(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{not-json"))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	apiErr, usage := baiduHandler(c, info, resp)
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

func TestBaiduHandler_BodyReadError(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: errReader{}}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	apiErr, usage := baiduHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when body read fails, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestBaiduEmbeddingHandler_Success(t *testing.T) {
	w, c := newTestContext()
	body := `{"id":"emb-1","object":"list","data":[{"object":"embedding","embedding":[0.5,0.6],"index":0}],"usage":{"total_tokens":3}}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	apiErr, usage := baiduEmbeddingHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.TotalTokens != 3 {
		t.Errorf("TotalTokens = %d, want 3", usage.TotalTokens)
	}
	if w.Code != http.StatusOK {
		t.Errorf("http status written = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "application/json")
	}
}

func TestBaiduEmbeddingHandler_UpstreamErrorMsg(t *testing.T) {
	_, c := newTestContext()
	body := `{"error_code":18,"error_msg":"rate limited"}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	apiErr, usage := baiduEmbeddingHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for non-empty error_msg, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestBaiduEmbeddingHandler_MalformedJSON(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{not-json"))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	apiErr, usage := baiduEmbeddingHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestBaiduEmbeddingHandler_BodyReadError(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: errReader{}}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	apiErr, usage := baiduEmbeddingHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when body read fails, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// ---------------------------------------------------------------------------
// baiduStreamHandler — exercises the SSE scan loop against an in-memory
// reader (no network); hermetic because StreamScannerHandler only requires
// an io.Reader body, not a live connection.
// ---------------------------------------------------------------------------

func TestBaiduStreamHandler(t *testing.T) {
	origTimeout := pkgconstant.StreamingTimeout
	pkgconstant.StreamingTimeout = 30
	defer func() { pkgconstant.StreamingTimeout = origTimeout }()

	sse := "data: {\"id\":\"s-1\",\"result\":\"chunk one\",\"is_end\":false}\n\n" +
		"data: {\"id\":\"s-1\",\"result\":\"chunk two\",\"is_end\":true,\"usage\":{\"total_tokens\":8,\"prompt_tokens\":2,\"completion_tokens\":6}}\n\n"
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ERNIE-Bot"},
		DisablePing: true,
	}

	apiErr, usage := baiduStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if usage.TotalTokens != 8 {
		t.Errorf("TotalTokens = %d, want 8", usage.TotalTokens)
	}
	if usage.PromptTokens != 2 || usage.CompletionTokens != 6 {
		t.Errorf("Prompt/Completion = %d/%d, want 2/6", usage.PromptTokens, usage.CompletionTokens)
	}
}

func TestBaiduStreamHandler_MalformedEventIsSkipped(t *testing.T) {
	origTimeout := pkgconstant.StreamingTimeout
	pkgconstant.StreamingTimeout = 30
	defer func() { pkgconstant.StreamingTimeout = origTimeout }()

	sse := "data: {not-json}\n\n"
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ERNIE-Bot"},
		DisablePing: true,
	}

	apiErr, usage := baiduStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 (malformed event should be skipped)", usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// DoResponse — dispatch across IsStream / RelayMode branches.
// ---------------------------------------------------------------------------

func TestDoResponse_Dispatch(t *testing.T) {
	a := &Adaptor{}

	t.Run("stream", func(t *testing.T) {
		origTimeout := pkgconstant.StreamingTimeout
		pkgconstant.StreamingTimeout = 30
		defer func() { pkgconstant.StreamingTimeout = origTimeout }()

		_, c := newTestContext()
		sse := "data: {\"id\":\"s-1\",\"result\":\"hi\",\"is_end\":true}\n\n"
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

	t.Run("embeddings mode", func(t *testing.T) {
		_, c := newTestContext()
		body := `{"id":"e-1","data":[],"usage":{"total_tokens":1}}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 1 {
			t.Errorf("usage = %+v, want TotalTokens=1", usage)
		}
	})

	t.Run("default (chat) mode", func(t *testing.T) {
		_, c := newTestContext()
		body := `{"id":"c-1","result":"hi","usage":{"total_tokens":2}}`
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
