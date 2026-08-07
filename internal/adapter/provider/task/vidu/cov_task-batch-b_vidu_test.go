package vidu

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
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
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

func task_batch_b_newRelayInfo(channelType int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelType: channelType},
	}
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestValidateRequestAndSetAction_EmptyPromptRejected(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":""}`, "application/json")
	info := task_batch_b_newRelayInfo(constant.ChannelTypeVidu)
	if err := a.ValidateRequestAndSetAction(c, info); err == nil {
		t.Fatal("expected validation error for empty prompt")
	}
}

func TestValidateRequestAndSetAction_NoImageDefaultsToTextGenerate(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"a cat"}`, "application/json")
	info := task_batch_b_newRelayInfo(constant.ChannelTypeVidu)
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Action != constant.TaskActionTextGenerate {
		t.Errorf("Action = %q, want textGenerate when no image supplied", info.Action)
	}
}

func TestValidateRequestAndSetAction_MetadataActionOverride(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"a cat","metadata":"{\"action\":\"customAction\"}"}`, "application/json")
	info := task_batch_b_newRelayInfo(constant.ChannelTypeVidu)
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Action != "customAction" {
		t.Errorf("Action = %q, want explicit metadata action to take priority", info.Action)
	}
}

func TestValidateRequestAndSetAction_TwoImagesFirstTail(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"a cat","images":["a.png","b.png"]}`, "application/json")
	info := task_batch_b_newRelayInfo(constant.ChannelTypeVidu)
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Action != constant.TaskActionFirstTailGenerate {
		t.Errorf("Action = %q, want firstTailGenerate for exactly 2 images on Vidu channel", info.Action)
	}
}

func TestValidateRequestAndSetAction_ThreeImagesReference(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"a cat","images":["a.png","b.png","c.png"]}`, "application/json")
	info := task_batch_b_newRelayInfo(constant.ChannelTypeVidu)
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Action != constant.TaskActionReferenceGenerate {
		t.Errorf("Action = %q, want referenceGenerate for >2 images on Vidu channel", info.Action)
	}
}

func TestValidateRequestAndSetAction_OneImageNonViduChannel(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"a cat","images":["a.png"]}`, "application/json")
	info := task_batch_b_newRelayInfo(9999) // arbitrary non-Vidu channel type
	if err := a.ValidateRequestAndSetAction(c, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Action != constant.TaskActionGenerate {
		t.Errorf("Action = %q, want generate for single-image on non-Vidu channel", info.Action)
	}
}

// ---------------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------------

func TestBuildRequestBody_MissingContext(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	if _, err := a.BuildRequestBody(c, task_batch_b_newRelayInfo(constant.ChannelTypeVidu)); err == nil {
		t.Fatal("expected error when task_request missing from context")
	}
}

func TestBuildRequestBody_ReferenceGenerateForcesViduq2(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "x", Model: "viduq2-pro"})
	info := task_batch_b_newRelayInfo(constant.ChannelTypeVidu)
	info.Action = constant.TaskActionReferenceGenerate
	reader, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	var payload requestPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if payload.Model != "viduq2" {
		t.Errorf("Model = %q, reference-to-video must be forced to bare viduq2 (no pro/turbo suffix)", payload.Model)
	}
}

func TestBuildRequestBody_NonReferenceKeepsModel(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "x", Model: "viduq2-pro"})
	info := task_batch_b_newRelayInfo(constant.ChannelTypeVidu)
	info.Action = constant.TaskActionGenerate
	reader, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	var payload requestPayload
	_ = json.Unmarshal(data, &payload)
	if payload.Model != "viduq2-pro" {
		t.Errorf("Model = %q, non-reference actions should keep the requested model suffix", payload.Model)
	}
}

// ---------------------------------------------------------------------------
// convertToRequestPayload
// ---------------------------------------------------------------------------

func TestConvertToRequestPayload_Defaults(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{Prompt: "hi"}
	p, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Model != "viduq1" {
		t.Errorf("default Model = %q, want viduq1", p.Model)
	}
	if p.Duration != 5 {
		t.Errorf("default Duration = %d, want 5", p.Duration)
	}
	if p.Resolution != "1080p" {
		t.Errorf("default Resolution = %q, want 1080p", p.Resolution)
	}
	if p.MovementAmplitude != "auto" {
		t.Errorf("default MovementAmplitude = %q, want auto", p.MovementAmplitude)
	}
}

func TestConvertToRequestPayload_MetadataOverride(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt:   "hi",
		Metadata: map[string]any{"bgm": true, "seed": 42},
	}
	p, err := a.convertToRequestPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.Bgm {
		t.Error("metadata bgm=true must override the default false")
	}
	if p.Seed != 42 {
		t.Errorf("Seed = %d, want 42 from metadata", p.Seed)
	}
}

// ---------------------------------------------------------------------------
// defaultString / defaultInt
// ---------------------------------------------------------------------------

func TestDefaultString(t *testing.T) {
	if got := defaultString("", "x"); got != "x" {
		t.Errorf("empty should fall back, got %q", got)
	}
	if got := defaultString("y", "x"); got != "y" {
		t.Errorf("non-empty should be preserved, got %q", got)
	}
}

func TestDefaultInt(t *testing.T) {
	if got := defaultInt(0, 7); got != 7 {
		t.Errorf("zero should fall back, got %d", got)
	}
	if got := defaultInt(3, 7); got != 3 {
		t.Errorf("non-zero should be preserved, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------------

func TestBuildRequestURL_ActionRouting(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://vidu.example"}
	cases := []struct {
		action string
		want   string
	}{
		{constant.TaskActionGenerate, "https://vidu.example/ent/v2/img2video"},
		{constant.TaskActionFirstTailGenerate, "https://vidu.example/ent/v2/start-end2video"},
		{constant.TaskActionReferenceGenerate, "https://vidu.example/ent/v2/reference2video"},
		{constant.TaskActionTextGenerate, "https://vidu.example/ent/v2/text2video"},
		{"", "https://vidu.example/ent/v2/text2video"},
	}
	for _, tt := range cases {
		t.Run(tt.action, func(t *testing.T) {
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: tt.action}}
			got, err := a.BuildRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("BuildRequestURL(%q) = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "secret-token"}}
	if err := a.BuildRequestHeader(nil, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("Authorization") != "Token secret-token" {
		t.Errorf("Authorization = %q, want Token secret-token", req.Header.Get("Authorization"))
	}
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := task_batch_b_newGinContext(http.MethodPost, "", "")
	body := `{"task_id":"vid-1","state":"created"}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{OriginModelName: "viduq1"}
	taskID, data, taskErr := a.DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if taskID != "vid-1" {
		t.Errorf("taskID = %q, want vid-1", taskID)
	}
	if len(data) == 0 {
		t.Error("expected raw body returned")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if ov.Model != "viduq1" {
		t.Errorf("Model = %q, want viduq1", ov.Model)
	}
}

func TestDoResponse_FailedState(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"state":"failed"}`))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error for failed submission state")
	}
}

func TestDoResponse_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("{bad"))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error for malformed JSON, must not panic")
	}
}

func TestDoResponse_EmptyBody(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(""))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error for empty body")
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
		_, _ = w.Write([]byte(`{"state":"success","creations":[{"url":"https://cdn/x.mp4"}]}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	resp, err := a.FetchTask(srv.URL, "vidu-key", map[string]any{"task_id": "abc"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if gotPath != "/ent/v2/tasks/abc/creations" {
		t.Errorf("path = %q, want /ent/v2/tasks/abc/creations", gotPath)
	}
	if gotAuth != "Token vidu-key" {
		t.Errorf("Authorization = %q, want Token vidu-key", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "vidu" {
		t.Errorf("GetChannelName() = %q, want vidu", a.GetChannelName())
	}
	models := a.GetModelList()
	found := false
	for _, m := range models {
		if m == "viduq1" {
			found = true
		}
	}
	if !found {
		t.Error("expected viduq1 in model list")
	}
}

// ---------------------------------------------------------------------------
// ParseTaskResult
// ---------------------------------------------------------------------------

func TestParseTaskResult_StateMapping(t *testing.T) {
	a := &TaskAdaptor{}
	cases := []struct {
		state string
		want  repo.TaskStatus
	}{
		{"created", repo.TaskStatusSubmitted},
		{"queueing", repo.TaskStatusSubmitted},
		{"processing", repo.TaskStatusInProgress},
	}
	for _, tt := range cases {
		t.Run(tt.state, func(t *testing.T) {
			ti, err := a.ParseTaskResult([]byte(`{"state":"` + tt.state + `"}`))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ti.Status != string(tt.want) {
				t.Errorf("Status = %q, want %q", ti.Status, tt.want)
			}
		})
	}
}

func TestParseTaskResult_SuccessExtractsFirstCreationURL(t *testing.T) {
	a := &TaskAdaptor{}
	body := `{"state":"success","creations":[{"url":"https://cdn/a.mp4"},{"url":"https://cdn/b.mp4"}]}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != string(repo.TaskStatusSuccess) {
		t.Errorf("Status = %q, want SUCCESS", ti.Status)
	}
	if ti.Url != "https://cdn/a.mp4" {
		t.Errorf("Url = %q, want first creation url", ti.Url)
	}
}

func TestParseTaskResult_FailedWithErrCode(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"state":"failed","err_code":"content_moderation"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != string(repo.TaskStatusFailure) {
		t.Errorf("Status = %q, want FAILURE", ti.Status)
	}
	if ti.Reason != "content_moderation" {
		t.Errorf("Reason = %q, want content_moderation", ti.Reason)
	}
}

func TestParseTaskResult_UnknownStateErrors(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.ParseTaskResult([]byte(`{"state":"bogus"}`)); err == nil {
		t.Fatal("expected error for unrecognized state enum")
	}
}

func TestParseTaskResult_MalformedJSON(t *testing.T) {
	a := &TaskAdaptor{}
	if _, err := a.ParseTaskResult([]byte("not json")); err == nil {
		t.Fatal("expected error, must not panic")
	}
}

// ---------------------------------------------------------------------------
// ConvertToOpenAIVideo
// ---------------------------------------------------------------------------

func TestConvertToOpenAIVideo_SuccessWithCreation(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID: "t1",
		Status: repo.TaskStatusSuccess,
		Data:   []byte(`{"state":"success","creations":[{"url":"https://cdn/v.mp4"}]}`),
	}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(data, &ov); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if ov.Metadata["url"] != "https://cdn/v.mp4" {
		t.Errorf("expected metadata url set, got %+v", ov.Metadata)
	}
	if ov.Status != "completed" {
		t.Errorf("Status = %q, want completed", ov.Status)
	}
}

func TestConvertToOpenAIVideo_FailedSetsError(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{
		TaskID: "t2",
		Status: repo.TaskStatusFailure,
		Data:   []byte(`{"state":"failed","err_code":"timeout"}`),
	}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	_ = json.Unmarshal(data, &ov)
	if ov.Error == nil || ov.Error.Message != "timeout" {
		t.Errorf("expected error.message=timeout, got %+v", ov.Error)
	}
}

func TestConvertToOpenAIVideo_MalformedData(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{TaskID: "t3", Data: []byte("not json")}
	if _, err := a.ConvertToOpenAIVideo(task); err == nil {
		t.Fatal("expected error for malformed stored task data")
	}
}
