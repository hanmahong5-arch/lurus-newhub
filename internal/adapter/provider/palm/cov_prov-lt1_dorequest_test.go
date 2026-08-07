package palm

// Business-acceptance tests filling the remaining gaps in the palm adaptor:
// DoRequest's end-to-end HTTP round trip (0% covered by the existing
// longtail suite) and the "upstream body unreadable" branches of both
// palmHandler and palmStreamHandler (io.ReadAll failure, distinct from the
// already-covered "malformed JSON" branch which reads fine but fails to
// unmarshal).

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

func provLt1PalmAllowPrivateIP(t *testing.T) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prev })
}

func provLt1PalmClosedPortURL(t *testing.T) string {
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

// provLt1PalmErrorBody always fails on Read, modelling a connection dropped
// mid-response while the adaptor is buffering the upstream body.
type provLt1PalmErrorBody struct{}

func (provLt1PalmErrorBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (provLt1PalmErrorBody) Close() error              { return nil }

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest(a, c, info, requestBody).
// ---------------------------------------------------------------------------

func TestPalmLt1_DoRequest_Success(t *testing.T) {
	app.InitHttpClient()
	provLt1PalmAllowPrivateIP(t)

	var gotKey, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-goog-api-key")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[]}`))
	}))
	defer server.Close()

	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "palm-secret"}}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"prompt":{"messages":[]}}`))
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
	if gotKey != "palm-secret" {
		t.Errorf("upstream x-goog-api-key = %q, want %q", gotKey, "palm-secret")
	}
	if gotBody != `{"prompt":{"messages":[]}}` {
		t.Errorf("upstream body = %q, want request body forwarded verbatim", gotBody)
	}
}

func TestPalmLt1_DoRequest_NetworkErrorWrapsDoRequestFailed(t *testing.T) {
	app.InitHttpClient()
	provLt1PalmAllowPrivateIP(t)

	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provLt1PalmClosedPortURL(t), ApiKey: "k"}}

	_, err := a.DoRequest(c, info, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "do request failed") {
		t.Fatalf("err = %v, want wrapped 'do request failed'", err)
	}
}

// ---------------------------------------------------------------------------
// palmHandler / palmStreamHandler: unreadable upstream body.
// ---------------------------------------------------------------------------

func TestPalmLt1_DoResponse_NonStream_UnreadableBodyErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: provLt1PalmErrorBody{}}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error when the upstream body cannot be read")
	}
	if u, ok := usage.(*dto.Usage); ok && u != nil {
		t.Errorf("usage should be nil on read failure, got %+v", u)
	}
}

func TestPalmLt1_DoResponse_Stream_UnreadableBody_EmptyResponseTextNoPanic(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: provLt1PalmErrorBody{}}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 when the upstream body could not be read", u.CompletionTokens)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("stream must still terminate with [DONE] even on a read error, got %q", w.Body.String())
	}
}
