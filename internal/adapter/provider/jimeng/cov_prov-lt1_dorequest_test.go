package jimeng

// Business-acceptance tests filling the remaining gaps in the jimeng
// adaptor's money/wire path: DoRequest's end-to-end HTTP round trip (success,
// network failure, malformed method, unreadable request body) and DoResponse's
// non-image dispatch branches (stream/non-stream OpenAI-format passthrough),
// plus the base64 image-data branch of responseJimeng2OpenAIImage that the
// existing longtail suite only exercised via image URLs.

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func init() {
	// OaiStreamHandler delegates to helper.StreamScannerHandler, which builds
	// a time.NewTicker(StreamingTimeout*time.Second); a zero/unset value
	// panics ("non-positive interval"), so ensure a safe default here.
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

// provLt1JimengAllowPrivateIP lets DoRequest's underlying transport dial the
// loopback httptest.Server used below; the relay SSRF guard otherwise refuses
// private-IP destinations. Restored via t.Cleanup.
func provLt1JimengAllowPrivateIP(t *testing.T) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prev })
}

func provLt1JimengCtx(method, target string, body io.Reader) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	_ = body
	return c
}

// provLt1JimengErrorReader always fails on Read, modelling a client
// disconnecting mid-upload while the adaptor is composing the signed
// upstream request body.
type provLt1JimengErrorReader struct{}

func (provLt1JimengErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("connection reset by peer")
}

func provLt1JimengClosedPortURL(t *testing.T) string {
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
// DoRequest: full round trip through GetRequestURL -> http.NewRequest ->
// Sign -> provider.DoRequest.
// ---------------------------------------------------------------------------

func TestJimengLt1_DoRequest_Success(t *testing.T) {
	app.InitHttpClient()
	provLt1JimengAllowPrivateIP(t)

	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":10000}`))
	}))
	defer server.Close()

	a := &Adaptor{}
	c := provLt1JimengCtx(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "AK123|SK456"}}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"prompt":"a cat"}`))
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
	if !strings.HasPrefix(gotAuth, "HMAC-SHA256 Credential=AK123/") {
		t.Errorf("upstream Authorization = %q, want HMAC-SHA256 signature with access key AK123", gotAuth)
	}
	if gotBody != `{"prompt":"a cat"}` {
		t.Errorf("upstream body = %q, want request body forwarded verbatim", gotBody)
	}
}

func TestJimengLt1_DoRequest_NetworkErrorWrapsDoRequestFailed(t *testing.T) {
	app.InitHttpClient()
	provLt1JimengAllowPrivateIP(t)

	a := &Adaptor{}
	c := provLt1JimengCtx(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provLt1JimengClosedPortURL(t), ApiKey: "AK|SK"}}

	resp, err := a.DoRequest(c, info, strings.NewReader(""))
	if resp != nil {
		t.Errorf("resp = %v, want nil on connection failure", resp)
	}
	if err == nil || !strings.Contains(err.Error(), "do request failed") {
		t.Fatalf("err = %v, want wrapped 'do request failed'", err)
	}
}

func TestJimengLt1_DoRequest_UnreadableBody_SigningFails(t *testing.T) {
	a := &Adaptor{}
	c := provLt1JimengCtx(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://visual.volcengineapi.com", ApiKey: "AK|SK"}}

	resp, err := a.DoRequest(c, info, provLt1JimengErrorReader{})
	if resp != nil {
		t.Errorf("resp = %v, want nil", resp)
	}
	if err == nil || !strings.Contains(err.Error(), "setup request header failed") {
		t.Fatalf("err = %v, want wrapped 'setup request header failed' (Sign's body read must fail)", err)
	}
}

func TestJimengLt1_DoRequest_InvalidHTTPMethod(t *testing.T) {
	a := &Adaptor{}
	c := provLt1JimengCtx(http.MethodPost, "/v1/images/generations", nil)
	c.Request.Method = "BAD METHOD" // contains a space: rejected by http.NewRequest's method validation
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://visual.volcengineapi.com", ApiKey: "AK|SK"}}

	resp, err := a.DoRequest(c, info, strings.NewReader(""))
	if resp != nil {
		t.Errorf("resp = %v, want nil", resp)
	}
	if err == nil || !strings.Contains(err.Error(), "new request failed") {
		t.Fatalf("err = %v, want wrapped 'new request failed'", err)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: non-image branches (stream / non-stream) delegate to the
// shared openai handlers. This locks the dispatch decision itself, feeding
// real bodies through so the resulting usage/response are non-trivial.
// ---------------------------------------------------------------------------

func TestJimengLt1_DoResponse_NonStream_DelegatesToOpenAIHandler(t *testing.T) {
	a := &Adaptor{}
	c := provLt1JimengCtx(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: "openai",
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "some-jimeng-chat-model"},
	}
	body := `{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 5 {
		t.Errorf("TotalTokens = %d, want 5 (from upstream usage block)", u.TotalTokens)
	}
}

func TestJimengLt1_DoResponse_Stream_DelegatesToOaiStreamHandler(t *testing.T) {
	a := &Adaptor{}
	c := provLt1JimengCtx(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: "openai",
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "some-jimeng-chat-model"},
	}
	sse := `data: {"id":"c1","model":"m","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" + "data: [DONE]\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 for a non-empty streamed reply", u.CompletionTokens)
	}
}

// ---------------------------------------------------------------------------
// responseJimeng2OpenAIImage: base64 image-data branch (the existing longtail
// suite only covers the URL branch via jimengImageHandler success test).
// ---------------------------------------------------------------------------

func TestJimengLt1_ResponseJimeng2OpenAIImage_Base64Branch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
	body := `{"code":10000,"message":"success","data":{"binary_data_base64":["Zm9v","YmFy"]},"status":10000}`
	resp := &http.Response{StatusCode: 200, Body: newJimengBody(body)}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage == nil {
		t.Fatal("expected non-nil usage on success")
	}
	var out dto.ImageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if len(out.Data) != 2 {
		t.Fatalf("Data len = %d, want 2 (one per binary_data_base64 entry)", len(out.Data))
	}
	if out.Data[0].B64Json != "Zm9v" || out.Data[1].B64Json != "YmFy" {
		t.Errorf("Data = %+v, want B64Json entries Zm9v/YmFy carried through from binary_data_base64", out.Data)
	}
	if out.Data[0].Url != "" || out.Data[1].Url != "" {
		t.Errorf("Data = %+v, want empty Url for the base64 branch (no image_urls in response)", out.Data)
	}
}
