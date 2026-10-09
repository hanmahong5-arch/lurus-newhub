package migration_test

// INTEGRATION coverage for migrations 050 (relay data control) and 051
// (logs.channel_key_idx) against a real PostgreSQL, gated on
// TEST_POSTGRES_DSN. Helpers (setupPG, countApplied, tableExists, scalarInt,
// expectedFullMigrationCount, mustExec, runThrough049, baselineThrough044)
// live in the sibling *_pg_test.go files.
//
// RUNNER-FIRST on purpose: after a normal AutoMigrate boot 050/051 are no-ops,
// so the DDL only really executes on DR restores and bare databases - that is
// what is proven here, including pre-existing rows taking the new defaults and
// a re-execution of the files themselves.

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/migrations"
)

func TestIntegration050to051_RunnerFirst_AppliesAndIsIdempotent(t *testing.T) {
	db := setupPG(t)
	mustExec(t, db, `
		CREATE TABLE tokens  (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text, deleted_at timestamptz);
		CREATE TABLE logs    (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', created_at bigint NOT NULL DEFAULT 0, project_id bigint NOT NULL DEFAULT 0);
		CREATE TABLE projects(id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text, deleted_at timestamptz);
		CREATE TABLE users   (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default');
		CREATE TABLE tenants (id varchar(36) PRIMARY KEY);
		CREATE TABLE tenant_invites (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL, code varchar(32) NOT NULL);
		INSERT INTO tokens (tenant_id, name) VALUES ('t1', 'pre-existing');
		INSERT INTO logs (tenant_id, created_at) VALUES ('t1', 1);
		INSERT INTO tenants (id) VALUES ('t1');
	`)

	runThrough049(t, db) // the runner executes everything after the 044 baseline, 050 and 051 included

	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("schema_migrations = %d, want %d", got, want)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM schema_migrations WHERE version IN
		('050_data_policy','051_logs_channel_key_idx')`); n != 2 {
		t.Fatalf("050/051 recorded %d/2", n)
	}

	// Pre-existing rows land on the defaults, never NULL.
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE content_retention = ''`); n != 1 {
		t.Errorf("existing tenant did not take content_retention default (%d)", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tokens WHERE content_retention = ''`); n != 1 {
		t.Errorf("existing token did not take content_retention default (%d)", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM logs WHERE channel_key_idx = -1`); n != 1 {
		t.Errorf("existing log row did not take channel_key_idx = -1 (%d)", n)
	}
	for _, tbl := range []string{"content_rules", "channel_override_templates", "channel_template_applications"} {
		if !tableExists(t, db, tbl) {
			t.Errorf("050 did not create %s", tbl)
		}
	}

	// Idempotency: a second Run is a no-op, and re-executing the SQL itself
	// (a DR re-run) must not fail either.
	runThrough049(t, db)
	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("second Run changed schema_migrations: %d, want %d", got, want)
	}
	for _, f := range []string{"050_data_policy.sql", "051_logs_channel_key_idx.sql"} {
		body, err := migrations.FS.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
			t.Errorf("re-executing %s is not idempotent: %v", f, err)
		}
	}

	// The unique template name is a database guarantee.
	mustExec(t, db, `INSERT INTO channel_override_templates (name) VALUES ('t')`)
	if _, err := db.ExecContext(context.Background(), `INSERT INTO channel_override_templates (name) VALUES ('t')`); !isUniqueViolation(err) {
		t.Errorf("duplicate template name = %v, want 23505", err)
	}
}

// 050/051 on a database whose data tables do not exist yet (DR order): the
// to_regclass guards turn the ALTERs into warnings, the CREATE TABLE half still
// runs, and AutoMigrate adds the columns at boot.
func TestIntegration050to051_RunnerFirst_SkipsAbsentTables(t *testing.T) {
	db := setupPG(t)
	mustExec(t, db, `CREATE TABLE logs (id bigserial PRIMARY KEY, tenant_id varchar(36), created_at bigint, project_id bigint)`)
	runThrough049(t, db)
	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("schema_migrations = %d, want %d", got, want)
	}
	if !tableExists(t, db, "content_rules") || !tableExists(t, db, "channel_override_templates") {
		t.Fatal("050 must create its own tables even when tenants/tokens are absent")
	}
}
