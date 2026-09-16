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
		// per-device session registry (L7, internal/adapter/handler/v2_session_revoke_test.go).
		// RevokeSessionByIDV2/repo.GetUserSessionByID mirror ConsumeTenantInvite's
		// not-found-shaped IDOR pattern: a row belonging to a different user 404s
		// exactly like a nonexistent id.
		"DELETE /api/v2/:tenant_slug/sessions/:id": true, // TestV2SessionRevokeByID_NotOwned404
		// pricing / self-profile (internal/adapter/handler/v2_cross_tenant_isolation_test.go)
		"POST /api/v2/:tenant_slug/pricing": true, // TestUpdatePricingV2_CrossTenantIsolation
		"PUT /api/v2/:tenant_slug/user/me":  true, // TestUpdateSelfV2_CrossTenantIsolation
		// billing checkout status ownership (internal/adapter/handler/v2_billing_checkout_status_idor_test.go)
		"GET /api/v2/user/billing/checkout/:order_no/status": true, // TestGetBillingCheckoutStatus_CrossAccountIDOR
		// chat session persistence — migration 038, cycle-10 L3
		// (router/chat_sessions_real_chain_test.go). GetChatSessionOwned/
		// UpdateChatSessionOwned/DeleteChatSessionOwned (repo/chat_session.go)
		// all filter by (id, tenant_id, user_id) in one WHERE clause, so
		// another user's session id and a nonexistent one 404 identically.
		"GET /api/v2/:tenant_slug/chat/sessions/:id":    true, // TestChatSessionsRealChain_OwnershipFailClosed_SameShape
		"PATCH /api/v2/:tenant_slug/chat/sessions/:id":  true, // TestChatSessionsRealChain_OwnershipFailClosed_SameShape
		"DELETE /api/v2/:tenant_slug/chat/sessions/:id": true, // TestChatSessionsRealChain_OwnershipFailClosed_SameShape
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
		"DELETE /api/v2/:tenant_slug/sessions/others":  "self-service: RevokeOtherSessionsV2 revokes only the caller's OWN other sessions (repo.RevokeOtherUserSessions scopes by the caller's user_id); literal \"others\" segment, not an :id param",
		"POST /api/v2/:tenant_slug/user/heartbeat":     "inline raw-token auth (UserHeartbeat resolves the caller's own Token.Key); self-scoped, no separate resource id",
		"POST /api/v2/user/billing/checkout":           "CreateBillingCheckout scopes to getIdentityAccountID(c) — the caller's own linked platform account; cannot target another account",

		// ---- create endpoints that stamp the caller's own tenant/user; cannot target another's ----
		"POST /api/v2/:tenant_slug/playground/presets": "CreatePresetV2 stamps the caller's own (tenant_id, user_id); cannot target another's",
		"POST /api/v2/:tenant_slug/tokens":             "CreateTokenV2 stamps the caller's own tenant_id/user_id from tenantCtx; cannot target another tenant",
		"POST /api/v2/:tenant_slug/channels":           "CreateChannelV2 stamps the caller's tenant_id from tenantCtx; cannot target another tenant",
		"POST /api/v2/:tenant_slug/redemptions":        "CreateRedemptionV2 stamps the caller's own tenant_id; cannot target another tenant",
		"POST /api/v2/:tenant_slug/projects":           "CreateProjectV2 stamps the caller's own tenant_id from tenantCtx; cannot target another tenant",
		"POST /api/v2/:tenant_slug/redeem":             "self-service: RedeemCodeV2 redeems into the caller's own balance (mirrors v1's POST /api/user/topup exemption)",
		"POST /api/v2/:tenant_slug/chat/sessions":      "CreateChatSessionV2 stamps the caller's own (tenant_id, user_id) from tenantCtx (migration 038, cycle-10 L3); cannot target another tenant or user",

		// ---- credential-is-the-resource: the token/code presented IS the auth, mirrors v1's rationale ----
		"POST /api/v2/:tenant_slug/provision": "public entitlement-token exchange: the platform entitlement token (verified offline against the platform JWKS) is the credential",
		"POST /api/v2/switch/redeem":          "anonymous activation-code redemption (Phase D Track 2.1): the code itself is the credential, no authenticated caller to cross-tenant-check",
		"POST /api/v2/switch/heartbeat":       "inline raw-token auth (Token.Key); self-scoped to the token owner",
		"POST /api/v2/switch/reconciliation":  "inline raw-token auth; self usage-reconciliation for the token owner",
		"POST /api/v2/switch/user/topup":      "inline raw-token auth; self-service topup for the token owner",

		// ---- global catalog entities: no tenant_id column at all (verified: entity/model_meta.go) ----
		"POST /api/v2/:tenant_slug/models":       "CreateModelV2: entity.Model has no tenant_id column (global catalog); tenant_slug is validated only for route existence, never as a data filter — and because the write is global the handler gates on requirePlatformRoot, not tenant-admin",
		"DELETE /api/v2/:tenant_slug/models/:id": "DeleteModelV2: same — global catalog entry, mirrors v1's POST /api/channel/fix \"global maintenance, not a per-tenant data mutation\" exemption",

		// ---- read-only preview: computes a diff, never persists ----
		"POST /api/v2/:tenant_slug/pricing/preview": "PreviewPricingV2: read-only dry-run of the same batch UpdatePricingV2 accepts — does not call repo.UpdateOption and does not bump PricingVersion (TestV2PricingPreview_NeverPersists); root-gated (requirePlatformRoot) for the same reason as the write route above",

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
		"GET /api/v2/admin/tenants/:id/model-allowlist":             "RootJWTAuth-gated (requireModelLimitTenant only 404s on a nonexistent tenant id): root manages every tenant's model allow-list by design",
		"PUT /api/v2/admin/tenants/:id/model-allowlist":             "RootJWTAuth-gated: root manages every tenant's model allow-list by design; the sole writer of the tenant_configs models.allowlist row",
		"DELETE /api/v2/admin/tenants/:id/model-allowlist":          "RootJWTAuth-gated: root manages every tenant's model allow-list by design",
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
		"DELETE /api/v2/admin/users/:id/sessions":                   "RootJWTAuth-gated: same class as /admin/users/:id above — root revokes any user's per-device sessions by design (L7 compromised-account runbook step), not a per-tenant ownership check",
		"PUT /api/v2/admin/options":                                 "RootJWTAuth-gated: system-wide options, not a per-tenant resource",
		"POST /api/v2/admin/switch/presets":                         "RootJWTAuth-gated: platform-wide Switch presets, not a per-tenant resource",
		"GET /api/v2/admin/internal-keys/:id/tenants":               "RootJWTAuth-gated: :id addresses an internal_api_keys row (platform-wide credential), not a tenant's own data; root manages every key's whitelist by design",
		"POST /api/v2/admin/internal-keys/:id/tenants":              "RootJWTAuth-gated: same as above — grants a tenant onto a platform-wide internal API key, not a per-tenant mutation",
		"DELETE /api/v2/admin/internal-keys/:id/tenants/:tenant_id": "RootJWTAuth-gated: same as above — revokes a tenant from a platform-wide internal API key",

		// Session-affinity purge (L5, routing-resilience-limits-11). Bindings
		// live in session_affinity.go's own Redis/in-process store, not a
		// tenant table — root purges any binding by design, same class as the
		// internal-keys/tenants routes above.
		"DELETE /api/v2/admin/routing/affinity":      "RootJWTAuth-gated: bulk purge-all of session-affinity bindings (requires ?all=true — PurgeAllAffinityBindingsV2 400s without it); a process-wide cache-steering optimisation, not a per-tenant resource",
		"DELETE /api/v2/admin/routing/affinity/:key": "RootJWTAuth-gated: :key addresses one HMAC-derived affinity binding (session_affinity.go), not a tenant's own data — root purges any binding by design",

		// TOTP admin escape hatch (L6, auth-security-06/07/25). :id addresses
		// a user_totps row across every tenant, same class as
		// /admin/users/:id above — root manages any user's 2FA enrollment by
		// design, additionally gated by SecureVerificationRequired (the
		// acting root's own step-up), not a per-tenant ownership check.
		"POST /api/v2/admin/security/users/:id/totp/force-disable": "RootJWTAuth-gated: root manages any user's TOTP enrollment by design, same class as /admin/users/:id; additionally requires the acting root's own SecureVerificationRequired step-up wired on the real route (TestSetApiV2Router_ForceDisableTotp_RequiresOwnStepUp in router/v2_admin_security_wiring_test.go — TestAdminTotpForceDisable_RequiresStepUp hand-mounts the middleware and stays green if the real route loses it)",

		// Delegated admin permission grants (L4, auth-security-17/18,
		// console-ux-36). Grant MANAGEMENT (who holds a grant) is
		// RootJWTAuth-gated under adminRoute — only root decides who gets
		// delegated access, mirrors every other /admin/* write above.
		// Grants themselves are GLOBAL this cycle (CreateGrantV2 rejects a
		// non-null tenant_id as GRANT_INVALID), so there is no tenant
		// dimension to isolate. What a grant UNLOCKS (the four audit GETs,
		// auditRoute) is a SEPARATE, narrower RootOrGranted gate, proven by
		// TestRootOrGranted_* in the middleware package and
		// TestAuditRoutes_MountedUnderRootOrGranted here.
		"POST /api/v2/admin/authz/grants":       "RootJWTAuth-gated: root mints delegated permission grants for any user by design; grants are global (no tenant_id) this cycle",
		"DELETE /api/v2/admin/authz/grants/:id": "RootJWTAuth-gated: root revokes any delegated permission grant by design, same not-found-shaped 404 for absent/already-revoked ids as RevokeTenantInvite above",
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
