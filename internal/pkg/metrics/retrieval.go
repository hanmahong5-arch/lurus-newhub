package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// InvalidProviderUsageTotal counts upstream usage blocks that contradict
	// themselves (total=0 with prompt>0, negative counts). Such a call is
	// answered 502 and not billed, so without a counter a vendor that starts
	// emitting garbage would only show up as a quiet revenue dip.
	// relay_mode is a small closed set of names (rerank, embeddings, ...).
	// (No Netdata alarm is wired yet; add one in deploy/r6-host-netdata/health.d before calling this alertable.)
	InvalidProviderUsageTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "relay",
			Name:      "invalid_usage_total",
			Help:      "Upstream responses rejected because their usage block was self-contradictory, by relay mode",
		},
		[]string{"relay_mode"},
	)

	// RetrievalUsageTotal counts settled retrieval-style calls by billing unit
	// and by where the usage figure came from, so the share of estimated
	// (not upstream-reported) metering is visible.
	RetrievalUsageTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "retrieval",
			Name:      "usage_total",
			Help:      "Settled rerank/embeddings calls by billing unit (token|search_unit|request) and usage source (upstream|estimated|unreported)",
		},
		[]string{"unit", "source"},
	)
)

// RecordInvalidProviderUsage counts one rejected contradictory usage block.
func RecordInvalidProviderUsage(relayMode string) {
	InvalidProviderUsageTotal.WithLabelValues(relayMode).Inc()
}

// RecordRetrievalUsage counts one settled retrieval call.
func RecordRetrievalUsage(unit, source string) {
	RetrievalUsageTotal.WithLabelValues(unit, source).Inc()
}
