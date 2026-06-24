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

	// 20 baseline records + 021 executed = 21.
	if got := countApplied(t, db); got != 21 {
		t.Errorf("schema_migrations = %d, want 21 (20 baseline + 021)", got)
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
	if got := countApplied(t, db); got != 21 {
		t.Errorf("after rerun schema_migrations = %d, want 21", got)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_configs WHERE tenant_id = 'default'`); n != 16 {
		t.Errorf("after rerun tenant_configs rows = %d, want 16 (seed must not duplicate)", n)
	}
}

// TestIntegration021_PreexistingNonCanonicalDefaultTenant reproduces the STAGE
// boot-FATAL incident the §4 fix targets. The default tenant already existed
// under a NON-canonical id ('lurus-default') but the reserved slug ('lurus').
// The pre-fix §4 did `INSERT id='default' ... ON CONFLICT (id) DO NOTHING`,
// which did not collide on the PK, proceeded, and then violated
// uni_tenants_slug = UNIQUE(slug) → SQLSTATE 23505, aborting the single-tx
// migration → crashloop. The fresh-PG _ClosesGaps test cannot catch this (the
// pre-fix code is green there too); only a pre-seeded non-canonical tenant
// discriminates the fix. The fix resolves the existing default tenant by id OR
// the reserved slug before inserting, so this path must succeed and seed configs
// against the EXISTING id, never creating a second 'default' tenant.
func TestIntegration021_PreexistingNonCanonicalDefaultTenant(t *testing.T) {
	db := setupPG(t)
	autoMigratePrereqs(t, db)

	// Reproduce STAGE: default tenant hand-seeded under id='lurus-default' with
	// the reserved slug 'lurus' that 021's §4 seed also wants to claim.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO tenants (id, zitadel_org_id, slug, name, status, plan_type, max_users, max_quota, created_at, updated_at)
		 VALUES ('lurus-default', 'preexisting-org', 'lurus', 'Lurus Platform', 1, 'enterprise', 10000, 1000000000, NOW(), NOW())`); err != nil {
		t.Fatalf("seed pre-existing non-canonical default tenant: %v", err)
	}

	r := &migration.Runner{
		DB:              db,
		FS:              migrations.FS,
		BaselineThrough: "020_create_privacy_erasure_requests",
	}
	// Pre-fix, this Run aborts with SQLSTATE 23505 on uni_tenants_slug.
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run with pre-existing 'lurus-default'/'lurus' tenant (slug-clash regression): %v", err)
	}

	// The fix must reuse the existing row, never insert a second 'default'.
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE id = 'default'`); n != 0 {
		t.Errorf("a 'default' tenant was created (=%d); fix must reuse the existing 'lurus-default' row", n)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenants WHERE slug = 'lurus'`); n != 1 {
		t.Errorf("tenants with slug 'lurus' = %d, want exactly 1 (no duplicate)", n)
	}
	// Configs must be seeded against the RESOLVED existing id, not 'default'.
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_configs WHERE tenant_id = 'lurus-default'`); n != 16 {
		t.Errorf("tenant_configs for 'lurus-default' = %d, want 16 (seed must target the existing tenant)", n)
	}

	// Idempotency: a second pass stays a no-op (no extra tenant, no dup configs).
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if n := scalarInt(t, db, `SELECT count(*) FROM tenant_configs WHERE tenant_id = 'lurus-default'`); n != 16 {
		t.Errorf("after rerun tenant_configs for 'lurus-default' = %d, want 16 (seed must not duplicate)", n)
	}
}
