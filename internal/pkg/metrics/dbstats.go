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
	c := collectors.NewDBStatsCollector(db, name)
	if err := prometheus.Register(c); err != nil {
		if _, dup := err.(prometheus.AlreadyRegisteredError); !dup {
			// Registration failed for a reason other than "already registered".
			// DB telemetry is best-effort observability; never fail boot for it.
			_ = err
		}
	}
}
