package ali

// image_timeout_test.go — cycle 12 L7 oracle for the ali async-task poller.
//
// updateTask built a bare &http.Client{} — no Timeout — and a request with no
// context, so a task-status endpoint that accepts the connection and never
// answers pinned the polling goroutine for the life of the process. Every
// async image generation goes through this loop, once per poll, up to 20 polls.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// hungUpstream returns the base URL of a server that accepts and never answers
// inside the test's horizon, plus a cleanup that releases it.
func hungUpstream(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	return srv.URL
}

func hungInfo(baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: baseURL, ApiKey: "k"}}
}

// TestUpdateTask_HungUpstreamIsBounded: the no-context entry point must still
// come back on its own budget rather than never.
func TestUpdateTask_HungUpstreamIsBounded(t *testing.T) {
	prev := aliTaskPollTimeout
	aliTaskPollTimeout = 300 * time.Millisecond
	t.Cleanup(func() { aliTaskPollTimeout = prev })

	info := hungInfo(hungUpstream(t))

	type result struct{ err error }
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		_, err, _ := updateTask(info, "t-hung")
		done <- result{err}
	}()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("a task poll against an upstream that never answers must return an error")
		}
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("updateTask returned after %v with a 300ms budget", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("updateTask did not return within 2s against an upstream that never answers: " +
			"the poll has neither a client timeout nor a request context")
	}
}

// TestUpdateTaskCtx_HonoursCallerDeadline: the caller's budget wins over the
// package default, which is what lets asyncTaskWait stop polling when the
// originating request is gone.
func TestUpdateTaskCtx_HonoursCallerDeadline(t *testing.T) {
	info := hungInfo(hungUpstream(t))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := updateTaskCtx(ctx, info, "t-ctx")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when the caller's deadline expires mid-poll")
	}
	if elapsed > time.Second {
		t.Errorf("updateTaskCtx took %v with a 200ms caller budget", elapsed)
	}
}

// TestAsyncTaskWaitPollsThroughTheContextAwareEntryPoint keeps the loop wired to
// the request. Driving asyncTaskWait for real costs its mandatory 5s pre-poll
// sleep plus a 10s retry interval, neither injectable, so the loop's use of the
// context-aware poll is pinned structurally instead.
func TestAsyncTaskWaitPollsThroughTheContextAwareEntryPoint(t *testing.T) {
	raw, err := os.ReadFile("image.go")
	if err != nil {
		t.Fatalf("read image.go: %v", err)
	}
	// Line endings normalised: a Windows checkout of this repo produces CRLF
	// working copies for text-attributed sources, and the needle below spans a
	// line break.
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(src, "func asyncTaskWait(")
	if i < 0 {
		t.Fatal("asyncTaskWait not found in image.go — this check is measuring nothing")
	}
	rest := src[i:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatal("could not find the end of asyncTaskWait")
	}
	if !strings.Contains(rest[:end], "updateTaskCtx(") {
		t.Error("asyncTaskWait no longer polls through updateTaskCtx: the loop would stop " +
			"honouring the originating request's context")
	}
}
