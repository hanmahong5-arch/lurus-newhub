package migration_test

// INTEGRATION coverage for migration 052 (tenants.sedimentation_consent and
// the log_bodies archive table) against a real PostgreSQL, gated on
// TEST_POSTGRES_DSN (setupPG skips without it). Helpers live in the sibling
// *_pg_test.go files. Runner-first on purpose: after an AutoMigrate boot 052 is
// a no-op, so the DDL really executes only on DR restores and bare databases.

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/migrations"
)

func TestIntegration052_RunnerFirst_AppliesAndIsIdempotent(t *testing.T) {
	db := setupPG(t)
	mustExec(t, db, `
		CREATE TABLE tokens  (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text, deleted_at timestamptz);
		CREATE TABLE logs    (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', created_at bigint NOT NULL DEFAULT 0, project_id bigint NOT NULL DEFAULT 0);
		CREATE TABLE projects(id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text, deleted_at timestamptz);
		CREATE TABLE users   (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default');
		CREATE TABLE tenants (id varchar(36) PRIMARY KEY);
		CREATE TABLE tenant_invites (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL, code varchar(32) NOT NULL);
		INSERT INTO tenants (id) VALUES ('t1');
	`)

	runThrough049(t, db) // executes everything after the 044 baseline, 052 included

	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("schema_migrations = %d, want %d", got, want)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM schema_migrations WHERE version = '052_log_bodies_and_sedimentation_consent'`); n != 1 {
		t.Fatalf("052 recorded %d/1", n)
	}
	// An existing tenant takes the OFF default, never NULL: consent is opt-in.
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE sedimentation_consent = false`); n != 1 {
		t.Errorf("existing tenant did not default to no consent (%d)", n)
	}
	if !tableExists(t, db, "log_bodies") {
		t.Fatal("052 did not create log_bodies")
	}
	for _, idx := range []string{"idx_log_bodies_tenant_created", "idx_log_bodies_request_id", "idx_log_bodies_expires_at"} {
		if ex, valid, _ := indexState(t, db, idx); !ex || !valid {
			t.Errorf("index %s: exists=%v valid=%v", idx, ex, valid)
		}
	}
	// expires_at has no default on purpose: a row cannot be written without a deadline.
	if _, err := db.ExecContext(context.Background(), `INSERT INTO log_bodies (request_id) VALUES ('r')`); err == nil {
		t.Error("a log_bodies row without expires_at was accepted")
	}
	mustExec(t, db, `INSERT INTO log_bodies (request_id, tenant_id, expires_at, request_body) VALUES ('r','t1',1,'x')`)

	// Idempotency: a second Run is a no-op and re-executing the SQL itself must not fail.
	runThrough049(t, db)
	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("second Run changed schema_migrations: %d, want %d", got, want)
	}
	body, err := migrations.FS.ReadFile("052_log_bodies_and_sedimentation_consent.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Errorf("re-executing 052 is not idempotent: %v", err)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM log_bodies`); n != 1 {
		t.Errorf("re-execution disturbed existing rows (%d)", n)
	}
}

// DR order: tenants absent. The guard downgrades the ALTER to a warning, the
// CREATE TABLE half still runs, and AutoMigrate adds the column at boot.
func TestIntegration052_RunnerFirst_SkipsAbsentTenants(t *testing.T) {
	db := setupPG(t)
	mustExec(t, db, `CREATE TABLE logs (id bigserial PRIMARY KEY, tenant_id varchar(36), created_at bigint, project_id bigint)`)
	runThrough049(t, db)
	if !tableExists(t, db, "log_bodies") {
		t.Fatal("052 must create log_bodies even when tenants is absent")
	}
}
