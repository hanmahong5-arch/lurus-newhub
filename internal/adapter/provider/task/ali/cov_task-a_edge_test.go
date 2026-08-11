package ali

// Business-acceptance edge-case tests filling in the remaining uncovered
// branches of the Ali (Tongyi Wanxiang) video adaptor: resolution-suffix
// normalization when the caller omits the trailing "P", propagation of an
// unsupported custom size through convertToAliRequest, upstream body-read
// failures, and misconfigured proxy / corrupted base-URL handling in
// FetchTask. Reuses the taskBatchA* scaffolding from
// cov_task-batch-a_ali_test.go in this package; helpers/types unique to
// this file are prefixed task_a_ to avoid collisions with concurrent
// writers in sibling packages.

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// task_a_errReader always fails on Read, simulating a connection that drops
// mid-response (upstream TCP reset / TLS truncation).
type task_a_errReader struct{}

func (task_a_errReader) Read(p []byte) (int, error) {
	return 0, errors.New("simulated upstream read failure")
}

// ─── ProcessAliOtherRatios: bare (non-"P"-suffixed) resolution values ──────

func TestAli_TaskA_ProcessAliOtherRatios_BareResolutionGetsPSuffix(t *testing.T) {
	// Resolution values already ending in "p"/"P" (as exercised by the
	// existing batch-a tests) never hit the suffix-append branch. A bare
	// numeric resolution (no unit letter at all) forces it.
	req := &AliVideoRequest{
		Model:      "wan2.2-i2v-plus",
		Parameters: &AliVideoParameters{Resolution: "1080"},
	}
	got, err := ProcessAliOtherRatios(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := got["resolution-1080P"]
	if !ok {
		t.Fatalf("expected bare resolution '1080' to be normalized to '1080P' key, got %v", got)
	}
	if v != 0.7/0.14 {
		t.Fatalf("expected wan2.2-i2v-plus 1080P discount ratio, got %v", v)
	}
}

// ─── convertToAliRequest: size normalization + invalid custom size ────────

func TestAli_TaskA_ConvertToAliRequest_BareSizeGetsPSuffix(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{}
	// "720" (no "p"/"P" at all) forces the append-suffix branch inside
	// convertToAliRequest's own resolution normalization, distinct from
	// the "720p" case already covered in batch-a which is already
	// suffixed after ToUpper and never enters the if-body.
	req, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
		Model: "wan2.2-i2v-plus", Prompt: "p", Size: "720",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Parameters.Resolution != "720P" {
		t.Fatalf("expected bare size '720' normalized to '720P', got %q", req.Parameters.Resolution)
	}
}

func TestAli_TaskA_ConvertToAliRequest_UnsupportedWildcardSizeIsRejected(t *testing.T) {
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{}
	// A wildcard ("*") size not present in any of the 480p/720p/1080p
	// lookup tables passes the "contains *" gate but fails
	// sizeToResolution inside the billing-ratio computation; the error
	// must propagate out of convertToAliRequest rather than silently
	// falling through to a submit with unbilled/unknown resolution.
	_, err := a.convertToAliRequest(info, relaycommon.TaskSubmitReq{
		Model: "wan2.2-t2v-plus", Prompt: "p", Size: "999*999",
	})
	if err == nil {
		t.Fatalf("expected error for unsupported custom size 999*999")
	}
	if !strings.Contains(err.Error(), "invalid size") {
		t.Fatalf("expected error to surface the invalid-size reason, got: %v", err)
	}
}

// ─── DoResponse: upstream body read failure ────────────────────────────────

func TestAli_TaskA_DoResponse_BodyReadError(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
	resp := &http.Response{Body: io.NopCloser(task_a_errReader{})}

	taskID, taskData, taskErr := a.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatalf("expected task error when upstream body read fails, got taskID=%q", taskID)
	}
	if taskErr.Code != "read_response_body_failed" {
		t.Fatalf("expected read_response_body_failed error code, got %q", taskErr.Code)
	}
	if taskData != nil {
		t.Fatalf("expected nil task data on read failure, got %v", taskData)
	}
}

// ─── FetchTask: misconfigured proxy / corrupted task id ───────────────────

func TestAli_TaskA_FetchTask_ProxyClientError(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp1, err := a.FetchTask("https://dashscope.aliyuncs.com", "sk-x", map[string]any{"task_id": "t1"}, "://not-a-proxy")
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	if err == nil {
		t.Fatalf("expected error for malformed proxy URL")
	}
}

func TestAli_TaskA_FetchTask_NewRequestError(t *testing.T) {
	a := &TaskAdaptor{}
	// The task_id (sourced from the earlier submit response, i.e. from the
	// upstream vendor -- not locally validated) is interpolated directly
	// into the poll URL path. A task_id containing a raw control character
	// makes http.NewRequest reject the URI instead of the code
	// panicking/mangling the request.
	bcResp0, err := a.FetchTask("https://dashscope.aliyuncs.com", "sk-x", map[string]any{"task_id": "bad\x7fid"}, "")
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err == nil {
		t.Fatalf("expected error for task_id containing an invalid control character")
	}
}
