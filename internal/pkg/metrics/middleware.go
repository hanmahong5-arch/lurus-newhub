package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// httpMethodWhitelist is the fixed set of method label values RequestsTotal
// and RequestDuration will ever carry. Before this, the label was
// c.Request.Method verbatim: gin's router accepts arbitrary verbs on an
// unmatched route (there is no auth or rate limit ahead of this middleware —
// it runs on every request, including ones gin never routes anywhere), so an
// unauthenticated caller could grow the method label's cardinality without
// bound simply by sending a new made-up verb on each request (cycle-13 §1.2
// finding 15). Collapsing anything outside this list to "other" bounds that
// cardinality at len(httpMethodWhitelist)+1 regardless of what a caller
// sends.
var httpMethodWhitelist = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodPost:    true,
	http.MethodPut:     true,
	http.MethodPatch:   true,
	http.MethodDelete:  true,
	http.MethodOptions: true,
	http.MethodTrace:   true,
	http.MethodConnect: true,
}

// normalizeMethodLabel maps an HTTP request method to the value it is
// allowed to carry as the "method" label: itself if it is one of the nine
// standard verbs in httpMethodWhitelist, otherwise the literal string
// "other". See httpMethodWhitelist's doc comment for why this exists.
func normalizeMethodLabel(method string) string {
	if httpMethodWhitelist[method] {
		return method
	}
	return "other"
}

// Middleware returns a Gin middleware that records request metrics
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Track active connections
		ActiveConnections.Inc()
		defer ActiveConnections.Dec()

		// Process request
		c.Next()

		// Record metrics after request completes
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}
		method := normalizeMethodLabel(c.Request.Method)

		RequestsTotal.WithLabelValues(method, path, status).Inc()
		RequestDuration.WithLabelValues(method, path).Observe(duration)
	}
}
