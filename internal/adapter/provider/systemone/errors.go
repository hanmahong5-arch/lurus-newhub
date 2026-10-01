package systemone

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

const (
	// maxErrorBodyBytes bounds how much of an error body is read; error
	// bodies are one line of JSON, so anything larger is not worth keeping.
	maxErrorBodyBytes = 64 << 10
	// maxDetailRunes bounds the upstream text echoed to the caller, so an
	// upstream cannot inflate our error responses.
	maxDetailRunes = 500
	// maxRetryAfterSeconds caps a hint so a bad upstream cannot park a
	// channel for days.
	maxRetryAfterSeconds = 3600
	// minRedactKeyLen is the shortest key worth redacting from echoed text; a
	// one-character placeholder key would otherwise mangle every message.
	minRedactKeyLen = 8
)

// SystemOneError maps a non-200 upstream reply onto the relay layer's error
// vocabulary. Three things drive what the relay does with the result:
//
//   - a "channel:" code is retried on another channel and auto-disables the
//     channel: credential and endpoint faults, which are our configuration;
//   - skipRetry plus a 4xx status is the caller's own mistake: never retried,
//     never counted against the channel, and the upstream explanation is
//     passed on because it names the offending question;
//   - everything else is an upstream failure (retried, charged to the breaker),
//     except 429 which is retried without charging it.
//
// Upstream auth failures are deliberately NOT passed through as 401/403: the
// caller would conclude their own hub key is wrong. Server-side bodies (5xx,
// edge pages, auth) are never echoed.
func (a *Adaptor) SystemOneError(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) *types.NewAPIError {
	if resp == nil {
		return types.NewErrorWithStatusCode(errors.New("systemone upstream returned no response"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	var body []byte
	if resp.Body != nil {
		// A body that cannot be read only loses the explanation; the status
		// alone still classifies the failure.
		body, _ = io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		app.CloseResponseBodyGracefully(resp)
	}

	apiErr := classify(resp.StatusCode, body, info)
	if secs := retryAfterSeconds(resp.Header); secs > 0 {
		// relay.go forwards only this header from UpstreamHeader, and only
		// in seconds, so retry-after-ms is converted here.
		apiErr.UpstreamHeader = http.Header{"Retry-After": []string{strconv.Itoa(secs)}}
	}
	return apiErr
}

func classify(status int, body []byte, info *relaycommon.RelayInfo) *types.NewAPIError {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		msg := upstreamDetail(body, info)
		if msg == "" {
			msg = fmt.Sprintf("systemone upstream rejected the request (status %d)", status)
		}
		return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeInvalidRequest, status, types.ErrOptionWithSkipRetry())
	case http.StatusUnauthorized, http.StatusForbidden:
		return channelFault(fmt.Sprintf("systemone upstream rejected the channel credential (status %d)", status))
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return channelFault(fmt.Sprintf("systemone upstream endpoint not found (status %d); check the channel base URL", status))
	case http.StatusTooManyRequests:
		return statusError(fmt.Sprintf("systemone upstream rate limit reached (status %d)", status), http.StatusTooManyRequests)
	case http.StatusServiceUnavailable, 529:
		return statusError(fmt.Sprintf("systemone upstream is overloaded (status %d)", status), http.StatusServiceUnavailable)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout, 524:
		return statusError(fmt.Sprintf("systemone upstream timed out (status %d)", status), http.StatusGatewayTimeout)
	default:
		return statusError(fmt.Sprintf("systemone upstream returned status %d", status), http.StatusBadGateway)
	}
}

// channelFault marks a problem with how the channel is configured. The code
// carries the "channel:" prefix, which is what makes the relay fail over and
// auto-disable the channel.
func channelFault(msg string) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeChannelInvalidKey, http.StatusBadGateway)
}

func statusError(msg string, status int) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, status)
}

// upstreamDetail extracts the human explanation from the three shapes seen in
// the wild: {"detail":"text"}, {"detail":{"message":"text"}} and FastAPI's
// {"detail":[{"loc":[...],"msg":"text"}]}. It returns "" when there is none.
func upstreamDetail(body []byte, info *relaycommon.RelayInfo) string {
	var env struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &env) != nil || len(env.Detail) == 0 {
		return ""
	}
	var asString string
	if json.Unmarshal(env.Detail, &asString) == nil {
		return sanitizeDetail(asString, info)
	}
	var asObject struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(env.Detail, &asObject) == nil {
		return sanitizeDetail(asObject.Message, info)
	}
	var asArray []struct {
		Loc []any  `json:"loc"`
		Msg string `json:"msg"`
	}
	if json.Unmarshal(env.Detail, &asArray) != nil {
		return ""
	}
	parts := make([]string, 0, len(asArray))
	for _, e := range asArray {
		locs := make([]string, 0, len(e.Loc))
		for _, l := range e.Loc {
			locs = append(locs, fmt.Sprint(l))
		}
		if len(locs) == 0 {
			parts = append(parts, e.Msg)
			continue
		}
		parts = append(parts, strings.Join(locs, ".")+": "+e.Msg)
	}
	return sanitizeDetail(strings.Join(parts, "; "), info)
}

// sanitizeDetail makes upstream text safe to hand to the caller: the channel
// key is redacted if the upstream echoed it, control characters become spaces
// and the length is bounded on a rune boundary.
func sanitizeDetail(text string, info *relaycommon.RelayInfo) string {
	if info != nil && len(info.ApiKey) >= minRedactKeyLen {
		text = strings.ReplaceAll(text, info.ApiKey, "[redacted]")
	}
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > maxDetailRunes {
		text = string(r[:maxDetailRunes]) + "..."
	}
	return text
}

// retryAfterSeconds reads the upstream wait hint. The hosted SDK prefers
// retry-after-ms over Retry-After (seconds or an HTTP date), so the same order
// is used here. Unusable values yield 0 (no hint); sub-second hints round up so
// the caller never retries early.
func retryAfterSeconds(h http.Header) int {
	if h == nil {
		return 0
	}
	if secs := positiveSeconds(h.Get("Retry-After-Ms"), 1000); secs > 0 {
		return secs
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if secs := positiveSeconds(v, 1); secs > 0 {
		return secs
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return clampSeconds(math.Ceil(d.Seconds()))
		}
	}
	return 0
}

func positiveSeconds(v string, perSecond float64) int {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return 0
	}
	return clampSeconds(math.Ceil(f / perSecond))
}

func clampSeconds(s float64) int {
	if s > maxRetryAfterSeconds {
		return maxRetryAfterSeconds
	}
	return int(s)
}
