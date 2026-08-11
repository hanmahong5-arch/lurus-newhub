package gemini

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

// ---------------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://gemini.example"}
	info := &relaycommon.RelayInfo{OriginModelName: "veo-3.0-generate-001"}
	got, err := a.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://gemini.example/v1beta/models/veo-3.0-generate-001:predictLongRunning"
	if got != want {
		t.Errorf("BuildRequestURL = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "goog-key"}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := a.BuildRequestHeader(nil, req, &relaycommon.RelayInfo{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Header.Get("x-goog-api-key") != "goog-key" {
		t.Errorf("x-goog-api-key = %q, want goog-key", req.Header.Get("x-goog-api-key"))
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", req.Header.Get("Content-Type"))
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

func TestBuildRequestBody_WrongContextType(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("task_request", "not-a-task-request")
	if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
		t.Fatal("expected error for unexpected task_request type")
	}
}

func TestBuildRequestBody_MetadataAppliedToParameters(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "a river",
		Metadata: map[string]any{"aspectRatio": "16:9", "durationSeconds": 8},
	})
	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(reader)
	var body GeminiVideoPayload
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if len(body.Instances) != 1 || body.Instances[0].Prompt != "a river" {
		t.Errorf("prompt not carried into instances: %+v", body.Instances)
	}
	if body.Parameters.AspectRatio != "16:9" {
		t.Errorf("AspectRatio = %q, want 16:9 from metadata", body.Parameters.AspectRatio)
	}
	if body.Parameters.DurationSeconds != 8 {
		t.Errorf("DurationSeconds = %v, want 8 from metadata", body.Parameters.DurationSeconds)
	}
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func TestDoResponse_Success(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"name":"operations/abc123"}`))}
	info := &relaycommon.RelayInfo{OriginModelName: "veo-3.0-generate-001"}
	taskID, data, taskErr := a.DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if len(data) == 0 {
		t.Error("expected raw response body returned")
	}
	// task ID must be reversible back to the upstream operation name.
	decoded, err := decodeLocalTaskID(taskID)
	if err != nil || decoded != "operations/abc123" {
		t.Errorf("taskID %q does not decode back to upstream operation name: decoded=%q err=%v", taskID, decoded, err)
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if ov.Model != "veo-3.0-generate-001" {
		t.Errorf("Model = %q", ov.Model)
	}
}

func TestDoResponse_MissingOperationName(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"name":""}`))}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error when upstream omits operation name")
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
	bcResp1, err := a.FetchTask("https://x", "key", map[string]any{}, "")
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error when task_id missing")
	}
}

func TestFetchTask_InvalidTaskIDEncoding(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp0, err := a.FetchTask("https://x", "key", map[string]any{"task_id": "not-base64!!"}, "")
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error decoding malformed local task id")
	}
}

func TestFetchTask_URLAndAuth(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		_, _ = w.Write([]byte(`{"done":true}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	localID := encodeLocalTaskID("operations/xyz")
	resp, err := a.FetchTask(srv.URL, "goog-key", map[string]any{"task_id": localID}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if gotPath != "/v1beta/operations/xyz" {
		t.Errorf("path = %q, want /v1beta/operations/xyz", gotPath)
	}
	if gotKey != "goog-key" {
		t.Errorf("x-goog-api-key = %q", gotKey)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	if a.GetChannelName() != "gemini" {
		t.Errorf("GetChannelName() = %q", a.GetChannelName())
	}
	models := a.GetModelList()
	if len(models) == 0 {
		t.Fatal("model list must not be empty")
	}
}

// ---------------------------------------------------------------------------
// ParseTaskResult
// ---------------------------------------------------------------------------

func TestParseTaskResult_ErrorTakesPriority(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"done":true,"error":{"message":"quota exceeded"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != string(repo.TaskStatusFailure) {
		t.Errorf("Status = %q, want FAILURE when upstream reports error even if done=true", ti.Status)
	}
	if ti.Reason != "quota exceeded" {
		t.Errorf("Reason = %q, want quota exceeded", ti.Reason)
	}
}

func TestParseTaskResult_NotDoneIsInProgress(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"done":false}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != string(repo.TaskStatusInProgress) {
		t.Errorf("Status = %q, want IN_PROGRESS", ti.Status)
	}
	if ti.Progress != "50%" {
		t.Errorf("Progress = %q, want 50%%", ti.Progress)
	}
}

func TestParseTaskResult_DoneSuccessExtractsRemoteURL(t *testing.T) {
	a := &TaskAdaptor{}
	system_setting.ServerAddress = "https://hub.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = "" })

	body := `{"name":"operations/abc","done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":"https://storage.googleapis.com/x.mp4"}}]}}}`
	ti, err := a.ParseTaskResult([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.Status != string(repo.TaskStatusSuccess) {
		t.Errorf("Status = %q, want SUCCESS", ti.Status)
	}
	if ti.RemoteUrl != "https://storage.googleapis.com/x.mp4" {
		t.Errorf("RemoteUrl = %q, want upstream video uri", ti.RemoteUrl)
	}
	wantLocalURL := "https://hub.example.com/v1/videos/" + encodeLocalTaskID("operations/abc") + "/content"
	if ti.Url != wantLocalURL {
		t.Errorf("Url = %q, want %q", ti.Url, wantLocalURL)
	}
}

func TestParseTaskResult_DoneSuccessNoSamples(t *testing.T) {
	a := &TaskAdaptor{}
	ti, err := a.ParseTaskResult([]byte(`{"name":"operations/abc","done":true,"response":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ti.RemoteUrl != "" {
		t.Errorf("RemoteUrl should stay empty when upstream has no generated samples, got %q", ti.RemoteUrl)
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

func TestConvertToOpenAIVideo_ExtractsModelFromOperationName(t *testing.T) {
	a := &TaskAdaptor{}
	localID := encodeLocalTaskID("models/veo-3.1-generate-preview/operations/xyz")
	task := &repo.Task{
		TaskID:     localID,
		Status:     repo.TaskStatusSuccess,
		CreatedAt:  100,
		FinishTime: 200,
	}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	if err := json.Unmarshal(data, &ov); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if ov.Model != "veo-3.1-generate-preview" {
		t.Errorf("Model = %q, want extracted from operation name", ov.Model)
	}
	if ov.Status != "completed" {
		t.Errorf("Status = %q, want completed", ov.Status)
	}
	if ov.CompletedAt != 200 {
		t.Errorf("CompletedAt = %d, want FinishTime=200 preferred over UpdatedAt", ov.CompletedAt)
	}
}

func TestConvertToOpenAIVideo_FallsBackToDefaultModelWhenUndecodable(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{TaskID: "not-base64!!", Status: repo.TaskStatusInProgress}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	_ = json.Unmarshal(data, &ov)
	if ov.Model != "veo-3.0-generate-001" {
		t.Errorf("Model = %q, want default fallback model for undecodable task id", ov.Model)
	}
}

func TestConvertToOpenAIVideo_UsesUpdatedAtWhenNoFinishTime(t *testing.T) {
	a := &TaskAdaptor{}
	localID := encodeLocalTaskID("operations/no-model-here")
	task := &repo.Task{TaskID: localID, Status: repo.TaskStatusInProgress, UpdatedAt: 555}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var ov dto.OpenAIVideo
	_ = json.Unmarshal(data, &ov)
	if ov.CompletedAt != 555 {
		t.Errorf("CompletedAt = %d, want UpdatedAt fallback of 555", ov.CompletedAt)
	}
}

// ---------------------------------------------------------------------------
// encodeLocalTaskID / decodeLocalTaskID / extractModelFromOperationName
// ---------------------------------------------------------------------------

func TestEncodeDecodeLocalTaskID_RoundTrip(t *testing.T) {
	name := "operations/some-op-id"
	encoded := encodeLocalTaskID(name)
	decoded, err := decodeLocalTaskID(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded != name {
		t.Errorf("round trip mismatch: got %q, want %q", decoded, name)
	}
}

func TestDecodeLocalTaskID_InvalidBase64(t *testing.T) {
	if _, err := decodeLocalTaskID("not valid base64!!!"); err == nil {
		t.Fatal("expected decode error for invalid base64 input")
	}
}

func TestExtractModelFromOperationName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"", ""},
		{"models/veo-3.0-generate-001/operations/abc", "veo-3.0-generate-001"},
		{"no-models-segment", ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractModelFromOperationName(tt.name); got != tt.want {
				t.Errorf("extractModelFromOperationName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
