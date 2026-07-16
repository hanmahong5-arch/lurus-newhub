package migration_test

// Reproduction fence for the 2026-07-15 STAGE crash-loop: the boot DSN
// injects statement_timeout on every pooled connection (P1-1), and a
// replica waiting on the Runner's pg_advisory_lock longer than that cap
// was cancelled by Postgres and died FATAL. Run()'s lock wait (and its
// migration DDL) must survive a cap far shorter than the holder's
// critical section. Gated on TEST_POSTGRES_DSN like the rest of the PG
// suite; reuses setupPG/testDSN/fixtureFS from runner_pg_test.go.

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
)

func TestIntegrationRun_LockWaitSurvivesStatementTimeout(t *testing.T) {
	db := setupPG(t)

	var dbName string
	if err := db.QueryRowContext(context.Background(),
		`SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatalf("current_database: %v", err)
	}

	base := os.Getenv("TEST_POSTGRES_DSN")
	u, err := url.Parse(testDSN(base, dbName))
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Skipf("TEST_POSTGRES_DSN is not URL-form; cannot inject statement_timeout (%v)", err)
	}
	q := u.Query()
	q.Set("statement_timeout", "150")
	u.RawQuery = q.Encode()
	capped, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatalf("open capped pool: %v", err)
	}
	t.Cleanup(func() { _ = capped.Close() })

	// The cap must actually bite — otherwise the assertions below pass
	// without exercising the exemption at all.
	if _, err := capped.Exec(`SELECT pg_sleep(0.5)`); err == nil {
		t.Fatal("statement_timeout=150ms did not cancel pg_sleep(0.5) — harness is not reproducing the boot DSN shape")
	}

	// Holder occupies the Runner's advisory lock on a dedicated session.
	holder, err := capped.Conn(context.Background())
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	defer func() { _ = holder.Close() }()
	if _, err := holder.ExecContext(context.Background(),
		`SELECT pg_advisory_lock($1)`, migration.AdvisoryLockID); err != nil {
		t.Fatalf("holder acquire: %v", err)
	}

	r := &migration.Runner{DB: capped, FS: fixtureFS(), BaselineThrough: "020_base"}
	runErr := make(chan error, 1)
	go func() { runErr <- r.Run(context.Background()) }()

	// 600ms = 4x the cap; the pre-fix code was already dead at 150ms with
	// "canceling statement due to statement timeout".
	select {
	case err := <-runErr:
		t.Fatalf("Run returned while lock still held: %v", err)
	case <-time.After(600 * time.Millisecond):
	}

	if _, err := holder.ExecContext(context.Background(),
		`SELECT pg_advisory_unlock($1)`, migration.AdvisoryLockID); err != nil {
		t.Fatalf("holder release: %v", err)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("Run did not survive the capped lock wait: %v", err)
	}

	// The run must have really executed 021 on the capped pool — the DDL
	// path shares the exemption (SET LOCAL inside each migration tx).
	var rows int
	if err := capped.QueryRowContext(context.Background(),
		`SELECT count(*) FROM mig_smoke`).Scan(&rows); err != nil {
		t.Fatalf("mig_smoke was not created: %v", err)
	}
	if rows != 1 {
		t.Errorf("mig_smoke rows = %d, want 1", rows)
	}
}
