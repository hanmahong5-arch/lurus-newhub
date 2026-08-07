package doubao

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

func task_batch_b_newGinContext(method, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, "/v1/video/generations", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
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
// BuildRequestURL / BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://doubao.example"}
	got, err := a.BuildRequestURL(&relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://doubao.example/api/v3/contents/generations/tasks"
	if got != want {
		t.Errorf("BuildRequestURL = %q, want %q", got, want)
	}
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "doubao-key"}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := a.BuildRequestHeader(nil, req, &relaycommon.RelayInfo{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("Authorization") != "Bearer doubao-key" {
		t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
	}
}

// ---------------------------------------------------------------------------
// convertToRequestPayload
// ---------------------------------------------------------------------------

func TestConvertToRequestPayload_TextOnly(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{Prompt: "a sunrise", Model: "doubao-seedance-1-0-lite-t2v"}
	p, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Content) != 1 || p.Content[0].Type != "text" || p.Content[0].Text != "a sunrise" {
		t.Errorf("Content = %+v, want single text item", p.Content)
	}
	if p.Model != "doubao-seedance-1-0-lite-t2v" {
		t.Errorf("Model = %q", p.Model)
	}
}

func TestConvertToRequestPayload_TextAndImages(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt: "animate this",
		Images: []string{"https://img/1.png", "https://img/2.png"},
	}
	p, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Content) != 3 {
		t.Fatalf("Content len = %d, want 3 (1 text + 2 images)", len(p.Content))
	}
	if p.Content[1].Type != "image_url" || p.Content[1].ImageURL.URL != "https://img/1.png" {
		t.Errorf("first image item wrong: %+v", p.Content[1])
	}
	if p.Content[2].ImageURL.URL != "https://img/2.png" {
		t.Errorf("second image item wrong: %+v", p.Content[2])
	}
}

func TestConvertToRequestPayload_EmptyPromptNoTextItem(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{Images: []string{"https://img/1.png"}}
	p, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Content) != 1 || p.Content[0].Type != "image_url" {
		t.Errorf("Content = %+v, want single image item with no leading empty text", p.Content)
	}
}

// ---------------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------------

func TestBuildRequestBody_MissingContext(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
		t.Fatal("expected error when task_request missing from context")
	}
}

func TestBuildRequestBody_MarshalsPayload(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "hi", Model: "doubao-seedance-1-0-pro-250528"})
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	var payload requestPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if payload.Model != "doubao-seedance-1-0-pro-250528" {
		t.Errorf("Model = %q", payload.Model)
	}
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"id":"doubao-task-1"}`))}
	taskID, data, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if taskID != "doubao-task-1" {
		t.Errorf("taskID = %q, want doubao-task-1", taskID)
	}
	if len(data) == 0 {
		t.Error("expected raw response body returned")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("client body not valid JSON: %v", err)
	}
	if out["task_id"] != "doubao-task-1" {
		t.Errorf("client-facing task_id wrong: %+v", out)
	}
}

func TestDoResponse_MissingTaskIDErrors(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"id":""}`))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error when upstream returns empty task id")
	}
}

func TestDoResponse_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("{bad"))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error, must not panic")
	}
}

// ---------------------------------------------------------------------------
// FetchTask
// ---------------------------------------------------------------------------

func TestFetchTask_MissingTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.FetchTask("https://x", "key", map[string]any{}, ""); err == nil {
		t.Fatal("expected error when task_id missing")
	}
}

func TestFetchTask_URLAndAuth(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"t1","status":"processing"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	resp, err := a.FetchTask(srv.URL, "doubao-key", map[string]any{"task_id": "t1"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if gotPath != "/api/v3/contents/generations/tasks/t1" {
		t.Errorf("path = %q, want /api/v3/contents/generations/tasks/t1", gotPath)
	}
	if gotAuth != "Bearer doubao-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "doubao-video" {
		t.Errorf("GetChannelName() = %q, want doubao-video", a.GetChannelName())
	}
	if len(a.GetModelList()) == 0 {
		t.Fatal("model list must not be empty")
	}
}

// ---------------------------------------------------------------------------
// ParseTaskResult
// ---------------------------------------------------------------------------

func TestParseTaskResult_StatusMapping(t *testing.T) {
	a := &TaskAdaptor{}
	cases := []struct {
		status string
		want   repo.TaskStatus
	}{
		{"pending", repo.TaskStatusQueued},
		{"queued", repo.TaskStatusQueued},
		{"processing", repo.TaskStatusInProgress},
		{"succeeded", repo.TaskStatusSuccess},
		{"failed", repo.TaskStatusFailure},
		{"some_unrecognized_value", repo.TaskStatusInProgress},
	}
	for _, tt := range cases {
		t.Run(tt.status, func(t *testing.T) {
			ti, err := a.ParseTaskResult([]byte(`{"status":"` + tt.status + `"}`))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ti.Status != string(tt.want) {
				t.Errorf("Status = %q, want %q", ti.Status, tt.want)
			}
		})
	}
}

func TestParseTaskResult_SuccessExtractsURLAndUsage(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"status":"succeeded","content":{"video_url":"https://cdn/v.mp4"},"usage":{"completion_tokens":10,"total_tokens":20}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Url != "https://cdn/v.mp4" {
		t.Errorf("Url = %q, want video url", ti.Url)
	}
	if ti.CompletionTokens != 10 || ti.TotalTokens != 20 {
		t.Errorf("usage not propagated for rate-based billing: completion=%d total=%d", ti.CompletionTokens, ti.TotalTokens)
	}
}

func TestParseTaskResult_FailedSetsReason(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"status":"failed"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Reason != "task failed" {
		t.Errorf("Reason = %q, want task failed", ti.Reason)
	}
}

func TestParseTaskResult_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.ParseTaskResult([]byte("not json")); err == nil {
		t.Fatal("expected error, must not panic")
	}
}
