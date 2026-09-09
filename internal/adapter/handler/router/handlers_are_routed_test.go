package router

// handlers_are_routed_test.go — structural forcing function paired with
// v2_completeness_test.go: v2_completeness_test.go proves every ROUTED
// by-id/mutation endpoint is IDOR-classified; this test proves the inverse —
// every exported gin.Context handler DECLARED in the tenant admin files is
// referenced by a router registration. Without it, a handler like the old
// GetTenantConfigs/UpdateTenantConfig (deleted this cycle — never routed,
// dead since introduction) can be re-added by a future edit and sit
// unnoticed: it compiles, it is exported, and nothing calls it.
//
// It lives in the router package (not handler) for the same import-cycle
// reason as idor_completeness_test.go: it needs to parse the real
// route-table source files under router/, and a package-handler test
// importing router would cycle (router imports handler).
//
// The reference scan parses router/*.go with go/parser and collects
// *ast.SelectorExpr nodes whose X is the identifier "handler" — not a
// substring search — so a comment such as "// handler.Foo was removed"
// cannot satisfy the check.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// ginContextHandlerNames parses path and returns the names of every exported
// top-level func whose sole parameter is *gin.Context.
func ginContextHandlerNames(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var names []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() {
			continue
		}
		if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
			continue
		}
		param := fn.Type.Params.List[0]
		if len(param.Names) != 1 {
			continue
		}
		star, ok := param.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok || pkgIdent.Name != "gin" || sel.Sel.Name != "Context" {
			continue
		}
		names = append(names, fn.Name.Name)
	}
	return names
}

// routerHandlerSelectorReferences parses every non-test .go file directly
// under the router package directory (dir) with go/parser and returns the
// set of names N such that a "handler.N" selector expression (an
// *ast.SelectorExpr whose X is the identifier "handler") occurs anywhere in
// that file's AST — a real Go reference, not a textual match, so it cannot
// be satisfied by a comment or a string literal.
func routerHandlerSelectorReferences(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	refs := map[string]bool{}
	fset := token.NewFileSet()
	scannedFiles := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := dir + "/" + e.Name()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scannedFiles++
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || pkgIdent.Name != "handler" {
				return true
			}
			refs[sel.Sel.Name] = true
			return true
		})
	}
	if scannedFiles == 0 {
		t.Fatal("scanned zero non-test .go files under router/ — the reference scan itself is measuring nothing")
	}
	return refs
}

func TestHandlersAreRouted(t *testing.T) {
	sourceFiles := []string{
		"../tenant.go",
		"../tenant_model_limits.go",
		"../tenant_model_allowlist.go",
	}

	refs := routerHandlerSelectorReferences(t, ".")

	scanned := 0
	for _, sf := range sourceFiles {
		names := ginContextHandlerNames(t, sf)
		scanned += len(names)
		for _, name := range names {
			if !refs[name] {
				t.Errorf("%s: handler.%s is exported with a sole *gin.Context parameter but is never referenced as handler.%s in a non-test file under router/ — it looks routable but nothing routes it", sf, name, name)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned zero *gin.Context handler funcs — the scan itself is measuring nothing")
	}
}
