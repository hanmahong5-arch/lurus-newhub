package gemini

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

// task_b_ensureMaxBodyMB guards against constant.MaxRequestBodyMB being unset
// (0) when this file's tests are the first in the package to exercise the
// UnmarshalBodyReusable request-body-size check.
func task_b_ensureMaxBodyMB() {
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
}

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
			ChannelType:    9,
			ChannelBaseUrl: "https://generativelanguage.googleapis.com",
			ApiKey:         "goog-secret",
		},
	}
	a.Init(info)
	if a.ChannelType != 9 {
		t.Errorf("ChannelType = %d, want 9", a.ChannelType)
	}
	if a.baseURL != "https://generativelanguage.googleapis.com" {
		t.Errorf("baseURL = %q", a.baseURL)
	}
	if a.apiKey != "goog-secret" {
		t.Errorf("apiKey = %q, want goog-secret", a.apiKey)
	}
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestTaskB_ValidateRequestAndSetAction_Success(t *testing.T) {
	task_b_ensureMaxBodyMB()
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"a river at sunset"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if info.Action != constant.TaskActionTextGenerate {
		t.Errorf("Action = %q, want textGenerate (gemini's default action)", info.Action)
	}
}

func TestTaskB_ValidateRequestAndSetAction_EmptyPromptRejected(t *testing.T) {
	task_b_ensureMaxBodyMB()
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"   "}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr == nil {
		t.Fatal("expected validation error for whitespace-only prompt")
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

	var gotMethod, gotPath, gotKey string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"operations/abc123"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{apiKey: "goog-secret", baseURL: srv.URL}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	info := &relaycommon.RelayInfo{
		OriginModelName: "veo-3.0-generate-001",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "goog-secret"},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"instances":[{"prompt":"a river"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1beta/models/veo-3.0-generate-001:predictLongRunning" {
		t.Errorf("upstream path = %q", gotPath)
	}
	if gotKey != "goog-secret" {
		t.Errorf("upstream x-goog-api-key = %q, want goog-secret", gotKey)
	}
	if !strings.Contains(string(gotBody), "a river") {
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
	localID := encodeLocalTaskID("operations/xyz")
	bcResp0, err := a.FetchTask("https://generativelanguage.googleapis.com", "goog-key", map[string]any{
		"task_id": localID,
	}, "://malformed-proxy")
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error for malformed channel proxy configuration")
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Errorf("error should mention proxy client setup failure, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// extractModelFromOperationName: manual fallback path when the fast-path
// regex fails to match (model segment itself contains extra slashes so the
// [^/]+ capture cannot bridge straight to "/operations/").
// ---------------------------------------------------------------------------

func TestTaskB_ExtractModelFromOperationName_FallbackPath(t *testing.T) {
	// Contains a nested "/" between the model segment and "/operations/", so
	// the regex's [^/]+ capture cannot bridge across it and the function must
	// fall back to the manual strings.Index scan.
	got := extractModelFromOperationName("models/foo/bar/operations/xyz")
	want := "foo/bar"
	if got != want {
		t.Errorf("extractModelFromOperationName fallback = %q, want %q", got, want)
	}
}
