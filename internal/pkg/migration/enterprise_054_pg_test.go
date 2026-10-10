package migration_test

// Migration 054 (routing_policies): INTEGRATION coverage against a real
// PostgreSQL, gated on TEST_POSTGRES_DSN (setupPG skips without it), plus one
// hermetic test that keeps the SQL, the GORM tags and the code default of
// min_confidence from drifting apart. Runner-first on purpose: after an
// AutoMigrate boot 054 is a no-op, so the DDL really executes only on DR
// restores and bare databases.

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/migrations"
)

// rp054Column reads one column's information_schema shape.
func rp054Column(t *testing.T, db *sql.DB, column string) (dataType, nullable, def string, ok bool) {
	t.Helper()
	var d sql.NullString
	err := db.QueryRowContext(context.Background(), `
		SELECT data_type, is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'routing_policies' AND column_name = $1`, column).
		Scan(&dataType, &nullable, &d)
	if err == sql.ErrNoRows {
		return "", "", "", false
	}
	if err != nil {
		t.Fatalf("column routing_policies.%s: %v", column, err)
	}
	return dataType, nullable, d.String, true
}

func TestIntegration054_RunnerFirst_AppliesAndIsIdempotent(t *testing.T) {
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

	runThrough049(t, db) // executes everything after the 044 baseline, 054 included

	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("schema_migrations = %d, want %d", got, want)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM schema_migrations WHERE version = '054_routing_policies'`); n != 1 {
		t.Fatalf("054 recorded %d/1", n)
	}
	if !tableExists(t, db, "routing_policies") {
		t.Fatal("054 did not create routing_policies")
	}

	// Column shapes and defaults: these are the contract the GORM tags and the
	// admin handler rely on, so a drift is a boot-time surprise.
	type want struct{ dataType, nullable, defContains string }
	for col, w := range map[string]want{
		"id":                {"bigint", "NO", "nextval"},
		"tenant_id":         {"character varying", "NO", "''"},
		"public_model":      {"character varying", "NO", ""},
		"strategy":          {"character varying", "NO", "'decision'"},
		"enabled":           {"boolean", "NO", "false"},
		"evaluator_model":   {"character varying", "NO", "''"},
		"instructions":      {"text", "NO", "''"},
		"min_confidence":    {"double precision", "NO", "0.65"},
		"default_candidate": {"character varying", "NO", "''"},
		"candidates":        {"jsonb", "NO", "'[]'"},
		"created_at":        {"bigint", "NO", "0"},
		"updated_at":        {"bigint", "NO", "0"},
	} {
		dt, nullable, def, ok := rp054Column(t, db, col)
		if !ok {
			t.Errorf("column %s missing", col)
			continue
		}
		if dt != w.dataType || nullable != w.nullable || !strings.Contains(def, w.defContains) {
			t.Errorf("column %s = (%s, nullable=%s, default=%q), want (%s, %s, default containing %q)",
				col, dt, nullable, def, w.dataType, w.nullable, w.defContains)
		}
	}

	if ex, valid, def := indexState(t, db, "uk_routing_policies_tenant_model"); !ex || !valid ||
		!strings.Contains(def, "UNIQUE") || !strings.Contains(def, "(tenant_id, public_model)") {
		t.Errorf("unique index: exists=%v valid=%v def=%q", ex, valid, def)
	}

	// A bare insert takes the documented born-disabled defaults.
	mustExec(t, db, `INSERT INTO routing_policies (tenant_id, public_model) VALUES ('t1', 'smart')`)
	if n := scalarInt(t, db, `SELECT count(*) FROM routing_policies WHERE enabled = false AND strategy = 'decision' AND min_confidence = 0.65 AND candidates = '[]'::jsonb`); n != 1 {
		t.Errorf("defaults not applied to a bare insert (%d/1)", n)
	}
	// One policy per (tenant, public model).
	if _, err := db.ExecContext(context.Background(), `INSERT INTO routing_policies (tenant_id, public_model) VALUES ('t1', 'smart')`); err == nil || !isUniqueViolation(err) {
		t.Errorf("duplicate (tenant, model) accepted or wrong error: %v", err)
	}
	mustExec(t, db, `INSERT INTO routing_policies (tenant_id, public_model) VALUES ('t2', 'smart')`) // another tenant, same model

	// Idempotency: a second Run is a no-op and re-executing the SQL itself must
	// neither fail nor disturb data.
	runThrough049(t, db)
	if got, want := countApplied(t, db), expectedFullMigrationCount(t); got != want {
		t.Fatalf("second Run changed schema_migrations: %d, want %d", got, want)
	}
	body, err := migrations.FS.ReadFile("054_routing_policies.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Errorf("re-executing 054 is not idempotent: %v", err)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM routing_policies`); n != 2 {
		t.Errorf("re-execution disturbed existing rows (%d/2)", n)
	}
}

// Hermetic: SQL default, GORM tag and the Go constant must say the same thing.
// "Close the flag" safety rests on a policy being born disabled and on 0.65
// being the same number everywhere.
func TestMigration054_SQLGormAndCodeDefaultsAgree(t *testing.T) {
	body, err := migrations.FS.ReadFile("054_routing_policies.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := strings.Join(strings.Fields(string(body)), " ") // whitespace-insensitive

	rt := reflect.TypeOf(entity.RoutingPolicy{})
	tag := func(field string) string {
		f, ok := rt.FieldByName(field)
		if !ok {
			t.Fatalf("entity.RoutingPolicy has no field %s", field)
		}
		return f.Tag.Get("gorm")
	}

	if !strings.Contains(sqlText, "min_confidence DOUBLE PRECISION NOT NULL DEFAULT 0.65") {
		t.Error("054 SQL no longer defaults min_confidence to 0.65")
	}
	if !strings.Contains(tag("MinConfidence"), "default:0.65") {
		t.Errorf("GORM tag drifted from the SQL default: %q", tag("MinConfidence"))
	}
	if entity.DefaultRoutingMinConfidence != 0.65 {
		t.Errorf("code default = %v, want 0.65", entity.DefaultRoutingMinConfidence)
	}

	if !strings.Contains(sqlText, "enabled BOOLEAN NOT NULL DEFAULT false") || !strings.Contains(tag("Enabled"), "default:false") {
		t.Error("enabled must default to false in both SQL and the GORM tag: a policy is born disabled")
	}
	if !strings.Contains(sqlText, "DEFAULT 'decision'") || !strings.Contains(tag("Strategy"), "default:'decision'") {
		t.Error("strategy default drifted between SQL and the GORM tag")
	}
	if !strings.Contains(sqlText, "candidates JSONB NOT NULL DEFAULT '[]'") || !strings.Contains(tag("Candidates"), "type:jsonb") {
		t.Error("candidates must be jsonb defaulting to '[]' in both SQL and the GORM tag")
	}
	if !strings.Contains(sqlText, "CREATE UNIQUE INDEX IF NOT EXISTS uk_routing_policies_tenant_model") ||
		!strings.Contains(tag("TenantID"), "uniqueIndex:uk_routing_policies_tenant_model,priority:1") ||
		!strings.Contains(tag("PublicModel"), "uniqueIndex:uk_routing_policies_tenant_model,priority:2") {
		t.Error("unique index name / column order drifted between SQL and the GORM tags")
	}
	if strings.Contains(strings.ToUpper(sqlText), "REFERENCES") {
		t.Error("054 must not declare foreign keys (tenants are soft-deleted)")
	}
}
