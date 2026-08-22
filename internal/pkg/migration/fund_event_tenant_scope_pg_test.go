package migration_test

// INTEGRATION coverage for migration 031_fund_event_idempotency_per_tenant
// against a real PostgreSQL, gated on TEST_POSTGRES_DSN (the CI pg-integration
// job provides a disposable Postgres). Same pattern as
// tier2_uniques_pg_test.go (migration 025): helpers setupPG, countApplied,
// tableExists live in runner_pg_test.go; scalarInt, hasUniqueOnColumns live in
// baseline_gaps_pg_test.go (same package).

import (
	"context"
	"database/sql"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// baselineThrough030 makes the Runner execute only 031 (the highest embedded
// version at the time this test was written).
const baselineThrough030 = "030_seed_switch_tenant_and_credit_pool"

func runOnly031(t *testing.T, db *sql.DB) {
	t.Helper()
	r := &migration.Runner{DB: db, FS: migrations.FS, BaselineThrough: baselineThrough030}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run (execute 031): %v", err)
	}
}

// makeLegacyFundEventsTable reproduces the pre-031 shape: a GLOBAL unique on
// event_id, deliberately in BOTH historical forms at once — the named table
// constraint the 019/021 SQL path creates, AND a standalone unique index (the
// shape a bare `uniqueIndex` GORM tag with no explicit name could produce on
// an AutoMigrate-only DB) — plus the plain non-unique tenant_id lookup index
// 019/021 also creates. 031's drop loop is shape-matched, so it must clear
// both unique forms and leave the plain index alone.
func makeLegacyFundEventsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	const ddl = `
CREATE TABLE credit_pool_fund_events (
    id          BIGSERIAL    PRIMARY KEY,
    event_id    VARCHAR(128) NOT NULL,
    tenant_id   VARCHAR(36)  NOT NULL,
    pool_id     BIGINT       NOT NULL,
    amount      BIGINT       NOT NULL,
    new_balance BIGINT       NOT NULL,
    source      VARCHAR(64)  NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_credit_pool_fund_events_event_id UNIQUE (event_id)
);
CREATE UNIQUE INDEX idx_credit_pool_fund_events_event_id_manual ON credit_pool_fund_events (event_id);
CREATE INDEX idx_credit_pool_fund_events_tenant ON credit_pool_fund_events (tenant_id);
`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("create legacy credit_pool_fund_events table: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO credit_pool_fund_events (event_id, tenant_id, pool_id, amount, new_balance, source)
		 VALUES ('evt-shared', 'tenant-a', 1, 500, 500, 'platform-billing-outbox')`); err != nil {
		t.Fatalf("seed legacy fund event: %v", err)
	}
}

// TestIntegration031_LegacyGlobalUnique_ConvergesToPerTenant is the money-path
// contract for FundPoolIdempotent's schema: before 031 a second tenant cannot
// even INSERT a row reusing a first tenant's event_id (proving the historical
// bug was schema-level, not just an app-layer lookup bug); after 031 the same
// insert succeeds, and a genuine same-tenant replay is still rejected.
func TestIntegration031_LegacyGlobalUnique_ConvergesToPerTenant(t *testing.T) {
	db := setupPG(t)
	makeLegacyFundEventsTable(t, db)

	// Negative control: under the legacy global unique, a second tenant
	// reusing the same event_id cannot even be inserted — so the post-031
	// success below cannot pass hollowly.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO credit_pool_fund_events (event_id, tenant_id, pool_id, amount, new_balance, source)
		 VALUES ('evt-shared', 'tenant-b', 2, 700, 700, 'platform-billing-outbox')`); !isPGUniqueViolation(err) {
		t.Fatalf("negative control void: cross-tenant event_id reuse must fail pre-031, got: %v", err)
	}

	runOnly031(t, db)

	// 30 baselined (001-030) + 031 executed = 31.
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d (all embedded migrations recorded)", got, want)
	}

	// Both legacy single-column uniques are gone; the composite exists.
	if hasUniqueOnColumns(t, db, "credit_pool_fund_events", "event_id") {
		t.Error("single-column unique on credit_pool_fund_events(event_id) survived 031")
	}
	if !hasUniqueOnColumns(t, db, "credit_pool_fund_events", "tenant_id", "event_id") {
		t.Error("composite unique on credit_pool_fund_events(tenant_id, event_id) missing after 031")
	}
	// The plain non-unique tenant lookup index is untouched.
	if n := scalarInt(t, db, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'credit_pool_fund_events'
		  AND indexname = 'idx_credit_pool_fund_events_tenant'`); n != 1 {
		t.Errorf("plain idx_credit_pool_fund_events_tenant rows = %d, want 1 (031 must not drop the non-unique index)", n)
	}

	// Behavior: cross-tenant event_id reuse now inserts; same-tenant replay
	// (identical tenant_id + event_id) is still rejected by the composite unique.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO credit_pool_fund_events (event_id, tenant_id, pool_id, amount, new_balance, source)
		 VALUES ('evt-shared', 'tenant-b', 2, 700, 700, 'platform-billing-outbox')`); err != nil {
		t.Fatalf("cross-tenant event_id reuse must insert after 031: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO credit_pool_fund_events (event_id, tenant_id, pool_id, amount, new_balance, source)
		 VALUES ('evt-shared', 'tenant-a', 1, 500, 500, 'platform-billing-outbox')`); !isPGUniqueViolation(err) {
		t.Fatalf("same-tenant duplicate event_id must trip uk_credit_pool_fund_events_tenant_event_id, got: %v", err)
	}

	// Idempotency 1 — a second Runner pass re-records and re-executes nothing.
	runOnly031(t, db)
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("after rerun schema_migrations = %d, want %d", got, want)
	}

	// Idempotency 2 — executing the SQL body itself again is a clean no-op
	// (the drop loop finds nothing; CREATE ... IF NOT EXISTS no-ops).
	body, err := migrations.FS.ReadFile("031_fund_event_idempotency_per_tenant.sql")
	if err != nil {
		t.Fatalf("read 031 body: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Fatalf("031 re-run not idempotent: %v", err)
	}
}

// TestIntegration031_EmptyDBGuard: on a runner-only database (no AutoMigrate
// and no 019/021 execution, so credit_pool_fund_events is absent) 031 must
// no-op via the to_regclass guard — recording as applied WITHOUT creating the
// table (021/AutoMigrate own it, not this migration).
func TestIntegration031_EmptyDBGuard(t *testing.T) {
	db := setupPG(t)
	runOnly031(t, db)

	if tableExists(t, db, "credit_pool_fund_events") {
		t.Error("031 must not create credit_pool_fund_events (021/AutoMigrate own it)")
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d", got, want)
	}
}
