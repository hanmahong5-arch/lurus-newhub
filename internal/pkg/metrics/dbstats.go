package metrics

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// RegisterDBStats exposes database/sql connection-pool telemetry on /metrics
// for the named pool (db_name label): go_sql_max_open_connections,
// go_sql_open_connections, go_sql_in_use_/idle_connections, go_sql_wait_count
// and go_sql_wait_duration. With the pool capped at SQL_MAX_OPEN_CONNS
// (default 1000), a rising wait_count/wait_duration is the early-warning signal
// that the pool is saturating — previously invisible until requests simply got
// slow.
//
// Re-registration (e.g. InitDB running twice in a test process) is tolerated:
// an identical collector is silently ignored, so callers need no guard.
func RegisterDBStats(name string, db *sql.DB) {
	if db == nil {
		return
	}
	// Best-effort telemetry: re-registering an identical collector (InitDB twice
	// in a test process) returns AlreadyRegisteredError, and any other failure is
	// likewise non-fatal — DB telemetry must never fail boot. Both outcomes are
	// ignored, so no error branching is needed.
	_ = prometheus.Register(collectors.NewDBStatsCollector(db, name))
}
