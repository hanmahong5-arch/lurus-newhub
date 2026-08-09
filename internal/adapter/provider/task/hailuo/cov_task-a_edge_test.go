package hailuo

// Business-acceptance edge-case tests filling in the remaining uncovered
// branches of the Hailuo (MiniMax) video adaptor: type-mismatched
// submit-metadata, upstream body-read failures, misconfigured proxy /
// corrupted task-id handling in FetchTask, and network-level failure modes
// of the two-hop file-retrieve lookup in buildVideoURL. Reuses the
// taskBatchA* scaffolding from cov_task-batch-a_hailuo_test.go in this
// package; helpers/types unique to this file are prefixed task_a_ to avoid
// collisions with concurrent writers in sibling packages.

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// task_a_errReader always fails on Read, simulating a connection that drops
// mid-response (upstream TCP reset / TLS truncation).
type task_a_errReader struct{}

func (task_a_errReader) Read(p []byte) (int, error) {
	return 0, errors.New("simulated upstream read failure")
}

// ─── convertToRequestPayload / BuildRequestBody: metadata type mismatch ───

func TestHailuo_TaskA_ConvertToRequestPayload_MetadataTypeMismatch(t *testing.T) {
	a := &TaskAdaptor{}
	// Duration is *int on VideoRequest; a client sending a string for it
	// via metadata must fail loudly rather than silently drop the field.
	_, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:    "T2V-01",
		Metadata: map[string]interface{}{"duration": "not-a-number"},
	})
	if err == nil {
		t.Fatalf("expected error for type-mismatched metadata field (duration must be numeric)")
	}
}

func TestHailuo_TaskA_BuildRequestBody_MetadataTypeMismatch(t *testing.T) {
	a := &TaskAdaptor{}
	c, _ := taskBatchANewGinCtx(t, http.MethodPost, "/x", nil, "")
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    "T2V-01",
		Prompt:   "a robot dancing",
		Metadata: map[string]interface{}{"duration": []string{"nope"}},
	})
	if _, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{}); err == nil {
		t.Fatalf("expected BuildRequestBody to surface the metadata conversion error, got nil")
	}
}

// ─── DoResponse: upstream body read failure ────────────────────────────────

func TestHailuo_TaskA_DoResponse_BodyReadError(t *testing.T) {
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

func TestHailuo_TaskA_FetchTask_ProxyClientError(t *testing.T) {
	a := &TaskAdaptor{}
	bcResp1, err := a.FetchTask("https://api.minimax.chat", "k", map[string]any{"task_id": "t1"}, "://not-a-proxy")
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	if err == nil {
		t.Fatalf("expected error for malformed proxy URL")
	}
}

func TestHailuo_TaskA_FetchTask_NewRequestError(t *testing.T) {
	a := &TaskAdaptor{}
	// task_id (sourced from the earlier submit response) is interpolated
	// directly into the poll query string. A task_id containing a raw
	// control character makes http.NewRequest reject the URI instead of
	// the code panicking/mangling the request.
	bcResp0, err := a.FetchTask("https://api.minimax.chat", "k", map[string]any{"task_id": "bad\x7fid"}, "")
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err == nil {
		t.Fatalf("expected error for task_id containing an invalid control character")
	}
}

// ─── buildVideoURL: network-level failure modes of the file-retrieve hop ──

func TestHailuo_TaskA_BuildVideoURL_NewRequestError(t *testing.T) {
	// fileID is sourced from the upstream task-status response, not
	// locally validated, and is interpolated directly into the
	// file-retrieve query string.
	a := &TaskAdaptor{apiKey: "k", baseURL: "https://api.minimax.chat"}
	if got := a.buildVideoURL("t", "bad\x7fid"); got != "" {
		t.Fatalf("expected empty url for a file_id containing an invalid control character, got %q", got)
	}
}

func TestHailuo_TaskA_BuildVideoURL_ConnectionRefused(t *testing.T) {
	// Bind and immediately close a listener to get a real "connection
	// refused" target without hitting the network, then point buildVideoURL
	// at it to exercise the client.Do error branch.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a local port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	taskBatchAAllowLoopbackHTTP(t)
	a := &TaskAdaptor{apiKey: "k", baseURL: "http://" + addr}
	if got := a.buildVideoURL("t", "f"); got != "" {
		t.Fatalf("expected empty url when the upstream connection is refused, got %q", got)
	}
}

func TestHailuo_TaskA_BuildVideoURL_TruncatedBody(t *testing.T) {
	// Declare a larger Content-Length than what is actually written, then
	// hijack and close the connection early so the client's body reader
	// hits an unexpected-EOF mid-read, exercising buildVideoURL's
	// io.ReadAll error branch on a real (not synthetic) upstream response.
	taskBatchAAllowLoopbackHTTP(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("test server does not support hijacking")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\nshort")
		_ = buf.Flush()
	}))
	defer srv.Close()

	a := &TaskAdaptor{apiKey: "k", baseURL: srv.URL}
	if got := a.buildVideoURL("t", "f"); got != "" {
		t.Fatalf("expected empty url for a truncated upstream response body, got %q", got)
	}
}
