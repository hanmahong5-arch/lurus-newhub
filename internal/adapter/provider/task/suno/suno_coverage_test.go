package suno

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
	// Multipart/JSON request bodies built in this file are tiny in-memory
	// fixtures; disable the size cap so UnmarshalBodyReusable doesn't reject
	// them (production sets this from env/config, which is 0 by default in
	// a bare test binary).
	pkgconstant.MaxRequestBodyMB = -1
	// FetchTask calls app.GetHttpClientWithProxy(""), which returns the
	// package-level httpClient singleton; without this it is nil in a bare
	// test binary and Client.Do panics before any I/O is attempted.
	app.InitHttpClient()
}

func newTestContext(t *testing.T, method, path, body, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, w
}

// ---------------------------------------------------------------------
// constants / metadata
// ---------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &TaskAdaptor{}

	want := []string{"suno_music", "suno_lyrics"}
	got := a.GetModelList()
	if len(got) != len(want) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], w)
		}
	}

	if name := a.GetChannelName(); name != "suno" {
		t.Errorf("GetChannelName() = %q, want %q", name, "suno")
	}
}

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 42}})
	if a.ChannelType != 42 {
		t.Errorf("Init() did not set ChannelType, got %d, want 42", a.ChannelType)
	}
}

func TestParseTaskResult(t *testing.T) {
	a := &TaskAdaptor{}
	info, err := a.ParseTaskResult([]byte(`{}`))
	if info != nil {
		t.Errorf("ParseTaskResult() info = %v, want nil", info)
	}
	if err == nil || err.Error() != "not implement" {
		t.Errorf("ParseTaskResult() err = %v, want %q", err, "not implement")
	}
}

// ---------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		want string
	}{
		{
			name: "music action",
			info: &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://upstream.example"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: "MUSIC"}},
			want: "https://upstream.example/suno/submit/MUSIC",
		},
		{
			name: "lyrics action, no trailing slash on base",
			info: &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.suno.ai"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: "LYRICS"}},
			want: "https://api.suno.ai/suno/submit/LYRICS",
		},
		{
			name: "empty base url",
			info: &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ""}, TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: "MUSIC"}},
			want: "/suno/submit/MUSIC",
		},
	}

	a := &TaskAdaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.BuildRequestURL(tt.info)
			if err != nil {
				t.Fatalf("BuildRequestURL() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("BuildRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// BuildRequestHeader
// ---------------------------------------------------------------------

func TestBuildRequestHeader(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", "", "")
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "application/json")

	req, err := http.NewRequest(http.MethodPost, "http://upstream/suno/submit/MUSIC", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test-key"}}
	if err := a.BuildRequestHeader(c, req, info); err != nil {
		t.Fatalf("BuildRequestHeader() error = %v, want nil", err)
	}

	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want %q", got, "application/json")
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-test-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test-key")
	}
}

// ---------------------------------------------------------------------
// BuildRequestBody
// ---------------------------------------------------------------------

func TestBuildRequestBody_FromContextTaskRequest(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", "", "")
	sunoReq := &dto.SunoSubmitReq{Prompt: "a happy song", Mv: "chirp-v3-0"}
	c.Set("task_request", sunoReq)

	a := &TaskAdaptor{}
	r, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("BuildRequestBody() error = %v, want nil", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}

	var got dto.SunoSubmitReq
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Prompt != "a happy song" || got.Mv != "chirp-v3-0" {
		t.Errorf("BuildRequestBody() roundtrip = %+v, want Prompt=%q Mv=%q", got, "a happy song", "chirp-v3-0")
	}
}

func TestBuildRequestBody_FallbackUnmarshalFromRawBody(t *testing.T) {
	body := `{"prompt":"raw body prompt","mv":"chirp-v2-0"}`
	c, _ := newTestContext(t, http.MethodPost, "/x", body, "application/json")

	a := &TaskAdaptor{}
	r, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err != nil {
		t.Fatalf("BuildRequestBody() error = %v, want nil", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}

	var got dto.SunoSubmitReq
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Prompt != "raw body prompt" || got.Mv != "chirp-v2-0" {
		t.Errorf("BuildRequestBody() fallback roundtrip = %+v, want Prompt=%q Mv=%q", got, "raw body prompt", "chirp-v2-0")
	}
}

func TestBuildRequestBody_FallbackUnmarshalError(t *testing.T) {
	body := `not json at all`
	c, _ := newTestContext(t, http.MethodPost, "/x", body, "application/json")

	a := &TaskAdaptor{}
	r, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
	if err == nil {
		t.Fatalf("BuildRequestBody() error = nil, want non-nil for invalid JSON")
	}
	if r != nil {
		t.Errorf("BuildRequestBody() reader = %v, want nil on error", r)
	}
}

// ---------------------------------------------------------------------
// ValidateRequestAndSetAction / actionValidate
// ---------------------------------------------------------------------

func TestValidateRequestAndSetAction_InvalidBody(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", "not json", "application/json")
	c.Params = gin.Params{{Key: "action", Value: "music"}}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr == nil {
		t.Fatal("ValidateRequestAndSetAction() taskErr = nil, want non-nil for invalid JSON body")
	}
	if taskErr.Code != "invalid_request" {
		t.Errorf("taskErr.Code = %q, want %q", taskErr.Code, "invalid_request")
	}
	if taskErr.StatusCode != http.StatusBadRequest {
		t.Errorf("taskErr.StatusCode = %d, want %d", taskErr.StatusCode, http.StatusBadRequest)
	}
	if !taskErr.LocalError {
		t.Errorf("taskErr.LocalError = false, want true")
	}
}

func TestValidateRequestAndSetAction_InvalidAction(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", `{}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "bogus"}}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr == nil {
		t.Fatal("ValidateRequestAndSetAction() taskErr = nil, want non-nil for invalid action")
	}
	if taskErr.Message != "invalid_action" {
		t.Errorf("taskErr.Message = %q, want %q", taskErr.Message, "invalid_action")
	}
}

func TestValidateRequestAndSetAction_LyricsEmptyPrompt(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", `{"prompt":""}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "lyrics"}}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr == nil {
		t.Fatal("ValidateRequestAndSetAction() taskErr = nil, want non-nil for empty lyrics prompt")
	}
	if taskErr.Message != "prompt_empty" {
		t.Errorf("taskErr.Message = %q, want %q", taskErr.Message, "prompt_empty")
	}
}

func TestValidateRequestAndSetAction_LyricsValidPrompt(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", `{"prompt":"write me a chorus"}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "lyrics"}}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() taskErr = %+v, want nil", taskErr)
	}
	if info.Action != "LYRICS" {
		t.Errorf("info.Action = %q, want %q", info.Action, "LYRICS")
	}
	stored, ok := c.Get("task_request")
	if !ok {
		t.Fatal(`c.Get("task_request") ok = false, want true`)
	}
	sunoReq, ok := stored.(*dto.SunoSubmitReq)
	if !ok {
		t.Fatalf("stored task_request type = %T, want *dto.SunoSubmitReq", stored)
	}
	if sunoReq.Prompt != "write me a chorus" {
		t.Errorf("sunoReq.Prompt = %q, want %q", sunoReq.Prompt, "write me a chorus")
	}
}

func TestValidateRequestAndSetAction_MusicDefaultsMv(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", `{"prompt":"a song"}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "music"}}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() taskErr = %+v, want nil", taskErr)
	}
	stored, _ := c.Get("task_request")
	sunoReq := stored.(*dto.SunoSubmitReq)
	if sunoReq.Mv != "chirp-v3-0" {
		t.Errorf("sunoReq.Mv = %q, want default %q", sunoReq.Mv, "chirp-v3-0")
	}
	if info.Action != "MUSIC" {
		t.Errorf("info.Action = %q, want %q", info.Action, "MUSIC")
	}
}

func TestValidateRequestAndSetAction_MusicKeepsExplicitMv(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", `{"prompt":"a song","mv":"chirp-v4-0"}`, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "music"}}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() taskErr = %+v, want nil", taskErr)
	}
	stored, _ := c.Get("task_request")
	sunoReq := stored.(*dto.SunoSubmitReq)
	if sunoReq.Mv != "chirp-v4-0" {
		t.Errorf("sunoReq.Mv = %q, want explicit %q", sunoReq.Mv, "chirp-v4-0")
	}
}

func TestValidateRequestAndSetAction_ContinueClipIdWithoutTaskID(t *testing.T) {
	body := `{"prompt":"a song","continue_clip_id":"clip-123"}`
	c, _ := newTestContext(t, http.MethodPost, "/x", body, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "music"}}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr == nil {
		t.Fatal("ValidateRequestAndSetAction() taskErr = nil, want non-nil when continue_clip_id set without task_id")
	}
	if taskErr.Message != "task id is empty" {
		t.Errorf("taskErr.Message = %q, want %q", taskErr.Message, "task id is empty")
	}
}

func TestValidateRequestAndSetAction_ContinueClipIdWithTaskID(t *testing.T) {
	body := `{"prompt":"a song","continue_clip_id":"clip-123","task_id":"task-abc"}`
	c, _ := newTestContext(t, http.MethodPost, "/x", body, "application/json")
	c.Params = gin.Params{{Key: "action", Value: "music"}}

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() taskErr = %+v, want nil", taskErr)
	}
	if info.OriginTaskID != "task-abc" {
		t.Errorf("info.OriginTaskID = %q, want %q", info.OriginTaskID, "task-abc")
	}
}

// ---------------------------------------------------------------------
// DoResponse
// ---------------------------------------------------------------------

func newHTTPResponse(t *testing.T, body string, statusCode int) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"X-Upstream": []string{"suno"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestDoResponse_ReadBodyError(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", "", "")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(&erroringReader{}),
	}

	a := &TaskAdaptor{}
	taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskID != "" {
		t.Errorf("taskID = %q, want empty", taskID)
	}
	if taskData != nil {
		t.Errorf("taskData = %v, want nil", taskData)
	}
	if taskErr == nil {
		t.Fatal("taskErr = nil, want non-nil on read error")
	}
	if taskErr.Code != "read_response_body_failed" {
		t.Errorf("taskErr.Code = %q, want %q", taskErr.Code, "read_response_body_failed")
	}
	if taskErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("taskErr.StatusCode = %d, want %d", taskErr.StatusCode, http.StatusInternalServerError)
	}
}

type erroringReader struct{}

func (e *erroringReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestDoResponse_UnmarshalError(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", "", "")
	resp := newHTTPResponse(t, "not json", http.StatusOK)
	defer func() { _ = resp.Body.Close() }()

	a := &TaskAdaptor{}
	taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskID != "" {
		t.Errorf("taskID = %q, want empty", taskID)
	}
	if taskData != nil {
		t.Errorf("taskData = %v, want nil", taskData)
	}
	if taskErr == nil {
		t.Fatal("taskErr = nil, want non-nil on invalid json")
	}
	if taskErr.Code != "unmarshal_response_body_failed" {
		t.Errorf("taskErr.Code = %q, want %q", taskErr.Code, "unmarshal_response_body_failed")
	}
}

func TestDoResponse_UpstreamFailureCode(t *testing.T) {
	c, _ := newTestContext(t, http.MethodPost, "/x", "", "")
	resp := newHTTPResponse(t, `{"code":"failed","message":"upstream rejected the task"}`, http.StatusOK)
	defer func() { _ = resp.Body.Close() }()

	a := &TaskAdaptor{}
	taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskID != "" {
		t.Errorf("taskID = %q, want empty", taskID)
	}
	if taskData != nil {
		t.Errorf("taskData = %v, want nil", taskData)
	}
	if taskErr == nil {
		t.Fatal("taskErr = nil, want non-nil on non-success code")
	}
	if taskErr.Code != "failed" {
		t.Errorf("taskErr.Code = %q, want %q", taskErr.Code, "failed")
	}
	if taskErr.Message != "upstream rejected the task" {
		t.Errorf("taskErr.Message = %q, want %q", taskErr.Message, "upstream rejected the task")
	}
}

func TestDoResponse_Success(t *testing.T) {
	c, w := newTestContext(t, http.MethodPost, "/x", "", "")
	resp := newHTTPResponse(t, `{"code":"success","message":"ok","data":"task-xyz"}`, http.StatusCreated)
	defer func() { _ = resp.Body.Close() }()

	a := &TaskAdaptor{}
	taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr != nil {
		t.Fatalf("taskErr = %+v, want nil", taskErr)
	}
	if taskID != "task-xyz" {
		t.Errorf("taskID = %q, want %q", taskID, "task-xyz")
	}
	if taskData != nil {
		t.Errorf("taskData = %v, want nil", taskData)
	}
	if w.Code != http.StatusCreated {
		t.Errorf("recorder status = %d, want %d", w.Code, http.StatusCreated)
	}
	if got := w.Header().Get("X-Upstream"); got != "suno" {
		t.Errorf("propagated header X-Upstream = %q, want %q", got, "suno")
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"code":"success","message":"ok","data":"task-xyz"}` {
		t.Errorf("written body = %q, want the raw upstream body echoed back", got)
	}
}

// ---------------------------------------------------------------------
// FetchTask (network guard branches only; no live I/O)
// ---------------------------------------------------------------------

func TestFetchTask_MarshalAndURLConstruction(t *testing.T) {
	// FetchTask dials out over HTTP; we only exercise the pure request
	// construction path by pointing it at an unroutable address and
	// asserting it fails fast without panicking (the live upstream call
	// itself is out of hermetic scope).
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("http://127.0.0.1:0", "test-key", map[string]any{"ids": []string{"t1"}}, "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
		t.Errorf("FetchTask() resp = %v, want nil for unroutable address", resp)
	}
	if err == nil {
		t.Fatal("FetchTask() err = nil, want non-nil for unroutable address")
	}
}

func TestFetchTask_MarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	// A channel value cannot be marshaled to JSON, forcing json.Marshal to
	// fail before any network I/O is attempted.
	body := map[string]any{"bad": make(chan int)}
	resp, err := a.FetchTask("http://example.com", "test-key", body, "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
		t.Errorf("FetchTask() resp = %v, want nil on marshal error", resp)
	}
	if err == nil {
		t.Fatal("FetchTask() err = nil, want non-nil on marshal error")
	}
}
