package migration_test

// INTEGRATION coverage for the Runner against a real PostgreSQL,
// gated on TEST_POSTGRES_DSN (same env as the repo PG suite; the CI
// pg-integration job provides a disposable Postgres). Each test gets
// its own database so the advisory lock and schema_migrations state
// never leak between tests.

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

var migTestDBCounter atomic.Int64

// setupPG creates an isolated database and returns an open *sql.DB on
// it. Skips when TEST_POSTGRES_DSN is unset (mirrors repo.SetupTestDB).
func setupPG(t *testing.T) *sql.DB {
	t.Helper()
	baseDSN := os.Getenv("TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping PostgreSQL integration test")
	}

	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	dbName := fmt.Sprintf("test_migration_%d_%d", time.Now().UnixNano(), migTestDBCounter.Add(1))
	if _, err := admin.ExecContext(context.Background(), fmt.Sprintf(`CREATE DATABASE %q`, dbName)); err != nil {
		t.Fatalf("create test database %q: %v", dbName, err)
	}

	db, err := sql.Open("pgx", testDSN(baseDSN, dbName))
	if err != nil {
		t.Fatalf("open test database %q: %v", dbName, err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping test database %q: %v", dbName, err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if _, err := admin.ExecContext(context.Background(),
			fmt.Sprintf(`DROP DATABASE %q WITH (FORCE)`, dbName)); err != nil {
			t.Logf("drop test database %q: %v", dbName, err)
		}
		_ = admin.Close()
	})
	return db
}

// testDSN swaps the database name in a postgres:// URL or key-value DSN.
func testDSN(baseDSN, dbName string) string {
	if strings.HasPrefix(baseDSN, "postgres://") || strings.HasPrefix(baseDSN, "postgresql://") {
		if u, err := url.Parse(baseDSN); err == nil {
			u.Path = "/" + dbName
			return u.String()
		}
	}
	return baseDSN + " dbname=" + dbName
}

func countApplied(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM public.schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	return n
}

// expectedFullMigrationCount is the number of schema_migrations rows after a
// full r.Run() against the real embedded FS: one row per discovered *.sql
// (baseline recorded + 021+ executed). Deriving it from migrations.FS is the
// single source of truth — adding migration NNN no longer means hunting down
// every hard-coded "want N" literal across the pg tests (there were 8).
func expectedFullMigrationCount(t *testing.T) int {
	t.Helper()
	versions, err := migration.DiscoverVersions(migrations.FS)
	if err != nil {
		t.Fatalf("discover embedded migrations: %v", err)
	}
	return len(versions)
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
         WHERE table_schema = 'public' AND table_name = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("check table %q: %v", name, err)
	}
	return exists
}

// TestIntegrationRun_EmptyDB_BaselinesWithoutExecuting is the bookkeeping
// contract on a fresh database: Run() with the real embedded FS records every
// shipped migration as applied WITHOUT executing any of it. We baseline through
// the current head (022) here so the mechanic is exercised across all files —
// 001 is MySQL dialect (any execution would fail the Run) and 005 creates the
// releases tables (their absence proves no DDL ran). NOTE: production
// (internal/adapter/repo/main.go) baselines through 020 so that 021+ DO
// execute; that path is covered by baseline_gaps_pg_test.go and
// converge_default_tenant_pg_test.go.
func TestIntegrationRun_EmptyDB_BaselinesWithoutExecuting(t *testing.T) {
	db := setupPG(t)
	r := &migration.Runner{
		DB:              db,
		FS:              migrations.FS,
		BaselineThrough: "022_converge_default_tenant_id",
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run on empty DB: %v", err)
	}
	// Every discovered migration records a row: 001-022 as baseline bookkeeping,
	// 023+ executed above the baseline (023-025's to_regclass guards skip absent
	// tables on an empty DB but still record; 026+ CREATE their own tables). The
	// expected total is derived from the embedded FS, not hard-coded.
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("schema_migrations rows = %d, want %d (all embedded migrations recorded)", got, want)
	}
	if tableExists(t, db, "releases") {
		t.Error("releases table exists — a baseline migration was EXECUTED, not just recorded")
	}
	if tableExists(t, db, "tokens") {
		t.Error("tokens table exists — 023 must skip absent tables, not create them")
	}
	if !tableExists(t, db, "model_rate_limits") {
		t.Error("model_rate_limits missing — 026 (above baseline) must EXECUTE and create it")
	}
	// Idempotent: a second Run is a no-op.
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if want, got := expectedFullMigrationCount(t), countApplied(t, db); got != want {
		t.Errorf("after rerun schema_migrations rows = %d, want %d", got, want)
	}
}

// post-baseline fixture: 020 is deliberately MySQL-invalid (execution
// would fail loudly), 021 is deliberately NON-idempotent (re-execution
// would fail loudly) — so a green test proves 020 was only recorded and
// 021 ran exactly once.
func fixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"020_base.sql": {Data: []byte(
			"CREATE TABLE base (id INT) COMMENT 'mysql dialect, must never execute';")},
		"021_test.sql": {Data: []byte(
			"CREATE TABLE mig_smoke (id INT PRIMARY KEY); INSERT INTO mig_smoke VALUES (1);")},
	}
}

func TestIntegrationRun_AppliesPost021_OnceAndIdempotent(t *testing.T) {
	db := setupPG(t)
	r := &migration.Runner{DB: db, FS: fixtureFS(), BaselineThrough: "020_base"}
	for i := 0; i < 2; i++ {
		if err := r.Run(context.Background()); err != nil {
			t.Fatalf("Run #%d: %v", i+1, err)
		}
	}
	var rows int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM mig_smoke`).Scan(&rows); err != nil {
		t.Fatalf("mig_smoke was not created: %v", err)
	}
	if rows != 1 {
		t.Errorf("mig_smoke rows = %d, want 1 (021 must apply exactly once)", rows)
	}
	if got := countApplied(t, db); got != 2 {
		t.Errorf("schema_migrations rows = %d, want 2 (020 baseline + 021 applied)", got)
	}
}

// TestIntegrationRun_ConcurrentSerializedByAdvisoryLock: two Run()
// invocations race; the pg_advisory_lock must serialize them so the
// non-idempotent 021 executes exactly once and both calls return nil.
func TestIntegrationRun_ConcurrentSerializedByAdvisoryLock(t *testing.T) {
	db := setupPG(t)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := &migration.Runner{DB: db, FS: fixtureFS(), BaselineThrough: "020_base"}
			errs[i] = r.Run(context.Background())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent Run #%d: %v", i, err)
		}
	}
	var rows int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM mig_smoke`).Scan(&rows); err != nil {
		t.Fatalf("mig_smoke was not created: %v", err)
	}
	if rows != 1 {
		t.Errorf("mig_smoke rows = %d, want 1 (concurrent Runs double-applied)", rows)
	}
}

// TestIntegrationRun_RespectsPreseededRows simulates the STAGE shape:
// a version already recorded in schema_migrations (here via
// MarkApplied, on STAGE via the baseline) must not execute even when
// its SQL is present — the fixture's 021 SQL is replaced with garbage
// that would error if executed.
func TestIntegrationRun_RespectsPreseededRows(t *testing.T) {
	db := setupPG(t)
	r := &migration.Runner{
		DB: db,
		FS: fstest.MapFS{
			"021_test.sql": {Data: []byte("THIS IS NOT SQL; EXECUTION MUST NOT HAPPEN;")},
		},
	}
	if err := r.MarkApplied(context.Background(), []string{"021_test"}); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run with preseeded row executed its SQL: %v", err)
	}
	if got := countApplied(t, db); got != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", got)
	}
}
