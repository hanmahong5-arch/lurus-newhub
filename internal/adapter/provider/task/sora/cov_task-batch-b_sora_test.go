package sora

import (
	"bytes"
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
	req := httptest.NewRequest(method, "/v1/videos", strings.NewReader(body))
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
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestValidateRequestAndSetAction_RemixRequiresPrompt(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":""}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionRemix}}
	if err := a.ValidateRequestAndSetAction(c, info); err == nil {
		t.Fatal("expected error for remix request with empty prompt")
	}
}

func TestValidateRequestAndSetAction_RemixWithPromptSucceeds(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"make it blue"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionRemix}}
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRequestAndSetAction_NonRemixDelegatesToMultipartValidation(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":""}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	err := a.ValidateRequestAndSetAction(c, info)
	if err == nil {
		t.Fatal("expected error: default (non-remix) path requires model field per ValidateMultipartDirect")
	}
}

// ---------------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------------

func TestBuildRequestURL_Remix(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://sora.example"}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionRemix, OriginTaskID: "orig-vid-1"}}
	got, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://sora.example/v1/videos/orig-vid-1/remix"
	if got != want {
		t.Errorf("BuildRequestURL = %q, want %q", got, want)
	}
}

func TestBuildRequestURL_Generate(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://sora.example"}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate}}
	got, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://sora.example/v1/videos" {
		t.Errorf("BuildRequestURL = %q, want /v1/videos", got)
	}
}

// ---------------------------------------------------------------------------
// BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sora-key"}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "multipart/form-data; boundary=x")
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := a.BuildRequestHeader(c, req, &relaycommon.RelayInfo{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("Authorization") != "Bearer sora-key" {
		t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
	}
	if req.Header.Get("Content-Type") != "multipart/form-data; boundary=x" {
		t.Errorf("Content-Type should mirror the incoming request, got %q", req.Header.Get("Content-Type"))
	}
}

// ---------------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------------

func TestBuildRequestBody_PassesThroughCachedRawBody(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"raw passthrough"}`, "application/json")
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	if !strings.Contains(string(data), "raw passthrough") {
		t.Errorf("expected raw request body forwarded verbatim, got %s", data)
	}
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"id":"sora-1","status":"queued"}`))}
	taskID, data, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if taskID != "sora-1" {
		t.Errorf("taskID = %q, want sora-1", taskID)
	}
	if len(data) == 0 {
		t.Error("expected raw response body returned")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDoResponse_LegacyTaskIDFieldCompat(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"task_id":"legacy-99"}`))}
	taskID, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if taskID != "legacy-99" {
		t.Errorf("taskID = %q, want legacy task_id field promoted to id, legacy-99", taskID)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("client body not valid JSON: %v", err)
	}
	if out["id"] != "legacy-99" {
		t.Errorf("client-facing id = %v, want legacy-99 promoted from task_id", out["id"])
	}
}

func TestDoResponse_MissingBothIDFieldsErrors(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{}`))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error when both id and task_id are empty")
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
	bcResp0, err := a.FetchTask("https://x", "key", map[string]any{}, "")
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err == nil {
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
	resp, err := a.FetchTask(srv.URL, "sora-key", map[string]any{"task_id": "t1"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if gotPath != "/v1/videos/t1" {
		t.Errorf("path = %q, want /v1/videos/t1", gotPath)
	}
	if gotAuth != "Bearer sora-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "sora" {
		t.Errorf("GetChannelName() = %q, want sora", a.GetChannelName())
	}
	models := a.GetModelList()
	found := false
	for _, m := range models {
		if m == "sora-2" {
			found = true
		}
	}
	if !found {
		t.Error("expected sora-2 in model list")
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
		{"queued", repo.TaskStatusQueued},
		{"pending", repo.TaskStatusQueued},
		{"processing", repo.TaskStatusInProgress},
		{"in_progress", repo.TaskStatusInProgress},
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

func TestParseTaskResult_UnknownStatusLeavesEmpty(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"status":"totally_unknown"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != "" {
		t.Errorf("Status = %q, want empty for unrecognized status (sora silently ignores unknown enums)", ti.Status)
	}
}

func TestParseTaskResult_CompletedBuildsContentURL(t *testing.T) {
	a := &TaskAdaptor{}
	system_setting.ServerAddress = "https://hub.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = "" })
	ti, err := a.ParseTaskResult([]byte(`{"id":"vid-5","status":"completed"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != string(repo.TaskStatusSuccess) {
		t.Errorf("Status = %q, want SUCCESS", ti.Status)
	}
	want := "https://hub.example.com/v1/videos/vid-5/content"
	if ti.Url != want {
		t.Errorf("Url = %q, want %q", ti.Url, want)
	}
}

func TestParseTaskResult_FailedWithErrorObject(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"status":"failed","error":{"message":"content policy violation","code":"moderation_blocked"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != string(repo.TaskStatusFailure) {
		t.Errorf("Status = %q, want FAILURE", ti.Status)
	}
	if ti.Reason != "content policy violation" {
		t.Errorf("Reason = %q, want upstream error message", ti.Reason)
	}
}

func TestParseTaskResult_CancelledWithoutErrorObjectUsesGenericReason(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"status":"cancelled"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Reason != "task failed" {
		t.Errorf("Reason = %q, want generic fallback 'task failed' when no error object present", ti.Reason)
	}
}

func TestParseTaskResult_ProgressBoundaries(t *testing.T) {
	a := &TaskAdaptor{}
	cases := []struct {
		name     string
		progress int
		want     string
	}{
		{"mid progress renders percent", 42, "42%"},
		{"zero progress stays empty", 0, ""},
		{"full progress stays empty (redundant with completed status)", 100, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"status":"processing","progress":` + jsonInt(tt.progress) + `}`
			ti, err := a.ParseTaskResult([]byte(body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ti.Progress != tt.want {
				t.Errorf("Progress = %q, want %q", ti.Progress, tt.want)
			}
		})
	}
}

func jsonInt(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestParseTaskResult_MalformedJSONDoesNotPanic(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.ParseTaskResult([]byte("not json")); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseTaskResult_EmptyBodyErrorsWithoutPanic(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.ParseTaskResult([]byte("")); err == nil {
		t.Fatal("expected error for empty upstream body, must not panic")
	}
}

// ---------------------------------------------------------------------------
// ConvertToOpenAIVideo
// ---------------------------------------------------------------------------

func TestConvertToOpenAIVideo_ReturnsStoredDataVerbatim(t *testing.T) {
	a := &TaskAdaptor{}
	raw := []byte(`{"id":"vid-1","status":"completed"}`)
	task := &repo.Task{TaskID: "vid-1", Data: raw}
	got, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Errorf("ConvertToOpenAIVideo = %s, want the stored task.Data returned verbatim (no reformatting)", got)
	}
}
