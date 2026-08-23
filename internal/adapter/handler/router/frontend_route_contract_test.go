package router

// The console is a separate build with its own test suite, so nothing today
// notices when a button calls an endpoint the server no longer registers: the
// web tests mock the transport, and the Go tests never look at the console.
// The failure surfaces only as a 404 toast in front of an operator.
//
// This walks web/src for API.<verb>('/api/…') calls and resolves each against
// the real route table, enumerated the same way v2_completeness_test.go does.
//
// Four such calls exist today; they are listed in knownUnrouted below with
// what each one breaks. Removing a console surface is an owner's call, so they
// are recorded rather than deleted — but no NEW one can be added.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Frontend calls with no matching route. Each entry is a control that cannot
// work; the value says what it is. Shrink this list, never grow it.
var knownUnrouted = map[string]string{
	"POST /api/user/": "legacy /console/user “Add User”. The admin user group " +
		"registers GET /, GET /search, GET /:id and PUT / only — there is no " +
		"create handler anywhere in the tree, and v2 admin users is also " +
		"GET/PUT/DELETE, so no console can create a user.",
	"POST /api/user/manage": "legacy /console/user row actions — Disable, " +
		"Enable, Promote, Demote, Deregister. No ManageUser handler exists; it " +
		"went when the service was slimmed to a pure gateway. Working path is " +
		"/console/v2/admin/users.",
	"POST /api/deployments/batch_delete": "legacy /console/deployment batch " +
		"delete. Single delete (DELETE /:id) is registered; the batch endpoint " +
		"never was.",
	"GET /api/status/github-latest-release": "the “check for updates” button " +
		"in settings › Other. No handler references that path at all.",
	"GET /api/user/${userId}/daily-quota": "the whole “daily quota” panel in " +
		"the legacy user editor reads this; no handler in the tree serves it, " +
		"so the panel renders empty every time it is opened.",
	"POST /api/user/${userId}/daily-quota/reset": "the “reset daily quota” " +
		"button in the same panel. No handler exists.",
	"POST /api/deployments/${deploymentId}/start": "the deployment start " +
		"button. The group registers /:id/extend and DELETE /:id but nothing " +
		"for start.",
	"POST /api/deployments/${deploymentId}/restart": "the deployment restart " +
		"button; same gap as start.",
}

// API.get('/x') / API.post(`/x/${id}`) — the axios wrapper the console uses.
// Bare fetch() is not matched: its verb lives in an options object rather than
// the callee, and every current fetch call is a streaming relay path.
var apiCallRe = regexp.MustCompile(
	`\bAPI\.(get|post|put|delete|patch)\s*\(\s*['"` + "`" + `](/[^'"` + "`" + `]*)`)

var interpolationRe = regexp.MustCompile(`\$\{[^}]*\}`)

func collectRoutes(t *testing.T) map[string][]string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	SetApiV2Router(engine)

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

func TestConsoleCallsResolveToRegisteredRoutes(t *testing.T) {
	webSrc := filepath.Join("..", "..", "..", "..", "web", "src")
	if _, err := os.Stat(webSrc); err != nil {
		t.Skipf("console sources not present at %s: %v", webSrc, err)
	}

	routes := collectRoutes(t)
	if len(routes) == 0 {
		t.Fatal("route table came back empty; the enumeration itself is broken")
	}

	type call struct{ key, site string }
	var unrouted []call
	seenKnown := map[string]bool{}

	err := filepath.Walk(webSrc, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if strings.Contains(name, ".test.") {
			return nil
		}
		if ext := filepath.Ext(name); ext != ".js" && ext != ".jsx" &&
			ext != ".ts" && ext != ".tsx" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			for _, m := range apiCallRe.FindAllStringSubmatch(line, -1) {
				method := strings.ToUpper(m[1])
				raw := m[2]
				// Drop any query string, and stand a placeholder in for each
				// interpolated id so the path has its real segment count.
				if q := strings.IndexAny(raw, "?#"); q >= 0 {
					raw = raw[:q]
				}
				concrete := interpolationRe.ReplaceAllString(raw, "1")
				if strings.Contains(concrete, "$") {
					// A template whose interpolation spans lines; not resolvable
					// from this line alone.
					continue
				}
				// `/api/x/` in the source is the collection route itself when
				// nothing follows, so try it with and without the trailing slash.
				candidates := []string{concrete}
				if strings.HasSuffix(concrete, "/") {
					candidates = append(candidates, strings.TrimSuffix(concrete, "/"))
				}
				matched := false
				for _, cand := range candidates {
					for _, gp := range routes[method] {
						if routeMatches(gp, cand) {
							matched = true
							break
						}
					}
					if matched {
						break
					}
				}
				if matched {
					continue
				}
				key := method + " " + raw
				if _, ok := knownUnrouted[key]; ok {
					seenKnown[key] = true
					continue
				}
				rel, _ := filepath.Rel(filepath.Join("..", "..", "..", ".."), path)
				unrouted = append(unrouted, call{
					key:  key,
					site: filepath.ToSlash(rel) + ":" + itoa(i+1),
				})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", webSrc, err)
	}

	if len(unrouted) > 0 {
		sort.Slice(unrouted, func(i, j int) bool { return unrouted[i].key < unrouted[j].key })
		var b strings.Builder
		b.WriteString("console calls with no registered route — these are buttons that 404:\n")
		for _, u := range unrouted {
			b.WriteString("  " + u.key + "\n      " + u.site + "\n")
		}
		b.WriteString("Register the route, change the call, or add it to " +
			"knownUnrouted with what it breaks.")
		t.Error(b.String())
	}

	// A stale allow-list is its own defect: it reads as "known broken" long
	// after the call was fixed or deleted.
	for key := range knownUnrouted {
		if !seenKnown[key] {
			t.Errorf("knownUnrouted lists %q but no console call makes it any more; drop the entry", key)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
