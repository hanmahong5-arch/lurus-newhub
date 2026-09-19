package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ChannelCacheSyncFailedTotal counts channel-cache rebuild passes that were
// abandoned because one of their two database reads failed, labeled by which
// read failed:
//
//	channels  — the SELECT over `channels` (repo.InitChannelCache's first Find)
//	abilities — the SELECT over `abilities` (its second Find)
//
// Before cycle-12 L8 both reads discarded their error and the rebuild carried
// on with whatever partial slice GORM had produced — an empty one on a failed
// query — then swapped that into the live routing table. One failed query on
// one replica therefore emptied that replica's relay routing table until the
// next successful sync (SYNC_FREQUENCY, 60s on r6-stage), and nothing recorded
// that it had happened. The rebuild now keeps the previous table and
// increments this counter instead, which makes the event visible but also
// means a persistently failing query leaves the table STALE rather than empty
// — see repo.InitChannelCache's doc comment for that trade-off.
var ChannelCacheSyncFailedTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "channel_cache_sync_failed_total",
		Help:      "Channel-cache rebuilds abandoned on a failed database read, by which read failed",
	},
	[]string{"query"},
)

// RecordChannelCacheSyncFailed increments the abandoned-rebuild counter for
// the named read ("channels" or "abilities").
func RecordChannelCacheSyncFailed(query string) {
	ChannelCacheSyncFailedTotal.WithLabelValues(query).Inc()
}

// DBSlowQueryTotal counts queries whose wall time crossed the GORM logger's
// slow-query threshold (DB_SLOW_QUERY_MS, default 200ms), labeled by pool —
// the same db_name values RegisterDBStats uses, so this counter and the
// go_sql_* pool gauges line up on a dashboard:
//
//	newhub     — the main pool (SQL_DSN)
//	newhub_log — the separate log pool, present only when LOG_SQL_DSN is set
//
// Each increment is paired with one log line from the same Trace call, so the
// counter is the alertable form of a signal that before this change existed as
// a log line and no metric. Pool saturation and a struggling database both
// surface here; the two are told apart by reading go_sql_wait_count_total /
// go_sql_wait_duration_seconds_total alongside it
// (doc/runbook/db-pool-saturation.md).
var DBSlowQueryTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "db_slow_query_total",
		Help:      "Database queries that crossed the slow-query threshold, by pool",
	},
	[]string{"db"},
)

// RecordDBSlowQuery increments the slow-query counter for the named pool.
func RecordDBSlowQuery(db string) {
	DBSlowQueryTotal.WithLabelValues(db).Inc()
}

// init pre-registers the label values a default deployment produces, matching
// the pattern in r4_cost_spike.go and r6_rate_limit_degraded.go: a CounterVec
// child series only appears on /metrics once its first Inc() fires, so an
// absent series would otherwise be ambiguous between "this has not happened
// yet" and "this counter is not wired into anything".
//
// The db label is pre-registered for "newhub" alone, not for "newhub_log":
// the log pool exists only when LOG_SQL_DSN is set (repo.InitLogDB returns
// early otherwise, leaving LOG_DB aliased to DB), so pre-registering it would
// publish a series for a pool that does not exist on the two deployments in
// this repo — grep deploy/k8s/r6-stage/deployment.yaml and
// deploy/k8s/r6-uat/deployment.yaml for LOG_SQL_DSN: zero hits in both.
func init() {
	ChannelCacheSyncFailedTotal.WithLabelValues("channels")
	ChannelCacheSyncFailedTotal.WithLabelValues("abilities")
	DBSlowQueryTotal.WithLabelValues("newhub")
}
