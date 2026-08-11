package metrics

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Schema-migration drift was previously invisible: with MIGRATIONS_AUTO_RUN=false,
// or a replica set where no pod is master-capable, the boot path simply logs a
// line (or nothing) and serves traffic against an older schema than the shipped
// code expects. These two gauges make that alertable —
// `lurus_gateway_schema_migrations_pending > 0` is the condition to page on.
var (
	// SchemaMigrationsPending is the number of embedded migrations whose DDL has
	// not been recorded as applied. Baseline (bookkeeping-only) versions never
	// count toward it — see migration.PendingVersions.
	SchemaMigrationsPending = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "schema_migrations_pending",
		Help:      "Embedded SQL migrations discovered but not yet recorded as applied",
	})

	// SchemaMigrationsApplied is the number of versions recorded in
	// public.schema_migrations, i.e. the schema level this pod booted against.
	SchemaMigrationsApplied = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "schema_migrations_applied",
		Help:      "Versions recorded in public.schema_migrations",
	})
)

// pendingSnapshot mirrors the pending gauge for readers that cannot afford a
// database round trip. /api/health is polled by the readiness probe every few
// seconds; re-querying schema_migrations there would add DB load to the exact
// path that has to stay responsive when the database is struggling.
var pendingSnapshot struct {
	count atomic.Int64
	known atomic.Bool
}

// SetSchemaMigrations publishes a migration-state snapshot taken at boot.
func SetSchemaMigrations(pending, applied int) {
	SchemaMigrationsPending.Set(float64(pending))
	SchemaMigrationsApplied.Set(float64(applied))
	pendingSnapshot.count.Store(int64(pending))
	pendingSnapshot.known.Store(true)
}

// PendingSchemaMigrations returns the last published pending count. known is
// false until SetSchemaMigrations has run, which lets callers distinguish
// "nothing pending" from "never measured".
func PendingSchemaMigrations() (count int, known bool) {
	return int(pendingSnapshot.count.Load()), pendingSnapshot.known.Load()
}
