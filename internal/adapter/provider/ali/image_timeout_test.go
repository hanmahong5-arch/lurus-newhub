package ali

// image_timeout_test.go — cycle 12 L7 oracles for the ali async-task poller.
//
// Two defects, one call site. The single GET (updateTaskCtx) built a bare
// &http.Client{} — no Timeout — and a request with no context, so a task-status
// endpoint that accepts the connection and never answers pinned the polling
// goroutine. And the loop around it (asyncTaskWait) treated every failed attempt
// as "sleep and try again" with no exit at all: once the originating request was
// cancelled, every attempt failed instantly and the loop spun for the life of
// the pod. Every async image generation goes through this loop, up to 20 polls.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"

	"github.com/gin-gonic/gin"
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

// deadUpstream returns a base URL whose port has no listener: every poll against
// it fails immediately with connection refused, which is what makes it the right
// shape for driving the loop's retry budget.
func deadUpstream(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}

func hungInfo(baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: baseURL, ApiKey: "k"}}
}

// shortPollDelays shrinks the two loop delays for the duration of one test.
// Production values (5s before the first poll, 10s between attempts) are pinned
// separately by TestAliPollDelayDefaults.
func shortPollDelays(t *testing.T) {
	t.Helper()
	prevFirst, prevInterval := aliTaskFirstPollDelay, aliTaskPollInterval
	aliTaskFirstPollDelay = 10 * time.Millisecond
	aliTaskPollInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		aliTaskFirstPollDelay, aliTaskPollInterval = prevFirst, prevInterval
	})
}

// ginContextWithRequest builds the gin context asyncTaskWait reads its polling
// context out of, and hands back the cancel func so a test can play the client
// disconnecting mid-generation.
func ginContextWithRequest(t *testing.T) (*gin.Context, context.CancelFunc) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(ctx)
	t.Cleanup(cancel)
	return c, cancel
}

func TestAliPollDelayDefaults(t *testing.T) {
	if aliTaskFirstPollDelay != 5*time.Second {
		t.Errorf("aliTaskFirstPollDelay = %v, want 5s", aliTaskFirstPollDelay)
	}
	if aliTaskPollInterval != 10*time.Second {
		t.Errorf("aliTaskPollInterval = %v, want 10s", aliTaskPollInterval)
	}
	if aliTaskClient.Timeout != aliTaskPollTimeout {
		t.Errorf("aliTaskClient.Timeout = %v, aliTaskPollTimeout = %v: the shared client's own "+
			"ceiling has drifted from the declared per-poll budget, so a caller that arrives "+
			"without a deadline is bounded at a number nobody chose",
			aliTaskClient.Timeout, aliTaskPollTimeout)
	}
	if aliTaskPollTimeout != 30*time.Second {
		t.Errorf("aliTaskPollTimeout = %v, want 30s", aliTaskPollTimeout)
	}
}

// TestUpdateTaskCtx_SharedClientBoundsADeadlineLessCaller: the loop always hands
// in a deadline, but the shared client carries its own Timeout so a future caller
// with a plain context.Background() still cannot park forever. Driving it through
// an injected short-timeout client also pins that the poll goes through
// aliTaskClient rather than http.DefaultClient (which has no Timeout).
func TestUpdateTaskCtx_SharedClientBoundsADeadlineLessCaller(t *testing.T) {
	prev := aliTaskClient
	aliTaskClient = &http.Client{Timeout: 300 * time.Millisecond}
	t.Cleanup(func() { aliTaskClient = prev })

	info := hungInfo(hungUpstream(t))

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, _, err := updateTaskCtx(context.Background(), info, "t-hung")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a task poll against an upstream that never answers must return an error")
		}
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("updateTaskCtx returned after %v with a 300ms client timeout", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("updateTaskCtx did not return within 2s against an upstream that never answers: " +
			"the poll is not running on the bounded shared client")
	}
}

// TestUpdateTaskCtx_HonoursCallerDeadline: the caller's budget wins, which is
// what lets asyncTaskWait stop polling when the originating request is gone.
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

// TestAsyncTaskWait_CancelledRequestStopsPolling is the blocker oracle. The
// client hangs up after the first poll is already in flight; every later attempt
// then fails instantly on the dead context, which is exactly the state the old
// error branch had no exit from — it logged, slept and continued, forever.
//
// Timed out rather than asserted on elapsed alone because the regression shape
// is "never returns", not "returns late".
func TestAsyncTaskWait_CancelledRequestStopsPolling(t *testing.T) {
	shortPollDelays(t)
	info := hungInfo(hungUpstream(t))
	c, cancel := ginContextWithRequest(t)

	type outcome struct {
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		_, _, err := asyncTaskWait(c, info, "t-cancelled")
		done <- outcome{err}
	}()

	// Let the first poll start, then play the disconnect.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("asyncTaskWait must report the cancellation, not a nil error")
		}
		if !strings.Contains(got.err.Error(), "context canceled") {
			t.Errorf("err = %v, want the cancellation surfaced", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("asyncTaskWait never returned after its request context was cancelled: the " +
			"poll loop's error branch has no exit and this goroutine would leak for the " +
			"life of the process")
	}
}

// TestAsyncTaskWait_PersistentFailureExhaustsTheStepBudget: an upstream that
// refuses every connection, with a perfectly live caller, must still end at the
// step budget instead of retrying forever.
func TestAsyncTaskWait_PersistentFailureExhaustsTheStepBudget(t *testing.T) {
	shortPollDelays(t)
	info := hungInfo(deadUpstream(t))
	c, _ := ginContextWithRequest(t)

	done := make(chan error, 1)
	go func() {
		_, _, err := asyncTaskWait(c, info, "t-refused")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("20 failed polls must end in an error, not a nil success")
		}
		if !strings.Contains(err.Error(), "aliAsyncTaskWait timeout") {
			t.Errorf("err = %v, want the step-budget timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("asyncTaskWait never returned against an upstream that refuses every poll: " +
			"the error branch does not honour maxStep")
	}
}

// TestAsyncTaskWaitPollsThroughTheContextAwareEntryPoint keeps the loop wired to
// the request context even if someone reintroduces a context-free poll helper.
func TestAsyncTaskWaitPollsThroughTheContextAwareEntryPoint(t *testing.T) {
	raw, err := os.ReadFile("image.go")
	if err != nil {
		t.Fatalf("read image.go: %v", err)
	}
	// Line endings normalised: a Windows checkout of this repo can produce CRLF
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
