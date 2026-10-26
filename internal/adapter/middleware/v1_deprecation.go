package middleware

import (
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// V1SunsetDate is the announced calendar date for REST /api/* removal
// (doc/decisions/v1-deprecation.md Phase 1). Relay OpenAI /v1/* is out of scope.
const V1SunsetDate = "2026-06-30"

// v1PublicLongLived prefixes stay on /api without Deprecation headers — they
// are boot/status surfaces the SPA and anonymous clients still need, and are
// explicitly carved out of the sunset in the ADR.
var v1PublicLongLived = []string{
	"/api/status",
	"/api/notice",
	"/api/health",
	"/api/pricing",
	"/api/setup",
	"/api/user-agreement",
	"/api/privacy-policy",
}

func isV1PublicLongLived(path string) bool {
	for _, p := range v1PublicLongLived {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// V1DeprecationHeaders injects Deprecation + Sunset on REST /api/* responses
// that are in the deprecation set, and increments a usage counter so migration
// progress is visible on /metrics. Mount on SetApiRouter only — never on
// relay /v1 or /api/v2.
func V1DeprecationHeaders() gin.HandlerFunc {
	sunsetHTTP := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC).Format(time.RFC1123)
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/api/v2") {
			c.Next()
			return
		}
		if isV1PublicLongLived(path) {
			c.Next()
			return
		}
		c.Header("Deprecation", "true")
		c.Header("Sunset", sunsetHTTP)
		c.Header("Link", `</docs/decisions/v1-deprecation.md>; rel="deprecation"; type="text/markdown"`)
		metrics.V1APIRequests.WithLabelValues(c.Request.Method, v1MetricPath(path)).Inc()
		c.Next()
	}
}

// v1MetricPath collapses /api/user/123 → /api/user/:id style buckets so the
// cardinality stays bounded.
func v1MetricPath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "" {
			continue
		}
		if isMostlyDigits(p) {
			parts[i] = ":id"
		}
	}
	out := strings.Join(parts, "/")
	if len(out) > 96 {
		return out[:96]
	}
	return out
}

func isMostlyDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
