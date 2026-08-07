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
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

func task_batch_b_newGinContext(method, path, action, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
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

func task_batch_b_allowLoopbackHTTP() func() {
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	return func() { system_setting.GetFetchSetting().AllowPrivateIp = prev }
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestValidateRequestAndSetAction_MusicDefaultsModelVersion(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/suno/submit/music", "music", `{"prompt":"a song"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Action != constant.SunoActionMusic {
		t.Errorf("Action = %q, want MUSIC", info.Action)
	}
	raw, ok := c.Get("task_request")
	if !ok {
		t.Fatal("task_request not stored in context")
	}
	req := raw.(*dto.SunoSubmitReq)
	if req.Mv != "chirp-v3-0" {
		t.Errorf("Mv default = %q, want chirp-v3-0", req.Mv)
	}
}

func TestValidateRequestAndSetAction_LyricsRequiresPrompt(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/suno/submit/lyrics", "lyrics", `{"prompt":""}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if err := a.ValidateRequestAndSetAction(c, info); err == nil {
		t.Fatal("expected error when lyrics action has empty prompt")
	}
}

func TestValidateRequestAndSetAction_LyricsWithPromptSucceeds(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/suno/submit/lyrics", "lyrics", `{"prompt":"write lyrics about the sea"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Action != constant.SunoActionLyrics {
		t.Errorf("Action = %q, want LYRICS", info.Action)
	}
}

func TestValidateRequestAndSetAction_InvalidAction(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/suno/submit/bogus", "bogus", `{"prompt":"x"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if err := a.ValidateRequestAndSetAction(c, info); err == nil {
		t.Fatal("expected error for unrecognized action")
	}
}

func TestValidateRequestAndSetAction_ContinueClipRequiresTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/suno/submit/music", "music", `{"prompt":"x","continue_clip_id":"clip-1"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if err := a.ValidateRequestAndSetAction(c, info); err == nil {
		t.Fatal("expected error: continue_clip_id set without task_id")
	}
}

func TestValidateRequestAndSetAction_ContinueClipWithTaskIDSetsOriginTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/suno/submit/music", "music", `{"prompt":"x","continue_clip_id":"clip-1","task_id":"orig-99"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.OriginTaskID != "orig-99" {
		t.Errorf("OriginTaskID = %q, want orig-99 propagated from task_id", info.OriginTaskID)
	}
}

func TestValidateRequestAndSetAction_InvalidJSON(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/suno/submit/music", "music", `{bad json`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if err := a.ValidateRequestAndSetAction(c, info); err == nil {
		t.Fatal("expected error for malformed request JSON")
	}
}

// ---------------------------------------------------------------------------
// BuildRequestURL / BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: "MUSIC"},
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelBaseUrl: "https://suno.example"},
	}
	got, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://suno.example/suno/submit/MUSIC"
	if got != want {
		t.Errorf("BuildRequestURL = %q, want %q", got, want)
	}
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/x", "", "", "application/json")
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "suno-key"}}
	if err := a.BuildRequestHeader(c, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("Authorization") != "Bearer suno-key" {
		t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type not copied from incoming request: %q", req.Header.Get("Content-Type"))
	}
}

// ---------------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------------

func TestBuildRequestBody_UsesContextValueWhenPresent(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/x", "", "", "")
	c.Set("task_request", &dto.SunoSubmitReq{GptDescriptionPrompt: "from-context"})
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	if !strings.Contains(string(data), "from-context") {
		t.Errorf("body should marshal the context task_request, got %s", data)
	}
}

func TestBuildRequestBody_FallsBackToRawBodyWhenContextMissing(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/x", "", `{"prompt":"raw body prompt"}`, "application/json")
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	if !strings.Contains(string(data), "raw body prompt") {
		t.Errorf("body should fall back to unmarshalling the raw request, got %s", data)
	}
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func TestDoResponse_SuccessCopiesHeadersAndBody(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := task_batch_b_newGinContext(http.MethodPost, "/x", "", "", "")
	body := `{"code":"success","message":"ok","data":"task-77"}`
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"X-Upstream": []string{"upstream-value"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	taskID, data, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if taskID != "task-77" {
		t.Errorf("taskID = %q, want task-77", taskID)
	}
	if data != nil {
		t.Errorf("suno DoResponse does not persist raw taskData, want nil, got %v", data)
	}
	if w.Code != http.StatusCreated {
		t.Errorf("status code = %d, want upstream's 201 propagated", w.Code)
	}
	if w.Header().Get("X-Upstream") != "upstream-value" {
		t.Errorf("upstream header not copied through: %q", w.Header().Get("X-Upstream"))
	}
	if !strings.Contains(w.Body.String(), "task-77") {
		t.Errorf("response body not copied through: %s", w.Body.String())
	}
}

func TestDoResponse_UpstreamFailureCode(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/x", "", "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"code":"error","message":"rate limited"}`))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error for non-success upstream code")
	}
	if !strings.Contains(taskErr.Message, "rate limited") {
		t.Errorf("failure reason not propagated: %q", taskErr.Message)
	}
}

func TestDoResponse_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "/x", "", "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("{bad"))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error, must not panic")
	}
}

// ---------------------------------------------------------------------------
// FetchTask
// ---------------------------------------------------------------------------

func TestFetchTask_PostsBodyAndAuth(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()
	var gotPath, gotAuth, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"code":"success","data":{}}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	resp, err := a.FetchTask(srv.URL, "suno-key", map[string]any{"ids": []string{"t1"}}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/suno/fetch" {
		t.Errorf("path = %q, want /suno/fetch", gotPath)
	}
	if gotAuth != "Bearer suno-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBody, "t1") {
		t.Errorf("body should carry through task ids, got %q", gotBody)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName / ParseTaskResult / Init
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "suno" {
		t.Errorf("GetChannelName() = %q, want suno", a.GetChannelName())
	}
	models := a.GetModelList()
	if len(models) != 2 {
		t.Fatalf("model list = %v, want 2 entries", models)
	}
}

func TestParseTaskResult_NotImplemented(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.ParseTaskResult(nil); err == nil {
		t.Fatal("suno ParseTaskResult is a documented not-implemented stub; expected error")
	}
}

func TestInit_SetsChannelType(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 42}})
	if a.ChannelType != 42 {
		t.Errorf("ChannelType = %d, want 42", a.ChannelType)
	}
}
