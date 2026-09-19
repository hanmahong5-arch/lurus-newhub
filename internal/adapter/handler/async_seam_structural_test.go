package handler

// async_seam_structural_test.go — cycle-12 L1's structural gate.
//
// Every goroutine this package starts must go through the AsyncGo seam
// (model_meta.go) unless it is on the exemption list below with a reason.
// Without the seam a spawn has no join point, so it keeps reading package
// globals (repo.DB, common.RDB, common.RedisEnabled, releaseService …) after
// the test that caused it has restored or closed them. That is the failure
// mode behind three of the four red -race runs the cycle-12 plan §1.1/§1.2
// records around 2026-09 (the fourth, 09-08, has no recorded cause). -race
// needs cgo and does not run on the dev host, so a structural scan is the part
// of it that can be enforced everywhere.
//
// The scan is a go/ast walk over this package's real non-test sources, not a
// hand-maintained list, so a spawn added tomorrow fails here the moment it
// lands. Three honesty guards: zero files scanned fails, zero spawn sites
// found fails, and an exemption that matches nothing fails as dead
// configuration, so a broken scanner cannot pass vacuously.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// seamExemption names one function, in one file of this package, that is
// allowed to start a goroutine without the AsyncGo seam, plus why.
//
// The key is file + enclosing function rather than a line number, because
// line numbers drift with every edit to the file and a drifted exemption
// silently stops matching.
type seamExemption struct {
	file   string
	fn     string
	reason string
}

// unseamedSpawnExemptions is the whole exemption set. Two shapes qualify:
//
//   - the goroutine is joined before the function that started it returns
//     (sync.WaitGroup / channel), so it cannot outlive a test; and
//   - the goroutine is a loop whose lifetime is a program lifetime, joined by
//     context cancellation, which the inline seam used by this package's
//     TestMain would turn into a blocking call.
//
// Anything else belongs in AsyncGo.
var unseamedSpawnExemptions = []seamExemption{
	{
		file: "model_sync.go", fn: "SyncUpstreamModels",
		reason: "two fetchJSON goroutines joined by wg.Wait() inside the same function (model_sync.go, `var wg sync.WaitGroup` / `wg.Add(2)` / `wg.Wait()`); neither can outlive the handler call",
	},
	{
		file: "model_sync.go", fn: "SyncUpstreamPreview",
		reason: "same wg.Add(2)/wg.Wait() fan-out as SyncUpstreamModels, joined before the handler returns",
	},
	{
		file: "playground.go", fn: "PlaygroundFanOut",
		reason: "per-model fan-out joined by wg.Wait() before the JSON response is written; results are read only after the join",
	},
	{
		file: "ratio_sync.go", fn: "FetchUpstreamRatios",
		reason: "per-upstream fan-out joined by wg.Wait() + close(ch) before the results channel is drained",
	},
	{
		file: "tool_version_worker.go", fn: "StartToolVersionWorker",
		reason: "lifecycle poller: an interval loop that exits on ctx.Done(), started once from cmd/server/main.go and by no test (grep StartToolVersionWorker over *.go finds that one call site plus its own declaration). Routing it through AsyncGo would make it block for the process lifetime under the inline seam this package's TestMain installs, and would move a panic in pollAndCache from a loud process crash to a gopool recover that silently stops version polling",
	},
	{
		file: "channel-test.go", fn: "testAllChannels",
		reason: "launch-not-completion semantics are asserted against the real function: TestChannelHealthTest_StampMeansLaunchedNotCompleted (context_tasks_integration_test.go) seeds 4 channels at 200ms RequestInterval and fails when testAllChannels takes more than 150ms to return. Under the inline seam this package's TestMain installs, AsyncGo here would make it take the full ~800ms pass and turn that test red. cycle-12 plan L5 lists this site for conversion; that finding is handed back instead",
	},
}

// TestNoUnseamedSpawnsInHandlerPackage fails when a non-test file in this
// package starts a goroutine outside the AsyncGo seam and outside the
// exemption list above.
func TestNoUnseamedSpawnsInHandlerPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	exemptByKey := map[string]seamExemption{}
	for _, e := range unseamedSpawnExemptions {
		exemptByKey[e.file+"::"+e.fn] = e
	}
	exemptHits := map[string]int{}

	fset := token.NewFileSet()
	var (
		filesScanned int
		sitesScanned int
		violations   []string
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

		// Walk each top-level func separately so a violation can be attributed
		// to the function that contains it. Spawns outside any FuncDecl (a
		// package-level var initialiser, say) are attributed to "".
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

		record := func(pos token.Pos, form string) {
			sitesScanned++
			fnName := enclosing(pos)
			key := name + "::" + fnName
			if _, ok := exemptByKey[key]; ok {
				exemptHits[key]++
				return
			}
			violations = append(violations, fmt.Sprintf(
				"%s: %s in %s() — route it through AsyncGo, or add it to unseamedSpawnExemptions with a reason",
				fset.Position(pos), form, fnName))
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				record(node.Pos(), "bare `go` statement")
			case *ast.CallExpr:
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "gopool" {
					return true
				}
				if sel.Sel.Name != "Go" && sel.Sel.Name != "CtxGo" {
					return true
				}
				record(node.Pos(), "gopool."+sel.Sel.Name+" call")
			}
			return true
		})
	}

	// Honesty guard 1: a scan that walked nothing must not report success.
	if filesScanned == 0 {
		t.Fatal("scanned 0 non-test .go files in this package — the scanner is broken (wrong CWD or a changed layout), not the package clean")
	}
	// Honesty guard 2: this package has had spawn sites since before this gate
	// existed. Finding none means the matcher stopped matching, not that they
	// were all removed.
	if sitesScanned == 0 {
		t.Fatalf("scanned %d files and found 0 goroutine spawn sites — the `go` statement / gopool matcher is broken; this package is known to contain both shapes", filesScanned)
	}

	// Honesty guard 3: an exemption that matches nothing is dead configuration
	// — the site was removed or renamed, and leaving the entry behind would
	// silently exempt whatever function later takes that name.
	var stale []string
	for key, e := range exemptByKey {
		if exemptHits[key] == 0 {
			stale = append(stale, fmt.Sprintf("%s (%s) — no unseamed spawn found there any more; delete this entry", key, e.reason))
		}
	}
	sort.Strings(stale)
	for _, s := range stale {
		t.Errorf("stale exemption: %s", s)
	}

	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("unseamed goroutine spawn: %s", v)
	}
	if len(violations) > 0 {
		t.Logf("scanned %d non-test files, %d spawn sites, %d exemptions", filesScanned, sitesScanned, len(unseamedSpawnExemptions))
	}
}
