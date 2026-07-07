package jimeng

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
	// FetchTask calls app.GetHttpClientWithProxy(""), which returns the
	// package-level httpClient singleton; without this it is nil in a bare
	// test binary and Client.Do panics before any I/O is attempted.
	app.InitHttpClient()
}

func newGinContext(method, target string, body []byte, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, w
}

// ---------------------------------------------------------------------------
// isNewAPIRelay
// ---------------------------------------------------------------------------

func TestIsNewAPIRelay(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"sk-abc123", true},
		{"ak|sk", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isNewAPIRelay(tc.key); got != tc.want {
			t.Errorf("isNewAPIRelay(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestTaskAdaptorInit(t *testing.T) {
	t.Run("valid ak|sk pair", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 42, ChannelBaseUrl: "https://base.example", ApiKey: " ak1 | sk1 "}}
		a.Init(info)
		if a.ChannelType != 42 {
			t.Errorf("ChannelType = %d, want 42", a.ChannelType)
		}
		if a.baseURL != "https://base.example" {
			t.Errorf("baseURL = %q", a.baseURL)
		}
		if a.accessKey != "ak1" {
			t.Errorf("accessKey = %q, want ak1", a.accessKey)
		}
		if a.secretKey != "sk1" {
			t.Errorf("secretKey = %q, want sk1", a.secretKey)
		}
	})

	t.Run("malformed api key leaves keys empty", func(t *testing.T) {
		a := &TaskAdaptor{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-newapi-token"}}
		a.Init(info)
		if a.accessKey != "" || a.secretKey != "" {
			t.Errorf("expected empty keys for non ak|sk format, got accessKey=%q secretKey=%q", a.accessKey, a.secretKey)
		}
	})
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestValidateRequestAndSetAction(t *testing.T) {
	a := &TaskAdaptor{}

	prevMaxBody := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = -1
	t.Cleanup(func() { constant.MaxRequestBodyMB = prevMaxBody })

	t.Run("valid json body sets task_request in context", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"prompt": "a cat running"})
		c, _ := newGinContext(http.MethodPost, "/v1/videos", body, "application/json")
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr != nil {
			t.Fatalf("unexpected task error: %+v", taskErr)
		}
		v, exists := c.Get("task_request")
		if !exists {
			t.Fatal("expected task_request to be stored in context")
		}
		req, ok := v.(relaycommon.TaskSubmitReq)
		if !ok {
			t.Fatalf("stored value has unexpected type %T", v)
		}
		if req.Prompt != "a cat running" {
			t.Errorf("Prompt = %q, want %q", req.Prompt, "a cat running")
		}
		if info.Action != constant.TaskActionGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionGenerate)
		}
	})

	t.Run("missing prompt returns task error", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{})
		c, _ := newGinContext(http.MethodPost, "/v1/videos", body, "application/json")
		info := &relaycommon.RelayInfo{}
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected task error for missing prompt")
		}
	})

	t.Run("invalid json body returns task error", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/v1/videos", []byte("{not json"), "application/json")
		info := &relaycommon.RelayInfo{}
		taskErr := a.ValidateRequestAndSetAction(c, info)
		if taskErr == nil {
			t.Fatal("expected task error for invalid json")
		}
	})
}

// ---------------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://visual.volcengineapi.com"}

	t.Run("newapi relay key", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-abc"}}
		got, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://visual.volcengineapi.com/jimeng/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31"
		if got != want {
			t.Errorf("BuildRequestURL() = %q, want %q", got, want)
		}
	})

	t.Run("direct volcengine key", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "ak|sk"}}
		got, err := a.BuildRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://visual.volcengineapi.com/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31"
		if got != want {
			t.Errorf("BuildRequestURL() = %q, want %q", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// BuildRequestHeader
// ---------------------------------------------------------------------------

func TestBuildRequestHeader(t *testing.T) {
	t.Run("newapi relay sets bearer auth", func(t *testing.T) {
		a := &TaskAdaptor{}
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		req := httptest.NewRequest(http.MethodPost, "https://example.com/path", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-token123"}}
		if err := a.BuildRequestHeader(c, req, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk-token123" {
			t.Errorf("Authorization = %q", got)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := req.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
	})

	t.Run("direct key signs request and sets authorization header", func(t *testing.T) {
		a := &TaskAdaptor{accessKey: "AK123", secretKey: "SK456"}
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		req := httptest.NewRequest(http.MethodPost, "https://visual.example.com/?Action=CVSync2AsyncSubmitTask", strings.NewReader(`{"a":1}`))
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "AK123|SK456"}}
		if err := a.BuildRequestHeader(c, req, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		auth := req.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "HMAC-SHA256 Credential=AK123/") {
			t.Errorf("Authorization = %q, want HMAC-SHA256 Credential=AK123/... prefix", auth)
		}
		if !strings.Contains(auth, "SignedHeaders=") || !strings.Contains(auth, "Signature=") {
			t.Errorf("Authorization missing expected components: %q", auth)
		}
		if req.Header.Get("X-Date") == "" {
			t.Error("expected X-Date header to be set")
		}
		if req.Header.Get("X-Content-Sha256") == "" {
			t.Error("expected X-Content-Sha256 header to be set")
		}
	})
}

// ---------------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------------

func multipartRequest(t *testing.T, fields map[string]string, fileField string, files map[string][]byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	for name, content := range files {
		fw, err := w.CreateFormFile(fileField, name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("write file content: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestBuildRequestBody(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("missing task_request in context returns error", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		info := &relaycommon.RelayInfo{}
		_, err := a.BuildRequestBody(c, info)
		if err == nil || err.Error() != "request not found in context" {
			t.Fatalf("err = %v, want 'request not found in context'", err)
		}
	})

	t.Run("wrong type stored in context returns error", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		c.Set("task_request", "not-a-task-req")
		info := &relaycommon.RelayInfo{}
		_, err := a.BuildRequestBody(c, info)
		if err == nil || err.Error() != "invalid request type in context" {
			t.Fatalf("err = %v, want 'invalid request type in context'", err)
		}
	})

	t.Run("plain json request without multipart form builds payload", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_vgfm_t2v_l20", Prompt: "hello", Duration: 10})
		info := &relaycommon.RelayInfo{}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		data, _ := json.Marshal(struct {
			ReqKey string `json:"req_key"`
			Prompt string `json:"prompt"`
			Frames int    `json:"frames"`
		}{})
		_ = data
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r); err != nil {
			t.Fatalf("read built body: %v", err)
		}
		var got requestPayload
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal built body: %v", err)
		}
		if got.ReqKey != "jimeng_vgfm_t2v_l20" {
			t.Errorf("ReqKey = %q", got.ReqKey)
		}
		if got.Prompt != "hello" {
			t.Errorf("Prompt = %q", got.Prompt)
		}
		if got.Frames != 241 {
			t.Errorf("Frames = %d, want 241 for duration=10", got.Frames)
		}
	})

	t.Run("multipart single file sets Generate action and binary image", func(t *testing.T) {
		req := multipartRequest(t, map[string]string{"prompt": "p"}, "input_reference", map[string][]byte{
			"a.png": []byte("filedata"),
		})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_vgfm_t2v_l20", Prompt: "p"})
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Action != constant.TaskActionGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionGenerate)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r); err != nil {
			t.Fatalf("read built body: %v", err)
		}
		var got requestPayload
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal built body: %v", err)
		}
		if len(got.BinaryDataBase64) != 1 {
			t.Fatalf("BinaryDataBase64 len = %d, want 1", len(got.BinaryDataBase64))
		}
	})

	t.Run("multipart multiple files sets FirstTailGenerate action", func(t *testing.T) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		if err := w.WriteField("prompt", "p"); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
		for _, name := range []string{"a.png", "b.png"} {
			fw, err := w.CreateFormFile("input_reference", name)
			if err != nil {
				t.Fatalf("CreateFormFile: %v", err)
			}
			if _, err := fw.Write([]byte("data-" + name)); err != nil {
				t.Fatalf("write file content: %v", err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close multipart writer: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/videos", &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		wr := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(wr)
		c.Request = req
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_vgfm_t2v_l20", Prompt: "p"})
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		_, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Action != constant.TaskActionFirstTailGenerate {
			t.Errorf("Action = %q, want %q", info.Action, constant.TaskActionFirstTailGenerate)
		}
	})

	t.Run("multipart oversized file returns error", func(t *testing.T) {
		oversized := make([]byte, MaxFileSize+1)
		req := multipartRequest(t, map[string]string{"prompt": "p"}, "input_reference", map[string][]byte{
			"big.png": oversized,
		})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_vgfm_t2v_l20", Prompt: "p"})
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		_, err := a.BuildRequestBody(c, info)
		if err == nil {
			t.Fatal("expected error for oversized file")
		}
		if !strings.Contains(err.Error(), "大小超过限制") {
			t.Errorf("err = %v, want message containing 大小超过限制", err)
		}
	})

	t.Run("jimeng_v30 pro reqkey conversion with no images", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_v30_pro", Prompt: "p"})
		info := &relaycommon.RelayInfo{}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r); err != nil {
			t.Fatalf("read built body: %v", err)
		}
		var got requestPayload
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal built body: %v", err)
		}
		if got.ReqKey != "jimeng_ti2v_v30_pro" {
			t.Errorf("ReqKey = %q, want jimeng_ti2v_v30_pro", got.ReqKey)
		}
	})

	t.Run("jimeng_v30 reqkey conversion with one image (image-to-video)", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_v30p", Prompt: "p", Images: []string{"https://x.com/a.png"}})
		info := &relaycommon.RelayInfo{}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r); err != nil {
			t.Fatalf("read built body: %v", err)
		}
		var got requestPayload
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal built body: %v", err)
		}
		if got.ReqKey != "jimeng_i2v_first_v30" {
			t.Errorf("ReqKey = %q, want jimeng_i2v_first_v30", got.ReqKey)
		}
		if len(got.ImageUrls) != 1 {
			t.Errorf("ImageUrls len = %d, want 1", len(got.ImageUrls))
		}
	})

	t.Run("jimeng_v30 reqkey conversion with multiple images (first-tail)", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_v30p", Prompt: "p", Images: []string{"a-base64", "b-base64"}})
		info := &relaycommon.RelayInfo{}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r); err != nil {
			t.Fatalf("read built body: %v", err)
		}
		var got requestPayload
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal built body: %v", err)
		}
		if got.ReqKey != "jimeng_i2v_first_tail_v30" {
			t.Errorf("ReqKey = %q, want jimeng_i2v_first_tail_v30", got.ReqKey)
		}
		if len(got.BinaryDataBase64) != 2 {
			t.Errorf("BinaryDataBase64 len = %d, want 2", len(got.BinaryDataBase64))
		}
	})

	t.Run("jimeng_v30 reqkey conversion with no images (text-to-video)", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_v30p", Prompt: "p"})
		info := &relaycommon.RelayInfo{}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r); err != nil {
			t.Fatalf("read built body: %v", err)
		}
		var got requestPayload
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal built body: %v", err)
		}
		if got.ReqKey != "jimeng_t2v_v30p" {
			t.Errorf("ReqKey = %q, want jimeng_t2v_v30p", got.ReqKey)
		}
	})

	t.Run("duration other than 10 defaults to 121 frames", func(t *testing.T) {
		c, _ := newGinContext(http.MethodPost, "/", nil, "")
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "jimeng_vgfm_t2v_l20", Prompt: "p", Duration: 5})
		info := &relaycommon.RelayInfo{}
		r, err := a.BuildRequestBody(c, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r); err != nil {
			t.Fatalf("read built body: %v", err)
		}
		var got requestPayload
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal built body: %v", err)
		}
		if got.Frames != 121 {
			t.Errorf("Frames = %d, want 121", got.Frames)
		}
	})
}

// ---------------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------------

func TestDoResponse(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("success response returns task id and writes openai video JSON", func(t *testing.T) {
		body := `{"code":10000,"message":"success","request_id":"req-1","data":{"task_id":"task-123"}}`
		resp := &http.Response{
			Body: io_NopCloser(body),
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		info := &relaycommon.RelayInfo{OriginModelName: "jimeng-video"}
		taskID, taskData, taskErr := a.DoResponse(c, resp, info)
		if taskErr != nil {
			t.Fatalf("unexpected task error: %+v", taskErr)
		}
		if taskID != "task-123" {
			t.Errorf("taskID = %q, want task-123", taskID)
		}
		if string(taskData) != body {
			t.Errorf("taskData = %q, want %q", string(taskData), body)
		}
		if w.Code != http.StatusOK {
			t.Errorf("status code = %d, want 200", w.Code)
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
			t.Errorf("ov = %+v, want ID/TaskID = task-123", ov)
		}
		if ov.Model != "jimeng-video" {
			t.Errorf("Model = %q, want jimeng-video", ov.Model)
		}
		if ov.Object != "video" {
			t.Errorf("Object = %q, want video", ov.Object)
		}
	})

	t.Run("non-10000 code returns task error with code and message", func(t *testing.T) {
		body := `{"code":50429,"message":"rate limited","request_id":"req-2"}`
		resp := &http.Response{Body: io_NopCloser(body)}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		info := &relaycommon.RelayInfo{}
		_, _, taskErr := a.DoResponse(c, resp, info)
		if taskErr == nil {
			t.Fatal("expected task error for non-10000 code")
		}
		if taskErr.Code != "50429" {
			t.Errorf("taskErr.Code = %q, want 50429", taskErr.Code)
		}
	})

	t.Run("invalid json body returns unmarshal task error", func(t *testing.T) {
		resp := &http.Response{Body: io_NopCloser("{not json")}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		info := &relaycommon.RelayInfo{}
		_, _, taskErr := a.DoResponse(c, resp, info)
		if taskErr == nil {
			t.Fatal("expected task error for invalid json body")
		}
		if taskErr.Code != "unmarshal_response_body_failed" {
			t.Errorf("taskErr.Code = %q, want unmarshal_response_body_failed", taskErr.Code)
		}
	})
}

// io_NopCloser wraps a string body into an io.ReadCloser for http.Response.Body.
func io_NopCloser(s string) *stringReadCloser {
	return &stringReadCloser{Reader: strings.NewReader(s)}
}

type stringReadCloser struct {
	*strings.Reader
}

func (s *stringReadCloser) Close() error { return nil }

// ---------------------------------------------------------------------------
// FetchTask
// ---------------------------------------------------------------------------

func TestFetchTask(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://visual.volcengineapi.com"}

	t.Run("missing task_id returns error before any network call", func(t *testing.T) {
		resp, err := a.FetchTask("https://visual.volcengineapi.com", "sk-abc", map[string]any{}, "")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err == nil || err.Error() != "invalid task_id" {
			t.Fatalf("err = %v, want 'invalid task_id'", err)
		}
	})

	t.Run("non-newapi key with malformed ak|sk format returns error before signing", func(t *testing.T) {
		resp, err := a.FetchTask("https://visual.volcengineapi.com", "not-a-valid-key", map[string]any{"task_id": "t1"}, "")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err == nil {
			t.Fatal("expected error for malformed api key")
		}
		if !strings.Contains(err.Error(), "invalid api key format for jimeng") {
			t.Errorf("err = %v, want message about invalid api key format", err)
		}
	})

	t.Run("newapi relay success round-trips against local test server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/jimeng/" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer sk-abc" {
				t.Errorf("Authorization = %q, want Bearer sk-abc", got)
			}
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			if payload["task_id"] != "t-99" {
				t.Errorf("task_id in payload = %q, want t-99", payload["task_id"])
			}
			if payload["req_key"] != "jimeng_vgfm_t2v_l20" {
				t.Errorf("req_key in payload = %q, want jimeng_vgfm_t2v_l20", payload["req_key"])
			}
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"code":10000}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		}))
		defer srv.Close()

		local := &TaskAdaptor{baseURL: srv.URL}
		resp, err := local.FetchTask(srv.URL, "sk-abc", map[string]any{"task_id": "t-99"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("direct signed key success round-trips against local test server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				t.Error("expected Authorization header to be set by signRequest")
			}
			if r.Header.Get("X-Date") == "" {
				t.Error("expected X-Date header to be set")
			}
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"code":10000}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		}))
		defer srv.Close()

		local := &TaskAdaptor{baseURL: srv.URL}
		resp, err := local.FetchTask(srv.URL, "AK1|SK1", map[string]any{"task_id": "t-1"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
	})
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}
	models := a.GetModelList()
	if len(models) != 1 || models[0] != "jimeng_vgfm_t2v_l20" {
		t.Errorf("GetModelList() = %v, want [jimeng_vgfm_t2v_l20]", models)
	}
	if got := a.GetChannelName(); got != "jimeng" {
		t.Errorf("GetChannelName() = %q, want jimeng", got)
	}
}

// ---------------------------------------------------------------------------
// ParseTaskResult
// ---------------------------------------------------------------------------

func TestParseTaskResult(t *testing.T) {
	a := &TaskAdaptor{}

	cases := []struct {
		name         string
		body         string
		wantCode     int
		wantStatus   string
		wantProgress string
		wantURL      string
		wantReason   string
	}{
		{
			name:         "success code and done status",
			body:         `{"code":10000,"data":{"status":"done","video_url":"https://cdn/video.mp4"}}`,
			wantCode:     0,
			wantStatus:   string(repo.TaskStatusSuccess),
			wantProgress: "100%",
			wantURL:      "https://cdn/video.mp4",
		},
		{
			name:         "in_queue status",
			body:         `{"code":10000,"data":{"status":"in_queue"}}`,
			wantCode:     0,
			wantStatus:   string(repo.TaskStatusQueued),
			wantProgress: "10%",
		},
		{
			name:         "failure code sets failure status and reason",
			body:         `{"code":50500,"message":"internal error","data":{"status":"unknown_status"}}`,
			wantCode:     50500,
			wantStatus:   string(repo.TaskStatusFailure),
			wantProgress: "100%",
			wantReason:   "internal error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := a.ParseTaskResult([]byte(tc.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d", info.Code, tc.wantCode)
			}
			if info.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", info.Status, tc.wantStatus)
			}
			if info.Progress != tc.wantProgress {
				t.Errorf("Progress = %q, want %q", info.Progress, tc.wantProgress)
			}
			if info.Url != tc.wantURL {
				t.Errorf("Url = %q, want %q", info.Url, tc.wantURL)
			}
			if info.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", info.Reason, tc.wantReason)
			}
		})
	}

	t.Run("invalid json returns error", func(t *testing.T) {
		_, err := a.ParseTaskResult([]byte("{not json"))
		if err == nil {
			t.Fatal("expected error for invalid json")
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertToOpenAIVideo
// ---------------------------------------------------------------------------

func TestConvertToOpenAIVideo(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("success task with no upstream error", func(t *testing.T) {
		data := []byte(`{"code":10000,"data":{"video_url":"https://cdn/video.mp4"}}`)
		task := &repo.Task{
			TaskID:    "task-1",
			Status:    repo.TaskStatusSuccess,
			Progress:  "100%",
			Data:      data,
			CreatedAt: 1000,
			UpdatedAt: 2000,
		}
		out, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov struct {
			ID          string         `json:"id"`
			Status      string         `json:"status"`
			Progress    int            `json:"progress"`
			CreatedAt   int64          `json:"created_at"`
			CompletedAt int64          `json:"completed_at"`
			Metadata    map[string]any `json:"metadata"`
			Error       *struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out, &ov); err != nil {
			t.Fatalf("unmarshal converted video: %v", err)
		}
		if ov.ID != "task-1" {
			t.Errorf("ID = %q, want task-1", ov.ID)
		}
		if ov.Status != "completed" {
			t.Errorf("Status = %q, want completed", ov.Status)
		}
		if ov.Progress != 100 {
			t.Errorf("Progress = %d, want 100", ov.Progress)
		}
		if ov.CreatedAt != 1000 || ov.CompletedAt != 2000 {
			t.Errorf("CreatedAt/CompletedAt = %d/%d, want 1000/2000", ov.CreatedAt, ov.CompletedAt)
		}
		if ov.Metadata["url"] != "https://cdn/video.mp4" {
			t.Errorf("Metadata[url] = %v, want https://cdn/video.mp4", ov.Metadata["url"])
		}
		if ov.Error != nil {
			t.Errorf("Error = %+v, want nil", ov.Error)
		}
	})

	t.Run("failure task sets error field", func(t *testing.T) {
		data := []byte(`{"code":50500,"message":"boom"}`)
		task := &repo.Task{
			TaskID: "task-2",
			Status: repo.TaskStatusFailure,
			Data:   data,
		}
		out, err := a.ConvertToOpenAIVideo(task)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var ov struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out, &ov); err != nil {
			t.Fatalf("unmarshal converted video: %v", err)
		}
		if ov.Status != "failed" {
			t.Errorf("Status = %q, want failed", ov.Status)
		}
		if ov.Error == nil {
			t.Fatal("expected Error to be set")
		}
		if ov.Error.Message != "boom" {
			t.Errorf("Error.Message = %q, want boom", ov.Error.Message)
		}
		if ov.Error.Code != "50500" {
			t.Errorf("Error.Code = %q, want 50500", ov.Error.Code)
		}
	})

	t.Run("invalid task data returns error", func(t *testing.T) {
		task := &repo.Task{TaskID: "task-3", Data: []byte("{not json")}
		_, err := a.ConvertToOpenAIVideo(task)
		if err == nil {
			t.Fatal("expected error for invalid task data")
		}
	})
}
