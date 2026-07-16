package migration_test

// Additional UNIT coverage for runner.go — no live Postgres required.
//
// Targets:
//   - DiscoverVersions edge cases (empty FS, non-.sql-only FS, single file,
//     numeric lex ordering, deeply nested paths ignored)
//   - AdvisoryLockID constant value (lock key contract)
//   - Embedded migrations.FS: exact file count (baseline contract = 20 files)
//   - Version lex ordering invariant: leading-zero numeric names must sort
//     correctly under plain string comparison (010 > 002 > 001 etc.)
//
// DB-gated functions (lock/unlock/ensureTracker/loadApplied/applyOne/markApplied
// and the baseline-partition loop inside Run) cannot be reached without a live
// *sql.DB; they are intentionally covered by runner_pg_test.go under the CI
// pg-integration gate (TEST_POSTGRES_DSN). We deliberately do NOT re-implement
// that partition logic in a pure test here: a test that mirrors production code
// and asserts the mirror would pass even if Run()'s real predicate regressed.

import (
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// ---------------------------------------------------------------------------
// AdvisoryLockID constant
// ---------------------------------------------------------------------------

// TestAdvisoryLockID_Value guards the 64-bit advisory lock key used to
// serialize concurrent Runner.Run() calls. The value encodes "LurusHub"
// in ASCII packed big-endian; changing it silently would allow concurrent
// migrations to race each other or conflict with platform's lock.
func TestAdvisoryLockID_Value(t *testing.T) {
	const wantHex = int64(0x4C75727573487562) // "LurusHub" big-endian
	if migration.AdvisoryLockID != wantHex {
		t.Errorf("AdvisoryLockID = 0x%X, want 0x%X", migration.AdvisoryLockID, wantHex)
	}
}

// ---------------------------------------------------------------------------
// DiscoverVersions edge cases
// ---------------------------------------------------------------------------

// TestDiscoverVersions_EmptyFS_ReturnsEmpty verifies that an FS with no
// files at all returns an empty (non-nil) slice without error.
func TestDiscoverVersions_EmptyFS_ReturnsEmpty(t *testing.T) {
	got, err := migration.DiscoverVersions(fstest.MapFS{})
	if err != nil {
		t.Fatalf("DiscoverVersions on empty FS: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("versions = %v, want empty slice", got)
	}
}

// TestDiscoverVersions_NonSQLFilesOnly_ReturnsEmpty confirms that a
// directory containing only non-.sql files produces an empty result.
func TestDiscoverVersions_NonSQLFilesOnly_ReturnsEmpty(t *testing.T) {
	got, err := migration.DiscoverVersions(fstest.MapFS{
		"README.md":   {Data: []byte("docs")},
		"001_foo.txt": {Data: []byte("not sql")},
		"rollback.sh": {Data: []byte("#!/bin/sh")},
	})
	if err != nil {
		t.Fatalf("DiscoverVersions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("versions = %v, want empty (no .sql files)", got)
	}
}

// TestDiscoverVersions_SingleFile_ReturnsSingleVersion checks the single-
// element path — important because lex sort of one item is trivially valid
// but the slice construction can still be broken.
func TestDiscoverVersions_SingleFile_ReturnsSingleVersion(t *testing.T) {
	got, err := migration.DiscoverVersions(fstest.MapFS{
		"021_add_thing.sql": {Data: []byte("-- single")},
	})
	if err != nil {
		t.Fatalf("DiscoverVersions: %v", err)
	}
	if len(got) != 1 || got[0] != "021_add_thing" {
		t.Errorf("versions = %v, want [021_add_thing]", got)
	}
}

// TestDiscoverVersions_LexOrder_NumericPaddedNames locks the fundamental
// ordering invariant: version names use zero-padded numbers so that plain
// lexicographic sort matches intended execution order. This documents the
// contract that breaks if a future migration is named without zero-padding
// (e.g. "21_x" would sort before "9_x", which itself sorts after "100_x").
func TestDiscoverVersions_LexOrder_NumericPaddedNames(t *testing.T) {
	fsys := fstest.MapFS{
		"020_last_baseline.sql":    {Data: []byte("--")},
		"001_first.sql":            {Data: []byte("--")},
		"100_far_future.sql":       {Data: []byte("--")},
		"021_first_executable.sql": {Data: []byte("--")},
		"010_middle_baseline.sql":  {Data: []byte("--")},
	}
	got, err := migration.DiscoverVersions(fsys)
	if err != nil {
		t.Fatalf("DiscoverVersions: %v", err)
	}
	want := []string{
		"001_first",
		"010_middle_baseline",
		"020_last_baseline",
		"021_first_executable",
		"100_far_future",
	}
	if len(got) != len(want) {
		t.Fatalf("versions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("versions[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDiscoverVersions_SubdirsIgnored_OnlyFlat confirms that .sql files
// inside subdirectories are never returned — rollback scripts live in
// sub-dirs and must not be auto-applied.
func TestDiscoverVersions_SubdirsIgnored_OnlyFlat(t *testing.T) {
	fsys := fstest.MapFS{
		"001_flat.sql":          {Data: []byte("--")},
		"rollback/001_flat.sql": {Data: []byte("-- rollback, must be ignored")},
		"down/002_x.sql":        {Data: []byte("-- down migration, must be ignored")},
		"nested/deep/003.sql":   {Data: []byte("-- deep nest, must be ignored")},
	}
	got, err := migration.DiscoverVersions(fsys)
	if err != nil {
		t.Fatalf("DiscoverVersions: %v", err)
	}
	if len(got) != 1 || got[0] != "001_flat" {
		t.Errorf("versions = %v, want only [001_flat] (subdirs must be skipped)", got)
	}
}

// TestDiscoverVersions_ExtensionEdgeCases confirms that ".sql" must be the
// FULL suffix: files ending in ".sql.bak" or starting with ".sql" should
// not match; only clean "*.sql" names count.
func TestDiscoverVersions_ExtensionEdgeCases(t *testing.T) {
	fsys := fstest.MapFS{
		"001_valid.sql":     {Data: []byte("--")},      // should match
		"002_partial.sql.x": {Data: []byte("-- bad")},  // should not match
		"003.SQL":           {Data: []byte("-- case")}, // wrong case — should not match
		".sql_hidden":       {Data: []byte("-- dot")},  // should not match
	}
	got, err := migration.DiscoverVersions(fsys)
	if err != nil {
		t.Fatalf("DiscoverVersions: %v", err)
	}
	if len(got) != 1 || got[0] != "001_valid" {
		t.Errorf("versions = %v, want only [001_valid]", got)
	}
}

// ---------------------------------------------------------------------------
// Embedded migrations.FS shape — extended contract checks
// ---------------------------------------------------------------------------

// TestEmbeddedFS_ExactBaselineCount asserts that the embedded FS contains
// EXACTLY 20 baseline files (001–020). More than 20 is fine (021+ are
// executables), but fewer would mean a baseline file was deleted.
func TestEmbeddedFS_ExactBaselineCount(t *testing.T) {
	versions, err := migration.DiscoverVersions(migrations.FS)
	if err != nil {
		t.Fatalf("DiscoverVersions(migrations.FS): %v", err)
	}
	const baselineThrough = "020_create_privacy_erasure_requests"
	var baselineCount int
	for _, v := range versions {
		if v <= baselineThrough {
			baselineCount++
		}
	}
	if baselineCount != 20 {
		t.Errorf("baseline versions (<=020) = %d, want exactly 20; versions=%v", baselineCount, versions)
	}
}

// TestEmbeddedFS_FirstVersionIsBaseline verifies that 001_create_tenants
// (the first-ever migration, MySQL-dialect, must NEVER execute) is the
// lexicographically first file in the embedded FS.
func TestEmbeddedFS_FirstVersionIsBaseline(t *testing.T) {
	versions, err := migration.DiscoverVersions(migrations.FS)
	if err != nil {
		t.Fatalf("DiscoverVersions(migrations.FS): %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("embedded FS is empty")
	}
	const wantFirst = "001_create_tenants"
	if versions[0] != wantFirst {
		t.Errorf("versions[0] = %q, want %q", versions[0], wantFirst)
	}
}

// TestEmbeddedFS_PostBaselineAreExecutable is a soft checkpoint (t.Logf, never
// fails) that lists the post-baseline (021+) migrations — the ones the Runner
// actually executes. It is a forcing function to eyeball the executable set
// when reviewing a migration change; the hard contract lives in
// TestEmbeddedFS_VersionsAreContiguous and the pg-integration count tests.
func TestEmbeddedFS_PostBaselineAreExecutable(t *testing.T) {
	versions, err := migration.DiscoverVersions(migrations.FS)
	if err != nil {
		t.Fatalf("DiscoverVersions(migrations.FS): %v", err)
	}
	const threshold = "020_create_privacy_erasure_requests"
	for _, v := range versions {
		if v > threshold {
			t.Logf("executable post-baseline migration: %q", v)
		}
	}
}

// migrationCountFloor is a ratchet: the number of embedded migrations only ever
// grows. The contiguity check below catches deletion of any MIDDLE migration
// (it leaves a gap), but deleting the HIGHEST-numbered file leaves 1..N-1 still
// contiguous — invisible to both contiguity and the FS-derived count assertions
// (which shrink to match). This floor is the one place that catches a dropped
// tail migration. Bump it by one when you add a migration.
const migrationCountFloor = 28

// TestEmbeddedFS_VersionsAreContiguous pins the migration file set structurally
// without hard-coding its exact size in 8 places: the numeric prefixes must be
// exactly 1, 2, …, N with no gap and no duplicate, and there must be at least
// migrationCountFloor of them. Together these catch a *.sql accidentally
// deleted anywhere (middle → gap; tail → below floor) — the FS-derived count
// assertions in the pg tests alone would silently pass on a deletion because
// their "want" shrinks to match. Adding migration NNN needs only the floor bump.
func TestEmbeddedFS_VersionsAreContiguous(t *testing.T) {
	versions, err := migration.DiscoverVersions(migrations.FS)
	if err != nil {
		t.Fatalf("DiscoverVersions(migrations.FS): %v", err)
	}
	if len(versions) < migrationCountFloor {
		t.Fatalf("embedded migrations = %d, want >= %d — a tail migration was deleted (or bump migrationCountFloor if you removed one intentionally)", len(versions), migrationCountFloor)
	}
	for i, v := range versions {
		prefix, _, ok := strings.Cut(v, "_")
		if !ok {
			t.Fatalf("version %q has no NNN_ numeric prefix", v)
		}
		n, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("version %q prefix %q is not numeric: %v", v, prefix, err)
		}
		if want := i + 1; n != want {
			t.Fatalf("migration versions are not contiguous at index %d: got %03d (%q), want %03d — a *.sql file is missing or misnumbered", i, n, v, want)
		}
	}
}
