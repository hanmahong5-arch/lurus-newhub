package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// breakerConsecutiveFails reads the failure run of one channel's breaker
// without touching its state machine.
func breakerConsecutiveFails(t *testing.T, reg *resilience.Registry, id int) (state string, fails int) {
	t.Helper()
	for _, s := range reg.Snapshot() {
		if s.ChannelID == id {
			return s.State, s.ConsecutiveFails
		}
	}
	return "closed", 0
}

// doRealUpstreamCall drives the production provider.DoRequest against a
// stalled upstream and ends the call the requested way, then wraps the result
// exactly as the non-streaming handlers do (compatible_handler.go:
// types.NewOpenAIError(err, ErrorCodeDoRequestFailed, 500)). Returns the error
// that reaches reportBreakerOutcome and the request context it ran under.
func doRealUpstreamCall(t *testing.T, mkCtx func() (context.Context, context.CancelFunc), end func(cancel context.CancelFunc)) (*types.NewAPIError, context.Context) {
	t.Helper()
	app.InitHttpClient()
	// The upstream is a loopback httptest server; the relay dial guard would
	// refuse it as an internal address.
	fs := system_setting.GetFetchSetting()
	prevFS := *fs
	fs.EnableSSRFProtection = false
	t.Cleanup(func() { *fs = prevFS })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := mkCtx()
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	go func() {
		time.Sleep(40 * time.Millisecond)
		end(cancel)
	}()
	resp, doErr := provider.DoRequest(c, req, info)
	if doErr == nil {
		_ = resp.Body.Close()
		t.Fatal("DoRequest unexpectedly succeeded against a stalled upstream")
	}
	return types.NewOpenAIError(doErr, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError), ctx
}

// The production incident class: a batch caller hangs up on in-flight
// non-streaming calls over and over. Built from the REAL transport path, not a
// stand-in error. None of those may count toward the trip threshold, so the
// channel stays closed for every other tenant.
func TestBreaker_ClientHangupOnNonStreamingCallsNeverTrips(t *testing.T) {
	reg := swapChannelBreakers(t, resilience.Config{Threshold: 3, Timeout: time.Minute})
	const channelID = 6

	e, _ := doRealUpstreamCall(t,
		func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		func(cancel context.CancelFunc) { cancel() })
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("setup: relay error lost the caller's cancellation: %v", e)
	}
	if types.IsUpstreamFailure(e) {
		t.Fatalf("hang-up during client.Do is an upstream failure (code=%s status=%d)", e.GetErrorCode(), e.StatusCode)
	}

	for i := 0; i < 55; i++ {
		channelBreakers.Allow(channelID)
		reportBreakerOutcome(channelID, e, "")
	}
	if st, fails := breakerConsecutiveFails(t, reg, channelID); st != "closed" || fails != 0 {
		t.Fatalf("55 client hang-ups left breaker %s with %d failures, want closed/0", st, fails)
	}
}

// Control: the gateway's own upstream-call deadline is a real failure and must
// still open the breaker, so the fix cannot be "ignore every context error".
func TestBreaker_GatewayOwnDeadlineStillTrips(t *testing.T) {
	reg := swapChannelBreakers(t, resilience.Config{Threshold: 3, Timeout: time.Minute})
	const channelID = 7

	e, ctx := doRealUpstreamCall(t,
		func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 40*time.Millisecond)
		},
		func(cancel context.CancelFunc) {})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("setup: relay error lost the deadline: %v", e)
	}
	// Even with the request context in hand a deadline is not a hang-up.
	for i := 0; i < 3; i++ {
		channelBreakers.Allow(channelID)
		reportBreakerOutcomeCtx(ctx, channelID, e, "")
	}
	if got := reg.GetState(channelID); got != resilience.StateOpen {
		t.Fatalf("3 gateway-side deadlines left breaker %s, want open", got)
	}
}

// A body read cut by the caller's hang-up, in a provider that re-wraps the
// failure as text, still carries the request context's verdict. reqCtx is the
// witness; the error alone would read as an opaque 500.
func TestBreaker_OpaqueErrorWithCanceledRequestContextIsInconclusive(t *testing.T) {
	reg := swapChannelBreakers(t, resilience.Config{Threshold: 2, Timeout: time.Minute})
	const channelID = 8
	opaque := types.NewErrorWithStatusCode(errors.New("read upstream body failed"),
		types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	if !types.IsUpstreamFailure(opaque) {
		t.Fatal("setup: the opaque error must look like an upstream failure on its own")
	}

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 10; i++ {
		channelBreakers.Allow(channelID)
		reportBreakerOutcomeCtx(gone, channelID, opaque, "")
	}
	if st, fails := breakerConsecutiveFails(t, reg, channelID); st != "closed" || fails != 0 {
		t.Fatalf("hang-ups with a canceled request ctx left breaker %s/%d, want closed/0", st, fails)
	}

	// Control: the same error with a live context is a real failure.
	live := context.Background()
	reportBreakerOutcomeCtx(live, channelID, opaque, "")
	reportBreakerOutcomeCtx(live, channelID, opaque, "")
	if got := reg.GetState(channelID); got != resilience.StateOpen {
		t.Fatalf("same error under a live ctx left breaker %s, want open", got)
	}
}

// A hang-up on the half-open PROBE must hand the probe slot back without
// closing the breaker (nothing proven) and without counting a failure.
func TestBreaker_ClientGoneProbeReleasesSlotWithoutClosing(t *testing.T) {
	reg := swapChannelBreakers(t, resilience.Config{Threshold: 1, Timeout: 5 * time.Millisecond, HalfOpenTimeout: time.Hour})
	const channelID = 9
	reg.RecordFailure(channelID)
	time.Sleep(15 * time.Millisecond)
	if !channelBreakers.Allow(channelID) {
		t.Fatal("setup: probe not admitted")
	}

	// Streaming: nil error + client gone.
	reportBreakerOutcome(channelID, nil, relaycommon.StreamEndClientGone)
	if got := reg.GetState(channelID); got != resilience.StateOpen {
		t.Fatalf("client-gone probe left breaker %s; closing it would claim recovery nobody observed", got)
	}

	// Non-streaming: canceled error on the next probe.
	time.Sleep(15 * time.Millisecond)
	if !channelBreakers.Allow(channelID) {
		t.Fatal("slot was not released: no second probe after the cooldown")
	}
	canceled := types.NewOpenAIError(fmt.Errorf("do: %w", context.Canceled), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	reportBreakerOutcome(channelID, canceled, "")
	if st, fails := breakerConsecutiveFails(t, reg, channelID); st != "open" || fails != 1 {
		t.Fatalf("canceled probe left %s/%d, want open with the single original failure", st, fails)
	}
}
