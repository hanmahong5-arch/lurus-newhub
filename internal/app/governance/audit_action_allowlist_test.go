package governance

// audit_action_allowlist_test.go — cycle 13 L4 (D-L4-2) structural gate.
//
// The taxonomy has two halves that must agree: the `Action*` constant block
// (what handlers pass to RecordAuditEvent) and `validAuditActions` (what
// IsValidAuditAction answers for). Nothing linked them: a new action wired
// into a handler but forgotten in the map still records rows, so every
// handler-side oracle stays green, while
// GET /api/v2/admin/audit/events?action=<new> answers 400 "unknown audit
// action" (v2_admin_audit.go) and AllAuditActions() leaves the entry out of
// the console's filter menu. That combination was measured on the six
// actions this cycle added: deleting them from validAuditActions left all
// four taxonomy tests and all three handler audit oracles green.
//
// This gate reads the package's own source with go/ast, so it sees every
// declared constant rather than a hand-copied list, and asserts each one's
// value is in the allow-set.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// auditActionConstFloor is a sites-seen floor: if the AST walk suddenly sees
// far fewer constants than the taxonomy has (a moved file, a parser change, a
// build-tag split), the gate must fail rather than pass vacuously. 80 is
// below the 88 constants present when this gate was written, leaving room to
// retire a few without editing the test, and far above zero.
const auditActionConstFloor = 80

// auditActionConstants walks every non-test .go file in this package and
// returns identifier -> string value for each `Action…` constant. A
// name that starts with "Action" but is not a plain string constant is
// returned in the second slice: unclassifiable, and the caller fails on it
// rather than dropping it silently.
func auditActionConstants(t *testing.T) (map[string]string, []string) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	found := map[string]string{}
	var unclassifiable []string
	filesParsed := 0

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		filesParsed++

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range vs.Names {
					if !strings.HasPrefix(ident.Name, "Action") {
						continue
					}
					if i >= len(vs.Values) {
						unclassifiable = append(unclassifiable,
							name+":"+ident.Name+" (no value expression — iota or grouped assignment?)")
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						unclassifiable = append(unclassifiable,
							name+":"+ident.Name+" (value is not a string literal)")
						continue
					}
					value, unqErr := strconv.Unquote(lit.Value)
					if unqErr != nil {
						unclassifiable = append(unclassifiable, name+":"+ident.Name+" ("+unqErr.Error()+")")
						continue
					}
					found[ident.Name] = value
				}
			}
		}
	}

	if filesParsed == 0 {
		t.Fatalf("parsed 0 non-test .go files in the package directory — the walk is broken, not the taxonomy")
	}
	return found, unclassifiable
}

// TestEveryActionConstantIsInTheAllowSet is the gate: every declared
// Action… constant must answer true from IsValidAuditAction, i.e. be present
// in validAuditActions.
func TestEveryActionConstantIsInTheAllowSet(t *testing.T) {
	found, unclassifiable := auditActionConstants(t)

	if len(unclassifiable) > 0 {
		t.Fatalf("Action… constants the gate could not read (fix the gate or the declaration, do not ignore): %v", unclassifiable)
	}
	if len(found) == 0 {
		t.Fatalf("the AST walk found 0 Action… constants — the gate proves nothing in this state")
	}
	if len(found) < auditActionConstFloor {
		t.Fatalf("the AST walk found only %d Action… constants, below the floor of %d", len(found), auditActionConstFloor)
	}

	for ident, value := range found {
		if !IsValidAuditAction(value) {
			t.Errorf("%s = %q is declared but missing from validAuditActions: "+
				"handlers can record it, but the audit export endpoint answers 400 for it "+
				"and AllAuditActions() omits it from the console filter", ident, value)
		}
	}
}

// TestCycle13ActionsAreDeclaredAndAllowed names the six actions cycle 13 L4
// added, so the gate above cannot quietly stop covering them (a renamed
// identifier would still satisfy a purely generic walk).
func TestCycle13ActionsAreDeclaredAndAllowed(t *testing.T) {
	found, _ := auditActionConstants(t)

	want := map[string]string{
		"ActionInternalKeyCreated": ActionInternalKeyCreated,
		"ActionInternalKeyUpdated": ActionInternalKeyUpdated,
		"ActionInternalKeyDeleted": ActionInternalKeyDeleted,
		"ActionInternalKeyToggled": ActionInternalKeyToggled,
		"ActionChannelKeyAccessed": ActionChannelKeyAccessed,
		"ActionLogsPurged":         ActionLogsPurged,
	}
	for ident, value := range want {
		got, ok := found[ident]
		if !ok {
			t.Errorf("%s is not declared as a string constant in this package any more", ident)
			continue
		}
		if got != value {
			t.Errorf("%s: AST reads %q, compiled value is %q", ident, got, value)
		}
		if !IsValidAuditAction(value) {
			t.Errorf("%s = %q is not in validAuditActions", ident, value)
		}
	}
}
