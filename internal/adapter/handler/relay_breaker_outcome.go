package handler

import (
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// reportBreakerOutcome closes the loop that channelBreakers.Allow opens.
// EVERY request the breaker admits must report back exactly once: in
// HalfOpen the admission consumed the single probe slot, and only a report
// gives it back. Until cycle 14 the sole report was RecordFailure on
// IsUpstreamFailure, so a probe that ended in a user 4xx, a 402 or a
// cancelled request reported nothing at all and left the channel parked in
// HalfOpen — excluded from routing until the process restarted.
// The three outcomes stay distinct on purpose:
//   - nil            → RecordSuccess, the upstream answered; breaker closes.
//   - upstream fault → RecordFailure, counts toward the trip threshold.
//   - anything else  → RecordInconclusive, hands the probe slot back
//     WITHOUT counting a failure, so a caller's own bad request still
//     cannot trip a healthy channel (that rule predates this cycle and
//     TestRelay_UserErrorDoesNotTripHealthyChannel keeps it).
//
// endReason is RelayInfo.StreamEndReason. A nil error with
// StreamEndClientGone is the third kind of "nothing learned": the caller hung
// up mid-stream, the stream handlers return no error for that (nobody is left
// to tell), and it is not evidence either way — recording it as a success
// would close a half-open breaker on a request that proved nothing, recording
// it as a failure would punish the channel for the caller's disconnect.
//
// An upstream that cut the stream AFTER bytes were delivered arrives here as
// the handlers' incomplete-stream error (502/504), which IsUpstreamFailure
// already counts: it used to arrive as nil and be recorded as a success.
func reportBreakerOutcome(channelID int, err *types.NewAPIError, endReason string) {
	// The same single choke point feeds the per-channel traffic series
	// (lurus_channel_requests_total / errors_total). A caller hang-up proved
	// nothing about the channel, so it is not an attempt there either.
	if err != nil || endReason != relaycommon.StreamEndClientGone {
		recordChannelAttempt(channelID, err)
	}
	switch {
	case err == nil && endReason == relaycommon.StreamEndClientGone:
		channelBreakers.RecordInconclusive(channelID)
	case err == nil:
		channelBreakers.RecordSuccess(channelID)
	case types.IsUpstreamFailure(err):
		channelBreakers.RecordFailure(channelID)
	default:
		channelBreakers.RecordInconclusive(channelID)
	}
}
