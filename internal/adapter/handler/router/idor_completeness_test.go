package router

// idor_completeness_test.go — structural forcing function for v1 tenant
// isolation. Every by-id or mutating route on a tenant-scoped resource group
// (channel / redemption / user) in the v1 console MUST be either exercised by a
// cross-tenant IDOR test (v1_cross_tenant_idor_test.go + v1_idor_sweep_test.go,
// package handler) or explicitly exempted here with a justification.
//
// A newly-added, unclassified tenant-scoped mutation fails this test: you cannot
// ship one without either proving its isolation or documenting why it needs
// none. This is exactly how the DeleteInvalidRedemption cross-tenant purge would
// have been caught before shipping.
//
// It lives in the router package (not handler) because it enumerates the real
// route table via SetApiRouter/engine.Routes(); a package-handler test importing
// the router package would form an import cycle (router imports handler).

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

func TestV1IDOR_Completeness(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	// Resource groups whose rows carry a tenant_id and are addressable by id.
	// Tokens are intentionally excluded: they are USER-scoped (every v1 token
	// handler resolves the id under the caller's own user id), a distinct
	// isolation covered elsewhere, not tenant-by-id IDOR.
	// /api/openrouter-sync/ joined the set in cycle 13 L4: its rows describe
	// process-global sync state, but the group was AdminAuth-gated, so a
	// tenant admin reached every route under it. The prefix is listed here so
	// a new route added to that group has to be classified, not so it is
	// assumed tenant-scoped.
	tenantScopedPrefixes := []string{"/api/channel/", "/api/redemption/", "/api/user/", "/api/openrouter-sync/"}

	// Proven tenant-isolated by a cross-tenant IDOR test. Key = "METHOD PATH".
	swept := map[string]bool{
		// channel — single-resource (v1_cross_tenant_idor_test.go)
		"GET /api/channel/:id":    true,
		"DELETE /api/channel/:id": true,
		"PUT /api/channel/":       true,
		// channel — action handlers (v1_idor_sweep_test.go)
		"GET /api/channel/test/:id":           true,
		"GET /api/channel/update_balance/:id": true,
		"POST /api/channel/copy/:id":          true,
		"GET /api/channel/fetch_models/:id":   true,
		"GET /api/channel/ollama/version/:id": true,
		"POST /api/channel/multi_key/manage":  true,
		// channel — ollama mutations (guarded by the shared enforceTenantScope)
		"POST /api/channel/ollama/pull":        true,
		"POST /api/channel/ollama/pull/stream": true,
		"DELETE /api/channel/ollama/delete":    true,
		// channel — bulk / tag (v1_cross_tenant_idor_test.go)
		"DELETE /api/channel/disabled":   true,
		"POST /api/channel/tag/disabled": true,
		"POST /api/channel/tag/enabled":  true,
		"PUT /api/channel/tag":           true,
		"POST /api/channel/batch":        true,
		"POST /api/channel/batch/tag":    true,
		// redemption
		"GET /api/redemption/:id":        true,
		"DELETE /api/redemption/:id":     true,
		"PUT /api/redemption/":           true,
		"DELETE /api/redemption/invalid": true,
		// user (admin)
		"GET /api/user/:id": true,
		"PUT /api/user/":    true,
	}

	// Not a tenant-admin IDOR surface, each with the reason it needs no per-id
	// tenant check.
	exempt := map[string]string{
		"POST /api/channel/:id/key":      "RootAuth-gated (root only); not reachable by a tenant admin",
		"POST /api/channel/":             "AddChannel stamps the caller's tenant_id; cannot target another tenant",
		"POST /api/channel/fetch_models": "operates on request-body base_url/key; no stored resource id to cross tenants",
		"POST /api/channel/fix":          "RootAuth-gated (cycle-11 L8/L3): operator-only maintenance, not reachable by a tenant admin",
		"POST /api/redemption/":          "AddRedemption stamps the caller's tenant_id; cannot target another tenant",
		// user self-service (UserAuth; operate on the authenticated principal, no id)
		"PUT /api/user/self":                          "self-service: updates the authenticated user only",
		"PUT /api/user/setting":                       "self-service: updates the authenticated user's settings only",
		"POST /api/user/topup":                        "self-service: redeems into the authenticated user's own balance",
		"POST /api/user/totp/enroll":                  "self-service: manages the authenticated user's own TOTP factor",
		"POST /api/user/totp/confirm":                 "self-service: manages the authenticated user's own TOTP factor",
		"POST /api/user/totp/disable":                 "self-service: manages the authenticated user's own TOTP factor",
		"POST /api/user/totp/backup-codes/regenerate": "self-service: manages the authenticated user's own TOTP backup codes",
		// openrouter-sync: the job catalogue is process-global (no tenant_id
		// column on the row at all), and every write is RootAuth-gated in
		// api-router.go, so a tenant admin cannot reach one to cross with.
		"POST /api/openrouter-sync/jobs":            "RootAuth-gated (root only); not reachable by a tenant admin",
		"PUT /api/openrouter-sync/jobs/:id":         "RootAuth-gated (root only); not reachable by a tenant admin",
		"DELETE /api/openrouter-sync/jobs/:id":      "RootAuth-gated (root only); not reachable by a tenant admin",
		"POST /api/openrouter-sync/jobs/:id/run":    "RootAuth-gated (root only); not reachable by a tenant admin",
		"POST /api/openrouter-sync/run-all":         "RootAuth-gated (root only); not reachable by a tenant admin",
		"GET /api/openrouter-sync/jobs/:id/preview": "PreviewOpenRouterSyncJob dry-runs a sync job against the process-global free-model catalog (the same catalog CreateOpenRouterSyncJob mutates); the job row itself carries no tenant_id",
	}

	isMutation := func(m string) bool {
		return m == http.MethodPost || m == http.MethodPut || m == http.MethodDelete
	}
	inScope := func(path string) bool {
		for _, p := range tenantScopedPrefixes {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
		return false
	}

	for _, rt := range engine.Routes() {
		if !inScope(rt.Path) {
			continue
		}
		// The IDOR surface is by-id lookups and mutations. Read-only list/search
		// endpoints are a separate (list-scoping) concern covered by the
		// *_ListTenantScoped tests, not this by-id/mutation guard.
		if !strings.Contains(rt.Path, ":id") && !isMutation(rt.Method) {
			continue
		}
		key := rt.Method + " " + rt.Path
		if swept[key] {
			continue
		}
		if _, ok := exempt[key]; ok {
			continue
		}
		t.Errorf("tenant-scoped route %q is neither swept by a cross-tenant IDOR test nor exempted — "+
			"add a case to v1_idor_sweep_test.go or an exemption with justification here", key)
	}
}

// TestV1IDOR_ListScopeCompleteness is the list-scoping sibling of
// TestV1IDOR_Completeness above: every in-scope, :id-less GET whose path
// does not contain "/self" must appear in listScoped (backed by a named
// *_ListTenantScoped test in v1_cross_tenant_idor_test.go, verified to
// actually exist by source grep, not just declared here) or listExempt
// (with a factual reason). An unclassified route fails the test — the map
// must never be merely "non-empty".
func TestV1IDOR_ListScopeCompleteness(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	tenantScopedPrefixes := []string{"/api/channel/", "/api/redemption/", "/api/user/", "/api/task/", "/api/mj/", "/api/models", "/api/openrouter-sync/"}

	// route -> name of the *_ListTenantScoped test in package handler
	// (internal/adapter/handler/v1_cross_tenant_idor_test.go) that proves
	// this list narrows to the caller's tenant.
	listScoped := map[string]string{
		"GET /api/channel/":          "TestV1Channel_ListTenantScoped",
		"GET /api/channel/search":    "TestV1ChannelSearch_ListTenantScoped",
		"GET /api/redemption/":       "TestV1Redemption_ListTenantScoped",
		"GET /api/redemption/search": "TestV1RedemptionSearch_ListTenantScoped",
		"GET /api/user/":             "TestV1User_ListTenantScoped",
		"GET /api/user/search":       "TestV1UserSearch_ListTenantScoped",
		"GET /api/task/":             "TestV1Task_ListTenantScoped",
		"GET /api/mj/":               "TestV1Midjourney_ListTenantScoped",
		// cycle-12 L5: narrowed to the caller's tenant.
		"GET /api/user/models":            "TestV1UserModels_ListTenantScoped",
		"GET /api/channel/models_enabled": "TestV1ModelsEnabled_ListTenantScoped",
		"GET /api/channel/tag/models":     "TestV1ChannelTagModels_ListTenantScoped",
		"GET /api/models/missing":         "TestV1MissingModels_ListTenantScoped",
		// cycle-12 L5: the models table itself stays global; what is scoped is
		// the per-row bound_channels / enable_groups enrichment and, for
		// pricing_info, the rows another tenant's channels alone serve.
		"GET /api/models/":             "TestV1ModelsMeta_ListTenantScoped",
		"GET /api/models/search":       "TestV1ModelsMeta_ListTenantScoped",
		"GET /api/models/pricing_info": "TestV1ModelsPricingInfo_ListTenantScoped",
	}

	listExempt := map[string]string{
		// self-service: the caller reads only its own account, no tenant list to scope.
		"GET /api/user/token":       "self-service: issues an access token for the authenticated user only",
		"GET /api/user/totp/status": "self-service: reads the authenticated user's own TOTP enrollment state",
		// root-only after W's RootAuth edits above: a whole-platform operator pass, not a tenant-admin list.
		"GET /api/channel/test":           "RootAuth-gated (cycle-11 L8/L3): whole-platform operator pass, not reachable by a tenant admin",
		"GET /api/channel/update_balance": "RootAuth-gated (cycle-11 L8/L3): whole-platform operator pass, not reachable by a tenant admin",
		// No tenant dimension to scope: both answer from the compile-time
		// vendor catalogue model.go's init() builds (openAIModels /
		// channelId2Models), running no query at all.
		"GET /api/channel/models":               "ChannelListModels returns the compile-time vendor catalogue built in model.go init(); it issues no query and has no tenant dimension",
		"GET /api/models":                       "DashboardListModels returns channelId2Models, the same compile-time per-channel-TYPE catalogue from model.go init(); no query, no tenant dimension",
		"GET /api/models/sync_upstream/preview": "SyncUpstreamPreview reports only names that exist in the public upstream catalogue it fetched (model_sync.go intersects both the local rows and the missing list with it), so a name only one tenant's channel serves cannot appear",
		// openrouter-sync reads, root-only since cycle 13 L4 (api-router.go).
		"GET /api/openrouter-sync/jobs":        "RootAuth-gated after the cycle-13 L4 api-router.go edit; ListOpenRouterSyncJobs reads the process-global free-model sync job catalog, not tenant data",
		"GET /api/openrouter-sync/categories":  "RootAuth-gated after the cycle-13 L4 api-router.go edit; ListOpenRouterSyncCategories reads the process-global OpenRouter model category list",
		"GET /api/openrouter-sync/last-status": "RootAuth-gated after the cycle-13 L4 api-router.go edit; GetOpenRouterSyncLastStatus reads the process-global last-sync-run status",
		"GET /api/openrouter-sync/api-pool":    "RootAuth-gated after the cycle-13 L4 api-router.go edit; also defence-in-depth tenant-scoped by repo.ListOpenRouterMultiKeyChannelsForScope (cycle 13 L4) — proven by handler.TestV1OpenRouterApiPool_TenantScoped in internal/adapter/handler/openrouter_pool_test.go, a different file than the one readHandlerTestSrc greps here, so listed as exempt rather than in listScoped",
	}

	inScope := func(path string) bool {
		for _, p := range tenantScopedPrefixes {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
		return false
	}

	handlerTestSrc := "" // lazily read once
	readHandlerTestSrc := func(t *testing.T) string {
		if handlerTestSrc != "" {
			return handlerTestSrc
		}
		b, err := os.ReadFile("../v1_cross_tenant_idor_test.go")
		if err != nil {
			t.Fatalf("read v1_cross_tenant_idor_test.go: %v", err)
		}
		handlerTestSrc = string(b)
		return handlerTestSrc
	}

	for _, rt := range engine.Routes() {
		if !inScope(rt.Path) {
			continue
		}
		if rt.Method != http.MethodGet || strings.Contains(rt.Path, ":id") || strings.Contains(rt.Path, "/self") {
			continue
		}
		key := rt.Method + " " + rt.Path
		if testName, ok := listScoped[key]; ok {
			if !strings.Contains(readHandlerTestSrc(t), "func "+testName+"(") {
				t.Errorf("listScoped[%q] names %q but no such function exists in v1_cross_tenant_idor_test.go", key, testName)
			}
			continue
		}
		if _, ok := listExempt[key]; ok {
			continue
		}
		t.Errorf("tenant-scoped list route %q is neither in listScoped nor listExempt — "+
			"add a *_ListTenantScoped test and a listScoped entry, or a listExempt reason", key)
	}
}
