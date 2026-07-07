package vidu

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
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func init() {
	gin.SetMode(gin.TestMode)
	// production config normally sets this from env at boot; default zero value
	// would reject every non-empty request body in common.GetRequestBody.
	constant.MaxRequestBodyMB = -1
}

// newRelayInfo builds a RelayInfo with its embedded pointer sub-structs
// initialized, since ChannelType/ChannelBaseUrl/ApiKey live on *ChannelMeta
// and Action lives on *TaskRelayInfo.
func newRelayInfo(channelType int, baseURL, apiKey, action, originModel string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: originModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    channelType,
			ChannelBaseUrl: baseURL,
			ApiKey:         apiKey,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: action,
		},
	}
}

// ---------- simple accessors ----------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	models := a.GetModelList()
	want := []string{"viduq2", "viduq1", "vidu2.0", "vidu1.5"}
	if len(models) != len(want) {
		t.Fatalf("expected %d models, got %d", len(want), len(models))
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("model[%d] = %q, want %q", i, models[i], m)
		}
	}
	if got := a.GetChannelName(); got != "vidu" {
		t.Errorf("GetChannelName() = %q, want vidu", got)
	}
}

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	info := newRelayInfo(constant.ChannelTypeVidu, "https://api.vidu.example", "", "", "")
	a.Init(info)
	if a.ChannelType != constant.ChannelTypeVidu {
		t.Errorf("ChannelType = %d, want %d", a.ChannelType, constant.ChannelTypeVidu)
	}
	if a.baseURL != "https://api.vidu.example" {
		t.Errorf("baseURL = %q, want https://api.vidu.example", a.baseURL)
	}
}

// ---------- BuildRequestURL ----------

func TestBuildRequestURL(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   string
	}{
		{"generate", constant.TaskActionGenerate, "https://base/ent/v2/img2video"},
		{"firstTail", constant.TaskActionFirstTailGenerate, "https://base/ent/v2/start-end2video"},
		{"reference", constant.TaskActionReferenceGenerate, "https://base/ent/v2/reference2video"},
		{"textGenerate", constant.TaskActionTextGenerate, "https://base/ent/v2/text2video"},
		{"unknown-default", "some-unknown-action", "https://base/ent/v2/text2video"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &TaskAdaptor{baseURL: "https://base"}
			info := newRelayInfo(0, "", "", tc.action, "")
			got, err := a.BuildRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("BuildRequestURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------- BuildRequestHeader ----------

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{}
	req, err := http.NewRequest(http.MethodPost, "https://x", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	info := newRelayInfo(0, "", "secret-key", "", "")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if err := a.BuildRequestHeader(c, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Token secret-key" {
		t.Errorf("Authorization = %q, want %q", got, "Token secret-key")
	}
}

// ---------- ValidateRequestAndSetAction ----------

func newJSONContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func TestValidateRequestAndSetAction(t *testing.T) {
	t.Run("invalid_request_missing_prompt", func(t *testing.T) {
		a := &TaskAdaptor{}
		c := newJSONContext(t, `{"prompt":""}`)
		info := &relaycommon.RelayInfo{}
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected task error for empty prompt")
		}
		if taskErr.Code != "invalid_request" {
			t.Errorf("Code = %q, want invalid_request", taskErr.Code)
		}
	})

	t.Run("metadata_action_overrides", func(t *testing.T) {
		a := &TaskAdaptor{}
		c := newJSONContext(t, `{"prompt":"hello","metadata":{"action":"customAction"}}`)
		info := newRelayInfo(0, "", "", "", "")
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected error: %+v", taskErr)
		}
		if info.Action != "customAction" {
			t.Errorf("Action = %q, want customAction", info.Action)
		}
	})

	t.Run("no_metadata_no_image_text_generate", func(t *testing.T) {
		a := &TaskAdaptor{}
		c := newJSONContext(t, `{"prompt":"hello"}`)
		info := newRelayInfo(0, "", "", "", "")
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected error: %+v", taskErr)
		}
		if info.Action != constant.TaskActionTextGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionTextGenerate)
		}
	})

	t.Run("one_image_non_vidu_channel_generate", func(t *testing.T) {
		a := &TaskAdaptor{}
		c := newJSONContext(t, `{"prompt":"hello","images":["img1"]}`)
		info := newRelayInfo(999, "", "", "", "")
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected error: %+v", taskErr)
		}
		if info.Action != constant.TaskActionGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionGenerate)
		}
	})

	t.Run("one_image_vidu_channel_generate", func(t *testing.T) {
		a := &TaskAdaptor{}
		c := newJSONContext(t, `{"prompt":"hello","images":["img1"]}`)
		info := newRelayInfo(constant.ChannelTypeVidu, "", "", "", "")
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected error: %+v", taskErr)
		}
		if info.Action != constant.TaskActionGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionGenerate)
		}
	})

	t.Run("two_images_vidu_first_tail", func(t *testing.T) {
		a := &TaskAdaptor{}
		c := newJSONContext(t, `{"prompt":"hello","images":["img1","img2"]}`)
		info := newRelayInfo(constant.ChannelTypeVidu, "", "", "", "")
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected error: %+v", taskErr)
		}
		if info.Action != constant.TaskActionFirstTailGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionFirstTailGenerate)
		}
	})

	t.Run("three_images_vidu_reference", func(t *testing.T) {
		a := &TaskAdaptor{}
		c := newJSONContext(t, `{"prompt":"hello","images":["img1","img2","img3"]}`)
		info := newRelayInfo(constant.ChannelTypeVidu, "", "", "", "")
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected error: %+v", taskErr)
		}
		if info.Action != constant.TaskActionReferenceGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionReferenceGenerate)
		}
	})

	t.Run("two_images_non_vidu_channel_generate", func(t *testing.T) {
		a := &TaskAdaptor{}
		c := newJSONContext(t, `{"prompt":"hello","images":["img1","img2"]}`)
		info := newRelayInfo(999, "", "", "", "")
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected error: %+v", taskErr)
		}
		if info.Action != constant.TaskActionGenerate {
			t.Errorf("Action = %q, want %q (non-vidu channel skips first-tail split)", info.Action, constant.TaskActionGenerate)
		}
	})
}

// ---------- BuildRequestBody ----------

func TestBuildRequestBody_MissingContext(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{}
	_, err := a.BuildRequestBody(c, info)
	if err == nil {
		t.Fatal("expected error when task_request missing from context")
	}
	if err.Error() != "request not found in context" {
		t.Errorf("error = %q, want %q", err.Error(), "request not found in context")
	}
}

func TestBuildRequestBody_DefaultsAndMetadataMerge(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := relaycommon.TaskSubmitReq{
		Prompt: "a cat",
		Images: []string{"img1"},
	}
	c.Set("task_request", req)
	info := newRelayInfo(0, "", "", constant.TaskActionGenerate, "")

	r, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got requestPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "viduq1" {
		t.Errorf("Model = %q, want default viduq1", got.Model)
	}
	if got.Duration != 5 {
		t.Errorf("Duration = %d, want default 5", got.Duration)
	}
	if got.Resolution != "1080p" {
		t.Errorf("Resolution = %q, want default 1080p", got.Resolution)
	}
	if got.MovementAmplitude != "auto" {
		t.Errorf("MovementAmplitude = %q, want auto", got.MovementAmplitude)
	}
	if got.Bgm != false {
		t.Errorf("Bgm = %v, want false", got.Bgm)
	}
	if len(got.Images) != 1 || got.Images[0] != "img1" {
		t.Errorf("Images = %v, want [img1]", got.Images)
	}
}

func TestBuildRequestBody_ExplicitValuesOverrideDefaults(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := relaycommon.TaskSubmitReq{
		Prompt:   "a dog",
		Model:    "viduq2pro",
		Duration: 8,
		Size:     "720p",
	}
	c.Set("task_request", req)
	info := newRelayInfo(0, "", "", constant.TaskActionTextGenerate, "")

	r, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(r)
	var got requestPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "viduq2pro" {
		t.Errorf("Model = %q, want viduq2pro (not coerced, action != reference)", got.Model)
	}
	if got.Duration != 8 {
		t.Errorf("Duration = %d, want 8", got.Duration)
	}
	if got.Resolution != "720p" {
		t.Errorf("Resolution = %q, want 720p", got.Resolution)
	}
}

func TestBuildRequestBody_ReferenceGenerateCoercesViduq2Model(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := relaycommon.TaskSubmitReq{
		Prompt: "ref",
		Model:  "viduq2-turbo",
		Images: []string{"img1", "img2", "img3"},
	}
	c.Set("task_request", req)
	info := newRelayInfo(0, "", "", constant.TaskActionReferenceGenerate, "")

	r, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(r)
	var got requestPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "viduq2" {
		t.Errorf("Model = %q, want coerced to viduq2", got.Model)
	}
}

func TestBuildRequestBody_ReferenceGenerateNonViduq2ModelUnchanged(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := relaycommon.TaskSubmitReq{
		Prompt: "ref",
		Model:  "viduq1",
		Images: []string{"img1", "img2", "img3"},
	}
	c.Set("task_request", req)
	info := newRelayInfo(0, "", "", constant.TaskActionReferenceGenerate, "")

	r, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(r)
	var got requestPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "viduq1" {
		t.Errorf("Model = %q, want unchanged viduq1", got.Model)
	}
}

func TestBuildRequestBody_MetadataUnmarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// "duration" is an int field on requestPayload; feeding a string via metadata
	// makes the second json.Unmarshal (metadata onto requestPayload) fail.
	req := relaycommon.TaskSubmitReq{
		Prompt: "a cat",
		Metadata: map[string]interface{}{
			"duration": "not-a-number",
		},
	}
	c.Set("task_request", req)
	info := newRelayInfo(0, "", "", constant.TaskActionGenerate, "")

	_, err := a.BuildRequestBody(c, info)
	if err == nil {
		t.Fatal("expected error from metadata type mismatch")
	}
	if !strings.Contains(err.Error(), "unmarshal metadata failed") {
		t.Errorf("error = %q, want contains 'unmarshal metadata failed'", err.Error())
	}
}

// ---------- DoResponse ----------

func TestDoResponse_ReadBodyError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := &http.Response{Body: io.NopCloser(&errorReader{})}
	defer func() { _ = resp.Body.Close() }()
	info := &relaycommon.RelayInfo{}

	_, _, taskErr := a.DoResponse(c, resp, info)
	if taskErr == nil {
		t.Fatal("expected task error")
	}
	if taskErr.Code != "read_response_body_failed" {
		t.Errorf("Code = %q, want read_response_body_failed", taskErr.Code)
	}
}

func TestDoResponse_UnmarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("not json"))}
	defer func() { _ = resp.Body.Close() }()
	info := &relaycommon.RelayInfo{}

	_, _, taskErr := a.DoResponse(c, resp, info)
	if taskErr == nil {
		t.Fatal("expected task error")
	}
	if taskErr.Code != "unmarshal_response_failed" {
		t.Errorf("Code = %q, want unmarshal_response_failed", taskErr.Code)
	}
}

func TestDoResponse_TaskFailed(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `{"task_id":"t1","state":"failed"}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	defer func() { _ = resp.Body.Close() }()
	info := &relaycommon.RelayInfo{}

	_, _, taskErr := a.DoResponse(c, resp, info)
	if taskErr == nil {
		t.Fatal("expected task error")
	}
	if taskErr.Code != "task_failed" {
		t.Errorf("Code = %q, want task_failed", taskErr.Code)
	}
}

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"task_id":"task-123","state":"created"}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	defer func() { _ = resp.Body.Close() }()
	info := &relaycommon.RelayInfo{OriginModelName: "vidu-model"}

	taskID, taskData, taskErr := a.DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("unexpected error: %+v", taskErr)
	}
	if taskID != "task-123" {
		t.Errorf("taskID = %q, want task-123", taskID)
	}
	if string(taskData) != body {
		t.Errorf("taskData = %q, want %q", string(taskData), body)
	}
	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", w.Code)
	}
	var ov struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
		Model  string `json:"model"`
		Object string `json:"object"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
		t.Fatalf("unmarshal response body: %v", err)
	}
	if ov.ID != "task-123" || ov.TaskID != "task-123" {
		t.Errorf("response ID/TaskID = %q/%q, want task-123", ov.ID, ov.TaskID)
	}
	if ov.Model != "vidu-model" {
		t.Errorf("response Model = %q, want vidu-model", ov.Model)
	}
	if ov.Object != "video" {
		t.Errorf("response Object = %q, want video", ov.Object)
	}
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

// ---------- FetchTask ----------

func TestFetchTask_MissingTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("https://base", "key", map[string]any{}, "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected error for missing task_id")
	}
	if err.Error() != "invalid task_id" {
		t.Errorf("error = %q, want invalid task_id", err.Error())
	}
}

func TestFetchTask_WrongTypeTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("https://base", "key", map[string]any{"task_id": 12345}, "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected error for non-string task_id")
	}
	if err.Error() != "invalid task_id" {
		t.Errorf("error = %q, want invalid task_id", err.Error())
	}
}

func TestFetchTask_InvalidURLFromBaseUrl(t *testing.T) {
	a := &TaskAdaptor{}
	// control character makes the constructed URL invalid for http.NewRequest,
	// letting us exercise the http.NewRequest error branch without any network I/O.
	resp, err := a.FetchTask("https://base\x7f", "key", map[string]any{"task_id": "abc"}, "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected error building request from invalid base URL")
	}
}

// ---------- ParseTaskResult ----------

func TestParseTaskResult(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantErr    bool
		wantStatus repo.TaskStatus
		wantURL    string
		wantReason string
	}{
		{"created", `{"state":"created"}`, false, repo.TaskStatusSubmitted, "", ""},
		{"queueing", `{"state":"queueing"}`, false, repo.TaskStatusSubmitted, "", ""},
		{"processing", `{"state":"processing"}`, false, repo.TaskStatusInProgress, "", ""},
		{"success_with_creation", `{"state":"success","creations":[{"id":"c1","url":"https://x/video.mp4"}]}`, false, repo.TaskStatusSuccess, "https://x/video.mp4", ""},
		{"success_no_creations", `{"state":"success","creations":[]}`, false, repo.TaskStatusSuccess, "", ""},
		{"failed_with_err_code", `{"state":"failed","err_code":"E_BAD"}`, false, repo.TaskStatusFailure, "", "E_BAD"},
		{"failed_no_err_code", `{"state":"failed"}`, false, repo.TaskStatusFailure, "", ""},
		{"unknown_state", `{"state":"weird"}`, true, "", "", ""},
		{"bad_json", `not-json`, true, "", "", ""},
	}
	a := &TaskAdaptor{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := a.ParseTaskResult([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Status != string(tc.wantStatus) {
				t.Errorf("Status = %q, want %q", info.Status, tc.wantStatus)
			}
			if info.Url != tc.wantURL {
				t.Errorf("Url = %q, want %q", info.Url, tc.wantURL)
			}
			if info.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", info.Reason, tc.wantReason)
			}
		})
	}
}

func TestParseTaskResult_UnknownStateErrorMessage(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.ParseTaskResult([]byte(`{"state":"bogus"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "unknown task state: bogus" {
		t.Errorf("error = %q, want %q", err.Error(), "unknown task state: bogus")
	}
}

// ---------- ConvertToOpenAIVideo ----------

func TestConvertToOpenAIVideo_InvalidData(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{Data: []byte("not-json")}
	_, err := a.ConvertToOpenAIVideo(task)
	if err == nil {
		t.Fatal("expected error for invalid task data")
	}
	if !strings.Contains(err.Error(), "unmarshal vidu task data failed") {
		t.Errorf("error = %q, want contains 'unmarshal vidu task data failed'", err.Error())
	}
}

func TestConvertToOpenAIVideo_SuccessWithCreation(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID:    "task-1",
		Status:    repo.TaskStatusSuccess,
		Progress:  "100%",
		CreatedAt: 1000,
		UpdatedAt: 2000,
		Data:      []byte(`{"state":"success","creations":[{"id":"c1","url":"https://x/vid.mp4"}]}`),
	}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out struct {
		ID          string         `json:"id"`
		Status      string         `json:"status"`
		Progress    int            `json:"progress"`
		CreatedAt   int64          `json:"created_at"`
		CompletedAt int64          `json:"completed_at"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.ID != "task-1" {
		t.Errorf("ID = %q, want task-1", out.ID)
	}
	if out.Progress != 100 {
		t.Errorf("Progress = %d, want 100", out.Progress)
	}
	if out.CreatedAt != 1000 || out.CompletedAt != 2000 {
		t.Errorf("CreatedAt/CompletedAt = %d/%d, want 1000/2000", out.CreatedAt, out.CompletedAt)
	}
	if out.Metadata["url"] != "https://x/vid.mp4" {
		t.Errorf("Metadata[url] = %v, want https://x/vid.mp4", out.Metadata["url"])
	}
}

func TestConvertToOpenAIVideo_FailedSetsError(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID: "task-2",
		Status: repo.TaskStatusFailure,
		Data:   []byte(`{"state":"failed","err_code":"E_TIMEOUT"}`),
	}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.Error == nil {
		t.Fatal("expected error field to be set")
	}
	if out.Error.Message != "E_TIMEOUT" || out.Error.Code != "E_TIMEOUT" {
		t.Errorf("Error = %+v, want Message/Code=E_TIMEOUT", out.Error)
	}
}

func TestConvertToOpenAIVideo_NoCreationsNoError(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID: "task-3",
		Status: repo.TaskStatusInProgress,
		Data:   []byte(`{"state":"processing"}`),
	}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out struct {
		Metadata map[string]any `json:"metadata"`
		Error    any            `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.Metadata != nil {
		t.Errorf("Metadata = %v, want nil (no url set)", out.Metadata)
	}
	if out.Error != nil {
		t.Errorf("Error = %v, want nil", out.Error)
	}
}

// ---------- helper funcs ----------

func TestDefaultStringAndDefaultInt(t *testing.T) {
	if got := defaultString("", "fallback"); got != "fallback" {
		t.Errorf("defaultString empty = %q, want fallback", got)
	}
	if got := defaultString("value", "fallback"); got != "value" {
		t.Errorf("defaultString non-empty = %q, want value", got)
	}
	if got := defaultInt(0, 7); got != 7 {
		t.Errorf("defaultInt zero = %d, want 7", got)
	}
	if got := defaultInt(3, 7); got != 3 {
		t.Errorf("defaultInt non-zero = %d, want 3", got)
	}
}

// sanity: ensure bytes import used if request payload changes shape in future.
var _ = bytes.MinRead
