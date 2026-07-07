package gemini

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    77,
			ChannelBaseUrl: "https://gemini.example.com",
			ApiKey:         "sk-gemini",
		},
	}
	a.Init(info)

	if a.ChannelType != 77 {
		t.Errorf("ChannelType = %d, want 77", a.ChannelType)
	}
	if a.baseURL != "https://gemini.example.com" {
		t.Errorf("baseURL = %q, want https://gemini.example.com", a.baseURL)
	}
	if a.apiKey != "sk-gemini" {
		t.Errorf("apiKey = %q, want sk-gemini", a.apiKey)
	}
}

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://gemini.example.com"}

	t.Run("unknown model uses default version", func(t *testing.T) {
		url, err := a.BuildRequestURL(&relaycommon.RelayInfo{OriginModelName: "veo-3.0-generate-001"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://gemini.example.com/v1beta/models/veo-3.0-generate-001:predictLongRunning"
		if url != want {
			t.Errorf("BuildRequestURL() = %q, want %q", url, want)
		}
	})

	t.Run("model with explicit version setting", func(t *testing.T) {
		url, err := a.BuildRequestURL(&relaycommon.RelayInfo{OriginModelName: "gemini-1.0-pro"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://gemini.example.com/v1/models/gemini-1.0-pro:predictLongRunning"
		if url != want {
			t.Errorf("BuildRequestURL() = %q, want %q", url, want)
		}
	})
}

func TestBuildRequestHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &TaskAdaptor{apiKey: "sk-abc"}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/", nil)

	err := a.BuildRequestHeader(c, req, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := req.Header.Get("x-goog-api-key"); got != "sk-abc" {
		t.Errorf("x-goog-api-key = %q, want sk-abc", got)
	}
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	models := a.GetModelList()
	want := []string{"veo-3.0-generate-001", "veo-3.1-generate-preview", "veo-3.1-fast-generate-preview"}
	if len(models) != len(want) {
		t.Fatalf("GetModelList() len = %d, want %d", len(models), len(want))
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, models[i], m)
		}
	}
	if got := a.GetChannelName(); got != "gemini" {
		t.Errorf("GetChannelName() = %q, want gemini", got)
	}
}

func TestBuildRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &TaskAdaptor{}

	t.Run("missing task_request in context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
		if body != nil {
			t.Errorf("body = %v, want nil", body)
		}
		if err == nil || err.Error() != "request not found in context" {
			t.Errorf("err = %v, want 'request not found in context'", err)
		}
	})

	t.Run("wrong task_request type", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("task_request", "not-a-task-request")
		body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
		if body != nil {
			t.Errorf("body = %v, want nil", body)
		}
		if err == nil || err.Error() != "unexpected task_request type" {
			t.Errorf("err = %v, want 'unexpected task_request type'", err)
		}
	})

	t.Run("valid task_request with metadata", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("task_request", relaycommon.TaskSubmitReq{
			Prompt: "a running dog",
			Metadata: map[string]interface{}{
				"aspectRatio":     "16:9",
				"durationSeconds": 8,
				"negativePrompt":  "blurry",
			},
		})

		body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, readErr := io.ReadAll(body)
		if readErr != nil {
			t.Fatalf("read body error: %v", readErr)
		}
		var payload GeminiVideoPayload
		if jsonErr := json.Unmarshal(data, &payload); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if len(payload.Instances) != 1 || payload.Instances[0].Prompt != "a running dog" {
			t.Errorf("Instances = %+v, want single prompt 'a running dog'", payload.Instances)
		}
		if payload.Parameters.AspectRatio != "16:9" {
			t.Errorf("AspectRatio = %q, want 16:9", payload.Parameters.AspectRatio)
		}
		if payload.Parameters.DurationSeconds != 8 {
			t.Errorf("DurationSeconds = %v, want 8", payload.Parameters.DurationSeconds)
		}
		if payload.Parameters.NegativePrompt != "blurry" {
			t.Errorf("NegativePrompt = %q, want blurry", payload.Parameters.NegativePrompt)
		}
	})

	t.Run("no metadata", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "hello"})

		body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, readErr := io.ReadAll(body)
		if readErr != nil {
			t.Fatalf("read body error: %v", readErr)
		}
		var payload GeminiVideoPayload
		if jsonErr := json.Unmarshal(data, &payload); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if payload.Parameters.AspectRatio != "" {
			t.Errorf("AspectRatio = %q, want empty", payload.Parameters.AspectRatio)
		}
	})

	t.Run("metadata field type mismatch fails unmarshal", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		// aspectRatio is a string field; passing a number should fail json.Unmarshal.
		c.Set("task_request", relaycommon.TaskSubmitReq{
			Prompt: "x",
			Metadata: map[string]interface{}{
				"aspectRatio": 12345,
			},
		})

		body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
		if body != nil {
			t.Errorf("body = %v, want nil", body)
		}
		if err == nil || !strings.Contains(err.Error(), "unmarshal metadata failed") {
			t.Errorf("err = %v, want wrapped 'unmarshal metadata failed'", err)
		}
	})
}

func TestDoResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &TaskAdaptor{}

	t.Run("valid response with operation name", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{}
		httpResp := resp.Result()
		httpResp.Body = io.NopCloser(newStringReader(`{"name":"models/veo-3.0-generate-001/operations/op-123"}`))

		taskID, taskData, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{OriginModelName: "veo-3.0-generate-001"})
		if taskErr != nil {
			t.Fatalf("unexpected taskErr: %+v", taskErr)
		}
		wantID := encodeLocalTaskID("models/veo-3.0-generate-001/operations/op-123")
		if taskID != wantID {
			t.Errorf("taskID = %q, want %q", taskID, wantID)
		}
		if string(taskData) != `{"name":"models/veo-3.0-generate-001/operations/op-123"}` {
			t.Errorf("taskData = %q, unexpected", string(taskData))
		}
		if w.Code != 200 {
			t.Errorf("response code = %d, want 200", w.Code)
		}
	})

	t.Run("invalid json body", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{}
		httpResp := resp.Result()
		httpResp.Body = io.NopCloser(newStringReader(`not-json`))

		taskID, taskData, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "unmarshal_response_failed" {
			t.Errorf("taskErr.Code = %q, want unmarshal_response_failed", taskErr.Code)
		}
		if taskID != "" {
			t.Errorf("taskID = %q, want empty", taskID)
		}
		if taskData != nil {
			t.Errorf("taskData = %v, want nil", taskData)
		}
	})

	t.Run("missing operation name", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{}
		httpResp := resp.Result()
		httpResp.Body = io.NopCloser(newStringReader(`{"name":"  "}`))

		taskID, taskData, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "invalid_response" {
			t.Errorf("taskErr.Code = %q, want invalid_response", taskErr.Code)
		}
		if taskID != "" {
			t.Errorf("taskID = %q, want empty", taskID)
		}
		if taskData != nil {
			t.Errorf("taskData = %v, want nil", taskData)
		}
	})
}

func TestFetchTask(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("missing task_id", func(t *testing.T) {
		resp, err := a.FetchTask("https://gemini.example.com", "sk-key", map[string]any{}, "")
		if resp != nil {
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil || err.Error() != "invalid task_id" {
			t.Errorf("err = %v, want 'invalid task_id'", err)
		}
	})

	t.Run("task_id wrong type", func(t *testing.T) {
		resp, err := a.FetchTask("https://gemini.example.com", "sk-key", map[string]any{"task_id": 123}, "")
		if resp != nil {
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil || err.Error() != "invalid task_id" {
			t.Errorf("err = %v, want 'invalid task_id'", err)
		}
	})

	t.Run("task_id fails base64 decode", func(t *testing.T) {
		resp, err := a.FetchTask("https://gemini.example.com", "sk-key", map[string]any{"task_id": "not!!valid!!base64"}, "")
		if resp != nil {
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil || !strings.Contains(err.Error(), "decode task_id failed") {
			t.Errorf("err = %v, want wrapped 'decode task_id failed'", err)
		}
	})

	t.Run("invalid proxy causes client creation failure", func(t *testing.T) {
		validTaskID := encodeLocalTaskID("operations/op-1")
		resp, err := a.FetchTask("https://gemini.example.com", "sk-key", map[string]any{"task_id": validTaskID}, "://bad-proxy")
		if resp != nil {
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil {
			t.Fatal("expected error for bad proxy, got nil")
		}
	})
}

func TestParseTaskResult(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("invalid json", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(`not-json`))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if info != nil {
			t.Errorf("info = %+v, want nil", info)
		}
	})

	t.Run("error message present", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(`{"error":{"message":"quota exceeded"}}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Status != repo.TaskStatusFailure {
			t.Errorf("Status = %q, want %q", info.Status, repo.TaskStatusFailure)
		}
		if info.Reason != "quota exceeded" {
			t.Errorf("Reason = %q, want quota exceeded", info.Reason)
		}
		if info.Progress != "100%" {
			t.Errorf("Progress = %q, want 100%%", info.Progress)
		}
	})

	t.Run("not done", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(`{"done":false}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Status != repo.TaskStatusInProgress {
			t.Errorf("Status = %q, want %q", info.Status, repo.TaskStatusInProgress)
		}
		if info.Progress != "50%" {
			t.Errorf("Progress = %q, want 50%%", info.Progress)
		}
	})

	t.Run("done with generated sample uri", func(t *testing.T) {
		body := `{
			"name": "models/veo-3.0-generate-001/operations/op-9",
			"done": true,
			"response": {
				"generateVideoResponse": {
					"generatedSamples": [
						{"video": {"uri": "https://files.example.com/video.mp4"}}
					]
				}
			}
		}`
		info, err := a.ParseTaskResult([]byte(body))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Status != repo.TaskStatusSuccess {
			t.Errorf("Status = %q, want %q", info.Status, repo.TaskStatusSuccess)
		}
		if info.Progress != "100%" {
			t.Errorf("Progress = %q, want 100%%", info.Progress)
		}
		wantTaskID := encodeLocalTaskID("models/veo-3.0-generate-001/operations/op-9")
		if info.TaskID != wantTaskID {
			t.Errorf("TaskID = %q, want %q", info.TaskID, wantTaskID)
		}
		if !strings.HasSuffix(info.Url, "/v1/videos/"+wantTaskID+"/content") {
			t.Errorf("Url = %q, want suffix /v1/videos/%s/content", info.Url, wantTaskID)
		}
		if info.RemoteUrl != "https://files.example.com/video.mp4" {
			t.Errorf("RemoteUrl = %q, want https://files.example.com/video.mp4", info.RemoteUrl)
		}
	})

	t.Run("done without generated samples leaves remote url empty", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(`{"name":"operations/op-2","done":true}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.RemoteUrl != "" {
			t.Errorf("RemoteUrl = %q, want empty", info.RemoteUrl)
		}
	})
}

func TestConvertToOpenAIVideo(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("valid task id extracts model", func(t *testing.T) {
		upstreamName := "models/veo-3.1-generate-preview/operations/op-42"
		task := &repo.Task{
			TaskID:     encodeLocalTaskID(upstreamName),
			Status:     repo.TaskStatusSuccess,
			Progress:   "100%",
			CreatedAt:  1000,
			FinishTime: 2000,
		}
		data, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var video map[string]any
		if jsonErr := json.Unmarshal(data, &video); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if video["model"] != "veo-3.1-generate-preview" {
			t.Errorf("model = %v, want veo-3.1-generate-preview", video["model"])
		}
		if video["id"] != task.TaskID {
			t.Errorf("id = %v, want %v", video["id"], task.TaskID)
		}
		if video["completed_at"] != float64(2000) {
			t.Errorf("completed_at = %v, want 2000", video["completed_at"])
		}
	})

	t.Run("no finish time falls back to updated at", func(t *testing.T) {
		upstreamName := "models/veo-3.0-generate-001/operations/op-1"
		task := &repo.Task{
			TaskID:    encodeLocalTaskID(upstreamName),
			Status:    repo.TaskStatusInProgress,
			Progress:  "50%",
			UpdatedAt: 555,
		}
		data, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var video map[string]any
		if jsonErr := json.Unmarshal(data, &video); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if video["completed_at"] != float64(555) {
			t.Errorf("completed_at = %v, want 555", video["completed_at"])
		}
	})

	t.Run("undecodable task id falls back to default model", func(t *testing.T) {
		task := &repo.Task{
			TaskID:   "not!!valid!!base64",
			Status:   repo.TaskStatusFailure,
			Progress: "100%",
		}
		data, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var video map[string]any
		if jsonErr := json.Unmarshal(data, &video); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if video["model"] != "veo-3.0-generate-001" {
			t.Errorf("model = %v, want veo-3.0-generate-001 (fallback)", video["model"])
		}
	})

	t.Run("decodable but non-matching operation name falls back to default model", func(t *testing.T) {
		task := &repo.Task{
			TaskID:   encodeLocalTaskID("no-model-pattern-here"),
			Status:   repo.TaskStatusInProgress,
			Progress: "10%",
		}
		data, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var video map[string]any
		if jsonErr := json.Unmarshal(data, &video); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if video["model"] != "veo-3.0-generate-001" {
			t.Errorf("model = %v, want veo-3.0-generate-001 (fallback)", video["model"])
		}
	})
}

func TestEncodeDecodeLocalTaskID(t *testing.T) {
	name := "models/veo-3.0-generate-001/operations/abc-123"
	encoded := encodeLocalTaskID(name)
	decoded, err := decodeLocalTaskID(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded != name {
		t.Errorf("decoded = %q, want %q", decoded, name)
	}

	if _, err := decodeLocalTaskID("not!!valid!!base64"); err == nil {
		t.Error("expected error for invalid base64, got nil")
	}
}

func TestExtractModelFromOperationName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty string", in: "", want: ""},
		{name: "standard regex match", in: "models/veo-3.0-generate-001/operations/op-1", want: "veo-3.0-generate-001"},
		{name: "no models prefix", in: "operations/op-1", want: ""},
		{name: "models prefix without operations suffix", in: "models/veo-3.0-generate-001", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractModelFromOperationName(tt.in)
			if got != tt.want {
				t.Errorf("extractModelFromOperationName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// newStringReader avoids importing strings solely for a reader helper.
func newStringReader(s string) io.Reader {
	return &stringReaderCloser{s: s}
}

type stringReaderCloser struct {
	s   string
	pos int
}

func (r *stringReaderCloser) Read(p []byte) (int, error) {
	if r.pos >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}
