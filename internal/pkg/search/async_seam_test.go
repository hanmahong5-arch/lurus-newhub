package search

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMain forces this package's fire-and-forget spawn seam inline for the
// whole test binary.
//
// Tests here swap the package globals Client / Enabled / SyncEnabled /
// RetryCount / RetryDelay / Debug / IndexPrefix and restore them from
// t.Cleanup (cov_helpers_test.go withFakeClient/withDisabled). The work the
// Sync*Async functions submit reads those same globals, and a pool submission
// has no join point, so the restore and the read run concurrently. Forcing
// AsyncGo inline means each submission has finished before the test that made
// it returns. Production is unaffected: AsyncGo's default value submits to
// asyncPool (sync.go).
func TestMain(m *testing.M) {
	AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}

// TestSyncLogsBatchAsync_HonoursSeam is the oracle for the seam: with AsyncGo
// forced inline (TestMain above), SyncLogsBatchAsync must have issued the
// index request by the time it returns — no polling, no sleep. A submission
// that goes straight to asyncPool.Go instead of through the seam leaves
// fm.requests() empty at that instant, which is what makes this test red.
func TestSyncLogsBatchAsync_HonoursSeam(t *testing.T) {
	_, fm := withFakeClient(t)
	SyncEnabled = true
	SyncBatchSize = 1000
	WorkerCount = 2
	SyncInterval = 0
	RetryCount = 1
	resetSyncGlobals(t)
	if err := InitSyncWithContext(context.Background()); err != nil {
		t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
	}

	SyncLogsBatchAsync([]*Log{sampleLog()})

	reqs := fm.requests()
	if len(reqs) != 1 {
		t.Fatalf("after SyncLogsBatchAsync returned, fm.requests() = %d (%+v), want exactly 1 — "+
			"the batch submission did not go through the AsyncGo seam", len(reqs), reqs)
	}
	if reqs[0].Method != http.MethodPost || reqs[0].Path != "/indexes/logs/documents" {
		t.Errorf("request = %s %s, want POST /indexes/logs/documents", reqs[0].Method, reqs[0].Path)
	}
}

// TestSyncAsyncFunctions_HonourSeam covers the other three submissions the
// same way, so a later edit cannot quietly route one of them back around the
// seam. Enumerated by grepping `asyncPool.Go(` and `AsyncGo(` in this package's
// non-test files: SyncLogAsync, SyncLogsBatchAsync (above), SyncUserAsync,
// SyncChannelAsync. InitSyncWithContext's `go ScheduledSyncWithContext(...)`
// is deliberately not in that set — it is a lifecycle loop joined by context
// cancellation (StopSync), not a fire-and-forget task, and forcing it inline
// would block until its context is cancelled.
func TestSyncAsyncFunctions_HonourSeam(t *testing.T) {
	cases := []struct {
		name string
		call func()
		path string
	}{
		{"SyncLogAsync", func() { SyncLogAsync(sampleLog()) }, "/indexes/logs/documents"},
		{"SyncUserAsync", func() { SyncUserAsync(sampleUser()) }, "/indexes/users/documents"},
		{"SyncChannelAsync", func() { SyncChannelAsync(&Channel{Id: 1, Name: "c"}) }, "/indexes/channels/documents"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, fm := withFakeClient(t)
			SyncEnabled = true
			WorkerCount = 2
			SyncInterval = 0
			RetryCount = 1
			resetSyncGlobals(t)
			if err := InitSyncWithContext(context.Background()); err != nil {
				t.Fatalf("InitSyncWithContext() error = %v, want nil", err)
			}

			tc.call()

			reqs := fm.requests()
			if len(reqs) != 1 || reqs[0].Path != tc.path {
				t.Fatalf("after %s returned, fm.requests() = %+v, want exactly one %s — "+
					"the submission did not go through the AsyncGo seam", tc.name, reqs, tc.path)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Structural gate for this package's spawns.
//
// This is a second copy of the scan in
// internal/adapter/handler/async_seam_structural_test.go, not a shared helper:
// a gate that walks "this package's directory" has to live in the package it
// guards, and sharing it would mean a new non-test package imported only by
// tests. The copy is small and its exemption list is this package's own; a
// third copy would be the moment to pay for the shared package. Four other
// packages now have an AsyncGo seam and NO gate (internal/adapter/repo,
// internal/adapter/middleware, internal/app, internal/app/governance) — that
// is a known limit of this cycle, not an oversight.
//
// What is enforced: every goroutine start the scan sees in this package's
// non-test sources is either a call to the AsyncGo seam, or is listed in
// searchSpawnExemptions with a reason AND the exact number of sites in that
// function — so a NEW spawn inside an already-exempted function still fails.
// What the scan sees: a bare `go` statement, and a call whose selector is `Go`
// or `CtxGo` on any receiver. What it does not see: a goroutine started inside
// a function this package calls but does not declare.
//
// Why here in particular: the cycle-12 plan §1.2 records the 2026-09-09 red
// -race run as an unjoined worker-pool submission in this package's sync.go.
// The seam fixed the submissions that existed; this keeps the next one from
// landing unseamed.

type searchSpawnExemption struct {
	file string
	// fn is the enclosing top-level function; "" means a package-level
	// declaration rather than a function body.
	fn     string
	sites  int
	reason string
}

var searchSpawnExemptions = []searchSpawnExemption{
	{
		file: "sync.go", fn: "", sites: 1,
		reason: "this IS the seam — `var AsyncGo = func(f func()) { asyncPool.Go(f) }` is the one place allowed to submit to asyncPool directly. The site count is what keeps that true: a second package-level submission fails here",
	},
	{
		file: "sync.go", fn: "InitSyncWithContext", sites: 1,
		reason: "`go ScheduledSyncWithContext(syncCtx, SyncInterval)` is a lifecycle loop, not a task: it returns when syncCtx is cancelled (StopSync), and the inline seam this package's TestMain installs would make InitSyncWithContext block for the process lifetime. Its join points are StopSync and the context; cov_sync_test.go's resetSyncGlobals waits for its exit log line",
	},
}

type searchSpawnSite struct {
	file string
	fn   string
	pos  token.Position
	form string
}

// searchRenderCallee renders a call's function expression: `Go`, `pool.Go`,
// `x.y.Go`. Anything it cannot render comes back as "".
func searchRenderCallee(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if prefix := searchRenderCallee(e.X); prefix != "" {
			return prefix + "." + e.Sel.Name
		}
		return e.Sel.Name
	}
	return ""
}

// searchCollectSpawnSites walks one parsed file and returns every goroutine
// start the matcher recognises, attributed to the enclosing top-level function.
func searchCollectSpawnSites(fset *token.FileSet, name string, file *ast.File) []searchSpawnSite {
	enclosing := func(pos token.Pos) string {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if pos >= fn.Pos() && pos <= fn.End() {
				return fn.Name.Name
			}
		}
		return ""
	}

	var sites []searchSpawnSite
	record := func(pos token.Pos, form string) {
		sites = append(sites, searchSpawnSite{file: name, fn: enclosing(pos), pos: fset.Position(pos), form: form})
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			record(node.Pos(), "bare `go` statement")
		case *ast.CallExpr:
			var selector string
			switch fn := node.Fun.(type) {
			case *ast.SelectorExpr:
				selector = fn.Sel.Name
			case *ast.Ident:
				selector = fn.Name
			default:
				return true
			}
			if selector != "Go" && selector != "CtxGo" {
				return true
			}
			callee := searchRenderCallee(node.Fun)
			if callee == "AsyncGo" {
				return true
			}
			if callee == "" {
				callee = selector
			}
			record(node.Pos(), callee+" call")
		}
		return true
	})
	return sites
}

// TestNoUnseamedSpawnsInSearchPackage fails when a non-test file here starts a
// goroutine outside the AsyncGo seam and outside searchSpawnExemptions, and
// when an exempted function's spawn count changes.
func TestNoUnseamedSpawnsInSearchPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	exemptByKey := map[string]searchSpawnExemption{}
	for _, e := range searchSpawnExemptions {
		exemptByKey[e.file+"::"+e.fn] = e
	}

	fset := token.NewFileSet()
	var (
		filesScanned int
		all          []searchSpawnSite
	)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		filesScanned++
		all = append(all, searchCollectSpawnSites(fset, name, file)...)
	}

	// Honesty guard 1: a scan that walked nothing must not report success.
	if filesScanned == 0 {
		t.Fatal("scanned 0 non-test .go files in this package — the scanner is broken (wrong CWD or a changed layout), not the package clean")
	}
	// Honesty guard 2: sync.go has contained both shapes since before this gate.
	if len(all) == 0 {
		t.Fatalf("scanned %d files and found 0 goroutine spawn sites — the matcher is broken; sync.go is known to contain both the `go` statement and the pool-call shapes", filesScanned)
	}

	byKey := map[string][]searchSpawnSite{}
	for _, s := range all {
		byKey[s.file+"::"+s.fn] = append(byKey[s.file+"::"+s.fn], s)
	}

	var problems []string
	for key, sites := range byKey {
		exemption, exempt := exemptByKey[key]
		if !exempt {
			for _, s := range sites {
				problems = append(problems, fmt.Sprintf(
					"%s: %s in %s() — route it through AsyncGo, or add it to searchSpawnExemptions with a reason", s.pos, s.form, s.fn))
			}
			continue
		}
		if len(sites) != exemption.sites {
			var where []string
			for _, s := range sites {
				where = append(where, fmt.Sprintf("%s (%s)", s.pos, s.form))
			}
			sort.Strings(where)
			problems = append(problems, fmt.Sprintf(
				"%s is exempted for %d spawn site(s) but now has %d: %s — the exemption covers the sites that were judged, not the function name. Current reason: %s",
				key, exemption.sites, len(sites), strings.Join(where, ", "), exemption.reason))
		}
	}
	// Honesty guard 3: an exemption that matches nothing is dead configuration.
	for key, e := range exemptByKey {
		if len(byKey[key]) == 0 {
			problems = append(problems, fmt.Sprintf(
				"stale exemption: %s (%s) — no unseamed spawn found there any more; delete this entry", key, e.reason))
		}
	}

	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("%s", p)
	}
}

// TestSearchSpawnMatcherSeesEverySpelling pins what the scan above recognises,
// against synthetic source with a known answer. Counting sites in the real
// package cannot catch a matcher that is blind to a spelling: an undercount
// satisfies the "found 0 sites" guard just as well as a full count does.
func TestSearchSpawnMatcherSeesEverySpelling(t *testing.T) {
	const synthetic = "package p\n" +
		"\n" +
		"func wantBareGo()  { go func() {}() }\n" +
		"func wantPoolGo()  { asyncPool.Go(func() {}) }\n" +
		"func wantPoolCtx() { asyncPool.CtxGo(nil, func() {}) }\n" +
		"func wantGroupGo() { g.Go(func() error { return nil }) }\n" +
		"func seamAsyncGo() { AsyncGo(func() {}) }\n" +
		"func notASpawn()   { fmt.Println(\"x\") }\n"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", synthetic, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}
	got := map[string]int{}
	for _, s := range searchCollectSpawnSites(fset, "synthetic.go", file) {
		got[s.fn]++
	}
	for fn, want := range map[string]int{"wantBareGo": 1, "wantPoolGo": 1, "wantPoolCtx": 1, "wantGroupGo": 1} {
		if got[fn] != want {
			t.Errorf("matcher saw %d spawn site(s) in %s(), want %d — that spelling is invisible to the gate", got[fn], fn, want)
		}
	}
	for _, fn := range []string{"seamAsyncGo", "notASpawn"} {
		if got[fn] != 0 {
			t.Errorf("matcher reported %d spawn site(s) in %s(), want 0 — a compliant call is being flagged", got[fn], fn)
		}
	}
}
