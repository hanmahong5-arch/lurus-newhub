package music

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

func task_c_newGinContext(method, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, "/v1/audio/music", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, w
}

func task_c_allowLoopbackHTTP() func() {
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	return func() { system_setting.GetFetchSetting().AllowPrivateIp = prev }
}

// ---------------------------------------------------------------------------
// DoRequest: end-to-end delegation through provider.DoTaskApiRequest
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
		_, _ = w.Write([]byte(`{"code":"success","data":"music-task-1"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	c, _ := task_c_newGinContext(http.MethodPost, `{"prompt":"upbeat pop","style":"pop"}`, "application/json")
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "music-live-key"},
	}
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	body, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected BuildRequestBody error: %v", err)
	}

	resp, err := a.DoRequest(c, info, body)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
	if gotPath != "/suno/submit/music" {
		t.Errorf("upstream saw path %q, want /suno/submit/music", gotPath)
	}
	if gotAuth != "Bearer music-live-key" {
		t.Errorf("upstream saw Authorization %q, want Bearer music-live-key", gotAuth)
	}
	if !strings.Contains(gotBody, "pop") {
		t.Errorf("upstream did not receive the translated Suno request body, got %q", gotBody)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: upstream body read failure
// ---------------------------------------------------------------------------

type task_c_erroringReadCloser struct{}

func (task_c_erroringReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (task_c_erroringReadCloser) Close() error              { return nil }

func TestDoResponse_BodyReadFailureReturnsTaskError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_c_newGinContext(http.MethodPost, "", "")
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
// FetchTask: marshal, malformed-URL, and bad-proxy error branches
// ---------------------------------------------------------------------------

func TestFetchTask_BodyMarshalFailureReturnsError(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp2, err := a.FetchTask("https://example.com", "key", map[string]any{"bad": make(chan int)}, "")
	defer func() {
		if bcResp2 != nil {
			_ = bcResp2.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error when task body cannot be marshalled to JSON")
	}
}

func TestFetchTask_InvalidBaseURLFailsToBuildRequest(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp1, err := a.FetchTask("http://example.com/\x7f", "key", map[string]any{}, "")
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error for malformed base URL, must not panic")
	}
}

func TestFetchTask_InvalidProxyFailsClientConstruction(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp0, err := a.FetchTask("https://example.com", "key", map[string]any{}, "://not-a-valid-proxy-url")
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error when proxy URL cannot be parsed")
	}
	if !strings.Contains(err.Error(), "new proxy http client") {
		t.Errorf("err = %v, want wrapped 'new proxy http client'", err)
	}
}
