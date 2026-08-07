package xai

// Business-acceptance tests filling the remaining gaps in the xai adaptor:
// DoRequest's end-to-end HTTP round trip (0% covered by the existing
// longtail suite), the image-generation branch of DoResponse, the
// nil-response guard of streamResponseXAI2OpenAI, and the malformed-chunk
// skip branch of xAIStreamHandler.

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

func provLt1XaiAllowPrivateIP(t *testing.T) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prev })
}

func provLt1XaiClosedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return "http://" + addr
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest(a, c, info, requestBody).
// ---------------------------------------------------------------------------

func TestXaiLt1_DoRequest_Success(t *testing.T) {
	app.InitHttpClient()
	provLt1XaiAllowPrivateIP(t)

	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	a := &Adaptor{}
	xc := newProvLongtailXaiCtx()
	xc.ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "xai-secret"},
		RequestURLPath: "/v1/chat/completions",
	}

	resp, err := a.DoRequest(xc.ctx, info, strings.NewReader(`{"model":"grok-3"}`))
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok {
		t.Fatalf("expected *http.Response, got %T", resp)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", httpResp.StatusCode)
	}
	if gotAuth != "Bearer xai-secret" {
		t.Errorf("upstream Authorization = %q, want %q", gotAuth, "Bearer xai-secret")
	}
	if gotBody != `{"model":"grok-3"}` {
		t.Errorf("upstream body = %q, want request body forwarded verbatim", gotBody)
	}
}

func TestXaiLt1_DoRequest_NetworkErrorWrapsDoRequestFailed(t *testing.T) {
	app.InitHttpClient()
	provLt1XaiAllowPrivateIP(t)

	a := &Adaptor{}
	xc := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: provLt1XaiClosedPortURL(t), ApiKey: "k"},
		RequestURLPath: "/v1/chat/completions",
	}

	_, err := a.DoRequest(xc.ctx, info, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "do request failed") {
		t.Fatalf("err = %v, want wrapped 'do request failed'", err)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: image-generation branch delegates to
// openai.OpenaiHandlerWithUsage instead of the xAI text handlers.
// ---------------------------------------------------------------------------

func TestXaiLt1_DoResponse_ImagesGeneration_DelegatesToOpenAIImageHandler(t *testing.T) {
	a := &Adaptor{}
	xc := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeImagesGenerations,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	body := `{"created":1700000000,"data":[{"url":"https://x/img.png"}]}`
	resp := xaiSSEBody(body)

	usage, apiErr := a.DoResponse(xc.ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage == nil {
		t.Fatal("expected non-nil usage envelope on success")
	}
	if !strings.Contains(xc.rec.Body.String(), "https://x/img.png") {
		t.Errorf("response body = %q, want the generated image URL forwarded", xc.rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// streamResponseXAI2OpenAI: nil-input guard.
// ---------------------------------------------------------------------------

func TestXaiLt1_StreamResponseXAI2OpenAI_NilInputReturnsNil(t *testing.T) {
	got := streamResponseXAI2OpenAI(nil, &dto.Usage{CompletionTokens: 3})
	if got != nil {
		t.Errorf("streamResponseXAI2OpenAI(nil, ...) = %+v, want nil", got)
	}
}

func TestXaiLt1_StreamResponseXAI2OpenAI_PropagatesFieldsAndUsage(t *testing.T) {
	in := &dto.ChatCompletionsStreamResponse{
		Id:      "c1",
		Object:  "chat.completion.chunk",
		Created: 42,
		Model:   "grok-3",
		Usage:   &dto.Usage{PromptTokens: 1, TotalTokens: 4},
	}
	usage := &dto.Usage{CompletionTokens: 3}
	got := streamResponseXAI2OpenAI(in, usage)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Id != "c1" || got.Model != "grok-3" || got.Created != 42 {
		t.Errorf("got = %+v, want fields copied from xAIResp", got)
	}
	if got.Usage.CompletionTokens != 3 {
		t.Errorf("Usage.CompletionTokens = %d, want 3 (overwritten from the accumulated usage)", got.Usage.CompletionTokens)
	}
}

// ---------------------------------------------------------------------------
// xAIStreamHandler: a malformed chunk must be skipped (logged, loop
// continues) rather than aborting the stream.
// ---------------------------------------------------------------------------

func TestXaiLt1_DoResponse_Stream_SkipsMalformedChunk(t *testing.T) {
	a := &Adaptor{}
	xc := newProvLongtailXaiCtx()
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-3"},
	}
	sse := "data: {malformed\n\n" +
		`data: {"id":"c1","model":"grok-3","choices":[{"delta":{"content":"ok"}}]}` + "\n\n" +
		"data: [DONE]\n\n"
	resp := xaiSSEBody(sse)

	usage, apiErr := a.DoResponse(xc.ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (the malformed chunk must not zero out the valid one that follows)", u.CompletionTokens)
	}
}
