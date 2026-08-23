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
	"strings"
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

// preHardening031Body is 031's DO-block exactly as it read before the
// 2026-08-22 contype filter (see the migration file's own header comment) —
// kept here ONLY as the negative-control fixture for
// TestIntegration031_ExternalFKOnLegacyIndex_DropLoopNoLongerMisfires: it
// proves the failure this hardening fixes was real, without depending on git
// history at test-run time.
const preHardening031Body = `
DO $mig$
DECLARE
    legacy record;
BEGIN
    IF to_regclass('public.credit_pool_fund_events') IS NULL THEN
        RAISE WARNING '031_fund_event_idempotency_per_tenant: credit_pool_fund_events absent (021/AutoMigrate creates it); skipping';
        RETURN;
    END IF;

    FOR legacy IN
        SELECT i.indexrelid::regclass::text AS index_name,
               c.conname                    AS constraint_name
        FROM pg_index i
        LEFT JOIN pg_constraint c ON c.conindid = i.indexrelid
        WHERE i.indrelid = 'public.credit_pool_fund_events'::regclass
          AND i.indisunique
          AND i.indnkeyatts = 1
          AND (SELECT a.attname FROM pg_attribute a
               WHERE a.attrelid = i.indrelid AND a.attnum = i.indkey[0]) = 'event_id'
    LOOP
        IF legacy.constraint_name IS NOT NULL THEN
            EXECUTE format('ALTER TABLE credit_pool_fund_events DROP CONSTRAINT %I', legacy.constraint_name);
        ELSE
            EXECUTE format('DROP INDEX %s', legacy.index_name);
        END IF;
    END LOOP;

    EXECUTE 'CREATE UNIQUE INDEX IF NOT EXISTS uk_credit_pool_fund_events_tenant_event_id ON credit_pool_fund_events (tenant_id, event_id)';
END
$mig$;
`

// makeLegacyFundEventsTableBareIndexPlusExternalFK reproduces the OTHER
// historical shape noted in this file's header (a bare `uniqueIndex` GORM tag
// with no explicit name — a standalone unique index, no owning table
// constraint) and adds a SECOND table with a foreign key referencing
// credit_pool_fund_events(event_id) — standing in for "some future external
// table references this table's legacy unique index" (031's header comment,
// 2026-08-22 hardening note). This is the shape that actually demonstrates
// the fix: pg_constraint has exactly one row joinable via conindid for this
// index — the FK's own row (contype='f') — so the unfiltered join
// misattributes the FK's conname to this table's index.
func makeLegacyFundEventsTableBareIndexPlusExternalFK(t *testing.T, db *sql.DB) {
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
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_credit_pool_fund_events_event_id_manual ON credit_pool_fund_events (event_id);
-- Synthetic external referrer: no such table exists in this codebase today —
-- this stands in for the "future outside table" the hardening defends
-- against, per the migration's own 2026-08-22 header note.
CREATE TABLE hypothetical_external_fk_probe (
    id        BIGSERIAL    PRIMARY KEY,
    event_ref VARCHAR(128) NOT NULL REFERENCES credit_pool_fund_events(event_id)
);
`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("create bare-index+external-FK fixture: %v", err)
	}
}

// TestIntegration031_ExternalFKOnLegacyIndex_DropLoopNoLongerMisfires is the
// regression lock for the 2026-08-22 contype-filter hardening. Negative
// control first: preHardening031Body (the exact pre-hardening SQL) MUST fail
// against this fixture — proving the bug was real, not a hypothetical. Then
// the LIVE migrations.FS body (via runOnly031, i.e. through the real Runner)
// MUST succeed against a fresh instance of the same fixture. If someone
// reverts the contype filter from the live file, this second half starts
// failing with the same error the negative control demonstrates.
func TestIntegration031_ExternalFKOnLegacyIndex_DropLoopNoLongerMisfires(t *testing.T) {
	// Negative control: the unfiltered join was real and reproducible.
	before := setupPG(t)
	makeLegacyFundEventsTableBareIndexPlusExternalFK(t, before)
	if _, err := before.ExecContext(context.Background(), preHardening031Body); err == nil {
		t.Fatal("negative control void: pre-hardening 031 body must fail when an external FK references the legacy index")
	} else if !strings.Contains(err.Error(), "42704") {
		// Pin the FAILURE MODE, not just failure: the unfiltered join hands
		// the loop the FK's name and DROP CONSTRAINT dies with undefined_object
		// (SQLSTATE 42704). Any other error (e.g. a typo sneaking into the
		// embedded pre-hardening body) would make this control a straw man.
		t.Fatalf("negative control failed with the wrong error: want SQLSTATE 42704 (undefined_object from dropping the FK's name on this table), got: %v", err)
	}

	// Positive: the live (fixed) migration completes without error against the
	// SAME fixture shape — the whole point of this hardening.
	after := setupPG(t)
	makeLegacyFundEventsTableBareIndexPlusExternalFK(t, after)
	runOnly031(t, after)

	// The legacy single-column index is left in place: it is now excluded
	// from the drop loop's result set entirely (its only pg_constraint match
	// via conindid is the external FK's own row, contype='f', filtered out),
	// so the loop never attempts to touch it. This is the accepted trade-off
	// of the 2026-08-22 hardening — it prevents the crash but cannot also
	// drop an index a live FK depends on (Postgres would refuse that without
	// CASCADE regardless; out of scope here). The new composite index is
	// created successfully alongside it either way.
	if !hasUniqueOnColumns(t, after, "credit_pool_fund_events", "event_id") {
		t.Error("legacy single-column unique on event_id should survive untouched when an external FK depends on it (skip, not drop)")
	}
	if !hasUniqueOnColumns(t, after, "credit_pool_fund_events", "tenant_id", "event_id") {
		t.Error("composite unique on credit_pool_fund_events(tenant_id, event_id) missing after 031")
	}
	// The external FK itself must still be intact — 031 must not have touched
	// the other table.
	var fkCount int
	if err := after.QueryRowContext(context.Background(), `
		SELECT count(*) FROM information_schema.table_constraints
		WHERE table_name = 'hypothetical_external_fk_probe' AND constraint_type = 'FOREIGN KEY'`,
	).Scan(&fkCount); err != nil {
		t.Fatalf("count external FK: %v", err)
	}
	if fkCount != 1 {
		t.Errorf("external FK on hypothetical_external_fk_probe = %d, want 1 (031 must not disturb other tables)", fkCount)
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
