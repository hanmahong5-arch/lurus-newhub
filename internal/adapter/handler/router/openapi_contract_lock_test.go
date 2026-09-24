package router

// openapi_contract_lock_test.go — L3-CONTRACT-TAXONOMY's DOC-1 closer: a Go
// test that fails the moment a published spec under docs/openapi/ drifts from
// either the real route table (engine.Routes(), built the same way
// cov_router-relay_wiring_test.go's SetRouter tests do) or the ErrorCode
// constants in internal/pkg/types — in EITHER direction, so a doc that
// invents an endpoint/code fails just as loudly as one that goes stale.
//
// Locks (b)-(f) are relay.json-specific. Lock (a) runs over every spec in
// contractLockMountedSpecs (relay.json AND api-v2.json since 2026-09-21);
// (g)-(i) are spec-wide.
//
// BLIND SPOT of this whole file: it compares path templates, method names,
// header names, enum members and server URLs. It never sends a request through
// a documented operation, so it cannot tell you that a documented request body,
// response body or status code matches what the handler really emits. Two of
// the defects this file's own extension found — GET /models documented with the
// OpenAI /v1/models envelope it does not use, and the redeem operation
// documenting a "quota" field the handler never writes — were invisible to it
// and had to be read out of the handlers by hand.
//
// (a) every documented path+method exists in the real route table, for every
//     spec in contractLockMountedSpecs. The per-spec operation-count floor
//     that used to ride along here is now its own test, over EVERY published
//     spec (TestOpenAPIContract_SpecOperationFloors).
// (b) the 200 response on the four chat-class paths documents X-Request-Id.
// (c) every documented 429 documents Retry-After and X-RateLimit-Scope.
// (d) /v1/key and /v1/generation are documented.
// (e) the GatewayError.code enum and the ErrorCode = "..." literals in
//     internal/pkg/types (non-test sources) match exactly, bidirectionally.
// (f) x-lurus-enforcement-order equals the ten-stage relay gate order.
// (g) api-v2.json and api-v2.yaml document the same operation set.
// (h) every published spec names the production host as its first server.
// (i) every mounted route in each group listed in
//     contractLockFullyDocumentedGroups (the v2 tenant billing group and the
//     /pg playground group) is documented — the only route groups where the
//     "mounted but undocumented" direction is closed.
// (j) every tag api-v2.json declares is used, and every tag it uses is declared.
// (k) the v2 rate-limit paragraph in both api-v2 twins states the budget,
//     window and keying dimension GlobalV2RateLimit really enforces.
// (l) api.json states, in its own bytes, how many of its path templates are
//     NOT mounted — and that number is recomputed from the real router here.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/config"
	"github.com/LurusTech/lurus-hub/web"

	"github.com/gin-gonic/gin"
)

// repoRootFromRouterPkg mirrors frontend_route_contract_test.go's convention:
// this package lives at internal/adapter/handler/router, four levels below
// the repo root.
func repoRootFromRouterPkg() string {
	return filepath.Join("..", "..", "..", "..")
}

// openapiSpecDir is docs/openapi/ relative to this package.
func openapiSpecDir() string {
	return filepath.Join(repoRootFromRouterPkg(), "docs", "openapi")
}

// loadOpenAPIJSONDoc parses one published spec EXACTLY as it is served: the
// bytes on disk go straight into encoding/json, with nothing stripped or
// rewritten first.
//
// Until 2026-09-21 this helper ran bytes.TrimPrefix(raw, {0xEF,0xBB,0xBF})
// before parsing. relay.json and api.json were therefore allowed to carry a
// UTF-8 byte order mark that a standard JSON parser — encoding/json included
// — rejects outright, and every lock in this file stayed green on bytes no
// consumer could read. A consumer does not get to strip anything, so neither
// does the lock.
func loadOpenAPIJSONDoc(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join(openapiSpecDir(), name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s as published (no BOM stripping, no preprocessing): %v", path, err)
	}
	return doc
}

// loadRelayOpenAPIDoc is the relay.json shorthand locks (b)-(f) use.
func loadRelayOpenAPIDoc(t *testing.T) map[string]any {
	t.Helper()
	return loadOpenAPIJSONDoc(t, "relay.json")
}

// buildContractLockEngine mirrors TestSetRouter_MetricsEndpoint_RejectsPublicScraper's
// setup: SetRouter is the same full wiring function that runs in production,
// so a route that exists in relay.json but was never mounted (or vice versa)
// is caught here, not just in a hand-picked subset.
func buildContractLockEngine(t *testing.T) *gin.Engine {
	t.Helper()
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })
	prevMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = prevMaster })
	prevEnv, hadEnv := os.LookupEnv("FRONTEND_BASE_URL")
	_ = os.Unsetenv("FRONTEND_BASE_URL")
	t.Cleanup(func() {
		if hadEnv {
			_ = os.Setenv("FRONTEND_BASE_URL", prevEnv)
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRouter(engine, web.BuildFS, web.IndexPage)
	return engine
}

var openapiBraceParam = regexp.MustCompile(`\{([^{}]+)\}`)

// ginPathFromOpenAPI converts an OpenAPI path template ({tenant_slug}) into
// gin's own (:tenant_slug) so the two route tables compare directly.
func ginPathFromOpenAPI(p string) string {
	return openapiBraceParam.ReplaceAllString(p, ":$1")
}

var contractLockHTTPMethods = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH",
}

// contractLockMountedSpecs lists the published JSON specs whose every
// documented path+method must exist on the real router.
//
// api.json is deliberately NOT here. It is the frozen v1 legacy surface, 157
// operations that were never reconciled against this router; adding it would
// turn one reviewable red into a hundred unreviewed ones. Its bytes are still
// gated — openapi_strict_parse_test.go parses every spec in docs/openapi/.
var contractLockMountedSpecs = []string{"relay.json", "api-v2.json"}

// contractLockOperationFloors is a per-spec "you may not publish less than
// this" ratchet, checked by TestOpenAPIContract_SpecOperationFloors over EVERY
// published spec — api.json included, which is why README.md may say the lock
// notices an operation being deleted from a spec without naming exceptions.
// Raise an entry when a spec legitimately grows; lowering one is the reviewable
// act of publishing less than we published before.
//
// BLIND SPOT, stated plainly because the previous wording oversold it: the
// floor catches an UNCOMPENSATED deletion only. Delete one operation and add a
// different one in the same edit and the count is unchanged — lock (a) passes
// (both are real routes), and the swap is visible only to lock (g), and only
// for api-v2, because relay.json and api.json have no twin. For those two that
// combination is invisible to every gate in this file.
//
// It also does NOT catch "mounted but undocumented" — see
// contractLockFullyDocumentedGroups for the route groups where that direction
// is closed structurally.
var contractLockOperationFloors = map[string]int{
	"relay.json":  46,
	"api-v2.json": 42,
	"api.json":    157,
}

// TestOpenAPIContract_SpecOperationFloors is the deletion half of lock (a),
// split out of it on 2026-09-21 repair so it can cover api.json too. api.json
// is not reconciled against the router (see contractLockMountedSpecs), but
// "someone deleted a hundred published operations" is checkable without any
// router knowledge at all, and before this split it was not checked: a scratch
// copy with 131 -> 31 path objects still passed every test in this package.
func TestOpenAPIContract_SpecOperationFloors(t *testing.T) {
	if len(contractLockOperationFloors) == 0 {
		t.Fatal("contractLockOperationFloors is empty — this lock would pass vacuously")
	}
	for _, spec := range publishedJSONSpecs(t) {
		floor, ok := contractLockOperationFloors[spec]
		if !ok {
			t.Errorf("%s is published under docs/openapi/ but has no entry in contractLockOperationFloors — add one (its current operation count) so deleting from it is caught", spec)
			continue
		}
		t.Run(spec, func(t *testing.T) {
			ops := documentedOperations(t, loadOpenAPIJSONDoc(t, spec), spec)
			if len(ops) < floor {
				t.Errorf("%s documents %d operation(s), floor is %d — %d operation(s) were deleted from the published spec", spec, len(ops), floor, floor-len(ops))
			}
			t.Logf("%s: %d operation(s), floor %d", spec, len(ops), floor)
		})
	}
}

// realRouteTable returns the mounted "METHOD /gin/path" set plus the
// "METHOD /prefix" list for gin catch-all routes.
//
// wildcardPrefixes exists because the /v1beta vendor REST convention encodes an
// action into the model segment (models/{model}:generateContent) that gin
// cannot bind as a named param (the literal ":" isn't a segment boundary), so
// relay-router.go mounts it as POST /v1beta/models/*path instead. The doc keeps
// the human/SDK-facing {model}:generateContent template, which IS that
// vendor's real wire shape; this is the one legitimate
// named-param-vs-wildcard translation, not a stale doc.
func realRouteTable(t *testing.T, engine *gin.Engine) (map[string]bool, []string) {
	t.Helper()
	registered := map[string]bool{}
	var wildcardPrefixes []string
	for _, rt := range engine.Routes() {
		registered[rt.Method+" "+rt.Path] = true
		if idx := indexOfWildcard(rt.Path); idx >= 0 {
			wildcardPrefixes = append(wildcardPrefixes, rt.Method+" "+rt.Path[:idx])
		}
	}
	if len(registered) == 0 {
		t.Fatal("engine.Routes() is empty — SetRouter wiring is broken, not the doc")
	}
	return registered, wildcardPrefixes
}

// documentedOperations returns the "METHOD /doc/{template}/path" set of a
// parsed OpenAPI 3 document.
func documentedOperations(t *testing.T, doc map[string]any, specName string) map[string]bool {
	t.Helper()
	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatalf("%s has no paths object — parser or fixture is broken", specName)
	}
	ops := map[string]bool{}
	for docPath, v := range paths {
		methods, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for m := range methods {
			upper, isMethod := contractLockHTTPMethods[m]
			if !isMethod {
				continue
			}
			ops[upper+" "+docPath] = true
		}
	}
	if len(ops) == 0 {
		t.Fatalf("%s: scanned zero documented operations — the doc walk is broken", specName)
	}
	return ops
}

// TestOpenAPIContract_PathsExistInRealRouteTable is lock (a), now run over
// every spec in contractLockMountedSpecs rather than relay.json alone.
// api-v2.json joined on 2026-09-21: it had been advertising seven v2 routes
// nobody ever mounted — two subscription reads, a subscription cancel, a
// subscribe, POST /billing/topup (a money path deliberately NOT restored,
// api-v2-router.go:357), and a tenant config pair — plus a redeem route under
// the wrong collection.
//
// The operation-count floor that used to live in this loop moved to
// TestOpenAPIContract_SpecOperationFloors, which runs over every published
// spec rather than only the reconciled ones.
func TestOpenAPIContract_PathsExistInRealRouteTable(t *testing.T) {
	engine := buildContractLockEngine(t)
	registered, wildcardPrefixes := realRouteTable(t, engine)

	for _, spec := range contractLockMountedSpecs {
		t.Run(spec, func(t *testing.T) {
			doc := loadOpenAPIJSONDoc(t, spec)
			ops := documentedOperations(t, doc, spec)

			var missing []string
			for op := range ops {
				sep := strings.IndexByte(op, ' ')
				key := op[:sep+1] + ginPathFromOpenAPI(op[sep+1:])
				if registered[key] {
					continue
				}
				matched := false
				for _, prefix := range wildcardPrefixes {
					if strings.HasPrefix(key, prefix) {
						matched = true
						break
					}
				}
				if !matched {
					missing = append(missing, key+" (documented as "+op+")")
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("%s: %d documented path(s) not present in the real route table:\n%s",
					spec, len(missing), joinLines(missing))
			}
			t.Logf("%s: %d documented operation(s) checked against %d mounted route(s)", spec, len(ops), len(registered))
		})
	}
}

// TestOpenAPIContract_ChatClassPathsDocumentRequestId is lock (b).
func TestOpenAPIContract_ChatClassPathsDocumentRequestId(t *testing.T) {
	doc := loadRelayOpenAPIDoc(t)
	paths := doc["paths"].(map[string]any)

	chatClass := []string{"/v1/chat/completions", "/v1/messages", "/v1/completions", "/v1/embeddings"}
	for _, p := range chatClass {
		t.Run(p, func(t *testing.T) {
			op, ok := paths[p].(map[string]any)
			if !ok {
				t.Fatalf("%s not documented at all", p)
			}
			post, ok := op["post"].(map[string]any)
			if !ok {
				t.Fatalf("%s has no post operation", p)
			}
			responses, ok := post["responses"].(map[string]any)
			if !ok {
				t.Fatalf("%s post has no responses", p)
			}
			resp200, ok := responses["200"].(map[string]any)
			if !ok {
				t.Fatalf("%s post has no 200 response", p)
			}
			headers, ok := resp200["headers"].(map[string]any)
			if !ok {
				t.Fatalf("%s 200 response has no headers object", p)
			}
			if _, ok := headers["X-Request-Id"]; !ok {
				t.Errorf("%s 200 response does not document X-Request-Id", p)
			}
		})
	}
}

// TestOpenAPIContract_429sDocumentRetryAfterAndScope is lock (c). Since
// 2026-09-21 repair it iterates contractLockMountedSpecs, the same list lock
// (a) walks, instead of relay.json alone: the L7 lane brought api-v2.json
// under lock (a) but left this one relay-only, and then authored the first
// 429 in api-v2.json (POST /{tenant_slug}/redeem) — which therefore became the
// only documented 429 in the repository exempt from the house rule, and was
// published with no headers block at all while middleware.RedemptionRateLimit
// sends five of them.
//
// The minDocumented429s floor stays relay-specific: it names four relay paths.
func TestOpenAPIContract_429sDocumentRetryAfterAndScope(t *testing.T) {
	if len(contractLockMountedSpecs) == 0 {
		t.Fatal("contractLockMountedSpecs is empty — this lock would pass vacuously")
	}
	for _, spec := range contractLockMountedSpecs {
		t.Run(spec, func(t *testing.T) {
			assert429sDocumentRateLimitHeaders(t, spec)
		})
	}
}

// assert429sDocumentRateLimitHeaders is lock (c)'s per-spec body.
// No t.Helper() here on purpose: it would re-attribute every t.Errorf below
// to the one-line call site in the loop above, so all six distinct checks
// would report the same file:line.
func assert429sDocumentRateLimitHeaders(t *testing.T, spec string) {
	doc := loadOpenAPIJSONDoc(t, spec)
	paths := doc["paths"].(map[string]any)

	found := 0
	for docPath, v := range paths {
		methods, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for method, opAny := range methods {
			if _, isMethod := contractLockHTTPMethods[method]; !isMethod {
				continue
			}
			op, ok := opAny.(map[string]any)
			if !ok {
				continue
			}
			responses, ok := op["responses"].(map[string]any)
			if !ok {
				continue
			}
			resp429, ok := responses["429"].(map[string]any)
			if !ok {
				continue
			}
			found++
			headers, ok := resp429["headers"].(map[string]any)
			if !ok {
				t.Errorf("%s %s: 429 response has no headers object", method, docPath)
				continue
			}
			if _, ok := headers["Retry-After"]; !ok {
				t.Errorf("%s %s: 429 response does not document Retry-After", method, docPath)
			}
			if _, ok := headers["X-RateLimit-Scope"]; !ok {
				t.Errorf("%s %s: 429 response does not document X-RateLimit-Scope", method, docPath)
			}
			// L6: this doc's per-path 429 entry documents the header set
			// the rate-limit and concurrency middlewares on the relay chain
			// write (BusinessRateLimit and BusinessModelRateLimit,
			// RelayConcurrencyLimit, ModelRequestRateLimit - scopes
			// token/tenant/model/user; the ip and key scopes belong to the
			// keyed limiters guarding /api/* and /internal/*, which no /v1
			// route mounts): X-RateLimit-Limit/-Remaining
			// alongside Scope/Type, via setRateLimitResponseHeaders (Remaining is
			// 0 on a reject). It does NOT hold for every 429 origin on the same
			// path - the entitlement gate (account/quota, entitlement.go:132-133)
			// and the cost-spike fuse (user/cost, cost_spike.go:121-122) write
			// only Scope/Type and no Retry-After/Limit/Remaining; relay.json's
			// description says so. Reset is deliberately NOT required here:
			// bizReject clears it (business_rate_limit.go) because a reject
			// carries Retry-After, not a window reset.
			if _, ok := headers["X-RateLimit-Limit"]; !ok {
				t.Errorf("%s %s: 429 response does not document X-RateLimit-Limit", method, docPath)
			}
			if _, ok := headers["X-RateLimit-Remaining"]; !ok {
				t.Errorf("%s %s: 429 response does not document X-RateLimit-Remaining", method, docPath)
			}
		}
	}
	// L3-CONTRACT-TAXONOMY residual item 1: /v1/chat/completions, /v1/completions
	// and /v1/embeddings must carry the same 429 (and 401/402/403/404/413/503)
	// rows /v1/messages already had, so at least those four chat-class paths
	// document a 429 with Retry-After + Scope/Type. Relay-only, because it
	// names relay paths; the other specs get the floor below instead, which
	// only says "the walk found something to check".
	if spec == "relay.json" {
		const minDocumented429s = 4
		if found < minDocumented429s {
			t.Fatalf("relay.json: scanned %d documented 429 response(s), want at least %d (one each for /v1/chat/completions, /v1/completions, /v1/embeddings, /v1/messages) — a chat-class path lost its 429 documentation", found, minDocumented429s)
		}
	} else if found == 0 {
		// Scanner-honesty floor for every other spec: a spec under lock (a)
		// that documents no 429 at all means either the walk broke or the
		// spec stopped documenting a limit its routes really enforce. Every
		// /api/v2 route sits behind middleware.GlobalV2RateLimit
		// (api-v2-router.go:45), so "no 429 anywhere" is never the truth here.
		t.Errorf("%s documents zero 429 response(s) — every route in it sits behind GlobalV2RateLimit, so either the scan broke or the spec hides a limit the server enforces", spec)
	}
	t.Logf("%s: 429 responses checked: %d", spec, found)
}

// TestOpenAPIContract_KeyHolderEndpointsDocumented is lock (d).
func TestOpenAPIContract_KeyHolderEndpointsDocumented(t *testing.T) {
	doc := loadRelayOpenAPIDoc(t)
	paths := doc["paths"].(map[string]any)
	for _, p := range []string{"/v1/key", "/v1/generation"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("%s is not documented in relay.json", p)
		}
	}
}

// TestOpenAPIContract_ErrorCodeEnumMatchesTypesPackage is lock (e): the
// GatewayError.code enum in relay.json and the ErrorCode = "..." literals in
// internal/pkg/types (non-test .go files) must match exactly, bidirectionally.
func TestOpenAPIContract_ErrorCodeEnumMatchesTypesPackage(t *testing.T) {
	doc := loadRelayOpenAPIDoc(t)
	schemas, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)
	if !ok {
		t.Fatal("components.schemas missing")
	}
	gatewayError, ok := schemas["GatewayError"].(map[string]any)
	if !ok {
		t.Fatal("components.schemas.GatewayError missing")
	}
	props, ok := gatewayError["properties"].(map[string]any)
	if !ok {
		t.Fatal("GatewayError.properties missing")
	}
	codeProp, ok := props["code"].(map[string]any)
	if !ok {
		t.Fatal("GatewayError.properties.code missing")
	}
	enumAny, ok := codeProp["enum"].([]any)
	if !ok || len(enumAny) == 0 {
		t.Fatal("GatewayError.properties.code.enum missing or empty")
	}
	docCodes := map[string]bool{}
	for _, e := range enumAny {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("non-string enum entry: %v", e)
		}
		docCodes[s] = true
	}

	typesDir := filepath.Join(repoRootFromRouterPkg(), "internal", "pkg", "types")
	entries, err := os.ReadDir(typesDir)
	if err != nil {
		t.Fatalf("read %s: %v", typesDir, err)
	}
	codeLiteral := regexp.MustCompile(`ErrorCode\s*=\s*"([^"]*)"`)
	codeCodes := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || len(name) > 8 && name[len(name)-8:] == "_test.go" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(typesDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range codeLiteral.FindAllStringSubmatch(string(src), -1) {
			codeCodes[m[1]] = true
		}
	}
	if len(codeCodes) == 0 {
		t.Fatal("scanned zero ErrorCode literals — the scan is broken, not the codebase clean")
	}

	t.Logf("error codes compared: %d in relay.json, %d in internal/pkg/types", len(docCodes), len(codeCodes))

	var docOnly, codeOnly []string
	for c := range docCodes {
		if !codeCodes[c] {
			docOnly = append(docOnly, c)
		}
	}
	for c := range codeCodes {
		if !docCodes[c] {
			codeOnly = append(codeOnly, c)
		}
	}
	sort.Strings(docOnly)
	sort.Strings(codeOnly)
	if len(docOnly) > 0 {
		t.Errorf("relay.json documents %d code(s) not present in internal/pkg/types: %v", len(docOnly), docOnly)
	}
	if len(codeOnly) > 0 {
		t.Errorf("internal/pkg/types defines %d code(s) not documented in relay.json: %v", len(codeOnly), codeOnly)
	}
}

// TestOpenAPIContract_EnforcementOrder is lock (f).
func TestOpenAPIContract_EnforcementOrder(t *testing.T) {
	doc := loadRelayOpenAPIDoc(t)
	raw, ok := doc["x-lurus-enforcement-order"].([]any)
	if !ok {
		t.Fatal("x-lurus-enforcement-order missing or not an array")
	}
	want := []string{
		"StampRelayFormat", "TokenAuth", "PoolBalanceCheck", "CostSpikeLimit",
		"EntitlementCheck", "ModelRequestRateLimit", "BusinessRateLimit",
		"RelayConcurrencyLimit", "Distribute", "BusinessModelRateLimit",
	}
	if len(raw) != len(want) {
		t.Fatalf("x-lurus-enforcement-order has %d entries, want %d: %v", len(raw), len(want), raw)
	}
	for i, w := range want {
		got, ok := raw[i].(string)
		if !ok || got != w {
			t.Errorf("x-lurus-enforcement-order[%d] = %v, want %q", i, raw[i], w)
		}
	}
}

// indexOfWildcard returns the index of the "/*" catch-all marker in a gin
// route path, or -1 if the path has none.
func indexOfWildcard(ginPath string) int {
	for i := 0; i < len(ginPath)-1; i++ {
		if ginPath[i] == '/' && ginPath[i+1] == '*' {
			return i
		}
	}
	return -1
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += "  " + l + "\n"
	}
	return out
}

// TestOpenAPIContract_DocumentedResponseHeadersAreCORSExposed is L6's new
// lock: every header name documented under any response's "headers" object
// in relay.json must actually reach Access-Control-Expose-Headers on a real
// request through the real middleware.CORS() middleware — not just be
// listed in the middleware.CORSExposedHeaders Go slice, which a deleted
// `corsConfig.ExposeHeaders = CORSExposedHeaders` assignment (cors.go)
// would leave populated while the wire response carried nothing. A header
// the docs promise but the response never exposes is invisible to a
// browser SDK reading response.headers.get(...) — a silent trap, not a
// real contract. The reverse (exposed but undocumented) is fine: not every
// internal header rises to public API.
func TestOpenAPIContract_DocumentedResponseHeadersAreCORSExposed(t *testing.T) {
	doc := loadRelayOpenAPIDoc(t)
	paths := doc["paths"].(map[string]any)

	cfg := config.Get()
	prevOrigins := cfg.CORS.AllowedOrigins
	cfg.CORS.AllowedOrigins = []string{"https://contract-lock.example"}
	t.Cleanup(func() { cfg.CORS.AllowedOrigins = prevOrigins })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.CORS())
	engine.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://contract-lock.example")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	exposed := map[string]bool{}
	for _, h := range strings.Split(w.Header().Get("Access-Control-Expose-Headers"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			exposed[h] = true
		}
	}
	if len(exposed) == 0 {
		t.Fatal("Access-Control-Expose-Headers is empty on a real CORS()-mounted request from an allowed origin — the scan has nothing real to compare against")
	}

	documented := map[string]bool{}
	for _, v := range paths {
		methods, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for method, opAny := range methods {
			if _, isMethod := contractLockHTTPMethods[method]; !isMethod {
				continue
			}
			op, ok := opAny.(map[string]any)
			if !ok {
				continue
			}
			responses, ok := op["responses"].(map[string]any)
			if !ok {
				continue
			}
			for _, respAny := range responses {
				resp, ok := respAny.(map[string]any)
				if !ok {
					continue
				}
				headers, ok := resp["headers"].(map[string]any)
				if !ok {
					continue
				}
				for h := range headers {
					documented[strings.ToLower(h)] = true
				}
			}
		}
	}

	// Scanner-honesty floor: relay.json documents well over a dozen distinct
	// response headers today; a broken scan that walked zero paths would
	// otherwise vacuously pass.
	const minDocumentedHeaders = 5
	if len(documented) < minDocumentedHeaders {
		t.Fatalf("scanned %d distinct documented response header name(s), want at least %d — the scan is broken, not the doc thin", len(documented), minDocumentedHeaders)
	}

	var missing []string
	for h := range documented {
		if !exposed[h] {
			missing = append(missing, h)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("relay.json documents %d response header(s) not in middleware.CORSExposedHeaders: %v", len(missing), missing)
	}
}

// rateLimitScopeLiteral / rateLimitTypeLiteral / bizRejectLiterals /
// ccRejectLiteral scan middleware/*.go non-test sources for the literal
// scope/type strings a reject site actually writes on the wire.
//
// bizRejectVarScopeLiteralType additionally catches bizTPMAdmit's own
// bizReject(c, scope, "tpm", ...) call (business_rate_limit.go): scope
// there is the enclosing function's own parameter, not a quoted literal, so
// bizRejectLiterals (which requires BOTH args quoted) never matches it —
// without this second pattern "tpm" was never scanned even though it is a
// real X-RateLimit-Type value on the wire.
//
// rateLimitScopeForIdentReturnLiteral catches the "ip"/"key" values
// rate-limit.go's rateLimitScopeForIdent computes and returns dynamically
// (rate-limit.go:77/:98 call Set("X-RateLimit-Scope", rateLimitScopeForIdent(ident))
// — a function call, not a quoted literal, so rateLimitScopeLiteral never
// matches those two sites either); it reads the return literals straight out
// of that one function's body instead of guessing from the call sites.
var (
	rateLimitScopeLiteral               = regexp.MustCompile(`Set\("X-RateLimit-Scope",\s*"([^"]+)"\)`)
	rateLimitTypeLiteral                = regexp.MustCompile(`Set\("X-RateLimit-Type",\s*"([^"]+)"\)`)
	bizRejectLiterals                   = regexp.MustCompile(`bizReject\(c,\s*"([^"]+)",\s*"([^"]+)"`)
	bizRejectVarScopeLiteralType        = regexp.MustCompile(`bizReject\(c,\s*\w+,\s*"([^"]+)"`)
	ccRejectLiteral                     = regexp.MustCompile(`ccReject\(c,\s*"([^"]+)"`)
	rateLimitScopeForIdentFunc          = regexp.MustCompile(`(?s)func rateLimitScopeForIdent\([^)]*\)[^{]*\{(.*?)\n}`)
	rateLimitScopeForIdentReturnLiteral = regexp.MustCompile(`return "([^"]+)"`)
)

// TestOpenAPIContract_RateLimitScopeTypeEnumsMatchMiddleware is L6's new
// lock: every X-RateLimit-Scope/-Type value the reject sites in
// internal/adapter/middleware actually write — whether the call site passes
// a quoted literal directly (Set(...,"token") / bizReject(c,"tenant","rpm")
// / ccReject(c,"token")) or a variable whose value this scan can still pin
// to a literal (bizTPMAdmit's bizReject(c, scope, "tpm", ...), and
// rateLimitScopeForIdent's own "ip"/"key" return statements) — must appear
// in relay.json's XRateLimitScope/XRateLimitType header descriptions, so
// the documented value set cannot silently drift from what the code emits.
// A go/regexp scan over the real source files (not a hand-maintained list),
// same posture as TestOpenAPIContract_ErrorCodeEnumMatchesTypesPackage; it
// still cannot see a scope/type computed by logic this scan does not know
// about (e.g. a brand-new dynamic-value helper), only literals and the two
// named exceptions above.
func TestOpenAPIContract_RateLimitScopeTypeEnumsMatchMiddleware(t *testing.T) {
	middlewareDir := filepath.Join(repoRootFromRouterPkg(), "internal", "adapter", "middleware")
	entries, err := os.ReadDir(middlewareDir)
	if err != nil {
		t.Fatalf("read %s: %v", middlewareDir, err)
	}

	scopes := map[string]bool{}
	types := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(middlewareDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(src)
		for _, m := range rateLimitScopeLiteral.FindAllStringSubmatch(s, -1) {
			scopes[m[1]] = true
		}
		for _, m := range rateLimitTypeLiteral.FindAllStringSubmatch(s, -1) {
			types[m[1]] = true
		}
		for _, m := range bizRejectLiterals.FindAllStringSubmatch(s, -1) {
			scopes[m[1]] = true
			types[m[2]] = true
		}
		for _, m := range bizRejectVarScopeLiteralType.FindAllStringSubmatch(s, -1) {
			types[m[1]] = true
		}
		for _, m := range ccRejectLiteral.FindAllStringSubmatch(s, -1) {
			scopes[m[1]] = true
		}
		if fn := rateLimitScopeForIdentFunc.FindStringSubmatch(s); fn != nil {
			for _, m := range rateLimitScopeForIdentReturnLiteral.FindAllStringSubmatch(fn[1], -1) {
				scopes[m[1]] = true
			}
		}
	}
	if len(scopes) == 0 || len(types) == 0 {
		t.Fatalf("scanned 0 scope/type literal(s) (scopes=%d types=%d) — the scan is broken, not the codebase clean", len(scopes), len(types))
	}

	doc := loadRelayOpenAPIDoc(t)
	headers, ok := doc["components"].(map[string]any)["headers"].(map[string]any)
	if !ok {
		t.Fatal("components.headers missing")
	}
	scopeDesc := strings.ToLower(headerDescription(t, headers, "XRateLimitScope"))
	typeDesc := strings.ToLower(headerDescription(t, headers, "XRateLimitType"))

	var scopeList, typeList []string
	for s := range scopes {
		scopeList = append(scopeList, s)
	}
	for ty := range types {
		typeList = append(typeList, ty)
	}
	sort.Strings(scopeList)
	sort.Strings(typeList)
	t.Logf("scope literals: %v; type literals: %v", scopeList, typeList)

	for _, s := range scopeList {
		if !strings.Contains(scopeDesc, strings.ToLower(s)) {
			t.Errorf("X-RateLimit-Scope literal %q written by middleware is not in relay.json's XRateLimitScope description", s)
		}
	}
	for _, ty := range typeList {
		if !strings.Contains(typeDesc, strings.ToLower(ty)) {
			t.Errorf("X-RateLimit-Type literal %q written by middleware is not in relay.json's XRateLimitType description", ty)
		}
	}
}

// headerDescription reads components.headers.<name>.description as a string.
func headerDescription(t *testing.T, headers map[string]any, name string) string {
	t.Helper()
	h, ok := headers[name].(map[string]any)
	if !ok {
		t.Fatalf("components.headers.%s missing", name)
	}
	desc, ok := h["description"].(string)
	if !ok {
		t.Fatalf("components.headers.%s.description missing", name)
	}
	return desc
}

// openapiProductionServer is the public origin this service is actually served
// from. Confirmed against two independent sources rather than the README:
// deploy/r6-host-nginx/lurus-newhub.conf:18 (`server_name hub.lurus.cn;`,
// proxy_pass to the NodePort) and deploy/k8s/r6-stage/deployment.yaml, whose
// ALLOWED_ORIGINS / OIDC_REDIRECT_URI / OIDC_POST_LOGOUT_REDIRECT_URI all name
// https://hub.lurus.cn. The specs used to claim https://api.lurus.cn, a host
// retired with the lurus-api service in 2026-04.
const openapiProductionServer = "https://hub.lurus.cn"

// TestOpenAPIContract_SpecsDeclareProductionServer is lock (h). A spec whose
// servers array is empty (all three were, until 2026-09-21) gives an SDK
// generator and a Swagger UI reader no base URL at all: the generated client
// points at wherever the spec file happened to be fetched from.
//
// The spec list comes from publishedJSONSpecs (the directory walk in
// openapi_strict_parse_test.go), not from a literal in this file. It used to be
// `var openapiPublishedSpecs = []string{...}` — three names hardcoded beside a
// sibling gate whose own header advertises directory discovery "so a spec added
// tomorrow is covered the day it lands". A fourth spec would have had its bytes
// parsed and its server URL unchecked.
func TestOpenAPIContract_SpecsDeclareProductionServer(t *testing.T) {
	for _, spec := range publishedJSONSpecs(t) {
		t.Run(spec, func(t *testing.T) {
			doc := loadOpenAPIJSONDoc(t, spec)
			servers, ok := doc["servers"].([]any)
			if !ok || len(servers) == 0 {
				t.Fatalf("%s declares no servers — a consumer has no base URL", spec)
			}
			first, ok := servers[0].(map[string]any)
			if !ok {
				t.Fatalf("%s servers[0] is not an object: %v", spec, servers[0])
			}
			if got, _ := first["url"].(string); got != openapiProductionServer {
				t.Errorf("%s servers[0].url = %q, want %q", spec, got, openapiProductionServer)
			}
		})
	}
}

// yamlPathKey / yamlOperationKey drive the api-v2.yaml operation scan below.
var (
	yamlPathKey      = regexp.MustCompile(`^  (/\S*):\s*$`)
	yamlOperationKey = regexp.MustCompile(`^    (get|post|put|delete|patch|head|options|trace):\s*$`)
	yamlTopLevelKey  = regexp.MustCompile(`^[A-Za-z]`)
)

// yamlDocumentedOperations returns the "METHOD /template/path" set of a YAML
// OpenAPI document.
//
// BLIND SPOT: this is a line scan, not a YAML parser (gopkg.in/yaml.v3 is an
// indirect module dependency and this package must not promote it). It sees
// two-space path keys and four-space method keys and nothing else: an anchor,
// an alias, a flow-style paths map, or a multi-document file would all be
// invisible to it. The self-check floor below is what stops a scan that
// silently matched nothing from passing as agreement.
func yamlDocumentedOperations(t *testing.T, name string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(openapiSpecDir(), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	ops := map[string]bool{}
	inPaths := false
	current := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if yamlTopLevelKey.MatchString(line) {
			inPaths = strings.HasPrefix(line, "paths:")
			current = ""
			continue
		}
		if !inPaths {
			continue
		}
		if m := yamlPathKey.FindStringSubmatch(line); m != nil {
			current = m[1]
			continue
		}
		if current == "" {
			continue
		}
		if m := yamlOperationKey.FindStringSubmatch(line); m != nil {
			ops[strings.ToUpper(m[1])+" "+current] = true
		}
	}
	return ops
}

// TestOpenAPIContract_V2TwinsDocumentTheSameOperations is lock (g): api-v2.json
// and api-v2.yaml are two hand-maintained renderings of one contract, and only
// the JSON is machine-checked by lock (a). Before this lock the YAML alone
// carried four operations the JSON did not (one of them, GET
// /{tenant_slug}/settings, mounted nowhere) — and docs/openapi/index.html loads
// the YAML by default, so the copy nobody checked was the copy readers saw.
//
// BLIND SPOT: operation sets only. Two twins can agree here and still disagree
// about parameters, request bodies, response schemas and component definitions
// — api-v2.json's ChannelConfigValidationError schema has no YAML counterpart
// today and this lock is silent about it.
func TestOpenAPIContract_V2TwinsDocumentTheSameOperations(t *testing.T) {
	jsonOps := documentedOperations(t, loadOpenAPIJSONDoc(t, "api-v2.json"), "api-v2.json")
	yamlOps := yamlDocumentedOperations(t, "api-v2.yaml")

	// Scanner-honesty floor: the YAML scan returning nothing must fail loudly
	// rather than agree vacuously with an empty set.
	const minYAMLOperations = 40
	if len(yamlOps) < minYAMLOperations {
		t.Fatalf("api-v2.yaml scan found %d operation(s), want at least %d — the line scan is broken, not the spec thin", len(yamlOps), minYAMLOperations)
	}

	var jsonOnly, yamlOnly []string
	for op := range jsonOps {
		if !yamlOps[op] {
			jsonOnly = append(jsonOnly, op)
		}
	}
	for op := range yamlOps {
		if !jsonOps[op] {
			yamlOnly = append(yamlOnly, op)
		}
	}
	sort.Strings(jsonOnly)
	sort.Strings(yamlOnly)
	if len(jsonOnly) > 0 {
		t.Errorf("api-v2.json documents %d operation(s) missing from api-v2.yaml: %v", len(jsonOnly), jsonOnly)
	}
	if len(yamlOnly) > 0 {
		t.Errorf("api-v2.yaml documents %d operation(s) missing from api-v2.json: %v", len(yamlOnly), yamlOnly)
	}
	// Only claim agreement when there IS agreement: this line used to print
	// "api-v2 twins agree on 41 operation(s)" in the same output as
	// "api-v2.yaml documents 1 operation(s) missing from api-v2.json".
	if len(jsonOnly) == 0 && len(yamlOnly) == 0 {
		t.Logf("api-v2 twins agree on %d operation(s)", len(jsonOps))
	} else {
		t.Logf("api-v2 twins DISAGREE: %d json-only, %d yaml-only, %d in common", len(jsonOnly), len(yamlOnly), len(jsonOps)-len(jsonOnly))
	}
}

var ginPathParam = regexp.MustCompile(`:([^/]+)`)

// openAPIPathFromGin is the inverse of ginPathFromOpenAPI.
func openAPIPathFromGin(p string) string {
	return ginPathParam.ReplaceAllString(p, "{$1}")
}

// contractLockDocumentedGroup names a gin route-group prefix where the router
// -> doc direction is closed: every route mounted under prefix must appear in
// spec. Everywhere else in this file runs doc -> router, which can only ever
// say "you documented something that does not exist".
//
// minRoutes is a scanner-honesty floor per group: zero matches means the
// prefix stopped matching (a group renamed, a router refactor), not that the
// group emptied out, and a silent zero would let the group pass vacuously.
type contractLockDocumentedGroup struct {
	prefix    string
	spec      string
	minRoutes int
	why       string
}

var contractLockFullyDocumentedGroups = []contractLockDocumentedGroup{
	{
		prefix:    "/api/v2/:tenant_slug/billing/",
		spec:      "api-v2.json",
		minRoutes: 2,
		why: "money routes. GET /api/v2/:tenant_slug/billing/invoices had been mounted the whole time and appeared in neither api-v2 twin, " +
			"while POST /billing/topup, which does not exist, did.",
	},
	{
		prefix:    "/pg/",
		spec:      "relay.json",
		minRoutes: 1,
		why: "POST /pg/chat/completions is a BILLED relay path (relay-router.go:64-85: PlaygroundAuth, PoolBalanceCheck, CostSpikeLimit, " +
			"EntitlementCheck, ModelRequestRateLimit, BusinessRateLimit, RelayConcurrencyLimit, Distribute), and on 2026-09-21 its only " +
			"published description was deleted: it lived in api-v2.yaml alone, the JSON twin never had it, and the removal was justified " +
			"with the claim that relay.json owned that surface when relay.json documented no /pg path at all. Nothing in this file could " +
			"see it: lock (a) walks doc -> router, and this lock covered the billing group only.",
	},
}

// TestOpenAPIContract_MountedGroupsFullyDocumented is lock (i): the only
// router -> doc direction in this file, now over a table of groups rather than
// the single v2 billing prefix it started as. The mounted set comes from
// engine.Routes(), not from a list here, so a route added to one of these
// groups without documentation fails on the commit that adds it.
//
// BLIND SPOT: these groups and no others. A new undocumented route under
// /tokens, /channels, /logs, /v1 or the admin surface still passes every lock
// in this file; contractLockOperationFloors only catches DELETIONS from a
// spec, not additions to the router. Widening this table means first
// documenting those groups — see the hand-off notes in the L7 report.
func TestOpenAPIContract_MountedGroupsFullyDocumented(t *testing.T) {
	engine := buildContractLockEngine(t)
	if len(contractLockFullyDocumentedGroups) == 0 {
		t.Fatal("contractLockFullyDocumentedGroups is empty — this lock would pass vacuously")
	}
	for _, group := range contractLockFullyDocumentedGroups {
		t.Run(group.prefix, func(t *testing.T) {
			documented := documentedOperations(t, loadOpenAPIJSONDoc(t, group.spec), group.spec)

			var mounted []string
			for _, rt := range engine.Routes() {
				if strings.HasPrefix(rt.Path, group.prefix) {
					mounted = append(mounted, rt.Method+" "+openAPIPathFromGin(rt.Path))
				}
			}
			if len(mounted) < group.minRoutes {
				t.Fatalf("found %d mounted route(s) under %s, want at least %d — the prefix scan is broken", len(mounted), group.prefix, group.minRoutes)
			}

			var undocumented []string
			for _, op := range mounted {
				if !documented[op] {
					undocumented = append(undocumented, op)
				}
			}
			sort.Strings(undocumented)
			if len(undocumented) > 0 {
				t.Errorf("%d route(s) mounted under %s are documented nowhere in %s: %v\nwhy this group is locked: %s",
					len(undocumented), group.prefix, group.spec, undocumented, group.why)
			}
			sort.Strings(mounted)
			t.Logf("%s -> %s: %d mounted route(s) checked: %v", group.prefix, group.spec, len(mounted), mounted)
		})
	}
}

// ---------------------------------------------------------------------------
// (j) tags: declared <-> used, both directions
// ---------------------------------------------------------------------------

// specTags returns (declared, used) tag-name sets for a parsed OpenAPI 3 doc.
//
// BLIND SPOT: names only. It cannot tell you that a tag's DESCRIPTION is
// accurate — api-v2.json's Billing tag read "Billing and subscriptions" for a
// while after the last subscription operation was deleted, and nothing
// mechanical could see that. All this proves is that no section is advertised
// with nothing in it, and that no section appears without a description.
func specTags(t *testing.T, doc map[string]any, specName string) (declared, used map[string]bool) {
	t.Helper()
	declared = map[string]bool{}
	if raw, ok := doc["tags"].([]any); ok {
		for _, e := range raw {
			tag, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := tag["name"].(string); ok && name != "" {
				declared[name] = true
			}
		}
	}
	used = map[string]bool{}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no paths object", specName)
	}
	for _, v := range paths {
		methods, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for method, opAny := range methods {
			if _, isMethod := contractLockHTTPMethods[method]; !isMethod {
				continue
			}
			op, ok := opAny.(map[string]any)
			if !ok {
				continue
			}
			raw, ok := op["tags"].([]any)
			if !ok {
				continue
			}
			for _, e := range raw {
				if name, ok := e.(string); ok && name != "" {
					used[name] = true
				}
			}
		}
	}
	return declared, used
}

// TestOpenAPIContract_TagsDeclaredAndUsed is lock (j). Swagger UI — which is
// what docs/openapi/index.html serves — renders one collapsible section per
// DECLARED tag, and an undeclared tag used by an operation gets a section with
// no description at all. So a declared-but-unused tag is a surface that still
// looks advertised with nothing behind it, and a used-but-undeclared tag is a
// section a reader meets with no explanation.
//
// It found four live drifts the day it was written: api-v2.json declared
// "Tenant Config" after both tenant-config operations were deleted as
// non-existent and used "ChatSessions" without declaring it, and relay.json
// declared three tags ("图片生成", "图片生成/OpenAI兼容格式", "未实现") that no
// operation carries while using "异步任务/通用" undeclared.
func TestOpenAPIContract_TagsDeclaredAndUsed(t *testing.T) {
	for _, spec := range publishedJSONSpecs(t) {
		t.Run(spec, func(t *testing.T) {
			declared, used := specTags(t, loadOpenAPIJSONDoc(t, spec), spec)
			if len(used) == 0 {
				t.Fatalf("%s: scanned zero tag usages — the walk is broken, not the spec untagged", spec)
			}
			var unused, undeclared []string
			for name := range declared {
				if !used[name] {
					unused = append(unused, name)
				}
			}
			for name := range used {
				if !declared[name] {
					undeclared = append(undeclared, name)
				}
			}
			sort.Strings(unused)
			sort.Strings(undeclared)
			if len(unused) > 0 {
				t.Errorf("%s declares %d tag(s) no operation uses %v — Swagger UI renders each as an empty advertised section", spec, len(unused), unused)
			}
			if len(undeclared) > 0 {
				t.Errorf("%s uses %d tag(s) it never declares %v — those sections render with no description", spec, len(undeclared), undeclared)
			}
			t.Logf("%s: %d declared tag(s), %d used", spec, len(declared), len(used))
		})
	}
}

// ---------------------------------------------------------------------------
// (k) the published v2 rate limit must be the one the middleware enforces
// ---------------------------------------------------------------------------

var (
	globalV2LimitNumLiteral      = regexp.MustCompile(`GlobalV2RateLimitNum\s*=\s*GetEnvOrDefault\("GLOBAL_V2_RATE_LIMIT",\s*(\d+)\)`)
	globalV2LimitDurationLiteral = regexp.MustCompile(`GlobalV2RateLimitDuration\s*=\s*int64\(GetEnvOrDefault\("GLOBAL_V2_RATE_LIMIT_DURATION",\s*(\d+)\)\)`)
	globalV2RateLimitFuncBody    = regexp.MustCompile(`(?s)func GlobalV2RateLimit\(\)[^{]*\{(.*?)
}`)
)

// v2RateLimitFactsFromSource reads the /api/v2 rate-limit budget, window and
// keying dimension out of the two source files that decide them, and returns
// the sentence the published spec has to contain.
//
// The keying dimension is DERIVED, not assumed: GlobalV2RateLimit's body is
// read, and the required wording is "per source IP" only when that body calls
// rateLimitFactory (whose limiters key on c.ClientIP(), rate-limit.go:155/:157)
// rather than keyedRateLimitFactory (which takes a key function). If someone
// rekeys the bucket to a user or a session, this test fails until the spec is
// rewritten to say so.
//
// BLIND SPOT: it reads the DEFAULTS in common/init.go. A deployment that sets
// GLOBAL_V2_RATE_LIMIT in its manifest runs a different number, which is why
// the required sentence also has to name the env var — a reader who sees only
// "600" would take it for a fixed product limit. The UAT overlay really does
// override it (deploy/k8s/r6-uat/deployment.yaml).
func v2RateLimitFactsFromSource(t *testing.T) string {
	t.Helper()
	initSrc, err := os.ReadFile(filepath.Join(repoRootFromRouterPkg(), "internal", "pkg", "common", "init.go"))
	if err != nil {
		t.Fatalf("read internal/pkg/common/init.go: %v", err)
	}
	numMatch := globalV2LimitNumLiteral.FindStringSubmatch(string(initSrc))
	durMatch := globalV2LimitDurationLiteral.FindStringSubmatch(string(initSrc))
	if numMatch == nil || durMatch == nil {
		t.Fatalf("could not read GLOBAL_V2_RATE_LIMIT / _DURATION defaults out of internal/pkg/common/init.go — the scan is broken, not the limiter gone (num=%v dur=%v)", numMatch, durMatch)
	}

	mwSrc, err := os.ReadFile(filepath.Join(repoRootFromRouterPkg(), "internal", "adapter", "middleware", "rate-limit-v2.go"))
	if err != nil {
		t.Fatalf("read internal/adapter/middleware/rate-limit-v2.go: %v", err)
	}
	body := globalV2RateLimitFuncBody.FindStringSubmatch(string(mwSrc))
	if body == nil {
		t.Fatal("could not find func GlobalV2RateLimit() in rate-limit-v2.go — the scan is broken")
	}
	if strings.Contains(body[1], "keyedRateLimitFactory(") {
		t.Fatalf("GlobalV2RateLimit now uses keyedRateLimitFactory — the bucket is no longer keyed by source IP, so this lock's required wording is stale and BOTH api-v2 twins must be rewritten before it can be re-enabled:\n%s", body[1])
	}
	if !strings.Contains(body[1], "rateLimitFactory(") {
		t.Fatalf("GlobalV2RateLimit calls neither rateLimitFactory nor keyedRateLimitFactory — this lock cannot derive the keying dimension any more:\n%s", body[1])
	}
	return numMatch[1] + " requests per " + durMatch[1] + " s per source IP"
}

// normalizeSpecWhitespace collapses every run of whitespace to one space so a
// required sentence can be found across a YAML line wrap or a JSON \n.
func normalizeSpecWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// infoDescription returns info.description of a parsed JSON spec.
func infoDescription(t *testing.T, doc map[string]any, specName string) string {
	t.Helper()
	info, ok := doc["info"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no info object", specName)
	}
	desc, ok := info["description"].(string)
	if !ok {
		t.Fatalf("%s has no info.description", specName)
	}
	return desc
}

// yamlInfoDescription returns the text of a YAML spec's info.description.
//
// BLIND SPOT: it understands exactly one shape — a literal block scalar
// written as "  description: |" directly under a column-0 "info:" key, with the
// body indented four spaces. A folded scalar (">"), a quoted single-line
// description, or an info block reached through an anchor would all read as
// absent, and the Fatalf below is what turns that into a loud failure instead
// of a vacuous pass. Same reason as yamlDocumentedOperations: this package must
// not promote gopkg.in/yaml.v3 from an indirect dependency to a direct one.
func yamlInfoDescription(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(openapiSpecDir(), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var out []string
	inInfo, started := false, false
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if yamlTopLevelKey.MatchString(line) {
			if started {
				break
			}
			inInfo = strings.HasPrefix(line, "info:")
			continue
		}
		if !inInfo {
			continue
		}
		if !started {
			if strings.TrimRight(line, " ") == "  description: |" {
				started = true
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		if !strings.HasPrefix(line, "    ") {
			break
		}
		out = append(out, line[4:])
	}
	if len(out) == 0 {
		t.Fatalf("%s: found no info.description literal block scalar (\"  description: |\" under a column-0 \"info:\") — the line scan is broken, or the file stopped using that shape", name)
	}
	return strings.Join(out, "\n")
}

// TestOpenAPIContract_V2RateLimitProseMatchesMiddleware is lock (k).
//
// Until 2026-09-21 repair both api-v2 twins published "Standard endpoints: 100
// requests/minute per user". Every one of those three facts was wrong: the
// limiter on that surface is middleware.GlobalV2RateLimit (api-v2-router.go:45)
// at a default 600 per 180 s (common/init.go), keyed by source IP and therefore
// shared by everybody behind one egress address. An enterprise office on one
// public address hits a budget it was told was personal.
//
// The required sentence is BUILT from the source files, not typed here, so the
// spec cannot stay behind a change to the default or to the keying.
//
// BLIND SPOT: substring presence, over info.description alone. The spec can
// contain the true sentence AND a contradicting one beside it; the "per user"
// ban below is the only contradiction this lock knows how to look for by name,
// and a per-user claim written into an individual operation's description
// rather than into info.description is outside what this reads.
func TestOpenAPIContract_V2RateLimitProseMatchesMiddleware(t *testing.T) {
	want := v2RateLimitFactsFromSource(t)
	t.Logf("required sentence, derived from common/init.go + rate-limit-v2.go: %q", want)

	for _, tc := range []struct{ name, text string }{
		{"api-v2.json", infoDescription(t, loadOpenAPIJSONDoc(t, "api-v2.json"), "api-v2.json")},
		{"api-v2.yaml", yamlInfoDescription(t, "api-v2.yaml")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flat := normalizeSpecWhitespace(tc.text)
			if !strings.Contains(flat, want) {
				t.Errorf("%s does not state the real /api/v2 rate limit. It must contain, verbatim: %q", tc.name, want)
			}
			if !strings.Contains(flat, "GLOBAL_V2_RATE_LIMIT") {
				t.Errorf("%s states a rate limit without naming GLOBAL_V2_RATE_LIMIT — a reader cannot tell it is an operator-tunable default rather than a product limit", tc.name)
			}
			if strings.Contains(strings.ToLower(flat), "per user") {
				t.Errorf("%s still says \"per user\" somewhere. The /api/v2 bucket is keyed by source IP (rateLimitFactory -> c.ClientIP()), so no sentence in it may claim a per-user budget", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// (l) api.json must state how unreconciled it is
// ---------------------------------------------------------------------------

// unmountedOperations returns the documented operations of spec that the real
// router does not mount, in lock (a)'s exact terms (including its wildcard
// tolerance), so the two can never disagree about what "unmounted" means.
func unmountedOperations(t *testing.T, spec string, registered map[string]bool, wildcardPrefixes []string) []string {
	t.Helper()
	ops := documentedOperations(t, loadOpenAPIJSONDoc(t, spec), spec)
	var missing []string
	for op := range ops {
		sep := strings.IndexByte(op, ' ')
		key := op[:sep+1] + ginPathFromOpenAPI(op[sep+1:])
		if registered[key] {
			continue
		}
		matched := false
		for _, prefix := range wildcardPrefixes {
			if strings.HasPrefix(key, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			missing = append(missing, op)
		}
	}
	sort.Strings(missing)
	return missing
}

// legacySpecName is the one published spec that is NOT reconciled against the
// router (see contractLockMountedSpecs).
const legacySpecName = "api.json"

// TestOpenAPIContract_LegacySpecStatesItsUnreconciledPaths is lock (l), and it
// is here because of a specific mistake: api.json's servers array was empty
// (claiming nothing), the L7 lane set it to the production host, and wrote lock
// (h) to pin it there — without ever measuring how much of the file is
// fiction. It is a lot: an SDK generated from it points phantom operations
// (GET /api/user/topup, GET /api/user/topup/info, POST /api/stripe/webhook,
// the passkey pair the repo's own CLAUDE.md records as never having had a
// handler, the whole /api/oauth/{github,wechat,...} set) at the live host.
//
// Rather than delete the file or blank its servers, the number is measured
// here, from the real route table, and api.json has to carry it in its own
// bytes — so a reader of the document, not just a reader of this test, sees
// it. README.md has to carry the same pair, which is what stops the prose
// there from rotting away from the measurement.
//
// Mount one of the phantom routes and this test goes red telling you the new
// number: that is the ratchet, and it only turns one way.
//
// BLIND SPOT: a count, not a list, and it says nothing about the operations
// that ARE mounted — their request and response shapes are unverified too,
// like every other operation in this package.
func TestOpenAPIContract_LegacySpecStatesItsUnreconciledPaths(t *testing.T) {
	engine := buildContractLockEngine(t)
	registered, wildcardPrefixes := realRouteTable(t, engine)

	unmounted := unmountedOperations(t, legacySpecName, registered, wildcardPrefixes)
	total := len(documentedOperations(t, loadOpenAPIJSONDoc(t, legacySpecName), legacySpecName))
	measured := len(unmounted)
	if measured == 0 {
		t.Fatalf("%s measured 0 unmounted operation(s) out of %d — either every legacy path really is mounted now (in which case move it into contractLockMountedSpecs and delete this lock) or the measurement broke", legacySpecName, total)
	}
	t.Logf("%s: %d of %d documented operation(s) are not mounted; first five: %v", legacySpecName, measured, total, unmounted[:min(5, len(unmounted))])

	doc := loadOpenAPIJSONDoc(t, legacySpecName)
	ext, ok := doc["x-lurus-unreconciled-operations"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no x-lurus-unreconciled-operations object. Add one saying {\"count\": %d, \"of\": %d, ...}: this document declares the production host as its server while %d of its %d operations are mounted nowhere, and a reader of the file itself deserves to know that", legacySpecName, measured, total, measured, total)
	}
	gotCount, _ := ext["count"].(float64)
	gotOf, _ := ext["of"].(float64)
	if int(gotCount) != measured || int(gotOf) != total {
		t.Errorf("%s x-lurus-unreconciled-operations says %d of %d, the real route table says %d of %d — update the extension, info.description and the api.json row in README.md together",
			legacySpecName, int(gotCount), int(gotOf), measured, total)
	}

	desc := infoDescription(t, doc, legacySpecName)
	if !strings.Contains(desc, "x-lurus-unreconciled-operations") {
		t.Errorf("%s info.description does not point the reader at x-lurus-unreconciled-operations — the number is only useful to someone who knows to look for it", legacySpecName)
	}

	readme, err := os.ReadFile(filepath.Join(repoRootFromRouterPkg(), "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	wantRow := fmt.Sprintf("%d of %d", measured, total)
	if !strings.Contains(normalizeSpecWhitespace(string(readme)), wantRow) {
		t.Errorf("README.md does not contain %q — its %s row claims the file is not reconciled without saying how far off it is, and that prose is exactly what rots", wantRow, legacySpecName)
	}
}
