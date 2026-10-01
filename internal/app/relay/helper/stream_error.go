package helper

import (
	"errors"
	"fmt"
	"net/http"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// StreamError surfaces a failure that happens after the response headers and
// some SSE frames have already gone to the caller. The status line is spent,
// so the only signal left is the wire's own in-band error frame, and each
// official SDK recognises exactly one shape:
//
//   - OpenAI: a data frame whose JSON carries an "error" key (openai-python
//     raises APIError on it); a terminal [DONE] follows as on a normal end.
//   - Anthropic: an SSE frame whose event line is "error" (anthropic-sdk-python
//     dispatches on sse.event and silently drops frames with no event line, so
//     a data-only {"type":"error"} frame never reaches the caller).
//   - Gemini: a data frame whose JSON starts with {"error": (google-genai
//     checks that prefix and raises APIError); no [DONE], which it would try to
//     json-decode.
func StreamError(c *gin.Context, format types.RelayFormat, apiErr *types.NewAPIError) {
	if c == nil || apiErr == nil {
		return
	}
	switch format {
	case types.RelayFormatClaude:
		_ = ClaudeData(c, dto.ClaudeResponse{Type: "error", Error: apiErr.ToClaudeError()})
	case types.RelayFormatGemini:
		_ = ObjectData(c, apiErr.ToGeminiError())
	default:
		_ = ObjectData(c, gin.H{"error": apiErr.ToOpenAIError()})
		Done(c)
	}
}

// The Gemini error envelope moved to internal/pkg/types (ToGeminiError /
// GeminiStatus): the non-streaming relay path needs the same shape, and it
// cannot import this package. No forwarding shim is left behind — one wire
// shape, one definition.

// ClientListening reports whether there is still a caller to write to: false
// once the request context is done or the scanner saw the caller hang up.
func ClientListening(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if info != nil && info.StreamEndReason == relaycommon.StreamEndClientGone {
		return false
	}
	return c != nil && c.Request != nil && c.Request.Context().Err() == nil
}

// IncompleteStreamError is the error a caller gets when the upstream stream
// stopped before signalling completion. 504 when the relay's own idle timeout
// fired, 502 otherwise; never retried (frames already left the process).
func IncompleteStreamError(info *relaycommon.RelayInfo) *types.NewAPIError {
	msg := "upstream stream ended before completion"
	status := http.StatusBadGateway
	if info != nil && info.StreamEndReason == relaycommon.StreamEndTimeout {
		msg = "upstream stream idle timeout before completion"
		status = http.StatusGatewayTimeout
	}
	return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeUpstreamStreamIncomplete, status, types.ErrOptionWithSkipRetry())
}

// surfacedIncompleteStream is the marker SurfaceIncompleteStream hangs on the
// error's cause. It is a type, not a message or a code, so
// IsIncompleteStreamSurfaced never has to guess from text: the same error code
// (upstream_stream_incomplete) is also carried by the retryable pre-first-byte
// error, which has NOT been rendered anywhere yet.
type surfacedIncompleteStream struct{ msg string }

func (e *surfacedIncompleteStream) Error() string { return e.msg }

// IsIncompleteStreamSurfaced reports whether err is the error
// SurfaceIncompleteStream returned: the in-band error frame has ALREADY been
// written to the caller's stream, so the relay's terminal error renderer must
// not write a second one (nor count the failure a second time: the
// relay_errors_total increment happened in ReportIncompleteStream).
func IsIncompleteStreamSurfaced(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	var marker *surfacedIncompleteStream
	return errors.As(err.Err, &marker)
}

// SurfaceIncompleteStream is the mid-stream answer to an upstream that stopped
// before completing after bytes already reached the caller: write the wire's
// in-band error frame (StreamError) exactly once and hand the same failure back
// as the handler's error. Returning it (instead of nil, as before) lets the
// relay loop treat the attempt as what it was: a breaker FAILURE, a failed
// route-attempt/request outcome and no billing — while shouldRetry still
// refuses to fail over (skip-retry, and bytes are written).
//
// Call it only for the case FailoverIncompleteStream declined with the caller
// still listening; a caller that hung up gets neither frame nor error. The
// failure is counted once in relay_errors_total (ReportIncompleteStream).
func SurfaceIncompleteStream(c *gin.Context, format types.RelayFormat, info *relaycommon.RelayInfo) *types.NewAPIError {
	apiErr := ReportIncompleteStream(c, info)
	StreamError(c, format, apiErr)
	apiErr.Err = &surfacedIncompleteStream{msg: apiErr.Error()}
	return apiErr
}

// FailoverIncompleteStream is the incomplete-stream answer for an attempt
// that has not yet put a single byte on the caller's connection. The status
// line is still unspent and the caller has seen nothing, so the right move is
// the one every other pre-first-byte upstream failure gets: hand a retryable
// error back to the relay loop, which fails over to another channel and
// records the failure on this channel's breaker. Writing an in-band error
// frame here instead (the mid-stream answer) spent the response on a request
// that nothing had been delivered for, and the nil return it came with was
// counted as a breaker success.
//
// It returns nil when the in-band path applies: the caller already received
// frames (a retry would append a second response to the same stream; the relay
// loop's shouldRetry refuses for the same reason) or the caller hung up.
//
// The error is deliberately NOT IncompleteStreamError: that one carries
// skip-retry (frames had left the process) and, for the idle-timeout case, a
// 504, which shouldRetry never retries. Neither is wanted here, so the status
// is always 502 and the timeout is only named in the message. It is also not a
// "channel:" code (those auto-disable the channel; one empty stream is not
// grounds for that). Nothing is billed: callers return zero usage with it.
func FailoverIncompleteStream(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if !ClientListening(c, info) || c.Writer.Written() {
		return nil
	}
	msg := "upstream stream ended before any response data"
	reason, model := "", "unknown"
	if info != nil {
		reason = info.StreamEndReason
		if info.StreamEndReason == relaycommon.StreamEndTimeout {
			msg = "upstream stream idle timeout before any response data"
		}
		if info.OriginModelName != "" {
			model = info.OriginModelName
		}
	}
	logger.LogWarn(c, fmt.Sprintf("upstream stream incomplete before first byte, failing over: reason=%s model=%s", reason, model))
	return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeUpstreamStreamIncomplete, http.StatusBadGateway)
}

// ReportIncompleteStream builds the incomplete-stream error and makes the
// event visible to operators before it goes to the caller. The relay's
// terminal-error path skips a surfaced incomplete stream (the frame is already
// out, see IsIncompleteStreamSurfaced), so this is the one place it is counted:
// without it the only trace was a consume-log note. The count lands in relay_errors_total under the same provider/model/type
// taxonomy as every other terminal failure (502 → upstream_5xx, the relay's
// own idle timeout → upstream_timeout), plus one error line carrying the
// reason. Call exactly once per abandoned stream.
func ReportIncompleteStream(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	err := IncompleteStreamError(info)
	provider, model, reason, product := "Unknown", "unknown", "", "unknown"
	if info != nil {
		reason = info.StreamEndReason
		if info.ChannelMeta != nil {
			provider = constant.GetChannelTypeName(info.ChannelType)
		}
		if info.OriginModelName != "" {
			model = info.OriginModelName
		}
		if info.SourceProduct != "" {
			product = info.SourceProduct
		}
	}
	metrics.RecordRelayError(provider, model, types.RelayErrorType(err), product)
	logger.LogError(c, fmt.Sprintf("upstream stream incomplete: reason=%s provider=%s model=%s", reason, provider, model))
	return err
}
