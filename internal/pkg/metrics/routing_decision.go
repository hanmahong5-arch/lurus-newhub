package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RoutingDecisionReasons is the closed set of reason label values, identical
// to the suffixes of the X-Routing-Reason response header
// ("decision:<reason>"). A closed enum keeps the label cardinality fixed no
// matter how many tenants or policies exist.
var RoutingDecisionReasons = []string{
	"applied", "low_confidence", "no_preference",
	"evaluator_unavailable", "ineligible", "single_candidate",
}

var (
	// RoutingDecisionTotal counts requests that matched an enabled routing
	// policy, by outcome. Requests with no policy are deliberately not counted:
	// that is the default state of every tenant and would only add noise.
	RoutingDecisionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "routing_decision",
			Name:      "total",
			Help:      "Requests that matched an enabled decision-routing policy, by outcome reason",
		},
		[]string{"reason"},
	)

	// RoutingDecisionLatencySeconds is the wall time the evaluator call added
	// to a request. Only requests that actually ran the evaluator observe it.
	RoutingDecisionLatencySeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "routing_decision",
			Name:      "latency_seconds",
			Help:      "Latency the decision-model evaluator added to a routed request",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 0.75, 1, 1.5, 3},
		},
	)
)

// init pre-registers the zero-valued series so a dashboard can tell "no
// request was ever routed" from "the series is not wired".
func init() {
	for _, r := range RoutingDecisionReasons {
		RoutingDecisionTotal.WithLabelValues(r)
	}
}

// RecordRoutingDecision is the production writer, called from
// middleware.ApplyDecisionRouting. latency <= 0 means the evaluator never ran
// (ineligible / single_candidate / concurrency-full) and is not observed.
func RecordRoutingDecision(reason string, latency time.Duration) {
	RoutingDecisionTotal.WithLabelValues(reason).Inc()
	if latency > 0 {
		RoutingDecisionLatencySeconds.Observe(latency.Seconds())
	}
}
