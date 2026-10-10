package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Data-pipeline failure counters. Each of these paths used to fail with at
// most a log line (and the Meilisearch one only under Debug), so a dead
// search index, a failing aggregate table or an unreachable broker was
// invisible until a customer noticed a stale dashboard. All are plain
// low-cardinality counters; none carries a user/tenant/subject label.

var (
	// LogSearchSyncFailedTotal counts log documents that could not be
	// indexed into Meilisearch after the retry budget was spent. The DB row
	// is intact — what is lost is full-text search over it.
	// ALERTABLE: lurus_log_search_sync_failed_total
	LogSearchSyncFailedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "log_search",
			Name:      "sync_failed_total",
			Help:      "Log documents that failed to sync to the search index after retries",
		},
	)

	// QuotaDataWriteFailedTotal counts quota_data aggregate buckets dropped
	// because their upsert failed. quota_data feeds the usage dashboards, so
	// the loss shows up as under-reported usage.
	// (No alarm yet: dashboards-only impact; see doc/runbook/data-pipeline-failures.md.)
	QuotaDataWriteFailedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "quota_data",
			Name:      "write_failed_total",
			Help:      "quota_data aggregate buckets whose write failed and were dropped",
		},
	)

	// NATSPublishFailedTotal counts failed event publishes, by a coarse
	// subject group (see NATSSubjectGroups) so the label stays a small enum
	// regardless of how many subjects exist.
	// ALERTABLE: lurus_nats_publish_failed_total
	NATSPublishFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "nats",
			Name:      "publish_failed_total",
			Help:      "NATS event publishes that failed, by subject group (quota/usage/image/other)",
		},
		[]string{"subject_group"},
	)
)

// NATSSubjectGroups is the closed set of subject_group label values.
var NATSSubjectGroups = []string{"quota", "usage", "image", "other"}

// init pre-registers the zero-valued series: a counter child only appears on
// /metrics at its first Inc(), and Netdata cannot tell "never failed" from
// "not wired" when the chart is missing (same reasoning as billing.go).
func init() {
	for _, g := range NATSSubjectGroups {
		NATSPublishFailedTotal.WithLabelValues(g)
	}
}
