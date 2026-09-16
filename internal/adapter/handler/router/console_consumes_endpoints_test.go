package router

// The reverse of frontend_route_contract_test.go. That one catches a console
// control calling a route the server does not register. This one catches the
// opposite and, as of cycle 10, more common failure: the server ships a route
// that projects data to a person, and no console surface ever reads it.
//
// Measured 2026-09-16, before this gate existed:
//   - GET /api/data/self/ had been aggregating per-day, per-model usage since
//     2026-06-25 (1614 rows in production) with zero consumers in web/src.
//   - GET /api/status already returned announcements, faq and api_info; the v2
//     dashboard discarded all three.
//   - The per-tenant model allow-list endpoints had no console consumer while
//     the Models page rendered a banner calling that capability "deferred".
//   - PUT /api/user/setting had a working legacy consumer, and the v2 Settings
//     page was about to delete its own on the theory the feature did not exist.
//
// In each case the product looked unfinished while the capability was built
// and running. Three cycles produced one of these, so it gets a gate.
//
// The list is deliberately explicit and hand-curated rather than derived: only
// a person can say whether a route is meant to reach a person at all. Adding a
// user-facing route means adding it here with a consumer, or adding it to
// noConsoleConsumer with a reason that names the intended consumer.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A path named in a COMMENT is not a consumer. Stripping comments before the
// match is what makes this gate mean "something calls it" rather than
// "something mentions it": when this test was first mutation-checked, deleting
// the admin-rankings call left the gate green because the same file's doc
// comment still spelled the path out.
var (
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineCommentRe  = regexp.MustCompile(`(?m)^\s*//.*$`)
)

func stripComments(src string) string {
	return lineCommentRe.ReplaceAllString(blockCommentRe.ReplaceAllString(src, " "), " ")
}

// userFacingProjections are routes whose response is rendered to a human in
// the console. Each value is the substring that must appear in web/src for the
// route to count as consumed — usually the path itself, or a distinctive
// fragment of it when the console builds the URL from a template.
//
// NOT in scope, on purpose: relay endpoints (/v1/**, consumed by customer code
// and not by our console), /internal/** (service-to-service, X-API-Key),
// health/metrics (scraped), and anything whose only caller is another backend.
var userFacingProjections = map[string]string{
	"GET /api/status":         "/api/status",
	"GET /api/data/self/":     "/api/data/self",
	"GET /api/uptime/status":  "/api/uptime/status",
	"GET /api/task/self/":     "/api/task/self",
	"GET /api/mj/self/":       "/api/mj/self",
	"PUT /api/user/setting":   "/api/user/setting",
	"GET /api/user/self":      "/api/user/self",
	"GET /api/channel/":       "/api/channel",
	"GET /api/token/":         "/api/token",
	"GET /api/log/self":       "/api/log/self",
	"GET /api/redemption/":    "/api/redemption",
	"GET /api/pricing":        "/api/pricing",
	"POST /api/verify":        "/api/verify",
	"chat send (v2)":          "/chat/send",
	"chat sessions (v2)":      "/chat/sessions",
	"tenant logs (v2)":        "/logs",
	"tenant tokens (v2)":      "/tokens",
	"tenant channels (v2)":    "/channels",
	"tenant models (v2)":      "/models",
	"tenant billing (v2)":     "/billing",
	"tenant analytics (v2)":   "/analytics/",
	"admin audit (v2)":        "/admin/audit",
	"admin users (v2)":        "/admin/users",
	"admin authz grants (v2)": "/admin/authz/grants",
	"admin model-allowlist":   "model-allowlist",
	"admin model-limits":      "model-limits",
	"admin routing affinity":  "/admin/routing/affinity",
	"admin totp stats":        "/admin/security/totp-stats",
	"admin system tasks":      "/admin/system/tasks",
	"admin rankings (v2)":     "/admin/analytics/rankings",
	"admin model performance": "/admin/analytics/model-performance",
	"admin gateway health":    "/admin/gateway",
	"admin cost intelligence": "/admin/cost",
	"admin tenants (v2)":      "/admin/tenants",
	"projects (v2)":           "/projects",
	"credit pool (v2)":        "/credit-pool",
}

// noConsoleConsumer records a user-facing route deliberately without a console
// surface. The reason must name who is meant to consume it instead; "not yet"
// is not a reason, it is this cycle's defect.
var noConsoleConsumer = map[string]string{
	// The v1 redemption listing/search pair. Its console shell was retired on
	// 2026-09-07 in favour of the tenant-scoped v2 surface
	// (/api/v2/:tenant_slug/redemptions), which the v2 Redemption page reads.
	// The v1 routes stay registered for scripted admin clients that predate v2;
	// they are an API surface, not a console one.
	"GET /api/redemption/": "superseded in the console by the v2 tenant-scoped " +
		"redemptions endpoint; kept as a v1 API surface for scripted clients",
}

// TestConsoleReadsEveryUserFacingEndpoint fails when a route in
// userFacingProjections has no reference anywhere under web/src.
func TestConsoleReadsEveryUserFacingEndpoint(t *testing.T) {
	webSrc := filepath.Join("..", "..", "..", "..", "web", "src")
	if _, err := os.Stat(webSrc); err != nil {
		t.Skipf("console sources not present at %s: %v", webSrc, err)
	}

	var blob strings.Builder
	files := 0
	err := filepath.Walk(webSrc, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "node_modules" || info.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".js", ".jsx", ".ts", ".tsx":
		default:
			return nil
		}
		// Test files do not count as a consumer: a page can be deleted while
		// its test keeps the string alive, which is exactly how a surface
		// disappears without this gate noticing.
		base := filepath.Base(path)
		if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		blob.WriteString(stripComments(string(b)))
		blob.WriteString("\n")
		files++
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", webSrc, err)
	}
	// Guard the guard: an empty corpus would make every lookup below vacuous.
	if files < 100 {
		t.Fatalf("scanned only %d console source files under %s — the walk did not find the console", files, webSrc)
	}

	corpus := blob.String()
	var missing []string
	for name, needle := range userFacingProjections {
		if _, excused := noConsoleConsumer[name]; excused {
			continue
		}
		if !strings.Contains(corpus, needle) {
			missing = append(missing, name+" (looked for "+needle+")")
		}
	}
	if len(missing) > 0 {
		t.Errorf("user-facing routes with no console consumer — wire one, or add the route to "+
			"noConsoleConsumer with a reason naming its intended consumer:\n  %s",
			strings.Join(missing, "\n  "))
	}

	// An excuse for a route that is not in the list at all is dead weight and
	// hides the fact that nothing is checking it.
	for name := range noConsoleConsumer {
		if _, listed := userFacingProjections[name]; !listed {
			t.Errorf("noConsoleConsumer excuses %q, which is not in userFacingProjections — remove the excuse or add the route", name)
		}
	}
}
