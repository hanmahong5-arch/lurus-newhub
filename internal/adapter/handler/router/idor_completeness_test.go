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
	tenantScopedPrefixes := []string{"/api/channel/", "/api/redemption/", "/api/user/"}

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
		"POST /api/channel/fix":          "FixChannelsAbilities rebuilds ability rows idempotently; global maintenance, not a per-tenant data mutation",
		"POST /api/redemption/":          "AddRedemption stamps the caller's tenant_id; cannot target another tenant",
		// user self-service (UserAuth; operate on the authenticated principal, no id)
		"PUT /api/user/self":          "self-service: updates the authenticated user only",
		"PUT /api/user/setting":       "self-service: updates the authenticated user's settings only",
		"POST /api/user/topup":        "self-service: redeems into the authenticated user's own balance",
		"POST /api/user/totp/enroll":  "self-service: manages the authenticated user's own TOTP factor",
		"POST /api/user/totp/confirm": "self-service: manages the authenticated user's own TOTP factor",
		"POST /api/user/totp/disable": "self-service: manages the authenticated user's own TOTP factor",
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
