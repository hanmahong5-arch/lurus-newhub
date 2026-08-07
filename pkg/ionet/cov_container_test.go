package ionet

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func timeMustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("failed to parse test time %q: %v", s, err)
	}
	return tm
}

// --- ListContainers ---------------------------------------------------

func TestListContainers_EmptyDeploymentID(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	_, err := c.ListContainers("")
	if err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Fatalf("err = %v, want exact 'deployment ID cannot be empty'", err)
	}
}

func TestListContainers_Success(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/deployment/dep-1/containers" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"data":{"total":2,"workers":[
			{"device_id":"d1","container_id":"c1","created_at":"2026-01-01T10:00:00","status":"running"},
			{"device_id":"d2","container_id":"c2","created_at":"2026-01-01T11:00:00.123456","status":"stopped"}
		]}}`), nil
	}}
	c := newTestClient(mc)
	list, err := c.ListContainers("dep-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if list.Total != 2 {
		t.Errorf("Total = %d, want 2", list.Total)
	}
	if len(list.Workers) != 2 {
		t.Fatalf("Workers len = %d, want 2", len(list.Workers))
	}
	if list.Workers[0].ContainerID != "c1" || list.Workers[1].ContainerID != "c2" {
		t.Errorf("worker order/content wrong: %+v", list.Workers)
	}
	// flexible-time decoding must have normalized a timezone-less timestamp
	// into a parseable time.Time rather than leaving it zero.
	if list.Workers[0].CreatedAt.IsZero() {
		t.Error("CreatedAt for worker[0] should be parsed, not zero")
	}
}

func TestListContainers_DoError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("network down")
	}}
	c := newTestClient(mc)
	_, err := c.ListContainers("dep-1")
	if err == nil || !strings.Contains(err.Error(), "failed to list containers") {
		t.Fatalf("err = %v, want wrapping 'failed to list containers'", err)
	}
}

func TestListContainers_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not json`), nil
	}}
	c := newTestClient(mc)
	_, err := c.ListContainers("dep-1")
	if err == nil || !strings.Contains(err.Error(), "failed to parse containers list") {
		t.Fatalf("err = %v, want wrapping 'failed to parse containers list'", err)
	}
}

// --- GetContainerDetails ------------------------------------------------

func TestGetContainerDetails_EmptyIDs(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.GetContainerDetails("", "c1"); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v, want deployment ID error", err)
	}
	if _, err := c.GetContainerDetails("d1", ""); err == nil || err.Error() != "container ID cannot be empty" {
		t.Errorf("err = %v, want container ID error", err)
	}
}

func TestGetContainerDetails_DoError_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.GetContainerDetails("d1", "c1"); err == nil || !strings.Contains(err.Error(), "failed to get container details") {
		t.Errorf("err = %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.GetContainerDetails("d1", "c1"); err == nil || !strings.Contains(err.Error(), "failed to parse container details") {
		t.Errorf("err = %v", err)
	}
}

func TestGetContainerDetails_Success_DirectFormat(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/deployment/d1/container/c1" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"device_id":"dev1","container_id":"c1","status":"running","created_at":"2026-02-03T04:05:06"}`), nil
	}}
	c := newTestClient(mc)
	container, err := c.GetContainerDetails("d1", "c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if container.ContainerID != "c1" || container.Status != "running" {
		t.Errorf("unexpected container: %+v", container)
	}
	if container.CreatedAt.IsZero() {
		t.Error("CreatedAt should be normalized/parsed, not zero")
	}
}

// --- GetContainerJobs -----------------------------------------------------

func TestGetContainerJobs_EmptyIDs(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.GetContainerJobs("", "c1"); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.GetContainerJobs("d1", ""); err == nil || err.Error() != "container ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestGetContainerJobs_DoError_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.GetContainerJobs("d1", "c1"); err == nil || !strings.Contains(err.Error(), "failed to get container jobs") {
		t.Errorf("err = %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.GetContainerJobs("d1", "c1"); err == nil || !strings.Contains(err.Error(), "failed to parse container jobs") {
		t.Errorf("err = %v", err)
	}
}

func TestGetContainerJobs_Success(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/deployment/d1/containers-jobs/c1" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"data":{"total":1,"workers":[{"container_id":"job1"}]}}`), nil
	}}
	c := newTestClient(mc)
	list, err := c.GetContainerJobs("d1", "c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if list.Total != 1 || len(list.Workers) != 1 || list.Workers[0].ContainerID != "job1" {
		t.Errorf("unexpected result: %+v", list)
	}
}

// --- buildLogEndpoint -------------------------------------------------

func TestBuildLogEndpoint_EmptyIDs(t *testing.T) {
	if _, err := buildLogEndpoint("", "c1", nil); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if _, err := buildLogEndpoint("d1", "", nil); err == nil || err.Error() != "container ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestBuildLogEndpoint_NilOpts(t *testing.T) {
	got, err := buildLogEndpoint("d1", "c1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/deployment/d1/log/c1"
	if got != want {
		t.Errorf("got %q, want %q (no query string for nil opts)", got, want)
	}
}

func TestBuildLogEndpoint_AllOptions(t *testing.T) {
	opts := &GetLogsOptions{
		Level:  "error",
		Stream: "stderr",
		Limit:  50,
		Cursor: "cur-abc",
		Follow: true,
	}
	got, err := buildLogEndpoint("d1", "c1", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "/deployment/d1/log/c1?") {
		t.Fatalf("got %q, want prefix /deployment/d1/log/c1?", got)
	}
	rawQuery := strings.SplitN(got, "?", 2)[1]
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("query not parseable: %v", err)
	}
	if values.Get("level") != "error" {
		t.Errorf("level = %q, want error", values.Get("level"))
	}
	if values.Get("stream") != "stderr" {
		t.Errorf("stream = %q, want stderr", values.Get("stream"))
	}
	if values.Get("limit") != "50" {
		t.Errorf("limit = %q, want 50", values.Get("limit"))
	}
	if values.Get("cursor") != "cur-abc" {
		t.Errorf("cursor = %q, want cur-abc", values.Get("cursor"))
	}
	if values.Get("follow") != "true" {
		t.Errorf("follow = %q, want true", values.Get("follow"))
	}
}

func TestBuildLogEndpoint_TimeRangeOptions(t *testing.T) {
	start := timeMustParse(t, "2026-01-01T00:00:00Z")
	end := timeMustParse(t, "2026-01-02T00:00:00Z")
	opts := &GetLogsOptions{StartTime: &start, EndTime: &end}
	got, err := buildLogEndpoint("d1", "c1", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rawQuery := strings.SplitN(got, "?", 2)[1]
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("query not parseable: %v", err)
	}
	if values.Get("start_time") == "" {
		t.Error("start_time query param missing when StartTime set")
	}
	if values.Get("end_time") == "" {
		t.Error("end_time query param missing when EndTime set")
	}
}

func TestBuildLogEndpoint_ZeroLimitAndFollowFalseOmitted(t *testing.T) {
	opts := &GetLogsOptions{Limit: 0, Follow: false}
	got, err := buildLogEndpoint("d1", "c1", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/deployment/d1/log/c1" {
		t.Errorf("got %q, want no query string when Limit=0 and Follow=false", got)
	}
}

// --- GetContainerLogsRaw -------------------------------------------------

func TestGetContainerLogsRaw_PropagatesEndpointError(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	_, err := c.GetContainerLogsRaw("", "c1", nil)
	if err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestGetContainerLogsRaw_ReturnsRawTextUnparsed(t *testing.T) {
	rawBody := "line one\nline two: not-json-at-all {broken"
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, rawBody), nil
	}}
	c := newTestClient(mc)
	got, err := c.GetContainerLogsRaw("d1", "c1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != rawBody {
		t.Errorf("got %q, want raw body returned verbatim (no JSON parsing)", got)
	}
}

func TestGetContainerLogsRaw_DoError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("timeout")
	}}
	c := newTestClient(mc)
	_, err := c.GetContainerLogsRaw("d1", "c1", nil)
	if err == nil || !strings.Contains(err.Error(), "failed to get container logs") {
		t.Errorf("err = %v", err)
	}
}

// --- GetContainerLogs (normalizes raw text into LogEntry) -----------------

func TestGetContainerLogs_EmptyRaw(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, ``), nil
	}}
	c := newTestClient(mc)
	logs, err := c.GetContainerLogs("d1", "c1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logs.ContainerID != "c1" {
		t.Errorf("ContainerID = %q, want c1", logs.ContainerID)
	}
	if len(logs.Logs) != 0 {
		t.Errorf("Logs = %+v, want empty for empty raw body", logs.Logs)
	}
}

func TestGetContainerLogs_NormalizesCRLFAndFiltersBlankLines(t *testing.T) {
	raw := "line1\r\n\r\n  \r\nline2\nline3\r\n"
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, raw), nil
	}}
	c := newTestClient(mc)
	logs, err := c.GetContainerLogs("d1", "c1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs.Logs) != 3 {
		t.Fatalf("Logs len = %d, want 3 (blank lines filtered), got %+v", len(logs.Logs), logs.Logs)
	}
	want := []string{"line1", "line2", "line3"}
	for i, w := range want {
		if logs.Logs[i].Message != w {
			t.Errorf("Logs[%d].Message = %q, want %q", i, logs.Logs[i].Message, w)
		}
	}
}

func TestGetContainerLogs_PropagatesRawError(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	_, err := c.GetContainerLogs("", "c1", nil)
	if err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

// --- StreamContainerLogs -------------------------------------------------

func TestStreamContainerLogs_ValidationErrors(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if err := c.StreamContainerLogs("", "c1", nil, func(*LogEntry) error { return nil }); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if err := c.StreamContainerLogs("d1", "", nil, func(*LogEntry) error { return nil }); err == nil || err.Error() != "container ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if err := c.StreamContainerLogs("d1", "c1", nil, nil); err == nil || err.Error() != "callback function cannot be nil" {
		t.Errorf("err = %v", err)
	}
}

func TestStreamContainerLogs_ForcesFollowTrue_AndInvokesCallback(t *testing.T) {
	var sawFollow string
	var received []string
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if u, err := url.Parse(req.URL); err == nil {
			sawFollow = u.Query().Get("follow")
		}
		return jsonResponse(200, `{"container_id":"c1","logs":[{"message":"hello"},{"message":"world"}],"has_more":false}`), nil
	}}
	c := newTestClient(mc)
	err := c.StreamContainerLogs("d1", "c1", &GetLogsOptions{Follow: false}, func(e *LogEntry) error {
		received = append(received, e.Message)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawFollow != "true" {
		t.Errorf("follow query param = %q, want true (forced regardless of caller's Follow=false)", sawFollow)
	}
	if len(received) != 2 || received[0] != "hello" || received[1] != "world" {
		t.Errorf("callback did not receive expected entries: %v", received)
	}
	if mc.CallCount != 1 {
		t.Errorf("expected exactly one poll when HasMore=false and no cursor, got %d", mc.CallCount)
	}
}

func TestStreamContainerLogs_CallbackErrorAbortsImmediately(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `{"logs":[{"message":"a"},{"message":"b"}],"has_more":true,"next_cursor":"next"}`), nil
	}}
	c := newTestClient(mc)
	callbackCalls := 0
	err := c.StreamContainerLogs("d1", "c1", nil, func(e *LogEntry) error {
		callbackCalls++
		return errors.New("consumer stopped")
	})
	if err == nil || !strings.Contains(err.Error(), "callback error") {
		t.Fatalf("err = %v, want wrapping 'callback error'", err)
	}
	if callbackCalls != 1 {
		t.Errorf("callback called %d times, want exactly 1 (must abort on first error)", callbackCalls)
	}
	if mc.CallCount != 1 {
		t.Errorf("Do called %d times, want exactly 1 (must not poll again after callback error)", mc.CallCount)
	}
}

func TestStreamContainerLogs_DoError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("upstream unreachable")
	}}
	c := newTestClient(mc)
	err := c.StreamContainerLogs("d1", "c1", nil, func(*LogEntry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "failed to stream container logs") {
		t.Errorf("err = %v, want wrapping 'failed to stream container logs'", err)
	}
}

func TestStreamContainerLogs_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json-at-all`), nil
	}}
	c := newTestClient(mc)
	err := c.StreamContainerLogs("d1", "c1", nil, func(*LogEntry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "failed to parse container logs") {
		t.Errorf("err = %v, want wrapping 'failed to parse container logs'", err)
	}
}

func TestStreamContainerLogs_CursorPaginationUpdatesEndpoint(t *testing.T) {
	var seenCursors []string
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		u, _ := url.Parse(req.URL)
		seenCursors = append(seenCursors, u.Query().Get("cursor"))
		if len(seenCursors) == 1 {
			return jsonResponse(200, `{"logs":[{"message":"page1"}],"has_more":true,"next_cursor":"page2-cursor"}`), nil
		}
		return jsonResponse(200, `{"logs":[{"message":"page2"}],"has_more":false}`), nil
	}}
	c := newTestClient(mc)
	var msgs []string
	err := c.StreamContainerLogs("d1", "c1", nil, func(e *LogEntry) error {
		msgs = append(msgs, e.Message)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mc.CallCount != 2 {
		t.Fatalf("Do called %d times, want exactly 2 (one per page)", mc.CallCount)
	}
	if seenCursors[0] != "" {
		t.Errorf("first request cursor = %q, want empty", seenCursors[0])
	}
	if seenCursors[1] != "page2-cursor" {
		t.Errorf("second request cursor = %q, want page2-cursor propagated from first response", seenCursors[1])
	}
	if strings.Join(msgs, ",") != "page1,page2" {
		t.Errorf("msgs = %v, want both pages delivered in order", msgs)
	}
}

// --- RestartContainer / StopContainer -----------------------------------

func TestRestartContainer_EmptyIDs(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if err := c.RestartContainer("", "c1"); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if err := c.RestartContainer("d1", ""); err == nil || err.Error() != "container ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestRestartContainer_Success_And_Error(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.Method != "POST" {
			t.Errorf("method = %s, want POST", req.Method)
		}
		if req.URL != "https://unit-test.invalid/deployment/d1/container/c1/restart" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{}`), nil
	}}
	c := newTestClient(mc)
	if err := c.RestartContainer("d1", "c1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("upstream down")
	}}
	c2 := newTestClient(mc2)
	err := c2.RestartContainer("d1", "c1")
	if err == nil || !strings.Contains(err.Error(), "failed to restart container") {
		t.Errorf("err = %v, want wrapping 'failed to restart container'", err)
	}
}

func TestStopContainer_EmptyIDs(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if err := c.StopContainer("", "c1"); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if err := c.StopContainer("d1", ""); err == nil || err.Error() != "container ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestStopContainer_Success_And_Error(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/deployment/d1/container/c1/stop" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{}`), nil
	}}
	c := newTestClient(mc)
	if err := c.StopContainer("d1", "c1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("upstream down")
	}}
	c2 := newTestClient(mc2)
	err := c2.StopContainer("d1", "c1")
	if err == nil || !strings.Contains(err.Error(), "failed to stop container") {
		t.Errorf("err = %v", err)
	}
}

// --- ExecuteInContainer ---------------------------------------------------

func TestExecuteInContainer_ValidationErrors(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.ExecuteInContainer("", "c1", []string{"ls"}); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.ExecuteInContainer("d1", "", []string{"ls"}); err == nil || err.Error() != "container ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.ExecuteInContainer("d1", "c1", nil); err == nil || err.Error() != "command cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.ExecuteInContainer("d1", "c1", []string{}); err == nil || err.Error() != "command cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestExecuteInContainer_OutputField(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if !strings.Contains(string(req.Body), `"ls"`) {
			t.Errorf("request body missing command: %s", req.Body)
		}
		return jsonResponse(200, `{"output":"total 0\n","exit_code":0}`), nil
	}}
	c := newTestClient(mc)
	out, err := c.ExecuteInContainer("d1", "c1", []string{"ls"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "total 0\n" {
		t.Errorf("out = %q, want the 'output' field value", out)
	}
}

func TestExecuteInContainer_FallbackToRawBody_WhenNoOutputField(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `{"exit_code":0}`), nil
	}}
	c := newTestClient(mc)
	out, err := c.ExecuteInContainer("d1", "c1", []string{"ls"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"exit_code":0}` {
		t.Errorf("out = %q, want raw body fallback when 'output' key absent", out)
	}
}

func TestExecuteInContainer_DoError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("exec upstream down")
	}}
	c := newTestClient(mc)
	_, err := c.ExecuteInContainer("d1", "c1", []string{"ls"})
	if err == nil || !strings.Contains(err.Error(), "failed to execute command in container") {
		t.Errorf("err = %v, want wrapping 'failed to execute command in container'", err)
	}
}

func TestExecuteInContainer_InvalidJSONResult(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c := newTestClient(mc)
	_, err := c.ExecuteInContainer("d1", "c1", []string{"ls"})
	if err == nil || !strings.Contains(err.Error(), "failed to parse execution result") {
		t.Errorf("err = %v, want wrapping 'failed to parse execution result'", err)
	}
}
