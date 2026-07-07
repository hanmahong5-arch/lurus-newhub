package zhipu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// closeNotifyRecorder wraps httptest.ResponseRecorder to satisfy
// http.CloseNotifier, which gin's Context.Stream requires of the
// underlying ResponseWriter (used by zhipuStreamHandler).
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func newTestContext() (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(&closeNotifyRecorder{w})
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return w, c
}

// errReader always fails on Read, to exercise the io.ReadAll error branch.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReader) Close() error               { return nil }

// ---------------------------------------------------------------------------
// GetRequestURL — stream vs non-stream method segment.
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name     string
		isStream bool
		wantURL  string
	}{
		{"non-stream uses invoke", false, "https://open.bigmodel.cn/api/paas/v3/model-api/chatglm_pro/invoke"},
		{"stream uses sse-invoke", true, "https://open.bigmodel.cn/api/paas/v3/model-api/chatglm_pro/sse-invoke"},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				IsStream: tt.isStream,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://open.bigmodel.cn",
					UpstreamModelName: "chatglm_pro",
				},
			}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantURL {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getZhipuToken — cache hit / malformed key / fresh sign.
// ---------------------------------------------------------------------------

func TestGetZhipuToken_CacheHitNotExpired(t *testing.T) {
	apiKey := "cache-hit-key.secret"
	zhipuTokens.Store(apiKey, zhipuTokenData{Token: "cached-token", ExpiryTime: time.Now().Add(time.Hour)})
	t.Cleanup(func() { zhipuTokens.Delete(apiKey) })

	got := getZhipuToken(apiKey)
	if got != "cached-token" {
		t.Errorf("getZhipuToken() = %q, want %q", got, "cached-token")
	}
}

func TestGetZhipuToken_MalformedKeyReturnsEmpty(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
	}{
		{"no dot", "nodothere"},
		{"too many parts", "a.b.c"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getZhipuToken(tt.apiKey)
			if got != "" {
				t.Errorf("getZhipuToken(%q) = %q, want empty", tt.apiKey, got)
			}
		})
	}
}

func TestGetZhipuToken_CacheExpiredSignsFreshToken(t *testing.T) {
	apiKey := "expired-cache-key.mysecret"
	zhipuTokens.Store(apiKey, zhipuTokenData{Token: "stale-token", ExpiryTime: time.Now().Add(-time.Hour)})
	t.Cleanup(func() { zhipuTokens.Delete(apiKey) })

	got := getZhipuToken(apiKey)
	if got == "" {
		t.Fatal("expected a freshly signed JWT, got empty string")
	}
	if got == "stale-token" {
		t.Errorf("expected a freshly signed token distinct from the stale cached one, got %q", got)
	}
	// A JWT has 3 dot-separated segments (header.payload.signature).
	if parts := strings.Split(got, "."); len(parts) != 3 {
		t.Errorf("token = %q, want a 3-segment JWT", got)
	}

	// Second call within the fresh TTL should now hit the cache and return
	// the exact same token string.
	got2 := getZhipuToken(apiKey)
	if got2 != got {
		t.Errorf("second call = %q, want cached %q", got2, got)
	}
}

func TestGetZhipuToken_NoCacheEntrySignsFreshToken(t *testing.T) {
	apiKey := "brand-new-key.freshsecret"
	t.Cleanup(func() { zhipuTokens.Delete(apiKey) })

	got := getZhipuToken(apiKey)
	if got == "" {
		t.Fatal("expected a freshly signed JWT, got empty string")
	}
	if parts := strings.Split(got, "."); len(parts) != 3 {
		t.Errorf("token = %q, want a 3-segment JWT", got)
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	_, c := newTestContext()
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "application/json")

	apiKey := "header-test-key.headersecret"
	t.Cleanup(func() { zhipuTokens.Delete(apiKey) })
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: apiKey}}
	header := http.Header{}

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", header.Get("Content-Type"), "application/json")
	}
	wantToken := getZhipuToken(apiKey)
	if header.Get("Authorization") != wantToken {
		t.Errorf("Authorization = %q, want %q", header.Get("Authorization"), wantToken)
	}
	if header.Get("Authorization") == "" {
		t.Error("expected a non-empty Authorization header")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest / ConvertRerankRequest
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

	t.Run("TopP >= 1 is clamped to 0.99", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{TopP: 1}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.TopP != 0.99 {
			t.Errorf("request.TopP mutated to = %v, want 0.99", req.TopP)
		}
		zr, ok := got.(*ZhipuRequest)
		if !ok {
			t.Fatalf("expected *ZhipuRequest, got %T", got)
		}
		if zr.TopP != 0.99 {
			t.Errorf("ZhipuRequest.TopP = %v, want 0.99", zr.TopP)
		}
	})

	t.Run("TopP below 1 is left untouched", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{TopP: 0.5}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		zr := got.(*ZhipuRequest)
		if zr.TopP != 0.5 {
			t.Errorf("ZhipuRequest.TopP = %v, want 0.5", zr.TopP)
		}
	})

	t.Run("system message is followed by a synthetic user Okay message", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Messages: []dto.Message{
				{Role: "system", Content: "be nice"},
				{Role: "user", Content: "hello"},
			},
		}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		zr := got.(*ZhipuRequest)
		if len(zr.Prompt) != 3 {
			t.Fatalf("Prompt length = %d, want 3 (system, synthetic Okay, user)", len(zr.Prompt))
		}
		if zr.Prompt[0].Role != "system" || zr.Prompt[0].Content != "be nice" {
			t.Errorf("Prompt[0] = %+v, want system/be nice", zr.Prompt[0])
		}
		if zr.Prompt[1].Role != "user" || zr.Prompt[1].Content != "Okay" {
			t.Errorf("Prompt[1] = %+v, want user/Okay", zr.Prompt[1])
		}
		if zr.Prompt[2].Role != "user" || zr.Prompt[2].Content != "hello" {
			t.Errorf("Prompt[2] = %+v, want user/hello", zr.Prompt[2])
		}
		if zr.Incremental {
			t.Error("Incremental should always be false")
		}
	})

	t.Run("Temperature is forwarded unchanged", func(t *testing.T) {
		temp := 0.7
		req := &dto.GeneralOpenAIRequest{Temperature: &temp}
		got, _ := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		zr := got.(*ZhipuRequest)
		if zr.Temperature == nil || *zr.Temperature != 0.7 {
			t.Errorf("Temperature = %v, want 0.7", zr.Temperature)
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
	wantModels := []string{"chatglm_turbo", "chatglm_pro", "chatglm_std", "chatglm_lite"}
	if len(got) != len(wantModels) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(wantModels))
	}
	for i, m := range wantModels {
		if got[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], m)
		}
	}
	if a.GetChannelName() != "zhipu" {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), "zhipu")
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Zhipu / responseZhipu2OpenAI / stream conversion helpers
// ---------------------------------------------------------------------------

func TestResponseZhipu2OpenAI(t *testing.T) {
	resp := &ZhipuResponse{
		Data: ZhipuResponseData{
			TaskId: "task-1",
			Choices: []ZhipuMessage{
				{Role: "assistant", Content: "\"first\""},
				{Role: "assistant", Content: "\"second\""},
			},
			Usage: dto.Usage{TotalTokens: 10, PromptTokens: 4, CompletionTokens: 6},
		},
	}
	got := responseZhipu2OpenAI(resp)
	if got.Id != "task-1" || got.Object != "chat.completion" {
		t.Errorf("unexpected envelope: %+v", got)
	}
	if len(got.Choices) != 2 {
		t.Fatalf("Choices length = %d, want 2", len(got.Choices))
	}
	if got.Choices[0].Index != 0 || got.Choices[0].Content != "first" || got.Choices[0].FinishReason != "" {
		t.Errorf("Choices[0] = %+v, want Index=0 Content=first FinishReason=empty", got.Choices[0])
	}
	if got.Choices[1].Index != 1 || got.Choices[1].Content != "second" || got.Choices[1].FinishReason != "stop" {
		t.Errorf("Choices[1] = %+v, want Index=1 Content=second FinishReason=stop", got.Choices[1])
	}
	if got.TotalTokens != 10 {
		t.Errorf("Usage.TotalTokens = %d, want 10", got.TotalTokens)
	}
}

func TestStreamResponseZhipu2OpenAI(t *testing.T) {
	got := streamResponseZhipu2OpenAI("hello chunk")
	if got.Object != "chat.completion.chunk" || got.Model != "chatglm" {
		t.Errorf("unexpected envelope: %+v", got)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("Choices length = %d, want 1", len(got.Choices))
	}
	content := got.Choices[0].Delta.GetContentString()
	if content != "hello chunk" {
		t.Errorf("Delta content = %q, want %q", content, "hello chunk")
	}
}

func TestStreamMetaResponseZhipu2OpenAI(t *testing.T) {
	meta := &ZhipuStreamMetaResponse{
		RequestId: "req-1",
		Usage:     dto.Usage{TotalTokens: 7, PromptTokens: 2, CompletionTokens: 5},
	}
	resp, usage := streamMetaResponseZhipu2OpenAI(meta)
	if resp.Id != "req-1" || resp.Object != "chat.completion.chunk" || resp.Model != "chatglm" {
		t.Errorf("unexpected envelope: %+v", resp)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices length = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %v, want stop", resp.Choices[0].FinishReason)
	}
	content := resp.Choices[0].Delta.GetContentString()
	if content != "" {
		t.Errorf("Delta content = %q, want empty", content)
	}
	if usage == nil || usage.TotalTokens != 7 {
		t.Errorf("usage = %+v, want TotalTokens=7", usage)
	}
}

// ---------------------------------------------------------------------------
// zhipuHandler — hermetic (only requires an io.Reader body, no network).
// ---------------------------------------------------------------------------

func TestZhipuHandler_Success(t *testing.T) {
	w, c := newTestContext()
	body := `{"success":true,"data":{"task_id":"t1","choices":[{"role":"assistant","content":"hi there"}],"usage":{"total_tokens":9,"prompt_tokens":3,"completion_tokens":6}}}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := zhipuHandler(c, info, resp)
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

func TestZhipuHandler_UpstreamFailure(t *testing.T) {
	_, c := newTestContext()
	body := `{"success":false,"code":1234,"msg":"quota exceeded"}`
	resp := &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := zhipuHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for success=false, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
	if !strings.Contains(apiErr.Error(), "quota exceeded") {
		t.Errorf("error = %q, want it to contain %q", apiErr.Error(), "quota exceeded")
	}
}

func TestZhipuHandler_MalformedJSON(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{not-json"))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := zhipuHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestZhipuHandler_BodyReadError(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: errReader{}}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := zhipuHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when body read fails, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// ---------------------------------------------------------------------------
// zhipuStreamHandler — exercises the scan goroutine + c.Stream loop against
// an in-memory reader (no network).
// ---------------------------------------------------------------------------

func TestZhipuStreamHandler_DataAndMetaEvents(t *testing.T) {
	w, c := newTestContext()
	sse := "data:chunk one\n" +
		"meta:{\"request_id\":\"r-1\",\"usage\":{\"total_tokens\":11,\"prompt_tokens\":3,\"completion_tokens\":8}}\n"
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{}

	usage, apiErr := zhipuStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if usage.TotalTokens != 11 {
		t.Errorf("TotalTokens = %d, want 11", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), "chunk one") {
		t.Errorf("streamed body = %q, want it to contain the data chunk", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "data: [DONE]") {
		t.Errorf("streamed body = %q, want the terminal DONE event", w.Body.String())
	}
}

func TestZhipuStreamHandler_MalformedMetaIsSkipped(t *testing.T) {
	_, c := newTestContext()
	sse := "meta:{not-json}\n"
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{}

	usage, apiErr := zhipuStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil (malformed meta event skipped, no usage ever set)", usage)
	}
}

func TestZhipuStreamHandler_NoEventsOnlyDone(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}
	info := &relaycommon.RelayInfo{}

	usage, apiErr := zhipuStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// ---------------------------------------------------------------------------
// DoResponse — dispatch across IsStream branches.
// ---------------------------------------------------------------------------

func TestDoResponse_Dispatch(t *testing.T) {
	a := &Adaptor{}

	t.Run("stream", func(t *testing.T) {
		_, c := newTestContext()
		sse := "meta:{\"request_id\":\"r-2\",\"usage\":{\"total_tokens\":5}}\n"
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse))}
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 5 {
			t.Errorf("usage = %+v, want *dto.Usage{TotalTokens:5}", usage)
		}
	})

	t.Run("non-stream", func(t *testing.T) {
		_, c := newTestContext()
		body := `{"success":true,"data":{"task_id":"t2","usage":{"total_tokens":3}}}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.TotalTokens != 3 {
			t.Errorf("usage = %+v, want *dto.Usage{TotalTokens:3}", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// DoRequest — not hermetically exercisable: GetRequestURL for zhipu never
// errors, so DoApiRequest always proceeds to a live upstream HTTP call
// (https://open.bigmodel.cn/...). No network I/O is permitted in this
// hermetic test file, so DoRequest's success/failure paths are not covered
// here.
// ---------------------------------------------------------------------------
