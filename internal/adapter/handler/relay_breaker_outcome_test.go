package handler

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// swapChannelBreakers replaces the process-wide breaker registry for one test
// with one whose windows are measured in milliseconds, and puts the original
// back afterwards. The production registry's 30s cooldown is not testable in a
// unit test, and reaching into it would leak state into every other test in
// this binary (see the -shuffle story in relay_success_fixture_test.go).
func swapChannelBreakers(t *testing.T, cfg resilience.Config) *resilience.Registry {
	t.Helper()
	prev := channelBreakers
	fresh := resilience.NewRegistry(cfg)
	channelBreakers = fresh
	t.Cleanup(func() { channelBreakers = prev })
	return fresh
}

// pinRetryTimes fixes the failover budget for one test. common.RetryTimes is a
// deploy-tunable global; these tests must describe the behaviour at a KNOWN
// budget rather than inherit whatever the current default happens to be.
func pinRetryTimes(t *testing.T, n int) {
	t.Helper()
	prev := common.RetryTimes
	common.RetryTimes = n
	t.Cleanup(func() { common.RetryTimes = prev })
}

// userErrorThenSuccessUpstream answers the first request with a caller-side
// 400 (a malformed request — the channel is perfectly healthy) and every later
// request with a normal completion. calls reports how many times it was hit.
func userErrorThenSuccessUpstream(calls *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":{"message":"your request is malformed","type":"invalid_request_error","code":"invalid_request_error"}}`)
			return
		}
		openAIChatEchoUpstream(w, r)
	}
}

// TestRelay_AdmittedRequestAlwaysReportsAnOutcome is the oracle for cycle 14
// L1-A(ii). The breaker admits a HALF-OPEN probe and then refuses every other
// caller until that probe reports. The relay loop used to report only when
// types.IsUpstreamFailure was true, so a probe that ended in a caller-side 4xx
// reported NOTHING — and since half-open has no other exit, the channel was
// excluded from routing until the process restarted.
//
// The half-open bound is deliberately set to an hour here so it cannot rescue
// the test: the ONLY way out of half-open in this test is the relay reporting
// an outcome.
func TestRelay_AdmittedRequestAlwaysReportsAnOutcome(t *testing.T) {
	pinRetryTimes(t, 0) // exactly one attempt per request
	reg := swapChannelBreakers(t, resilience.Config{
		Threshold:       1,
		Timeout:         20 * time.Millisecond,
		HalfOpenTimeout: time.Hour,
	})

	var calls atomic.Int32
	ctx := setupRelaySuccessRouter(t, userErrorThenSuccessUpstream(&calls))
	body := `{"model":"` + ctx.channel.Models + `","messages":[{"role":"user","content":"hi"}]}`

	reg.RecordFailure(ctx.channel.Id) // → Open
	if got := reg.GetState(ctx.channel.Id).String(); got != "open" {
		t.Fatalf("setup: breaker state = %q, want open", got)
	}
	time.Sleep(30 * time.Millisecond) // cooldown elapsed: the next request is THE probe

	w := ctx.postChat(t, body, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("probe request: status = %d, want 400 (the upstream rejected the caller's own request); body=%s",
			w.Code, w.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 — the probe must actually have been sent", got)
	}
	if got := reg.GetState(ctx.channel.Id).String(); got == "half_open" {
		t.Fatalf("breaker is still half_open after the probe finished: the request the breaker "+
			"admitted reported no outcome, so this channel is now excluded from routing forever "+
			"(upstream calls=%d)", calls.Load())
	} else if got != "open" {
		t.Fatalf("breaker state = %q, want open — an inconclusive probe hands the slot back and "+
			"restarts the cooldown", got)
	}

	// And the channel comes back: one cooldown later the next request is
	// admitted as a fresh probe, succeeds, and closes the breaker.
	time.Sleep(30 * time.Millisecond)
	w2 := ctx.postChat(t, body, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("follow-up request: status = %d, want 200; body=%s", w2.Code, w2.Body.String())
	}
	if got := reg.GetState(ctx.channel.Id).String(); got != "closed" {
		t.Errorf("breaker state = %q, want closed after a successful probe", got)
	}
}

// TestRelay_UserErrorDoesNotTripHealthyChannel guards the rule that must
// SURVIVE the fix above: a caller-side 4xx says nothing about the upstream, so
// it must not count toward the trip threshold. The threshold here is 1 — the
// most sensitive setting there is — so a single mis-accounted failure opens the
// breaker and the test fails.
//
// This behaviour was already correct before cycle 14 (the old code simply
// recorded nothing for a non-upstream failure); the test exists so the new
// RecordInconclusive path cannot quietly become RecordFailure.
func TestRelay_UserErrorDoesNotTripHealthyChannel(t *testing.T) {
	pinRetryTimes(t, 0)
	reg := swapChannelBreakers(t, resilience.Config{
		Threshold:       1,
		Timeout:         time.Hour,
		HalfOpenTimeout: time.Hour,
	})

	var calls atomic.Int32
	ctx := setupRelaySuccessRouter(t, userErrorThenSuccessUpstream(&calls))
	body := `{"model":"` + ctx.channel.Models + `","messages":[{"role":"user","content":"hi"}]}`

	w := ctx.postChat(t, body, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if got := reg.GetState(ctx.channel.Id).String(); got != "closed" {
		t.Fatalf("breaker state = %q, want closed — one caller's bad request must never take a "+
			"healthy channel out of rotation for everyone else", got)
	}

	// Still routable, immediately, with no cooldown to wait out.
	w2 := ctx.postChat(t, body, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("follow-up status = %d, want 200 (the channel was never unhealthy); body=%s",
			w2.Code, w2.Body.String())
	}
	if got := reg.GetState(ctx.channel.Id).String(); got != "closed" {
		t.Errorf("breaker state = %q, want closed", got)
	}
}

// TestRelay_ChannelScopedSelectionErrorFailsOver is the oracle for cycle 14
// L1-C. getChannel can fail AFTER picking a candidate:
// middleware.SetupContextForSelectedChannel calls Channel.GetNextEnabledKey,
// which returns channel:no_available_key (repo/channel.go:160) or
// channel:all_keys_cooling (:104) when that one multi-key channel has no
// usable key left. The relay loop broke out on ANY selection error, so one
// exhausted channel hard-failed the request even though other channels serve
// the same model — the exact failure shouldRetry grants a retry for once a
// channel HAS been called.
//
// Effect in production depends on the failover budget: with RetryTimes 0 there
// is no next attempt to continue to. This test pins the budget at 1.
func TestRelay_ChannelScopedSelectionErrorFailsOver(t *testing.T) {
	pinRetryTimes(t, 1)
	swapChannelBreakers(t, resilience.Config{
		Threshold:       5,
		Timeout:         time.Hour,
		HalfOpenTimeout: time.Hour,
	})

	var upstreamCalls atomic.Int32
	ctx := setupRelaySuccessRouter(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		openAIChatEchoUpstream(w, r)
	})

	const exhaustedChannelID = 777000777
	var selections atomic.Int32
	prevGetChannel := getChannelFn
	getChannelFn = func(c *gin.Context, info *relaycommon.RelayInfo, rp *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
		if selections.Add(1) == 1 {
			// Reproduce what SetupContextForSelectedChannel leaves behind when
			// GetNextEnabledKey fails: the candidate is already stamped on the
			// context, then the channel-scoped error comes back.
			common.SetContextKey(c, constant.ContextKeyChannelId, exhaustedChannelID)
			common.SetContextKey(c, constant.ContextKeyChannelName, "exhausted-multikey")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			return nil, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
		}
		// The next candidate is healthy.
		if apiErr := middleware.SetupContextForSelectedChannel(c, ctx.channel, info.OriginModelName); apiErr != nil {
			return nil, apiErr
		}
		return ctx.channel, nil
	}
	t.Cleanup(func() { getChannelFn = prevGetChannel })

	w := ctx.postChat(t, `{"model":"`+ctx.channel.Models+`","messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — one channel with no usable key must not fail a request "+
			"that another channel can serve; selections=%d upstream_calls=%d body=%s",
			w.Code, selections.Load(), upstreamCalls.Load(), w.Body.String())
	}
	if got := selections.Load(); got != 2 {
		t.Errorf("channel selections = %d, want 2 (the exhausted one, then the healthy one)", got)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1", got)
	}

	var logs []repo.Log
	if err := ctx.db.Where("type = ?", repo.LogTypeConsume).Find(&logs).Error; err != nil {
		t.Fatalf("query consume logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("consume log rows = %d, want 1 — the failover must still settle normally", len(logs))
	}
}

// TestRelay_NoAvailableChannelStillAborts is the other half of the rule above:
// "no channel at all for this model" (ErrorCodeGetChannelFailed, which
// getChannel marks SkipRetry) has nothing to fail over to, so it must still
// end the request immediately instead of burning the retry budget.
func TestRelay_NoAvailableChannelStillAborts(t *testing.T) {
	pinRetryTimes(t, 2)
	swapChannelBreakers(t, resilience.Config{Threshold: 5, Timeout: time.Hour, HalfOpenTimeout: time.Hour})

	var upstreamCalls atomic.Int32
	ctx := setupRelaySuccessRouter(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		openAIChatEchoUpstream(w, r)
	})

	var selections atomic.Int32
	prevGetChannel := getChannelFn
	getChannelFn = func(c *gin.Context, info *relaycommon.RelayInfo, rp *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
		selections.Add(1)
		return nil, types.NewError(errors.New("no available channel"), types.ErrorCodeGetChannelFailed,
			types.ErrOptionWithSkipRetry())
	}
	t.Cleanup(func() { getChannelFn = prevGetChannel })

	w := ctx.postChat(t, `{"model":"`+ctx.channel.Models+`","messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want an error; body=%s", w.Body.String())
	}
	if got := selections.Load(); got != 1 {
		t.Errorf("channel selections = %d, want 1 — there is nothing to fail over to", got)
	}
	if got := upstreamCalls.Load(); got != 0 {
		t.Errorf("upstream calls = %d, want 0", got)
	}
}
