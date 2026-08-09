package kling

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
// UnmarshalBodyReusable request-body-size check, which otherwise rejects
// every body as "exceeds 0 MB".
func task_b_ensureMaxBodyMB() {
	if constant.MaxRequestBodyMB == 0 {
		constant.MaxRequestBodyMB = 10
	}
}

// task_b_errReader is an io.ReadCloser whose Read always fails, simulating an
// upstream connection dropping mid-body-read (a real network failure mode,
// not a synthetic nil-shape).
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
			ChannelType:    7,
			ChannelBaseUrl: "https://api.kling.example",
			ApiKey:         "ak|sk",
		},
	}
	a.Init(info)
	if a.ChannelType != 7 {
		t.Errorf("ChannelType = %d, want 7", a.ChannelType)
	}
	if a.baseURL != "https://api.kling.example" {
		t.Errorf("baseURL = %q, want https://api.kling.example", a.baseURL)
	}
	if a.apiKey != "ak|sk" {
		t.Errorf("apiKey = %q, want ak|sk", a.apiKey)
	}
}

// ---------------------------------------------------------------------------
// ValidateRequestAndSetAction
// ---------------------------------------------------------------------------

func TestTaskB_ValidateRequestAndSetAction_Success(t *testing.T) {
	task_b_ensureMaxBodyMB()
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":"a cat","image":"data:image/png;base64,abc"}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("unexpected task error: %+v", taskErr)
	}
	if info.Action != constant.TaskActionGenerate {
		t.Errorf("Action = %q, want generate (default action for kling)", info.Action)
	}
	v, exists := c.Get("task_request")
	if !exists {
		t.Fatal("expected task_request to be stored in gin context")
	}
	req := v.(relaycommon.TaskSubmitReq)
	if req.Prompt != "a cat" {
		t.Errorf("stored request prompt = %q, want %q", req.Prompt, "a cat")
	}
}

func TestTaskB_ValidateRequestAndSetAction_EmptyPromptRejected(t *testing.T) {
	task_b_ensureMaxBodyMB()
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{"prompt":""}`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := a.ValidateRequestAndSetAction(c, info)
	if taskErr == nil {
		t.Fatal("expected validation error for empty/whitespace prompt")
	}
	if taskErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", taskErr.StatusCode)
	}
}

func TestTaskB_ValidateRequestAndSetAction_MalformedJSON(t *testing.T) {
	task_b_ensureMaxBodyMB()
	a := &TaskAdaptor{}
	c, _ := task_batch_b_newGinContext(http.MethodPost, `{not json`, "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr == nil {
		t.Fatal("expected validation error for malformed request body, must not panic")
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
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"remote-1","task_status":"submitted"}}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{apiKey: "myAccess|mySecret", baseURL: srv.URL}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate},
		ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: "myAccess|mySecret"},
	}

	requestBody := strings.NewReader(`{"prompt":"a cat","image":"data:image/png;base64,abc"}`)
	resp, err := a.DoRequest(c, info, requestBody)
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
	if gotPath != "/v1/videos/image2video" {
		t.Errorf("upstream path = %q, want /v1/videos/image2video (generate action)", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("upstream Authorization = %q, want Bearer-prefixed JWT", gotAuth)
	}
	if !strings.Contains(string(gotBody), "a cat") {
		t.Errorf("upstream did not receive request body, got %q", gotBody)
	}
}

func TestTaskB_DoRequest_ContextActionOverride(t *testing.T) {
	// DoRequest reads the "action" gin key (set by BuildRequestBody when no
	// image is present) and applies it to info.Action before delegating, so
	// text-only submissions route to the text2video endpoint.
	defer task_batch_b_allowLoopbackHTTP()()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"remote-2","task_status":"submitted"}}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{apiKey: "myAccess|mySecret", baseURL: srv.URL}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	c.Set("action", constant.TaskActionTextGenerate)
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate},
		ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: "myAccess|mySecret"},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"prompt":"a cat"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if info.Action != constant.TaskActionTextGenerate {
		t.Errorf("info.Action = %q, want textGenerate to be applied from gin context", info.Action)
	}
	if gotPath != "/v1/videos/text2video" {
		t.Errorf("upstream path = %q, want /v1/videos/text2video after action override", gotPath)
	}
}

func TestTaskB_DoRequest_HeaderBuildFailurePropagates(t *testing.T) {
	// A malformed api key (no accessKey|secretKey separator) makes JWT signing
	// fail inside BuildRequestHeader; DoRequest must surface that as an error
	// rather than silently sending an unauthenticated request upstream.
	a := &TaskAdaptor{apiKey: "no-pipe-here", baseURL: "https://unreachable.invalid"}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate},
		ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: "no-pipe-here"},
	}
	bcResp1, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error when request header construction fails")
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
// FetchTask: JWT-signing fallback and proxy-configuration failure
// ---------------------------------------------------------------------------

func TestTaskB_FetchTask_JWTFailureFallsBackToRawKey(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"t1","task_status":"succeed"}}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{}
	// Malformed key: not sk- prefixed and not in accessKey|secretKey form, so
	// createJWTTokenWithKey fails and FetchTask must fall back to sending the
	// raw key verbatim rather than erroring out the whole poll.
	resp, err := a.FetchTask(srv.URL, "malformed-key-no-pipe", map[string]any{
		"task_id": "t1",
		"action":  constant.TaskActionGenerate,
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if gotAuth != "Bearer malformed-key-no-pipe" {
		t.Errorf("Authorization = %q, want raw-key fallback when JWT signing fails", gotAuth)
	}
}

func TestTaskB_FetchTask_InvalidProxyErrors(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp0, err := a.FetchTask("https://api.kling.example", "sk-key", map[string]any{
		"task_id": "t1",
		"action":  constant.TaskActionGenerate,
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
// convertToRequestPayload: metadata type mismatch (client sends wrong-typed
// field for a known key, e.g. cfg_scale as a string instead of a number).
// ---------------------------------------------------------------------------

func TestTaskB_ConvertToRequestPayload_MetadataTypeMismatchErrors(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt:   "a cat",
		Metadata: map[string]any{"cfg_scale": "not-a-number"},
	}
	if _, err := a.convertToRequestPayload(req); err == nil {
		t.Fatal("expected error when metadata cfg_scale is not numeric (struct field is float64)")
	}
}
