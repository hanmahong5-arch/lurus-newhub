package sora

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
	req := httptest.NewRequest(method, "/v1/videos", strings.NewReader(body))
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
// Init
// ---------------------------------------------------------------------------

func TestInit_SetsChannelStateFromRelayInfo(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate},
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelType: 7, ChannelBaseUrl: "https://sora.example", ApiKey: "sora-key-1"},
	}
	a.Init(info)
	if a.ChannelType != 7 {
		t.Errorf("ChannelType = %d, want 7", a.ChannelType)
	}
	if a.baseURL != "https://sora.example" {
		t.Errorf("baseURL = %q, want https://sora.example", a.baseURL)
	}
	if a.apiKey != "sora-key-1" {
		t.Errorf("apiKey = %q, want sora-key-1", a.apiKey)
	}
	// Init must actually feed BuildRequestURL/BuildRequestHeader downstream.
	url, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://sora.example/v1/videos" {
		t.Errorf("BuildRequestURL after Init = %q, want https://sora.example/v1/videos", url)
	}
}

// ---------------------------------------------------------------------------
// validateRemixRequest: malformed body error branch
// ---------------------------------------------------------------------------

func TestValidateRequestAndSetAction_RemixMalformedBodyErrors(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_c_newGinContext(http.MethodPost, `{"prompt":`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionRemix}}
	if err := a.ValidateRequestAndSetAction(c, info); err == nil {
		t.Fatal("expected error for malformed remix request body, must not panic")
	}
}

// ---------------------------------------------------------------------------
// BuildRequestBody: underlying body-read failure
// ---------------------------------------------------------------------------

type task_c_erroringReadCloser struct{}

func (task_c_erroringReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (task_c_erroringReadCloser) Close() error              { return nil }

func TestBuildRequestBody_UnderlyingBodyReadFailureErrors(t *testing.T) {
	a := &TaskAdaptor{}
	gin.SetMode(gin.TestMode)
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	req.Body = task_c_erroringReadCloser{}
	c.Request = req

	_, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err == nil {
		t.Fatal("expected error when the underlying request body cannot be read, must not panic")
	}
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
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"vid-live","status":"queued"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{apiKey: "sora-live-key", baseURL: srv.URL}
	c, _ := task_c_newGinContext(http.MethodPost, `{"prompt":"a cat"}`, "application/json")
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate},
		ChannelMeta:   &relaycommon.ChannelMeta{},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"prompt":"a cat"}`))
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
	if gotPath != "/v1/videos" {
		t.Errorf("upstream saw path %q, want /v1/videos", gotPath)
	}
	if gotAuth != "Bearer sora-live-key" {
		t.Errorf("upstream saw Authorization %q, want Bearer sora-live-key", gotAuth)
	}
	if !strings.Contains(gotBody, "a cat") {
		t.Errorf("upstream did not receive the request body, got %q", gotBody)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: upstream body read failure
// ---------------------------------------------------------------------------

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
// FetchTask: malformed-URL and bad-proxy error branches
// ---------------------------------------------------------------------------

func TestFetchTask_InvalidBaseURLFailsToBuildRequest(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("http://example.com/\x7f", "key", map[string]any{"task_id": "t1"}, "")
	if err == nil {
		t.Fatal("expected error for malformed base URL, must not panic")
	}
}

func TestFetchTask_InvalidProxyFailsClientConstruction(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("https://example.com", "key", map[string]any{"task_id": "t1"}, "://not-a-valid-proxy-url")
	if err == nil {
		t.Fatal("expected error when proxy URL cannot be parsed")
	}
	if !strings.Contains(err.Error(), "new proxy http client failed") {
		t.Errorf("err = %v, want wrapped 'new proxy http client failed'", err)
	}
}
