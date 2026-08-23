package router

// The console is a separate build with its own test suite, so nothing today
// notices when a button calls an endpoint the server no longer registers: the
// web tests mock the transport, and the Go tests never look at the console.
// The failure surfaces only as a 404 toast in front of an operator.
//
// This walks web/src for API.<verb>('/api/…') calls and resolves each against
// the real route table, enumerated the same way v2_completeness_test.go does.
//
// Eight such calls existed as of 2026-08-23 (Add User, the legacy row actions
// Disable/Enable/Promote/Demote/Deregister, deployments batch_delete, the
// "check for updates" GET, and the daily-quota status/reset pair). Every one
// of them was a console control that could never work, so all eight were
// removed from web/src rather than kept as always-404 buttons; see PR history
// around 2026-08-23 for the removal. knownUnrouted stays declared (empty) so
// a NEW unrouted call still fails loudly instead of silently passing.
//
// Removing a console surface is an owner's call, so entries are recorded
// here with what they broke rather than deleted outright when found — but no
// NEW one can be added without either registering the route or getting that
// call.

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
var knownUnrouted = map[string]string{}

// API.get('/x') / API.post(`/x/${id}`) — the axios wrapper the console uses.
// Bare fetch() is not matched: its verb lives in an options object rather than
// the callee, and every current fetch call is a streaming relay path.
// \s* spans newlines, so this also catches the prettier-wrapped form where the
// URL sits on the line after `API.post(`; the URL itself may not span lines.
var apiCallRe = regexp.MustCompile(
	`\bAPI\.(get|post|put|delete|patch)\s*\(\s*['"` + "`" + `](/[^'"` + "`" + `\n]*)`)

var interpolationRe = regexp.MustCompile(`\$\{[^}]*\}`)

// Stands in for an interpolated segment. Its value is unknown at scan time: it
// is usually an id, but `/tenants/${id}/${action}` interpolates the verb too,
// so a segment holding one matches any gin segment, literal or :param. It
// still has to be exactly one segment, so /x/${id}/daily-quota is checked for
// a real `daily-quota` route.
const interpolatedSeg = "\x00"

// Routes that register only when a dependency is configured, so they are
// absent from the table this test builds but present in a real boot. Verified
// against the registration site, not assumed.
var conditionallyRegistered = map[string]string{
	"POST /api/v2/auth/zita-bootstrap": "api-v2-router.go registers this " +
		"inside `if common.ZitaClient != nil`, and the client is nil in a unit " +
		"test process, so the route is absent here for a reason this test " +
		"cannot distinguish from a missing handler. Whether it exists at " +
		"runtime is a deploy-config question, not a routing one.",
}

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
	seenConditional := map[string]bool{}

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
		// Scan the whole file, not line by line: prettier wraps any call whose
		// URL pushes the line past 80 columns onto its own line, and a
		// line-scoped scan cannot see those at all — which is most of the
		// interpolated ones.
		src := string(body)
		for _, loc := range apiCallRe.FindAllStringSubmatchIndex(src, -1) {
			method := strings.ToUpper(src[loc[2]:loc[3]])
			raw := src[loc[4]:loc[5]]
			i := strings.Count(src[:loc[0]], "\n")
			// Drop any query string, and stand a placeholder in for each
			// interpolated segment so the path has its real segment count.
			if q := strings.IndexAny(raw, "?#"); q >= 0 {
				raw = raw[:q]
			}
			concrete := interpolationRe.ReplaceAllString(raw, interpolatedSeg)
			if strings.Contains(concrete, "$") {
				// A template whose interpolation spans lines; not resolvable.
				continue
			}
			key := method + " " + raw
			if _, ok := conditionallyRegistered[key]; ok {
				seenConditional[key] = true
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
	for key := range conditionallyRegistered {
		if !seenConditional[key] {
			t.Errorf("conditionallyRegistered lists %q but no console call makes it any more; drop the entry", key)
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
