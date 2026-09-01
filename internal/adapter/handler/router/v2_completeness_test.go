package router

// v2_completeness_test.go — structural forcing function for v2 tenant
// isolation, mirroring idor_completeness_test.go's pattern for the
// /api/v2/:tenant_slug/* surface (and the handful of platform-scoped v2
// routes alongside it). Every by-id lookup or mutation (POST/PUT/DELETE)
// route under /api/v2/ must be either:
//   - swept: proven by a named cross-tenant/cross-account test (comment
//     names the test), or
//   - exempt: justified here with the specific reason no per-id ownership
//     check is needed (self-service, credential-is-the-resource, global
//     catalog entry, RootJWTAuth-gated platform admin, etc).
//
// A newly-added, unclassified route fails this test with an actionable
// message: you cannot ship one without either proving its isolation or
// documenting why it needs none.
//
// It lives in the router package (not handler) for the same reason as
// idor_completeness_test.go: it enumerates the real route table via
// SetApiV2Router/engine.Routes(), and a package-handler test importing the
// router package would form an import cycle (router imports handler).

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

func TestV2IDOR_Completeness(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiV2Router(engine)

	// Proven tenant/account-isolated by a named cross-tenant test. Key =
	// "METHOD PATH" (gin's registered path, with :params as gin writes them).
	swept := map[string]bool{
		// channels (internal/adapter/handler/v2_channel_idor_test.go)
		"GET /api/v2/:tenant_slug/channels/:id":                 true, // TestGetChannelV2_CrossTenantForbidden
		"PUT /api/v2/:tenant_slug/channels/:id":                 true, // TestUpdateChannelV2_CrossTenantForbidden
		"DELETE /api/v2/:tenant_slug/channels/:id":              true, // TestDeleteChannelV2_CrossTenantForbidden
		"POST /api/v2/:tenant_slug/channels/:id/test":           true, // TestChannelV2_CrossTenantForbidden
		"GET /api/v2/:tenant_slug/channels/:id/upstream-models": true, // TestFetchUpstreamModelsV2_ChannelCrossTenantForbidden
		// tokens (internal/adapter/handler/v2_token_test.go, v2_token_batch_test.go, v2_token_rotate_idor_test.go)
		"PUT /api/v2/:tenant_slug/tokens/:id":           true, // TestUpdateTokenV2_TenantMismatch
		"DELETE /api/v2/:tenant_slug/tokens/:id":        true, // TestDeleteTokenV2_NotOwned
		"POST /api/v2/:tenant_slug/tokens/:id/rotate":   true, // TestRotateTokenV2_TenantMismatch
		"POST /api/v2/:tenant_slug/tokens/batch-delete": true, // TestDeleteTokensV2_DeletesOwnedOnly
		// redemptions (internal/adapter/handler/v2_redemption_test.go)
		"DELETE /api/v2/:tenant_slug/redemptions/:id": true, // TestDeleteRedemptionV2_TenantVerification
		// projects — migration 029 cost attribution (internal/adapter/handler/v2_project_idor_test.go).
		// repo.GetProjectByID/UpdateProject/SoftDeleteProject all take tenantID
		// as a mandatory positional argument, so another tenant's id resolves to
		// ErrProjectNotFound -> 404, indistinguishable from a nonexistent id.
		"GET /api/v2/:tenant_slug/projects/:id":          true, // TestGetProjectV2_CrossTenantNotFound
		"PUT /api/v2/:tenant_slug/projects/:id":          true, // TestUpdateProjectV2_CrossTenantNotFound
		"DELETE /api/v2/:tenant_slug/projects/:id":       true, // TestDeleteProjectV2_CrossTenantNotFound
		"POST /api/v2/:tenant_slug/projects/:id/restore": true, // TestRestoreProjectV2_CrossTenantNotFound
		// playground presets (internal/adapter/handler/v2_cross_tenant_isolation_test.go)
		"DELETE /api/v2/:tenant_slug/playground/presets/:id": true, // TestDeletePresetV2_CrossTenantIsolation
		// pricing / self-profile (internal/adapter/handler/v2_cross_tenant_isolation_test.go)
		"POST /api/v2/:tenant_slug/pricing": true, // TestUpdatePricingV2_CrossTenantIsolation
		"PUT /api/v2/:tenant_slug/user/me":  true, // TestUpdateSelfV2_CrossTenantIsolation
		// billing checkout status ownership (internal/adapter/handler/v2_billing_checkout_status_idor_test.go)
		"GET /api/v2/user/billing/checkout/:order_no/status": true, // TestGetBillingCheckoutStatus_CrossAccountIDOR
	}

	// Not a cross-tenant/cross-account IDOR surface, each with the reason no
	// per-id ownership check is needed.
	exempt := map[string]string{
		// ---- self-service: acts on the caller's own session/identity, no addressable id ----
		"POST /api/v2/oauth/logout":                    "self-service: clears the authenticated caller's own session, no resource id",
		"POST /api/v2/oauth/refresh":                   "self-service: refreshes the authenticated caller's own session/tokens, no resource id",
		"POST /api/v2/auth/zita-logout":                "self-service: idempotent clear of the caller's own session/cookie, no resource id",
		"POST /api/v2/:tenant_slug/playground/run":     "self-service: runs the caller's own prompt against models, no stored per-id resource accessed",
		"POST /api/v2/:tenant_slug/chat/send":          "self-service: sends the caller's own message, no stored per-id resource",
		"DELETE /api/v2/:tenant_slug/sessions/current": "self-service: revokes the caller's own current session; literal \"current\" segment, not an :id param",
		"POST /api/v2/:tenant_slug/user/heartbeat":     "inline raw-token auth (UserHeartbeat resolves the caller's own Token.Key); self-scoped, no separate resource id",
		"POST /api/v2/user/billing/checkout":           "CreateBillingCheckout scopes to getIdentityAccountID(c) — the caller's own linked platform account; cannot target another account",

		// ---- create endpoints that stamp the caller's own tenant/user; cannot target another's ----
		"POST /api/v2/:tenant_slug/playground/presets": "CreatePresetV2 stamps the caller's own (tenant_id, user_id); cannot target another's",
		"POST /api/v2/:tenant_slug/tokens":             "CreateTokenV2 stamps the caller's own tenant_id/user_id from tenantCtx; cannot target another tenant",
		"POST /api/v2/:tenant_slug/channels":           "CreateChannelV2 stamps the caller's tenant_id from tenantCtx; cannot target another tenant",
		"POST /api/v2/:tenant_slug/redemptions":        "CreateRedemptionV2 stamps the caller's own tenant_id; cannot target another tenant",
		"POST /api/v2/:tenant_slug/projects":           "CreateProjectV2 stamps the caller's own tenant_id from tenantCtx; cannot target another tenant",
		"POST /api/v2/:tenant_slug/redeem":             "self-service: RedeemCodeV2 redeems into the caller's own balance (mirrors v1's POST /api/user/topup exemption)",

		// ---- credential-is-the-resource: the token/code presented IS the auth, mirrors v1's rationale ----
		"POST /api/v2/:tenant_slug/provision": "public entitlement-token exchange: the platform entitlement token (verified offline against the platform JWKS) is the credential",
		"POST /api/v2/switch/redeem":          "anonymous activation-code redemption (Phase D Track 2.1): the code itself is the credential, no authenticated caller to cross-tenant-check",
		"POST /api/v2/switch/heartbeat":       "inline raw-token auth (Token.Key); self-scoped to the token owner",
		"POST /api/v2/switch/reconciliation":  "inline raw-token auth; self usage-reconciliation for the token owner",
		"POST /api/v2/switch/user/topup":      "inline raw-token auth; self-service topup for the token owner",

		// ---- global catalog entities: no tenant_id column at all (verified: entity/model_meta.go) ----
		"POST /api/v2/:tenant_slug/models":       "CreateModelV2: entity.Model has no tenant_id column (global catalog); tenant_slug is validated only for route existence, never as a data filter — and because the write is global the handler gates on requirePlatformRoot, not tenant-admin",
		"DELETE /api/v2/:tenant_slug/models/:id": "DeleteModelV2: same — global catalog entry, mirrors v1's POST /api/channel/fix \"global maintenance, not a per-tenant data mutation\" exemption",

		// ---- conditionally-registered routes (only present under specific env/config; exempted defensively) ----
		"GET /api/v2/me/zita":              "guarded by common.ZitaClient.AuthMiddleware(); resolves the caller's own SDK identity, no resource id",
		"POST /api/v2/auth/zita-bootstrap": "guarded by common.ZitaClient.AuthMiddleware() + BootstrapRateLimit; self-service SDK identity bootstrap, no resource id",
		"POST /api/v2/bridge/exchange":     "registers only when E2E_BRIDGE_TOKEN is set (STAGE/CI); the bridge token itself is the credential, same class as switch/redeem",

		// ---- pure external proxy: no stored resource, no ownership concept ----
		"POST /api/v2/lutu/search": "PostWebSearch is a stateless proxy to Tavily search keyed only by the request body's free-text query; there is no stored per-tenant resource to leak cross-tenant via an id. " +
			"Gated by an explicit middleware.RequireOIDCToken() on the route (api-v2-router.go) — it previously registered with no effective auth (group OptionalZitaIdentity never aborts), which let any caller burn the " +
			"paid Tavily budget. The Lutu APP is a consumer OIDC identity (authenticates against identity.lurus.cn, no newhub tenant), so RequireOIDCToken — not TokenAuth or OIDCAuth — is the correct gate; see lutu_search_auth_test.go.",

		// ---- platform admin surface: RootJWTAuth-gated (single global root role, not tenant-scoped) ----
		// Root manages every tenant/user/mapping/option/audit record by design —
		// mirrors v1's RootAuth-gated exemption for POST /api/channel/:id/key.
		"POST /api/v2/admin/tenants":                                "RootJWTAuth-gated (admin_jwt_auth.go hasRole(roles,\"root\")): platform-wide tenant management is the endpoint's purpose",
		"GET /api/v2/admin/tenants/:id":                             "RootJWTAuth-gated: root manages every tenant by id, by design",
		"PUT /api/v2/admin/tenants/:id":                             "RootJWTAuth-gated: root manages every tenant by id, by design",
		"DELETE /api/v2/admin/tenants/:id":                          "RootJWTAuth-gated: root manages every tenant by id, by design",
		"POST /api/v2/admin/tenants/:id/enable":                     "RootJWTAuth-gated: root manages every tenant by id, by design",
		"POST /api/v2/admin/tenants/:id/disable":                    "RootJWTAuth-gated: root manages every tenant by id, by design",
		"POST /api/v2/admin/tenants/:id/suspend":                    "RootJWTAuth-gated: root manages every tenant by id, by design",
		"GET /api/v2/admin/tenants/:id/stats":                       "RootJWTAuth-gated: root reads stats for every tenant by id, by design",
		"GET /api/v2/admin/tenants/:id/model-limits":                "RootJWTAuth-gated (requireModelLimitTenant only 404s on a nonexistent tenant id): root manages every tenant's model limits by design",
		"PUT /api/v2/admin/tenants/:id/model-limits":                "RootJWTAuth-gated: root manages every tenant's model limits by design",
		"DELETE /api/v2/admin/tenants/:id/model-limits":             "RootJWTAuth-gated: root manages every tenant's model limits by design",
		"POST /api/v2/admin/tenants/:id/credit-pool":                "RootJWTAuth-gated: root manages every tenant's credit pool by design (ADR 2026-05-18 §4.1)",
		"GET /api/v2/admin/tenants/:id/credit-pool":                 "RootJWTAuth-gated: root manages every tenant's credit pool by design",
		"POST /api/v2/admin/tenants/:id/credit-pool/topup":          "RootJWTAuth-gated: root manages every tenant's credit pool by design",
		"GET /api/v2/admin/tenants/:id/credit-pool/usage":           "RootJWTAuth-gated: root manages every tenant's credit pool by design",
		"DELETE /api/v2/admin/tenants/:id/credit-pool":              "RootJWTAuth-gated: root manages every tenant's credit pool by design",
		"POST /api/v2/admin/tenants/:id/invites":                    "RootJWTAuth-gated: root mints onboarding invite codes for every tenant by design (N2)",
		"DELETE /api/v2/admin/tenants/:id/invites/:invite_id":       "RootJWTAuth-gated: repo.RevokeTenantInvite scopes by (id, tenant_id) itself — a code belonging to a different tenant 404s as not-found, same as the credit-pool group above",
		"GET /api/v2/admin/mappings/:id":                            "RootJWTAuth-gated: root reads platform user-identity mappings across every tenant by design",
		"DELETE /api/v2/admin/mappings/:id":                         "RootJWTAuth-gated: root manages platform user-identity mappings across every tenant by design",
		"PUT /api/v2/admin/users/:id":                               "RootJWTAuth-gated: root manages platform admin users across every tenant by design",
		"DELETE /api/v2/admin/users/:id":                            "RootJWTAuth-gated: root manages platform admin users across every tenant by design",
		"PUT /api/v2/admin/options":                                 "RootJWTAuth-gated: system-wide options, not a per-tenant resource",
		"POST /api/v2/admin/switch/presets":                         "RootJWTAuth-gated: platform-wide Switch presets, not a per-tenant resource",
		"GET /api/v2/admin/internal-keys/:id/tenants":               "RootJWTAuth-gated: :id addresses an internal_api_keys row (platform-wide credential), not a tenant's own data; root manages every key's whitelist by design",
		"POST /api/v2/admin/internal-keys/:id/tenants":              "RootJWTAuth-gated: same as above — grants a tenant onto a platform-wide internal API key, not a per-tenant mutation",
		"DELETE /api/v2/admin/internal-keys/:id/tenants/:tenant_id": "RootJWTAuth-gated: same as above — revokes a tenant from a platform-wide internal API key",
	}

	isMutation := func(m string) bool {
		return m == http.MethodPost || m == http.MethodPut || m == http.MethodDelete
	}
	// hasResourceID reports whether path carries a URL param other than
	// :tenant_slug (the tenant-scope selector, not a resource id).
	hasResourceID := func(path string) bool {
		for _, seg := range strings.Split(path, "/") {
			if strings.HasPrefix(seg, ":") && seg != ":tenant_slug" {
				return true
			}
		}
		return false
	}
	inScope := func(path string) bool {
		return strings.HasPrefix(path, "/api/v2/")
	}

	for _, rt := range engine.Routes() {
		if !inScope(rt.Path) {
			continue
		}
		if !hasResourceID(rt.Path) && !isMutation(rt.Method) {
			continue
		}
		key := rt.Method + " " + rt.Path
		if swept[key] {
			continue
		}
		if _, ok := exempt[key]; ok {
			continue
		}
		t.Errorf("tenant/account-scoped route %q is neither swept by a cross-tenant/cross-account test nor "+
			"exempted — add a cross-tenant test or an exemption with justification here", key)
	}
}
