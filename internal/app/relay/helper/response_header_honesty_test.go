package helper

// response_header_honesty_test.go — cycle-14 L6 (PC and PD).
//
// PC: X-Model-Provider was set from constant.GetChannelTypeName(ChannelType),
// which is the channel's configured ADAPTER FAMILY (the wire dialect we speak
// upstream), never a measurement of who serves the model. A model served by
// one vendor through an OpenAI-compatible channel reported the other vendor's
// name on a live instance.
//
// PD: streamed responses carried none of the documented perception headers,
// because doRequest (provider/api_request.go) calls SetEventStreamHeaders
// with no RelayInfo before the upstream call, which trips the
// "event_stream_headers_set" flag, so the later call from
// StreamScannerHandler — the one that DOES have the RelayInfo — returned at
// the guard without setting anything.

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// deepSeekOverOpenAIWire is the live case: a DeepSeek model reached through a
// channel typed as OpenAI (an OpenAI-compatible base URL). The gateway knows
// the adapter family; it does not know, and cannot measure, who serves the
// model.
func deepSeekOverOpenAIWire() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "deepseek-chat",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://api.deepseek.com",
			UpstreamModelName: "deepseek-chat",
		},
	}
}

// TestSetPerceptionHeaders_ReportsTheAdapterNotAProvider is the PC oracle.
func TestSetPerceptionHeaders_ReportsTheAdapterNotAProvider(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	SetPerceptionHeaders(c, deepSeekOverOpenAIWire(), &types.LurusUsageExtension{CostLB: 0.25, BalanceRemaining: 9.75})

	if got := w.Header().Get("X-Model-Provider"); got != "" {
		t.Errorf("X-Model-Provider = %q — the gateway does not measure who serves the model; this header claimed %q for a deepseek-chat call", got, got)
	}
	if got := w.Header().Get("X-Relay-Adapter"); got != "OpenAI" {
		t.Errorf("X-Relay-Adapter = %q, want OpenAI (the adapter family the channel is configured as, which is what this value has always been)", got)
	}
}

// TestSetEventStreamHeaders_ReportsTheAdapterNotAProvider is the same check on
// the streaming path.
func TestSetEventStreamHeaders_ReportsTheAdapterNotAProvider(t *testing.T) {
	c, w := writeCtx()
	SetEventStreamHeaders(c, deepSeekOverOpenAIWire())

	if got := w.Header().Get("X-Model-Provider"); got != "" {
		t.Errorf("X-Model-Provider = %q on a stream — the mis-named header must be gone from both paths", got)
	}
	if got := w.Header().Get("X-Relay-Adapter"); got != "OpenAI" {
		t.Errorf("X-Relay-Adapter = %q, want OpenAI", got)
	}
}

// TestSetEventStreamHeaders_FillsPerceptionHeadersAfterAnEarlierBareCall is
// the PD oracle. It replays production's real call order:
//
//	provider/api_request.go doRequest -> SetEventStreamHeaders(c)          // no info
//	helper/stream_scanner.go          -> SetEventStreamHeaders(c, info)    // has info
//
// Before the fix the second call returned at the "already set" guard, so a
// streamed response carried no adapter header at all — while every direct
// single-call test in this package passed.
func TestSetEventStreamHeaders_FillsPerceptionHeadersAfterAnEarlierBareCall(t *testing.T) {
	c, w := writeCtx()
	c.Set(common.RequestIdKey, "rid-stream-1")

	SetEventStreamHeaders(c)                           // doRequest, before the upstream call
	SetEventStreamHeaders(c, deepSeekOverOpenAIWire()) // StreamScannerHandler, once info exists

	if got := w.Header().Get("X-Relay-Adapter"); got != "OpenAI" {
		t.Errorf("X-Relay-Adapter = %q after the real two-call sequence, want OpenAI — the second call must still fill in what the first could not know", got)
	}
	if got := w.Header().Get("X-Request-Id"); got != "rid-stream-1" {
		t.Errorf("X-Request-Id = %q, want rid-stream-1", got)
	}
	// The static SSE headers must not be disturbed by the fill-in.
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := w.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
}

// TestPerceptionHeaderSets_StreamingVersusNonStreaming states the asymmetry
// explicitly so it cannot drift silently: what a streamed response carries,
// what a buffered one carries, and the header that tells a caller which of
// the two they are holding.
func TestPerceptionHeaderSets_StreamingVersusNonStreaming(t *testing.T) {
	t.Run("non-streaming carries the cost in headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		c.Set(common.RequestIdKey, "rid-buffered")

		SetPerceptionHeaders(c, deepSeekOverOpenAIWire(),
			&types.LurusUsageExtension{CostLB: 1.25, BalanceRemaining: 8.5})

		for name, want := range map[string]string{
			"X-Request-Cost":    "1.25",
			"X-Quota-Remaining": "8.5",
			"X-Relay-Adapter":   "OpenAI",
			"X-Request-Id":      "rid-buffered",
			"X-Cost-Reporting":  "headers",
		} {
			if got := w.Header().Get(name); got != want {
				t.Errorf("non-streaming %s = %q, want %q", name, got, want)
			}
		}
	})

	t.Run("streaming says where the cost will be instead", func(t *testing.T) {
		c, w := writeCtx()
		c.Set(common.RequestIdKey, "rid-streamed")

		SetEventStreamHeaders(c, deepSeekOverOpenAIWire())

		for name, want := range map[string]string{
			"X-Relay-Adapter":  "OpenAI",
			"X-Request-Id":     "rid-streamed",
			"X-Cost-Reporting": "final-usage-frame",
		} {
			if got := w.Header().Get(name); got != want {
				t.Errorf("streaming %s = %q, want %q", name, got, want)
			}
		}
		// Not knowable before the first frame is flushed; saying so beats
		// emitting a number that means something different from the one the
		// buffered path emits.
		for _, name := range []string{"X-Request-Cost", "X-Quota-Remaining"} {
			if got := w.Header().Get(name); got != "" {
				t.Errorf("streaming %s = %q, want absent — the cost is not known when these headers must be flushed", name, got)
			}
		}
	})
}
