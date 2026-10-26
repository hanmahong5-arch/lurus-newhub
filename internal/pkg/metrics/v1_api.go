package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// V1APIRequests counts REST /api/* calls that carry Deprecation headers
// (excludes long-lived public boot surfaces). Used to track console/reseller
// migration off the v1 surface (doc/decisions/v1-deprecation.md Phase Monitor).
var V1APIRequests = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "v1_api_requests_total",
		Help:      "Deprecated REST /api/* requests by method and path bucket",
	},
	[]string{"method", "path"},
)
