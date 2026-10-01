package handler

// relay_stream_incomplete_test.go — the relay loop's view of an upstream stream
// that stops mid-answer AFTER bytes already reached the caller.
//
// The stream handlers used to answer that case with an in-band error frame and
// a nil error, so the loop recorded a breaker SUCCESS, the route attempt and
// request metrics said "success", and the channel kept taking traffic however
// often it truncated. The handlers now return the error as well; these tests
// pin what the loop must do with it.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// truncatedStreamUpstream sends two content chunks and then ends the response
// cleanly with no finish_reason and no [DONE] — an upstream that hung up
// mid-answer. calls reports how many requests reached it.
func truncatedStreamUpstream(calls *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, part := range []string{"hel", "lo"} {
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"model-a\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\n", part)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// TestRelay_MidStreamTruncationIsABreakerFailureNotASuccess: upstream answers
// 200 + some content, then EOF with no terminal event.
//
//   - exactly ONE in-band error frame reaches the caller (the handler writes it;
//     the relay's own error renderer must not write a second one),
//   - the channel's breaker records a FAILURE (threshold 1 => open),
//   - no second channel/attempt (bytes are out; shouldRetry refuses),
//   - nothing is charged and the pre-consumed quota is handed back.
func TestRelay_MidStreamTruncationIsABreakerFailureNotASuccess(t *testing.T) {
	pinRetryTimes(t, 2) // a failover budget exists; it must still not be used
	prevTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 60 // the scanner's idle timer needs a positive value
	t.Cleanup(func() { constant.StreamingTimeout = prevTimeout })
	reg := swapChannelBreakers(t, resilience.Config{
		Threshold:       1,
		Timeout:         time.Hour,
		HalfOpenTimeout: time.Hour,
	})

	var upstreamCalls atomic.Int32
	ctx := setupRelaySuccessRouter(t, truncatedStreamUpstream(&upstreamCalls))

	var selections atomic.Int32
	prevGetChannel := getChannelFn
	getChannelFn = func(c *gin.Context, info *relaycommon.RelayInfo, rp *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
		selections.Add(1)
		return prevGetChannel(c, info, rp)
	}
	t.Cleanup(func() { getChannelFn = prevGetChannel })

	var quotaBefore int
	if err := ctx.db.Model(&repo.User{}).Where("id = ?", ctx.user.Id).Select("quota").Scan(&quotaBefore).Error; err != nil {
		t.Fatalf("read user quota: %v", err)
	}

	w := ctx.postChat(t, `{"model":"`+ctx.channel.Models+`","stream":true,"messages":[{"role":"user","content":"hi"}]}`, nil)
	body := w.Body.String()

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the status line was spent on the first frame); body=%s", w.Code, body)
	}
	if !strings.Contains(body, `"content":"lo"`) {
		t.Fatalf("content delivered before the cut must still be in the body:\n%s", body)
	}
	if got := strings.Count(body, `"code":"upstream_stream_incomplete"`); got != 1 {
		t.Errorf("in-band error frames = %d, want exactly 1 (a second one is the relay's error renderer writing the same failure again):\n%s", got, body)
	}
	if got := strings.Count(body, `"error":{`); got != 1 {
		t.Errorf("error envelopes in the body = %d, want exactly 1:\n%s", got, body)
	}

	if got := reg.GetState(ctx.channel.Id).String(); got != "open" {
		t.Errorf("breaker state = %q, want open — a mid-stream truncation is the channel's failure, "+
			"and recording it as a success (nil error) keeps a truncating channel in rotation forever", got)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 — no failover once bytes have left", got)
	}
	if got := selections.Load(); got != 1 {
		t.Errorf("channel selections = %d, want 1 — no second attempt", got)
	}

	var logs []repo.Log
	if err := ctx.db.Where("type = ?", repo.LogTypeConsume).Find(&logs).Error; err != nil {
		t.Fatalf("query consume logs: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("consume log rows = %d, want 0 — the caller was told the stream failed, nothing is charged", len(logs))
	}
	var quotaAfter int
	if err := ctx.db.Model(&repo.User{}).Where("id = ?", ctx.user.Id).Select("quota").Scan(&quotaAfter).Error; err != nil {
		t.Fatalf("read user quota: %v", err)
	}
	if quotaAfter != quotaBefore {
		t.Errorf("user quota %d -> %d: the pre-consumed quota must be handed back exactly once", quotaBefore, quotaAfter)
	}
}

// TestRelay_ClientHangUpIsNotAChannelFailure: a caller who disconnects
// mid-stream says nothing against the upstream. The handlers return nil for it
// (no frame: nobody to write to) and the breaker must not count it as a
// failure; in half-open it must hand the probe slot back rather than close the
// breaker on a request that proved nothing.
func TestRelay_ClientHangUpIsNotAChannelFailure(t *testing.T) {
	reg := swapChannelBreakers(t, resilience.Config{
		Threshold:       1,
		Timeout:         20 * time.Millisecond,
		HalfOpenTimeout: time.Hour,
	})
	const id = 424242

	// Closed breaker, threshold 1: a failure would open it.
	if !reg.Allow(id) {
		t.Fatal("fresh breaker must admit")
	}
	reportBreakerOutcome(id, nil, relaycommon.StreamEndClientGone)
	if got := reg.GetState(id).String(); got != "closed" {
		t.Fatalf("breaker = %q after a client hang-up, want closed", got)
	}

	// Half-open probe that ends in a client hang-up: slot handed back, breaker
	// not closed (that would claim the channel recovered) and not stuck half-open.
	reg.RecordFailure(id)
	time.Sleep(30 * time.Millisecond)
	if !reg.Allow(id) {
		t.Fatal("cooldown elapsed: the next request must be admitted as the probe")
	}
	reportBreakerOutcome(id, nil, relaycommon.StreamEndClientGone)
	if got := reg.GetState(id).String(); got != "open" {
		t.Errorf("breaker = %q after an inconclusive probe, want open (cooldown restarts; closed would claim the channel recovered, half_open would strand it)", got)
	}
}

// TestReportBreakerOutcome_Classification pins the three-way split, including
// the incomplete-stream error being a FAILURE.
func TestReportBreakerOutcome_Classification(t *testing.T) {
	reg := swapChannelBreakers(t, resilience.Config{Threshold: 1, Timeout: time.Hour, HalfOpenTimeout: time.Hour})

	incomplete := helper.IncompleteStreamError(&relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndUpstreamClosed})
	cases := []struct {
		name string
		err  *types.NewAPIError
		want string
	}{
		{"success", nil, "closed"},
		{"upstream 502", types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeBadResponse, http.StatusBadGateway), "open"},
		{"user 400", types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeInvalidRequest, http.StatusBadRequest), "closed"},
		{"incomplete stream (skip-retry 502)", incomplete, "open"},
	}
	for i, tc := range cases {
		id := 500000 + i
		reg.Allow(id)
		reportBreakerOutcome(id, tc.err, "")
		if got := reg.GetState(id).String(); got != tc.want {
			t.Errorf("%s: breaker = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A mid-stream truncation must never auto-disable the channel: one cut answer
// is not grounds for taking the channel out of rotation (that is the breaker's
// job). ShouldDisableChannel already refuses skip-retry errors; this pins it
// for the surfaced error with auto-disable ON, so a future rule that matches
// the code or the 502 cannot start banning channels over truncated streams.
func TestSurfacedIncompleteStream_NeverAutoDisablesChannel(t *testing.T) {
	prev := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = prev })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndUpstreamClosed}
	apiErr := helper.SurfaceIncompleteStream(c, types.RelayFormatOpenAI, info)
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeGemini} {
		if app.ShouldDisableChannel(channelType, apiErr) {
			t.Errorf("channel type %d: a truncated stream must not auto-disable the channel", channelType)
		}
	}
}
