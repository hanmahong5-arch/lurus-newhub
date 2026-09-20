package middleware

// session_identity_write_sites_test.go — cycle-13 L8 repair round, operator
// decision D-L8-1.
//
// The session-fixation fix is a list of call sites, and a list maintained by
// hand is a list that goes stale: the first round found three of them and
// the fourth (middleware/auth.go's SDK-bridge self-heal arm) shipped
// unrotated while a comment claimed the set was complete.
//
// This gate rebuilds the list from the source instead. It parses the
// non-test files of internal/adapter/handler and internal/adapter/middleware,
// collects the functions that write an authenticated identity into a gin
// session (session.Set("id"…) / session.Set("username"…)) and requires that
// each one calls RotateSessionID earlier in its body. A fifth site added
// tomorrow fails here rather than at the next security review.
//
// Two deliberate limits, stated rather than papered over:
//   - _test.go files are skipped; the subject is production identity writes.
//   - a receiver the gate cannot classify (neither a *gin.Context nor a
//     value obtained from sessions.Default) is reported as a failure rather
//     than ignored, so renaming a variable cannot walk around it.

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

// identityWriteKeys are the session keys that turn a session into an
// authenticated one. "id" is the one authHelper reads back as the caller's
// user id; "username" is the one that makes resolveSessionIdentity stop
// looking for another credential.
var identityWriteKeys = map[string]bool{"id": true, "username": true}

// sessionWriteScanDirs are the two packages that can reach a gin session.
// Paths are module-root relative.
var sessionWriteScanDirs = []string{
	filepath.Join("internal", "adapter", "handler"),
	filepath.Join("internal", "adapter", "middleware"),
}

// sessionGateModuleRoot walks up from the test's working directory (the
// package directory) to the directory holding go.mod.
func sessionGateModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the module root above this package")
	return ""
}

// identityWriteSite is one function that writes an identity into a session.
type identityWriteSite struct {
	file     string
	function string
	line     int
	rotated  bool
}

// collectTypedIdents returns the names bound to *gin.Context parameters and
// the names bound to a session value, anywhere in one file. The union is
// taken per FILE rather than per function on purpose: it over-approximates
// in the safe direction (a name that is a gin context somewhere in the file
// is not mistaken for a session elsewhere), and the enclosing-function check
// that matters — "was RotateSessionID called first" — is still per function.
func collectTypedIdents(file *ast.File) (ginIdents, sessionIdents map[string]bool) {
	ginIdents = map[string]bool{}
	sessionIdents = map[string]bool{}

	addParams := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			var typeName string
			switch typ := field.Type.(type) {
			case *ast.StarExpr:
				if sel, ok := typ.X.(*ast.SelectorExpr); ok {
					typeName = exprPkgSel(sel)
				}
			case *ast.SelectorExpr:
				typeName = exprPkgSel(typ)
			}
			for _, name := range field.Names {
				switch typeName {
				case "gin.Context":
					ginIdents[name.Name] = true
				case "sessions.Session":
					sessionIdents[name.Name] = true
				}
			}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			addParams(node.Type.Params)
		case *ast.FuncLit:
			addParams(node.Type.Params)
		case *ast.AssignStmt:
			// name := sessions.Default(c)
			for i, rhs := range node.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || i >= len(node.Lhs) {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || exprPkgSel(sel) != "sessions.Default" {
					continue
				}
				if ident, ok := node.Lhs[i].(*ast.Ident); ok {
					sessionIdents[ident.Name] = true
				}
			}
		}
		return true
	})
	return ginIdents, sessionIdents
}

// exprPkgSel renders a selector as "pkg.Name" when its base is a plain
// identifier, else "".
func exprPkgSel(sel *ast.SelectorExpr) string {
	base, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return base.Name + "." + sel.Sel.Name
}

// stringLiteral returns the value of a plain string literal argument.
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// rotationCallPositions returns the positions of every RotateSessionID call
// inside body, written either bare (same package) or qualified.
func rotationCallPositions(body ast.Node) []token.Pos {
	var out []token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "RotateSessionID" {
				out = append(out, call.Pos())
			}
		case *ast.SelectorExpr:
			if fn.Sel.Name == "RotateSessionID" {
				out = append(out, call.Pos())
			}
		}
		return true
	})
	return out
}

func TestSessionIdentityWriteSites_RotateFirst(t *testing.T) {
	root := sessionGateModuleRoot(t)
	fset := token.NewFileSet()

	var sites []identityWriteSite
	for _, rel := range sessionWriteScanDirs {
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ginIdents, sessionIdents := collectTypedIdents(file)

			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				rotations := rotationCallPositions(fn.Body)
				firstWrite := token.NoPos

				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) == 0 {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Set" {
						return true
					}
					recv, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					key, ok := stringLiteral(call.Args[0])
					if !ok || !identityWriteKeys[key] {
						return true
					}
					if ginIdents[recv.Name] {
						return true // c.Set(…) — a gin context key, not a session
					}
					if !sessionIdents[recv.Name] {
						t.Errorf("%s:%d: %s.Set(%q, …): the gate cannot tell whether %q is a gin context or a session. "+
							"Bind it from sessions.Default(c) or declare it as sessions.Session so this stays checkable.",
							filepath.Join(rel, name), fset.Position(call.Pos()).Line, recv.Name, key, recv.Name)
						return true
					}
					if firstWrite == token.NoPos || call.Pos() < firstWrite {
						firstWrite = call.Pos()
					}
					return true
				})

				if firstWrite == token.NoPos {
					continue
				}
				rotated := false
				for _, pos := range rotations {
					if pos < firstWrite {
						rotated = true
						break
					}
				}
				sites = append(sites, identityWriteSite{
					file:     filepath.ToSlash(filepath.Join(rel, name)),
					function: fn.Name.Name,
					line:     fset.Position(firstWrite).Line,
					rotated:  rotated,
				})
			}
		}
	}

	if len(sites) == 0 {
		t.Fatal("found 0 session identity-write sites — the scan itself is broken (a gate that sees nothing passes everything)")
	}
	// Four sites are wired today (the three logins plus the SDK-bridge
	// self-heal arm). Fewer means one moved somewhere this gate no longer
	// looks, which is the case it exists to catch.
	if len(sites) < 4 {
		t.Errorf("found %d identity-write sites, want at least 4: %+v", len(sites), sites)
	}
	for _, site := range sites {
		if !site.rotated {
			t.Errorf("%s:%d: %s writes an authenticated identity into the gin session without calling RotateSessionID first — "+
				"a session cookie planted before this point becomes the authenticated session (session fixation)",
				site.file, site.line, site.function)
		}
	}
	t.Logf("identity-write sites checked: %d", len(sites))
	for _, site := range sites {
		t.Logf("  %s:%d %s (rotated=%v)", site.file, site.line, site.function, site.rotated)
	}
}

// TestSessionStoreKeyPrefix_AgreesWithTheOtherDeleters pins the third copy
// of the store's key prefix against the two that cannot be imported from
// here. If boj/redistore's prefix ever changes, all three have to move
// together or a rotated session id would be deleted under a key nobody
// wrote.
func TestSessionStoreKeyPrefix_AgreesWithTheOtherDeleters(t *testing.T) {
	root := sessionGateModuleRoot(t)
	literal := strconv.Quote(sessionStoreKeyPrefix)
	for _, rel := range []string{
		filepath.Join("internal", "adapter", "handler", "v2_session_revoke.go"),
		filepath.Join("internal", "adapter", "repo", "user_session.go"),
	} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(body), literal+"+") {
			t.Errorf("%s no longer builds a store key from %s — the three session-key deleters have drifted apart",
				filepath.ToSlash(rel), literal)
		}
	}
}
