package migration_test

// INTEGRATION coverage for migration 053 (abilities.modality,
// models.modality and the logs retrieval-usage columns) against a real
// PostgreSQL, gated on TEST_POSTGRES_DSN (setupPG skips without it, and -short
// skips integration tier tests like the 052 sibling). Runner-first on purpose:
// after an AutoMigrate boot 053 is a no-op, so the DDL really executes only on
// DR restores and bare databases.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/migrations"
)

// columnShape returns data_type, is_nullable and column_default of a column, or
// ok=false when it does not exist.
func columnShape(t *testing.T, db *sql.DB, table, column string) (dataType, nullable, def string, ok bool) {
	t.Helper()
	var dt, nl sql.NullString
	var d sql.NullString
	err := db.QueryRowContext(context.Background(), `
		SELECT data_type, is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&dt, &nl, &d)
	if err == sql.ErrNoRows {
		return "", "", "", false
	}
	if err != nil {
		t.Fatalf("column shape %s.%s: %v", table, column, err)
	}
	return dt.String, nl.String, d.String, true
}

func TestIntegration053_RunnerFirst_AppliesAndIsIdempotent(t *testing.T) {
	db := setupPG(t)
	mustExec(t, db, `
		CREATE TABLE tokens  (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text, deleted_at timestamptz);
		CREATE TABLE logs    (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', created_at bigint NOT NULL DEFAULT 0, project_id bigint NOT NULL DEFAULT 0);
		CREATE TABLE projects(id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default', name text, deleted_at timestamptz);
		CREATE TABLE users   (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL DEFAULT 'default');
		CREATE TABLE tenants (id varchar(36) PRIMARY KEY);
		CREATE TABLE tenant_invites (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL, code varchar(32) NOT NULL);
		CREATE TABLE abilities (channel_id bigint NOT NULL, model varchar(255) NOT NULL, enabled boolean);
		CREATE TABLE models (id bigserial PRIMARY KEY, model_name varchar(128) NOT NULL);
		INSERT INTO logs (tenant_id) VALUES ('t1');
		INSERT INTO abilities (channel_id, model, enabled) VALUES (1, 'model-a', true);
		INSERT INTO models (model_name) VALUES ('model-a');
	`)

	runThrough049(t, db) // executes everything after the 044 baseline, 053 included

	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("schema_migrations = %d, want %d", got, want)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM schema_migrations WHERE version = '053_modality_and_retrieval_usage'`); n != 1 {
		t.Fatalf("053 recorded %d/1", n)
	}

	cols := []struct {
		table, column, dataType, defaultHas string
	}{
		{"abilities", "modality", "character varying", "''"},
		{"models", "modality", "character varying", "''"},
		{"logs", "usage_unit", "character varying", "''"},
		{"logs", "usage_source", "character varying", "''"},
		{"logs", "usage_quantity", "bigint", "0"},
		{"logs", "retrieval_documents", "integer", "0"},
	}
	for _, c := range cols {
		dt, nullable, def, ok := columnShape(t, db, c.table, c.column)
		if !ok {
			t.Errorf("%s.%s missing", c.table, c.column)
			continue
		}
		if dt != c.dataType {
			t.Errorf("%s.%s type = %q, want %q", c.table, c.column, dt, c.dataType)
		}
		if nullable != "NO" {
			t.Errorf("%s.%s is_nullable = %q, want NO", c.table, c.column, nullable)
		}
		if !strings.Contains(def, c.defaultHas) {
			t.Errorf("%s.%s default = %q, want it to contain %s", c.table, c.column, def, c.defaultHas)
		}
	}
	// 16-char widths are part of the contract (GORM tags use varchar(16)).
	for _, c := range [][2]string{{"abilities", "modality"}, {"models", "modality"}, {"logs", "usage_unit"}, {"logs", "usage_source"}} {
		if n := scalarInt(t, db, `SELECT character_maximum_length FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, c[0], c[1]); n != 16 {
			t.Errorf("%s.%s length = %d, want 16", c[0], c[1], n)
		}
	}
	// Existing rows take the constant defaults, never NULL: '' = unknown.
	if n := scalarInt(t, db, `SELECT count(*) FROM abilities WHERE modality = ''`); n != 1 {
		t.Errorf("existing ability did not default to unknown modality (%d)", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM models WHERE modality = ''`); n != 1 {
		t.Errorf("existing model meta did not default to no override (%d)", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM logs WHERE usage_unit = '' AND usage_source = '' AND usage_quantity = 0 AND retrieval_documents = 0`); n != 1 {
		t.Errorf("existing log row did not take the legacy token-semantics defaults (%d)", n)
	}
	// No index on logs on purpose (see 051): none of the new columns may carry one.
	if n := scalarInt(t, db, `SELECT count(*) FROM pg_indexes WHERE tablename = 'logs'
		AND (indexdef ILIKE '%usage_unit%' OR indexdef ILIKE '%usage_quantity%' OR indexdef ILIKE '%usage_source%' OR indexdef ILIKE '%retrieval_documents%')`); n != 0 {
		t.Errorf("053 must not index logs, found %d", n)
	}

	// Idempotency: a second Run is a no-op and re-executing the SQL itself must not fail.
	runThrough049(t, db)
	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("second Run changed schema_migrations: %d, want %d", got, want)
	}
	body, err := migrations.FS.ReadFile("053_modality_and_retrieval_usage.sql")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE abilities SET modality = 'embedding'`)
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Errorf("re-executing 053 is not idempotent: %v", err)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM abilities WHERE modality = 'embedding'`); n != 1 {
		t.Errorf("re-execution disturbed existing values (%d)", n)
	}
}

// DR order: abilities, models and logs absent. Every ALTER sits behind a
// to_regclass guard that downgrades to a warning; AutoMigrate adds the columns
// at boot.
func TestIntegration053_RunnerFirst_SkipsAbsentTables(t *testing.T) {
	db := setupPG(t)
	mustExec(t, db, `CREATE TABLE tenants (id varchar(36) PRIMARY KEY)`)
	runThrough049(t, db)
	if tableExists(t, db, "abilities") || tableExists(t, db, "models") {
		t.Fatal("053 must not create the tables it only alters")
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM schema_migrations WHERE version = '053_modality_and_retrieval_usage'`); n != 1 {
		t.Fatalf("053 recorded %d/1 on a bare database", n)
	}
}
