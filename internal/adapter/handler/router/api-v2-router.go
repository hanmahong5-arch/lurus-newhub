package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/gzip"
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
	// Cycle-12 L4: CSRF origin guard. Directly after CORS and before anything
	// that reads the session, so a cross-site cookie-authenticated write is
	// refused before any handler or auth lookup runs. It skips GET/HEAD/OPTIONS,
	// any request carrying Authorization or X-API-Key, and any request with no
	// session cookie — POST /api/v2/bridge/exchange (no cookie inbound) is
	// unaffected. Decision table and CSRF_ORIGIN_GUARD_MODE: middleware/browser_origin_guard.go.
	apiV2.Use(middleware.BrowserOriginGuard())
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
	// GlobalV2RateLimit ("GV" bucket): see middleware/rate-limit-v2.go's doc
	// comment for the budget this default is sized against. It must be
	// mounted here, directly on apiV2, because gin.Group() snapshots its
	// parent's middleware chain at the moment a child group is created —
	// SetWebRouter's GlobalWebRateLimit ("GW") never reaches this one either
	// way: since cycle 12 L2 it is not a group middleware at all, but is called
	// inside that router's NoRoute handler, on the SPA HTML document alone.
	apiV2.Use(middleware.GlobalV2RateLimit())
	// Response compression for the console's JSON. gin-contrib/gzip buffers a
	// streamed response whole, so the three CSV exports are excluded — they
	// write row by row:
	//   GET /api/v2/:tenant_slug/logs/export  (tenantLogs.GET("/export"), :208)
	//   GET /api/v2/admin/logs/export         (:560)
	//   GET /api/v2/admin/audit/export        (:617)
	// One anchored regex covers all three: "admin" also matches [^/]+ in the
	// first alternative.
	//
	// NOTE for whoever edits this: in gin-contrib/gzip v0.0.6 newGzipHandler
	// sets `Options: DefaultOptions` — a PACKAGE-LEVEL pointer — and then
	// applies the option setters to it (handler.go:20-26), so an exclusion
	// declared here is also in effect for SetWebRouter's gzip instance, which
	// is constructed later (router/main.go: SetApiV2Router :46, SetWebRouter
	// :58). Harmless today, because the SPA router never sees an /api/v2 path —
	// but an exclusion is NOT scoped to the mount that declares it. Note also
	// that WithExcludedPaths matches by PREFIX (options.go: strings.HasPrefix)
	// while WithExcludedPathsRegexs uses MatchString, hence the ^...$ anchors.
	apiV2.Use(gzip.Gzip(
		gzip.DefaultCompression,
		gzip.WithExcludedPathsRegexs([]string{`^/api/v2/(?:[^/]+/logs|admin/audit)/export$`}),
	))
	{
		// ================================================================
		// OAuth / OIDC Routes (public — handles redirects & callbacks)
		// ================================================================

		apiV2.GET("/:tenant_slug/auth/login", handler.OIDCLoginRedirect)
		apiV2.GET("/oauth/callback", handler.OIDCCallback)
		apiV2.GET("/auth/session-info", handler.GetSessionInfo)
		apiV2.POST("/oauth/logout", handler.OIDCLogout)

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

		// Fault-injection upstream: UAT only, same env-gated construction as
		// the bridge above — registered ONLY when FAULTSIM_TOKEN is non-empty,
		// so in production the route does not exist at all. See faultsim.go for
		// why this has to exist (mid-stream abandonment, breaker transitions and
		// failover suppression cannot be proven live without an upstream that
		// can be made to die) and for the three independent safety properties.
		//
		// Mounted under /api/v2 rather than at the root so it inherits this
		// group's body-size cap; it is NOT under /:tenant_slug because it is
		// not tenant data — it is a stand-in for a provider.
		if handler.FaultSimEnabled() {
			apiV2.POST("/faultsim/v1/chat/completions", handler.FaultSimChatCompletions)
			// Task-vendor fault simulator (cycle-8 L8): imitates the Suno
			// wire so a UAT channel (type ChannelTypeSunoAPI, base_url
			// http://127.0.0.1:3000/api/v2/faultsim, key=FAULTSIM_TOKEN)
			// can prove POST/GET /v1/tasks/:platform end-to-end with no
			// vendor key. See faultsim.go for the shared safety properties.
			apiV2.POST("/faultsim/suno/submit/:action", handler.FaultSimTaskSubmit)
			apiV2.POST("/faultsim/suno/fetch", handler.FaultSimTaskFetch)
		}

		// Tenant-scoped user endpoint (session auth — called by frontend in V2 mode).
		// GetSelfV2, not the v1 GetSelf: the v1 projection has no remaining_quota
		// and no token_count, so the v2 dashboard's "remaining quota" card fell
		// through to its unlimited-plan branch (telling a metered customer they
		// were on an unlimited plan) and its "active keys" card was pinned at 0,
		// which also kept the "you have no keys yet" onboarding banner up while
		// the keys page listed keys. GetSelfV2 is a strict superset of GetSelf —
		// see the comment on its response body.
		apiV2.GET("/:tenant_slug/user/me", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.GetSelfV2)

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
		// AdminSessionAuth, not AdminAuth: the v1 refusal shape is HTTP 200
		// {success:false} and a browser reads that as success. Cycle 13 L7/W
		// routes the role-10 shortfall through the same rewrite RootAuth's
		// admin routes use, so a plain member gets 403 PERMISSION_DENIED and
		// the console can tell "refused" from "no channels".
		tenantChannels.Use(middleware.AdminSessionAuth())
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
			tenantLogs.GET("/stat", middleware.CriticalRateLimit(), handler.GetLogStatV2)
			// Tenant-wide stat (admin gate in the handler) — pairs with
			// GET /all so the header can summarise the same rows it lists.
			tenantLogs.GET("/stat/all", middleware.CriticalRateLimit(), handler.GetAllLogStatV2)
			// Wave 3 Phase 2 (2026-05-20): CSV export with streaming writer
			// and a 50k-row hard cap (clamped silently above that).
			tenantLogs.GET("/export", middleware.CriticalRateLimit(), handler.ExportLogsV2)
		}

		// ================================================================
		// Tenant-scoped Analytics (L4, 2026-09-12) — model/vendor/group
		// rankings leaderboard. Admin gate lives inside the handler (requireTenantAdmin),
		// same pattern as GetAllLogStatV2 above; CriticalRateLimit here mirrors
		// the root route (two GROUP BY aggregates per cache miss — a hit
		// within the 5-minute in-process cache runs no query at all).
		// ================================================================

		tenantAnalytics := apiV2.Group("/:tenant_slug/analytics")
		tenantAnalytics.Use(middleware.UserAuth())
		tenantAnalytics.Use(middleware.TenantSlugGuard())
		{
			tenantAnalytics.GET("/rankings", middleware.CriticalRateLimit(), handler.GetTenantRankingsV2)
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
		// RedemptionRateLimit (5/60s/IP, "RD" bucket) guards code-guessing —
		// see the identical rationale on the v1-compat POST /api/user/topup
		// mount in api-router.go (cycle-8 L1).
		apiV2.POST("/:tenant_slug/redeem", middleware.UserAuth(), middleware.TenantSlugGuard(), middleware.RedemptionRateLimit(), handler.RedeemCodeV2)

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
			// L7 per-device session registry (SESSION_REGISTRY_ENABLED).
			// "/others" is a literal path segment, registered ahead of the
			// "/:id" wildcard below so it is never swallowed by it.
			tenantSessions.DELETE("/others", handler.RevokeOtherSessionsV2)
			tenantSessions.DELETE("/:id", handler.RevokeSessionByIDV2)
		}

		// ================================================================
		// Tenant-scoped Catalog & Pricing & Billing.
		// Projections wired from the v2 console. Still not implemented here:
		// single-model edit, the markup engine, invoice PDF download and
		// payment-method edit. The console no longer advertises those with a
		// banner; per-model availability is administered through
		// /api/v2/admin/tenants/:id/model-allowlist instead.
		// ================================================================

		tenantModels := apiV2.Group("/:tenant_slug/models")
		tenantModels.Use(middleware.UserAuth())
		tenantModels.Use(middleware.TenantSlugGuard())
		{
			tenantModels.GET("", handler.ListModelsV2)
			// Add and delete are wired; editing a single model is not.
			//
			// The catalogue is platform-global (entity.Model has no tenant_id),
			// so these two enforce requirePlatformRoot INSIDE the handler — the
			// group's UserAuth level is for the GET only. Do not "restore" them
			// to tenant-admin: v1 keeps the equivalent writes behind RootAuth.
			tenantModels.POST("", handler.CreateModelV2)
			tenantModels.DELETE("/:id", handler.DeleteModelV2)
			tenantModels.GET("/routable", handler.ListRoutableModelsV2)
			// Per-model latency / error rate for this tenant (cycle 16):
			// quality for every member, volume for tenant admins only.
			tenantModels.GET("/performance", handler.ListModelPerformanceV2)
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
			// L1 (2026-09-12): dry-run diff of the same batch — does not call
			// repo.UpdateOption or bump PricingVersion (TestV2PricingPreview_
			// NeverPersists), same root gate (the maps it reads are
			// process-global, not tenant-scoped, same rationale as the write
			// above).
			tenantPricing.POST("/preview", handler.PreviewPricingV2)
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

		// Chat single-model multi-turn — non-stream only v1 (see
		// handler.ChatSend's doc comment for why). /sessions[/:id]
		// (migration 038, cycle-10 L3) is the client-driven persistence of
		// a conversation /send already ran; ownership is fail-closed the
		// same way tasks/logs are — see v2_chat_session.go's header.
		tenantChat := apiV2.Group("/:tenant_slug/chat")
		tenantChat.Use(middleware.UserAuth())
		tenantChat.Use(middleware.TenantSlugGuard())
		{
			tenantChat.POST("/send", handler.ChatSend)
			tenantChat.GET("/sessions", handler.ListChatSessionsV2)
			tenantChat.POST("/sessions", handler.CreateChatSessionV2)
			tenantChat.GET("/sessions/:id", handler.GetChatSessionV2)
			tenantChat.PATCH("/sessions/:id", handler.UpdateChatSessionV2)
			tenantChat.DELETE("/sessions/:id", handler.DeleteChatSessionV2)
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
			// Phase D Track 2.1: anonymous activation-code redemption (no auth).
			// Anonymous is exactly why this needs middleware.RedemptionRateLimit()
			// (5/60s/IP, "RD" bucket) — there is no user/token identity to key a
			// limiter on for this route, only the caller's IP (cycle-8 L1).
			switchGroup.POST("/redeem", middleware.RedemptionRateLimit(), handler.SwitchRedeemAnonymous)
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
			// RedemptionRateLimit (5/60s/IP, "RD" bucket) guards code-guessing
			// here too (cycle-8 L1).
			switchGroup.POST("/user/topup", middleware.RedemptionRateLimit(), handler.SwitchUserTopup)
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
		// L2 audit-completeness: fail-closed backstop for every write on this
		// group — records a typed admin.write_unaudited fallback event (and
		// increments lurus_gateway_admin_write_unaudited_total) for any
		// mutating request whose handler never called
		// governance.RecordAuditEvent. Mounted after RootJWTAuth so the
		// fallback's actor attribution can read the "id"/"admin_sub" context
		// keys that auth sets.
		adminRoute.Use(middleware.AuditWriteGuard())
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

				// Per-tenant model allow-list (observe-first, typed 403 under
				// enforce; internal/app/tenantpolicy). Sole writer of the
				// tenant_configs "models.allowlist" row.
				tenantMgmt.GET("/:id/model-allowlist", handler.ListTenantModelAllowlist)
				tenantMgmt.PUT("/:id/model-allowlist", handler.UpsertTenantModelAllowlist)
				tenantMgmt.DELETE("/:id/model-allowlist", handler.DeleteTenantModelAllowlist)

				// Reseller credit-pool admin (ADR 2026-05-18 §4.1)
				tenantMgmt.POST("/:id/credit-pool", handler.CreateCreditPool)
				tenantMgmt.GET("/:id/credit-pool", handler.GetCreditPool)
				tenantMgmt.POST("/:id/credit-pool/topup", handler.TopupCreditPool)
				tenantMgmt.GET("/:id/credit-pool/usage", handler.ListCreditPoolUsage)
				tenantMgmt.DELETE("/:id/credit-pool", handler.DeleteCreditPool)

				// Tenant invite codes (N2) — root-issued one-time onboarding
				// codes consumed by handler.ZitaBootstrap's auto-create branch
				// (?invite=<code>) to land a new zita-bridge user in this
				// tenant instead of "default".
				tenantMgmt.POST("/:id/invites", handler.IssueTenantInvite)
				tenantMgmt.GET("/:id/invites", handler.ListTenantInvites)
				tenantMgmt.DELETE("/:id/invites/:invite_id", handler.RevokeTenantInvite)
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
				// L7: "compromised account" runbook step — revoke every
				// per-device session of a user (SESSION_REGISTRY_ENABLED).
				adminUsers.DELETE("/:id/sessions", handler.RevokeUserSessionsAdminV2)
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
			// L4 (2026-09-13): the four audit-feed GETs moved OFF adminRoute
			// onto auditRoute (below, outside this block) — RootOrGranted, not
			// RootJWTAuth, so a delegated audit:read grant can reach them
			// without root. /audit/coverage stays here: it is L2's own
			// admin/internal-admin write-audit COVERAGE report, a different
			// surface from the audit EVENT feed the grant unlocks.
			//
			// L2 audit-completeness: explicit-vs-fallback coverage of the
			// admin/internal-admin write surface (audit_coverage_gen.go).
			adminRoute.GET("/audit/coverage", handler.GetAuditCoverageV2)

			// Live routing health: per-channel circuit-breaker state as this
			// replica sees it. Read-only and side-effect free.
			adminRoute.GET("/gateway/health", handler.GetGatewayHealthV2)

			// Session-affinity stats + purge (L5, routing-resilience-limits-11 /
			// console-ux-30). Two DELETE shapes on purpose: a bare
			// /affinity/:key path param addresses one binding, while wiping
			// every binding lives on the collection path and requires the
			// explicit ?all=true guard (handler.PurgeAllAffinityBindingsV2)
			// so a stray DELETE can never do it by accident.
			adminRoute.GET("/routing/affinity", handler.GetAffinityStatsV2)
			adminRoute.DELETE("/routing/affinity", handler.PurgeAllAffinityBindingsV2)
			adminRoute.DELETE("/routing/affinity/:key", handler.PurgeAffinityBindingV2)

			// Model performance analytics + platform-wide usage-log CSV export
			// (rate-limited: heavy aggregation / bulk row scans over logs).
			adminRoute.GET("/analytics/model-performance", middleware.CriticalRateLimit(), handler.GetModelPerformanceV2)
			// L4 (2026-09-12): period-over-period model/vendor/group
			// leaderboard, optionally filtered to one tenant. Same rate-limit rationale —
			// each cache miss runs two GROUP BY aggregates over logs; a hit
			// within the 5-minute in-process cache runs none.
			adminRoute.GET("/analytics/rankings", middleware.CriticalRateLimit(), handler.GetRankingsV2)
			adminRoute.GET("/logs/export", middleware.CriticalRateLimit(), handler.ExportAdminLogsV2)

			// L6 (2026-09-12): TOTP adoption stats for the security reviewer,
			// and the audited admin escape hatch for a lost-device user.
			// Stats is a plain aggregate read (no :id) — RootJWTAuth is the
			// only gate needed, same class as /analytics/rankings above.
			//
			// force-disable additionally requires SecureVerificationRequired:
			// the acting root must have stepped up in THEIR OWN session, not
			// merely present a Bearer JWT. RootJWTAuth's Bearer-JWT branch
			// (admin_jwt_auth.go) never populates the "id" context key
			// SecureVerificationRequired reads, so a pure Bearer-JWT caller
			// 401s before reaching the handler at all — deliberately never a
			// session-only gate, mitigating a stolen root JWT alone stripping
			// another user's 2FA (§8 L6 amendment).
			adminRoute.GET("/security/totp-stats", middleware.CriticalRateLimit(), handler.GetAdminTotpStatsV2)
			adminRoute.POST("/security/users/:id/totp/force-disable",
				middleware.CriticalRateLimit(), middleware.SecureVerificationRequired(), handler.ForceDisableTotpV2)

			// L3 (2026-09-13): per-pod heartbeat view of the periodic
			// background jobs registered in taskreg (see taskreg.Register's
			// call sites for the current list — it grows independently of
			// this comment). Root-only, same class of plain aggregate read
			// as /gateway/health above — no :id, no write.
			adminRoute.GET("/system/tasks", handler.GetSystemTasksV2)

			// L4 (2026-09-13, auth-security-17/18, console-ux-36): grant
			// MANAGEMENT (who holds a delegated permission) stays root-only
			// under adminRoute — only root decides who gets delegated
			// access. What a grant UNLOCKS (auditRoute, below) is the
			// separate, narrower RootOrGranted gate.
			authzRoute := adminRoute.Group("/authz")
			{
				authzRoute.GET("/grants", handler.ListGrantsV2)
				authzRoute.POST("/grants", handler.CreateGrantV2)
				authzRoute.DELETE("/grants/:id", handler.RevokeGrantV2)
				authzRoute.GET("/catalog", handler.GetAuthzCatalogV2)
			}
		}

		// L4 (2026-09-13): the audit-feed GETs, gated by RootOrGranted
		// instead of RootJWTAuth — a session-authenticated admin (role >=
		// RoleAdminUser) holding an ACTIVE admin_permission_grants row for
		// (audit, read) reaches these without root; a Bearer JWT and a
		// role < RoleAdminUser session are rejected exactly as RootJWTAuth
		// rejected them before this lane (root passes unconditionally). A
		// role >= RoleAdminUser session WITHOUT a grant gets HTTP 403
		// {error_code:"PERMISSION_DENIED"} — since cycle-12 L4 that is the
		// SAME shape RootJWTAuth's session branch answers with (it no longer
		// answers HTTP 200 {"success":false,...}), so what is special here is
		// WHO is admitted, not what a refusal looks like. A
		// deliberate, confined exception to "adminRoute is RootJWTAuth,
		// full stop" — see root_or_granted.go.
		auditRoute := apiV2.Group("/admin/audit")
		auditRoute.Use(middleware.RootOrGranted("audit", "read"))
		{
			auditRoute.GET("/events", middleware.CriticalRateLimit(), handler.GetAuditEvents)
			auditRoute.GET("/actions", handler.ListAuditActionsV2)
			auditRoute.GET("/export", middleware.CriticalRateLimit(), handler.ExportAuditEventsV2)
			// Tamper-evidence hash-chain verification (migration 024).
			auditRoute.GET("/chain-verify", middleware.CriticalRateLimit(), handler.VerifyAuditChainV2)
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
