package doubao

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// task_b_errReader is an io.ReadCloser whose Read always fails, simulating an
// upstream connection dropping mid-body-read.
type task_b_errReader struct{}

func (task_b_errReader) Read(p []byte) (int, error) { return 0, errors.New("connection reset by peer") }
func (task_b_errReader) Close() error                { return nil }

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestTaskB_Init_CapturesChannelMeta(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    12,
			ChannelBaseUrl: "https://ark.cn-beijing.volces.com",
			ApiKey:         "doubao-secret",
		},
	}
	a.Init(info)
	if a.ChannelType != 12 {
		t.Errorf("ChannelType = %d, want 12", a.ChannelType)
	}
	if a.baseURL != "https://ark.cn-beijing.volces.com" {
		t.Errorf("baseURL = %q", a.baseURL)
	}
	if a.apiKey != "doubao-secret" {
		t.Errorf("apiKey = %q, want doubao-secret", a.apiKey)
	}
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestTaskB_ValidateRequestAndSetAction_Success(t *testing.T) {
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"a mountain range","model":"doubao-seedance-1-0-lite-t2v"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if info.Action != constant.TaskActionGenerate {
		t.Errorf("Action = %q, want generate", info.Action)
	}
}

func TestTaskB_ValidateRequestAndSetAction_EmptyPromptRejected(t *testing.T) {
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":""}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr == nil {
		t.Fatal("expected validation error for empty prompt")
	}
	if taskErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", taskErr.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// DoRequest (full submit round trip against a stub upstream)
// ---------------------------------------------------------------------------

func TestTaskB_DoRequest_RoundTripsThroughUpstream(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()

	var gotMethod, gotPath, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"remote-1"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{apiKey: "doubao-secret", baseURL: srv.URL}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate},
		ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: "doubao-secret"},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"model":"doubao-seedance-1-0-lite-t2v","content":[{"type":"text","text":"hi"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v3/contents/generations/tasks" {
		t.Errorf("upstream path = %q", gotPath)
	}
	if gotAuth != "Bearer doubao-secret" {
		t.Errorf("upstream Authorization = %q, want Bearer doubao-secret", gotAuth)
	}
	if !strings.Contains(string(gotBody), "doubao-seedance-1-0-lite-t2v") {
		t.Errorf("upstream did not receive request body, got %q", gotBody)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: upstream body read failure
// ---------------------------------------------------------------------------

func TestTaskB_DoResponse_BodyReadError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	resp := &http.Response{Body: task_b_errReader{}}
	_, _, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected task error when upstream body read fails, must not panic")
	}
}

// ---------------------------------------------------------------------------
// FetchTask: invalid proxy configuration failure
// ---------------------------------------------------------------------------

func TestTaskB_FetchTask_InvalidProxyErrors(t *testing.T) {
	a := &TaskAdaptor{}
	_, err := a.FetchTask("https://ark.cn-beijing.volces.com", "doubao-key", map[string]any{
		"task_id": "t1",
	}, "://malformed-proxy")
	if err == nil {
		t.Fatal("expected error for malformed channel proxy configuration")
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Errorf("error should mention proxy client setup failure, got %v", err)
	}
}
