package router

// audit_coverage_test.go — L2 audit-completeness CI structural gate.
//
// TestAdminWriteRoutesAreAudited enumerates the real admin/internal-admin
// write route table (handler.IsAdminWriteRoute over SetApiV2Router +
// SetInternalApiRouter's engine.Routes(), same predicate the production
// router-setup capture uses — see audit_coverage_gen.go), resolves each
// route's handler function, and parses its declaring file with go/ast
// looking for a governance.NewAuditEvent / governance.RecordAuditEvent call
// — directly, or one level of same-package delegation (e.g.
// UpdateAdminOptionV2 → UpdateOption). A route with neither must be listed
// in knownFallbackRoutes with a reason; any other unaudited route fails the
// test naming the route and handler. knownFallbackRoutes may not contain a
// route listed in moneyRouteDenyList (asserted below) — those specific gaps
// must be closed with a real audit call, not documented away; the AST walk
// itself has a known blind spot (see the note beside knownFallbackRoutes).
//
// It also cross-checks handler.AuditExplicitRoutes (the static map the
// production coverage endpoint reads, since a deployed binary cannot parse
// its own source at request time) against this test's live classification,
// so the generated map cannot silently drift from what's actually audited.
//
// Lives in the router package (not handler) for the same import-cycle
// reason as idor_completeness_test.go / v2_completeness_test.go: it needs
// the real route table via SetApiV2Router/SetInternalApiRouter, and a
// package-handler test importing router would cycle (router imports
// handler).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// knownFallbackRoutes lists admin/internal-admin write routes that
// intentionally rely on middleware.AuditWriteGuard's fallback instead of an
// explicit audit call, with the reason. This is a merge-review artefact:
// it must shrink, never grow silently — a reviewer sees every addition in
// the PR diff. It is intentionally empty right now: every admin write route
// this lane could find gained (or already had) an explicit audit call.
//
// AST-walk limitation (not closeable by CI alone): bodyCallsGovernanceAudit
// checks for the *presence* of a governance call anywhere in the handler's
// (or one delegate's) body — it cannot tell a call reached on every branch
// from one reached on only some. A branch that forgets to call
// RecordAuditEvent — e.g. one outcome of a role-change handler — reads as
// "audited" here even though that specific request path is not. Runtime
// coverage for that gap is AuditWriteGuard itself (it checks the flag on
// the actual request, not the source); this test proves only "the handler
// somewhere calls governance.NewAuditEvent/RecordAuditEvent", not "every
// branch does".
var knownFallbackRoutes = map[string]string{}

// moneyRouteDenyList is a small, explicit list of routes that move or gate a
// real wallet-backed balance. knownFallbackRoutes must never contain one of
// these — a money route relying on the untyped fallback is exactly the gap
// this lane closes, not documents.
var moneyRouteDenyList = map[string]bool{
	"POST /api/v2/admin/tenants/:id/credit-pool":       true,
	"POST /api/v2/admin/tenants/:id/credit-pool/topup": true,
	"DELETE /api/v2/admin/tenants/:id/credit-pool":     true,
}

// catalogAuditRequiredRoutes is moneyRouteDenyList's sibling for the two
// root-gated, process-global catalog writes outside /admin
// (rootGatedWritesOutsideAdmin in audit_coverage_gen.go): they move no
// wallet balance, but the model catalog and its pricing apply regardless of
// tenant, so an unaudited write here is — per audit_action.go's own
// ActionModelCreated doc — "as consequential as one under /admin". Same
// invariant as moneyRouteDenyList: a route listed here must never be parked
// in knownFallbackRoutes.
var catalogAuditRequiredRoutes = map[string]bool{
	"POST /api/v2/:tenant_slug/models":       true,
	"DELETE /api/v2/:tenant_slug/models/:id": true,
}

// handlerFuncDecls parses every non-test .go file directly under dir and
// returns a map of exported-or-not top-level func name -> its *ast.FuncDecl.
func handlerFuncDecls(t *testing.T, dir string) map[string]*ast.FuncDecl {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.FuncDecl{}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			out[fn.Name.Name] = fn
		}
	}
	if scanned == 0 {
		t.Fatal("scanned zero non-test .go files under the handler package — the scan itself is measuring nothing")
	}
	return out
}

// bodyCallsGovernanceAudit reports whether fn's body directly contains a
// governance.NewAuditEvent or governance.RecordAuditEvent selector call.
func bodyCallsGovernanceAudit(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok || pkgIdent.Name != "governance" {
			return true
		}
		if sel.Sel.Name == "NewAuditEvent" || sel.Sel.Name == "RecordAuditEvent" {
			found = true
			return false
		}
		return true
	})
	return found
}

// samePackageCallees returns the set of bare-identifier function calls made
// directly in fn's body (e.g. `UpdateOption(c)`) — "one level of same-package
// function calls" per the L2 spec.
func samePackageCallees(fn *ast.FuncDecl) []string {
	var out []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok {
			out = append(out, ident.Name)
		}
		return true
	})
	return out
}

// handlerAudits reports whether name's function body — or one level of
// same-package delegation from it — contains a governance audit call.
func handlerAudits(name string, decls map[string]*ast.FuncDecl) bool {
	fn, ok := decls[name]
	if !ok {
		return false
	}
	if bodyCallsGovernanceAudit(fn) {
		return true
	}
	for _, callee := range samePackageCallees(fn) {
		if calleeFn, ok := decls[callee]; ok && bodyCallsGovernanceAudit(calleeFn) {
			return true
		}
	}
	return false
}

// handlerShortName extracts the function's short name from gin's
// fully-qualified RouteInfo.Handler string (e.g.
// ".../internal/adapter/handler.CreateCreditPool" -> "CreateCreditPool").
func handlerShortName(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

func TestAdminWriteRoutesAreAudited(t *testing.T) {
	// moneyRouteDenyList sanity: it must never overlap knownFallbackRoutes —
	// the whole point of this test is that a money route MUST have a real
	// audit call, not a documented fallback.
	for money := range moneyRouteDenyList {
		if reason, ok := knownFallbackRoutes[money]; ok {
			t.Errorf("money route %q is in knownFallbackRoutes (reason: %q) — money routes must have a real governance.RecordAuditEvent call, not a documented fallback", money, reason)
		}
	}

	for catalog := range catalogAuditRequiredRoutes {
		if reason, ok := knownFallbackRoutes[catalog]; ok {
			t.Errorf("catalog route %q is in knownFallbackRoutes (reason: %q) — process-global catalog writes must have a real governance.RecordAuditEvent call, not a documented fallback", catalog, reason)
		}
	}

	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)
	SetInternalApiRouter(engine)

	decls := handlerFuncDecls(t, "../")

	scanned := 0
	var scannedRoutes []string
	for _, rt := range engine.Routes() {
		if !handler.IsAdminWriteRoute(rt.Method, rt.Path) {
			continue
		}
		scanned++
		key := rt.Method + " " + rt.Path
		scannedRoutes = append(scannedRoutes, key)
		name := handlerShortName(rt.Handler)
		audited := handlerAudits(name, decls)

		if reason, ok := knownFallbackRoutes[key]; ok {
			if audited {
				t.Errorf("%s (handler.%s) is listed in knownFallbackRoutes (reason: %q) but the live AST walk now finds a governance audit call — shrink knownFallbackRoutes instead of leaving a stale entry", key, name, reason)
			}
			continue
		}
		if !audited {
			t.Errorf("admin write route %s (handler.%s) has no governance.NewAuditEvent/RecordAuditEvent call (directly, or via one level of same-package delegation) and is not in knownFallbackRoutes — every admin write must be provably audited", key, name)
			continue
		}

		// Cross-check the production coverage endpoint's static map against
		// this live classification, so it cannot silently go stale.
		if !handler.AuditExplicitRoutes[key] {
			t.Errorf("%s (handler.%s) is audited per the live AST walk but missing from handler.AuditExplicitRoutes — the coverage endpoint would wrongly report it as fallback-covered; add it to audit_coverage_gen.go", key, name)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned zero admin/internal-admin write routes — the scan itself is measuring nothing")
	}

	// Lock for mutation 3 (delete the captureAdminWriteRoutes(router) call,
	// or its handler.SetAdminWriteRoutes(routes) line, from
	// router/internal-api-router.go's SetInternalApiRouter): without either,
	// GetAdminWriteRoutes() answers a stale or empty list forever, and a
	// deployed binary's GET /api/v2/admin/audit/coverage would silently
	// report total_admin_write_routes=0 (or a wrong number) while every
	// assertion above — which reads engine.Routes() directly, not the
	// captured snapshot — still passes.
	gotRoutes := handler.GetAdminWriteRoutes()
	if len(gotRoutes) != scanned {
		t.Errorf("handler.GetAdminWriteRoutes() captured %d routes, want %d (this test's own live scan) — production route capture has drifted from IsAdminWriteRoute's scan", len(gotRoutes), scanned)
	}
	gotSet := make(map[string]bool, len(gotRoutes))
	for _, r := range gotRoutes {
		gotSet[r] = true
	}
	for _, key := range scannedRoutes {
		if !gotSet[key] {
			t.Errorf("handler.GetAdminWriteRoutes() is missing %q — captureAdminWriteRoutes did not pick up a route the live scan finds", key)
		}
	}

	// The reverse direction: every route AuditExplicitRoutes claims explicit
	// must actually be a currently-registered, currently-audited route —
	// catches a stale positive entry left behind by a later removal/rename.
	for key, explicit := range handler.AuditExplicitRoutes {
		if !explicit {
			continue
		}
		parts := strings.SplitN(key, " ", 2)
		if len(parts) == 2 && !handler.IsAdminWriteRoute(parts[0], parts[1]) {
			t.Errorf("handler.AuditExplicitRoutes lists %q but handler.IsAdminWriteRoute excludes it from scope — the coverage endpoint's GetAdminWriteRoutes() capture never includes this route, so GET /api/v2/admin/audit/coverage will never report it even though the map says it is explicitly audited", key)
		}
		found := false
		for _, rt := range engine.Routes() {
			if rt.Method+" "+rt.Path == key {
				found = true
				if !handlerAudits(handlerShortName(rt.Handler), decls) {
					t.Errorf("handler.AuditExplicitRoutes claims %q is explicitly audited but the live AST walk disagrees for handler.%s — the map is stale", key, handlerShortName(rt.Handler))
				}
				break
			}
		}
		if !found {
			t.Errorf("handler.AuditExplicitRoutes has a stale entry %q — no such route is currently registered", key)
		}
	}
}
