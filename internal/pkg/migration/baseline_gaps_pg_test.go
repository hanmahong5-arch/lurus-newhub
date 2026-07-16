package migration_test

// INTEGRATION coverage for migration 021_pg_baseline_gaps against a real
// PostgreSQL, gated on TEST_POSTGRES_DSN (the CI pg-integration job provides a
// disposable Postgres). These tests reproduce the production boot order —
// GORM AutoMigrate first, then the Runner — and prove that 021 closes the
// fresh-PG schema gaps that AutoMigrate alone cannot. Helpers setupPG,
// tableExists and countApplied live in runner_pg_test.go (same package).

import (
	"context"
	"database/sql"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// autoMigratePrereqs mirrors the production boot order: GORM AutoMigrate runs
// BEFORE the Runner and creates the tables that migration 021 then gap-fills.
// It deliberately OMITS the two tables 021 creates from scratch
// (internal_api_key_tenants, credit_pool_fund_events) so the negative-control
// test can prove they are absent until 021 runs.
func autoMigratePrereqs(t *testing.T, db *sql.DB) {
	t.Helper()
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	if err := gdb.AutoMigrate(
		&entity.Tenant{},
		&entity.TenantConfig{},
		&entity.UserIdentityMapping{},
		&entity.Release{},
		&entity.ReleaseArtifact{},
		&entity.DownloadLog{},
	); err != nil {
		t.Fatalf("AutoMigrate prereqs: %v", err)
	}
}

func scalarInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

// hasUniqueOnColumns reports whether `table` has a UNIQUE index covering exactly
// the given column set (order-independent), regardless of the index name — this
// keeps the assertion independent of the names migration 021 chose.
func hasUniqueOnColumns(t *testing.T, db *sql.DB, table string, cols ...string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT ix.indexrelid::text, a.attname
		FROM pg_index ix
		JOIN pg_class tb ON tb.oid = ix.indrelid
		JOIN pg_attribute a ON a.attrelid = tb.oid AND a.attnum = ANY (ix.indkey)
		WHERE tb.relname = $1 AND ix.indisunique
	`, table)
	if err != nil {
		t.Fatalf("query unique indexes on %q: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	idxCols := map[string]map[string]bool{}
	for rows.Next() {
		var idxID, col string
		if err := rows.Scan(&idxID, &col); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if idxCols[idxID] == nil {
			idxCols[idxID] = map[string]bool{}
		}
		idxCols[idxID][col] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := map[string]bool{}
	for _, c := range cols {
		want[c] = true
	}
	for _, got := range idxCols {
		if len(got) != len(want) {
			continue
		}
		match := true
		for c := range want {
			if !got[c] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// hasCheckOnColumn reports whether `table` has a CHECK constraint involving `col`.
func hasCheckOnColumn(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	return scalarInt(t, db, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		JOIN pg_class tb ON tb.oid = c.conrelid
		WHERE tb.relname = $1 AND c.contype = 'c' AND a.attname = $2
	`, table, col) > 0
}

// TestIntegration021_NegativeControl proves the gaps genuinely exist after
// AutoMigrate-only, so the gap-closing test below cannot pass hollowly.
func TestIntegration021_NegativeControl_GapsAbsentAfterAutoMigrate(t *testing.T) {
	db := setupPG(t)
	autoMigratePrereqs(t, db)

	if tableExists(t, db, "internal_api_key_tenants") {
		t.Error("internal_api_key_tenants present after AutoMigrate-only — negative control void")
	}
	if tableExists(t, db, "credit_pool_fund_events") {
		t.Error("credit_pool_fund_events present after AutoMigrate-only — negative control void")
	}
	if hasUniqueOnColumns(t, db, "releases", "product_id", "version") {
		t.Error("releases(product_id,version) unique present after AutoMigrate-only — negative control void")
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default'`); n != 0 {
		t.Errorf("default tenant present after AutoMigrate-only (=%d) — negative control void", n)
	}
}

// TestIntegration021_ClosesGaps runs the real boot order (AutoMigrate then the
// Runner, which executes 021 because BaselineThrough stops at 020) and asserts
// every TIER-1 gap is closed, then asserts a second Runner pass is an
// idempotent no-op.
func TestIntegration021_ClosesGaps_AndIdempotent(t *testing.T) {
	db := setupPG(t)
	autoMigratePrereqs(t, db)

	r := &migration.Runner{
		DB:              db,
		FS:              migrations.FS,
		BaselineThrough: "020_create_privacy_erasure_requests",
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Every discovered migration is recorded: 001-020 as baseline bookkeeping,
	// 021+ executed (BaselineThrough=020, so every version above it runs; 022 is
	// a no-op here since 021 seeds id=default, 023..025 skip absent tables via
	// their to_regclass guards, 026 creates model_rate_limits). The expected
	// total is derived from the embedded FS, not hard-coded.
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations = %d, want %d (all embedded migrations recorded)", got, want)
	}

	// §1 tables now present.
	if !tableExists(t, db, "internal_api_key_tenants") {
		t.Error("internal_api_key_tenants missing after 021")
	}
	if !tableExists(t, db, "credit_pool_fund_events") {
		t.Error("credit_pool_fund_events missing after 021")
	}

	// §2 composite uniques.
	for _, tc := range []struct {
		table string
		cols  []string
	}{
		{"user_identity_mapping", []string{"zitadel_user_id", "tenant_id"}},
		{"tenant_configs", []string{"tenant_id", "config_key"}},
		{"releases", []string{"product_id", "version"}},
		{"release_artifacts", []string{"release_id", "platform", "arch"}},
	} {
		if !hasUniqueOnColumns(t, db, tc.table, tc.cols...) {
			t.Errorf("missing composite unique on %s%v after 021", tc.table, tc.cols)
		}
	}

	// §3 CHECK constraints.
	for _, tc := range []struct{ table, col string }{
		{"releases", "release_type"},
		{"release_artifacts", "platform"},
		{"release_artifacts", "arch"},
		{"download_logs", "status"},
	} {
		if !hasCheckOnColumn(t, db, tc.table, tc.col) {
			t.Errorf("missing CHECK on %s.%s after 021", tc.table, tc.col)
		}
	}

	// §4 seed.
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default'`); n != 1 {
		t.Errorf("default tenant rows = %d, want 1", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_configs WHERE tenant_id = 'default'`); n != 16 {
		t.Errorf("default tenant_configs rows = %d, want 16", n)
	}

	// Idempotency: a second pass re-records nothing and the seed does not duplicate.
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("after rerun schema_migrations = %d, want %d", got, want)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_configs WHERE tenant_id = 'default'`); n != 16 {
		t.Errorf("after rerun tenant_configs rows = %d, want 16 (seed must not duplicate)", n)
	}
}

// TestIntegration021_PreExistingLegacyDefaultTenant_NoUniqueViolation reproduces
// the STAGE crashloop §4 exists to fix: a pre-existing tenants row hand-seeded
// with id='lurus-default', slug='lurus' (see deploy/k8s/r6-stage/seed-default-tenant.sql
// history) must not trip uni_tenants_slug when 021 tries to create id='default',
// and 021 must resolve/seed configs against the EXISTING row instead of
// aborting the whole migration transaction on a unique violation.
//
// The Runner has no "stop at version" bound, so r.Run() applies the full chain;
// 022_converge_default_tenant_id then converges the legacy id onto 'default'.
// The post-state assertions below reflect that converged end state (see inline).
func TestIntegration021_PreExistingLegacyDefaultTenant_NoUniqueViolation(t *testing.T) {
	db := setupPG(t)
	autoMigratePrereqs(t, db)

	const legacyID = "lurus-default"
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO tenants (id, zitadel_org_id, slug, name, status, plan_type, max_users, max_quota, created_at, updated_at)
		VALUES ($1, 'lurus-default-org', 'lurus', 'Lurus', 1, 'enterprise', 1000, 100000000000, NOW(), NOW())
	`, legacyID); err != nil {
		t.Fatalf("seed legacy default tenant: %v", err)
	}

	r := &migration.Runner{
		DB:              db,
		FS:              migrations.FS,
		BaselineThrough: "020_create_privacy_erasure_requests",
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run must not fail on pre-existing legacy default tenant (unique violation on slug='lurus'): %v", err)
	}

	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE slug = 'lurus'`); n != 1 {
		t.Errorf("tenants(slug='lurus') rows = %d, want 1 (021 must not create a second 'default' row)", n)
	}
	// r.Run() applies the FULL chain, so 022_converge_default_tenant_id runs after
	// 021 and renames the legacy slug='lurus' tenant from its non-canonical id onto
	// the canonical 'default', cascading every tenant-id column. The post-state
	// therefore reflects convergence: the tenant and the configs 021 seeded against
	// the legacy row both end up under id='default'. (021's own contract — not
	// aborting on uni_tenants_slug — is asserted by the r.Run() error check above.)
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default'`); n != 1 {
		t.Errorf("tenants(id='default') rows = %d, want 1 (022 converges the legacy id onto 'default')", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = $1`, legacyID); n != 0 {
		t.Errorf("tenants(id=%q) rows = %d, want 0 (022 converges the legacy id away)", legacyID, n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_configs WHERE tenant_id = 'default'`); n != 16 {
		t.Errorf("tenant_configs rows under 'default' = %d, want 16 (021 seeds against the legacy row, 022 repoints them to 'default')", n)
	}
}

// TestIntegration021_BothConflict_CanonicalIDWins covers the deterministic
// tie-break when BOTH a canonical id='default' row and a legacy slug='lurus'
// row (under a different id) exist: 021 must prefer id='default' per its
// `ORDER BY (id = 'default') DESC` resolution.
func TestIntegration021_BothConflict_CanonicalIDWins(t *testing.T) {
	db := setupPG(t)
	autoMigratePrereqs(t, db)

	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO tenants (id, zitadel_org_id, slug, name, status, plan_type, max_users, max_quota, created_at, updated_at)
		VALUES ('default', 'default-org', 'x', 'Default', 1, 'enterprise', 1000, 100000000000, NOW(), NOW())
	`); err != nil {
		t.Fatalf("seed canonical default tenant: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO tenants (id, zitadel_org_id, slug, name, status, plan_type, max_users, max_quota, created_at, updated_at)
		VALUES ('other', 'other-org', 'lurus', 'Other', 1, 'enterprise', 1000, 100000000000, NOW(), NOW())
	`); err != nil {
		t.Fatalf("seed legacy slug='lurus' tenant under a different id: %v", err)
	}

	r := &migration.Runner{
		DB:              db,
		FS:              migrations.FS,
		BaselineThrough: "020_create_privacy_erasure_requests",
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run must not fail with both a canonical and a legacy default-tenant row present: %v", err)
	}

	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_configs WHERE tenant_id = 'default'`); n != 16 {
		t.Errorf("tenant_configs rows for id='default' = %d, want 16 (canonical id must win the tie-break)", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_configs WHERE tenant_id = 'other'`); n != 0 {
		t.Errorf("tenant_configs rows for id='other' = %d, want 0 (seed must not also target the losing row)", n)
	}
}
