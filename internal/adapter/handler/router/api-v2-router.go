package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// SetApiV2Router sets up v2 API routes.
// Admin operations use AdminJWTAuth; billing uses OIDCAuth.
func SetApiV2Router(router *gin.Engine) {
	apiV2 := router.Group("/api/v2")
	// Every other group (v1, dashboard, relay) wires middleware.CORS(); v2 was
	// missing it, so browser cross-origin calls (console SPA, Switch app) had
	// no Access-Control-* response headers and silently failed preflight.
	apiV2.Use(middleware.CORS())
	// Same rationale as api-router.go: DecompressRequestMiddleware only lands
	// on the relay router, which main.go wires up AFTER this group already
	// snapshotted its middleware chain, so it never reaches /api/v2. Cap the
	// body directly on this group. See body_size_limit.go.
	apiV2.Use(middleware.RequestBodySizeLimit())
	// Non-blocking SDK identity injector: resolves the lurus_session cookie
	// (set by the platform SDK bridge) into the context so middleware.UserAuth
	// / AdminAuth can admit SDK-authenticated console users whose only
	// credential is that cookie. Never aborts — public routes (switch, oauth)
	// and bearer/session paths are unaffected (ADR-0011 Layer C).
	apiV2.Use(middleware.OptionalZitaIdentity())
	{
		// ================================================================
		// OAuth / OIDC Routes (public — handles redirects & callbacks)
		// ================================================================

		apiV2.GET("/:tenant_slug/auth/login", handler.OIDCLoginRedirect)
		apiV2.GET("/oauth/callback", handler.OIDCCallback)
		apiV2.GET("/auth/session-info", handler.GetSessionInfo)
		apiV2.POST("/oauth/logout", handler.OIDCLogout)
		apiV2.POST("/oauth/refresh", handler.RefreshAccessToken)

		// ----------------------------------------------------------------
		// Zita SDK login path (ADR-0011 Layer C). Coexists with the
		// legacy OIDC-direct routes above during migration. Frontend
		// rewires to this URL in the next session; legacy deletion follows.
		// ----------------------------------------------------------------

		apiV2.GET("/auth/zita-login", handler.ZitaLogin)
		// Logout must be unauthenticated — the gin session may already be
		// expired (the whole point is to clear it + the platform cookie).
		apiV2.GET("/auth/zita-logout", handler.ZitaLogout)
		apiV2.POST("/auth/zita-logout", handler.ZitaLogout)
		if common.ZitaClient != nil {
			apiV2.GET("/me/zita", common.ZitaClient.AuthMiddleware(), handler.GetZitaIdentity)
			apiV2.POST("/auth/zita-bootstrap", middleware.BootstrapRateLimit(), common.ZitaClient.AuthMiddleware(), handler.ZitaBootstrap)
		}

		// E2E bridge: STAGE/CI only. Registers ONLY when env E2E_BRIDGE_TOKEN
		// is non-empty so the route does not exist in prod (defense in depth).
		// Same rate-limit bucket as zita-bootstrap — a brute-force attempt on
		// the bridge token is structurally identical to a bootstrap replay.
		if handler.BridgeEnabled() {
			apiV2.POST("/bridge/exchange", middleware.BootstrapRateLimit(), handler.BridgeExchange)
		}

		// Tenant-scoped user endpoint (session auth — called by frontend in V2 mode)
		apiV2.GET("/:tenant_slug/user/me", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.GetSelf)

		// EndUser pool readback (Tier 1.2, 2026-05-19). OIDCAuth →
		// tenantCtx.TenantID must match the URL slug; otherwise the handler
		// returns 403 TENANT_MISMATCH. Whitelisted projection: only
		// current_balance / max_balance / health are serialised.
		apiV2.GET("/:tenant_slug/credit-pool/me", middleware.OIDCAuth(), handler.GetCreditPoolForEndUser)

		// Playground multi-model fan-out (2026-05-19). Session auth — runs
		// the user's prompt against N models in parallel via in-process
		// self-HTTP loopback through /v1/chat/completions, reusing the
		// existing TokenAuth → Distribute → Relay pipeline. Per-column
		// errors do not fail the whole call (each cell carries its own
		// {content, latency_ms, prompt_tokens, completion_tokens, error_code}).
		apiV2.POST("/:tenant_slug/playground/run", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.PlaygroundFanOut)

		// Playground named presets (Wave 3 Phase 1). User-scoped CRUD —
		// each user manages their own preset list, isolated by (tenant, user).
		playgroundPresets := apiV2.Group("/:tenant_slug/playground/presets")
		playgroundPresets.Use(middleware.UserAuth())
		playgroundPresets.Use(middleware.TenantSlugGuard())
		{
			playgroundPresets.GET("", handler.ListPresetsV2)
			playgroundPresets.POST("", handler.CreatePresetV2)
			playgroundPresets.DELETE("/:id", handler.DeletePresetV2)
		}

		// ================================================================
		// Tenant-scoped Token Management (session auth)
		// ================================================================

		tenantTokens := apiV2.Group("/:tenant_slug/tokens")
		tenantTokens.Use(middleware.UserAuth())
		tenantTokens.Use(middleware.TenantSlugGuard())
		{
			tenantTokens.GET("", handler.ListTokensV2)
			tenantTokens.POST("", handler.CreateTokenV2)
			tenantTokens.POST("/batch-delete", handler.DeleteTokensV2)
			tenantTokens.PUT("/:id", handler.UpdateTokenV2)
			tenantTokens.DELETE("/:id", handler.DeleteTokenV2)
			tenantTokens.POST("/:id/rotate", handler.RotateTokenV2)
		}

		// ================================================================
		// Tenant-scoped cost-attribution Projects (migration 029)
		//
		// Mounted under UserAuth + TenantSlugGuard, like tokens/redemptions.
		// READS are open to every user in the tenant (the token page needs a
		// project picker an ordinary member can use); WRITES enforce
		// requireTenantAdmin INSIDE the handler, matching the redemptions
		// group. A project is a cost LABEL, not a permission boundary —
		// see internal/domain/entity/project.go.
		// ================================================================

		tenantProjects := apiV2.Group("/:tenant_slug/projects")
		tenantProjects.Use(middleware.UserAuth())
		tenantProjects.Use(middleware.TenantSlugGuard())
		{
			tenantProjects.GET("", handler.ListProjectsV2)
			// Static sibling of /:id — registered first so the intent is
			// obvious; gin's tree already mixes the two shapes in this file
			// (POST /tokens/batch-delete next to POST /tokens/:id/rotate).
			tenantProjects.GET("/spend", handler.GetProjectSpendV2)
			tenantProjects.GET("/:id", handler.GetProjectV2)
			tenantProjects.POST("", handler.CreateProjectV2)
			tenantProjects.PUT("/:id", handler.UpdateProjectV2)
			tenantProjects.DELETE("/:id", handler.DeleteProjectV2)
			// Undo for DELETE. Safe to replay: restoring a live project is a
			// no-op, and re-attachment skips tokens reassigned since.
			tenantProjects.POST("/:id/restore", handler.RestoreProjectV2)
		}

		// ================================================================
		// Tenant-scoped Channel Management (session auth — admin only)
		// ================================================================

		tenantChannels := apiV2.Group("/:tenant_slug/channels")
		tenantChannels.Use(middleware.AdminAuth())
		tenantChannels.Use(middleware.TenantSlugGuard())
		{
			tenantChannels.GET("", handler.ListChannelsV2)
			tenantChannels.GET("/:id", handler.GetChannelV2)
			tenantChannels.POST("", handler.CreateChannelV2)
			tenantChannels.PUT("/:id", handler.UpdateChannelV2)
			tenantChannels.DELETE("/:id", handler.DeleteChannelV2)
			tenantChannels.POST("/:id/test", handler.TestChannelV2)
			tenantChannels.GET("/:id/upstream-models", handler.FetchUpstreamModelsV2)
		}

		// ================================================================
		// Tenant-scoped Logs (session auth)
		// ================================================================

		tenantLogs := apiV2.Group("/:tenant_slug/logs")
		tenantLogs.Use(middleware.UserAuth())
		tenantLogs.Use(middleware.TenantSlugGuard())
		{
			tenantLogs.GET("", handler.GetLogsV2)
			tenantLogs.GET("/all", handler.GetAllLogsV2)
			tenantLogs.GET("/cluster", handler.GetLogClusterV2)
			// Aggregate header (RPM/TPM/total requests/total quota) over the
			// active filters — mirrors GetLogsV2's filter shape.
			tenantLogs.GET("/stat", handler.GetLogStatV2)
			// Tenant-wide stat (admin gate in the handler) — pairs with
			// GET /all so the header can summarise the same rows it lists.
			tenantLogs.GET("/stat/all", handler.GetAllLogStatV2)
			// Wave 3 Phase 2 (2026-05-20): CSV export with streaming writer
			// and a 50k-row hard cap (clamped silently above that).
			tenantLogs.GET("/export", handler.ExportLogsV2)
		}

		// ================================================================
		// Tenant-scoped Redemption Codes (Wave 3 Phase 2 — 2026-05-20)
		// List / Create / Delete enforce admin role inside the handler;
		// /redeem is the user-facing redemption endpoint.
		// ================================================================

		tenantRedemptions := apiV2.Group("/:tenant_slug/redemptions")
		tenantRedemptions.Use(middleware.UserAuth())
		tenantRedemptions.Use(middleware.TenantSlugGuard())
		{
			tenantRedemptions.GET("", handler.ListRedemptionsV2)
			tenantRedemptions.POST("", handler.CreateRedemptionV2)
			tenantRedemptions.DELETE("/:id", handler.DeleteRedemptionV2)
		}
		apiV2.POST("/:tenant_slug/redeem", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.RedeemCodeV2)

		// P4 unified provisioning: exchange a platform entitlement token
		// (verified OFFLINE against the platform JWKS) for a bounded relay
		// token. Public — the entitlement token is the credential; same
		// rate-limit bucket as zita-bootstrap (structurally identical
		// credential exchange).
		apiV2.POST("/:tenant_slug/provision", middleware.BootstrapRateLimit(), handler.ProvisionV2)

		tenantSessions := apiV2.Group("/:tenant_slug/sessions")
		tenantSessions.Use(middleware.UserAuth())
		tenantSessions.Use(middleware.TenantSlugGuard())
		{
			tenantSessions.GET("", handler.ListSessionsV2)
			tenantSessions.DELETE("/current", handler.RevokeCurrentSessionV2)
		}

		// ================================================================
		// Tenant-scoped Catalog & Pricing & Billing (Wave 2 — 2026-05-19)
		// Read-only projections wired from the v2 console. Write paths
		// (single-model edit, markup engine, PDF download, payment-method
		// edit) deferred per Wave 2 scope — UI carries mini WIPBanner.
		// ================================================================

		tenantModels := apiV2.Group("/:tenant_slug/models")
		tenantModels.Use(middleware.UserAuth())
		tenantModels.Use(middleware.TenantSlugGuard())
		{
			tenantModels.GET("", handler.ListModelsV2)
			// Wave 3 Phase 1 (2026-05-20): add / delete wired.
			// Single-model edit deferred to v3 per scope-cut.
			//
			// The catalogue is platform-global (entity.Model has no tenant_id),
			// so these two enforce requirePlatformRoot INSIDE the handler — the
			// group's UserAuth level is for the GET only. Do not "restore" them
			// to tenant-admin: v1 keeps the equivalent writes behind RootAuth.
			tenantModels.POST("", handler.CreateModelV2)
			tenantModels.DELETE("/:id", handler.DeleteModelV2)
		}

		tenantPricing := apiV2.Group("/:tenant_slug/pricing")
		tenantPricing.Use(middleware.UserAuth())
		tenantPricing.Use(middleware.TenantSlugGuard())
		{
			tenantPricing.GET("", handler.GetPricingV2)
			// Wave 3 Phase 1 (2026-05-20): markup write path. Writes the
			// process-wide ratio maps + the single global option row, so it
			// enforces requirePlatformRoot inside the handler (same rationale
			// as tenantModels above).
			tenantPricing.POST("", handler.UpdatePricingV2)
		}

		tenantBilling := apiV2.Group("/:tenant_slug/billing")
		tenantBilling.Use(middleware.UserAuth())
		tenantBilling.Use(middleware.TenantSlugGuard())
		{
			tenantBilling.GET("/invoices", handler.ListInvoicesV2)
			// Lost in 7835280f, which removed the tenant route group whole
			// while migrating admin auth; GetTopUpsV2 and its unit tests
			// stayed, and so did the console call. The v2 billing panel has
			// been asking for /billing/topups and getting a 404 ever since.
			// Same context and isolation as ListInvoicesV2 beside it: scoped
			// by tenantCtx.UserID and tenantCtx.TenantID, behind the same
			// UserAuth + TenantSlugGuard.
			//
			// POST /topup is NOT restored here. It moves money, it was
			// removed in the same commit, and its auth model changed
			// underneath it — re-opening a money path is an owner's call.
			tenantBilling.GET("/topups", handler.GetTopUpsV2)
		}

		// Chat single-model multi-turn — non-stream only v1; in-memory
		// conversation client-side (no chat_session table yet).
		tenantChat := apiV2.Group("/:tenant_slug/chat")
		tenantChat.Use(middleware.UserAuth())
		tenantChat.Use(middleware.TenantSlugGuard())
		{
			tenantChat.POST("/send", handler.ChatSend)
		}

		// Settings — PUT for profile update (GET already registered above)
		apiV2.PUT("/:tenant_slug/user/me", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.UpdateSelfV2)

		// ================================================================
		// Switch Public Routes (no authentication required)
		// ================================================================

		switchGroup := apiV2.Group("/switch")
		{
			switchGroup.GET("/tools/versions", handler.GetToolVersions)
			switchGroup.GET("/presets", handler.ListSwitchPresets)
			// Phase D Track 2.1: anonymous activation-code redemption (no auth)
			switchGroup.POST("/redeem", handler.SwitchRedeemAnonymous)
			// Phase D Track 2.2: single-tenant fallback heartbeat (inline raw-token auth)
			switchGroup.POST("/heartbeat", handler.UserHeartbeat)
			// Wave 1 W1.1: public rate card for the Switch cost dashboard.
			switchGroup.GET("/pricing", handler.GetSwitchPricing)
			// Wave 1 W1.2: usage reconciliation (inline raw-token auth).
			switchGroup.POST("/reconciliation", handler.SwitchReconciliation)
			// Quota/identity snapshot for the token owner (inline raw-token
			// auth) — backs the Switch billing quota card.
			switchGroup.GET("/user/info", handler.GetSwitchUserInfo)
			// Redemption-code topup for the token owner (inline raw-token
			// auth) — lets a Switch client credit its own account without
			// an OIDC session (middleware.UserAuth() would reject it).
			switchGroup.POST("/user/topup", handler.SwitchUserTopup)
			// CN-survivable self-update mirror: latest Switch desktop release
			// (admin-published via switch_app.* options; 404 = unpublished,
			// client falls back to GitHub Releases).
			switchGroup.GET("/app/releases/latest", handler.GetSwitchAppRelease)
		}

		// Admin-published relay recommendations for Switch clients (public,
		// options-driven; bare-array contract, see GetRecommendedRelays).
		apiV2.GET("/relays/recommended", handler.GetRecommendedRelays)

		// Phase D Track 2.2: tenant-scoped heartbeat — sibling of /:tenant_slug/user/me.
		// No middleware: UserHeartbeat does inline raw-token (Token.Key) auth,
		// which middleware.UserAuth (access-token based) would otherwise reject.
		apiV2.POST("/:tenant_slug/user/heartbeat", handler.UserHeartbeat)

		apiV2.GET("/tools/download-manifest", handler.GetToolDownloadManifest)

		// ================================================================
		// Platform User Routes (OIDC JWT auth)
		// ================================================================

		platformUser := apiV2.Group("/user")
		platformUser.Use(middleware.OIDCAuth())
		{
			platformUser.GET("/identity-overview", handler.GetIdentityOverview)

			billingRoute := platformUser.Group("/billing")
			{
				billingRoute.GET("/summary", handler.GetBillingSummary)
				billingRoute.GET("/payment-methods", handler.GetBillingPaymentMethods)
				billingRoute.POST("/checkout", handler.CreateBillingCheckout)
				billingRoute.GET("/checkout/:order_no/status", handler.GetBillingCheckoutStatus)
			}
		}

		// ================================================================
		// Platform Admin Routes (AdminJWTAuth with root role)
		// ================================================================

		adminRoute := apiV2.Group("/admin")
		adminRoute.Use(middleware.RootJWTAuth())
		{
			tenantMgmt := adminRoute.Group("/tenants")
			{
				tenantMgmt.GET("", handler.ListTenants)
				tenantMgmt.POST("", handler.CreateTenant)
				tenantMgmt.GET("/:id", handler.GetTenant)
				tenantMgmt.PUT("/:id", handler.UpdateTenant)
				tenantMgmt.DELETE("/:id", handler.DeleteTenant)
				tenantMgmt.POST("/:id/enable", handler.EnableTenant)
				tenantMgmt.POST("/:id/disable", handler.DisableTenant)
				tenantMgmt.POST("/:id/suspend", handler.SuspendTenant)
				tenantMgmt.GET("/:id/stats", handler.GetTenantStats)

				// Per-model rate limits (migration 026). DELETE takes the
				// model as ?model= — model names contain '/' (vendor/model),
				// which a path parameter cannot carry.
				tenantMgmt.GET("/:id/model-limits", handler.ListTenantModelLimits)
				tenantMgmt.PUT("/:id/model-limits", handler.UpsertTenantModelLimit)
				tenantMgmt.DELETE("/:id/model-limits", handler.DeleteTenantModelLimit)

				// Reseller credit-pool admin (ADR 2026-05-18 §4.1)
				tenantMgmt.POST("/:id/credit-pool", handler.CreateCreditPool)
				tenantMgmt.GET("/:id/credit-pool", handler.GetCreditPool)
				tenantMgmt.POST("/:id/credit-pool/topup", handler.TopupCreditPool)
				tenantMgmt.GET("/:id/credit-pool/usage", handler.ListCreditPoolUsage)
				tenantMgmt.DELETE("/:id/credit-pool", handler.DeleteCreditPool)
			}

			mappingRoute := adminRoute.Group("/mappings")
			{
				mappingRoute.GET("", handler.ListUserMappingsV2)
				mappingRoute.GET("/:id", handler.GetUserMappingV2)
				mappingRoute.DELETE("/:id", handler.DeleteUserMappingV2)
			}

			// Internal API key tenant whitelist console (internal_api_key_tenants,
			// migration 013/021 §1) — previously only manageable by hand-writing
			// SQL. Key creation itself stays at POST /api/api-keys (v1,
			// handler.AdminCreateApiKey); this group only lists key metadata
			// (never key_hash) and manages the per-tenant whitelist.
			// Rate-limited like govRoute below: granting/revoking cross-tenant
			// reach for an internal key is at least as sensitive as the
			// audit-export/chain-verify/logs-export routes further down.
			internalKeysRoute := adminRoute.Group("/internal-keys")
			internalKeysRoute.Use(middleware.CriticalRateLimit())
			{
				internalKeysRoute.GET("", handler.ListInternalApiKeysV2)
				internalKeysRoute.GET("/:id/tenants", handler.ListInternalApiKeyTenantsV2)
				internalKeysRoute.POST("/:id/tenants", handler.GrantInternalApiKeyTenantV2)
				internalKeysRoute.DELETE("/:id/tenants/:tenant_id", handler.RevokeInternalApiKeyTenantV2)
			}

			// Platform user management (deferred backlog round 2). Create is
			// deferred (needs a password/invite flow) — the UI greys it.
			adminUsers := adminRoute.Group("/users")
			{
				adminUsers.GET("", handler.ListAdminUsersV2)
				adminUsers.PUT("/:id", handler.UpdateAdminUserV2)
				adminUsers.DELETE("/:id", handler.DeleteAdminUserV2)
			}

			// System options panels (read + one-key-per-call write). Thin
			// wrappers over GetOptions/UpdateOption (secret filtering + per-key
			// validation + audit live there).
			adminRoute.GET("/options", handler.ListAdminOptionsV2)
			adminRoute.PUT("/options", handler.UpdateAdminOptionV2)

			adminRoute.GET("/stats", handler.GetSystemStatsV2)
			adminRoute.POST("/switch/presets", handler.CreateSwitchPreset)
			// Phase D Track 2.3: white-label HMAC key derivation for Switch
			// installer signing. Tenant slug arrives via ?tenant_slug= query
			// (adminRoute is platform-scoped, not :tenant_slug-bound).
			adminRoute.GET("/whitelabel/hmac-key", handler.GetWhiteLabelHMACKey)

			// Governance (rate-limited: heavy aggregation queries)
			govRoute := adminRoute.Group("/governance")
			govRoute.Use(middleware.CriticalRateLimit())
			{
				govRoute.GET("/channels", handler.GetGovernanceChannelDistribution)
				govRoute.GET("/fingerprints", handler.GetGovernanceFingerprintStats)
				govRoute.GET("/latency", handler.GetGovernanceLatencyStats)
				govRoute.GET("/efficiency", handler.GetGovernanceEfficiencyStats)
				// Phase 1: cost-aware-routing savings analyzer (read-only).
				govRoute.GET("/savings", handler.GetGovernanceSavings)
			}
			adminRoute.GET("/audit/events", middleware.CriticalRateLimit(), handler.GetAuditEvents)
			adminRoute.GET("/audit/actions", handler.ListAuditActionsV2)
			adminRoute.GET("/audit/export", middleware.CriticalRateLimit(), handler.ExportAuditEventsV2)
			// Tamper-evidence hash-chain verification (migration 024).
			adminRoute.GET("/audit/chain-verify", middleware.CriticalRateLimit(), handler.VerifyAuditChainV2)

			// Live routing health: per-channel circuit-breaker state as this
			// replica sees it. Read-only and side-effect free.
			adminRoute.GET("/gateway/health", handler.GetGatewayHealthV2)

			// Model performance analytics + platform-wide usage-log CSV export
			// (rate-limited: heavy aggregation / bulk row scans over logs).
			adminRoute.GET("/analytics/model-performance", middleware.CriticalRateLimit(), handler.GetModelPerformanceV2)
			adminRoute.GET("/logs/export", middleware.CriticalRateLimit(), handler.ExportAdminLogsV2)
		}

		// ================================================================
		// Lutu APP integration — server-side web search (Tavily proxy).
		// Used by the Lutu Flutter APP to give chat models a `web_search`
		// tool without exposing the Tavily API key to clients. Gated by
		// RequireOIDCToken: the Lutu APP is a consumer OIDC identity (it
		// authenticates against identity.lurus.cn, not with an sk- relay
		// token), so a valid OIDC JWT is the bearer it already attaches —
		// required here so the shared Tavily quota can't be drained
		// anonymously. NOT OIDCAuth: Lutu users have no newhub tenant, so
		// its tenant mapping would 500 / pollute the tenant tables.
		// ================================================================
		apiV2.POST("/lutu/search", middleware.RequireOIDCToken(), handler.PostWebSearch)
	}
}
