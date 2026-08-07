package suno

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// task_c_newGinContext builds a gin.Context/Recorder pair for suno adaptor
// tests that need a real *http.Request (method/body/content-type) plus
// optional route params.
func task_c_newGinContext(method, path, action, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	if action != "" {
		c.Params = gin.Params{{Key: "action", Value: action}}
	}
	return c, w
}

// task_c_allowLoopbackHTTP disables the SSRF private-IP dial guard so tests
// can round-trip against an httptest.Server on 127.0.0.1. Restored via the
// returned func before the test's next assertions run (no goroutines read
// this setting asynchronously, so a synchronous restore is safe).
func task_c_allowLoopbackHTTP() func() {
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	return func() { system_setting.GetFetchSetting().AllowPrivateIp = prev }
}

// ---------------------------------------------------------------------------
// DoRequest: end-to-end delegation to provider.DoTaskApiRequest, exercising
// URL build + header build + real HTTP round trip against a loopback server.
// ---------------------------------------------------------------------------

func TestDoRequest_RoundTripsThroughLoopbackServer(t *testing.T) {
	defer task_c_allowLoopbackHTTP()()

	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"code":"success","data":"submitted-task-1"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	c, _ := task_c_newGinContext(http.MethodPost, "/suno/submit/music", "music", `{"prompt":"hi"}`, "application/json")
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: "MUSIC"},
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "suno-live-key"},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"prompt":"hi"}`))
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
	if gotPath != "/suno/submit/MUSIC" {
		t.Errorf("upstream saw path %q, want /suno/submit/MUSIC (built via BuildRequestURL)", gotPath)
	}
	if gotAuth != "Bearer suno-live-key" {
		t.Errorf("upstream saw Authorization %q, want Bearer suno-live-key", gotAuth)
	}
	if !strings.Contains(gotBody, "hi") {
		t.Errorf("upstream did not receive the request body, got %q", gotBody)
	}
}

// ---------------------------------------------------------------------------
// BuildRequestBody: malformed JSON fallback-unmarshal error branch (no
// task_request pre-populated in context, so the handler must parse the raw
// body itself and surface the parse failure).
// ---------------------------------------------------------------------------

func TestBuildRequestBody_MalformedRawBodyErrors(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_c_newGinContext(http.MethodPost, "/x", "", `{"prompt":`, "application/json")
	_, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err == nil {
		t.Fatal("expected error for malformed JSON body with no context task_request, must not silently succeed")
	}
}

// ---------------------------------------------------------------------------
// DoResponse: upstream body read failure must be surfaced as a TaskError,
// not panic.
// ---------------------------------------------------------------------------

type task_c_erroringReadCloser struct{}

func (task_c_erroringReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (task_c_erroringReadCloser) Close() error              { return nil }

func TestDoResponse_BodyReadFailureReturnsTaskError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_c_newGinContext(http.MethodPost, "/x", "", "", "")
	resp := &http.Response{Body: task_c_erroringReadCloser{}}
	taskID, data, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error when upstream body read fails, must not panic")
	}
	if taskID != "" || data != nil {
		t.Errorf("taskID/data should be zero on read failure, got %q / %v", taskID, data)
	}
}

// ---------------------------------------------------------------------------
// FetchTask: JSON-marshal failure, malformed-URL NewRequest failure, and bad
// proxy client-construction failure — the three defensive error paths that
// a healthy submit/fetch round trip never exercises.
// ---------------------------------------------------------------------------

func TestFetchTask_BodyMarshalFailureReturnsError(t *testing.T) {
	a := &TaskAdaptor{}
	// A channel value is not JSON-marshalable; this exercises the real
	// json.Marshal error-return branch inside FetchTask without needing any
	// network I/O.
	_, err := a.FetchTask("https://example.com", "key", map[string]any{"bad": make(chan int)}, "")
	if err == nil {
		t.Fatal("expected error when task body cannot be marshalled to JSON")
	}
}

func TestFetchTask_InvalidBaseURLFailsToBuildRequest(t *testing.T) {
	a := &TaskAdaptor{}
	// A control character in the base URL makes url.Parse (inside
	// http.NewRequest) reject it outright.
	_, err := a.FetchTask("http://example.com/\x7f", "key", map[string]any{}, "")
	if err == nil {
		t.Fatal("expected error for malformed base URL, must not panic")
	}
}

func TestFetchTask_InvalidProxyFailsClientConstruction(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("https://example.com", "key", map[string]any{}, "://not-a-valid-proxy-url")
	if err == nil {
		t.Fatal("expected error when proxy URL cannot be parsed")
	}
	if !strings.Contains(err.Error(), "new proxy http client failed") {
		t.Errorf("err = %v, want wrapped 'new proxy http client failed'", err)
	}
}
