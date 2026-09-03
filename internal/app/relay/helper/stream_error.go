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
		oai := apiErr.ToOpenAIError()
		_ = ObjectData(c, geminiErrorFrame{Error: geminiError{
			Code:    apiErr.StatusCode,
			Message: oai.Message,
			Status:  geminiStatus(apiErr.StatusCode),
		}})
	default:
		_ = ObjectData(c, gin.H{"error": apiErr.ToOpenAIError()})
		Done(c)
	}
}

// geminiErrorFrame is the Gemini API error envelope ({"error":{code,message,
// status}}); a struct so "error" is the first key, which is what the SDK
// checks.
type geminiErrorFrame struct {
	Error geminiError `json:"error"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// geminiStatus maps an HTTP status to the google.rpc.Code name the Gemini API
// puts in error.status.
func geminiStatus(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	}
	return "UNKNOWN"
}

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

// ReportIncompleteStream builds the incomplete-stream error and makes the
// event visible to operators before it goes to the caller. The stream handler
// returns no error for an abandoned stream (the frames are out, billing is
// settled separately), so the relay's terminal-error path never sees it: the
// request counted as a success and the only trace was a consume-log note. The
// count now lands in relay_errors_total under the same provider/model/type
// taxonomy as every other terminal failure (502 → upstream_5xx, the relay's
// own idle timeout → upstream_timeout), plus one error line carrying the
// reason. Call exactly once per abandoned stream.
func ReportIncompleteStream(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	err := IncompleteStreamError(info)
	provider, model, reason := "Unknown", "unknown", ""
	if info != nil {
		reason = info.StreamEndReason
		if info.ChannelMeta != nil {
			provider = constant.GetChannelTypeName(info.ChannelType)
		}
		if info.OriginModelName != "" {
			model = info.OriginModelName
		}
	}
	metrics.RecordRelayError(provider, model, types.RelayErrorType(err))
	logger.LogError(c, fmt.Sprintf("upstream stream incomplete: reason=%s provider=%s model=%s", reason, provider, model))
	return err
}
