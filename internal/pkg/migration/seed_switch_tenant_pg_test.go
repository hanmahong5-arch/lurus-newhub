package migration_test

// INTEGRATION coverage for migration 030_seed_switch_tenant_and_credit_pool
// against a real PostgreSQL, gated on TEST_POSTGRES_DSN (the CI pg-integration
// job provides a disposable Postgres). Helpers setupPG, countApplied,
// tableExists, expectedFullMigrationCount live in runner_pg_test.go; scalarInt
// lives in baseline_gaps_pg_test.go (same package).
//
// 030 is a SEED migration, which makes it structurally different from every
// other executable migration in this tree: it writes rows into tables that
// nothing in the SQL lineage creates. `tenants` and `tenant_credit_pools` come
// from GORM AutoMigrate, which runs before this runner at boot
// (repo/main.go runBootMigrations) but never runs in these tests — 001-020 are
// baseline-only. So the guards are not defensive decoration; without them the
// whole runner aborts on any database built from SQL alone.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// baselineThrough029 makes the Runner execute only 030.
const baselineThrough029 = "029_create_projects"

func runOnly030(t *testing.T, db *sql.DB) {
	t.Helper()
	r := &migration.Runner{DB: db, FS: migrations.FS, BaselineThrough: baselineThrough029}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run (execute 030): %v", err)
	}
}

// makeAutoMigrateShapedTables reproduces the columns 030 actually touches, in
// the shape GORM AutoMigrate produces from entity.Tenant and
// entity.TenantCreditPool. Deliberately not the full struct: the point is to
// pin what 030 depends on, so a future column rename breaks here rather than at
// boot on a live database.
func makeAutoMigrateShapedTables(t *testing.T, db *sql.DB) {
	t.Helper()
	const ddl = `
CREATE TABLE tenants (
    id             varchar(36) PRIMARY KEY,
    zitadel_org_id varchar(64) NOT NULL UNIQUE,
    slug           varchar(64) NOT NULL UNIQUE,
    name           varchar(128) NOT NULL,
    status         bigint NOT NULL DEFAULT 1,
    plan_type      varchar(32) NOT NULL DEFAULT 'free',
    max_users      bigint NOT NULL DEFAULT 10,
    max_quota      bigint NOT NULL DEFAULT 0
);
CREATE TABLE tenant_credit_pools (
    id                  bigserial PRIMARY KEY,
    tenant_id           varchar(36) NOT NULL UNIQUE,
    created_by_user_id  bigint NOT NULL,
    current_balance     bigint NOT NULL DEFAULT 0,
    max_balance         bigint NOT NULL DEFAULT -1,
    reset_period        varchar(16) NOT NULL DEFAULT 'monthly',
    alert_threshold_pct bigint NOT NULL DEFAULT 80
);
`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("create AutoMigrate-shaped tables: %v", err)
	}
}

// TestIntegration030_SeedsSwitchTenantAndPool is the reason the migration
// exists: platform hard-codes newhub_pool_slug="switch" and funds it via
// /internal/v1/provisioning/tenants/switch/credit-pool/fund, which 404s unless
// BOTH the tenant and its pool exist.
func TestIntegration030_SeedsSwitchTenantAndPool(t *testing.T) {
	db := setupPG(t)
	makeAutoMigrateShapedTables(t, db)

	runOnly030(t, db)

	if got := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'switch' AND slug = 'switch'`); got != 1 {
		t.Errorf("tenants rows for switch = %d, want 1 — the fund endpoint's GetTenantBySlug would 404", got)
	}
	if got := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE tenant_id = 'switch'`); got != 1 {
		t.Errorf("tenant_credit_pools rows for switch = %d, want 1 — seeding only the tenant just moves the 404", got)
	}

	// reset_period must NOT fall back to the column default 'monthly': this pool
	// holds credit users have already paid for, and a monthly reset would zero it.
	var resetPeriod string
	if err := db.QueryRow(`SELECT reset_period FROM tenant_credit_pools WHERE tenant_id = 'switch'`).Scan(&resetPeriod); err != nil {
		t.Fatalf("read reset_period: %v", err)
	}
	if resetPeriod != "none" {
		t.Errorf("reset_period = %q, want \"none\" — 'monthly' would wipe paid-for credit every month", resetPeriod)
	}

	if got := scalarInt(t, db, `SELECT current_balance FROM tenant_credit_pools WHERE tenant_id = 'switch'`); got != 0 {
		t.Errorf("current_balance = %d, want 0 — balance may only come from real funding events, never from a migration", got)
	}
	if got := scalarInt(t, db, `SELECT max_balance FROM tenant_credit_pools WHERE tenant_id = 'switch'`); got != -1 {
		t.Errorf("max_balance = %d, want -1 (uncapped); a ceiling here surfaces as POOL_CEILING_EXCEEDED", got)
	}
}

// TestIntegration030_Idempotent: the runner replays on every boot of a fresh
// database, and both statements are ON CONFLICT DO NOTHING. Running the body
// twice must not duplicate or error.
func TestIntegration030_Idempotent(t *testing.T) {
	db := setupPG(t)
	makeAutoMigrateShapedTables(t, db)

	runOnly030(t, db)

	// Re-execute the migration body directly: the runner itself would skip an
	// already-recorded version, so replaying through it would prove nothing.
	body, err := migrations.FS.ReadFile("030_seed_switch_tenant_and_credit_pool.sql")
	if err != nil {
		t.Fatalf("read 030: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Fatalf("re-executing 030 must be safe, got: %v", err)
	}

	if got := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'switch'`); got != 1 {
		t.Errorf("tenants rows for switch after replay = %d, want 1", got)
	}
	if got := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE tenant_id = 'switch'`); got != 1 {
		t.Errorf("pool rows for switch after replay = %d, want 1", got)
	}
}

// TestIntegration030_EmptyDBGuard: on a runner-only database (no AutoMigrate,
// so neither table exists) 030 must no-op via the to_regclass guards and still
// record as applied. Without the guard the INSERT fails with 42P01 and takes
// the whole runner — hence every migration after it — down with it.
func TestIntegration030_EmptyDBGuard(t *testing.T) {
	db := setupPG(t)

	runOnly030(t, db)

	if tableExists(t, db, "tenants") {
		t.Error("030 must not create tenants (AutoMigrate owns it)")
	}
	if tableExists(t, db, "tenant_credit_pools") {
		t.Error("030 must not create tenant_credit_pools (AutoMigrate owns it)")
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d", got, want)
	}
}

// TestIntegration030_PartialPoolTable_ColumnGuardSkips: created_by_user_id
// exists only in the GORM struct — no SQL migration in 001-029 adds it. A
// database whose tenant_credit_pools predates that field (or a partial DR
// restore) must be skipped by the column guard, not fail with 42703. The
// tenant seed still lands, because that half has no such dependency.
func TestIntegration030_PartialPoolTable_ColumnGuardSkips(t *testing.T) {
	db := setupPG(t)
	const ddl = `
CREATE TABLE tenants (
    id             varchar(36) PRIMARY KEY,
    zitadel_org_id varchar(64) NOT NULL UNIQUE,
    slug           varchar(64) NOT NULL UNIQUE,
    name           varchar(128) NOT NULL,
    status         bigint NOT NULL DEFAULT 1,
    plan_type      varchar(32) NOT NULL DEFAULT 'free',
    max_users      bigint NOT NULL DEFAULT 10,
    max_quota      bigint NOT NULL DEFAULT 0
);
CREATE TABLE tenant_credit_pools (
    id        bigserial PRIMARY KEY,
    tenant_id varchar(36) NOT NULL UNIQUE
);
`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("create partial pool table: %v", err)
	}

	runOnly030(t, db)

	if got := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'switch'`); got != 1 {
		t.Errorf("tenants rows for switch = %d, want 1 — the tenant half does not depend on the missing column", got)
	}
	if got := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE tenant_id = 'switch'`); got != 0 {
		t.Errorf("pool rows = %d, want 0 — the column guard must skip rather than fail", got)
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d", got, want)
	}
}
