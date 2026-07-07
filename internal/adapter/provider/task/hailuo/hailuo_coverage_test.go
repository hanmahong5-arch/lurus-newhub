package hailuo

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newGinCtx(body string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, w
}

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 42, ChannelBaseUrl: "https://api.minimaxi.com", ApiKey: "sk-test"}}
	a.Init(info)
	if a.ChannelType != 42 || a.baseURL != "https://api.minimaxi.com" || a.apiKey != "sk-test" {
		t.Fatalf("Init did not set fields correctly: %+v", a)
	}
}

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://api.minimaxi.com"}
	info := &relaycommon.RelayInfo{}
	url, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.minimaxi.com/v1/video_generation"
	if url != want {
		t.Fatalf("got %q want %q", url, want)
	}
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "sk-abc"}
	c, _ := newGinCtx("", nil)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := a.BuildRequestHeader(c, req, &relaycommon.RelayInfo{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-abc" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestBuildRequestBody_MissingContext(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx("", nil)
	body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err == nil || err.Error() != "request not found in context" {
		t.Fatalf("expected 'request not found in context' error, got %v", err)
	}
	if body != nil {
		t.Fatalf("expected nil body, got %v", body)
	}
}

func TestBuildRequestBody_WrongType(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx("", nil)
	c.Set("task_request", "not-a-task-req")
	body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err == nil || err.Error() != "invalid request type in context" {
		t.Fatalf("expected 'invalid request type in context' error, got %v", err)
	}
	if body != nil {
		t.Fatalf("expected nil body, got %v", body)
	}
}

func TestBuildRequestBody_MetadataUnmarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx("", nil)
	// Metadata whose values are incompatible with VideoRequest fields (Duration is *int)
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:  "T2V-01",
		Prompt: "hello",
		Metadata: map[string]interface{}{
			"duration": "not-a-number",
		},
	})
	body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err == nil {
		t.Fatalf("expected error from bad metadata, got nil")
	}
	if body != nil {
		t.Fatalf("expected nil body on error, got %v", body)
	}
}

func TestBuildRequestBody_Success(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx("", nil)
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    "T2V-01-Director",
		Prompt:   "a cat playing piano",
		Duration: 10,
		Size:     "1080p",
	})
	body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var vr VideoRequest
	if err := json.Unmarshal(data, &vr); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if vr.Model != "T2V-01-Director" {
		t.Fatalf("Model = %q", vr.Model)
	}
	if vr.Prompt != "a cat playing piano" {
		t.Fatalf("Prompt = %q", vr.Prompt)
	}
	if vr.Duration == nil || *vr.Duration != 10 {
		t.Fatalf("Duration = %v", vr.Duration)
	}
	if vr.Resolution != Resolution1080P {
		t.Fatalf("Resolution = %q, want %q", vr.Resolution, Resolution1080P)
	}
}

func TestBuildRequestBody_DefaultDurationAndResolution(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx("", nil)
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:  "T2V-01-Director",
		Prompt: "no duration or size set",
	})
	body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(body)
	var vr VideoRequest
	_ = json.Unmarshal(data, &vr)
	if vr.Duration == nil || *vr.Duration != DefaultDuration {
		t.Fatalf("Duration = %v, want default %d", vr.Duration, DefaultDuration)
	}
	// T2V-01-Director default resolution is 768P per model config
	if vr.Resolution != Resolution768P {
		t.Fatalf("Resolution = %q, want %q", vr.Resolution, Resolution768P)
	}
}

func TestParseResolutionFromSize(t *testing.T) {
	a := &TaskAdaptor{}
	cfg := GetModelConfig("T2V-01")
	cases := []struct {
		size string
		want string
	}{
		{"1920x1080", Resolution1080P},
		{"1024x768", Resolution768P},
		{"1280x720", Resolution720P},
		{"640x512", Resolution512P},
		{"unknown-size", cfg.DefaultResolution},
		{"", cfg.DefaultResolution},
	}
	for _, tc := range cases {
		got := a.parseResolutionFromSize(tc.size, cfg)
		if got != tc.want {
			t.Errorf("parseResolutionFromSize(%q) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

func TestDoResponse_ReadBodyError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx("", nil)
	resp := &http.Response{
		Body: io.NopCloser(&errReader{}),
	}
	taskID, data, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatalf("expected taskErr, got nil")
	}
	if taskID != "" || data != nil {
		t.Fatalf("expected empty taskID/data on error, got %q %v", taskID, data)
	}
}

type errReader struct{}

func (e *errReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestDoResponse_UnmarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx("", nil)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString("not json")),
	}
	taskID, data, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatalf("expected taskErr for bad json")
	}
	if taskID != "" || data != nil {
		t.Fatalf("expected empty taskID/data on error, got %q %v", taskID, data)
	}
}

func TestDoResponse_StatusError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newGinCtx("", nil)
	body, _ := json.Marshal(VideoResponse{
		TaskID:   "task-123",
		BaseResp: BaseResp{StatusCode: StatusAuthFailed, StatusMsg: "auth failed"},
	})
	resp := &http.Response{Body: io.NopCloser(bytes.NewBuffer(body))}
	taskID, data, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatalf("expected taskErr for non-success status")
	}
	if taskErr.Code != "1004" {
		t.Fatalf("taskErr.Code = %q, want %q", taskErr.Code, "1004")
	}
	if taskID != "" || data != nil {
		t.Fatalf("expected empty taskID/data on error, got %q %v", taskID, data)
	}
}

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := newGinCtx("", nil)
	body, _ := json.Marshal(VideoResponse{
		TaskID:   "task-success-1",
		BaseResp: BaseResp{StatusCode: StatusSuccess, StatusMsg: "ok"},
	})
	resp := &http.Response{Body: io.NopCloser(bytes.NewBuffer(body))}
	info := &relaycommon.RelayInfo{OriginModelName: "T2V-01"}
	taskID, data, taskErr := a.DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("unexpected taskErr: %v", taskErr)
	}
	if taskID != "task-success-1" {
		t.Fatalf("taskID = %q", taskID)
	}
	if len(data) == 0 {
		t.Fatalf("expected non-empty raw response data")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("http status = %d, want 200", w.Code)
	}
	var ov map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
		t.Fatalf("unmarshal recorded response: %v", err)
	}
	if ov["id"] != "task-success-1" || ov["task_id"] != "task-success-1" {
		t.Fatalf("recorded response id/task_id mismatch: %+v", ov)
	}
	if ov["model"] != "T2V-01" {
		t.Fatalf("recorded response model = %v", ov["model"])
	}
}

func TestFetchTask_InvalidTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("https://api.minimaxi.com", "key", map[string]any{}, "")
	if err == nil || err.Error() != "invalid task_id" {
		t.Fatalf("expected 'invalid task_id' error, got %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %v", resp)
	}
}

func TestFetchTask_ProxyClientError(t *testing.T) {
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("https://api.minimaxi.com", "key", map[string]any{"task_id": "abc"}, "://bad-proxy")
	if err == nil {
		t.Fatalf("expected error building proxy http client")
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %v", resp)
	}
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Fatalf("GetModelList length = %d, want %d", len(got), len(ModelList))
	}
	if !contains(got, "MiniMax-Hailuo-2.3") {
		t.Fatalf("GetModelList missing expected model")
	}
	if name := a.GetChannelName(); name != ChannelName {
		t.Fatalf("GetChannelName = %q, want %q", name, ChannelName)
	}
}

func TestParseTaskResult(t *testing.T) {
	a := &TaskAdaptor{apiKey: "", baseURL: ""}

	cases := []struct {
		name       string
		resp       QueryTaskResponse
		wantStatus string
		wantProg   string
		wantReason string
	}{
		{
			name:       "preparing",
			resp:       QueryTaskResponse{Status: TaskStatusPreparing, BaseResp: BaseResp{StatusCode: StatusSuccess}},
			wantStatus: repo.TaskStatusInProgress, wantProg: "30%",
		},
		{
			name:       "queueing",
			resp:       QueryTaskResponse{Status: TaskStatusQueueing, BaseResp: BaseResp{StatusCode: StatusSuccess}},
			wantStatus: repo.TaskStatusInProgress, wantProg: "30%",
		},
		{
			name:       "processing",
			resp:       QueryTaskResponse{Status: TaskStatusProcessing, BaseResp: BaseResp{StatusCode: StatusSuccess}},
			wantStatus: repo.TaskStatusInProgress, wantProg: "50%",
		},
		{
			name:       "success",
			resp:       QueryTaskResponse{Status: TaskStatusSuccess, TaskID: "t1", FileID: "f1", BaseResp: BaseResp{StatusCode: StatusSuccess}},
			wantStatus: repo.TaskStatusSuccess, wantProg: "100%",
		},
		{
			name:       "failed_with_reason_from_status_error",
			resp:       QueryTaskResponse{Status: TaskStatusFailed, BaseResp: BaseResp{StatusCode: StatusSensitive, StatusMsg: "sensitive content"}},
			wantStatus: repo.TaskStatusFailure, wantProg: "100%", wantReason: "sensitive content",
		},
		{
			name:       "failed_no_reason_gets_default",
			resp:       QueryTaskResponse{Status: TaskStatusFailed, BaseResp: BaseResp{StatusCode: StatusSuccess}},
			wantStatus: repo.TaskStatusFailure, wantProg: "100%", wantReason: "task failed",
		},
		{
			name:       "unknown_status_defaults_in_progress",
			resp:       QueryTaskResponse{Status: "SomeWeirdStatus", BaseResp: BaseResp{StatusCode: StatusSuccess}},
			wantStatus: repo.TaskStatusInProgress, wantProg: "30%",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.resp)
			result, err := a.ParseTaskResult(body)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != tc.wantStatus {
				t.Fatalf("Status = %q, want %q", result.Status, tc.wantStatus)
			}
			if result.Progress != tc.wantProg {
				t.Fatalf("Progress = %q, want %q", result.Progress, tc.wantProg)
			}
			if tc.wantReason != "" && result.Reason != tc.wantReason {
				t.Fatalf("Reason = %q, want %q", result.Reason, tc.wantReason)
			}
			if tc.name == "success" && result.Url != "" {
				t.Fatalf("expected empty Url since apiKey/baseURL are empty, got %q", result.Url)
			}
		})
	}
}

func TestParseTaskResult_NonSuccessBaseResp(t *testing.T) {
	a := &TaskAdaptor{}
	resp := QueryTaskResponse{
		Status:   TaskStatusPreparing,
		BaseResp: BaseResp{StatusCode: StatusRateLimit, StatusMsg: "rate limited"},
	}
	body, _ := json.Marshal(resp)
	result, err := a.ParseTaskResult(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Code != StatusRateLimit {
		t.Fatalf("Code = %d, want %d", result.Code, StatusRateLimit)
	}
	if result.Reason != "rate limited" {
		t.Fatalf("Reason = %q", result.Reason)
	}
	// Status/Progress get overwritten from the base_resp branch first (Failure/100%),
	// then re-assigned by the status switch since Status == Preparing.
	if result.Status != repo.TaskStatusInProgress {
		t.Fatalf("Status = %q, want %q (switch branch overrides base_resp branch)", result.Status, repo.TaskStatusInProgress)
	}
	if result.Progress != "30%" {
		t.Fatalf("Progress = %q, want 30%%", result.Progress)
	}
}

func TestParseTaskResult_UnmarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	result, err := a.ParseTaskResult([]byte("not json"))
	if err == nil {
		t.Fatalf("expected unmarshal error")
	}
	if result != nil {
		t.Fatalf("expected nil result on error, got %v", result)
	}
}

func TestConvertToOpenAIVideo_Success(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{TaskID: "abc", Status: repo.TaskStatusSuccess}
	resp := QueryTaskResponse{TaskID: "abc", BaseResp: BaseResp{StatusCode: StatusSuccess}}
	task.SetData(resp)

	out, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov map[string]interface{}
	if err := json.Unmarshal(out, &ov); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if _, hasErr := ov["error"]; hasErr {
		t.Fatalf("expected no error field on success, got %v", ov["error"])
	}
}

func TestConvertToOpenAIVideo_ErrorStatus(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{TaskID: "abc", Status: repo.TaskStatusFailure}
	resp := QueryTaskResponse{TaskID: "abc", BaseResp: BaseResp{StatusCode: StatusNoBalance, StatusMsg: "insufficient balance"}}
	task.SetData(resp)

	out, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &ov); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if ov.Error == nil {
		t.Fatalf("expected error field to be set")
	}
	if ov.Error.Message != "insufficient balance" {
		t.Fatalf("Error.Message = %q", ov.Error.Message)
	}
	if ov.Error.Code != "1008" {
		t.Fatalf("Error.Code = %q, want %q", ov.Error.Code, "1008")
	}
}

func TestConvertToOpenAIVideo_UnmarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{TaskID: "abc"}
	task.Data = []byte("not json")

	out, err := a.ConvertToOpenAIVideo(task)
	if err == nil {
		t.Fatalf("expected unmarshal error")
	}
	if out != nil {
		t.Fatalf("expected nil output on error, got %v", out)
	}
}

func TestBuildVideoURL_EmptyKeyOrBaseURL(t *testing.T) {
	a := &TaskAdaptor{apiKey: "", baseURL: ""}
	if url := a.buildVideoURL("task1", "file1"); url != "" {
		t.Fatalf("expected empty URL when apiKey/baseURL empty, got %q", url)
	}

	a2 := &TaskAdaptor{apiKey: "sk-1", baseURL: ""}
	if url := a2.buildVideoURL("task1", "file1"); url != "" {
		t.Fatalf("expected empty URL when baseURL empty, got %q", url)
	}

	a3 := &TaskAdaptor{apiKey: "", baseURL: "https://api.minimaxi.com"}
	if url := a3.buildVideoURL("task1", "file1"); url != "" {
		t.Fatalf("expected empty URL when apiKey empty, got %q", url)
	}
}

func TestValidateRequestAndSetAction_EmptyPromptFails(t *testing.T) {
	oldMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = -1 // no limit, isolate this test from real body-size config
	defer func() { constant.MaxRequestBodyMB = oldMax }()

	a := &TaskAdaptor{}
	c, _ := newGinCtx("{}", map[string]string{"Content-Type": "application/json"})
	taskErr := a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatalf("expected validation error for empty prompt")
	}
}

func TestValidateRequestAndSetAction_ValidPrompt(t *testing.T) {
	oldMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = -1 // no limit, isolate this test from real body-size config
	defer func() { constant.MaxRequestBodyMB = oldMax }()

	a := &TaskAdaptor{}
	c, _ := newGinCtx(`{"prompt":"a running dog","model":"T2V-01"}`, map[string]string{"Content-Type": "application/json"})
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr != nil {
		t.Fatalf("unexpected validation error: %v", taskErr)
	}
	v, exists := c.Get("task_request")
	if !exists {
		t.Fatalf("expected task_request to be stored in context")
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		t.Fatalf("stored task_request has unexpected type %T", v)
	}
	if req.Prompt != "a running dog" {
		t.Fatalf("Prompt = %q", req.Prompt)
	}
}

func TestContainsHelpers(t *testing.T) {
	if !contains([]string{"a", "b"}, "b") {
		t.Fatalf("contains should find existing string")
	}
	if contains([]string{"a", "b"}, "c") {
		t.Fatalf("contains should not find missing string")
	}
	if !containsInt([]int{1, 2, 3}, 2) {
		t.Fatalf("containsInt should find existing int")
	}
	if containsInt([]int{1, 2, 3}, 4) {
		t.Fatalf("containsInt should not find missing int")
	}
}

func TestGetModelConfig_KnownAndUnknown(t *testing.T) {
	cfg := GetModelConfig("MiniMax-Hailuo-2.3")
	if cfg.DefaultResolution != Resolution768P {
		t.Fatalf("DefaultResolution = %q, want %q", cfg.DefaultResolution, Resolution768P)
	}
	if !cfg.HasPromptOptimizer || !cfg.HasFastPretreatment {
		t.Fatalf("expected HasPromptOptimizer/HasFastPretreatment true for MiniMax-Hailuo-2.3")
	}

	unknown := GetModelConfig("totally-unknown-model")
	if unknown.Name != "totally-unknown-model" {
		t.Fatalf("Name = %q", unknown.Name)
	}
	if unknown.DefaultResolution != DefaultResolution {
		t.Fatalf("DefaultResolution = %q, want default %q", unknown.DefaultResolution, DefaultResolution)
	}
	if unknown.HasFastPretreatment {
		t.Fatalf("expected HasFastPretreatment false for unknown model")
	}
	if len(unknown.SupportedDurations) != 1 || unknown.SupportedDurations[0] != 6 {
		t.Fatalf("SupportedDurations = %v, want [6]", unknown.SupportedDurations)
	}
}
