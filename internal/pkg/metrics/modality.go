package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RoutingModalityMismatchTotal counts candidate routes the modality filter
// found to be the wrong kind of route for the request (for example an
// embeddings call offered a decision channel), by relay mode. It increments in
// both observe mode (the route is still eligible) and enforce mode (the route
// was removed), so flipping ROUTING_MODALITY_FILTER does not change the series
// an operator is watching while deciding whether to enforce. One request that
// retries re-evaluates the same candidates and counts again.
var RoutingModalityMismatchTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "routing",
		Name:      "modality_mismatch_total",
		Help:      "Candidate routes whose modality does not fit the request's relay mode, by relay mode",
	},
	[]string{"relay_mode"},
)

// RecordRoutingModalityMismatch is the production writer, called from the
// channel selection predicate in internal/app/channel_modality.go.
func RecordRoutingModalityMismatch(relayMode string) {
	RoutingModalityMismatchTotal.WithLabelValues(relayMode).Inc()
}
