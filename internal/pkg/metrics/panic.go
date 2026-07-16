package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// PanicsRecovered counts panics caught by a recovery boundary, labeled by the
// boundary that caught it (relay middleware, HTTP recovery, background
// goroutine). Panics were previously only visible in logs; this gives an
// alertable signal — a provider adapter that starts panicking in a loop should
// page, not sit silent behind a recover().
var PanicsRecovered = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "panics_recovered_total",
		Help:      "Total panics caught by a recovery boundary, by source",
	},
	[]string{"source"},
)

// RecordPanic increments the recovered-panic counter for the given boundary.
func RecordPanic(source string) {
	PanicsRecovered.WithLabelValues(source).Inc()
}
