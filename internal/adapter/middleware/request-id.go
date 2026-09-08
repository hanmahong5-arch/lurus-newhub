package middleware

import (
	"context"
	"regexp"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// validInboundRequestId matches a caller-supplied X-Request-Id we are willing
// to echo back and persist on the log row. Bounded length (8-36) keeps a
// hostile caller from making us store/echo an arbitrary blob and matches
// entity.AuditEvent.RequestID's varchar(36) column width (36 fits a UUID) —
// a longer id would make the audit INSERT fail (PG 22001) and silently
// suppress the caller's own audit row (governance/audit.go only SysLogs the
// write failure). Keep this in sync with that column if either changes. The
// charset excludes anything that would need escaping in a header value or a
// log filter query.
var validInboundRequestId = regexp.MustCompile(`^[A-Za-z0-9._-]{8,36}$`)

// traceparentPattern matches the W3C Trace Context header
// (version-traceid-parentid-flags, e.g.
// "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"). Only the
// 32-hex-char trace id (group 1) is used as a request id fallback — fixed at
// 32 chars, so it always fits under validInboundRequestId's 36-char cap
// without a separate length check.
var traceparentPattern = regexp.MustCompile(`^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$`)

// requestIdFromTraceparent extracts the trace id from a valid inbound
// traceparent header, or "" when the header is absent/malformed.
func requestIdFromTraceparent(header string) string {
	m := traceparentPattern.FindStringSubmatch(header)
	if m == nil {
		return ""
	}
	return m[1]
}

func RequestId() func(c *gin.Context) {
	return func(c *gin.Context) {
		var id string
		if inbound := c.Request.Header.Get(common.RequestIdHeader); validInboundRequestId.MatchString(inbound) {
			// Caller already logged this id on their own side before the call
			// left them; honouring it lets them correlate without a round trip.
			id = inbound
		} else if traceId := requestIdFromTraceparent(c.Request.Header.Get("traceparent")); traceId != "" {
			id = traceId
		} else {
			id = common.GetTimeString() + common.GetRandomString(8)
		}
		c.Set(common.RequestIdKey, id)
		ctx := context.WithValue(c.Request.Context(), common.RequestIdKey, id)
		c.Request = c.Request.WithContext(ctx)
		// X-Oneapi-Request-Id is the legacy name (== RequestIdKey); kept as an
		// alias for one release so existing consumers do not break mid-cycle.
		c.Header(common.RequestIdHeader, id)
		c.Header(common.RequestIdKey, id)
		c.Next()
	}
}
