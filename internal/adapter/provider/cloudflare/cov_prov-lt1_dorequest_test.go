package cloudflare

// Business-acceptance tests filling the remaining gaps in the cloudflare
// adaptor: DoRequest's end-to-end HTTP round trip (0% covered by the
// existing longtail suite, which only reached DoResponse's sub-handlers
// directly), the RelayModeResponses dispatch branches of DoResponse, and the
// "line shorter than the SSE 'data: ' prefix" skip branch inside
// cfStreamHandler.

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

func init() {
	// OaiResponsesStreamHandler delegates to helper.StreamScannerHandler,
	// which builds a time.NewTicker(StreamingTimeout*time.Second); a
	// zero/unset value panics ("non-positive interval").
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

func provLt1CfAllowPrivateIP(t *testing.T) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prev })
}

func provLt1CfClosedPortURL(t *testing.T) string {
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

func TestCloudflareLt1_DoRequest_Success(t *testing.T) {
	app.InitHttpClient()
	provLt1CfAllowPrivateIP(t)

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
	c, _ := provLongtailCfCtx(http.MethodPost, "/v1/chat/completions")
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiVersion: "acct1", ApiKey: "cf-secret"},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok {
		t.Fatalf("expected *http.Response, got %T", resp)
	}
	defer func() {
		if httpResp != nil {
			_ = httpResp.Body.Close()
		}
	}()
	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", httpResp.StatusCode)
	}
	if gotAuth != "Bearer cf-secret" {
		t.Errorf("upstream Authorization = %q, want %q", gotAuth, "Bearer cf-secret")
	}
	if gotBody != `{"model":"m"}` {
		t.Errorf("upstream body = %q, want request body forwarded verbatim", gotBody)
	}
}

func TestCloudflareLt1_DoRequest_NetworkErrorWrapsDoRequestFailed(t *testing.T) {
	app.InitHttpClient()
	provLt1CfAllowPrivateIP(t)

	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/v1/chat/completions")
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provLt1CfClosedPortURL(t), ApiVersion: "acct1", ApiKey: "k"},
	}

	_, err := a.DoRequest(c, info, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "do request failed") {
		t.Fatalf("err = %v, want wrapped 'do request failed'", err)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: RelayModeResponses branches, delegated to the shared openai
// Responses-API handlers.
// ---------------------------------------------------------------------------

func TestCloudflareLt1_DoResponse_ResponsesMode_NonStream(t *testing.T) {
	a := &Adaptor{}
	c, w := provLongtailCfCtx(http.MethodPost, "/v1/responses")
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	body := `{"id":"resp1","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15 (from upstream usage block)", u.TotalTokens)
	}
	if w.Code != 0 && w.Code != 200 {
		t.Errorf("recorder status = %d, want default/200", w.Code)
	}
}

func TestCloudflareLt1_DoResponse_ResponsesMode_Stream(t *testing.T) {
	a := &Adaptor{}
	c, _ := provLongtailCfCtx(http.MethodPost, "/v1/responses")
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeResponses,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	body := `data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"r1","status":"completed","output":[],"usage":{"input_tokens":6,"output_tokens":4,"total_tokens":10}}}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 10 || u.PromptTokens != 6 || u.CompletionTokens != 4 {
		t.Errorf("usage = %+v, want prompt=6 completion=4 total=10", u)
	}
}

// ---------------------------------------------------------------------------
// cfStreamHandler: a line shorter than "data: " must be skipped rather than
// panicking on the TrimPrefix/slice below the prefix check.
// ---------------------------------------------------------------------------

func TestCloudflareLt1_DoResponse_Stream_SkipsShortLines(t *testing.T) {
	a := &Adaptor{}
	c, w := provLongtailCfCtx(http.MethodPost, "/v1/chat/completions")
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		IsStream:    true,
		StartTime:   time.Unix(1700000000, 0),
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "m"},
	}
	// A blank line and a short "data" line (no colon/space) are both shorter
	// than len("data: ") == 6 and must be skipped without affecting the
	// eventually-accumulated response text.
	sseBody := "\n" + "da\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n" +
		"data: [DONE]\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sseBody))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u := usage.(*dto.Usage)
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (short lines must not abort accumulation of the valid chunk)", u.CompletionTokens)
	}
	_ = w
}
