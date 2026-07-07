package music

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	// GetRequestBody rejects any body once MaxRequestBodyMB <= 0 (its default
	// zero value outside of the app's init path); set an explicit limit so
	// hermetic gin.Context requests in this file are accepted.
	constant.MaxRequestBodyMB = 10
	os.Exit(m.Run())
}

func newMusicGinContext(t *testing.T, body string, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/music", bytes.NewBufferString(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, w
}

func TestModelListAndChannelName(t *testing.T) {
	models := (&TaskAdaptor{}).GetModelList()
	want := []string{"suno-v4", "suno-v3.5", "udio-v1", "music_generate"}
	if len(models) != len(want) {
		t.Fatalf("expected %d models, got %d", len(want), len(models))
	}
	for i, m := range want {
		if models[i] != m {
			t.Errorf("model[%d] = %q, want %q", i, models[i], m)
		}
	}
	if name := (&TaskAdaptor{}).GetChannelName(); name != "music" {
		t.Errorf("GetChannelName() = %q, want music", name)
	}
}

func TestInit(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 42}})
	if a.ChannelType != 42 {
		t.Errorf("ChannelType = %d, want 42", a.ChannelType)
	}
}

func TestBuildRequestURL(t *testing.T) {
	a := &TaskAdaptor{}
	url, err := a.BuildRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://upstream.example.com"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://upstream.example.com/suno/submit/music" {
		t.Errorf("BuildRequestURL() = %q", url)
	}
}

func TestBuildRequestHeader(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newMusicGinContext(t, "", "")
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	if err := a.BuildRequestHeader(c, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestValidateRequestAndSetAction(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantErr    bool
		wantCode   string
		wantStatus int
	}{
		{
			name:    "valid request",
			body:    `{"model":"suno-v4","prompt":"a happy tune"}`,
			wantErr: false,
		},
		{
			name:       "empty prompt",
			body:       `{"model":"suno-v4","prompt":""}`,
			wantErr:    true,
			wantCode:   "invalid_request",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid json",
			body:       `{invalid`,
			wantErr:    true,
			wantCode:   "invalid_request",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			c, _ := newMusicGinContext(t, tt.body, "application/json")
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			taskErr := a.ValidateRequestAndSetAction(c, info)
			if tt.wantErr {
				if taskErr == nil {
					t.Fatalf("expected error, got nil")
				}
				if taskErr.Code != tt.wantCode {
					t.Errorf("Code = %q, want %q", taskErr.Code, tt.wantCode)
				}
				if taskErr.StatusCode != tt.wantStatus {
					t.Errorf("StatusCode = %d, want %d", taskErr.StatusCode, tt.wantStatus)
				}
				if !taskErr.LocalError {
					t.Errorf("LocalError = false, want true")
				}
				return
			}
			if taskErr != nil {
				t.Fatalf("unexpected error: %+v", taskErr)
			}
			if info.Action != "MUSIC" {
				t.Errorf("Action = %q, want MUSIC", info.Action)
			}
			raw, ok := c.Get("music_request")
			if !ok {
				t.Fatalf("music_request not set in context")
			}
			req, ok := raw.(*dto.MusicSubmitReq)
			if !ok {
				t.Fatalf("music_request has wrong type: %T", raw)
			}
			if req.Prompt != "a happy tune" {
				t.Errorf("Prompt = %q", req.Prompt)
			}
		})
	}
}

func TestBuildRequestBody(t *testing.T) {
	tests := []struct {
		name         string
		req          *dto.MusicSubmitReq
		wantPrompt   string
		wantMv       string
		wantInstr    bool
		noReqInCtx   bool
		expectErrMsg string
	}{
		{
			name:       "no music_request in context",
			noReqInCtx: true,
		},
		{
			name:       "model suno-v4 with style",
			req:        &dto.MusicSubmitReq{Model: "suno-v4", Prompt: "a song", Style: "jazz", Instrumental: true},
			wantPrompt: "[Style: jazz] a song",
			wantMv:     "chirp-v4",
			wantInstr:  true,
		},
		{
			name:       "model suno-v3.5 no style",
			req:        &dto.MusicSubmitReq{Model: "suno-v3.5", Prompt: "another song"},
			wantPrompt: "another song",
			wantMv:     "chirp-v3-5",
			wantInstr:  false,
		},
		{
			name:       "unknown model defaults to chirp-v3-0",
			req:        &dto.MusicSubmitReq{Model: "unknown-model", Prompt: "third song"},
			wantPrompt: "third song",
			wantMv:     "chirp-v3-0",
			wantInstr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			c, _ := newMusicGinContext(t, "", "")
			if !tt.noReqInCtx {
				c.Set("music_request", tt.req)
			}

			r, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{})
			if tt.noReqInCtx {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if err.Error() != "music request not found in context" {
					t.Errorf("err = %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			data, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var sunoReq dto.SunoSubmitReq
			if err := json.Unmarshal(data, &sunoReq); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if sunoReq.GptDescriptionPrompt != tt.wantPrompt {
				t.Errorf("GptDescriptionPrompt = %q, want %q", sunoReq.GptDescriptionPrompt, tt.wantPrompt)
			}
			if sunoReq.Mv != tt.wantMv {
				t.Errorf("Mv = %q, want %q", sunoReq.Mv, tt.wantMv)
			}
			if sunoReq.MakeInstrumental != tt.wantInstr {
				t.Errorf("MakeInstrumental = %v, want %v", sunoReq.MakeInstrumental, tt.wantInstr)
			}
		})
	}
}

func TestDoResponse(t *testing.T) {
	tests := []struct {
		name         string
		respBody     string
		statusCode   int
		wantTaskID   string
		wantErr      bool
		wantErrCode  string
		wantErrLocal bool
	}{
		{
			name:       "success",
			respBody:   `{"code":"success","message":"","data":"task-123"}`,
			statusCode: http.StatusOK,
			wantTaskID: "task-123",
		},
		{
			name:        "upstream failure code",
			respBody:    `{"code":"error","message":"quota exceeded","data":""}`,
			statusCode:  http.StatusOK,
			wantErr:     true,
			wantErrCode: "error",
		},
		{
			name:        "invalid json",
			respBody:    `{not-json`,
			statusCode:  http.StatusOK,
			wantErr:     true,
			wantErrCode: "unmarshal_response_body_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			c, w := newMusicGinContext(t, "", "")
			upstreamResp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(bytes.NewBufferString(tt.respBody)),
			}
			taskID, taskData, taskErr := a.DoResponse(c, upstreamResp, &relaycommon.RelayInfo{})
			if tt.wantErr {
				if taskErr == nil {
					t.Fatalf("expected error, got nil")
				}
				if taskErr.Code != tt.wantErrCode {
					t.Errorf("Code = %q, want %q", taskErr.Code, tt.wantErrCode)
				}
				return
			}
			if taskErr != nil {
				t.Fatalf("unexpected error: %+v", taskErr)
			}
			if taskID != tt.wantTaskID {
				t.Errorf("taskID = %q, want %q", taskID, tt.wantTaskID)
			}
			if taskData != nil {
				t.Errorf("taskData = %v, want nil", taskData)
			}
			if w.Code != http.StatusOK {
				t.Errorf("recorder status = %d, want 200", w.Code)
			}
			var out map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("unmarshal recorder body: %v", err)
			}
			if out["task_id"] != tt.wantTaskID {
				t.Errorf("body task_id = %v, want %v", out["task_id"], tt.wantTaskID)
			}
			if out["status"] != "queued" {
				t.Errorf("body status = %v, want queued", out["status"])
			}
		})
	}
}

// errReadCloser always fails on Read, to exercise the io.ReadAll error branch
// of DoResponse.
type errReadCloser struct{}

func (errReadCloser) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReadCloser) Close() error                { return nil }

func TestDoResponseReadError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := newMusicGinContext(t, "", "")
	resp := &http.Response{StatusCode: http.StatusOK, Body: errReadCloser{}}
	taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatalf("expected error, got nil")
	}
	if taskErr.Code != "read_response_body_failed" {
		t.Errorf("Code = %q, want read_response_body_failed", taskErr.Code)
	}
	if taskID != "" || taskData != nil {
		t.Errorf("expected zero-value returns, got taskID=%q taskData=%v", taskID, taskData)
	}
}

func TestParseTaskResult(t *testing.T) {
	tests := []struct {
		name         string
		respBody     string
		wantErr      bool
		wantStatus   string
		wantProgress string
		wantURL      string
	}{
		{
			name:     "invalid json",
			respBody: `{not-json`,
			wantErr:  true,
		},
		{
			name:     "data not a map",
			respBody: `{"code":"success","message":"","data":"just-a-string"}`,
		},
		{
			name:         "full status/progress/audio url",
			respBody:     `{"code":"success","message":"","data":{"status":"processing","progress":"50%","data":{"audio_url":"https://cdn.example.com/song.mp3"}}}`,
			wantStatus:   "processing",
			wantProgress: "50%",
			wantURL:      "https://cdn.example.com/song.mp3",
		},
		{
			name:       "empty audio url is ignored",
			respBody:   `{"code":"success","message":"","data":{"status":"queued","data":{"audio_url":""}}}`,
			wantStatus: "queued",
		},
		{
			name:     "nested data not a map",
			respBody: `{"code":"success","message":"","data":{"status":"failed","data":"oops"}}`,
			wantStatus: "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &TaskAdaptor{}
			ti, err := a.ParseTaskResult([]byte(tt.respBody))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if ti != nil {
					t.Errorf("expected nil TaskInfo on error, got %+v", ti)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ti.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", ti.Status, tt.wantStatus)
			}
			if ti.Progress != tt.wantProgress {
				t.Errorf("Progress = %q, want %q", ti.Progress, tt.wantProgress)
			}
			if ti.Url != tt.wantURL {
				t.Errorf("Url = %q, want %q", ti.Url, tt.wantURL)
			}
		})
	}
}

// TestFetchTaskRequestConstructionError exercises FetchTask's hermetic error
// guard (invalid request URL from a malformed base URL) without performing
// any live network I/O.
func TestFetchTaskRequestConstructionError(t *testing.T) {
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("://bad-url", "key", map[string]any{"ids": []string{"1"}}, "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatalf("expected error for malformed base URL, got nil")
	}
}

// TestFetchTaskMarshalError exercises FetchTask's json.Marshal error guard
// (an un-marshalable body value) without performing any live network I/O.
func TestFetchTaskMarshalError(t *testing.T) {
	a := &TaskAdaptor{}
	body := map[string]any{"bad": make(chan int)}
	resp, err := a.FetchTask("https://upstream.example.com", "key", body, "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatalf("expected marshal error, got nil")
	}
}

// TestFetchTaskProxyClientError exercises FetchTask's proxy http client
// construction error guard without performing any live network I/O.
func TestFetchTaskProxyClientError(t *testing.T) {
	a := &TaskAdaptor{}
	body := map[string]any{"ids": []string{"1"}}
	resp, err := a.FetchTask("https://upstream.example.com", "key", body, "://bad-proxy")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatalf("expected proxy client error, got nil")
	}
	if got, want := err.Error()[:len("new proxy http client:")], "new proxy http client:"; got != want {
		t.Errorf("err = %q, want prefix %q", err.Error(), want)
	}
}
