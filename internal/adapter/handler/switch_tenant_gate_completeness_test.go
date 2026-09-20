package handler

// switch_tenant_gate_completeness_test.go — cycle-13 L9's forcing function for
// the raw-token half of "the tenant in the request has to reach the data".
//
// WHAT IS ENFORCED: every handler registered on the /api/v2/switch group in
// router/api-v2-router.go either reaches repo.TenantGate (directly, or through
// a function declared in this package — which is how the shared raw-token
// helper authenticateSwitchRawTokenWithCode covers user/info and user/topup),
// or appears in switchTenantGateExemptions naming the check it performs
// instead. A new /api/v2/switch route added without either fails here.
//
// WHY THIS GROUP NEEDS ITS OWN GATE: these routes carry no auth middleware at
// all — each handler does an inline Token.Key lookup — so neither
// middleware.UserAuth's tenant arm nor middleware.TenantSlugGuard (which the
// router-package completeness test walks for /api/v2/:tenant_slug/…) can see
// them. Before cycle-13 L9 a suspended tenant's key kept answering on every
// one of them, and the only thing that would have caught the next such route
// is the hand-written table in tenant_reaches_data_c13_test.go.
//
// WHAT THE SCAN SEES: the registration lines inside the `switchGroup` block of
// router/api-v2-router.go, read as TEXT (this package cannot import the router
// package — the router imports this one), and the call graph of this package's
// non-test sources, following calls to functions declared in this package. It
// does not follow calls into other packages, so a gate reached only through,
// say, an app/ helper would read as unguarded here; write the exemption with
// that reason if it ever happens.
//
// WHAT IT DOES NOT SEE: a switch route registered somewhere other than that
// block (guarded below by asserting the group is created exactly once and that
// the block opens no sub-group), a gate behind an interface method, and the
// runtime middleware chain (the router package's own tests own that half).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// switchTenantGateSink is the decision every raw-token Switch surface has to
// reach, rendered the way renderCallee (async_seam_structural_test.go) writes
// a call's function expression.
const switchTenantGateSink = "repo.TenantGate"

// switchRouterFile is read as text, not imported: package router imports
// package handler, so the dependency cannot go the other way.
const switchRouterFile = "router/api-v2-router.go"

// switchTenantGateExemptions maps a handler name to the reason it does not
// reach repo.TenantGate. A reason is a description of the check that stands in
// for it — "this route is public" is a reason; an empty entry is a hole.
var switchTenantGateExemptions = map[string]string{
	"GetToolVersions": "public, unauthenticated: serves the operator-published CLI/tool version map from options. " +
		"It reads no per-tenant row and returns nothing a tenant owns.",
	"ListSwitchPresets": "public, unauthenticated: the admin-published relay preset catalogue (options-driven), " +
		"identical for every caller.",
	"GetSwitchPricing": "public, unauthenticated: the rate card. Same numbers the pricing page shows anonymously.",
	"GetSwitchAppRelease": "public, unauthenticated: the desktop self-update manifest (options-driven; 404 when " +
		"unpublished). No caller identity exists to gate on.",
	"SwitchRedeemAnonymous": "anonymous activation-code redemption — there is no token or session yet, so the " +
		"tenant comes from the REDEMPTION row. It takes the equivalent decision inline: switch_redeem.go resolves " +
		"that tenant and refuses a disabled one with the suspended-reseller sentence before crediting anything " +
		"(switch_redeem.go, the arm right after GetTenantByID; oracle: the suspended/redeem_anonymous cell of " +
		"TestSuspendedTenantCannotReachSwitchSurfaces). Routing it through repo.TenantGate would also " +
		"exempt the bootstrap \"default\" tenant, which this path deliberately refuses instead.",
}

// switchRouteRegistration matches `switchGroup.POST("/user/topup", mw(), handler.SwitchUserTopup)`.
var switchRouteRegistration = regexp.MustCompile(
	`switchGroup\.(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\(\s*"([^"]*)"\s*,([^\n]*)\)`)

// switchHandlerRef pulls the LAST handler.Xxx reference out of a registration
// line — gin's final argument is the handler, anything before it is middleware.
var switchHandlerRef = regexp.MustCompile(`handler\.([A-Za-z0-9_]+)`)

type switchRoute struct {
	method  string
	path    string
	handler string
}

// switchGroupBlock returns the BODY of the `switchGroup := apiV2.Group("/switch")`
// block: the lines after the one that creates the group, up to the closing
// brace at the same indentation. The creation line itself is left out so the
// sub-group check below is not tripped by it.
func switchGroupBlock(t *testing.T, src string) string {
	t.Helper()
	lines := strings.Split(src, "\n")
	start := -1
	indent := ""
	for i, line := range lines {
		if strings.Contains(line, `apiV2.Group("/switch")`) {
			if start >= 0 {
				t.Fatalf("%s creates the /switch group more than once (line %d and %d) — this gate "+
					"parses one block, so extend it before splitting the group", switchRouterFile, start+1, i+1)
			}
			start = i
			indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		}
	}
	if start < 0 {
		t.Fatalf(`%s no longer contains apiV2.Group("/switch") — the group was renamed or moved; `+
			`this gate walked nothing, which is not the same as a clean result`, switchRouterFile)
	}
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == indent+"}" {
			return strings.Join(lines[start+1:i+1], "\n")
		}
	}
	t.Fatalf("could not find the end of the /switch group block in %s", switchRouterFile)
	return ""
}

// switchRoutes parses the group block into its registrations.
func switchRoutes(t *testing.T, block string) []switchRoute {
	t.Helper()
	if strings.Contains(block, ".Group(") {
		t.Fatalf("the /switch block now opens a sub-group; routes registered on it are invisible to this " +
			"gate — extend switchRoutes before landing that")
	}
	var out []switchRoute
	for _, m := range switchRouteRegistration.FindAllStringSubmatch(block, -1) {
		refs := switchHandlerRef.FindAllStringSubmatch(m[3], -1)
		if len(refs) == 0 {
			t.Errorf("registration %s %s names no handler.Xxx — this gate cannot tell what it runs", m[1], m[2])
			continue
		}
		out = append(out, switchRoute{
			method:  m[1],
			path:    m[2],
			handler: refs[len(refs)-1][1],
		})
	}
	return out
}

// switchPackageCallGraph maps each top-level function declared in this
// package's non-test sources to the calls its body makes, rendered by
// renderCallee. Methods are keyed by their bare name too, which only widens
// what a search can follow.
func switchPackageCallGraph(t *testing.T) map[string]map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	graph := map[string]map[string]bool{}
	fset := token.NewFileSet()
	scanned := 0
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
		scanned++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			calls := graph[fn.Name.Name]
			if calls == nil {
				calls = map[string]bool{}
				graph[fn.Name.Name] = calls
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if rendered := renderCallee(call.Fun); rendered != "" {
					calls[rendered] = true
				}
				return true
			})
		}
	}
	if scanned == 0 {
		t.Fatal("scanned 0 non-test .go files in this package — wrong working directory or a changed layout, " +
			"not a clean package")
	}
	return graph
}

// reachesSwitchTenantGate reports whether fn reaches switchTenantGateSink
// through calls to functions declared in this package, and the path it took.
func reachesSwitchTenantGate(graph map[string]map[string]bool, fn string) (bool, []string) {
	type frame struct {
		name string
		path []string
	}
	seen := map[string]bool{fn: true}
	queue := []frame{{name: fn, path: []string{fn}}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		calls := graph[cur.name]
		if calls[switchTenantGateSink] {
			return true, append(cur.path, switchTenantGateSink)
		}
		names := make([]string, 0, len(calls))
		for callee := range calls {
			names = append(names, callee)
		}
		sort.Strings(names)
		for _, callee := range names {
			// Only package-local functions are followed: a rendered call with
			// a dot is another package (or a method on a value), and this scan
			// does not resolve those.
			if strings.Contains(callee, ".") || seen[callee] {
				continue
			}
			if _, declared := graph[callee]; !declared {
				continue
			}
			seen[callee] = true
			queue = append(queue, frame{name: callee, path: append(append([]string{}, cur.path...), callee)})
		}
	}
	return false, nil
}

// TestSwitchRoutesReachTheTenantGate is the gate itself.
func TestSwitchRoutesReachTheTenantGate(t *testing.T) {
	src, err := os.ReadFile(switchRouterFile)
	if err != nil {
		t.Fatalf("read %s: %v", switchRouterFile, err)
	}
	routes := switchRoutes(t, switchGroupBlock(t, string(src)))

	// Fail fast rather than pass vacuously: 9 routes were registered on
	// 2026-09-20, and a parse that suddenly sees a handful means the regex
	// stopped matching, not that the surface shrank.
	if len(routes) < 8 {
		t.Fatalf("parsed only %d /api/v2/switch registrations from %s — the scan is broken, so a green "+
			"result here would prove nothing", len(routes), switchRouterFile)
	}

	graph := switchPackageCallGraph(t)
	if _, declared := graph["UserHeartbeat"]; !declared {
		t.Fatal("the call graph has no UserHeartbeat — it is declared in this package, so the graph is not " +
			"the package's")
	}

	registered := map[string]bool{}
	for _, rt := range routes {
		registered[rt.handler] = true
		guarded, path := reachesSwitchTenantGate(graph, rt.handler)
		reason, exempted := switchTenantGateExemptions[rt.handler]
		switch {
		case guarded && exempted:
			t.Errorf("%s %s (%s) reaches %s via %s and is ALSO exempted (%q) — delete the exemption",
				rt.method, "/api/v2/switch"+rt.path, rt.handler, switchTenantGateSink,
				strings.Join(path, " -> "), reason)
		case exempted && strings.TrimSpace(reason) == "":
			t.Errorf("%s %s (%s) is exempted with an empty reason, which is a hole, not an exemption",
				rt.method, "/api/v2/switch"+rt.path, rt.handler)
		case !guarded && !exempted:
			t.Errorf("%s %s (%s) never reaches %s and is not in switchTenantGateExemptions: call the gate "+
				"on the token's tenant (see GetSwitchUserInfo / UserHeartbeat), or add an entry naming the "+
				"tenant check it does instead",
				rt.method, "/api/v2/switch"+rt.path, rt.handler, switchTenantGateSink)
		}
	}

	// A stale exemption would silently cover a handler that was renamed or
	// unmounted, so it is as much of a defect as a missing gate.
	names := make([]string, 0, len(switchTenantGateExemptions))
	for name := range switchTenantGateExemptions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !registered[name] {
			t.Errorf("switchTenantGateExemptions lists %s, which is not registered on /api/v2/switch any "+
				"more — remove the stale entry", name)
		}
	}
}

// TestSwitchTenantGateScanFollowsTheSharedHelper pins the part of the scan
// that is easy to get wrong: the two handlers that reach the gate only through
// authenticateSwitchRawTokenWithCode must be seen as guarded. A scan that only
// looked at a handler's own body would report them unguarded (a false alarm
// this gate would then be softened to silence), and one that followed calls
// into other packages could report anything as guarded.
func TestSwitchTenantGateScanFollowsTheSharedHelper(t *testing.T) {
	graph := switchPackageCallGraph(t)

	for _, fn := range []string{"GetSwitchUserInfo", "SwitchUserTopup"} {
		guarded, path := reachesSwitchTenantGate(graph, fn)
		if !guarded {
			t.Errorf("%s reads as unguarded — it reaches %s through authenticateSwitchRawTokenWithCode, so "+
				"the scan lost the indirect hop", fn, switchTenantGateSink)
			continue
		}
		if len(path) < 3 {
			t.Errorf("%s reads as guarded via %v — expected the indirect path through the shared helper",
				fn, path)
		}
	}

	// The negative half: a public handler must NOT read as guarded, or the
	// scan is finding a path that does not exist and every exemption above
	// would start failing as "reaches the gate AND is exempted".
	if guarded, path := reachesSwitchTenantGate(graph, "GetSwitchPricing"); guarded {
		t.Errorf("GetSwitchPricing reads as reaching %s via %s — either the rate card really does gate on "+
			"the tenant now (update the exemption) or the scan is over-reaching",
			switchTenantGateSink, strings.Join(path, " -> "))
	}
}
