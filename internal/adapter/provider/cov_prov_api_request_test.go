package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	common2 "github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// provReqCovAllowPrivateIP temporarily disables the relay SSRF dial guard's
// private-IP block so a test can round-trip against a loopback httptest
// server. Restored automatically via t.Cleanup so it never leaks into other
// tests (mirrors the pattern used by the openai adaptor's own DoRequest
// dispatch test).
func provReqCovAllowPrivateIP(t *testing.T) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prev })
}

// provReqCovAdaptor is a minimal Adaptor stub letting each test control the
// URL/header behaviour that DoApiRequest/DoFormRequest/DoWssRequest depend on.
type provReqCovAdaptor struct {
	BaseAdaptor
	url       string
	urlErr    error
	headerErr error
	headerSet map[string]string
}

func (a *provReqCovAdaptor) GetRequestURL(info *common.RelayInfo) (string, error) {
	return a.url, a.urlErr
}

func (a *provReqCovAdaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *common.RelayInfo) error {
	if a.headerErr != nil {
		return a.headerErr
	}
	for k, v := range a.headerSet {
		req.Set(k, v)
	}
	return nil
}

// provReqCovTaskAdaptor is a minimal TaskAdaptor stub for DoTaskApiRequest.
type provReqCovTaskAdaptor struct {
	url       string
	urlErr    error
	headerErr error
	headerSet map[string]string
}

func (a *provReqCovTaskAdaptor) Init(info *common.RelayInfo) {}
func (a *provReqCovTaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *common.RelayInfo) *dto.TaskError {
	return nil
}
func (a *provReqCovTaskAdaptor) BuildRequestURL(info *common.RelayInfo) (string, error) {
	return a.url, a.urlErr
}
func (a *provReqCovTaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *common.RelayInfo) error {
	if a.headerErr != nil {
		return a.headerErr
	}
	for k, v := range a.headerSet {
		req.Header.Set(k, v)
	}
	return nil
}
func (a *provReqCovTaskAdaptor) BuildRequestBody(c *gin.Context, info *common.RelayInfo) (io.Reader, error) {
	return nil, nil
}
func (a *provReqCovTaskAdaptor) DoRequest(c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return nil, nil
}
func (a *provReqCovTaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *common.RelayInfo) (string, []byte, *dto.TaskError) {
	return "", nil, nil
}
func (a *provReqCovTaskAdaptor) GetModelList() []string { return nil }
func (a *provReqCovTaskAdaptor) GetChannelName() string { return "" }
func (a *provReqCovTaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	return nil, nil
}
func (a *provReqCovTaskAdaptor) ParseTaskResult(respBody []byte) (*common.TaskInfo, error) {
	return nil, nil
}

var _ TaskAdaptor = (*provReqCovTaskAdaptor)(nil)

func provReqCovNewGinContext(t *testing.T, method, target string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, target, body)
	c.Request = req
	return c, w
}

// freeLocalPort finds a TCP port on 127.0.0.1 that is guaranteed to refuse
// connections (closed immediately after Listen), used to deterministically
// exercise the "do request failed" network-error branch without any real
// external traffic.
func provReqCovClosedPortURL(t *testing.T) string {
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

func TestSetupApiRequestHeader(t *testing.T) {
	tests := []struct {
		name           string
		relayMode      int
		isStream       bool
		reqContentType string
		reqAccept      string
		wantContentSet bool
		wantAccept     string
	}{
		{
			name:           "normal mode copies content-type and accept",
			relayMode:      relayModeChatCompletionsForTest,
			reqContentType: "application/json",
			reqAccept:      "application/json",
			wantContentSet: true,
			wantAccept:     "application/json",
		},
		{
			name:           "streaming with no client accept defaults to text/event-stream",
			relayMode:      relayModeChatCompletionsForTest,
			isStream:       true,
			reqContentType: "application/json",
			reqAccept:      "",
			wantContentSet: true,
			wantAccept:     "text/event-stream",
		},
		{
			name:           "streaming with explicit client accept is preserved",
			relayMode:      relayModeChatCompletionsForTest,
			isStream:       true,
			reqContentType: "application/json",
			reqAccept:      "application/json",
			wantContentSet: true,
			wantAccept:     "application/json",
		},
		{
			name:           "audio transcription mode leaves headers untouched (multipart)",
			relayMode:      relayModeAudioTranscriptionForTest,
			reqContentType: "multipart/form-data; boundary=x",
			wantContentSet: false,
		},
		{
			name:           "realtime mode leaves headers untouched (websocket)",
			relayMode:      relayModeRealtimeForTest,
			reqContentType: "application/json",
			wantContentSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)
			if tt.reqContentType != "" {
				c.Request.Header.Set("Content-Type", tt.reqContentType)
			}
			if tt.reqAccept != "" {
				c.Request.Header.Set("Accept", tt.reqAccept)
			}
			info := &common.RelayInfo{RelayMode: tt.relayMode, IsStream: tt.isStream}
			out := make(http.Header)
			SetupApiRequestHeader(info, c, &out)

			if tt.wantContentSet {
				if got := out.Get("Content-Type"); got != tt.reqContentType {
					t.Errorf("Content-Type = %q, want %q", got, tt.reqContentType)
				}
				if got := out.Get("Accept"); got != tt.wantAccept {
					t.Errorf("Accept = %q, want %q", got, tt.wantAccept)
				}
			} else {
				if len(out) != 0 {
					t.Errorf("expected no headers to be set for mode %d, got %v", tt.relayMode, out)
				}
			}
		})
	}
}

func TestDoApiRequest_SuccessAppliesHeaderOverrideAndAdaptorHeaders(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	origDebug := common2.DebugEnabled
	common2.DebugEnabled = true
	t.Cleanup(func() { common2.DebugEnabled = origDebug })

	var gotAuth, gotAdaptorHeader, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Auth")
		gotAdaptorHeader = r.Header.Get("X-Adaptor")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	adaptor := &provReqCovAdaptor{
		url:       server.URL + "/v1/x",
		headerSet: map[string]string{"X-Adaptor": "1"},
	}
	info := &common.RelayInfo{
		ChannelMeta: &common.ChannelMeta{
			ApiKey:          "secret",
			HeadersOverride: map[string]interface{}{"Auth": "Bearer {api_key}"},
		},
	}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	resp, err := DoApiRequest(adaptor, c, info, strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("DoApiRequest() error = %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	respBody, _ := io.ReadAll(resp.Body)
	if string(respBody) != "ok" {
		t.Fatalf("resp body = %q, want %q", respBody, "ok")
	}

	// Business assertions: header override variable substitution happened,
	// and the adaptor's own SetupRequestHeader contribution was preserved
	// alongside it (both must reach the upstream request).
	if gotAuth != "Bearer secret" {
		t.Errorf("upstream Auth header = %q, want %q", gotAuth, "Bearer secret")
	}
	if gotAdaptorHeader != "1" {
		t.Errorf("upstream X-Adaptor header = %q, want %q", gotAdaptorHeader, "1")
	}
	if gotBody != "payload" {
		t.Errorf("upstream body = %q, want %q", gotBody, "payload")
	}
}

func TestDoApiRequest_GetRequestURLError(t *testing.T) {
	app.InitHttpClient()
	adaptor := &provReqCovAdaptor{urlErr: errors.New("boom")}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	resp, err := DoApiRequest(adaptor, c, info, nil)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp != nil {
		t.Errorf("resp = %v, want nil", resp)
	}
	if err == nil || !strings.Contains(err.Error(), "get request url failed") {
		t.Fatalf("err = %v, want wrapped 'get request url failed'", err)
	}
}

func TestDoApiRequest_HeaderOverrideInvalidValueError(t *testing.T) {
	app.InitHttpClient()
	adaptor := &provReqCovAdaptor{url: "http://127.0.0.1/x"}
	info := &common.RelayInfo{
		ChannelMeta: &common.ChannelMeta{
			HeadersOverride: map[string]interface{}{"X-Num": 42}, // non-string -> invalid
		},
	}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	resp, err := DoApiRequest(adaptor, c, info, nil)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp != nil {
		t.Errorf("resp = %v, want nil", resp)
	}
	if err == nil {
		t.Fatal("expected error for non-string header override value")
	}
}

func TestDoApiRequest_SetupRequestHeaderError(t *testing.T) {
	app.InitHttpClient()
	adaptor := &provReqCovAdaptor{url: "http://127.0.0.1/x", headerErr: errors.New("no creds configured")}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	resp, err := DoApiRequest(adaptor, c, info, nil)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp != nil {
		t.Errorf("resp = %v, want nil", resp)
	}
	if err == nil || !strings.Contains(err.Error(), "setup request header failed") {
		t.Fatalf("err = %v, want wrapped 'setup request header failed'", err)
	}
}

func TestDoApiRequest_NetworkErrorWrapsDoRequestFailed(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	closedURL := provReqCovClosedPortURL(t)
	adaptor := &provReqCovAdaptor{url: closedURL}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	resp, err := DoApiRequest(adaptor, c, info, nil)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp != nil {
		t.Errorf("resp = %v, want nil", resp)
	}
	if err == nil || !strings.Contains(err.Error(), "do request failed") {
		t.Fatalf("err = %v, want wrapped 'do request failed'", err)
	}
}

func TestDoFormRequest_SuccessSetsFormContentType(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	var gotContentType, gotAuth, gotAdaptorHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Auth")
		gotAdaptorHeader = r.Header.Get("X-Adaptor")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	adaptor := &provReqCovAdaptor{url: server.URL + "/upload", headerSet: map[string]string{"X-Adaptor": "form"}}
	info := &common.RelayInfo{
		ChannelMeta: &common.ChannelMeta{
			ApiKey:          "form-secret",
			HeadersOverride: map[string]interface{}{"Auth": "Bearer {api_key}"},
		},
	}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=XYZ")

	resp, err := DoFormRequest(adaptor, c, info, bytes.NewBufferString("form-data"))
	if err != nil {
		t.Fatalf("DoFormRequest() error = %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if gotContentType != "multipart/form-data; boundary=XYZ" {
		t.Errorf("upstream Content-Type = %q, want the multipart boundary preserved", gotContentType)
	}
	// Business assertions: DoFormRequest must apply the header override
	// substitution AND the adaptor's own headers, exactly like DoApiRequest.
	if gotAuth != "Bearer form-secret" {
		t.Errorf("upstream Auth header = %q, want %q", gotAuth, "Bearer form-secret")
	}
	if gotAdaptorHeader != "form" {
		t.Errorf("upstream X-Adaptor header = %q, want %q", gotAdaptorHeader, "form")
	}
}

func TestDoFormRequest_GetRequestURLError(t *testing.T) {
	app.InitHttpClient()
	adaptor := &provReqCovAdaptor{urlErr: errors.New("no url")}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	bcResp7, err := DoFormRequest(adaptor, c, info, nil)
	defer func() {
		if bcResp7 != nil {
			_ = bcResp7.Body.Close()
		}
	}()
	if err == nil || !strings.Contains(err.Error(), "get request url failed") {
		t.Fatalf("err = %v, want wrapped 'get request url failed'", err)
	}
}

func TestDoFormRequest_HeaderOverrideInvalidValueError(t *testing.T) {
	app.InitHttpClient()
	adaptor := &provReqCovAdaptor{url: "http://127.0.0.1/x"}
	info := &common.RelayInfo{
		ChannelMeta: &common.ChannelMeta{
			HeadersOverride: map[string]interface{}{"X-Num": 42},
		},
	}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	bcResp6, err := DoFormRequest(adaptor, c, info, nil)
	defer func() {
		if bcResp6 != nil {
			_ = bcResp6.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error for non-string header override value")
	}
}

func TestDoFormRequest_SetupRequestHeaderError(t *testing.T) {
	app.InitHttpClient()
	adaptor := &provReqCovAdaptor{url: "http://127.0.0.1/x", headerErr: errors.New("no creds")}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	bcResp5, err := DoFormRequest(adaptor, c, info, nil)
	defer func() {
		if bcResp5 != nil {
			_ = bcResp5.Body.Close()
		}
	}()
	if err == nil || !strings.Contains(err.Error(), "setup request header failed") {
		t.Fatalf("err = %v, want wrapped 'setup request header failed'", err)
	}
}

func TestDoFormRequest_NetworkErrorWrapsDoRequestFailed(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	closedURL := provReqCovClosedPortURL(t)
	adaptor := &provReqCovAdaptor{url: closedURL}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	bcResp4, err := DoFormRequest(adaptor, c, info, nil)
	defer func() {
		if bcResp4 != nil {
			_ = bcResp4.Body.Close()
		}
	}()
	if err == nil || !strings.Contains(err.Error(), "do request failed") {
		t.Fatalf("err = %v, want wrapped 'do request failed'", err)
	}
}

func TestDoWssRequest_SuccessEstablishesDuplexConnection(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, append([]byte("echo:"), msg...))
	}))
	defer server.Close()

	wsURL := "ws://" + strings.TrimPrefix(server.URL, "http://")
	adaptor := &provReqCovAdaptor{url: wsURL, headerSet: map[string]string{"X-Adaptor": "ws"}}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	conn, err := DoWssRequest(adaptor, c, info, nil)
	if err != nil {
		t.Fatalf("DoWssRequest() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if string(msg) != "echo:hi" {
		t.Errorf("echoed message = %q, want %q (proves the duplex tunnel is actually live)", msg, "echo:hi")
	}
}

func TestDoWssRequest_GetRequestURLError(t *testing.T) {
	adaptor := &provReqCovAdaptor{urlErr: errors.New("no ws url")}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)

	_, err := DoWssRequest(adaptor, c, info, nil)
	if err == nil || !strings.Contains(err.Error(), "get request url failed") {
		t.Fatalf("err = %v, want wrapped 'get request url failed'", err)
	}
}

func TestDoWssRequest_HeaderOverrideError(t *testing.T) {
	adaptor := &provReqCovAdaptor{url: "ws://127.0.0.1/x"}
	info := &common.RelayInfo{
		ChannelMeta: &common.ChannelMeta{
			HeadersOverride: map[string]interface{}{"X-Bad": true},
		},
	}
	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)

	_, err := DoWssRequest(adaptor, c, info, nil)
	if err == nil {
		t.Fatal("expected error for invalid header override value")
	}
}

func TestDoWssRequest_SetupRequestHeaderError(t *testing.T) {
	adaptor := &provReqCovAdaptor{url: "ws://127.0.0.1/x", headerErr: errors.New("denied")}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)

	_, err := DoWssRequest(adaptor, c, info, nil)
	if err == nil || !strings.Contains(err.Error(), "setup request header failed") {
		t.Fatalf("err = %v, want wrapped 'setup request header failed'", err)
	}
}

func TestDoWssRequest_DialFailure(t *testing.T) {
	// Malformed scheme (not ws/wss) makes gorilla's dialer fail fast, purely local.
	adaptor := &provReqCovAdaptor{url: "http://127.0.0.1/x"}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)

	_, err := DoWssRequest(adaptor, c, info, nil)
	if err == nil || !strings.Contains(err.Error(), "dial failed to") {
		t.Fatalf("err = %v, want wrapped 'dial failed to'", err)
	}
}

func TestDoRequest_ExportedWrapperDelegatesToInternal(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
	// Deliberately use a non-nil (but empty) body reader here — see
	// TestDoRequest_NilRequestBodyPanicsOnSuccess_Finding below for the
	// separate, dedicated nil-body regression case.
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, server.URL, strings.NewReader(""))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}

	resp, err := DoRequest(c, req, info)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want %d (proves the exported wrapper actually round-trips through doRequest)", resp.StatusCode, http.StatusTeapot)
	}
}

// doRequest's success path (api_request.go, after client.Do) used to call
// req.Body.Close() with no nil check, so a bodyless GET built the idiomatic
// way — http.NewRequestWithContext(ctx, method, url, nil) leaves req.Body nil —
// panicked instead of returning cleanly. Reachable from any adaptor that hands
// provider.DoRequest a nil requestBody for a GET-style upstream call.
//
// fix_nil_body_close_test.go covers both nil-body shapes through the channel
// proxy transport; this one drives the direct (dial-guarded) transport, which
// is a different branch of doRequest's client selection.
func TestDoRequest_NilRequestBodyIsTolerated(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Body != nil {
		t.Fatal("test setup invariant broken: expected req.Body to be nil for a bodyless client GET request")
	}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}

	resp, err := DoRequest(c, req, info)
	if err != nil {
		t.Fatalf("DoRequest() with a nil req.Body error = %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDoRequest_ProxyClientConstructionError(t *testing.T) {
	app.InitHttpClient()
	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, "http://127.0.0.1/x", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	info := &common.RelayInfo{
		ChannelMeta: &common.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{Proxy: "://not-a-valid-proxy-url"},
		},
	}

	bcResp3, err := DoRequest(c, req, info)
	defer func() {
		if bcResp3 != nil {
			_ = bcResp3.Body.Close()
		}
	}()
	if err == nil || !strings.Contains(err.Error(), "new proxy http client failed") {
		t.Fatalf("err = %v, want wrapped 'new proxy http client failed'", err)
	}
}

func TestDoRequest_NonStreamBodyIsReadable(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello-non-stream"))
	}))
	defer server.Close()

	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, server.URL, strings.NewReader(""))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}, IsStream: false}

	resp, err := DoRequest(c, req, info)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading wrapped non-stream body: %v", err)
	}
	// Business meaning: doRequest wraps the body with a read-deadline guard for
	// non-stream responses (RELAY_TIMEOUT=0 hang protection); it must still be
	// fully readable end-to-end for normal-sized responses.
	if string(body) != "hello-non-stream" {
		t.Errorf("body = %q, want %q", body, "hello-non-stream")
	}
}

func TestDoTaskApiRequest_Success(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	var gotHeader, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Task")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	adaptor := &provReqCovTaskAdaptor{url: server.URL + "/task", headerSet: map[string]string{"X-Task": "1"}}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	resp, err := DoTaskApiRequest(adaptor, c, info, strings.NewReader("task-body"))
	if err != nil {
		t.Fatalf("DoTaskApiRequest() error = %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if gotHeader != "1" {
		t.Errorf("upstream X-Task header = %q, want %q", gotHeader, "1")
	}
	if gotBody != "task-body" {
		t.Errorf("upstream body = %q, want %q", gotBody, "task-body")
	}
}

func TestDoTaskApiRequest_BuildRequestURLErrorIsUnwrapped(t *testing.T) {
	app.InitHttpClient()
	sentinel := errors.New("task url unavailable")
	adaptor := &provReqCovTaskAdaptor{urlErr: sentinel}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	bcResp2, err := DoTaskApiRequest(adaptor, c, info, nil)
	defer func() {
		if bcResp2 != nil {
			_ = bcResp2.Body.Close()
		}
	}()
	// Business note: unlike DoApiRequest, DoTaskApiRequest returns the
	// BuildRequestURL error verbatim (no fmt.Errorf wrap) — callers relying on
	// errors.Is against the sentinel must keep working.
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want errors.Is(err, sentinel) to hold (no wrap)", err)
	}
}

func TestDoTaskApiRequest_BuildRequestHeaderError(t *testing.T) {
	app.InitHttpClient()
	adaptor := &provReqCovTaskAdaptor{url: "http://127.0.0.1/x", headerErr: errors.New("bad header")}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	bcResp1, err := DoTaskApiRequest(adaptor, c, info, nil)
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	if err == nil || !strings.Contains(err.Error(), "setup request header failed") {
		t.Fatalf("err = %v, want wrapped 'setup request header failed'", err)
	}
}

func TestDoTaskApiRequest_NetworkErrorWrapsDoRequestFailed(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)
	closedURL := provReqCovClosedPortURL(t)
	adaptor := &provReqCovTaskAdaptor{url: closedURL}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
	c, _ := provReqCovNewGinContext(t, http.MethodPost, "/x", nil)

	bcResp0, err := DoTaskApiRequest(adaptor, c, info, nil)
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err == nil || !strings.Contains(err.Error(), "do request failed") {
		t.Fatalf("err = %v, want wrapped 'do request failed'", err)
	}
}

// -- ping keep-alive machinery --

// provReqCovSyncRecorder is an http.ResponseWriter whose buffer is safe to read
// while a writer goroutine is still running. httptest.ResponseRecorder.Body is a
// plain bytes.Buffer, so polling it from the test while the pinger writes to it
// is a genuine data race, not a detector artifact.
type provReqCovSyncRecorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
	code   int
}

func newProvReqCovSyncRecorder() *provReqCovSyncRecorder {
	return &provReqCovSyncRecorder{header: make(http.Header), code: http.StatusOK}
}

func (r *provReqCovSyncRecorder) Header() http.Header { return r.header }

func (r *provReqCovSyncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *provReqCovSyncRecorder) WriteHeader(code int) { r.code = code }

// Flush is required: gin's ResponseWriter type-asserts the wrapped writer to
// http.Flusher, and the SSE ping path flushes after every frame.
func (r *provReqCovSyncRecorder) Flush() {}

func (r *provReqCovSyncRecorder) body() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func (r *provReqCovSyncRecorder) length() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Len()
}

func provReqCovPingContext(method, target string) (*gin.Context, *provReqCovSyncRecorder) {
	gin.SetMode(gin.TestMode)
	rec := newProvReqCovSyncRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, rec
}

func TestStartPingKeepAlive_WritesPingsThenStops(t *testing.T) {
	// Deliberately does NOT flip common2.DebugEnabled: stop() cancels the
	// pinger's context but does not wait for the goroutine to exit, so a
	// t.Cleanup restoring that global races the goroutine's own debug reads.
	c, rec := provReqCovPingContext(http.MethodGet, "/x")

	stop := startPingKeepAlive(c, 15*time.Millisecond)

	// Poll for at least one ping frame to appear, bounded so the test cannot
	// hang if the ticker mechanism regresses.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.body(), "PING") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(rec.body(), "PING") {
		t.Fatal("expected at least one SSE ping frame to be written before timeout")
	}

	stop()
	time.Sleep(50 * time.Millisecond)
	lenAfterStop := rec.length()
	time.Sleep(50 * time.Millisecond)
	if rec.length() != lenAfterStop {
		t.Error("expected no further writes after stopPinger() is called")
	}
}

func TestStartPingKeepAlive_StopsOnRequestContextDone(t *testing.T) {
	c, rec := provReqCovPingContext(http.MethodGet, "/x")
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)

	stop := startPingKeepAlive(c, 15*time.Millisecond)
	defer stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.body(), "PING") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(rec.body(), "PING") {
		t.Fatal("expected at least one ping before cancelling request context")
	}

	cancel()
	time.Sleep(60 * time.Millisecond)
	lenAfterCancel := rec.length()
	time.Sleep(60 * time.Millisecond)
	if rec.length() != lenAfterCancel {
		t.Error("expected the ping goroutine to stop writing once the request context is cancelled")
	}
}

func TestSendPingData_Success(t *testing.T) {
	c, w := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
	var mu sync.Mutex
	if err := sendPingData(c, &mu); err != nil {
		t.Fatalf("sendPingData() error = %v", err)
	}
	if !strings.Contains(w.Body.String(), "PING") {
		t.Errorf("body = %q, want it to contain a PING frame", w.Body.String())
	}
}

func TestSendPingData_RequestContextAlreadyCancelled(t *testing.T) {
	c, w := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)

	var mu sync.Mutex
	err := sendPingData(c, &mu)
	if err == nil {
		t.Fatal("expected error when request context is already cancelled")
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected no ping bytes written once context is cancelled, got %q", w.Body.String())
	}
}

var relayModeChatCompletionsForTest = relayconstant.RelayModeChatCompletions
var relayModeAudioTranscriptionForTest = relayconstant.RelayModeAudioTranscription
var relayModeRealtimeForTest = relayconstant.RelayModeRealtime
