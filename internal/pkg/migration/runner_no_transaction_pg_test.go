package migration_test

// INTEGRATION coverage for the `-- lurus:no-transaction` directive
// (cycle-13 L6). Gated on TEST_POSTGRES_DSN like the rest of the PG tier:
// the behaviour under test is a PostgreSQL rule (CREATE INDEX CONCURRENTLY
// is rejected with SQLSTATE 25001 inside a transaction block, including the
// IMPLICIT transaction a multi-statement simple-query message opens), so a
// fake driver would prove nothing.

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// cicBody is the statement pair both tests apply. TWO CREATE INDEX
// CONCURRENTLY statements on purpose: sending them as one multi-statement
// simple-query message would put them in an implicit transaction block and
// PostgreSQL would reject them, so a green result also proves the runner
// executes a no-transaction file one statement at a time.
const cicBody = `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_cic_a ON cic_target (a);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_cic_b ON cic_target (b);
`

func noTxFixtureFS(marked bool) fstest.MapFS {
	body := cicBody
	if marked {
		body = migration.NoTransactionDirective + "\n-- fixture\n" + cicBody
	}
	return fstest.MapFS{
		"021_cic_target.sql": {Data: []byte(
			"CREATE TABLE IF NOT EXISTS cic_target (a INT, b INT);")},
		"022_cic.sql": {Data: []byte(body)},
	}
}

func TestIntegrationRun_NoTransactionDirectiveAppliesConcurrentIndexes(t *testing.T) {
	db := setupPG(t)
	r := &migration.Runner{DB: db, FS: noTxFixtureFS(true)}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run with %s directive: %v", migration.NoTransactionDirective, err)
	}
	for _, idx := range []string{"idx_cic_a", "idx_cic_b"} {
		var valid bool
		if err := db.QueryRowContext(context.Background(),
			`SELECT i.indisvalid FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid
             WHERE c.relname = $1`, idx).Scan(&valid); err != nil {
			t.Fatalf("index %s was not created: %v", idx, err)
		}
		if !valid {
			t.Errorf("index %s exists but indisvalid=false", idx)
		}
	}
	if got := countApplied(t, db); got != 2 {
		t.Errorf("schema_migrations rows = %d, want 2 (021 + 022 recorded)", got)
	}
	// Idempotent: IF NOT EXISTS + the applied record make a second Run a no-op.
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
}

func TestIntegrationRun_ConcurrentIndexWithoutDirectiveFails(t *testing.T) {
	db := setupPG(t)
	r := &migration.Runner{DB: db, FS: noTxFixtureFS(false)}
	err := r.Run(context.Background())
	if err == nil {
		t.Fatalf("Run without the directive succeeded; CREATE INDEX CONCURRENTLY must be " +
			"rejected inside the runner's per-file transaction")
	}
	if !strings.Contains(err.Error(), "25001") && !strings.Contains(err.Error(), "transaction block") {
		t.Errorf("error = %v, want SQLSTATE 25001 / 'cannot run inside a transaction block'", err)
	}
	if got := countApplied(t, db); got != 1 {
		t.Errorf("schema_migrations rows = %d, want 1 (021 only — the failed 022 must not record)", got)
	}
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pg_class WHERE relname = 'idx_cic_a'`).Scan(&n); err != nil {
		t.Fatalf("count idx_cic_a: %v", err)
	}
	if n != 0 {
		t.Errorf("idx_cic_a exists = %d, want 0 (the transaction must have rolled back)", n)
	}
}

// TestIntegration039_BuildsLogsTenantCreatedIndex applies the SHIPPED
// migrations.FS (baselined through 038, so 039 is the only file that
// executes) against a database that already has a logs table — the state
// every real boot is in, because GORM AutoMigrate runs before the Runner
// (internal/adapter/repo/main.go). The companion skip path (no logs table)
// is covered by TestIntegrationRun_EmptyDB_BaselinesWithoutExecuting in
// runner_pg_test.go.
func TestIntegration039_BuildsLogsTenantCreatedIndex(t *testing.T) {
	db := setupPG(t)
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE logs (
            id BIGSERIAL PRIMARY KEY,
            tenant_id VARCHAR(36) NOT NULL DEFAULT 'default',
            created_at BIGINT NOT NULL DEFAULT 0
        )`); err != nil {
		t.Fatalf("create logs fixture: %v", err)
	}

	r := &migration.Runner{DB: db, FS: migrations.FS, BaselineThrough: "038_create_chat_sessions"}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run with the shipped FS: %v", err)
	}

	var def string
	var valid bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT pg_get_indexdef(i.indexrelid), i.indisvalid
         FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid
         WHERE c.relname = 'idx_logs_tenant_created_id'`).Scan(&def, &valid); err != nil {
		t.Fatalf("idx_logs_tenant_created_id was not created: %v", err)
	}
	if !valid {
		t.Error("idx_logs_tenant_created_id exists but indisvalid=false")
	}
	for _, want := range []string{"tenant_id", "created_at DESC", "id DESC"} {
		if !strings.Contains(def, want) {
			t.Errorf("index definition %q does not contain %q", def, want)
		}
	}

	// Second Run: IF NOT EXISTS + the applied record make it a no-op.
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
}
