package app

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

// Per-attempt routing trace.
//
// WHY this exists: when a tenant reports "some requests are slow or fail", the
// existing signals cannot answer the question. The log row records only the
// channel that FINALLY served the request (plus use_channel, a bare id list
// with no outcome), and the Prometheus counters are aggregates with no way back
// to an individual request. So the most common support question — "this request
// took 40s, what happened?" — has no answer: the two channels that timed out
// first left no trace anywhere.
//
// One attempt per channel tried, with why it was abandoned and how long it
// burned, turns that into a lookup. Recorded on the request's own log row so it
// is scoped, retained, and permission-gated exactly like the rest of the row.
//
// Deliberately NOT a separate table: attempts are only ever read alongside
// their request, and a per-attempt table on a gateway's write path is a cost
// (and a retention problem) with no query that justifies it.

// RouteAttempt is one upstream attempt within a single client request.
type RouteAttempt struct {
	ChannelID int `json:"channel_id"`
	// ChannelName is carried because channels get renamed and deleted; an id
	// alone makes an old log row unreadable.
	ChannelName string `json:"channel_name,omitempty"`
	Provider    string `json:"provider,omitempty"`
	// Outcome is "success", or the reason this attempt was abandoned:
	// "upstream_error" / "breaker_open".
	Outcome string `json:"outcome"`
	// ErrorCode/StatusCode are the gateway's classification and the upstream's
	// HTTP status. Empty/zero on success and on breaker skips (never dialled).
	ErrorCode  string `json:"error_code,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	// DurationMs is time spent on this attempt alone. Zero for breaker skips.
	DurationMs int64 `json:"duration_ms"`
}

// Attempt outcomes.
const (
	RouteAttemptOutcomeSuccess     = "success"
	RouteAttemptOutcomeUpstreamErr = "upstream_error"
	RouteAttemptOutcomeBreakerOpen = "breaker_open"
)

// maxRecordedRouteAttempts bounds the slice. Retries are already capped by
// RetryTimes, but breaker skips are not tied to that counter, so a pathological
// config could otherwise grow this without limit inside one request.
const maxRecordedRouteAttempts = 16

// RecordRouteAttempt appends one attempt to the request-scoped trace.
func RecordRouteAttempt(c *gin.Context, attempt RouteAttempt) {
	if c == nil {
		return
	}
	attempts := GetRouteAttempts(c)
	if len(attempts) >= maxRecordedRouteAttempts {
		return
	}
	c.Set(string(constant.ContextKeyRouteAttempts), append(attempts, attempt))
}

// GetRouteAttempts returns the attempts recorded so far (nil when none).
func GetRouteAttempts(c *gin.Context) []RouteAttempt {
	if c == nil {
		return nil
	}
	raw, ok := c.Get(string(constant.ContextKeyRouteAttempts))
	if !ok {
		return nil
	}
	attempts, ok := raw.([]RouteAttempt)
	if !ok {
		return nil
	}
	return attempts
}
