package vidu

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
			ChannelType:    constant.ChannelTypeVidu,
			ChannelBaseUrl: "https://api.vidu.example",
		},
	}
	a.Init(info)
	if a.ChannelType != constant.ChannelTypeVidu {
		t.Errorf("ChannelType = %d, want %d", a.ChannelType, constant.ChannelTypeVidu)
	}
	if a.baseURL != "https://api.vidu.example" {
		t.Errorf("baseURL = %q, want https://api.vidu.example", a.baseURL)
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
		_, _ = w.Write([]byte(`{"task_id":"remote-1","state":"created"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{baseURL: srv.URL}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate},
		ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: "vidu-secret"},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"prompt":"a cat","images":["x.png"]}`))
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
	if gotPath != "/ent/v2/img2video" {
		t.Errorf("upstream path = %q, want /ent/v2/img2video (generate action)", gotPath)
	}
	if gotAuth != "Token vidu-secret" {
		t.Errorf("upstream Authorization = %q, want Token vidu-secret", gotAuth)
	}
	if !strings.Contains(string(gotBody), "a cat") {
		t.Errorf("upstream did not receive request body, got %q", gotBody)
	}
}

func TestTaskB_DoRequest_TextGenerateRoutesToTextEndpoint(t *testing.T) {
	defer task_batch_b_allowLoopbackHTTP()()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"task_id":"remote-2","state":"created"}`))
	}))
	defer srv.Close()

	a := &TaskAdaptor{baseURL: srv.URL}
	c, _ := task_batch_b_newGinContext(http.MethodPost, "", "")
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionTextGenerate},
		ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: "vidu-secret"},
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
	if gotPath != "/ent/v2/text2video" {
		t.Errorf("upstream path = %q, want /ent/v2/text2video", gotPath)
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
	bcResp0, err := a.FetchTask("https://api.vidu.example", "vidu-key", map[string]any{
		"task_id": "t1",
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
// field for a known key, e.g. seed as a string instead of a number).
// ---------------------------------------------------------------------------

func TestTaskB_ConvertToRequestPayload_MetadataTypeMismatchErrors(t *testing.T) {
	a := &TaskAdaptor{}
	req := &relaycommon.TaskSubmitReq{
		Prompt:   "hi",
		Metadata: map[string]any{"seed": "not-a-number"},
	}
	if _, err := a.convertToRequestPayload(req); err == nil {
		t.Fatal("expected error when metadata seed is not numeric (struct field is int)")
	}
}
