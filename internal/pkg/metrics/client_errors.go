package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ClientErrorKinds are the only label values ClientErrorsTotal takes. The
// kind comes from an unauthenticated browser report, so anything else is
// folded into "other" before it reaches a label: a caller must not be able
// to mint new series.
var ClientErrorKinds = []string{"render", "window_error", "unhandled_rejection", "chunk_load", "other"}

// ClientErrorsTotal counts errors the console reports from people's browsers
// (POST /api/client-error). Before it, a crash in the console was visible
// only in the browser's own devtools: ErrorBoundary logged to console.error
// and nothing reached the server.
var ClientErrorsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "client_errors_total",
		Help:      "Errors reported by the web console from users' browsers, by kind",
	},
	[]string{"kind"},
)

func init() {
	// Every kind exists at 0 from boot, so a rate alarm has a series to read
	// before the first crash rather than reading "absent" as "fine".
	for _, k := range ClientErrorKinds {
		ClientErrorsTotal.WithLabelValues(k)
	}
}

// RecordClientError counts one browser-reported error; unknown kinds count
// as "other".
func RecordClientError(kind string) {
	for _, k := range ClientErrorKinds {
		if k == kind {
			ClientErrorsTotal.WithLabelValues(k).Inc()
			return
		}
	}
	ClientErrorsTotal.WithLabelValues("other").Inc()
}
