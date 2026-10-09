package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

// A retry-time selection that finds every channel cooling surfaces as 503 with
// a Retry-After deadline under its own code; skip-retry so the loop stops.
func TestChannelSelectionError_AllCoolingIs503WithRetryAfter(t *testing.T) {
	cooling := fmt.Errorf("select: %w", &app.AllChannelsCoolingError{RetryAfterUnix: time.Now().Unix() + 45})
	got := channelSelectionError(cooling, "m", "g")
	if got.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got.StatusCode)
	}
	if got.GetErrorCode() != types.ErrorCodeAllChannelsCooling {
		t.Fatalf("code = %q, want %q", got.GetErrorCode(), types.ErrorCodeAllChannelsCooling)
	}
	if secs := got.RetryAfterUnix - time.Now().Unix(); secs < 30 || secs > 46 {
		t.Fatalf("Retry-After window = %ds, want ~45", secs)
	}
	if !types.IsSkipRetryError(got) {
		t.Fatal("cooling selection error must not be retried")
	}
}

func TestChannelSelectionError_OtherErrorsKeepGetChannelFailed(t *testing.T) {
	got := channelSelectionError(errors.New("boom"), "m", "g")
	if got.GetErrorCode() != types.ErrorCodeGetChannelFailed || got.RetryAfterUnix != 0 {
		t.Fatalf("code=%q retryAfter=%d, want get_channel_failed/0", got.GetErrorCode(), got.RetryAfterUnix)
	}
}

// A retry must be able to get past a sibling whose breaker is open. The relay
// loop skips an Open-breaker channel without calling it, and selection excludes
// only channels recorded in "use_channel": if the skipped channel is not
// recorded, every retry re-selects it, burns the budget on the skip, and the
// healthy lower-priority channel is never reached.
func TestRelay_BreakerOpenSiblingIsExcludedOnRetry(t *testing.T) {
	pinRetryTimes(t, 3)
	reg := swapChannelBreakers(t, resilience.Config{Threshold: 2, Timeout: time.Hour, HalfOpenTimeout: time.Hour})

	var calls atomic.Int32
	ctx := setupRelaySuccessRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"error":{"message":"boom","type":"server_error","code":"server_error"}}`)
			return
		}
		openAIChatEchoUpstream(w, r)
	})

	a1 := ctx.channel
	a2 := *ctx.channel
	a2.Id = a1.Id + 1000
	b := *ctx.channel
	b.Id = a1.Id + 2000
	reg.RecordFailure(a2.Id)
	reg.RecordFailure(a2.Id)
	if got := reg.GetState(a2.Id).String(); got != "open" {
		t.Fatalf("setup: sibling breaker = %q, want open", got)
	}

	// Priority order A1, A2, B; like the real selector it only skips channels
	// the request has already recorded.
	order := []*repo.Channel{a1, &a2, &b}
	var picked []int
	prev := getChannelFn
	getChannelFn = func(c *gin.Context, info *relaycommon.RelayInfo, rp *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
		used := map[string]bool{}
		for _, id := range c.GetStringSlice("use_channel") {
			used[id] = true
		}
		for _, ch := range order {
			if used[strconv.Itoa(ch.Id)] {
				continue
			}
			picked = append(picked, ch.Id)
			if apiErr := middleware.SetupContextForSelectedChannel(c, ch, info.OriginModelName); apiErr != nil {
				return nil, apiErr
			}
			return ch, nil
		}
		return nil, types.NewError(errors.New("no channel left"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	t.Cleanup(func() { getChannelFn = prev })

	w := ctx.postChat(t, `{"model":"`+ctx.channel.Models+`","messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 via the healthy third channel; picks=%v body=%s", w.Code, picked, w.Body.String())
	}
	want := []int{a1.Id, a2.Id, b.Id}
	if fmt.Sprint(picked) != fmt.Sprint(want) {
		t.Fatalf("selection order = %v, want %v (breaker-open sibling skipped once, then the healthy channel)", picked, want)
	}
}

// A 429 caused by the request itself ("Request too large ... must be reduced")
// would fail identically on every channel: the loop must hit the upstream once,
// hand the caller that 429 and leave no cooldown behind on any channel.
func TestRelay_RequestTooLarge429HitsUpstreamOnce(t *testing.T) {
	pinRetryTimes(t, 3)
	app.ClearChannelCooldowns()
	t.Cleanup(app.ClearChannelCooldowns)

	const msg = "Request too large for model `gpt-x` in organization org-1 on tokens per min (TPM): Limit 6000, Requested 9000. The input or output tokens must be reduced in order to run successfully."
	var calls atomic.Int32
	ctx := setupRelaySuccessRouter(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprintf(w, `{"error":{"message":%q,"type":"tokens","code":"rate_limit_exceeded"}}`, msg)
	})

	a := ctx.channel
	b := *ctx.channel
	b.Id = a.Id + 1000
	c3 := *ctx.channel
	c3.Id = a.Id + 2000
	order := []*repo.Channel{a, &b, &c3}
	prev := getChannelFn
	getChannelFn = func(c *gin.Context, info *relaycommon.RelayInfo, rp *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
		used := map[string]bool{}
		for _, id := range c.GetStringSlice("use_channel") {
			used[id] = true
		}
		for _, ch := range order {
			if used[strconv.Itoa(ch.Id)] {
				continue
			}
			if apiErr := middleware.SetupContextForSelectedChannel(c, ch, info.OriginModelName); apiErr != nil {
				return nil, apiErr
			}
			return ch, nil
		}
		return nil, types.NewError(errors.New("no channel left"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	t.Cleanup(func() { getChannelFn = prev })

	w := ctx.postChat(t, `{"model":"`+ctx.channel.Models+`","messages":[{"role":"user","content":"hi"}]}`, nil)
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream called %d times, want exactly 1", got)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want the upstream 429 verbatim; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "must be reduced") {
		t.Fatalf("caller did not get the upstream message: %s", w.Body.String())
	}
	for _, ch := range order {
		if app.ChannelCoolingUntil(ch.Id, 0) != 0 {
			t.Fatalf("channel %d was put on cooldown by a request-caused 429", ch.Id)
		}
	}
}
