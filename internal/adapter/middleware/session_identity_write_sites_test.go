package middleware

// session_identity_write_sites_test.go — cycle-13 L8, operator decisions
// D-L8-1 (the gate) and D-L8-7 (the two shapes it was blind to).
//
// The session-fixation fix is a list of call sites, and a list maintained by
// hand is a list that goes stale: the first round found three of them and
// the fourth (middleware/auth.go's SDK-bridge self-heal arm) shipped
// unrotated while a comment claimed the set was complete.
//
// This gate rebuilds the list from the source instead. It parses the
// non-test files of internal/adapter/handler and internal/adapter/middleware,
// collects the functions that write an authenticated identity into a gin
// session and requires that each one calls RotateSessionID earlier in its
// body. A fifth site added tomorrow fails here rather than at the next
// security review.
//
// What it understands, and what it refuses to guess (the acceptance round
// walked around the first version with both of these):
//
//   - receiver `s` bound anywhere in the file from sessions.Default(c), and
//     the inline form sessions.Default(c).Set("id", …);
//   - a key given as a string literal, or as an identifier that resolves to
//     a string constant declared in either scanned package (the code base
//     has two of those: SecureVerificationSessionKey and
//     sessionRotatedAtKey);
//   - a key it cannot resolve on a session receiver, and an identity key on
//     a receiver it cannot classify, are reported as PROBLEMS — the gate
//     fails rather than skipping the site.
//
// Two deliberate limits, stated rather than papered over: _test.go files are
// skipped (the subject is production identity writes), and a write hidden
// behind a helper that takes the session as a parameter would be attributed
// to the helper, which is where the rotation would then be required.
// TestIdentityWriteScanner_SeesBothWriteShapes is the gate's own oracle: it
// feeds the scanner synthetic sources in each shape, so "the scan found
// nothing" cannot be mistaken for "there is nothing to find".

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
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

// identityWriteScan is what one pass over a set of files produced: the
// identity-write sites it recognised, and the places it could not classify.
// Problems are failures, not silence — an unreadable site is exactly the
// shape a future fifth site would take.
type identityWriteScan struct {
	sites    []identityWriteSite
	problems []string
}

// stringConstants maps the name of every string constant declared in files
// to its value. A name declared twice with different values maps to two
// values and is then treated as unresolvable (see resolveKey).
func stringConstants(files map[string]*ast.File) map[string][]string {
	out := map[string][]string{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			decl, ok := n.(*ast.GenDecl)
			if !ok || decl.Tok != token.CONST {
				return true
			}
			for _, spec := range decl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if lit, ok := stringLiteral(vs.Values[i]); ok {
						known := false
						for _, seen := range out[name.Name] {
							if seen == lit {
								known = true
							}
						}
						if !known {
							out[name.Name] = append(out[name.Name], lit)
						}
					}
				}
			}
			return true
		})
	}
	return out
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
				if i >= len(node.Lhs) || !isSessionsDefaultCall(rhs) {
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

// isSessionsDefaultCall reports whether expr is a sessions.Default(…) call.
func isSessionsDefaultCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && exprPkgSel(sel) == "sessions.Default"
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

// stringLiteral returns the value of a plain string literal expression.
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

// resolveKey turns a Set() key argument into the string it will be at run
// time: a literal directly, an identifier through the constant table. The
// second return value is false when the key cannot be pinned down.
func resolveKey(expr ast.Expr, consts map[string][]string) (string, bool) {
	if v, ok := stringLiteral(expr); ok {
		return v, true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	values := consts[ident.Name]
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}

// receiverKind classifies the value a Set() call is made on.
type receiverKind int

const (
	receiverOther   receiverKind = iota // not a gin context and not a session (url.Values, http.Header, a prometheus gauge…)
	receiverGin                         // c.Set(…) — a gin context key
	receiverSession                     // a gin-contrib session
	receiverUnknown                     // a bare identifier the gate cannot place
)

// classifyReceiver places the expression a .Set() was called on.
func classifyReceiver(x ast.Expr, ginIdents, sessionIdents map[string]bool) (receiverKind, string) {
	if isSessionsDefaultCall(x) {
		return receiverSession, "sessions.Default(…)"
	}
	ident, ok := x.(*ast.Ident)
	if !ok {
		return receiverOther, ""
	}
	switch {
	case ginIdents[ident.Name]:
		return receiverGin, ident.Name
	case sessionIdents[ident.Name]:
		return receiverSession, ident.Name
	default:
		return receiverUnknown, ident.Name
	}
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

// scanIdentityWrites is the gate's engine, kept separate from the disk walk
// so it can be exercised on synthetic sources (see
// TestIdentityWriteScanner_SeesBothWriteShapes).
func scanIdentityWrites(fset *token.FileSet, files map[string]*ast.File) identityWriteScan {
	var scan identityWriteScan
	consts := stringConstants(files)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		file := files[name]
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
				kind, recv := classifyReceiver(sel.X, ginIdents, sessionIdents)
				if kind == receiverGin || kind == receiverOther {
					return true
				}
				line := fset.Position(call.Pos()).Line
				key, resolved := resolveKey(call.Args[0], consts)

				if kind == receiverUnknown {
					// Only interesting when the key IS an identity key:
					// http.Header, url.Values and friends live here too.
					if resolved && identityWriteKeys[key] {
						scan.problems = append(scan.problems, strings.Join([]string{
							name, ":", strconv.Itoa(line), ": ", recv, ".Set(", strconv.Quote(key),
							", …): the gate cannot tell whether ", recv, " is a gin context or a session. ",
							"Bind it from sessions.Default(c) or declare it as sessions.Session so this stays checkable.",
						}, ""))
					}
					return true
				}

				// A session receiver from here on.
				if !resolved {
					scan.problems = append(scan.problems, strings.Join([]string{
						name, ":", strconv.Itoa(line), ": ", recv, ".Set(<non-constant key>, …): ",
						"this writes into a gin session under a key the gate cannot read, so it cannot tell an identity ",
						"write from any other value. Use a string literal or a string constant declared in these packages.",
					}, ""))
					return true
				}
				if !identityWriteKeys[key] {
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
			scan.sites = append(scan.sites, identityWriteSite{
				file:     name,
				function: fn.Name.Name,
				line:     fset.Position(firstWrite).Line,
				rotated:  rotated,
			})
		}
	}
	return scan
}

func TestSessionIdentityWriteSites_RotateFirst(t *testing.T) {
	root := sessionGateModuleRoot(t)
	fset := token.NewFileSet()
	files := map[string]*ast.File{}

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
			files[filepath.ToSlash(filepath.Join(rel, name))] = file
		}
	}

	scan := scanIdentityWrites(fset, files)

	for _, problem := range scan.problems {
		t.Errorf("%s", problem)
	}
	if len(scan.sites) == 0 {
		t.Fatal("found 0 session identity-write sites — the scan itself is broken (a gate that sees nothing passes everything)")
	}
	// Four sites are wired today (the three logins plus the SDK-bridge
	// self-heal arm). Fewer means one moved somewhere this gate no longer
	// looks, which is the case it exists to catch.
	if len(scan.sites) < 4 {
		t.Errorf("found %d identity-write sites, want at least 4: %+v", len(scan.sites), scan.sites)
	}
	for _, site := range scan.sites {
		if !site.rotated {
			t.Errorf("%s:%d: %s writes an authenticated identity into the gin session without calling RotateSessionID first — "+
				"a session cookie planted before this point becomes the authenticated session (session fixation)",
				site.file, site.line, site.function)
		}
	}
	t.Logf("identity-write sites checked: %d", len(scan.sites))
	for _, site := range scan.sites {
		t.Logf("  %s:%d %s (rotated=%v)", site.file, site.line, site.function, site.rotated)
	}
}

// TestIdentityWriteScanner_SeesBothWriteShapes is the gate's own oracle.
// The acceptance round proved the first version was blind to two shapes it
// claimed to cover (a chained sessions.Default(c).Set receiver, and a key
// given as a constant): both walked past it while the site count stayed at
// four. Reading real files cannot demonstrate that a shape is SEEN, because
// nobody writes it here — so the scanner is fed each shape directly.
func TestIdentityWriteScanner_SeesBothWriteShapes(t *testing.T) {
	sources := map[string]string{
		// bound receiver, literal key, no rotation
		"a_bound.go": `package p
func BoundReceiver(c *gin.Context) {
	s := sessions.Default(c)
	s.Set("id", 1)
}`,
		// chained receiver, literal key, no rotation
		"b_chained.go": `package p
func ChainedReceiver(c *gin.Context) {
	sessions.Default(c).Set("username", "victim")
}`,
		// bound receiver, key behind a constant, no rotation
		"c_const.go": `package p
const identityKey = "id"

func ConstantKey(c *gin.Context) {
	s := sessions.Default(c)
	s.Set(identityKey, 1)
}`,
		// rotated first — a site, but a compliant one
		"d_rotated.go": `package p
func Rotated(c *gin.Context) {
	_ = RotateSessionID(c)
	s := sessions.Default(c)
	s.Set("id", 1)
}`,
		// a key the gate cannot read, on a session receiver
		"e_dynamic.go": `package p
func DynamicKey(c *gin.Context, name string) {
	s := sessions.Default(c)
	s.Set(name, 1)
}`,
		// not a session: gin context keys and a url.Values must stay silent
		"f_noise.go": `package p
func Noise(c *gin.Context) {
	c.Set("id", 1)
	params := url.Values{}
	params.Set("client_id", "x")
	params.Set(dynamic(), "y")
}`,
	}

	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for name, src := range sources {
		file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = file
	}

	scan := scanIdentityWrites(fset, files)

	got := map[string]bool{}
	for _, site := range scan.sites {
		got[site.function] = site.rotated
	}
	for _, want := range []string{"BoundReceiver", "ChainedReceiver", "ConstantKey", "Rotated"} {
		if _, ok := got[want]; !ok {
			t.Errorf("the scanner did not see %s as an identity-write site — that shape can be added to the two packages and ship unrotated: %+v",
				want, scan.sites)
		}
	}
	for _, want := range []string{"BoundReceiver", "ChainedReceiver", "ConstantKey"} {
		if got[want] {
			t.Errorf("%s has no rotation before its identity write but the scanner reported rotated=true", want)
		}
	}
	if !got["Rotated"] {
		t.Errorf("Rotated calls RotateSessionID before its write and must be reported as compliant")
	}
	if _, ok := got["Noise"]; ok {
		t.Errorf("c.Set(\"id\", …) and url.Values.Set are not session identity writes; the scanner reported Noise as one")
	}

	problems := strings.Join(scan.problems, "\n")
	if !strings.Contains(problems, "e_dynamic.go") {
		t.Errorf("a Set() on a session under a key the gate cannot read must be reported as a problem, got: %q", problems)
	}
	if strings.Contains(problems, "f_noise.go") {
		t.Errorf("a non-session receiver with an unreadable key is not this gate's business, got: %q", problems)
	}
}

// TestSessionStoreKeyPrefix_AgreesWithTheOtherDeleters pins the copies of
// the store's key prefix that are still spelled by hand against
// common.SessionStoreKeyPrefix, which middleware now uses. Neither file is
// callable from here (handler imports this package, not the other way round;
// repo's deleter is unexported), so a text check is what is left. If
// boj/redistore's prefix ever changes, every copy has to move together or a
// rotated session id would be deleted under a key nobody wrote.
func TestSessionStoreKeyPrefix_AgreesWithTheOtherDeleters(t *testing.T) {
	root := sessionGateModuleRoot(t)
	literal := strconv.Quote(common.SessionStoreKeyPrefix)
	for _, rel := range []string{
		filepath.Join("internal", "adapter", "handler", "v2_session_revoke.go"),
		filepath.Join("internal", "adapter", "repo", "user_session.go"),
	} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(body)
		// Either spelling is fine; what must not happen is a third value.
		if strings.Contains(text, "common.SessionStoreKeyPrefix+") {
			continue
		}
		if !strings.Contains(text, literal+"+") {
			t.Errorf("%s builds its session store key from neither %s nor common.SessionStoreKeyPrefix — the session-key deleters have drifted apart",
				filepath.ToSlash(rel), literal)
		}
	}
}
