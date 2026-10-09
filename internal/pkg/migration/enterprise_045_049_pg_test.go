package migration_test

// INTEGRATION coverage for migrations 045-049 (enterprise onboarding) against a
// real PostgreSQL, gated on TEST_POSTGRES_DSN. Helpers setupPG, countApplied,
// tableExists, expectedFullMigrationCount and scalarInt live in
// runner_pg_test.go / baseline_gaps_pg_test.go (same package).
//
// RUNNER-FIRST on purpose: on a normal boot AutoMigrate creates every column
// and table before the runner looks at them, which turns 045-049 into no-ops.
// The DDL only really executes on DR restores and bare test databases — what
// setupPG gives us — so that is where it must be proven, including the
// "runner twice" idempotency and the CREATE INDEX CONCURRENTLY file (047) going
// through the no-transaction path.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

const baselineThrough044 = "044_create_model_health"

func runThrough049(t *testing.T, db *sql.DB) {
	t.Helper()
	r := &migration.Runner{DB: db, FS: migrations.FS, BaselineThrough: baselineThrough044}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run (execute 045-049): %v", err)
	}
}

func indexState(t *testing.T, db *sql.DB, name string) (exists, valid bool, def string) {
	t.Helper()
	err := db.QueryRowContext(context.Background(), `
		SELECT i.indisvalid, pg_get_indexdef(i.indexrelid)
		FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = $1`, name).Scan(&valid, &def)
	if err == sql.ErrNoRows {
		return false, false, ""
	}
	if err != nil {
		t.Fatalf("index %s: %v", name, err)
	}
	return true, valid, def
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestIntegration045to049_RunnerFirst_AppliesAndIsIdempotent: a pre-045 schema
// (the tables 045-049 alter, with the columns their predicates need) gets every
// column, table and index, a second Run changes nothing, and each partial
// unique index enforces exactly its predicate.
func TestIntegration045to049_RunnerFirst_AppliesAndIsIdempotent(t *testing.T) {
	db := setupPG(t)
	mustExec(t, db, `
		CREATE TABLE tokens  (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text, deleted_at timestamptz);
		CREATE TABLE logs    (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', created_at bigint NOT NULL DEFAULT 0, project_id bigint NOT NULL DEFAULT 0);
		CREATE TABLE projects(id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text, deleted_at timestamptz);
		CREATE TABLE users   (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default');
		CREATE TABLE tenants (id varchar(36) PRIMARY KEY);
		CREATE TABLE tenant_invites (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL, code varchar(32) NOT NULL);
		INSERT INTO tokens (tenant_id, name) VALUES ('t1', 'pre-existing');
		INSERT INTO users (tenant_id) VALUES ('t1');
		INSERT INTO tenants (id) VALUES ('t1');
		INSERT INTO tenant_invites (tenant_id, code) VALUES ('t1', 'c1');
	`)

	runThrough049(t, db)

	want := expectedFullMigrationCount(t)
	if got := countApplied(t, db); got != want {
		t.Fatalf("schema_migrations = %d, want %d", got, want)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM schema_migrations WHERE version IN
		('045_enterprise_attribution','046_tenant_roles','047_logs_enterprise_indexes','048_tenant_payer_and_invites','049_account_key_bindings')`); n != 5 {
		t.Fatalf("045-049 recorded %d/5", n)
	}

	// Pre-existing rows land on the defaults, not NULL.
	if n := scalarInt(t, db, `SELECT count(*) FROM tokens WHERE employee_ref = '' AND source_product = '' AND trusted_identity_headers = false`); n != 1 {
		t.Errorf("existing token did not take the new column defaults (%d)", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE payer_user_id = 0 AND wallet_authoritative = false`); n != 1 {
		t.Errorf("existing tenant did not take defaults")
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_invites WHERE member_role = '' AND project_id = 0 AND revoked_at = 0`); n != 1 {
		t.Errorf("existing invite did not take defaults")
	}
	if !tableExists(t, db, "project_members") || !tableExists(t, db, "account_key_bindings") {
		t.Fatal("046 / 049 did not create project_members / account_key_bindings")
	}

	// 047: CONCURRENTLY went through, and the indexes are VALID.
	for _, name := range []string{"idx_logs_tenant_project_created", "idx_logs_tenant_employee_created"} {
		if ex, valid, def := indexState(t, db, name); !ex || !valid {
			t.Errorf("%s exists=%v valid=%v (%s)", name, ex, valid, def)
		}
	}

	// Idempotency: a second Run must be a clean no-op ...
	runThrough049(t, db)
	if got := countApplied(t, db); got != want {
		t.Fatalf("second Run changed schema_migrations: %d -> %d", want, got)
	}
	// ... and re-executing the DDL itself (as a DR re-run would) must not fail either.
	for _, f := range []string{"045_enterprise_attribution.sql", "046_tenant_roles.sql", "048_tenant_payer_and_invites.sql", "049_account_key_bindings.sql"} {
		body, err := migrations.FS.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
			t.Errorf("re-executing %s is not idempotent: %v", f, err)
		}
	}

	// tokens(tenant_id, employee_ref) is unique among LIVE, non-empty refs.
	mustExec(t, db, `INSERT INTO tokens (tenant_id, name, employee_ref) VALUES ('t1','a','E1')`)
	_, err := db.ExecContext(context.Background(), `INSERT INTO tokens (tenant_id, name, employee_ref) VALUES ('t1','b','E1')`)
	if !isUniqueViolation(err) {
		t.Errorf("duplicate live employee_ref = %v, want 23505", err)
	}
	mustExec(t, db, `INSERT INTO tokens (tenant_id, name, employee_ref) VALUES ('t2','c','E1')`)
	mustExec(t, db, `INSERT INTO tokens (tenant_id, name) VALUES ('t1','e1'),('t1','e2')`) // '' is unconstrained
	mustExec(t, db, `UPDATE tokens SET deleted_at = now() WHERE tenant_id='t1' AND employee_ref='E1'`)
	mustExec(t, db, `INSERT INTO tokens (tenant_id, name, employee_ref) VALUES ('t1','d','E1')`)

	// projects(tenant_id, external_code) likewise.
	mustExec(t, db, `INSERT INTO projects (tenant_id, name, external_code) VALUES ('t1','p1','D1')`)
	_, err = db.ExecContext(context.Background(), `INSERT INTO projects (tenant_id, name, external_code) VALUES ('t1','p2','D1')`)
	if !isUniqueViolation(err) {
		t.Errorf("duplicate live external_code = %v, want 23505", err)
	}
	mustExec(t, db, `INSERT INTO projects (tenant_id, name) VALUES ('t1','p3'),('t1','p4')`)

	// project_members unique triple.
	mustExec(t, db, `INSERT INTO project_members (tenant_id, project_id, user_id, created_at) VALUES ('t1',1,1,0)`)
	_, err = db.ExecContext(context.Background(), `INSERT INTO project_members (tenant_id, project_id, user_id, created_at) VALUES ('t1',1,1,0)`)
	if !isUniqueViolation(err) {
		t.Errorf("duplicate project member = %v, want 23505", err)
	}

	// account_key_bindings: one LIVE binding per (account, product).
	mustExec(t, db, `INSERT INTO account_key_bindings (identity_account_id, product, token_id) VALUES (5,'kova',1)`)
	_, err = db.ExecContext(context.Background(), `INSERT INTO account_key_bindings (identity_account_id, product, token_id) VALUES (5,'kova',2)`)
	if !isUniqueViolation(err) {
		t.Errorf("second live binding = %v, want 23505", err)
	}
	mustExec(t, db, `INSERT INTO account_key_bindings (identity_account_id, product, token_id) VALUES (5,'lutu',3)`)
	mustExec(t, db, `UPDATE account_key_bindings SET deleted_at = now() WHERE identity_account_id=5 AND product='kova'`)
	mustExec(t, db, `INSERT INTO account_key_bindings (identity_account_id, product, token_id) VALUES (5,'kova',4)`)
}

// TestIntegration045to049_RunnerFirst_SkipsAbsentTables: a DR database that has
// not created the data tables yet must not make 045-049 fail (the to_regclass
// guards turn them into warnings; AutoMigrate creates the columns at boot).
func TestIntegration045to049_RunnerFirst_SkipsAbsentTables(t *testing.T) {
	db := setupPG(t)
	// 047 indexes logs, so give it the one table whose absence is NOT guarded.
	mustExec(t, db, `CREATE TABLE logs (id bigserial PRIMARY KEY, tenant_id varchar(36), created_at bigint, project_id bigint)`)
	runThrough049(t, db)
	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("schema_migrations = %d, want %d", got, want)
	}
	if !tableExists(t, db, "account_key_bindings") || !tableExists(t, db, "project_members") {
		t.Fatal("tables owned by 046/049 must be created even when the data tables are absent")
	}
}
