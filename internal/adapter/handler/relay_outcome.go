package handler

import (
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// relayOutcome classifies how a Relay() call ended, for the status label on
// relay_requests_total and relay_total_duration (relay_errors_total has no
// status label — it is keyed by error_type):
//
//   - apiErr != nil                                  -> "error"
//   - apiErr == nil AND endReason == client hung up   -> "client_gone"
//   - otherwise                                       -> "success"
//
// A stream the caller abandoned mid-flight leaves apiErr nil (the provider
// helpers skip the error frame when helper.ClientListening reports nobody is
// left to write to), so before this label existed such a request counted as
// an ordinary "success". Known remaining gap, unchanged here: an upstream
// stream truncated while the caller is still listening goes through
// helper.ReportIncompleteStream (relay_errors_total{error_type=upstream_*}
// +1 and a 502/504 frame to the caller) and then returns nil, so it still
// lands in "success" on these two series.
func relayOutcome(apiErr *types.NewAPIError, endReason string) string {
	if apiErr != nil {
		return "error"
	}
	if endReason == relaycommon.StreamEndClientGone {
		return "client_gone"
	}
	return "success"
}

// observeRelayOutcome is the one place both Relay() metric sites record an
// outcome: the end-to-end defer (total=true -> RecordRelayTotal,
// relay_total_duration_seconds) and the post-channel-selection recorder
// (total=false -> RecordRelayRequest, relay_requests_total). Both status and
// product are derived here, so the two series always carry the same labels
// for a given request.
//
// info may be nil (the total=true site can run before GenRelayInfo has
// produced a RelayInfo, e.g. request-binding failures). A nil info or an
// empty SourceProduct yields product="unknown" — the same fallback
// helper.ReportIncompleteStream uses for relay_errors_total.
//
// The total=true (end-to-end) call is also the one place time-to-first-token
// is observed: it fires once per request regardless of retries, whereas the
// total=false site runs once per channel attempt and would double-count a
// request that failed over. HasSendResponse() (relay_info.go) tells apart a
// request that actually received a first token from upstream from the
// "never happened" sentinel non-streaming and failed-before-first-byte
// requests seed FirstResponseTime with — only the former gets an
// observation; a request that got a first token and then failed or had the
// client disconnect still observes, since HasSendResponse only asks whether
// a first token arrived, not how the request ended. The duration is also
// guarded explicitly (> 0) below so a future change to the sentinel or to
// HasSendResponse's comparison cannot resurrect a zero/negative observation.
// OpenAI Realtime sessions (RelayFormatOpenAIRealtime) are excluded even
// when HasSendResponse is true — see ttft.go for why a bidirectional
// websocket session doesn't belong in a request-latency histogram.
func observeRelayOutcome(provider, model string, info *relaycommon.RelayInfo, apiErr *types.NewAPIError, seconds float64, total bool) {
	endReason := ""
	product := "unknown"
	if info != nil {
		endReason = info.StreamEndReason
		if info.SourceProduct != "" {
			product = info.SourceProduct
		}
	}
	status := relayOutcome(apiErr, endReason)
	if total {
		metrics.RecordRelayTotal(provider, model, status, product, seconds)
		if info != nil && info.RelayFormat != types.RelayFormatOpenAIRealtime && info.HasSendResponse() {
			if ttft := info.FirstResponseTime.Sub(info.StartTime).Seconds(); ttft > 0 {
				metrics.RecordTimeToFirstToken(provider, model, product, ttft)
			}
		}
	} else {
		metrics.RecordRelayRequest(provider, model, status, product, seconds)
	}
}
