package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// LogBodyWriteFailedTotal counts archived-body inserts (migration 052,
// repo.archiveLogBody) that failed. The write is asynchronous and strictly
// best-effort - billing and the log row are already settled when it runs - so
// this counter is the only signal that a consented tenant's bodies are being
// lost. Unlabelled on purpose (low cardinality; the tenant is in the system
// log line). A plain counter, so the series is on /metrics from boot and 0
// means "no body lost", not "never wired".
var LogBodyWriteFailedTotal = promauto.NewCounter(
	prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "log_body_write_failed_total",
		Help:      "Archived request/response body inserts that failed (best-effort write, billing unaffected)",
	},
)
