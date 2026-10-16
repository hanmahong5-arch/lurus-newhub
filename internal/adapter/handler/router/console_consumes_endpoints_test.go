package router

// The reverse of frontend_route_contract_test.go. That one catches a console
// control calling a route the server does not register. This one catches the
// opposite and, as of cycle 10, more common failure: the server ships a route
// that projects data to a person, and no console surface ever reads it.
//
// "Reads" means an actual HTTP call: the corpus is the set of URLs the
// console's call sites resolve to (the extractor in
// frontend_route_contract_test.go, over web/next/src), not a text search, so a
// path that survives only in a string constant, a snippet or a type does not
// count as a consumer. This is stricter than the grep the classic console was
// held to, and it follows constants and helpers (`${BASE}/grants`).
//
// Measured 2026-09-16, before this gate existed (against the classic console):
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
	"sort"
	"strings"
	"testing"
)

// userFacingProjections are routes whose response is rendered to a human in
// the console. Each value is the substring that must appear in the resolved
// URL of some web/next/src call site for the route to count as consumed —
// usually the path itself, or a distinctive fragment of it.
//
// NOT in scope, on purpose: relay endpoints (/v1/**, consumed by customer code
// and not by our console), /internal/** (service-to-service, X-API-Key),
// health/metrics (scraped), and anything whose only caller is another backend.
var userFacingProjections = map[string]string{
	"GET /api/status":             "/api/status",
	"GET /api/data/self/":         "/api/data/self",
	"GET /api/uptime/status":      "/api/uptime/status",
	"GET /api/task/self/":         "/api/task/self",
	"GET /api/mj/self/":           "/api/mj/self",
	"PUT /api/user/setting":       "/api/user/setting",
	"GET /api/user/self":          "/api/user/self",
	"GET /api/channel/":           "/api/channel",
	"GET /api/token/":             "/api/token",
	"GET /api/log/self":           "/api/log/self",
	"GET /api/redemption/":        "/api/redemption",
	"GET /api/pricing":            "/api/pricing",
	"POST /api/verify":            "/api/verify",
	"chat send (v2)":              "/chat/send",
	"chat sessions (v2)":          "/chat/sessions",
	"tenant logs (v2)":            "/logs",
	"tenant tokens (v2)":          "/tokens",
	"tenant channels (v2)":        "/channels",
	"tenant models (v2)":          "/models",
	"tenant models routable (v2)": "/models/routable",
	"tenant billing (v2)":         "/billing",
	"tenant analytics (v2)":       "/analytics/",
	"admin audit (v2)":            "/admin/audit",
	"admin users (v2)":            "/admin/users",
	"admin authz grants (v2)":     "/admin/authz/grants",
	"admin model-allowlist":       "model-allowlist",
	"admin model-limits":          "model-limits",
	"admin routing affinity":      "/admin/routing/affinity",
	"admin totp stats":            "/admin/security/totp-stats",
	"admin system tasks":          "/admin/system/tasks",
	"admin rankings (v2)":         "/admin/analytics/rankings",
	"admin model performance":     "/admin/analytics/model-performance",
	"admin gateway health":        "/admin/gateway/health",
	"admin cost intelligence":     "/admin/governance/savings",
	"admin tenants (v2)":          "/admin/tenants",
	"projects (v2)":               "/projects",
	"credit pool (v2)":            "/credit-pool",
	"tenant invites (v2)":         "/invites",
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
	// The v1 per-user log listing. Its only console surface was the legacy
	// components/table/usage-logs grid (and hooks/usage-logs behind it),
	// deleted in cycle 12 L3 after an enumeration found no importer outside
	// that subtree. The new console's usage-logs feature reads the
	// tenant-scoped /api/v2/:tenant_slug/logs instead
	// (web/next/src/features/usage-logs/api.ts).
	// The v1 route stays registered for scripted API clients that predate v2.
	"GET /api/log/self": "superseded in the console by the v2 tenant-scoped " +
		"logs endpoint; kept as a v1 API surface for scripted clients",
	// The v1 token listing. The new console's API-keys feature
	// (web/next/src/features/keys/api.ts) manages tokens through the
	// tenant-scoped /api/v2/:tenant_slug/tokens routes only; no call in
	// web/next/src reaches /api/token. (The classic console's check passed on
	// the substring alone.) Kept as a v1 API surface for scripted clients.
	"GET /api/token/": "superseded in the console by the v2 tenant-scoped " +
		"tokens endpoints (features/keys); kept as a v1 API surface for scripted clients",
}

// consumedBy reports whether some URL in corpus (one per line) contains needle
// ending on a path-segment boundary: the needle must be followed by the end of
// the URL or a '/', unless it ends in '/' itself. A bare substring match would
// let a renamed route ("/chat/sendx") keep satisfying the needle "/chat/send".
func consumedBy(corpus, needle string) bool {
	for off := 0; ; {
		i := strings.Index(corpus[off:], needle)
		if i < 0 {
			return false
		}
		end := off + i + len(needle)
		if strings.HasSuffix(needle, "/") || end == len(corpus) ||
			corpus[end] == '\n' || corpus[end] == '/' {
			return true
		}
		off += i + 1
	}
}

func TestConsumedByRespectsSegmentBoundaries(t *testing.T) {
	cases := []struct {
		corpus, needle string
		want           bool
	}{
		{"/api/v2/~/chat/send", "/chat/send", true},
		{"/api/v2/~/chat/sendx", "/chat/send", false},
		{"/api/v2/~/chat/sendx\n/api/v2/~/chat/send\n/x", "/chat/send", true},
		{"/api/data/self/", "/api/data/self", true},
		{"/api/v2/~/analytics/rankings", "/analytics/", true},
		{"/api/v2/admin/tenants/{}/model-limits", "model-limits", true},
		{"/api/v2/admin/tenants/{}/model-limitsx", "model-limits", false},
		{"/api/v2/~/models/routable", "/models", true},
		{"/api/v2/~/modelsx", "/models", false},
	}
	for _, tc := range cases {
		if got := consumedBy(tc.corpus, tc.needle); got != tc.want {
			t.Errorf("consumedBy(%q, %q) = %v, want %v", tc.corpus, tc.needle, got, tc.want)
		}
	}
}

// minConsoleSourceFiles guards the guard: an empty corpus would make every
// lookup below vacuous. web/next/src holds ~820 shipped source files.
const minConsoleSourceFiles = 400

// minConsumerCalls: the resolved-call corpus this gate searches. The real
// console resolves ~230 call sites; see minConsoleCalls in
// frontend_route_contract_test.go.
const minConsumerCalls = 165

// TestConsoleReadsEveryUserFacingEndpoint fails when a route in
// userFacingProjections has no resolved call under web/next/src.
func TestConsoleReadsEveryUserFacingEndpoint(t *testing.T) {
	sources := loadConsoleSources(t)
	if len(sources) < minConsoleSourceFiles {
		t.Fatalf("scanned only %d console source files under %s — the walk did not find the console",
			len(sources), consoleSrcRoot())
	}

	calls := extractCalls(sources)
	var urls []string
	for _, c := range calls {
		urls = append(urls, c.paths...)
	}
	if len(calls) < minConsumerCalls {
		t.Fatalf("extracted only %d console calls (floor %d) — the extractor stopped recognising a call shape",
			len(calls), minConsumerCalls)
	}
	// Wildcard segments print as "{}" so a needle never has to know the byte.
	corpus := strings.ReplaceAll(strings.Join(urls, "\n"), interpolatedSeg, "{}")

	var missing []string
	for name, needle := range userFacingProjections {
		if _, excused := noConsoleConsumer[name]; excused {
			continue
		}
		if !consumedBy(corpus, needle) {
			missing = append(missing, name+" (looked for "+needle+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
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

// TestConsumerCorpusIsCallsNotMentions pins the property the header claims: a
// URL that only appears as a bare string (not a call argument) is not a
// consumer.
func TestConsumerCorpusIsCallsNotMentions(t *testing.T) {
	calls := extractCalls(map[string]string{
		"features/x/api.ts": "const UNUSED = '/api/never/called'\nconst NOTE = 'see /api/also/not/called'\napi.get('/api/real')",
	})
	var urls []string
	for _, c := range calls {
		urls = append(urls, c.paths...)
	}
	got := strings.Join(urls, ",")
	if got != "/api/real" {
		t.Fatalf("corpus = %q, want only the called URL", got)
	}
}
