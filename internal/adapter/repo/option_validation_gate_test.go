package repo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// option_validation_gate_test.go — structural gates over option.go's parsing
// and its validate-before-persist tables. They are in a file of their own
// rather than in option_owned_globals_gate_test.go because they answer a
// different question: that one asks "who writes an option-owned global", these
// ask "is every value that reaches the dispatch parsed safely and checked
// before the row is written".

// TestOptionParseErrorsAreNotDiscarded — the structural half of
// option_parse_keep_previous_test.go.
//
// Three keys are pinned there with a behavioural assertion; the other twenty
// are pinned by nothing, and re-introducing `x, _ = strconv.Atoi(value)` on any
// of them is a one-line edit no test would notice. That statement IS the
// defect: on a parse failure it stores the zero value, so one blank Settings
// field zeroes QuotaPerUnit (every currency number in the console reads 0) or
// ChannelDisableThreshold (the auto-disable threshold turns off), on every
// replica, silently.
//
// The rule: inside option.go no assignment may take a strconv result and throw
// the error away. optionInt and optionFloat are where that error is handled.
func TestOptionParseErrorsAreNotDiscarded(t *testing.T) {
	root := optionGateRepoRoot(t)
	optionGo := filepath.Join(root, "internal", "adapter", "repo", "option.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, optionGo, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", optionGo, err)
	}

	var offenders []string
	strconvSeen := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		funcName := fn.Name.Name
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || !assignmentCallsStrconv(assign) {
				return true
			}
			strconvSeen++
			// strconv's parsers return (value, error), so the error is the
			// LAST target. `_, err = strconv.Atoi(v)` throws the value away,
			// which is fine; `x, _ = strconv.Atoi(v)` throws the error away,
			// which is the defect.
			last, ok := assign.Lhs[len(assign.Lhs)-1].(*ast.Ident)
			if ok && last.Name == "_" && len(assign.Lhs) > 1 {
				offenders = append(offenders, fmt.Sprintf(
					"internal/adapter/repo/option.go:%d (in %s) discards a strconv error",
					fset.Position(assign.Pos()).Line, funcName))
			}
			return true
		})
	}

	// Fail fast rather than pass vacuously: if option.go ever stops calling
	// strconv from an assignment at all, this gate is watching nothing.
	if strconvSeen == 0 {
		t.Fatal("no strconv assignment found in option.go at all; this gate is no longer looking at the right thing")
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("a parse error is discarded, so a malformed value stores the zero value instead of keeping the previous one (use optionInt/optionFloat):\n  %s",
			strings.Join(offenders, "\n  "))
	}
	t.Logf("%d strconv assignments in option.go, none discarding the error", strconvSeen)
}

// assignmentCallsStrconv reports whether any right-hand side of assign calls
// into the strconv package.
func assignmentCallsStrconv(assign *ast.AssignStmt) bool {
	found := false
	for _, rhs := range assign.Rhs {
		ast.Inspect(rhs, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "strconv" && pkg.Obj == nil {
				found = true
			}
			return true
		})
	}
	return found
}

// TestOptionValidationTablesCoverTheDispatch — validate-before-persist is only
// worth something if the tables it validates against are complete.
//
// repo.UpdateOption refuses a value before it writes the options row, using
// numericOptionKinds and jsonOptionKinds. A key that reaches the dispatch but is
// missing from those tables is accepted into the table, refused afterwards, and
// refused again on every SyncOptions tick forever — the permanent
// row/running-value divergence the change exists to remove. So the tables are
// derived here from the dispatch itself and compared against the declared ones.
func TestOptionValidationTablesCoverTheDispatch(t *testing.T) {
	root := optionGateRepoRoot(t)
	optionGo := filepath.Join(root, "internal", "adapter", "repo", "option.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, optionGo, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", optionGo, err)
	}

	derivedNumeric := map[string]optionValueKind{}
	derivedJSON := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "updateOptionMap" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			var keys []string
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				keys = append(keys, strings.Trim(lit.Value, string('"')))
			}
			if len(keys) == 0 {
				return true
			}
			for _, stmt := range clause.Body {
				ast.Inspect(stmt, func(inner ast.Node) bool {
					call, ok := inner.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch fun := call.Fun.(type) {
					case *ast.Ident:
						for _, key := range keys {
							switch fun.Name {
							case "optionInt":
								derivedNumeric[key] = optionKindInteger
							case "optionFloat":
								derivedNumeric[key] = optionKindNumber
							}
						}
					case *ast.SelectorExpr:
						if strings.HasSuffix(fun.Sel.Name, "ByJSONString") || strings.HasSuffix(fun.Sel.Name, "ByJsonString") {
							for _, key := range keys {
								derivedJSON[key] = true
							}
						}
					}
					return true
				})
			}
			return true
		})
	}

	if len(derivedNumeric) < 20 || len(derivedJSON) < 10 {
		t.Fatalf("derived only %d numeric and %d JSON keys from updateOptionMap; the derivation no longer matches the source",
			len(derivedNumeric), len(derivedJSON))
	}

	var problems []string
	for key, kind := range derivedNumeric {
		got, ok := numericOptionKinds[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s is parsed as a %s by updateOptionMap but is missing from numericOptionKinds, so UpdateOption persists a malformed value and only then refuses it", key, kind))
			continue
		}
		if got != kind {
			problems = append(problems, fmt.Sprintf("%s is parsed as a %s but numericOptionKinds says %s", key, kind, got))
		}
	}
	for key := range numericOptionKinds {
		if _, ok := derivedNumeric[key]; !ok {
			problems = append(problems, fmt.Sprintf("numericOptionKinds has %s, which updateOptionMap no longer parses numerically", key))
		}
	}
	for key := range derivedJSON {
		if _, ok := jsonOptionKinds[key]; !ok {
			problems = append(problems, fmt.Sprintf("%s is handed to a JSON updater by updateOptionMap but is missing from jsonOptionKinds", key))
		}
	}
	for key := range jsonOptionKinds {
		if !derivedJSON[key] {
			problems = append(problems, fmt.Sprintf("jsonOptionKinds has %s, which updateOptionMap no longer hands to a JSON updater", key))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("the validate-before-persist tables and the dispatch disagree:\n  %s", strings.Join(problems, "\n  "))
	}
	t.Logf("validation tables cover %d numeric and %d JSON option keys", len(derivedNumeric), len(derivedJSON))
}

// TestRegisteredConfigModulesAreTheKnownSet keeps the module list that
// TestRegisteredPointerFieldsDecodeInPlace
// (internal/app/group_ratio_race_test.go) walks honest: that test can only
// check modules it names, so a Register call with a new module name would be
// invisible to it.
func TestRegisteredConfigModulesAreTheKnownSet(t *testing.T) {
	root := optionGateRepoRoot(t)

	known := map[string]bool{
		"gemini": true, "claude": true, "global": true, "fetch_setting": true,
		"group_ratio_setting": true, "console_setting": true, "checkin_setting": true,
		"general_setting": true, "monitor_setting": true, "quota_setting": true,
		"discord": true, "legal": true, "oidc": true,
	}

	found := map[string]string{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Logf("skipping unparseable %s: %v", rel, parseErr)
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Register" {
				return true
			}
			// Only config.GlobalConfig.Register — internal/lifecycle and
			// repo/tenant_plugin.go have Register methods of their own on
			// unrelated registries.
			receiver, ok := sel.X.(*ast.SelectorExpr)
			if !ok || receiver.Sel.Name != "GlobalConfig" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			found[strings.Trim(lit.Value, string('"'))] = filepath.ToSlash(rel)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}

	var problems []string
	for name, where := range found {
		if !known[name] {
			problems = append(problems, fmt.Sprintf("%s registers config module %q, which TestRegisteredPointerFieldsDecodeInPlace does not walk; add it there and here", where, name))
		}
	}
	for name := range known {
		if _, ok := found[name]; !ok {
			problems = append(problems, fmt.Sprintf("config module %q is in the known set but nothing registers it any more", name))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("registered config modules changed:\n  %s", strings.Join(problems, "\n  "))
	}
	t.Logf("%d registered config modules, all known", len(found))
}
