package doubao

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    123,
			ChannelBaseUrl: "https://ark.example.com",
			ApiKey:         "sk-test",
		},
	}
	a.Init(info)

	if a.ChannelType != 123 {
		t.Errorf("ChannelType = %d, want 123", a.ChannelType)
	}
	if a.baseURL != "https://ark.example.com" {
		t.Errorf("baseURL = %q, want https://ark.example.com", a.baseURL)
	}
	if a.apiKey != "sk-test" {
		t.Errorf("apiKey = %q, want sk-test", a.apiKey)
	}
}

func TestValidateRequestAndSetAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &TaskAdaptor{}

	t.Run("valid json body sets action and no error", func(t *testing.T) {
		prev := constant.MaxRequestBodyMB
		constant.MaxRequestBodyMB = -1 // no limit, hermetic test isolation
		defer func() { constant.MaxRequestBodyMB = prev }()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := `{"prompt":"a running cat","model":"doubao-seedance-1-0-pro-250528"}`
		req := httptest.NewRequest("POST", "/v1/video/generations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		c.Request = req
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}

		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr != nil {
			t.Fatalf("unexpected taskErr: %+v", taskErr)
		}
		v, exists := c.Get("task_request")
		if !exists {
			t.Fatal("expected task_request to be set in context")
		}
		submitted, ok := v.(relaycommon.TaskSubmitReq)
		if !ok {
			t.Fatalf("task_request has unexpected type %T", v)
		}
		if submitted.Prompt != "a running cat" {
			t.Errorf("Prompt = %q, want 'a running cat'", submitted.Prompt)
		}
		if info.Action != constant.TaskActionGenerate {
			t.Errorf("info.Action = %q, want %q", info.Action, constant.TaskActionGenerate)
		}
	})

	t.Run("invalid json body returns invalid_request error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest("POST", "/v1/video/generations", strings.NewReader(`not-json`))
		req.Header.Set("Content-Type", "application/json")
		c.Request = req

		taskErr := a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "invalid_request" {
			t.Errorf("taskErr.Code = %q, want invalid_request", taskErr.Code)
		}
	})

	t.Run("missing prompt returns validation error", func(t *testing.T) {
		prev := constant.MaxRequestBodyMB
		constant.MaxRequestBodyMB = -1
		defer func() { constant.MaxRequestBodyMB = prev }()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest("POST", "/v1/video/generations", strings.NewReader(`{"model":"m"}`))
		req.Header.Set("Content-Type", "application/json")
		c.Request = req

		taskErr := a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr for missing prompt, got nil")
		}
	})
}

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://ark.example.com"}
	url, err := a.BuildRequestURL(&relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://ark.example.com/api/v3/contents/generations/tasks"
	if url != want {
		t.Errorf("BuildRequestURL() = %q, want %q", url, want)
	}
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
	if got := req.Header.Get("Authorization"); got != "Bearer sk-abc" {
		t.Errorf("Authorization = %q, want Bearer sk-abc", got)
	}
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	models := a.GetModelList()
	if len(models) != 3 {
		t.Fatalf("GetModelList() len = %d, want 3", len(models))
	}
	want := []string{
		"doubao-seedance-1-0-pro-250528",
		"doubao-seedance-1-0-lite-t2v",
		"doubao-seedance-1-0-lite-i2v",
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, models[i], m)
		}
	}
	if got := a.GetChannelName(); got != "doubao-video" {
		t.Errorf("GetChannelName() = %q, want doubao-video", got)
	}
}

func TestConvertToRequestPayload(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("prompt only", func(t *testing.T) {
		req := &relaycommon.TaskSubmitReq{Model: "doubao-seedance-1-0-pro-250528", Prompt: "a cat"}
		payload, err := a.convertToRequestPayload(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if payload.Model != "doubao-seedance-1-0-pro-250528" {
			t.Errorf("Model = %q, want doubao-seedance-1-0-pro-250528", payload.Model)
		}
		if len(payload.Content) != 1 {
			t.Fatalf("Content len = %d, want 1", len(payload.Content))
		}
		if payload.Content[0].Type != "text" || payload.Content[0].Text != "a cat" {
			t.Errorf("Content[0] = %+v, want text/a cat", payload.Content[0])
		}
		if payload.Content[0].ImageURL != nil {
			t.Errorf("Content[0].ImageURL = %+v, want nil", payload.Content[0].ImageURL)
		}
	})

	t.Run("prompt and images", func(t *testing.T) {
		req := &relaycommon.TaskSubmitReq{
			Model:  "doubao-seedance-1-0-lite-i2v",
			Prompt: "a dog",
			Images: []string{"https://img1", "https://img2"},
		}
		payload, err := a.convertToRequestPayload(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payload.Content) != 3 {
			t.Fatalf("Content len = %d, want 3", len(payload.Content))
		}
		if payload.Content[0].Type != "text" || payload.Content[0].Text != "a dog" {
			t.Errorf("Content[0] = %+v, want text/a dog", payload.Content[0])
		}
		if payload.Content[1].Type != "image_url" || payload.Content[1].ImageURL == nil || payload.Content[1].ImageURL.URL != "https://img1" {
			t.Errorf("Content[1] = %+v, want image_url/https://img1", payload.Content[1])
		}
		if payload.Content[2].Type != "image_url" || payload.Content[2].ImageURL == nil || payload.Content[2].ImageURL.URL != "https://img2" {
			t.Errorf("Content[2] = %+v, want image_url/https://img2", payload.Content[2])
		}
	})

	t.Run("no prompt no images", func(t *testing.T) {
		req := &relaycommon.TaskSubmitReq{Model: "m"}
		payload, err := a.convertToRequestPayload(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payload.Content) != 0 {
			t.Errorf("Content len = %d, want 0", len(payload.Content))
		}
	})
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

	t.Run("valid task_request", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "m1", Prompt: "hello"})

		body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, readErr := io.ReadAll(body)
		if readErr != nil {
			t.Fatalf("read body error: %v", readErr)
		}
		var payload requestPayload
		if jsonErr := json.Unmarshal(data, &payload); jsonErr != nil {
			t.Fatalf("unmarshal error: %v", jsonErr)
		}
		if payload.Model != "m1" {
			t.Errorf("Model = %q, want m1", payload.Model)
		}
		if len(payload.Content) != 1 || payload.Content[0].Text != "hello" {
			t.Errorf("Content = %+v, want single text 'hello'", payload.Content)
		}
	})
}

func TestDoResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &TaskAdaptor{}

	t.Run("valid response with task id", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{}
		httpResp := resp.Result()
		httpResp.Body = io.NopCloser(newStringReader(`{"id":"task-123"}`))
		defer func() { _ = httpResp.Body.Close() }()

		taskID, taskData, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{})
		if taskErr != nil {
			t.Fatalf("unexpected taskErr: %+v", taskErr)
		}
		if taskID != "task-123" {
			t.Errorf("taskID = %q, want task-123", taskID)
		}
		if string(taskData) != `{"id":"task-123"}` {
			t.Errorf("taskData = %q, want {\"id\":\"task-123\"}", string(taskData))
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
		defer func() { _ = httpResp.Body.Close() }()

		taskID, taskData, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "unmarshal_response_body_failed" {
			t.Errorf("taskErr.Code = %q, want unmarshal_response_body_failed", taskErr.Code)
		}
		if taskID != "" {
			t.Errorf("taskID = %q, want empty", taskID)
		}
		if taskData != nil {
			t.Errorf("taskData = %v, want nil", taskData)
		}
	})

	t.Run("empty task id", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &httptest.ResponseRecorder{}
		httpResp := resp.Result()
		httpResp.Body = io.NopCloser(newStringReader(`{"id":""}`))
		defer func() { _ = httpResp.Body.Close() }()

		taskID, _, taskErr := a.DoResponse(c, httpResp, &relaycommon.RelayInfo{})
		if taskErr == nil {
			t.Fatal("expected taskErr, got nil")
		}
		if taskErr.Code != "invalid_response" {
			t.Errorf("taskErr.Code = %q, want invalid_response", taskErr.Code)
		}
		if taskID != "" {
			t.Errorf("taskID = %q, want empty", taskID)
		}
	})
}

func TestFetchTask(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("missing task_id", func(t *testing.T) {
		resp, err := a.FetchTask("https://ark.example.com", "sk-key", map[string]any{}, "")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil || err.Error() != "invalid task_id" {
			t.Errorf("err = %v, want 'invalid task_id'", err)
		}
	})

	t.Run("task_id wrong type", func(t *testing.T) {
		resp, err := a.FetchTask("https://ark.example.com", "sk-key", map[string]any{"task_id": 123}, "")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil || err.Error() != "invalid task_id" {
			t.Errorf("err = %v, want 'invalid task_id'", err)
		}
	})

	t.Run("invalid proxy causes client creation failure", func(t *testing.T) {
		resp, err := a.FetchTask("https://ark.example.com", "sk-key", map[string]any{"task_id": "t1"}, "://bad-proxy")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
			t.Errorf("resp = %v, want nil", resp)
		}
		if err == nil {
			t.Fatal("expected error for bad proxy, got nil")
		}
	})
}

func TestParseTaskResult(t *testing.T) {
	a := &TaskAdaptor{}

	tests := []struct {
		name           string
		body           string
		wantStatus     repo.TaskStatus
		wantProgress   string
		wantURL        string
		wantReason     string
		wantCompletion int
		wantTotal      int
	}{
		{name: "pending", body: `{"status":"pending"}`, wantStatus: repo.TaskStatusQueued, wantProgress: "10%"},
		{name: "queued", body: `{"status":"queued"}`, wantStatus: repo.TaskStatusQueued, wantProgress: "10%"},
		{name: "processing", body: `{"status":"processing"}`, wantStatus: repo.TaskStatusInProgress, wantProgress: "50%"},
		{
			name:           "succeeded",
			body:           `{"status":"succeeded","content":{"video_url":"https://video.mp4"},"usage":{"completion_tokens":7,"total_tokens":42}}`,
			wantStatus:     repo.TaskStatusSuccess,
			wantProgress:   "100%",
			wantURL:        "https://video.mp4",
			wantCompletion: 7,
			wantTotal:      42,
		},
		{name: "failed", body: `{"status":"failed"}`, wantStatus: repo.TaskStatusFailure, wantProgress: "100%", wantReason: "task failed"},
		{name: "unknown status", body: `{"status":"weird"}`, wantStatus: repo.TaskStatusInProgress, wantProgress: "30%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := a.ParseTaskResult([]byte(tt.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Code != 0 {
				t.Errorf("Code = %d, want 0", info.Code)
			}
			if info.Status != string(tt.wantStatus) {
				t.Errorf("Status = %q, want %q", info.Status, tt.wantStatus)
			}
			if info.Progress != tt.wantProgress {
				t.Errorf("Progress = %q, want %q", info.Progress, tt.wantProgress)
			}
			if info.Url != tt.wantURL {
				t.Errorf("Url = %q, want %q", info.Url, tt.wantURL)
			}
			if info.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", info.Reason, tt.wantReason)
			}
			if info.CompletionTokens != tt.wantCompletion {
				t.Errorf("CompletionTokens = %d, want %d", info.CompletionTokens, tt.wantCompletion)
			}
			if info.TotalTokens != tt.wantTotal {
				t.Errorf("TotalTokens = %d, want %d", info.TotalTokens, tt.wantTotal)
			}
		})
	}

	t.Run("invalid json", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(`not-json`))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if info != nil {
			t.Errorf("info = %+v, want nil", info)
		}
	})
}

// newStringReader avoids importing strings solely for a reader helper elsewhere in the file.
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
