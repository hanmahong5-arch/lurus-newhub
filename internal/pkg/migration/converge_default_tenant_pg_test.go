package migration_test

// INTEGRATION coverage for migration 022_converge_default_tenant_id against a
// real PostgreSQL, gated on TEST_POSTGRES_DSN (the CI pg-integration job
// provides a disposable Postgres). These tests isolate 022 by baselining
// everything through 021 (so the Runner executes ONLY 022) and hand-building a
// minimal schema that reproduces the STAGE split-brain: the credit pool + its
// children live under a non-canonical tenant id ('lurus-default') while users
// and channels already sit under the canonical 'default'. Helpers setupPG,
// countApplied, tableExists, scalarInt live in the sibling _pg_test.go files.

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

const baselineThrough021 = "021_pg_baseline_gaps"

// makeTenantTables hand-creates a minimal subset of the tenant-id-bearing
// schema. It deliberately OMITS several tables that migration 022 lists
// (redemptions, internal_api_key_tenants, playground_presets, …) so the run
// also proves 022's to_regclass guard skips absent tables without erroring.
func makeTenantTables(t *testing.T, db *sql.DB) {
	t.Helper()
	const ddl = `
CREATE TABLE tenants (
    id             varchar(36) PRIMARY KEY,
    zitadel_org_id varchar(128),
    slug           varchar(64) UNIQUE,
    name           varchar(128),
    status         int,
    plan_type      varchar(32),
    max_users      bigint,
    max_quota      bigint,
    created_at     timestamptz NOT NULL DEFAULT NOW(),
    updated_at     timestamptz NOT NULL DEFAULT NOW()
);
CREATE TABLE tenant_credit_pools (
    id               bigserial PRIMARY KEY,
    tenant_id        varchar(36) NOT NULL,
    parent_tenant_id varchar(36),
    current_balance  bigint,
    max_balance      bigint
);
CREATE TABLE tenant_credit_pool_draws (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL);
CREATE TABLE tenant_configs (tenant_id varchar(36) NOT NULL, config_key varchar(128) NOT NULL);
CREATE TABLE tokens (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL);
CREATE TABLE users  (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL);
CREATE TABLE channels (id bigserial PRIMARY KEY, tenant_id varchar(36) NOT NULL);
`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("create tenant tables: %v", err)
	}
}

// seedStageDrift reproduces the exact STAGE split-brain the oracle observed:
// the bootstrap tenant under id='lurus-default' (slug 'lurus'), its pool +
// draws + configs + one token + one user under 'lurus-default', a reseller
// child pool whose parent is 'lurus-default', and rows already orphaned under
// 'default' (one user, one channel) pointing at a tenant id that does not yet
// exist.
func seedStageDrift(t *testing.T, db *sql.DB) {
	t.Helper()
	const seed = `
INSERT INTO tenants (id, slug, name, status, plan_type, max_users, max_quota)
    VALUES ('lurus-default', 'lurus', 'Lurus', 1, 'enterprise', 1000, 100000000000);
-- A reseller sub-tenant with its own row + a child pool parented at the bootstrap
-- tenant. Its own tenant_id ('sub-reseller', distinct slug) must NOT be converged;
-- only its parent_tenant_id pointer should repoint lurus-default -> default.
INSERT INTO tenants (id, slug, name, status) VALUES ('sub-reseller', 'sub-reseller', 'Sub Reseller', 1);
INSERT INTO tenant_credit_pools (tenant_id, parent_tenant_id, current_balance, max_balance)
    VALUES ('lurus-default', NULL, 99904, 10000000);
INSERT INTO tenant_credit_pools (tenant_id, parent_tenant_id, current_balance, max_balance)
    VALUES ('sub-reseller', 'lurus-default', 500, 1000);
INSERT INTO tenant_credit_pool_draws (tenant_id) VALUES ('lurus-default'), ('lurus-default');
INSERT INTO tenant_configs (tenant_id, config_key) VALUES ('lurus-default', 'billing.currency'), ('lurus-default', 'quota.max_user_quota');
INSERT INTO tokens (tenant_id) VALUES ('lurus-default');
INSERT INTO users (tenant_id) VALUES ('lurus-default'), ('default');
INSERT INTO channels (tenant_id) VALUES ('default');
`
	if _, err := db.ExecContext(context.Background(), seed); err != nil {
		t.Fatalf("seed stage drift: %v", err)
	}
}

func runOnly022(t *testing.T, db *sql.DB) {
	t.Helper()
	r := &migration.Runner{DB: db, FS: migrations.FS, BaselineThrough: baselineThrough021}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run (execute 022): %v", err)
	}
}

// read022Body returns the raw 022 SQL so a test can exec it a second time and
// prove the SQL body itself is idempotent (the Runner alone would just skip an
// already-applied version, which does not exercise the body).
func read022Body(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(migrations.FS, "022_converge_default_tenant_id.sql")
	if err != nil {
		t.Fatalf("read 022 body: %v", err)
	}
	return string(b)
}

// TestIntegration022_NegativeControl proves the drift genuinely exists before
// 022 runs, so the convergence test below cannot pass hollowly.
func TestIntegration022_NegativeControl_DriftPresent(t *testing.T) {
	db := setupPG(t)
	makeTenantTables(t, db)
	seedStageDrift(t, db)

	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default'`); n != 0 {
		t.Errorf("canonical tenant id=default present before 022 (=%d) — negative control void", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE tenant_id = 'lurus-default'`); n != 1 {
		t.Errorf("pool under lurus-default = %d, want 1 — drift not seeded", n)
	}
	// The cash-path bug: users/channels reference 'default' but no such tenant
	// exists → the pool lookup by their tenant_id misses and billing is skipped.
	orphans := scalarInt(t, db, `SELECT count(*) FROM users WHERE tenant_id = 'default'`)
	if orphans == 0 {
		t.Error("expected orphan rows under tenant_id=default before 022 — negative control void")
	}
}

// TestIntegration022_Converges runs the real boot order (Runner executes only
// 022 because BaselineThrough stops at 021) and asserts the split-brain
// collapses onto the canonical id 'default', then proves both runner-level and
// SQL-body-level idempotency.
func TestIntegration022_Converges_AndIdempotent(t *testing.T) {
	db := setupPG(t)
	makeTenantTables(t, db)
	seedStageDrift(t, db)

	runOnly022(t, db)

	// 001-021 baselined, 022+ executed, and all discovered versions record a row
	// (023/024 skip absent tables via their to_regclass guards, 025 skips the
	// fixture's users table via its column-coverage guard — no username column —
	// but all still record; 026 creates model_rate_limits). The expected total is
	// derived from the embedded FS, not hard-coded.
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d (all embedded migrations recorded)", got, want)
	}

	// Tenant PK renamed; exactly one bootstrap tenant, under the canonical id.
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default' AND slug = 'lurus'`); n != 1 {
		t.Errorf("canonical tenant (id=default, slug=lurus) rows = %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'lurus-default'`); n != 0 {
		t.Errorf("non-canonical tenant id=lurus-default still present (=%d)", n)
	}

	// No 'lurus-default' may survive in ANY tenant-id column of the tables that
	// exist here — apply the check to every created table, incl. parent_tenant_id.
	for _, c := range []struct{ table, col string }{
		{"tenant_credit_pools", "tenant_id"},
		{"tenant_credit_pools", "parent_tenant_id"},
		{"tenant_credit_pool_draws", "tenant_id"},
		{"tenant_configs", "tenant_id"},
		{"tokens", "tenant_id"},
		{"users", "tenant_id"},
		{"channels", "tenant_id"},
	} {
		q := `SELECT count(*) FROM ` + c.table + ` WHERE ` + c.col + ` = 'lurus-default'`
		if n := scalarInt(t, db, q); n != 0 {
			t.Errorf("%s.%s still has %d rows under lurus-default after 022", c.table, c.col, n)
		}
	}

	// Positive: the pool and its children are now under 'default'; the reseller
	// child pool's parent repointed; pre-existing 'default' rows are untouched.
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE tenant_id = 'default'`); n != 1 {
		t.Errorf("pool under default = %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE parent_tenant_id = 'default'`); n != 1 {
		t.Errorf("child pool parent repointed to default = %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tokens WHERE tenant_id = 'default'`); n != 1 {
		t.Errorf("tokens under default = %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM users WHERE tenant_id = 'default'`); n != 2 {
		t.Errorf("users under default = %d, want 2 (1 converged + 1 pre-existing)", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pool_draws WHERE tenant_id = 'default'`); n != 2 {
		t.Errorf("draws under default = %d, want 2", n)
	}

	// No orphans remain: every distinct tenant_id across the converged tables
	// resolves to an existing tenant row.
	if n := scalarInt(t, db, `
		SELECT count(*) FROM (
			SELECT tenant_id FROM users
			UNION SELECT tenant_id FROM tokens
			UNION SELECT tenant_id FROM channels
			UNION SELECT tenant_id FROM tenant_credit_pools
		) s WHERE s.tenant_id NOT IN (SELECT id FROM tenants)`); n != 0 {
		t.Errorf("orphan tenant_id values with no tenant row after 022 = %d, want 0", n)
	}

	// Idempotency 1 — a second Runner pass re-records and re-executes nothing.
	runOnly022(t, db)
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("after rerun schema_migrations = %d, want %d", got, want)
	}

	// Idempotency 2 — executing the SQL body itself again is a clean no-op
	// (v_old resolves to NULL because the tenant is already canonical).
	if _, err := db.ExecContext(context.Background(), read022Body(t)); err != nil {
		t.Fatalf("re-exec 022 body: %v", err)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default'`); n != 1 {
		t.Errorf("after body re-exec canonical tenant rows = %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE tenant_id = 'default'`); n != 1 {
		t.Errorf("after body re-exec pool under default = %d, want 1", n)
	}
}

// TestIntegration022_FreshPG_NoOp proves the body does nothing on a database
// whose bootstrap tenant is already canonical (the fresh-PG path where 021's
// seed created id='default').
func TestIntegration022_FreshPG_NoOp(t *testing.T) {
	db := setupPG(t)
	makeTenantTables(t, db)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO tenants (id, slug, name, status) VALUES ('default', 'lurus', 'Lurus Platform', 1);
		 INSERT INTO tenant_credit_pools (tenant_id, current_balance, max_balance) VALUES ('default', 100, 1000);`); err != nil {
		t.Fatalf("seed canonical: %v", err)
	}

	runOnly022(t, db)

	// Scoped to the ids 022 can produce, NOT `count(*) FROM tenants`: runOnly022
	// runs every migration from 022 up, and 030 legitimately seeds an unrelated
	// `switch` tenant. An unqualified count silently turns "022 stayed a no-op"
	// into "no later migration ever inserts a tenant", which is not this test's
	// claim and breaks the next time anything seeds one.
	if n := scalarInt(t, db,
		`SELECT count(*) FROM tenants WHERE id IN ('default', 'lurus', 'lurus-default')`); n != 1 {
		t.Errorf("bootstrap tenant rows = %d, want 1 (no-op must not add rows)", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default'`); n != 1 {
		t.Errorf("canonical tenant rows = %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE tenant_id = 'default'`); n != 1 {
		t.Errorf("pool under default = %d, want 1 (must be untouched)", n)
	}
}

// TestIntegration022_AmbiguousDefault_Skips proves the safety guard: when a
// canonical id='default' tenant ALREADY coexists with a non-canonical
// slug='lurus' tenant, 022 refuses to auto-merge (child rows could belong to
// either) and leaves all data untouched.
func TestIntegration022_AmbiguousDefault_Skips(t *testing.T) {
	db := setupPG(t)
	makeTenantTables(t, db)
	// id=default carries a different slug (slug is UNIQUE, so the two cannot
	// share 'lurus'); the non-canonical tenant keeps slug='lurus'.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO tenants (id, slug, name, status) VALUES ('default', 'platform-root', 'Root', 1);
		 INSERT INTO tenants (id, slug, name, status) VALUES ('lurus-default', 'lurus', 'Lurus', 1);
		 INSERT INTO tenant_credit_pools (tenant_id, current_balance, max_balance) VALUES ('lurus-default', 5, 10);`); err != nil {
		t.Fatalf("seed ambiguous: %v", err)
	}

	runOnly022(t, db)

	// Both tenants must still exist; the lurus-default pool must be untouched.
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'lurus-default'`); n != 1 {
		t.Errorf("lurus-default tenant removed under ambiguity (=%d) — guard failed", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default'`); n != 1 {
		t.Errorf("default tenant rows = %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_credit_pools WHERE tenant_id = 'lurus-default'`); n != 1 {
		t.Errorf("pool moved under ambiguity (=%d) — guard must leave data untouched", n)
	}
}
