package migration_test

// INTEGRATION coverage for PendingVersions against a real PostgreSQL, gated on
// TEST_POSTGRES_DSN like the rest of this package's PG tier. The probe uses
// to_regclass and reads public.schema_migrations, so it has no meaningful
// SQLite equivalent; reuses setupPG/fixtureFS from runner_pg_test.go.

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
)

// statusFS is a three-version fixture: 001 sits at/below the baseline used in
// these tests, 002 and 003 are executable versions.
func statusFS() fstest.MapFS {
	return fstest.MapFS{
		"001_baseline.sql": &fstest.MapFile{Data: []byte(`SELECT 1;`)},
		"002_second.sql":   &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS status_probe_two (id int);`)},
		"003_third.sql":    &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS status_probe_three (id int);`)},
	}
}

// The whole point of this helper is to answer on a database the Runner has
// never touched — that is the MIGRATIONS_AUTO_RUN=false / no-master-replica
// case. A missing tracker table must read as "nothing applied", not as an error.
func TestIntegrationPendingVersions_NoTrackerTableYet(t *testing.T) {
	db := setupPG(t)

	pending, applied, err := migration.PendingVersions(
		context.Background(), db, statusFS(), "001_baseline")
	if err != nil {
		t.Fatalf("PendingVersions on a virgin database: %v", err)
	}
	if applied != 0 {
		t.Errorf("applied = %d, want 0", applied)
	}
	if len(pending) != 2 || pending[0] != "002_second" || pending[1] != "003_third" {
		t.Errorf("pending = %v, want the two above-baseline versions in order", pending)
	}
}

// After a full run nothing may be reported pending — otherwise the gauge would
// page on every healthy deployment.
func TestIntegrationPendingVersions_AfterRunNothingPending(t *testing.T) {
	db := setupPG(t)
	fsys := statusFS()

	runner := &migration.Runner{DB: db, FS: fsys, BaselineThrough: "001_baseline"}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}

	pending, applied, err := migration.PendingVersions(
		context.Background(), db, fsys, "001_baseline")
	if err != nil {
		t.Fatalf("PendingVersions: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %v, want none after a successful run", pending)
	}
	// Baseline versions are recorded too, so all three count as applied.
	if applied != 3 {
		t.Errorf("applied = %d, want 3 (baseline is recorded, just not executed)", applied)
	}
}

// The drift case this exists to catch: code ships a new migration file, the pod
// never runs the Runner, and the schema is one version behind.
func TestIntegrationPendingVersions_NewVersionAfterRunIsPending(t *testing.T) {
	db := setupPG(t)
	fsys := statusFS()

	runner := &migration.Runner{DB: db, FS: fsys, BaselineThrough: "001_baseline"}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}

	// A newer build carries one more migration that this pod never applied.
	shipped := statusFS()
	shipped["004_fourth.sql"] = &fstest.MapFile{Data: []byte(`SELECT 1;`)}

	pending, _, err := migration.PendingVersions(
		context.Background(), db, shipped, "001_baseline")
	if err != nil {
		t.Fatalf("PendingVersions: %v", err)
	}
	if len(pending) != 1 || pending[0] != "004_fourth" {
		t.Fatalf("pending = %v, want exactly [004_fourth]", pending)
	}
}

// An unrecorded baseline version is bookkeeping-only — there is no DDL behind
// it, so reporting it as pending would fire the alert on every fresh database.
func TestIntegrationPendingVersions_BaselineNeverCountsAsPending(t *testing.T) {
	db := setupPG(t)

	pending, _, err := migration.PendingVersions(
		context.Background(), db, statusFS(), "003_third")
	if err != nil {
		t.Fatalf("PendingVersions: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %v, want none when everything is at or below the baseline", pending)
	}
}

// PendingVersions must not take the advisory lock: an observability read that
// blocked behind a running migration would hang the very boot it observes.
func TestIntegrationPendingVersions_DoesNotContendWithTheRunnerLock(t *testing.T) {
	db := setupPG(t)

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(context.Background(),
		`SELECT pg_advisory_lock($1)`, migration.AdvisoryLockID); err != nil {
		t.Fatalf("hold the runner's advisory lock: %v", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(),
			`SELECT pg_advisory_unlock($1)`, migration.AdvisoryLockID)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := migration.PendingVersions(ctx, db, statusFS(), "001_baseline"); err != nil {
		t.Fatalf("PendingVersions blocked or failed while the runner lock was held: %v", err)
	}
}
