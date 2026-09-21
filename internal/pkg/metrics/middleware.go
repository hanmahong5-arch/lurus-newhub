package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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

// kubernetesProbePaths are the route templates (c.FullPath() values, see
// internal/adapter/handler/router/api-router.go) that carry nothing but
// Kubernetes probe traffic: /api/health is the readiness deep check and
// /api/status is the startup + liveness check. Both answer 503 from the
// moment a shutdown signal is observed until the process exits
// (internal/lifecycle/drain.go, cycle-13 L11), which is a deploy artifact
// rather than an availability incident — NonProbe5xxTotal below is what
// keeps that out of the 5xx alarm. Other paths a human might also call
// "a probe" (/api/uptime/status, the model-test endpoints) are not in this
// map: they are not wired to a kubelet probe and their 5xx are real.
//
// The cost of listing /api/status here, stated rather than implied: it is
// ALSO the console's bootstrap call (web/src reads it on every page load for
// the option snapshot), so a genuine 500 on it is customer-visible and, by
// this exclusion, unalarmed. Accepted for now because the alarm layer cannot
// express "not during a rollout" — the drain gate is what makes the
// exclusion necessary at all. Re-including it once netdata can be told
// about rollouts is an owner item (cycle-13 plan §8, O-scrape).
var kubernetesProbePaths = map[string]bool{
	"/api/health": true,
	"/api/status": true,
}

// NonProbe5xxTotal counts responses with a 5xx status on every route EXCEPT
// kubernetesProbePaths — the "customer-visible 5xx floor" that
// newhub_relay_5xx_elevated (deploy/r6-host-netdata/health.d/newhub.conf) is
// bound to.
//
// It exists because that alarm used to watch RequestsTotal with a
// `chart labels: status=5*` filter, which the operator's 2026-09-15 live
// check found matched exactly one chart — path=/api/health, status=503 —
// and cycle-13 L11 then made both probe routes answer 503 for the whole
// drain window. Expressing "5xx AND not a probe path" as a chart-label
// pattern needs two label conditions to hold at once, and how netdata
// combines them is live behaviour this repo cannot check from a checkout
// (see the conf file's own GATE GAP note). Doing the split here makes it
// testable instead, and leaves the alarm with no pattern to get wrong.
//
// Deliberately unlabelled: one chart, one aggregate rate. The per-path and
// per-status breakdown for triage is already on RequestsTotal, which is
// unchanged and still counts probe traffic.
var NonProbe5xxTotal = promauto.NewCounter(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "non_probe_5xx_total",
		Help:      "5xx responses on routes other than the Kubernetes probe paths (/api/health, /api/status)",
	},
)

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
		code := c.Writer.Status()
		status := strconv.Itoa(code)
		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}
		method := normalizeMethodLabel(c.Request.Method)

		RequestsTotal.WithLabelValues(method, path, status).Inc()
		RequestDuration.WithLabelValues(method, path).Observe(duration)
		if code >= 500 && !kubernetesProbePaths[path] {
			NonProbe5xxTotal.Inc()
		}
	}
}
