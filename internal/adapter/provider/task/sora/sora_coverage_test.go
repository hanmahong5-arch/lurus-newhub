package sora

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

func newSoraTestContext(t *testing.T, method, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	// no request-body size limit for hermetic tests (default zero value would
	// reject any non-empty body via common.GetRequestBody).
	constant.MaxRequestBodyMB = -1
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, w
}

func TestSora_Init(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    7,
			ChannelBaseUrl: "https://api.example.com",
			ApiKey:         "test-key",
		},
	}
	a.Init(info)
	if a.ChannelType != 7 {
		t.Fatalf("ChannelType = %d, want 7", a.ChannelType)
	}
	if a.baseURL != "https://api.example.com" {
		t.Fatalf("baseURL = %q, want %q", a.baseURL, "https://api.example.com")
	}
	if a.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want %q", a.apiKey, "test-key")
	}
}

func TestSora_BuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://api.example.com"}

	t.Run("remix action", func(t *testing.T) {
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionRemix, OriginTaskID: "task-123"}}
		url, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://api.example.com/v1/videos/task-123/remix"
		if url != want {
			t.Fatalf("url = %q, want %q", url, want)
		}
	})

	t.Run("generate action", func(t *testing.T) {
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate}}
		url, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://api.example.com/v1/videos"
		if url != want {
			t.Fatalf("url = %q, want %q", url, want)
		}
	})
}

func TestSora_BuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{apiKey: "secret-key"}
	c, _ := newSoraTestContext(t, http.MethodPost, "", "multipart/form-data; boundary=x")
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/videos", nil)

	err := a.BuildRequestHeader(c, req, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer secret-key" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer secret-key")
	}
	if got := req.Header.Get("Content-Type"); got != "multipart/form-data; boundary=x" {
		t.Fatalf("Content-Type header = %q, want %q", got, "multipart/form-data; boundary=x")
	}
}

func TestSora_BuildRequestBody(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newSoraTestContext(t, http.MethodPost, `{"prompt":"a cat"}`, "application/json")

	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(reader); err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if got := buf.String(); got != `{"prompt":"a cat"}` {
		t.Fatalf("body = %q, want %q", got, `{"prompt":"a cat"}`)
	}
}

func TestSora_BuildRequestBody_RequestBodyTooLarge(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newSoraTestContext(t, http.MethodPost, `{"prompt":"a cat"}`, "application/json")
	// Force common.GetRequestBody's size-limit branch to reject a non-empty body.
	prevMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 0
	defer func() { constant.MaxRequestBodyMB = prevMax }()

	reader, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if reader != nil {
		t.Fatalf("expected nil reader, got %+v", reader)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "get_request_body_failed: request body exceeds 0 MB: request body too large" {
		t.Fatalf("err = %q, want %q", got, "get_request_body_failed: request body exceeds 0 MB: request body too large")
	}
}

func TestSora_GetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	models := a.GetModelList()
	if len(models) != 2 || models[0] != "sora-2" || models[1] != "sora-2-pro" {
		t.Fatalf("GetModelList() = %v, want [sora-2 sora-2-pro]", models)
	}
	if name := a.GetChannelName(); name != "sora" {
		t.Fatalf("GetChannelName() = %q, want %q", name, "sora")
	}
}

func TestSora_ValidateRequestAndSetAction(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("remix with valid prompt", func(t *testing.T) {
		c, _ := newSoraTestContext(t, http.MethodPost, `{"prompt":"remix this"}`, "application/json")
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionRemix}}
		if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
			t.Fatalf("unexpected TaskError: %+v", taskErr)
		}
	})

	t.Run("remix with empty prompt", func(t *testing.T) {
		c, _ := newSoraTestContext(t, http.MethodPost, `{"prompt":"   "}`, "application/json")
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionRemix}}
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected TaskError, got nil")
		}
		if taskErr.Code != "invalid_request" {
			t.Fatalf("Code = %q, want %q", taskErr.Code, "invalid_request")
		}
		if taskErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("StatusCode = %d, want %d", taskErr.StatusCode, http.StatusBadRequest)
		}
		if taskErr.Error == nil || taskErr.Error.Error() != "field prompt is required" {
			t.Fatalf("Error = %v, want %q", taskErr.Error, "field prompt is required")
		}
	})

	t.Run("remix with malformed json body", func(t *testing.T) {
		c, _ := newSoraTestContext(t, http.MethodPost, `{bad json`, "application/json")
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionRemix}}
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected TaskError, got nil")
		}
		if taskErr.Code != "invalid_request" {
			t.Fatalf("Code = %q, want %q", taskErr.Code, "invalid_request")
		}
		if taskErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("StatusCode = %d, want %d", taskErr.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("non-remix action delegates to ValidateMultipartDirect", func(t *testing.T) {
		// generate action with an empty model hits ValidateMultipartDirect's
		// "missing_model" branch without needing a *TaskRelayInfo.
		c, _ := newSoraTestContext(t, http.MethodPost, `{"prompt":"a cat","model":""}`, "application/json")
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate}}
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected TaskError, got nil")
		}
		if taskErr.Code != "missing_model" {
			t.Fatalf("Code = %q, want %q", taskErr.Code, "missing_model")
		}
		if taskErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("StatusCode = %d, want %d", taskErr.StatusCode, http.StatusBadRequest)
		}
	})
}

func TestSora_FetchTask_InvalidTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("https://api.example.com", "key", map[string]any{}, "")
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if err == nil || err.Error() != "invalid task_id" {
		t.Fatalf("err = %v, want %q", err, "invalid task_id")
	}
}

func TestSora_FetchTask_InvalidProxyURL(t *testing.T) {
	// Malformed proxy URL is rejected by url.Parse inside
	// app.NewProxyHttpClient before any real network I/O happens.
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("https://api.example.com", "key", map[string]any{"task_id": "v1"}, "://bad-proxy")
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	const wantPrefix = "new proxy http client failed: "
	if got := err.Error(); len(got) < len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("err = %q, want prefix %q", got, wantPrefix)
	}
}

func TestSora_DoResponse(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("valid response with id", func(t *testing.T) {
		c, w := newSoraTestContext(t, http.MethodPost, "", "application/json")
		body := `{"id":"video_1","status":"queued"}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
		}
		resp.Body = readCloserFromString(body)

		taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if taskErr != nil {
			t.Fatalf("unexpected TaskError: %+v", taskErr)
		}
		if taskID != "video_1" {
			t.Fatalf("taskID = %q, want %q", taskID, "video_1")
		}
		if string(taskData) != body {
			t.Fatalf("taskData = %q, want %q", taskData, body)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("recorder status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("legacy response with task_id only", func(t *testing.T) {
		c, _ := newSoraTestContext(t, http.MethodPost, "", "application/json")
		body := `{"task_id":"legacy_1","status":"queued"}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: readCloserFromString(body)}

		taskID, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if taskErr != nil {
			t.Fatalf("unexpected TaskError: %+v", taskErr)
		}
		if taskID != "legacy_1" {
			t.Fatalf("taskID = %q, want %q", taskID, "legacy_1")
		}
	})

	t.Run("response missing both id and task_id", func(t *testing.T) {
		c, _ := newSoraTestContext(t, http.MethodPost, "", "application/json")
		body := `{"status":"queued"}`
		resp := &http.Response{StatusCode: http.StatusOK, Body: readCloserFromString(body)}

		_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected TaskError, got nil")
		}
		if taskErr.Code != "invalid_response" {
			t.Fatalf("Code = %q, want %q", taskErr.Code, "invalid_response")
		}
		if taskErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("StatusCode = %d, want %d", taskErr.StatusCode, http.StatusInternalServerError)
		}
	})

	t.Run("malformed json body", func(t *testing.T) {
		c, _ := newSoraTestContext(t, http.MethodPost, "", "application/json")
		resp := &http.Response{StatusCode: http.StatusOK, Body: readCloserFromString(`{not json`)}

		_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected TaskError, got nil")
		}
		if taskErr.Code != "unmarshal_response_body_failed" {
			t.Fatalf("Code = %q, want %q", taskErr.Code, "unmarshal_response_body_failed")
		}
	})
}

func TestSora_ParseTaskResult(t *testing.T) {
	a := &TaskAdaptor{}

	tests := []struct {
		name         string
		body         string
		wantStatus   string
		wantProgress string
		wantReason   string
		wantURLHas   string
	}{
		{name: "queued", body: `{"id":"v1","status":"queued"}`, wantStatus: string(repo.TaskStatusQueued)},
		{name: "pending", body: `{"id":"v1","status":"pending"}`, wantStatus: string(repo.TaskStatusQueued)},
		{name: "processing", body: `{"id":"v1","status":"processing","progress":42}`, wantStatus: string(repo.TaskStatusInProgress), wantProgress: "42%"},
		{name: "in_progress", body: `{"id":"v1","status":"in_progress"}`, wantStatus: string(repo.TaskStatusInProgress)},
		{name: "completed", body: `{"id":"v1","status":"completed"}`, wantStatus: string(repo.TaskStatusSuccess), wantURLHas: "/v1/videos/v1/content"},
		{name: "failed with error message", body: `{"id":"v1","status":"failed","error":{"message":"boom","code":"E1"}}`, wantStatus: string(repo.TaskStatusFailure), wantReason: "boom"},
		{name: "cancelled without error", body: `{"id":"v1","status":"cancelled"}`, wantStatus: string(repo.TaskStatusFailure), wantReason: "task failed"},
		{name: "unknown status", body: `{"id":"v1","status":"weird"}`, wantStatus: ""},
		{name: "progress boundary 0 not shown", body: `{"id":"v1","status":"queued","progress":0}`, wantStatus: string(repo.TaskStatusQueued), wantProgress: ""},
		{name: "progress boundary 100 not shown", body: `{"id":"v1","status":"completed","progress":100}`, wantStatus: string(repo.TaskStatusSuccess), wantProgress: "", wantURLHas: "/v1/videos/v1/content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := a.ParseTaskResult([]byte(tt.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Code != 0 {
				t.Fatalf("Code = %d, want 0", info.Code)
			}
			if info.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q", info.Status, tt.wantStatus)
			}
			if info.Progress != tt.wantProgress {
				t.Fatalf("Progress = %q, want %q", info.Progress, tt.wantProgress)
			}
			if info.Reason != tt.wantReason {
				t.Fatalf("Reason = %q, want %q", info.Reason, tt.wantReason)
			}
			if tt.wantURLHas != "" && info.Url == "" {
				t.Fatalf("Url is empty, want to contain %q", tt.wantURLHas)
			}
			if tt.wantURLHas == "" && info.Url != "" {
				t.Fatalf("Url = %q, want empty", info.Url)
			}
		})
	}

	t.Run("malformed json", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(`{not json`))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if info != nil {
			t.Fatalf("expected nil info, got %+v", info)
		}
	})
}

func TestSora_ConvertToOpenAIVideo(t *testing.T) {
	a := &TaskAdaptor{}
	task := &repo.Task{Data: json.RawMessage(`{"id":"v1"}`)}
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `{"id":"v1"}` {
		t.Fatalf("data = %q, want %q", data, `{"id":"v1"}`)
	}
}

// readCloserFromString builds an io.ReadCloser over a string for constructing
// fake http.Response bodies without any real network I/O.
func readCloserFromString(s string) *stringReadCloser {
	return &stringReadCloser{Buffer: bytes.NewBufferString(s)}
}

type stringReadCloser struct {
	*bytes.Buffer
}

func (s *stringReadCloser) Close() error { return nil }
