package router

// openapi_contract_lock_test.go — L3-CONTRACT-TAXONOMY's DOC-1 closer: a Go
// test that fails the moment docs/openapi/relay.json drifts from either the
// real route table (engine.Routes(), built the same way
// cov_router-relay_wiring_test.go's SetRouter tests do) or the ErrorCode
// constants in internal/pkg/types — in EITHER direction, so a doc that
// invents an endpoint/code fails just as loudly as one that goes stale.
//
// (a) every documented path+method exists in the real route table.
// (b) the 200 response on the four chat-class paths documents X-Request-Id.
// (c) every documented 429 documents Retry-After and X-RateLimit-Scope.
// (d) /v1/key and /v1/generation are documented.
// (e) the GatewayError.code enum and the ErrorCode = "..." literals in
//     internal/pkg/types (non-test sources) match exactly, bidirectionally.
// (f) x-lurus-enforcement-order equals the ten-stage relay gate order.

import (
	"bytes"
	"encoding/json"
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

func loadRelayOpenAPIDoc(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join(repoRootFromRouterPkg(), "docs", "openapi", "relay.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
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

// TestOpenAPIContract_PathsExistInRealRouteTable is lock (a).
func TestOpenAPIContract_PathsExistInRealRouteTable(t *testing.T) {
	doc := loadRelayOpenAPIDoc(t)
	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("relay.json has no paths object — parser or fixture is broken")
	}

	engine := buildContractLockEngine(t)
	registered := map[string]bool{}
	// wildcardPrefixes holds "METHOD prefix" for gin catch-all routes
	// (…/*path) — Gemini's REST convention encodes an action into the model
	// segment (models/{model}:generateContent) that gin cannot bind as a
	// named param (the literal ":" isn't a segment boundary), so
	// relay-router.go mounts it as POST /v1beta/models/*path instead. The
	// doc keeps the human/SDK-facing {model}:generateContent template
	// (that IS the real Gemini wire shape); this is the one legitimate
	// named-param-vs-wildcard translation, not a stale doc.
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

	var missing []string
	checked := 0
	for docPath, v := range paths {
		methods, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ginPath := ginPathFromOpenAPI(docPath)
		for m := range methods {
			upper, isMethod := contractLockHTTPMethods[m]
			if !isMethod {
				continue
			}
			checked++
			key := upper + " " + ginPath
			if registered[key] {
				continue
			}
			matched := false
			for _, prefix := range wildcardPrefixes {
				if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
					matched = true
					break
				}
			}
			if !matched {
				missing = append(missing, key+" (documented as "+docPath+")")
			}
		}
	}
	if checked == 0 {
		t.Fatal("scanned zero documented operations — the doc walk is broken")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d documented path(s) not present in the real route table:\n%s",
			len(missing), joinLines(missing))
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

// TestOpenAPIContract_429sDocumentRetryAfterAndScope is lock (c).
func TestOpenAPIContract_429sDocumentRetryAfterAndScope(t *testing.T) {
	doc := loadRelayOpenAPIDoc(t)
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
	// document a 429 with Retry-After + Scope/Type.
	const minDocumented429s = 4
	if found < minDocumented429s {
		t.Fatalf("scanned %d documented 429 response(s), want at least %d (one each for /v1/chat/completions, /v1/completions, /v1/embeddings, /v1/messages) — a chat-class path lost its 429 documentation", found, minDocumented429s)
	}
	t.Logf("429 responses checked: %d", found)
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
