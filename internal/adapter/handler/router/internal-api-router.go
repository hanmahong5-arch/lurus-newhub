package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// internalApiPreAuthRateLimitNum/Duration bound invalid-key attempts per
// source IP. Mounted BEFORE InternalApiAuth, so internal_api_key_id is never
// set yet — InternalApiRateLimit's key func falls back to "ip:<ClientIP>",
// giving an IP-keyed bucket for free without a new keying path. Looser than
// the post-auth per-key buckets (IKR/IKW/IKP): it only needs to bound the
// blast radius of a brute-force scan against invalid keys, not throttle
// legitimate authenticated traffic.
const (
	internalApiPreAuthRateLimitNum            = 300
	internalApiPreAuthRateLimitDuration int64 = 60
)

// SetInternalApiRouter sets up internal API routes for service-to-service communication
// These routes use API Key authentication instead of user session auth
func SetInternalApiRouter(router *gin.Engine) {
	internalGroup := router.Group("/internal")
	// IP-keyed guard mounted FIRST so a flood of invalid-key attempts is
	// throttled before it ever reaches auth — otherwise each attempt burns a
	// full auth check (hash compare / DB lookup) with no per-IP bound (F6).
	internalGroup.Use(middleware.InternalApiRateLimit(
		internalApiPreAuthRateLimitNum, internalApiPreAuthRateLimitDuration, "IKP-IP"))
	internalGroup.Use(middleware.InternalApiAuth())
	// Per-key general bucket on EVERY internal route (mounted after auth so the
	// key id is in context). A stolen key is bounded even on read endpoints; the
	// write/provision groups below add a second, tighter bucket on top. Marks are
	// unique per tier so the buckets never collide. (P0-3)
	internalGroup.Use(middleware.InternalApiRateLimit(
		common.InternalApiReadRateLimitNum, common.InternalApiReadRateLimitDuration, "IKR"))

	// User read APIs - query user information
	userReadGroup := internalGroup.Group("/user")
	userReadGroup.Use(middleware.RequireScope(repo.ScopeUserRead))
	{
		userReadGroup.GET("/:id", handler.InternalGetUser)
		userReadGroup.GET("/by-email/:email", handler.InternalGetUserByEmail)
		userReadGroup.GET("/by-phone/:phone", handler.InternalGetUserByPhone)
		userReadGroup.GET("/by-zitadel-sub/:sub", handler.InternalGetUserByZitadelSub)
	}

	// User write APIs - create and modify users
	userWriteGroup := internalGroup.Group("/user")
	userWriteGroup.Use(middleware.RequireScope(repo.ScopeUserWrite))
	userWriteGroup.Use(middleware.InternalApiRateLimit(
		common.InternalApiWriteRateLimitNum, common.InternalApiWriteRateLimitDuration, "IKW"))
	{
		userWriteGroup.POST("", handler.InternalCreateUser)
		userWriteGroup.PUT("/:id", handler.InternalUpdateUser)
		userWriteGroup.POST("/provision", handler.InternalProvisionUser)
	}

	// User delete APIs
	userDeleteGroup := internalGroup.Group("/user")
	userDeleteGroup.Use(middleware.RequireScope(repo.ScopeUserDelete))
	{
		userDeleteGroup.DELETE("/:id", handler.InternalDeleteUser)
	}

	// Token read APIs
	tokenReadGroup := internalGroup.Group("/token")
	tokenReadGroup.Use(middleware.RequireScope(repo.ScopeTokenRead))
	{
		tokenReadGroup.GET("/user/:id", handler.InternalGetUserTokens)
		tokenReadGroup.GET("/:id", handler.InternalGetToken)
		tokenReadGroup.GET("/:id/usage", handler.InternalGetTokenUsage)
	}

	// Token write APIs
	tokenWriteGroup := internalGroup.Group("/token")
	tokenWriteGroup.Use(middleware.RequireScope(repo.ScopeTokenWrite))
	{
		tokenWriteGroup.POST("", handler.InternalCreateToken)
		tokenWriteGroup.PUT("/:id", handler.InternalUpdateToken)
		tokenWriteGroup.DELETE("/:id", handler.InternalDeleteToken)
	}

	// Quota APIs - read user quota
	quotaReadGroup := internalGroup.Group("/quota")
	quotaReadGroup.Use(middleware.RequireScope(repo.ScopeQuotaRead))
	{
		quotaReadGroup.GET("/user/:id", handler.InternalGetUserQuota)
	}

	// Quota APIs - adjust user quota
	quotaWriteGroup := internalGroup.Group("/quota")
	quotaWriteGroup.Use(middleware.RequireScope(repo.ScopeQuotaWrite))
	{
		quotaWriteGroup.POST("/adjust", handler.InternalAdjustQuota)
	}

	// Balance APIs - read user balance
	balanceReadGroup := internalGroup.Group("/balance")
	balanceReadGroup.Use(middleware.RequireScope(repo.ScopeBalanceRead))
	{
		balanceReadGroup.GET("/user/:id", handler.InternalGetUserBalance)
	}

	// Balance APIs - top up user balance
	balanceWriteGroup := internalGroup.Group("/balance")
	balanceWriteGroup.Use(middleware.RequireScope(repo.ScopeBalanceWrite))
	{
		balanceWriteGroup.POST("/topup", handler.InternalTopupBalance)
	}

	// Currency APIs - read exchange rates, model pricing, user balance in Lute
	currencyReadGroup := internalGroup.Group("/currency")
	currencyReadGroup.Use(middleware.RequireScope(repo.ScopeCurrencyRead))
	{
		currencyReadGroup.GET("/info", handler.InternalGetCurrencyInfo)
		currencyReadGroup.GET("/models/pricing", handler.InternalGetModelPricing)
		currencyReadGroup.GET("/balance/:id", handler.InternalGetUserBalanceLute)
		currencyReadGroup.GET("/exchanges/:id", handler.InternalGetExchangeHistory)
	}

	// Currency APIs - perform LUC -> LUT exchange
	currencyExchangeGroup := internalGroup.Group("/currency")
	currencyExchangeGroup.Use(middleware.RequireScope(repo.ScopeCurrencyExchange))
	{
		currencyExchangeGroup.POST("/exchange", handler.InternalExchangeLucToLut)
	}

	// Log read APIs - query usage logs by user or token
	logReadGroup := internalGroup.Group("/log")
	logReadGroup.Use(middleware.RequireScope(repo.ScopeLogRead))
	{
		logReadGroup.GET("/user/:id", handler.InternalGetUserLogs)
		logReadGroup.GET("/user/:id/stat", handler.InternalGetUserLogStat)
		logReadGroup.GET("/token/:token_id", handler.InternalGetTokenLogs)
	}

	// Model catalog API - available models with pricing
	modelReadGroup := internalGroup.Group("/models")
	modelReadGroup.Use(middleware.RequireScope(repo.ScopeModelRead))
	{
		modelReadGroup.GET("/catalog", handler.InternalGetModelCatalog)
		modelReadGroup.GET("/video-catalog", handler.InternalGetVideoCatalog)
		modelReadGroup.GET("/video-status", handler.InternalGetVideoStatus)
	}

	// Admin operations (requires the admin scope; ScopeAll keys still pass via
	// HasScope's wildcard, so existing platform-admin keys keep working).
	adminGroup := internalGroup.Group("/admin")
	adminGroup.Use(middleware.RequireScope(repo.ScopeAdmin))
	{
		adminGroup.POST("/backfill-token-accounts", handler.InternalBackfillTokenAccountIDs)
		adminGroup.GET("/convergence-stats", handler.InternalConvergenceStats)
	}

	// Provisioning API — Reseller sub-tenant key issuance / revocation
	// (ADR 2026-05-18 §4.2). Auth: X-API-Key + scope "provisioning".
	provisioningGroup := internalGroup.Group("/v1/provisioning")
	provisioningGroup.Use(middleware.RequireScope(repo.ScopeProvisioning))
	provisioningGroup.Use(middleware.InternalApiRateLimit(
		common.InternalApiProvisionRateLimitNum, common.InternalApiProvisionRateLimitDuration, "IKP"))
	{
		provisioningGroup.POST("/tenants/:slug/keys", handler.CreateProvisionedKey)
		provisioningGroup.GET("/tenants/:slug/keys", handler.ListProvisionedKeys)
		provisioningGroup.DELETE("/tenants/:slug/keys/:key_id", handler.RevokeProvisionedKey)
		// Distributor batch redemption-code issuance / revoke — idempotent via
		// UNIQUE(event_id) in provisioned_redemption_batches (migration 027).
		provisioningGroup.POST("/tenants/:slug/redemptions", handler.InternalProvisionRedemptions)
		provisioningGroup.POST("/tenants/:slug/redemptions/revoke", handler.InternalRevokeProvisionedRedemptions)
	}

	// Platform BillingOutbox supply endpoint — SEAM S1 model (b).
	// Scope: balance:write — platform's internal key already carries this scope
	// for wallet operations; no new key rotation required.
	// Idempotency: enforced via UNIQUE(tenant_id, event_id) in credit_pool_fund_events
	// (migration 031; supersedes the migration-019 global UNIQUE(event_id)).
	poolFundGroup := internalGroup.Group("/v1/provisioning")
	poolFundGroup.Use(middleware.RequireScope(repo.ScopeBalanceWrite))
	poolFundGroup.Use(middleware.InternalApiRateLimit(
		common.InternalApiProvisionRateLimitNum, common.InternalApiProvisionRateLimitDuration, "IKF"))
	{
		poolFundGroup.POST("/tenants/:slug/credit-pool/fund", handler.InternalFundCreditPool)
	}

	// PIPL §47 account erasure — platform calls POST after the deletion
	// cooling-off period expires (newhub has no NATS consumer; trigger is
	// internal HTTP, SEAM S1 pattern). Idempotent via UNIQUE(event_id) in
	// privacy_erasure_requests (migration 020).
	privacyEraseGroup := internalGroup.Group("/v1/privacy")
	privacyEraseGroup.Use(middleware.RequireScope(repo.ScopeUserDelete))
	{
		privacyEraseGroup.POST("/erase", handler.InternalPrivacyErase)
	}
	privacyReadGroup := internalGroup.Group("/v1/privacy")
	privacyReadGroup.Use(middleware.RequireScope(repo.ScopeUserRead))
	{
		privacyReadGroup.GET("/erase/:event_id", handler.InternalGetPrivacyErasure)
	}
}
