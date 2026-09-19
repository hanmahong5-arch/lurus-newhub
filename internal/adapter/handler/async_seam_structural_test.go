package handler

// async_seam_structural_test.go — cycle-12 L1's structural gate.
//
// WHAT IS ENFORCED, exactly: every goroutine spawn this scan can see in this
// package's non-test sources either goes through the AsyncGo seam
// (model_meta.go) or appears in unseamedSpawnExemptions with a reason AND the
// exact number of spawn sites that function is allowed to contain. A second
// spawn added inside an already-exempted function fails, because the count no
// longer matches — the exemption covers the sites that were judged, not the
// function's name forever.
//
// WHAT THE SCAN SEES: three spellings — a bare `go` statement; a call whose
// selector is `Go` or `CtxGo` on any receiver (`gopool.Go`, an
// `errgroup.Group`, an aliased import, a pool held in a struct field); and a
// bare `Go(…)` / `CtxGo(…)` call. The seam spellings (AsyncGo, repo.AsyncGo,
// app.AsyncGo, governance.AsyncGo, search.AsyncGo) are recognised first and are
// not spawn sites. TestUnseamedSpawnMatcherSeesEverySpelling pins that set
// against synthetic source: the first version of this gate matched only `go`
// statements and a receiver literally named `gopool`, so the three live
// errgroup spawns in uptime_kuma.go were invisible to it — and invisible to its
// own "found 0 sites" honesty guard, which an undercount satisfies just as well
// as a full count.
//
// WHAT THE SCAN DOES NOT SEE, so do not read this gate as more than it is: a
// goroutine started inside a function this package calls but does not declare
// (another package of this repo, or a library); a spawn behind an interface
// method; a start driven by reflect. It walks this directory's non-test .go
// files and nothing else. The other packages that carry an AsyncGo seam have
// no copy of this scan yet, and each still holds a live unseamed spawn today
// (2026-09-19): internal/adapter/repo/internal_api_key.go:95,
// internal/app/notify-limit.go:46 and :85, internal/app/release_service.go:207.
//
// WHY: without the seam a spawn has no join point, so it keeps reading package
// globals (repo.DB, common.RDB, common.RedisEnabled, releaseService …) after
// the test that caused it has restored or closed them. That is the shape behind
// three of the four red -race runs the cycle-12 plan §1.1/§1.2 records around
// 2026-09 — 09-09 an unjoined worker-pool submission in internal/pkg/search,
// 09-19 a polling goroutine left behind by a billing test, PR #188 a bare
// gopool.Go in relay.go still calling DisableChannel after a test's cleanup;
// the fourth, 09-08, has no cause recorded there. -race needs cgo and does not
// run on the dev host, so this scan is the part of it that can be enforced
// everywhere.

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

// seamCallNames are the spawn seams themselves, written as the scan renders a
// call's function expression. A call to one of these is the compliant spelling,
// not a violation.
//
// Today none of them collides with the matcher below (the matcher keys on a
// trailing `Go`/`CtxGo`, and every seam is spelled `AsyncGo`), so this set
// currently subtracts nothing — it is here so that aliasing a seam to a `.Go`
// spelling, or adding a seam type with a `Go` method, stays recognised instead
// of landing as a false violation. That is stated rather than implied because
// an allow-list nobody has ever hit is exactly the kind of thing that quietly
// stops being true.
var seamCallNames = map[string]bool{
	"AsyncGo":            true,
	"repo.AsyncGo":       true,
	"app.AsyncGo":        true,
	"governance.AsyncGo": true,
	"search.AsyncGo":     true,
}

// seamExemption names one function, in one file of this package, that is
// allowed to start goroutines without the AsyncGo seam: how many, and why.
//
// The key is file + enclosing function rather than a line number, because line
// numbers drift with every edit to the file and a drifted exemption silently
// stops matching. sites is what keeps the key honest: the exemption is for the
// spawn sites that were actually judged, so a new one inside the same function
// still has to be judged.
type seamExemption struct {
	file   string
	fn     string
	sites  int
	reason string
}

// unseamedSpawnExemptions is the whole exemption set. Two shapes qualify:
//
//   - the goroutine is joined before the function that started it returns
//     (sync.WaitGroup, errgroup, channel), so it cannot outlive a test; and
//   - the goroutine is a loop whose lifetime is a program lifetime, joined by
//     context cancellation, which the inline seam used by this package's
//     TestMain would turn into a blocking call.
//
// Anything else belongs in AsyncGo.
var unseamedSpawnExemptions = []seamExemption{
	{
		file: "model_sync.go", fn: "SyncUpstreamModels", sites: 2,
		reason: "two fetchJSON goroutines joined by wg.Wait() inside the same function (model_sync.go, `var wg sync.WaitGroup` / `wg.Add(2)` / `wg.Wait()`); neither can outlive the handler call",
	},
	{
		file: "model_sync.go", fn: "SyncUpstreamPreview", sites: 2,
		reason: "same wg.Add(2)/wg.Wait() fan-out as SyncUpstreamModels, joined before the handler returns",
	},
	{
		file: "playground.go", fn: "PlaygroundFanOut", sites: 1,
		reason: "per-model fan-out joined by wg.Wait() before the JSON response is written; results are read only after the join",
	},
	{
		file: "ratio_sync.go", fn: "FetchUpstreamRatios", sites: 1,
		reason: "per-upstream fan-out joined by wg.Wait() + close(ch) before the results channel is drained",
	},
	{
		file: "uptime_kuma.go", fn: "fetchGroupData", sites: 2,
		reason: "errgroup: the status and heartbeat fetches are joined by `if g.Wait() != nil` in the same function, before either decoded struct is read; both take their context from errgroup.WithContext(ctx)",
	},
	{
		file: "uptime_kuma.go", fn: "GetUptimeKumaStatus", sites: 1,
		reason: "errgroup: one goroutine per configured group, joined by g.Wait() before the results slice is serialised into the response",
	},
	{
		file: "tool_version_worker.go", fn: "StartToolVersionWorker", sites: 1,
		reason: "lifecycle poller: an interval loop that exits on ctx.Done(), started once from cmd/server/main.go and by no test (grep StartToolVersionWorker over *.go finds that one call site plus its own declaration). Routing it through AsyncGo would make it block for the process lifetime under the inline seam this package's TestMain installs, and would move a panic in pollAndCache from a loud process crash to a gopool recover that silently stops version polling",
	},
	{
		file: "channel-test.go", fn: "testAllChannels", sites: 1,
		reason: "launch-not-completion semantics are asserted against the real function: TestChannelHealthTest_StampMeansLaunchedNotCompleted (context_tasks_integration_test.go) seeds 4 channels at 200ms RequestInterval and fails when testAllChannels takes more than 150ms to return. Under the inline seam this package's TestMain installs, AsyncGo here would make it take the full ~800ms pass and turn that test red. The cycle-12 plan's L5 line listing this site for conversion was struck by the operator in the repair round; this exemption is the decision",
	},
}

// spawnSite is one goroutine start the scan recognised.
type spawnSite struct {
	file string
	fn   string
	pos  token.Position
	form string
}

func (s spawnSite) key() string { return s.file + "::" + s.fn }

// renderCallee renders a call's function expression the way seamCallNames is
// written: `Go`, `gopool.Go`, `g.Go`, `x.y.Go`. Anything it cannot render
// (a call on a call, an index expression) comes back as "" and is matched on
// the selector name alone.
func renderCallee(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if prefix := renderCallee(e.X); prefix != "" {
			return prefix + "." + e.Sel.Name
		}
		return e.Sel.Name
	}
	return ""
}

// collectSpawnSites walks one parsed file and returns every goroutine start the
// matcher recognises, attributed to the enclosing top-level function (a spawn
// outside any FuncDecl — a package-level var initialiser, say — is attributed
// to "").
func collectSpawnSites(fset *token.FileSet, name string, file *ast.File) []spawnSite {
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

	var sites []spawnSite
	record := func(pos token.Pos, form string) {
		sites = append(sites, spawnSite{
			file: name,
			fn:   enclosing(pos),
			pos:  fset.Position(pos),
			form: form,
		})
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
			callee := renderCallee(node.Fun)
			if seamCallNames[callee] {
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

// TestNoUnseamedSpawnsInHandlerPackage fails when a non-test file in this
// package starts a goroutine outside the AsyncGo seam and outside the
// exemption list above, and when an exempted function's spawn count has
// changed.
func TestNoUnseamedSpawnsInHandlerPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	exemptByKey := map[string]seamExemption{}
	for _, e := range unseamedSpawnExemptions {
		exemptByKey[e.file+"::"+e.fn] = e
	}

	fset := token.NewFileSet()
	var (
		filesScanned int
		all          []spawnSite
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
		all = append(all, collectSpawnSites(fset, name, file)...)
	}

	// Honesty guard 1: a scan that walked nothing must not report success.
	if filesScanned == 0 {
		t.Fatal("scanned 0 non-test .go files in this package — the scanner is broken (wrong CWD or a changed layout), not the package clean")
	}
	// Honesty guard 2: this package has had spawn sites since before this gate
	// existed. Finding none means the matcher stopped matching, not that they
	// were all removed. (An undercount still passes this one — the matcher's
	// own coverage is pinned by TestUnseamedSpawnMatcherSeesEverySpelling.)
	if len(all) == 0 {
		t.Fatalf("scanned %d files and found 0 goroutine spawn sites — the matcher is broken; this package is known to contain both the `go` statement and the pool-call shapes", filesScanned)
	}

	byKey := map[string][]spawnSite{}
	for _, s := range all {
		byKey[s.key()] = append(byKey[s.key()], s)
	}

	var violations []string
	for key, sites := range byKey {
		exemption, exempt := exemptByKey[key]
		if !exempt {
			for _, s := range sites {
				violations = append(violations, fmt.Sprintf(
					"%s: %s in %s() — route it through AsyncGo, or add it to unseamedSpawnExemptions with a reason",
					s.pos, s.form, s.fn))
			}
			continue
		}
		if len(sites) != exemption.sites {
			var where []string
			for _, s := range sites {
				where = append(where, fmt.Sprintf("%s (%s)", s.pos, s.form))
			}
			sort.Strings(where)
			violations = append(violations, fmt.Sprintf(
				"%s is exempted for %d spawn site(s) but now has %d: %s — the exemption covers the sites that were judged, not the function name. Route the new one through AsyncGo, or raise the count and extend the reason to cover it. Current reason: %s",
				key, exemption.sites, len(sites), strings.Join(where, ", "), exemption.reason))
		}
	}

	// Honesty guard 3: an exemption that matches nothing is dead configuration
	// — the site was removed or renamed, and leaving the entry behind would
	// silently exempt whatever function later takes that name.
	var stale []string
	for key, e := range exemptByKey {
		if len(byKey[key]) == 0 {
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
	if len(violations) > 0 || len(stale) > 0 {
		t.Logf("scanned %d non-test files, %d spawn sites, %d exemptions", filesScanned, len(all), len(unseamedSpawnExemptions))
	}
}

// TestUnseamedSpawnMatcherSeesEverySpelling is the gate's own oracle: the
// matcher is checked against synthetic source containing every spelling it
// claims to recognise, plus the ones it must stay silent about.
//
// This exists because the first version of the matcher was blind to
// errgroup's g.Go — and nothing failed: the package's other spawn shapes kept
// the "0 sites found" guard satisfied, so an unjoined errgroup goroutine could
// be added to any handler and land green. A count of sites found in real
// sources cannot catch that; only a fixture with a known answer can.
func TestUnseamedSpawnMatcherSeesEverySpelling(t *testing.T) {
	const synthetic = `package p

import (
	"golang.org/x/sync/errgroup"

	"github.com/bytedance/gopkg/util/gopool"
)

func wantBareGo()      { go func() {}() }
func wantGopoolGo()    { gopool.Go(func() {}) }
func wantGopoolCtxGo() { gopool.CtxGo(nil, func() {}) }
func wantErrgroupGo() {
	g := new(errgroup.Group)
	g.Go(func() error { return nil })
}
func wantFieldPoolGo(s struct{ pool *gopool.Pool }) { s.pool.Go(func() {}) }
func wantAliasedPoolGo(worker *gopool.Pool)         { worker.CtxGo(nil, func() {}) }

func seamAsyncGo()           { AsyncGo(func() {}) }
func seamRepoAsyncGo()       { repo.AsyncGo(func() {}) }
func seamGovernanceAsyncGo() { governance.AsyncGo(func() {}) }
func notASpawn()             { fmt.Println(strings.Join(nil, "")) }
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", synthetic, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}

	got := map[string]int{}
	for _, s := range collectSpawnSites(fset, "synthetic.go", file) {
		got[s.fn]++
	}

	want := map[string]int{
		"wantBareGo":        1,
		"wantGopoolGo":      1,
		"wantGopoolCtxGo":   1,
		"wantErrgroupGo":    1,
		"wantFieldPoolGo":   1,
		"wantAliasedPoolGo": 1,
	}
	for fn, n := range want {
		if got[fn] != n {
			t.Errorf("matcher saw %d spawn site(s) in %s(), want %d — that spelling is invisible to the gate", got[fn], fn, n)
		}
	}
	for _, fn := range []string{"seamAsyncGo", "seamRepoAsyncGo", "seamGovernanceAsyncGo", "notASpawn"} {
		if got[fn] != 0 {
			t.Errorf("matcher reported %d spawn site(s) in %s(), want 0 — a compliant call is being flagged", got[fn], fn)
		}
	}
}
