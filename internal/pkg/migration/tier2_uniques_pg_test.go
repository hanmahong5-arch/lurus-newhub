package migration_test

// INTEGRATION coverage for migration 025_tier2_per_tenant_uniques against a
// real PostgreSQL, gated on TEST_POSTGRES_DSN (the CI pg-integration job
// provides a disposable Postgres). Helpers setupPG, countApplied, tableExists
// live in runner_pg_test.go; scalarInt, hasUniqueOnColumns live in
// baseline_gaps_pg_test.go (same package).

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// baselineThrough024 makes the Runner execute only 025 and above (025 + 026;
// 026 just creates its own unrelated model_rate_limits table).
const baselineThrough024 = "024_audit_events_tamper_evidence"

func runOnly025(t *testing.T, db *sql.DB) {
	t.Helper()
	r := &migration.Runner{DB: db, FS: migrations.FS, BaselineThrough: baselineThrough024}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run (execute 025+): %v", err)
	}
}

// makeLegacyUsersTable reproduces the pre-025 shape: a GLOBAL unique on
// username, deliberately in BOTH historical forms at once — a table constraint
// (GORM `unique` tag) AND a standalone unique index (hand-applied DBs) — plus
// the plain non-unique lookup index the `index` tag keeps. 025's drop loop is
// shape-matched, so it must clear both forms and leave the plain index alone.
func makeLegacyUsersTable(t *testing.T, db *sql.DB) {
	t.Helper()
	const ddl = `
CREATE TABLE users (
    id         bigserial PRIMARY KEY,
    tenant_id  varchar(36) NOT NULL DEFAULT 'default',
    username   text,
    deleted_at timestamptz,
    CONSTRAINT uni_users_username UNIQUE (username)
);
CREATE UNIQUE INDEX users_username_key_manual ON users (username);
CREATE INDEX idx_users_username ON users (username);
`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("create legacy users table: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO users (tenant_id, username) VALUES ('default', 'alice'), ('default', 'bob')`); err != nil {
		t.Fatalf("seed legacy users: %v", err)
	}
}

func isPGUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

func TestIntegration025_LegacyGlobalUnique_ConvergesToPerTenant(t *testing.T) {
	db := setupPG(t)
	makeLegacyUsersTable(t, db)

	// Negative control: under the legacy schema a cross-tenant duplicate is
	// rejected, so the post-025 success below cannot pass hollowly.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO users (tenant_id, username) VALUES ('tenant-b', 'alice')`); !isPGUniqueViolation(err) {
		t.Fatalf("negative control void: cross-tenant duplicate must fail pre-025, got: %v", err)
	}

	runOnly025(t, db)

	// 24 baselined (001-024) + 025..026 executed = 26.
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d (all embedded migrations recorded)", got, want)
	}

	// Both legacy single-column uniques are gone; the composite exists.
	if hasUniqueOnColumns(t, db, "users", "username") {
		t.Error("single-column unique on users(username) survived 025")
	}
	if !hasUniqueOnColumns(t, db, "users", "tenant_id", "username") {
		t.Error("composite unique on users(tenant_id, username) missing after 025")
	}
	// The plain non-unique lookup index is untouched.
	if n := scalarInt(t, db, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'users' AND indexname = 'idx_users_username'`); n != 1 {
		t.Errorf("plain idx_users_username rows = %d, want 1 (025 must not drop the non-unique index)", n)
	}

	// Behavior: cross-tenant duplicate now inserts; same-tenant duplicate is
	// still rejected by the composite unique.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO users (tenant_id, username) VALUES ('tenant-b', 'alice')`); err != nil {
		t.Fatalf("cross-tenant duplicate username must insert after 025: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO users (tenant_id, username) VALUES ('default', 'alice')`); !isPGUniqueViolation(err) {
		t.Fatalf("same-tenant duplicate username must trip uk_users_tenant_username, got: %v", err)
	}

	// Idempotency 1 — a second Runner pass re-records and re-executes nothing.
	runOnly025(t, db)
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("after rerun schema_migrations = %d, want %d", got, want)
	}

	// Idempotency 2 — executing the SQL body itself again is a clean no-op
	// (the drop loop finds nothing; CREATE ... IF NOT EXISTS no-ops).
	body, err := migrations.FS.ReadFile("025_tier2_per_tenant_uniques.sql")
	if err != nil {
		t.Fatalf("read 025 body: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Fatalf("025 re-run not idempotent: %v", err)
	}
}

// TestIntegration025_EmptyDBGuard: on a runner-only database (no AutoMigrate,
// so users is absent) 025 must no-op via the to_regclass guard — recording as
// applied WITHOUT creating the table (AutoMigrate owns it).
func TestIntegration025_EmptyDBGuard(t *testing.T) {
	db := setupPG(t)
	runOnly025(t, db)

	if tableExists(t, db, "users") {
		t.Error("025 must not create users (AutoMigrate owns it)")
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d", got, want)
	}
}

// TestIntegration025_PartialUsersTable_ColumnGuardSkips: a users table that
// lacks the username column (the converge-test fixture shape, and a plausible
// partial DR restore) must be SKIPPED by the column-coverage guard — not fail
// the transaction with "column username does not exist".
func TestIntegration025_PartialUsersTable_ColumnGuardSkips(t *testing.T) {
	db := setupPG(t)
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE users (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL)`); err != nil {
		t.Fatalf("create partial users table: %v", err)
	}

	runOnly025(t, db)

	if hasUniqueOnColumns(t, db, "users", "tenant_id", "username") {
		t.Error("025 must skip a users table without a username column")
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d", got, want)
	}
}
