package router

// The console is a separate build with its own test suite, so nothing today
// notices when a button calls an endpoint the server no longer registers: the
// web tests mock the transport, and the Go tests never look at the console.
// The failure surfaces only as a 404 toast in front of an operator.
//
// This walks web/next/src (the only console; the classic web/src console was
// retired) for every HTTP call the console can make and resolves each against
// the real route table, enumerated the same way v2_completeness_test.go does.
//
// The new console reaches the server through a handful of call shapes, and the
// extractor below understands each of them:
//
//	api.get<T>('/api/x')            plain helper from lib/api.ts (get/post/put/delete)
//	tenantApi.post<T>('/tokens')    prefixes /api/v2/~ (tenantUrl in lib/api.ts)
//	http.get(tenantUrl('/x'))       raw axios instance with an explicit URL
//	http.request({method, url})     axios config object; the verb is read from it
//	call<T>({method, url})          any wrapper taking such a config object
//	fetch('/api/x', {method})       streaming paths, verb from the options object
//	new SSE('/pg/x', {method})      the playground event-stream client
//
// The URL argument is rarely a bare literal: it is usually a module constant
// (`${BASE}/grants`), a one-line helper (`tenantPath(id)`), a local variable
// (`const route = wide ? '/logs/all' : '/logs'`) or an imported constant
// (API_ENDPOINTS.USER_MODELS). All of those are resolved statically; a ternary
// yields every branch and EVERY branch has to be routed. `~` (SELF_TENANT) is
// the :tenant_slug segment, any other interpolation is one wildcard segment.
//
// A call whose URL cannot be resolved to a rooted path is reported, not
// skipped: silently dropping the calls we cannot read would turn this gate
// into a check of whichever calls happen to be easy.
//
// Eight such unrouted calls existed in the classic console as of 2026-08-23
// (Add User, the legacy row actions Disable/Enable/Promote/Demote/Deregister,
// deployments batch_delete, the "check for updates" GET, and the daily-quota
// status/reset pair). Every one of them was a control that could never work,
// so all eight were removed rather than kept as always-404 buttons.
// knownUnrouted stays declared (empty) so a NEW unrouted call still fails
// loudly instead of silently passing.
//
// Removing a console surface is an owner's call, so entries are recorded
// here with what they broke rather than deleted outright when found — but no
// NEW one can be added without either registering the route or getting that
// call.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Frontend calls with no matching route. Each entry is a control that cannot
// work; the value says what it is. Shrink this list, never grow it.
// Key format: "VERB /resolved/path" (query string dropped, interpolations
// as \x00 — see consoleCall.key).
var knownUnrouted = map[string]string{}

// Stands in for an interpolated segment. Its value is unknown at scan time: it
// is usually an id, but `/tenants/${id}/${action}` interpolates the verb too,
// so a segment holding one matches any gin segment, literal or :param. It
// still has to be exactly one segment, so /x/${id}/daily-quota is checked for
// a real `daily-quota` route.
const interpolatedSeg = "\x00"

// Routes that register only when a dependency is configured, so they are
// absent from the table this test builds but present in a real boot. Verified
// against the registration site, not assumed. Keys use consoleCall.key.
var conditionallyRegistered = map[string]string{
	"POST /api/v2/auth/zita-bootstrap": "api-v2-router.go registers this " +
		"inside `if common.ZitaClient != nil`, and the client is nil in a unit " +
		"test process, so the route is absent here for a reason this test " +
		"cannot distinguish from a missing handler. Whether it exists at " +
		"runtime is a deploy-config question, not a routing one.",
	"POST /api/v2/bridge/exchange": "api-v2-router.go registers this " +
		"inside `if handler.BridgeEnabled()`, which reads env " +
		"E2E_BRIDGE_TOKEN — empty in a unit test process and in production, " +
		"set on the isolated acceptance instance. The console screen that " +
		"calls it (features/auth/bridge-login.tsx) asks /api/status first " +
		"and says so instead of calling where the route is absent.",
}

// dynamicCallSites lists calls whose URL is genuinely not statically
// knowable (it arrives as a function parameter), keyed by
// "<path under web/next/src>: <first-argument expression>". Each value says
// why that is acceptable. A call that is NOT listed here and cannot be
// resolved fails the gate.
var dynamicCallSites = map[string]string{}

func collectRoutes(t *testing.T) map[string][]string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	SetApiV2Router(engine)
	// The playground streams through /pg/chat/completions, which lives in the
	// relay router; the only catch-alls it adds sit under /v1 and /v1beta.
	SetRelayRouter(engine)

	routes := map[string][]string{}
	for _, r := range engine.Routes() {
		routes[r.Method] = append(routes[r.Method], r.Path)
	}
	return routes
}

// A gin path matches a concrete request path when they have the same number of
// segments and each gin segment is either identical or a :param. A trailing
// *wildcard swallows the remainder.
func routeMatches(ginPath, concrete string) bool {
	g := strings.Split(strings.Trim(ginPath, "/"), "/")
	c := strings.Split(strings.Trim(concrete, "/"), "/")
	for i, seg := range g {
		if strings.HasPrefix(seg, "*") {
			return true
		}
		if i >= len(c) {
			return false
		}
		if strings.Contains(c[i], interpolatedSeg) {
			continue
		}
		if strings.HasPrefix(seg, ":") {
			if c[i] == "" {
				return false
			}
			continue
		}
		if seg != c[i] {
			return false
		}
	}
	return len(g) == len(c)
}

// ---------------------------------------------------------------------------
// Console source loading
// ---------------------------------------------------------------------------

// consoleSrcRoot is the console's source tree relative to this package.
func consoleSrcRoot() string {
	return filepath.Join("..", "..", "..", "..", "web", "next", "src")
}

// isConsoleSource reports whether a file under web/next/src is shipped
// console code (as opposed to tests, fixtures or generated files). Test files
// are excluded on purpose: a page can be deleted while its test keeps a URL
// string alive, which is exactly how a dead call (or a missing consumer)
// goes unnoticed.
func isConsoleSource(rel string) bool {
	rel = filepath.ToSlash(rel)
	switch filepath.Ext(rel) {
	case ".ts", ".tsx":
	default:
		return false
	}
	base := filepath.Base(rel)
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		base == "routeTree.gen.ts" || strings.HasSuffix(base, ".d.ts") ||
		base == "test-setup.ts" {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "__tests__" || seg == "node_modules" {
			return false
		}
	}
	return true
}

// loadConsoleSources reads every console source file, keyed by its path
// relative to web/next/src (slash-separated). It skips the test when the
// console tree is absent (a checkout without web/).
func loadConsoleSources(t *testing.T) map[string]string {
	t.Helper()
	root := consoleSrcRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skipf("console sources not present at %s: %v", root, err)
	}
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !isConsoleSource(rel) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return files
}

// ---------------------------------------------------------------------------
// Lexical helpers (TypeScript is not parsed, only scanned)
// ---------------------------------------------------------------------------

// blankComments returns src with every comment replaced by spaces (newlines
// kept), so offsets and line numbers still line up with the original. A URL
// named in a comment is not a call, and `//` inside a string is not a comment,
// so strings and template literals (with their nested ${…}) are skipped.
func blankComments(src string) string {
	b := []byte(src)
	var code func(i int, inInterp bool) int
	var tmpl func(i int) int
	tmpl = func(i int) int {
		for i < len(b) {
			switch {
			case b[i] == '\\':
				i += 2
			case b[i] == '`':
				return i + 1
			case b[i] == '$' && i+1 < len(b) && b[i+1] == '{':
				i = code(i+2, true)
			default:
				i++
			}
		}
		return i
	}
	code = func(i int, inInterp bool) int {
		depth := 0
		for i < len(b) {
			c := b[i]
			switch {
			case c == '\'' || c == '"':
				q := c
				i++
				for i < len(b) && b[i] != q && b[i] != '\n' {
					if b[i] == '\\' {
						i++
					}
					i++
				}
				i++
			case c == '`':
				i = tmpl(i + 1)
			case c == '/' && i+1 < len(b) && b[i+1] == '/':
				for i < len(b) && b[i] != '\n' {
					b[i] = ' '
					i++
				}
			case c == '/' && i+1 < len(b) && b[i+1] == '*':
				for i < len(b) && !(b[i] == '*' && i+1 < len(b) && b[i+1] == '/') {
					if b[i] != '\n' {
						b[i] = ' '
					}
					i++
				}
				if i+1 < len(b) {
					b[i], b[i+1] = ' ', ' '
					i += 2
				}
			case c == '{':
				depth++
				i++
			case c == '}':
				if inInterp && depth == 0 {
					return i + 1
				}
				depth--
				i++
			default:
				i++
			}
		}
		return i
	}
	code(0, false)
	return string(b)
}

// skipLiteral returns the index just past the string or template literal that
// opens at s[i]. Template interpolations are skipped as balanced code.
func skipLiteral(s string, i int) int {
	q := s[i]
	i++
	for i < len(s) {
		switch {
		case s[i] == '\\':
			i += 2
		case s[i] == q:
			return i + 1
		case q != '`' && s[i] == '\n':
			return i
		case q == '`' && s[i] == '$' && i+1 < len(s) && s[i+1] == '{':
			i = skipBalanced(s, i+1)
		default:
			i++
		}
	}
	return i
}

// skipBalanced returns the index just past the bracket group opening at s[i]
// ((, [ or {), skipping nested literals.
func skipBalanced(s string, i int) int {
	depth := 0
	for i < len(s) {
		switch s[i] {
		case '\'', '"', '`':
			i = skipLiteral(s, i)
			continue
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return i
}

// skipAngle returns the index just past a generic argument list opening at
// s[i] == '<', or i unchanged when it does not look like one. A '>' that is
// part of '=>' does not close it.
func skipAngle(s string, i int) int {
	if i >= len(s) || s[i] != '<' {
		return i
	}
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '<':
			depth++
		case '>':
			if j > 0 && s[j-1] == '=' {
				continue
			}
			depth--
			if depth == 0 {
				return j + 1
			}
		case '(', '[', '{':
			j = skipBalanced(s, j) - 1
		case '\'', '"', '`':
			j = skipLiteral(s, j) - 1
		case ';':
			return i // a statement boundary: this was a less-than, not a generic
		}
	}
	return i
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// readExpr reads one expression starting at s[i] and returns it together with
// the index where it stops: the first `,` `)` `]` `}` or `;` at depth 0, or a
// newline that cannot be a continuation (so semicolon-less files work).
func readExpr(s string, i int) (string, int) {
	start := i
	for i < len(s) {
		c := s[i]
		switch c {
		case '\'', '"', '`':
			i = skipLiteral(s, i)
			continue
		case '(', '[', '{':
			i = skipBalanced(s, i)
			continue
		case ',', ')', ']', '}', ';':
			return strings.TrimSpace(s[start:i]), i
		case '\n':
			prev := strings.TrimRight(s[start:i], " \t\r\n")
			next := skipSpace(s, i)
			continues := false
			if prev == "" {
				continues = true
			} else if strings.ContainsRune("?:+=(,&|<>-*", rune(prev[len(prev)-1])) {
				continues = true
			}
			if next < len(s) && strings.ContainsRune("?:+.&|", rune(s[next])) {
				continues = true
			}
			if !continues {
				return strings.TrimSpace(s[start:i]), i
			}
		}
		i++
	}
	return strings.TrimSpace(s[start:i]), i
}

// splitTop splits expr on every top-level (depth 0, outside literals) byte sep.
func splitTop(expr string, sep byte) []string {
	var parts []string
	last := 0
	for i := 0; i < len(expr); {
		switch expr[i] {
		case '\'', '"', '`':
			i = skipLiteral(expr, i)
			continue
		case '(', '[', '{':
			i = skipBalanced(expr, i)
			continue
		}
		if expr[i] == sep {
			parts = append(parts, strings.TrimSpace(expr[last:i]))
			last = i + 1
		}
		i++
	}
	return append(parts, strings.TrimSpace(expr[last:]))
}

// splitTernary splits `cond ? a : b` at depth 0, returning (a, b, true). It
// ignores `?.` and `??`, and pairs the first `?` with its own `:` even when
// the branches contain nested ternaries.
func splitTernary(expr string) (string, string, bool) {
	q := -1
	nest := 0
	for i := 0; i < len(expr); {
		switch expr[i] {
		case '\'', '"', '`':
			i = skipLiteral(expr, i)
			continue
		case '(', '[', '{':
			i = skipBalanced(expr, i)
			continue
		case '?':
			if i+1 < len(expr) && (expr[i+1] == '.' || expr[i+1] == '?') {
				i += 2
				continue
			}
			if q < 0 {
				q = i
			} else {
				nest++
			}
		case ':':
			if q >= 0 {
				if nest > 0 {
					nest--
				} else {
					return strings.TrimSpace(expr[q+1 : i]), strings.TrimSpace(expr[i+1:]), true
				}
			}
		}
		i++
	}
	return "", "", false
}

// ---------------------------------------------------------------------------
// Static URL resolution
// ---------------------------------------------------------------------------

var (
	identRe    = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*$`)
	callExprRe = regexp.MustCompile(`^([A-Za-z_$][\w$]*)\s*\(`)
	declRe     = regexp.MustCompile(`(?:^|[^\w$.])(export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=;\n]*)?=\s*`)
	funcDeclRe = regexp.MustCompile(`(?:^|[^\w$.])(export\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(([^()]*)\)\s*(?::\s*[^{]+)?\{\s*return\s+`)
	arrowRe    = regexp.MustCompile(`^(?:async\s*)?\(([^()]*)\)\s*(?::\s*[^=]+?)?=>\s*`)
	objPairRe  = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*:\s*(['"` + "`" + `][^'"` + "`" + `\n]*['"` + "`" + `])`)
	asConstRe  = regexp.MustCompile(`\s+as\s+const\s*$`)
)

type decl struct {
	expr     string
	off      int
	exported bool
}

type helper struct {
	params   []string
	body     string
	exported bool
}

// tsFile is one scanned console file.
type tsFile struct {
	rel     string
	src     string // comments blanked
	decls   map[string][]decl
	helpers map[string]helper
}

// resolver evaluates URL expressions across the whole console tree, so an
// imported constant (`PERF_PATH`) resolves in the file that uses it.
type resolver struct {
	files           map[string]*tsFile
	exportedDecls   map[string][]decl
	exportedHelpers map[string]helper
}

func newResolver(sources map[string]string) *resolver {
	r := &resolver{
		files:           map[string]*tsFile{},
		exportedDecls:   map[string][]decl{},
		exportedHelpers: map[string]helper{},
	}
	for rel, raw := range sources {
		f := &tsFile{
			rel:     rel,
			src:     blankComments(raw),
			decls:   map[string][]decl{},
			helpers: map[string]helper{},
		}
		f.scanDecls(r)
		r.files[rel] = f
	}
	return r
}

func paramNames(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if k := strings.IndexAny(p, ":=?"); k >= 0 {
			p = strings.TrimSpace(p[:k])
		}
		out = append(out, p)
	}
	return out
}

func (f *tsFile) addDecl(r *resolver, name string, d decl) {
	f.decls[name] = append(f.decls[name], d)
	if d.exported {
		r.exportedDecls[name] = append(r.exportedDecls[name], d)
	}
}

func (f *tsFile) addHelper(r *resolver, name string, h helper) {
	f.helpers[name] = h
	if h.exported {
		r.exportedHelpers[name] = h
	}
}

// scanDecls records `const x = <expr>` bindings, one-return helper functions
// and arrow helpers, plus `const OBJ = { KEY: 'literal' }` as "OBJ.KEY".
func (f *tsFile) scanDecls(r *resolver) {
	s := f.src
	for _, m := range declRe.FindAllStringSubmatchIndex(s, -1) {
		exported := m[2] >= 0
		name := s[m[4]:m[5]]
		at := m[1]
		if loc := arrowRe.FindStringSubmatchIndex(s[at:]); loc != nil {
			body := at + loc[1]
			if body < len(s) && s[body] != '{' {
				expr, _ := readExpr(s, body)
				f.addHelper(r, name, helper{params: paramNames(s[at+loc[2] : at+loc[3]]), body: expr, exported: exported})
			}
			continue
		}
		expr, _ := readExpr(s, at)
		if strings.HasPrefix(expr, "{") {
			for _, p := range objPairRe.FindAllStringSubmatch(expr, -1) {
				f.addDecl(r, name+"."+p[1], decl{expr: p[2], off: m[0], exported: exported})
			}
			continue
		}
		f.addDecl(r, name, decl{expr: expr, off: m[0], exported: exported})
	}
	for _, m := range funcDeclRe.FindAllStringSubmatchIndex(s, -1) {
		expr, _ := readExpr(s, m[1])
		f.addHelper(r, s[m[4]:m[5]], helper{params: paramNames(s[m[6]:m[7]]), body: expr, exported: m[2] >= 0})
	}
}

const maxCandidates = 32

// eval resolves expr to the set of rooted-or-not path strings it can take, or
// nil when part of it is unknowable. pos is the call's offset (locals resolve
// to the nearest preceding declaration); locals binds helper parameters.
func (r *resolver) eval(f *tsFile, expr string, pos int, locals map[string]bool, depth int) []string {
	if depth > 8 {
		return nil
	}
	expr = strings.TrimSpace(expr)
	expr = asConstRe.ReplaceAllString(expr, "")
	expr = strings.TrimSuffix(expr, "!")
	if expr == "" {
		return nil
	}
	// (…) wrapper
	if expr[0] == '(' && skipBalanced(expr, 0) == len(expr) {
		return r.eval(f, expr[1:len(expr)-1], pos, locals, depth+1)
	}
	if a, b, ok := splitTernary(expr); ok {
		left := r.eval(f, a, pos, locals, depth+1)
		right := r.eval(f, b, pos, locals, depth+1)
		if left == nil || right == nil {
			return nil
		}
		return append(left, right...)
	}
	if parts := splitTop(expr, '+'); len(parts) > 1 {
		acc := []string{""}
		for _, p := range parts {
			c := r.eval(f, p, pos, locals, depth+1)
			if c == nil {
				return nil
			}
			acc = cross(acc, c)
		}
		return acc
	}
	switch expr[0] {
	case '\'', '"':
		if skipLiteral(expr, 0) == len(expr) {
			return []string{expr[1 : len(expr)-1]}
		}
		return nil
	case '`':
		if skipLiteral(expr, 0) == len(expr) {
			return r.evalTemplate(f, expr[1:len(expr)-1], pos, locals, depth)
		}
		return nil
	}
	if locals[expr] {
		return []string{interpolatedSeg}
	}
	if identRe.MatchString(expr) {
		return r.lookup(f, expr, pos, depth)
	}
	if m := callExprRe.FindStringSubmatch(expr); m != nil {
		open := len(m[0]) - 1
		if skipBalanced(expr, open) != len(expr) {
			return nil
		}
		args := splitTop(expr[open+1:len(expr)-1], ',')
		switch m[1] {
		case "encodeURIComponent", "String", "Number":
			return []string{interpolatedSeg}
		case "tenantUrl":
			if len(args) == 0 {
				return nil
			}
			inner := r.eval(f, args[0], pos, locals, depth+1)
			if inner == nil {
				return nil
			}
			out := make([]string, 0, len(inner))
			for _, p := range inner {
				if !strings.HasPrefix(p, "/") {
					p = "/" + p
				}
				out = append(out, "/api/v2/~"+p)
			}
			return out
		}
		h, ok := f.helpers[m[1]]
		if !ok {
			h, ok = r.exportedHelpers[m[1]]
		}
		if !ok {
			return nil
		}
		inner := map[string]bool{}
		for _, p := range h.params {
			inner[p] = true
		}
		// A helper body resolves against the file that declares it; params
		// stand for wildcards.
		hf := f
		if _, own := f.helpers[m[1]]; !own {
			for _, cand := range r.files {
				if hh, has := cand.helpers[m[1]]; has && hh.exported {
					hf = cand
					break
				}
			}
		}
		return r.eval(hf, h.body, len(hf.src), inner, depth+1)
	}
	return nil
}

func cross(a, b []string) []string {
	out := make([]string, 0, len(a)*len(b))
	for _, x := range a {
		for _, y := range b {
			out = append(out, x+y)
			if len(out) >= maxCandidates {
				return out
			}
		}
	}
	return out
}

// lookup resolves an identifier: the nearest declaration above pos in this
// file, else any exported declaration of that name elsewhere.
func (r *resolver) lookup(f *tsFile, name string, pos, depth int) []string {
	if ds := f.decls[name]; len(ds) > 0 {
		// Nearest declaration above the call; a use that precedes every
		// declaration (hoisted const used in a closure) takes the first.
		pick := ds[0]
		for _, d := range ds {
			if d.off <= pos {
				pick = d
			}
		}
		return r.eval(f, pick.expr, pick.off, nil, depth+1)
	}
	var out []string
	for _, d := range r.exportedDecls[name] {
		for _, of := range r.files {
			if hasDecl(of, name, d) {
				c := r.eval(of, d.expr, d.off, nil, depth+1)
				if c == nil {
					return nil
				}
				out = append(out, c...)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasDecl(f *tsFile, name string, d decl) bool {
	for _, x := range f.decls[name] {
		if x == d {
			return true
		}
	}
	return false
}

// evalTemplate resolves a template literal body. Each ${…} is resolved when it
// can be, else it becomes one wildcard segment.
func (r *resolver) evalTemplate(f *tsFile, body string, pos int, locals map[string]bool, depth int) []string {
	acc := []string{""}
	for i := 0; i < len(body); {
		if body[i] == '\\' && i+1 < len(body) {
			acc = cross(acc, []string{body[i+1 : i+2]})
			i += 2
			continue
		}
		if body[i] == '$' && i+1 < len(body) && body[i+1] == '{' {
			end := skipBalanced(body, i+1)
			inner := body[i+2 : end-1]
			cand := r.eval(f, inner, pos, locals, depth+1)
			if cand == nil {
				cand = []string{interpolatedSeg}
			}
			acc = cross(acc, cand)
			i = end
			continue
		}
		acc = cross(acc, []string{body[i : i+1]})
		i++
	}
	return acc
}

// ---------------------------------------------------------------------------
// Call extraction
// ---------------------------------------------------------------------------

// consoleCall is one HTTP call site in the console source.
type consoleCall struct {
	file   string   // path under web/next/src
	line   int      // 1-based
	verb   string   // upper-case, or "" when not statically knowable
	exprs  string   // the first-argument expression as written
	paths  []string // every concrete path it can take (query dropped); nil = unresolved
	viaFmt string   // which call shape matched, for diagnostics
}

// key is the identity used by knownUnrouted / conditionallyRegistered.
func (c consoleCall) key(path string) string {
	return c.verb + " " + path
}

var (
	callRe  = regexp.MustCompile(`\b(tenantApi|api|http)\s*\.\s*(get|post|put|delete|patch)\b`)
	fetchRe = regexp.MustCompile(`(?:\bfetch|\bnew\s+SSE)\s*\(`)
	urlKey  = regexp.MustCompile(`\burl\s*:\s*`)
	methodK = regexp.MustCompile(`\bmethod\s*:\s*['"` + "`" + `]([A-Za-z]+)['"` + "`" + `]`)
)

func lineOf(src string, off int) int { return strings.Count(src[:off], "\n") + 1 }

// cleanPath drops the query string / fragment of every candidate.
func cleanPath(p string) string {
	if q := strings.IndexAny(p, "?#"); q >= 0 {
		p = p[:q]
	}
	return p
}

func finalize(c *consoleCall, cands []string, tenant bool) {
	if cands == nil {
		return
	}
	var out []string
	for _, p := range cands {
		if tenant {
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			p = "/api/v2/~" + p
		}
		p = cleanPath(p)
		if !strings.HasPrefix(p, "/") || strings.Contains(p, "$") {
			return // not a rooted path: unresolved
		}
		out = append(out, p)
	}
	c.paths = out
}

// insideLiteral reports whether off sits inside a string or template literal
// (a code sample rendered to the user, not a call the console makes).
func insideLiteral(s string, off int) bool {
	for i := 0; i < off; i++ {
		switch s[i] {
		case '\'', '"', '`':
			end := skipLiteral(s, i)
			if end > off {
				return true
			}
			i = end - 1
		}
	}
	return false
}

// externalURL reports whether a URL expression starts with a literal
// http(s):// origin, i.e. targets another site.
func externalURL(expr string) bool {
	e := strings.TrimSpace(expr)
	if e == "" || (e[0] != '\'' && e[0] != '"' && e[0] != '`') {
		return false
	}
	return strings.HasPrefix(e[1:], "http://") || strings.HasPrefix(e[1:], "https://")
}

// objectAround returns the text of the innermost {…} that contains off.
func objectAround(s string, off int) (string, bool) {
	best := ""
	found := false
	for i := 0; i < off; i++ {
		switch s[i] {
		case '\'', '"', '`':
			i = skipLiteral(s, i) - 1
			continue
		case '{':
			end := skipBalanced(s, i)
			if end > off && (!found || end-i < len(best)) {
				best = s[i:end]
				found = true
			}
		}
	}
	return best, found
}

// extractCalls finds every HTTP call in the given sources (path under
// web/next/src → text). lib/api.ts is the transport itself, not a caller.
func extractCalls(sources map[string]string) []consoleCall {
	r := newResolver(sources)
	rels := make([]string, 0, len(r.files))
	for rel := range r.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	var calls []consoleCall
	for _, rel := range rels {
		if rel == "lib/api.ts" {
			continue
		}
		f := r.files[rel]
		s := f.src

		// api.get(...) / tenantApi.post<T>(...) / http.delete(...)
		for _, m := range callRe.FindAllStringSubmatchIndex(s, -1) {
			if m[0] > 0 && s[m[0]-1] == '.' {
				continue // a.b.api.get — not our client
			}
			j := skipSpace(s, skipAngle(s, skipSpace(s, m[1])))
			if j >= len(s) || s[j] != '(' {
				continue // a reference, not a call
			}
			expr, _ := readExpr(s, j+1)
			c := consoleCall{
				file:   rel,
				line:   lineOf(s, m[0]),
				verb:   strings.ToUpper(s[m[4]:m[5]]),
				exprs:  expr,
				viaFmt: s[m[2]:m[3]] + "." + s[m[4]:m[5]],
			}
			finalize(&c, r.eval(f, expr, m[0], nil, 0), s[m[2]:m[3]] == "tenantApi")
			calls = append(calls, c)
		}

		// fetch('/x', {method}) / new SSE('/x', {method})
		for _, m := range fetchRe.FindAllStringIndex(s, -1) {
			expr, end := readExpr(s, m[1])
			c := consoleCall{
				file:   rel,
				line:   lineOf(s, m[0]),
				verb:   "GET",
				exprs:  expr,
				viaFmt: strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s[m[0]:m[1]]), "(")),
			}
			if strings.HasPrefix(c.viaFmt, "new") {
				c.verb = ""
			}
			if end < len(s) && s[end] == ',' {
				if opts, _ := readExpr(s, skipSpace(s, end+1)); strings.HasPrefix(opts, "{") {
					if mm := methodK.FindStringSubmatch(opts); mm != nil {
						c.verb = strings.ToUpper(mm[1])
					} else if strings.Contains(opts, "method") {
						c.verb = "" // computed verb
					}
				}
			}
			cands := r.eval(f, expr, m[0], nil, 0)
			finalize(&c, cands, false)
			// fetch also reaches off-site URLs and appears inside code
			// samples shown to the user. Those two are not ours and are
			// skipped; anything else whose URL cannot be resolved stays in
			// the list as unresolved, so it must be named in
			// dynamicCallSites rather than silently leaving the gate.
			if c.paths == nil && (insideLiteral(s, m[0]) || externalURL(expr)) {
				continue
			}
			calls = append(calls, c)
		}

		// { method: 'GET', url: X } — axios config objects, directly or via a
		// wrapper such as call<T>({...}).
		for _, m := range urlKey.FindAllStringIndex(s, -1) {
			obj, ok := objectAround(s, m[0])
			if !ok {
				continue
			}
			mm := methodK.FindStringSubmatch(obj)
			if mm == nil {
				continue
			}
			expr, _ := readExpr(s, m[1])
			c := consoleCall{
				file:   rel,
				line:   lineOf(s, m[0]),
				verb:   strings.ToUpper(mm[1]),
				exprs:  expr,
				viaFmt: "{method,url}",
			}
			finalize(&c, r.eval(f, expr, m[0], nil, 0), false)
			calls = append(calls, c)
		}
	}
	return calls
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

// minConsoleCalls is the floor on how many calls the extractor must find in
// the real console. Measured at 236 (2026-10-14); the floor is ~70% of that,
// so a regex that quietly stops matching a whole call shape trips it, while
// ordinary churn does not. Raise it when the console grows.
const minConsoleCalls = 165

func TestConsoleCallsResolveToRegisteredRoutes(t *testing.T) {
	sources := loadConsoleSources(t)

	routes := collectRoutes(t)
	if len(routes) == 0 {
		t.Fatal("route table came back empty; the enumeration itself is broken")
	}

	calls := extractCalls(sources)
	t.Logf("extracted %d console HTTP call sites from %d source files", len(calls), len(sources))
	if len(calls) < minConsoleCalls {
		t.Fatalf("extracted only %d console calls (floor %d) — the extractor stopped "+
			"recognising a call shape, so a green run here would mean nothing", len(calls), minConsoleCalls)
	}

	type bad struct{ key, site string }
	var unrouted, unresolved []bad
	seenKnown := map[string]bool{}
	seenConditional := map[string]bool{}
	seenDynamic := map[string]bool{}
	resolved := 0

	for _, c := range calls {
		site := "web/next/src/" + c.file + ":" + strconv.Itoa(c.line)
		if c.paths == nil {
			dk := c.file + ": " + c.exprs
			if _, ok := dynamicCallSites[dk]; ok {
				seenDynamic[dk] = true
				continue
			}
			unresolved = append(unresolved, bad{key: c.viaFmt + "(" + c.exprs + ")", site: site})
			continue
		}
		resolved++
		for _, p := range c.paths {
			key := c.key(p)
			if _, ok := conditionallyRegistered[key]; ok {
				seenConditional[key] = true
				continue
			}
			// `/api/x/` in the source is the collection route itself when
			// nothing follows, so try it with and without the trailing slash.
			candidates := []string{p}
			if strings.HasSuffix(p, "/") {
				candidates = append(candidates, strings.TrimSuffix(p, "/"))
			}
			// Verbs: a knowable verb is checked exactly; an unknowable one
			// (computed method) must match under at least one verb.
			verbs := []string{c.verb}
			if c.verb == "" {
				verbs = verbs[:0]
				for v := range routes {
					verbs = append(verbs, v)
				}
			}
			matched := false
			for _, v := range verbs {
				for _, cand := range candidates {
					for _, gp := range routes[v] {
						if routeMatches(gp, cand) {
							matched = true
						}
					}
				}
			}
			if matched {
				continue
			}
			if _, ok := knownUnrouted[key]; ok {
				seenKnown[key] = true
				continue
			}
			unrouted = append(unrouted, bad{key: key, site: site})
		}
	}
	t.Logf("%d of %d calls resolved to concrete paths", resolved, len(calls))

	if len(unrouted) > 0 {
		sort.Slice(unrouted, func(i, j int) bool { return unrouted[i].key < unrouted[j].key })
		var b strings.Builder
		b.WriteString("console calls with no registered route — these are buttons that 404:\n")
		for _, u := range unrouted {
			b.WriteString("  " + strings.ReplaceAll(u.key, interpolatedSeg, "${…}") + "\n      " + u.site + "\n")
		}
		b.WriteString("Register the route, change the call, or add it to " +
			"knownUnrouted with what it breaks.")
		t.Error(b.String())
	}
	if len(unresolved) > 0 {
		var b strings.Builder
		b.WriteString("console calls whose URL the gate cannot resolve to a rooted path — a call it cannot read is a call it cannot check:\n")
		for _, u := range unresolved {
			b.WriteString("  " + u.key + "\n      " + u.site + "\n")
		}
		b.WriteString("Make the URL statically readable (a literal, a const, a one-line helper) " +
			"or list the call in dynamicCallSites with why it must stay dynamic.")
		t.Error(b.String())
	}

	// A stale allow-list is its own defect: it reads as "known broken" long
	// after the call was fixed or deleted.
	for key := range knownUnrouted {
		if !seenKnown[key] {
			t.Errorf("knownUnrouted lists %q but no console call makes it any more; drop the entry", key)
		}
	}
	for key := range conditionallyRegistered {
		if !seenConditional[key] {
			t.Errorf("conditionallyRegistered lists %q but no console call makes it any more; drop the entry", key)
		}
	}
	for key := range dynamicCallSites {
		if !seenDynamic[key] {
			t.Errorf("dynamicCallSites lists %q but no unresolved console call matches it any more; drop the entry", key)
		}
	}
}

// ---------------------------------------------------------------------------
// The extractor itself: one synthetic snippet per call shape, so the gate
// above cannot go quietly blind to a shape without this test noticing.
// ---------------------------------------------------------------------------

func TestExtractCallsHandlesEveryCallShape(t *testing.T) {
	const w = interpolatedSeg
	cases := []struct {
		name string
		src  string
		want []string // "VERB path", in order; "VERB" alone = unresolved
	}{
		{"literal api.get", `api.get('/api/status')`, []string{"GET /api/status"}},
		{"double quotes", `api.post("/api/verify", {})`, []string{"POST /api/verify"}},
		{"generic with nested braces", `api.get<{ a?: string; b: Record<string, X> } | null>('/api/x/', {})`,
			[]string{"GET /api/x/"}},
		{"generic with arrow type", `api.put<() => void>('/api/y')`, []string{"PUT /api/y"}},
		{"template interpolation", "api.delete(`/api/v2/admin/users/${id}`)",
			[]string{"DELETE /api/v2/admin/users/" + w}},
		{"query string dropped", "api.get(`/api/x?page=${p}&q=1`)", []string{"GET /api/x"}},
		{"prettier-wrapped url", "api.get<T>(\n  `/api/a/${id}/b`,\n  { x: 1 }\n)",
			[]string{"GET /api/a/" + w + "/b"}},
		{"tenantApi prefix", `tenantApi.post('/tokens/batch-delete', { ids })`,
			[]string{"POST /api/v2/~/tokens/batch-delete"}},
		{"tenantApi template", "tenantApi.put(`/tokens/${id}`, body)",
			[]string{"PUT /api/v2/~/tokens/" + w}},
		{"tenantApi path without slash", `tenantApi.get('tokens')`, []string{"GET /api/v2/~/tokens"}},
		{"http + tenantUrl", "http.delete(tenantUrl(`/sessions/${id}`))",
			[]string{"DELETE /api/v2/~/sessions/" + w}},
		{"http.get literal", `http.get<Blob>('/api/setup', { x: 1 })`, []string{"GET /api/setup"}},
		{"module const", "const BASE = '/api/v2/admin/authz'\napi.get(`${BASE}/grants`)",
			[]string{"GET /api/v2/admin/authz/grants"}},
		{"const referencing const", "const A = '/api/v2/admin'\nconst B = `${A}/audit`\napi.get(`${B}/events`)",
			[]string{"GET /api/v2/admin/audit/events"}},
		{"one-line function helper",
			"const BASE = '/api/t'\nfunction tenantPath(id: string): string {\n  return `${BASE}/${encodeURIComponent(id)}`\n}\napi.post(`${tenantPath(id)}/enable`, {})",
			[]string{"POST /api/t/" + w + "/enable"}},
		{"arrow helper", "const routingPath = (model: string) =>\n  `/routing-policies/${encodeURIComponent(model)}`\ntenantApi.put(routingPath(m), body)",
			[]string{"PUT /api/v2/~/routing-policies/" + w}},
		{"local ternary variable",
			"const route = wide ? '/logs/all' : '/logs'\ntenantApi.get<T>(`${route}?${qs}`)",
			[]string{"GET /api/v2/~/logs/all", "GET /api/v2/~/logs"}},
		{"nearest declaration wins",
			"function a() {\n  const route = '/one'\n  tenantApi.get(route)\n}\nfunction b() {\n  const route = '/two'\n  tenantApi.get(route)\n}",
			[]string{"GET /api/v2/~/one", "GET /api/v2/~/two"}},
		{"ternary url in argument", "http.get(scope === 'tenant' ? tenantUrl('/audit/export.csv') : `${ADMIN}/export`)",
			nil}, // ADMIN unresolved -> asserted separately below
		{"object const member", "const E = { USER_MODELS: '/api/user/models' } as const\napi.get(E.USER_MODELS)",
			[]string{"GET /api/user/models"}},
		{"axios config object", "http.request<unknown>({ method: 'GET', url: AFF })\nconst AFF = '/api/v2/admin/routing/affinity'",
			[]string{"GET /api/v2/admin/routing/affinity"}},
		{"wrapper taking config", "call<R>({ method: 'POST', url: '/api/channel/', data })",
			[]string{"POST /api/channel/"}},
		{"config with template url", "call<R>({\n  method: 'DELETE',\n  url: `/api/channel/${id}`,\n})",
			[]string{"DELETE /api/channel/" + w}},
		{"fetch with options", "fetch('/api/channel/vendor-x/pull/stream', { method: 'POST', body })",
			[]string{"POST /api/channel/vendor-x/pull/stream"}},
		{"fetch defaults to GET", "fetch('/api/x')", []string{"GET /api/x"}},
		{"SSE client", "new SSE(E.CHAT, { method: 'POST' })\nconst E = { CHAT: '/pg/chat/completions' }",
			[]string{"POST /pg/chat/completions"}},
		{"fetch to an external host is not ours", "fetch('https://example.com/x')", nil},
		{"fetch inside a code sample", "const s = `await fetch(\"${base}/v1/x\")`", nil},
		{"fetch with an unresolvable own-site url is reported", "fetch(`${base}/api/x`)", []string{"GET"}},
		{"fetch with a variable url is reported", "fetch(url, { method: 'POST' })", []string{"POST"}},
		{"new SSE with an unresolvable url is reported", "new SSE(buildUrl(), { method: 'POST' })", []string{"POST"}},
		{"commented-out call", "// api.get('/api/gone')\n/* api.post('/api/gone2') */", nil},
		{"call in a line comment after code", "const x = 1 // api.get('/api/gone')", nil},
		{"non-call reference", "const f = api.get\nconst g = tenantApi.post;", nil},
		{"url inside an interface", "interface X { method: string; url: string }", nil},
		{"unresolvable parameter", "api.get(someUrl)", []string{"GET"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := extractCalls(map[string]string{"features/x/api.ts": tc.src})
			var got []string
			for _, c := range calls {
				if c.paths == nil {
					got = append(got, c.verb)
					continue
				}
				for _, p := range c.paths {
					got = append(got, c.verb+" "+p)
				}
			}
			if tc.name == "ternary url in argument" {
				// One branch resolves, the other cannot: the whole call must be
				// reported as unresolved rather than half-checked.
				if len(calls) != 1 || calls[0].paths != nil {
					t.Fatalf("want one unresolved call, got %+v", calls)
				}
				return
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractCallsResolvesImportedConstants(t *testing.T) {
	calls := extractCalls(map[string]string{
		"features/a/api.ts":   "export const PERF_PATH = '/api/v2/admin/analytics/model-performance'",
		"features/a/index.ts": "api.get(PERF_PATH)",
	})
	if len(calls) != 1 || len(calls[0].paths) != 1 ||
		calls[0].paths[0] != "/api/v2/admin/analytics/model-performance" {
		t.Fatalf("exported const not resolved across files: %+v", calls)
	}
}

func TestExtractCallsSkipsTheTransportItself(t *testing.T) {
	calls := extractCalls(map[string]string{
		"lib/api.ts": "export const api = { get: (u) => send({ method: 'GET', url: u }) }\nhttp.get(tenantUrl(path))",
	})
	if len(calls) != 0 {
		t.Fatalf("lib/api.ts is the transport, not a caller: %+v", calls)
	}
}

func TestBlankCommentsKeepsOffsetsAndStrings(t *testing.T) {
	src := "const u = 'http://x/y' // api.get('/a')\n/* api.get('/b')\n*/ api.get(`/c/${'//'}`)\n"
	got := blankComments(src)
	if len(got) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(got))
	}
	if strings.Count(got, "\n") != strings.Count(src, "\n") {
		t.Fatal("newlines moved; line numbers would be wrong")
	}
	if !strings.Contains(got, "'http://x/y'") || !strings.Contains(got, "api.get(`/c/${'//'}`)") {
		t.Fatalf("code or strings were blanked: %q", got)
	}
	if strings.Contains(got, "'/a'") || strings.Contains(got, "'/b'") {
		t.Fatalf("comments survived: %q", got)
	}
}

func TestRouteMatchesWildcardSegments(t *testing.T) {
	cases := []struct {
		gin, concrete string
		want          bool
	}{
		{"/api/v2/:tenant_slug/tokens", "/api/v2/~/tokens", true},
		{"/api/v2/:tenant_slug/tokens", "/api/v2/~/tokenz", false},
		{"/api/v2/:tenant_slug/tokens/:id", "/api/v2/~/tokens/" + interpolatedSeg, true},
		{"/api/v2/:tenant_slug/tokens", "/api/v2/~/tokens/extra", false},
		{"/api/v2/admin/tenants/:id/stats", "/api/v2/admin/tenants/" + interpolatedSeg + "/" + interpolatedSeg, true},
		{"/api/v2/admin/tenants/:id/stats", "/api/v2/admin/tenants/" + interpolatedSeg, false},
	}
	for _, tc := range cases {
		if got := routeMatches(tc.gin, tc.concrete); got != tc.want {
			t.Errorf("routeMatches(%q, %q) = %v, want %v", tc.gin, tc.concrete, got, tc.want)
		}
	}
}
