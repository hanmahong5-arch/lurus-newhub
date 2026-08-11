package migration_test

// INTEGRATION coverage for migration 029_create_projects against a real
// PostgreSQL, gated on TEST_POSTGRES_DSN (the CI pg-integration job provides a
// disposable Postgres). Helpers setupPG, countApplied, tableExists,
// expectedFullMigrationCount live in runner_pg_test.go; scalarInt lives in
// baseline_gaps_pg_test.go (same package).
//
// WHY these tests must run against a BARE database: on a normal boot GORM's
// AutoMigrate runs BEFORE the Runner (repo/main.go runBootMigrations), so it
// creates `projects` / `tokens.project_id` / `logs.project_id` first and 029
// is a pure no-op. The only environments where 029's DDL actually executes are
// runner-first ones — DR restores and test databases — which is exactly what
// setupPG gives us. Asserting 029 on an AutoMigrated database would prove
// nothing about 029.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// baselineThrough028 makes the Runner execute only 029.
const baselineThrough028 = "028_create_billing_checkout_orders"

func runOnly029(t *testing.T, db *sql.DB) {
	t.Helper()
	r := &migration.Runner{DB: db, FS: migrations.FS, BaselineThrough: baselineThrough028}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run (execute 029): %v", err)
	}
}

// columnType returns the postgres data type of table.column, or "" when the
// column does not exist.
func columnType(t *testing.T, db *sql.DB, table, column string) string {
	t.Helper()
	var typ sql.NullString
	err := db.QueryRowContext(context.Background(), `
		SELECT data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&typ)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("column type %s.%s: %v", table, column, err)
	}
	return typ.String
}

// columnIsNotNullWithDefault asserts the NOT NULL + DEFAULT 0 shape that makes
// project_id safe to aggregate without COALESCE.
func columnIsNotNullWithDefault(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()
	var isNullable, colDefault sql.NullString
	if err := db.QueryRowContext(context.Background(), `
		SELECT is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&isNullable, &colDefault); err != nil {
		t.Fatalf("column shape %s.%s: %v", table, column, err)
	}
	if isNullable.String != "NO" {
		t.Errorf("%s.%s is_nullable = %q, want NO (0 = unassigned is the repo convention; "+
			"a nullable column would force COALESCE into every aggregate)", table, column, isNullable.String)
	}
	if !strings.Contains(colDefault.String, "0") {
		t.Errorf("%s.%s default = %q, want 0", table, column, colDefault.String)
	}
}

// TestIntegration029_CreatesProjectsAndAttributionColumns is the main contract:
// on a bare (runner-first) database 029 creates the projects table and both
// project_id columns with the exact shape AutoMigrate would produce.
func TestIntegration029_CreatesProjectsAndAttributionColumns(t *testing.T) {
	db := setupPG(t)
	// tokens/logs stand in for a DR restore that has the data tables but has
	// not booted the app yet. Shapes are trimmed to what 029 touches.
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE tokens (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text);
		CREATE TABLE logs   (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', quota bigint NOT NULL DEFAULT 0);
		INSERT INTO tokens (tenant_id, name) VALUES ('default', 'k1');
		INSERT INTO logs   (tenant_id, quota) VALUES ('default', 42);
	`); err != nil {
		t.Fatalf("create pre-029 fixture: %v", err)
	}

	runOnly029(t, db)

	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d (all embedded migrations recorded)", got, want)
	}
	if !tableExists(t, db, "projects") {
		t.Fatal("029 did not create the projects table")
	}

	// BIGINT, not INT — Go `int` maps to postgres bigint under GORM, and a
	// mismatch would make the next boot's AutoMigrate rewrite the column
	// (integer->bigint) under an ACCESS EXCLUSIVE lock on the logs table.
	for _, tc := range []struct{ table, column string }{
		{"projects", "id"},
		{"tokens", "project_id"},
		{"logs", "project_id"},
	} {
		if got := columnType(t, db, tc.table, tc.column); got != "bigint" {
			t.Errorf("%s.%s data_type = %q, want bigint", tc.table, tc.column, got)
		}
	}
	columnIsNotNullWithDefault(t, db, "tokens", "project_id")
	columnIsNotNullWithDefault(t, db, "logs", "project_id")

	// Pre-existing rows must land on 0 (unassigned), not NULL.
	if n := scalarInt(t, db, `SELECT count(*) FROM logs WHERE project_id = 0`); n != 1 {
		t.Errorf("pre-existing log rows with project_id = 0: %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tokens WHERE project_id = 0`); n != 1 {
		t.Errorf("pre-existing token rows with project_id = 0: %d, want 1", n)
	}
}

// TestIntegration029_PartialUniqueSemantics pins the three behaviours the
// partial index exists for. It is created ONLY by 029 (GORM cannot express the
// WHERE predicate), so this is the only place the semantics can be proven.
func TestIntegration029_PartialUniqueSemantics(t *testing.T) {
	db := setupPG(t)
	runOnly029(t, db)
	ctx := context.Background()

	insert := func(tenant, name string) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO projects (tenant_id, name) VALUES ($1, $2)`, tenant, name)
		return err
	}

	if err := insert("tenant-a", "Marketing"); err != nil {
		t.Fatalf("first insert must succeed: %v", err)
	}

	// (1) Same tenant, same name → rejected.
	if err := insert("tenant-a", "Marketing"); !isPGUniqueViolation(err) {
		t.Fatalf("duplicate name within a tenant must trip uk_projects_tenant_name, got: %v", err)
	}

	// (2) Different tenant, same name → accepted. Tenants must not be able to
	// squat each other's project names (and would infer their existence).
	if err := insert("tenant-b", "Marketing"); err != nil {
		t.Fatalf("same name in a different tenant must insert: %v", err)
	}

	// (3) Soft-deleted rows do not reserve the name — that is the entire point
	// of the WHERE deleted_at IS NULL predicate.
	if _, err := db.ExecContext(ctx,
		`UPDATE projects SET deleted_at = now() WHERE tenant_id = 'tenant-a' AND name = 'Marketing'`); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if err := insert("tenant-a", "Marketing"); err != nil {
		t.Fatalf("recreating a soft-deleted project name must succeed: %v", err)
	}
	// ...and the soft-deleted row is still there, so historical logs can still
	// resolve its name via Unscoped().
	if n := scalarInt(t, db,
		`SELECT count(*) FROM projects WHERE tenant_id = 'tenant-a' AND name = 'Marketing'`); n != 2 {
		t.Errorf("rows named Marketing in tenant-a = %d, want 2 (1 soft-deleted + 1 live)", n)
	}
	if n := scalarInt(t, db,
		`SELECT count(*) FROM projects WHERE tenant_id = 'tenant-a' AND name = 'Marketing' AND deleted_at IS NULL`); n != 1 {
		t.Errorf("LIVE rows named Marketing in tenant-a = %d, want 1", n)
	}
}

// TestIntegration029_MissingLogsTable_WarnsInsteadOfFailing is the LOG_DB
// split-database contract. When LOG_SQL_DSN is set, `logs` lives in a database
// the Runner never connects to (repo/main.go:257,275 vs :519-525) — so in the
// main database the table is simply absent. 029 must record itself as applied
// and carry on, not abort the boot.
func TestIntegration029_MissingLogsTable_WarnsInsteadOfFailing(t *testing.T) {
	db := setupPG(t)
	// tokens present, logs absent — precisely the split-log-database shape.
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE tokens (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default')`); err != nil {
		t.Fatalf("create tokens: %v", err)
	}

	runOnly029(t, db)

	if tableExists(t, db, "logs") {
		t.Error("029 must not create the logs table (AutoMigrate/migrateLOGDB owns it)")
	}
	if got := columnType(t, db, "tokens", "project_id"); got != "bigint" {
		t.Errorf("tokens.project_id data_type = %q, want bigint — the tokens branch must still run "+
			"when the logs branch is skipped", got)
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d — a skipped logs branch must still record 029", got, want)
	}
}

// TestIntegration029_MissingTokensTable_WarnsInsteadOfFailing covers the
// partial-DR-restore shape (neither data table present yet).
func TestIntegration029_MissingTokensTable_WarnsInsteadOfFailing(t *testing.T) {
	db := setupPG(t)

	runOnly029(t, db)

	if tableExists(t, db, "tokens") {
		t.Error("029 must not create the tokens table (AutoMigrate owns it)")
	}
	if !tableExists(t, db, "projects") {
		t.Error("029 must still create projects when tokens/logs are absent")
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d", got, want)
	}
}

// TestIntegration029_Idempotent covers both re-run paths: through the Runner
// (which should skip) and by executing the body a second time directly (which
// is what a partially-applied DR restore effectively does).
func TestIntegration029_Idempotent(t *testing.T) {
	db := setupPG(t)
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE tokens (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default');
		CREATE TABLE logs   (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default');
	`); err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	runOnly029(t, db)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO projects (tenant_id, name) VALUES ('tenant-a', 'Marketing')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// Re-run 1: through the Runner — already applied, so nothing executes.
	runOnly029(t, db)
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("after rerun schema_migrations = %d, want %d", got, want)
	}

	// Re-run 2: the body itself, executed directly. Every statement must be
	// IF-NOT-EXISTS-guarded — this is what the AutoMigrate-first ordering and
	// a partial DR restore both look like.
	body, err := migrations.FS.ReadFile("029_create_projects.sql")
	if err != nil {
		t.Fatalf("read 029 body: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Fatalf("029 re-run not idempotent: %v", err)
	}

	// Data survived and the constraint still holds after the replay.
	if n := scalarInt(t, db, `SELECT count(*) FROM projects`); n != 1 {
		t.Errorf("projects rows after replay = %d, want 1 (replay must not drop data)", n)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO projects (tenant_id, name) VALUES ('tenant-a', 'Marketing')`); !isPGUniqueViolation(err) {
		t.Fatalf("partial unique must survive a body replay, got: %v", err)
	}
}
