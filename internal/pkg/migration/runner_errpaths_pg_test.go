package migration_test

// INTEGRATION coverage for the Runner's ERROR/ROLLBACK arms against a
// real PostgreSQL (gated on TEST_POSTGRES_DSN, same tier as
// runner_pg_test.go). These tests drive the paths that the happy-path
// PG tests never reach:
//
//   - Run(): non-nil DB + nil FS guard, and a failing migration SQL that
//     must roll its transaction back so a bad deploy leaves NO trace.
//   - applyOne(): SQL exec error (transaction rollback), and read-file
//     error for a discovered-but-unreadable version.
//   - lock / ensureTracker / loadApplied / markApplied / unlock: DB-level
//     failures surfaced by running against a CLOSED *sql.DB, so the
//     boot-time gatekeeper reports a real error instead of proceeding on
//     a dead connection.
//
// Everything here uses a real *sql.DB (from setupPG in runner_pg_test.go)
// so the pgx driver produces genuine errors, not a hand-rolled mock.

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
)

// TestRun_NilFS_WithLiveDB_Errors reaches the FS-nil guard AFTER the
// DB-nil guard passes: a real *sql.DB is present but FS is nil. The
// pure-unit test (nil DB + nil FS) short-circuits at the DB guard, so
// this is the only way to cover the FS branch.
func TestRun_NilFS_WithLiveDB_Errors(t *testing.T) {
	db := setupPG(t)
	r := &migration.Runner{DB: db, FS: nil}
	err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Runner.FS is nil") {
		t.Fatalf("expected 'Runner.FS is nil' error, got %v", err)
	}
}

// TestRun_FailingMigrationSQL_RollsBackAndReportsError proves the
// gatekeeper's core safety property: when a migration's SQL fails
// mid-way, applyOne rolls the transaction back so NEITHER the DDL side
// effect NOR the schema_migrations bookkeeping row survives, and Run
// returns a wrapped "apply <version>" error. A half-migrated tenant DB
// is exactly the failure this must prevent.
func TestRun_FailingMigrationSQL_RollsBackAndReportsError(t *testing.T) {
	db := setupPG(t)
	// 021 creates a table, then issues invalid SQL in the SAME file so
	// the whole statement batch executes in one tx; the second statement
	// errors, forcing a rollback that must also undo the CREATE TABLE.
	fsys := fstest.MapFS{
		"021_bad.sql": {Data: []byte(
			"CREATE TABLE mig_should_not_survive (id INT);\n" +
				"THIS IS NOT VALID SQL AND MUST ABORT THE TX;")},
	}
	r := &migration.Runner{DB: db, FS: fsys, BaselineThrough: ""}

	err := r.Run(context.Background())
	if err == nil {
		t.Fatal("expected Run to fail on invalid migration SQL, got nil")
	}
	if !strings.Contains(err.Error(), "apply 021_bad") {
		t.Errorf("error should be wrapped with 'apply 021_bad', got %v", err)
	}

	// Rollback proof #1: the table created in the failed tx must not exist.
	if tableExists(t, db, "mig_should_not_survive") {
		t.Error("mig_should_not_survive exists — failed migration was NOT rolled back")
	}
	// Rollback proof #2: no bookkeeping row for the failed version.
	// (schema_migrations itself was created by ensureTracker, outside the
	// per-migration tx, so it exists but must be empty.)
	if got := countApplied(t, db); got != 0 {
		t.Errorf("schema_migrations rows = %d, want 0 (failed migration must not record)", got)
	}
}

// TestRun_DiscoveredFileUnreadable_ReadFileError covers applyOne's
// fs.ReadFile error arm: DiscoverVersions lists a version, but ReadFile
// on the backing file fails (a corrupt/unreadable migration file). We
// use a real DB so the run gets past lock/ensureTracker and actually
// reaches applyOne. The version is NOT baselined, so Run tries to apply
// it and hits the read error.
func TestRun_DiscoveredFileUnreadable_ReadFileError(t *testing.T) {
	db := setupPG(t)
	r := &migration.Runner{DB: db, FS: readErrAfterDiscoverFS{}, BaselineThrough: ""}
	err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "read 021_ghost.sql") {
		t.Fatalf("expected 'read 021_ghost.sql' error, got %v", err)
	}
}

// acquireErr reports whether err surfaces a closed-pool failure at either the
// dedicated-connection acquisition (the new first DB op — the run pins one
// connection for the whole critical section) or the advisory-lock acquisition.
// Both are legitimate first-failure points; the property under test is that a
// closed pool surfaces an error rather than panicking or silently proceeding.
func acquireErr(err error) bool {
	return err != nil &&
		(strings.Contains(err.Error(), "acquire connection") ||
			strings.Contains(err.Error(), "acquire advisory lock"))
}

// TestMarkApplied_ClosedDB_LockError: a closed connection pool makes the
// operator tooling entrypoint report the acquire error instead of silently
// skipping the lock.
func TestMarkApplied_ClosedDB_LockError(t *testing.T) {
	db := setupPG(t)
	closeDB(t, db)
	r := &migration.Runner{DB: db, FS: fstest.MapFS{}}
	err := r.MarkApplied(context.Background(), []string{"021_x"})
	if !acquireErr(err) {
		t.Fatalf("expected a connection/advisory-lock acquire error, got %v", err)
	}
}

// TestRun_ClosedDB_LockError drives Run's failure on the very first DB
// operation a booting pod performs. A closed pool must surface as an acquire
// error, not a panic or a silent proceed.
func TestRun_ClosedDB_LockError(t *testing.T) {
	db := setupPG(t)
	closeDB(t, db)
	r := &migration.Runner{DB: db, FS: fstest.MapFS{"021_x.sql": {Data: []byte("SELECT 1;")}}}
	err := r.Run(context.Background())
	if !acquireErr(err) {
		t.Fatalf("expected a connection/advisory-lock acquire error, got %v", err)
	}
}

// readErrAfterDiscoverFS is an fs.FS that lists exactly one *.sql file so
// DiscoverVersions returns "021_ghost", but whose Open of that file
// yields a handle whose Read fails — driving applyOne's fs.ReadFile
// error arm with a discovered-but-unreadable migration file. It
// implements fs.ReadDirFS so DiscoverVersions' fs.ReadDir(".") succeeds.
type readErrAfterDiscoverFS struct{}

var errUnreadableMigration = errors.New("migration file read error (simulated corrupt file)")

func (readErrAfterDiscoverFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	return []fs.DirEntry{ghostDirEntry{}}, nil
}

func (readErrAfterDiscoverFS) Open(name string) (fs.File, error) {
	if name == "021_ghost.sql" {
		return &readErrFile{}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// ghostDirEntry is the single non-dir "021_ghost.sql" entry ReadDir
// reports so DiscoverVersions yields the version "021_ghost".
type ghostDirEntry struct{}

func (ghostDirEntry) Name() string               { return "021_ghost.sql" }
func (ghostDirEntry) IsDir() bool                { return false }
func (ghostDirEntry) Type() fs.FileMode          { return 0 }
func (ghostDirEntry) Info() (fs.FileInfo, error) { return ghostFileInfo{}, nil }

// readErrFile is an fs.File whose Read always fails, so fs.ReadFile
// (used by applyOne) returns errUnreadableMigration.
type readErrFile struct{}

func (*readErrFile) Stat() (fs.FileInfo, error) { return ghostFileInfo{}, nil }
func (*readErrFile) Read([]byte) (int, error)   { return 0, errUnreadableMigration }
func (*readErrFile) Close() error               { return nil }

type ghostFileInfo struct{}

func (ghostFileInfo) Name() string       { return "021_ghost.sql" }
func (ghostFileInfo) Size() int64        { return 1 }
func (ghostFileInfo) Mode() fs.FileMode  { return 0 }
func (ghostFileInfo) ModTime() time.Time { return time.Time{} }
func (ghostFileInfo) IsDir() bool        { return false }
func (ghostFileInfo) Sys() interface{}   { return nil }

// ensure the io import is used even if the compiler folds paths.
var _ io.Reader = (*readErrFile)(nil)

// closeDB closes the pool and registers no-op cleanup override; setupPG's
// Cleanup will Close again (idempotent) and DROP the database via the
// admin connection, which is unaffected by closing the test pool.
func closeDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}
