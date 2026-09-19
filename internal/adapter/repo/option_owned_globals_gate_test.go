package repo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// option_owned_globals_gate_test.go — a structural gate: a setting that the
// option loader owns has exactly one writer.
//
// updateOptionMap (option.go) is the single dispatch that turns an options-row
// value into a process global. Every global it assigns is therefore
// "option-owned": its value is re-derived from the database on every
// SyncOptions tick. A second writer somewhere else does not merely race the
// tick — it loses to it, silently, up to SYNC_FREQUENCY seconds later, and the
// operator who set it sees the change apply and then evaporate.
//
// That is what setup.go did before this gate: it assigned
// operation_setting.SelfUseModeEnabled and .DemoSiteEnabled directly and then
// also wrote them through repo.UpdateOption on the next two statements, so the
// direct assignments were pure duplicate state.
//
// Scope of the gate, stated precisely so it is not read as more than it is:
// it derives the owned set from updateOptionMap's own assignments, then flags
// assignments to those globals written in the QUALIFIED form `pkg.Name = ...`
// in non-test .go files under internal/ and cmd/. A write from inside the
// package that declares the variable (where the spelling is the bare
// identifier) is not matched — that is where the compiled-in default and the
// env-var boot path legitimately live, and both are overwritten by the first
// loadOptionsFromDatabase anyway.
func TestOptionOwnedGlobalsHaveOneWriter(t *testing.T) {
	root := optionGateRepoRoot(t)

	owned := optionOwnedGlobals(t, filepath.Join(root, "internal", "adapter", "repo", "option.go"))
	// Fail fast rather than pass vacuously: if the derivation ever stops
	// matching updateOptionMap's shape it would return a tiny set (or none)
	// and this gate would report "no second writers" about nothing. The floor
	// is well under the count measured when the gate was written (see the
	// number in the failure message) so ordinary edits do not trip it.
	if len(owned) < 80 {
		t.Fatalf("derived only %d option-owned globals from updateOptionMap (%v); the derivation no longer matches the source", len(owned), owned)
	}
	t.Logf("derived %d option-owned globals from updateOptionMap", len(owned))

	allowed := map[string]bool{
		// The loader itself.
		filepath.Join("internal", "adapter", "repo", "option.go"): true,
	}

	var offenders []string
	scanRoots := []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")}
	for _, scanRoot := range scanRoots {
		err := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if allowed[rel] {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				// A file another lane is mid-edit must not be reported as a
				// finding of this gate.
				t.Logf("skipping unparseable %s: %v", rel, parseErr)
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				var targets []ast.Expr
				switch stmt := n.(type) {
				case *ast.AssignStmt:
					targets = stmt.Lhs
				case *ast.IncDecStmt:
					targets = []ast.Expr{stmt.X}
				default:
					return true
				}
				for _, target := range targets {
					name, ok := qualifiedGlobalName(target)
					if !ok || !owned[name] {
						continue
					}
					offenders = append(offenders, fmt.Sprintf("%s:%d writes %s", filepath.ToSlash(rel), fset.Position(target.Pos()).Line, name))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", scanRoot, err)
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("option-owned globals written outside updateOptionMap (the next SyncOptions tick overwrites them):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// optionGateRepoRoot walks up from this package directory to the module root.
func optionGateRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root above this package")
	return ""
}

// optionOwnedGlobals parses option.go and returns every `pkg.Name` that
// updateOptionMap assigns.
func optionOwnedGlobals(t *testing.T, optionGo string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, optionGo, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", optionGo, err)
	}

	owned := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "updateOptionMap" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, target := range assign.Lhs {
				if name, ok := qualifiedGlobalName(target); ok {
					owned[name] = true
				}
			}
			return true
		})
	}
	return owned
}

// qualifiedGlobalName reports the `pkg.Name` spelling of an assignment target
// when it is a package-qualified exported identifier, e.g. common.QuotaPerUnit.
// It deliberately does not match indexed or field-of-field targets
// (common.OptionMap[k], a.b.c): those are writes into a value, not a
// replacement of the option-owned variable itself.
func qualifiedGlobalName(target ast.Expr) (string, bool) {
	sel, ok := target.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	if !ast.IsExported(sel.Sel.Name) {
		return "", false
	}
	// A package identifier has no declaration object in this file's scope;
	// a local variable or a receiver does.
	if pkg.Obj != nil {
		return "", false
	}
	return pkg.Name + "." + sel.Sel.Name, true
}
