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
//
//   - The owned set is derived from updateOptionMap's own assignments, so it
//     covers the flat option keys. The hierarchical keys — everything the
//     config manager's reflect writer owns — are NOT in it; they are fields of
//     registered structs rather than package variables, and their second-writer
//     risk is covered instead by TestRegisteredPointerFieldsDecodeInPlace
//     (internal/app/group_ratio_race_test.go) and by the configuration lock
//     gate (internal/pkg/setting/config/config_cow_test.go).
//   - It flags both spellings of a write: the qualified `pkg.Name = ...` from
//     any package, and the bare `Name = ...` from inside the package that
//     declares the variable. The bare case needs the allow-list below, because
//     the compiled-in default and the env-var boot path legitimately live
//     there.
//   - A write THROUGH an owned value rather than TO it (common.OptionMap[k] =,
//     a method call that mutates) is not an assignment to the variable and is
//     not matched.
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

	// The bare-identifier allow-list: `<file>:<function>` sites inside the
	// declaring package that this gate does not (yet) get to remove. The
	// compiled defaults and the boot-time environment reads do not need an
	// entry — they are top-level `var x = ...` declarations and `init` bodies
	// that the walk below does not treat as assignments to begin with — so
	// everything listed here is a real run-time second writer with a reason it
	// is still there.
	allowedBareWriters := map[string]string{
		// SendEmail backfills SMTPFrom from SMTPAccount when the operator left
		// "From" empty, and does it by writing the global rather than a local.
		// It races the option-sync tick (which restores the stored "" every
		// SYNC_FREQUENCY seconds) and the other SendEmail goroutines. The fix
		// is a local variable, but the current behaviour is pinned by
		// internal/pkg/common/cov_r5core_boot_email_test.go:86 ("want it
		// backfilled from SMTPAccount"), so changing it is a behaviour decision
		// in another package's test, not a cleanup: it is recorded as an owner
		// item rather than silently altered here.
		"internal/pkg/common/email.go:SendEmail": "known second writer of common.SMTPFrom; the backfill contract is pinned by that package's own test — owner item",
	}

	var offenders []string
	packageVars := map[string]map[string]bool{} // package dir -> top-level var names
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

			// Bare identifiers only mean anything inside the package that
			// declares the variable, and only for names that really are
			// package-level variables there.
			pkgName := file.Name.Name
			dir := filepath.Dir(path)
			if _, seen := packageVars[dir]; !seen {
				packageVars[dir] = packageLevelVars(t, dir)
			}
			declared := packageVars[dir]

			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					var targets []ast.Expr
					switch stmt := n.(type) {
					case *ast.AssignStmt:
						if stmt.Tok == token.DEFINE {
							return true // `:=` declares a new local
						}
						targets = stmt.Lhs
					case *ast.IncDecStmt:
						targets = []ast.Expr{stmt.X}
					default:
						return true
					}
					for _, target := range targets {
						if name, ok := qualifiedGlobalName(target); ok && owned[name] {
							offenders = append(offenders, fmt.Sprintf("%s:%d writes %s", filepath.ToSlash(rel), fset.Position(target.Pos()).Line, name))
							continue
						}
						ident, ok := target.(*ast.Ident)
						if !ok || !declared[ident.Name] || !owned[pkgName+"."+ident.Name] {
							continue
						}
						site := filepath.ToSlash(rel) + ":" + fn.Name.Name
						if _, ok := allowedBareWriters[site]; ok {
							continue
						}
						offenders = append(offenders, fmt.Sprintf("%s:%d writes %s (bare identifier, inside the declaring package)",
							filepath.ToSlash(rel), fset.Position(target.Pos()).Line, pkgName+"."+ident.Name))
					}
					return true
				})
			}
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

// packageLevelVars returns the names declared by top-level `var` blocks in the
// non-test files of one package directory. A bare identifier that is not in
// this set cannot be a write to an option-owned global.
func packageLevelVars(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if parseErr != nil {
			continue
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, ident := range value.Names {
					names[ident.Name] = true
				}
			}
		}
	}
	return names
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
