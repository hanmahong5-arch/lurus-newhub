package migration_test

// Structural gate + unit coverage for the `-- lurus:no-transaction`
// directive (cycle-13 L6). Hermetic: no Postgres, no disk beyond the
// embedded FS. The PG-tier behaviour (a marked file really does apply
// CREATE INDEX CONCURRENTLY, an unmarked one really is rejected with
// SQLSTATE 25001) lives in runner_no_transaction_pg_test.go.
//
// WHY a structural gate at all: a no-transaction file runs outside any
// transaction, so a statement that fails half way is not undone. The runner
// enforces the CREATE INDEX CONCURRENTLY allow-list at apply time — i.e. in
// production, during boot. This gate enforces the same rule against the
// shipped files, so the rejection lands in CI instead.

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// embeddedSQLBodies reads every *.sql at the root of the embedded FS.
func embeddedSQLBodies(t *testing.T) map[string][]byte {
	t.Helper()
	versions, err := migration.DiscoverVersions(migrations.FS)
	if err != nil {
		t.Fatalf("DiscoverVersions(migrations.FS): %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("DiscoverVersions(migrations.FS) returned nothing — this gate would be vacuous")
	}
	out := make(map[string][]byte, len(versions))
	for _, v := range versions {
		body, err := fs.ReadFile(migrations.FS, v+".sql")
		if err != nil {
			t.Fatalf("read %s.sql: %v", v, err)
		}
		out[v] = body
	}
	return out
}

// TestEmbeddedFS_NoTransactionFilesContainOnlyConcurrentIndexes is the gate
// the plan names: every shipped file that opts out of transactions must
// parse under the runner's own allow-list. Adding any other statement (an
// UPDATE, a DROP, a second DDL) to such a file turns this red.
//
// The sites-seen floor is what keeps the gate from reporting green because
// it found nothing to check: at least one shipped migration carries the
// directive (039), so a run that sees zero is a broken gate, not a clean
// tree.
func TestEmbeddedFS_NoTransactionFilesContainOnlyConcurrentIndexes(t *testing.T) {
	seen := 0
	for version, body := range embeddedSQLBodies(t) {
		if !migration.HasNoTransactionDirective(body) {
			continue
		}
		seen++
		stmts, err := migration.NoTransactionStatements(body)
		if err != nil {
			t.Errorf("%s.sql carries %s but does not parse: %v",
				version, migration.NoTransactionDirective, err)
			continue
		}
		if len(stmts) == 0 {
			t.Errorf("%s.sql carries %s and runs nothing", version, migration.NoTransactionDirective)
		}
		if _, err := migration.RequiredTable(body); err != nil {
			t.Errorf("%s.sql: %v", version, err)
		}
	}
	if seen == 0 {
		t.Fatalf("no embedded migration carries %s — the gate scanned nothing "+
			"(039_logs_tenant_created_index.sql should)", migration.NoTransactionDirective)
	}
}

// TestEmbeddedFS_ConcurrentlyImpliesNoTransactionDirective is the reverse
// gate. Without it the rule is one-directional: a file could hold a CREATE
// INDEX CONCURRENTLY, be executed inside the runner's per-file transaction,
// and fail the whole boot with SQLSTATE 25001 the first time it reaches a
// real database — after CI was green.
func TestEmbeddedFS_ConcurrentlyImpliesNoTransactionDirective(t *testing.T) {
	seen := 0
	for version, body := range embeddedSQLBodies(t) {
		text := string(body)
		// Comment lines mention CONCURRENTLY in prose (the repair steps in
		// 039's own header, for one), so only look at what executes.
		var executable []string
		for _, line := range strings.Split(text, "\n") {
			if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "--") {
				executable = append(executable, line)
			}
		}
		if !strings.Contains(strings.ToUpper(strings.Join(executable, "\n")), "CONCURRENTLY") {
			continue
		}
		seen++
		if !migration.HasNoTransactionDirective(body) {
			t.Errorf("%s.sql runs CONCURRENTLY but its first line is not %q — "+
				"the runner would wrap it in a transaction and PostgreSQL would reject it (25001)",
				version, migration.NoTransactionDirective)
		}
	}
	if seen == 0 {
		t.Fatalf("no embedded migration executes CONCURRENTLY — the gate scanned nothing")
	}
}

func TestNoTransactionStatements_RejectsAnythingButConcurrentIndex(t *testing.T) {
	cic := "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_x ON t (a);"
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"single concurrent index", cic, true},
		{"two concurrent indexes", cic + "\n" + cic, true},
		{"trailing comment", cic + " -- a note\n", true},
		{"lower case", strings.ToLower(cic), true},
		{"no trailing semicolon", strings.TrimSuffix(cic, ";"), true},
		{"plain create index", "CREATE INDEX IF NOT EXISTS idx_x ON t (a);", false},
		{"concurrent without if not exists", "CREATE INDEX CONCURRENTLY idx_x ON t (a);", false},
		{"update smuggled in", cic + "\nUPDATE logs SET quota = 0;", false},
		{"drop index", "DROP INDEX CONCURRENTLY IF EXISTS idx_x;", false},
		{"comments only", "-- nothing to do\n", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := migration.NoTransactionStatements([]byte(tc.body))
			if tc.ok && err != nil {
				t.Fatalf("NoTransactionStatements(%q) = %v, want accepted", tc.body, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("NoTransactionStatements(%q) accepted; want rejected", tc.body)
			}
		})
	}
}

// TestHasNoTransactionDirective_FirstLineOnly: the directive changes how a
// file executes, so it must be declared where a reviewer reads it, not
// buried in a header paragraph twenty lines down.
func TestHasNoTransactionDirective_FirstLineOnly(t *testing.T) {
	d := migration.NoTransactionDirective
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"first line", d + "\nCREATE INDEX CONCURRENTLY IF NOT EXISTS i ON t (a);", true},
		{"first line with trailing space", d + "  \nSELECT 1;", true},
		{"second line", "-- header\n" + d + "\nSELECT 1;", false},
		{"inside a sentence", "-- see " + d + " for why\nSELECT 1;", false},
		{"absent", "SELECT 1;", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := migration.HasNoTransactionDirective([]byte(tc.body)); got != tc.want {
				t.Fatalf("HasNoTransactionDirective(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestRequiredTable_ParsesAndValidates(t *testing.T) {
	d := migration.NoTransactionDirective
	rt := migration.RequiresTableDirective
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"absent", d + "\nCREATE INDEX CONCURRENTLY IF NOT EXISTS i ON t (a);", "", false},
		{"schema qualified", d + "\n" + rt + " public.logs\nSELECT 1;", "public.logs", false},
		{"bare name", d + "\n" + rt + " logs\nSELECT 1;", "logs", false},
		{"colon form", d + "\n" + rt + ": logs\nSELECT 1;", "logs", false},
		{"after other comments", d + "\n-- why\n--\n" + rt + " logs\nSELECT 1;", "logs", false},
		{"injection attempt", d + "\n" + rt + " logs'); DROP TABLE logs; --\nSELECT 1;", "", true},
		{"not in the comment header", d + "\nSELECT 1;\n" + rt + " logs\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := migration.RequiredTable([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("RequiredTable(%q) = %q, nil; want an error", tc.body, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("RequiredTable(%q): %v", tc.body, err)
			}
			if got != tc.want {
				t.Fatalf("RequiredTable(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// TestMigration039_DeclaresTheLogsIndexItsOwnHeaderPromises pins the two
// facts the rest of the lane depends on: the shipped 039 builds
// idx_logs_tenant_created_id, and it does so with the requires-table guard
// (without which the runner hard-fails on a database where AutoMigrate has
// not created `logs` — proven by running the empty-database integration
// test with the directive removed).
func TestMigration039_DeclaresTheLogsIndexItsOwnHeaderPromises(t *testing.T) {
	body, err := fs.ReadFile(migrations.FS, "039_logs_tenant_created_index.sql")
	if err != nil {
		t.Fatalf("read 039: %v", err)
	}
	if !migration.HasNoTransactionDirective(body) {
		t.Errorf("039 first line is not %q", migration.NoTransactionDirective)
	}
	table, err := migration.RequiredTable(body)
	if err != nil {
		t.Fatalf("039 RequiredTable: %v", err)
	}
	if table != "public.logs" {
		t.Errorf("039 requires-table = %q, want %q", table, "public.logs")
	}
	stmts, err := migration.NoTransactionStatements(body)
	if err != nil {
		t.Fatalf("039 statements: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("039 statement count = %d, want 1", len(stmts))
	}
	for _, want := range []string{"idx_logs_tenant_created_id", "tenant_id", "created_at DESC", "id DESC"} {
		if !strings.Contains(stmts[0], want) {
			t.Errorf("039 statement %q does not contain %q", stmts[0], want)
		}
	}
}
