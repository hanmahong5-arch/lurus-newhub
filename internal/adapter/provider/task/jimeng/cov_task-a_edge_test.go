package jimeng

// Business-acceptance edge-case tests filling in the remaining uncovered
// branches of the Jimeng (Volcengine CV) adaptor: upstream body-read
// failures, misconfigured proxy settings, corrupted channel base URLs, and
// type-mismatched submit-metadata. Reuses the taskBatchA* scaffolding from
// cov_task-batch-a_jimeng_test.go in this package; helpers/types unique to
// this file are prefixed task_a_ to avoid collisions with concurrent writers
// in sibling packages.

import (
	"errors"
	"io"
	"net/http"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// task_a_errReader always fails on Read, simulating a connection that drops
// mid-response (upstream TCP reset / TLS truncation).
type task_a_errReader struct{}

func (task_a_errReader) Read(p []byte) (int, error) {
	return 0, errors.New("simulated upstream read failure")
}

func TestJimeng_TaskA_DoResponse_BodyReadError(t *testing.T) {
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

func TestJimeng_TaskA_FetchTask_ProxyClientError(t *testing.T) {
	a := &TaskAdaptor{}
	// A malformed proxy URL (missing scheme) makes url.Parse fail inside
	// GetHttpClientWithProxy before any network I/O is attempted. This
	// models a misconfigured channel-level proxy setting.
	_, err := a.FetchTask("https://x", "sk-relay", map[string]any{"task_id": "t1"}, "://not-a-proxy")
	if err == nil {
		t.Fatalf("expected error for malformed proxy URL")
	}
}

func TestJimeng_TaskA_FetchTask_NewRequestError(t *testing.T) {
	a := &TaskAdaptor{}
	// Direct-vendor key format ("ak|sk") routes the poll URI through the
	// caller-supplied baseUrl parameter (the new-api-relay branch instead
	// pins to a.baseURL, so it wouldn't exercise this). A base URL
	// containing a raw control character makes http.NewRequest reject the
	// constructed URI (net/url: invalid control character in URL), which
	// models a corrupted/garbage channel base_url value.
	badBaseURL := "https://x/\x7f"
	_, err := a.FetchTask(badBaseURL, "AK1|SK1", map[string]any{"task_id": "t1"}, "")
	if err == nil {
		t.Fatalf("expected error for base URL containing an invalid control character")
	}
}

func TestJimeng_TaskA_ConvertToRequestPayload_MetadataTypeMismatch(t *testing.T) {
	a := &TaskAdaptor{}
	// Seed is int64 on the wire payload; a client sending a string in
	// metadata for a field that must round-trip as a number should fail
	// loudly rather than silently drop/corrupt the value.
	req := &relaycommon.TaskSubmitReq{
		Model:  "jimeng_vgfm_t2v_l20",
		Prompt: "a cat",
		Metadata: map[string]interface{}{
			"seed": "not-a-number",
		},
	}
	_, err := a.convertToRequestPayload(req)
	if err == nil {
		t.Fatalf("expected error for type-mismatched metadata field (seed must be numeric)")
	}
}

func TestJimeng_TaskA_BuildRequestBody_MetadataTypeMismatch(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    "jimeng_vgfm_t2v_l20",
		Prompt:   "a cat",
		Metadata: map[string]interface{}{"seed": []string{"nope"}},
	})
	if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
		t.Fatalf("expected BuildRequestBody to surface the metadata conversion error, got nil")
	}
}
