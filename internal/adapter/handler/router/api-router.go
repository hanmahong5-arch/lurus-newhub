package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.CORS())
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	// DecompressRequestMiddleware (the only other body-size cap in this repo)
	// is wired onto the relay router only, which SetRelayRouter registers on
	// the engine AFTER this group is already built — gin snapshots a group's
	// middleware chain at Group() call time, so that later engine-level Use()
	// never reaches /api. Without this, any ShouldBindJSON endpoint here would
	// accept an unbounded body (OOM DoS). See body_size_limit.go.
	apiRouter.Use(middleware.RequestBodySizeLimit())
	{
		// ================================================================
		// Public routes (no authentication)
		// ================================================================

		apiRouter.GET("/setup", handler.GetSetup)
		apiRouter.POST("/setup", handler.PostSetup)
		apiRouter.GET("/status", handler.GetStatus)
		apiRouter.GET("/health", handler.GetHealthDetailed)
		apiRouter.GET("/uptime/status", handler.GetUptimeKumaStatus)
		apiRouter.GET("/notice", handler.GetNotice)
		apiRouter.GET("/user-agreement", handler.GetUserAgreement)
		apiRouter.GET("/privacy-policy", handler.GetPrivacyPolicy)
		apiRouter.GET("/about", handler.GetAbout)
		apiRouter.GET("/home_page_content", handler.GetHomePageContent)
		apiRouter.GET("/pricing", handler.GetPricing)
		apiRouter.GET("/ratio_config", middleware.CriticalRateLimit(), handler.GetRatioConfig)

		releaseRoute := apiRouter.Group("/releases")
		{
			releaseRoute.GET("/", handler.ListReleases)
			releaseRoute.GET("/latest/:product_id", handler.GetLatestRelease)
			releaseRoute.GET("/:id", handler.GetReleaseByID)
			releaseRoute.GET("/:id/changelog", handler.GetChangelog)
			releaseRoute.GET("/:id/download/:artifact_id", middleware.DownloadRateLimit(), middleware.ReleaseDownloadGate(), handler.DownloadArtifact)
		}

		// ================================================================
		// Regular user routes (session auth — any logged-in user)
		// ================================================================

		// -- Current user profile --
		apiRouter.GET("/user/self", middleware.UserAuth(), handler.GetSelf)
		apiRouter.PUT("/user/self", middleware.UserAuth(), handler.UpdateSelf)
		apiRouter.GET("/user/self/groups", middleware.UserAuth(), handler.GetUserGroups)
		apiRouter.GET("/user/token", middleware.UserAuth(), handler.GenerateAccessToken)
		apiRouter.PUT("/user/setting", middleware.UserAuth(), handler.UpdateUserSetting)
		apiRouter.POST("/user/topup", middleware.UserAuth(), handler.RedeemCodeV2)
		apiRouter.GET("/user/models", middleware.UserAuth(), handler.GetUserModels)

		// -- Secure verification (session-based step-up re-confirmation) --
		// Strong auth factor (MFA) is performed at the OIDC IdP login; this records a
		// short-lived (5min) session re-confirmation that gates sensitive ops such
		// as channel-key reveal. Frontend contract: web/src/services/secureVerification.js.
		apiRouter.POST("/verify", middleware.UserAuth(), handler.UniversalVerify)
		apiRouter.GET("/verify/status", middleware.UserAuth(), handler.GetVerificationStatus)

		// -- TOTP step-up factor management --
		// Enrolled users must present a valid TOTP code to POST /api/verify;
		// disable is itself gated behind a fresh step-up verification.
		apiRouter.GET("/user/totp/status", middleware.UserAuth(), handler.GetTotpStatus)
		apiRouter.POST("/user/totp/enroll", middleware.UserAuth(), middleware.CriticalRateLimit(), handler.TotpEnroll)
		apiRouter.POST("/user/totp/confirm", middleware.UserAuth(), middleware.CriticalRateLimit(), handler.TotpConfirm)
		apiRouter.POST("/user/totp/disable", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.SecureVerificationRequired(), handler.TotpDisable)

		// -- Platform wallet integration --
		apiRouter.GET("/wallet/info", middleware.UserAuth(), handler.GetWalletInfo)
		apiRouter.GET("/wallet/transactions", middleware.UserAuth(), handler.GetWalletTransactions)

		// -- Token management (users manage their own tokens) --
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/", handler.GetAllTokens)
			tokenRoute.GET("/search", handler.SearchTokens)
			tokenRoute.GET("/:id", handler.GetToken)
			tokenRoute.POST("/", handler.AddToken)
			tokenRoute.PUT("/", handler.UpdateToken)
			tokenRoute.DELETE("/:id", handler.DeleteToken)
			tokenRoute.POST("/batch", handler.DeleteTokenBatch)
		}

		// -- User's own logs --
		logRoute := apiRouter.Group("/log")
		logRoute.GET("/self/", middleware.UserAuth(), handler.GetUserLogs)
		logRoute.GET("/self/stat", middleware.UserAuth(), handler.GetLogsSelfStat)
		logRoute.GET("/self/search", middleware.UserAuth(), handler.SearchUserLogs)
		logRoute.Use(middleware.CORS())
		{
			logRoute.GET("/token", middleware.TokenAuth(), handler.GetLogByKey)
		}

		// -- User's own data/stats --
		dataRoute := apiRouter.Group("/data")
		dataRoute.GET("/self/", middleware.UserAuth(), handler.GetUserQuotaDates)

		// -- User's own Midjourney tasks --
		mjRoute := apiRouter.Group("/mj")
		mjRoute.GET("/self/", middleware.UserAuth(), handler.GetUserMidjourney)

		// -- User's own tasks --
		taskRoute := apiRouter.Group("/task")
		taskRoute.GET("/self/", middleware.UserAuth(), handler.GetUserTask)

		// -- Token usage (token auth) --
		usageRoute := apiRouter.Group("/usage")
		usageRoute.Use(middleware.CriticalRateLimit())
		{
			tokenUsageRoute := usageRoute.Group("/token")
			tokenUsageRoute.Use(middleware.TokenAuth())
			{
				tokenUsageRoute.GET("/", handler.GetTokenUsage)
			}
		}

		// ================================================================
		// Admin routes (session auth — requires admin role)
		// ================================================================

		apiRouter.GET("/models", middleware.AdminAuth(), handler.DashboardListModels)
		apiRouter.GET("/status/test", middleware.AdminAuth(), handler.TestStatus)

		channelRoute := apiRouter.Group("/channel")
		channelRoute.Use(middleware.AdminAuth())
		{
			channelRoute.GET("/", handler.GetAllChannels)
			channelRoute.GET("/search", handler.SearchChannels)
			channelRoute.GET("/models", handler.ChannelListModels)
			channelRoute.GET("/models_enabled", handler.EnabledListModels)
			channelRoute.GET("/:id", handler.GetChannel)
			channelRoute.POST("/:id/key", middleware.RootAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.SecureVerificationRequired(), handler.GetChannelKey)
			channelRoute.GET("/test", handler.TestAllChannels)
			channelRoute.GET("/test/:id", handler.TestChannel)
			channelRoute.GET("/update_balance", handler.UpdateAllChannelsBalance)
			channelRoute.GET("/update_balance/:id", handler.UpdateChannelBalance)
			channelRoute.POST("/", handler.AddChannel)
			channelRoute.PUT("/", handler.UpdateChannel)
			channelRoute.DELETE("/disabled", handler.DeleteDisabledChannel)
			channelRoute.POST("/tag/disabled", handler.DisableTagChannels)
			channelRoute.POST("/tag/enabled", handler.EnableTagChannels)
			channelRoute.PUT("/tag", handler.EditTagChannels)
			channelRoute.DELETE("/:id", handler.DeleteChannel)
			channelRoute.POST("/batch", handler.DeleteChannelBatch)
			channelRoute.POST("/fix", handler.FixChannelsAbilities)
			channelRoute.GET("/fetch_models/:id", handler.FetchUpstreamModels)
			channelRoute.POST("/fetch_models", handler.FetchModels)
			channelRoute.POST("/ollama/pull", handler.OllamaPullModel)
			channelRoute.POST("/ollama/pull/stream", handler.OllamaPullModelStream)
			channelRoute.DELETE("/ollama/delete", handler.OllamaDeleteModel)
			channelRoute.GET("/ollama/version/:id", handler.OllamaVersion)
			channelRoute.POST("/batch/tag", handler.BatchSetChannelTag)
			channelRoute.GET("/tag/models", handler.GetTagModels)
			channelRoute.POST("/copy/:id", handler.CopyChannel)
			channelRoute.POST("/multi_key/manage", handler.ManageMultiKeys)
		}

		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.AdminAuth())
		{
			redemptionRoute.GET("/", handler.GetAllRedemptions)
			redemptionRoute.GET("/search", handler.SearchRedemptions)
			redemptionRoute.GET("/:id", handler.GetRedemption)
			redemptionRoute.POST("/", handler.AddRedemption)
			redemptionRoute.PUT("/", handler.UpdateRedemption)
			redemptionRoute.DELETE("/invalid", handler.DeleteInvalidRedemption)
			redemptionRoute.DELETE("/:id", handler.DeleteRedemption)
		}

		// OpenRouter free-model sync admin routes
		openrouterSyncRoute := apiRouter.Group("/openrouter-sync")
		openrouterSyncRoute.Use(middleware.AdminAuth())
		{
			// Sync jobs mutate the GLOBAL free-model catalog for all tenants;
			// job reads stay at admin, but create/update/delete/run are root.
			openrouterSyncRoute.GET("/jobs", handler.ListOpenRouterSyncJobs)
			openrouterSyncRoute.POST("/jobs", middleware.RootAuth(), handler.CreateOpenRouterSyncJob)
			openrouterSyncRoute.PUT("/jobs/:id", middleware.RootAuth(), handler.UpdateOpenRouterSyncJob)
			openrouterSyncRoute.DELETE("/jobs/:id", middleware.RootAuth(), handler.DeleteOpenRouterSyncJob)
			openrouterSyncRoute.POST("/jobs/:id/run", middleware.RootAuth(), handler.RunOpenRouterSyncJob)
			openrouterSyncRoute.POST("/run-all", middleware.RootAuth(), handler.RunAllOpenRouterSyncJobs)
			openrouterSyncRoute.GET("/jobs/:id/preview", handler.PreviewOpenRouterSyncJob)
			openrouterSyncRoute.GET("/categories", handler.ListOpenRouterSyncCategories)
			openrouterSyncRoute.GET("/last-status", handler.GetOpenRouterSyncLastStatus)
			openrouterSyncRoute.GET("/api-pool", handler.GetOpenRouterApiPoolStatus)
		}

		// Admin log routes (view all users' logs)
		logRoute.GET("/", middleware.AdminAuth(), handler.GetAllLogs)
		logRoute.DELETE("/", middleware.AdminAuth(), handler.DeleteHistoryLogs)
		logRoute.GET("/stat", middleware.AdminAuth(), handler.GetLogsStat)
		logRoute.GET("/search", middleware.AdminAuth(), handler.SearchAllLogs)

		// Admin data routes
		dataRoute.GET("/", middleware.AdminAuth(), handler.GetAllQuotaDates)

		// Admin MJ/task routes
		mjRoute.GET("/", middleware.AdminAuth(), handler.GetAllMidjourney)
		taskRoute.GET("/", middleware.AdminAuth(), handler.GetAllTask)

		groupRoute := apiRouter.Group("/group")
		groupRoute.Use(middleware.AdminAuth())
		{
			groupRoute.GET("/", handler.GetGroups)
		}

		prefillGroupRoute := apiRouter.Group("/prefill_group")
		prefillGroupRoute.Use(middleware.AdminAuth())
		{
			// Prefill groups are global console presets; reads stay at admin,
			// mutations are operator-only.
			prefillGroupRoute.GET("/", handler.GetPrefillGroups)
			prefillGroupRoute.POST("/", middleware.RootAuth(), handler.CreatePrefillGroup)
			prefillGroupRoute.PUT("/", middleware.RootAuth(), handler.UpdatePrefillGroup)
			prefillGroupRoute.DELETE("/:id", middleware.RootAuth(), handler.DeletePrefillGroup)
		}

		// User management (admin only)
		userRoute := apiRouter.Group("/user")
		userRoute.Use(middleware.AdminAuth())
		{
			userRoute.GET("/", handler.GetAllUsers)
			userRoute.GET("/search", handler.SearchUsers)
			userRoute.GET("/:id", handler.GetUser)
			userRoute.PUT("/", handler.UpdateUser)
		}

		// The vendor catalog is GLOBAL (not tenant-scoped): a mutation here is
		// visible to every tenant. Reads stay at admin so tenant admins can
		// populate channel/pricing pickers, but writes are operator-only (root)
		// — otherwise a role-10 tenant admin could rewrite platform-wide vendors.
		vendorRoute := apiRouter.Group("/vendors")
		vendorRoute.Use(middleware.AdminAuth())
		{
			vendorRoute.GET("/", handler.GetAllVendors)
			vendorRoute.GET("/search", handler.SearchVendors)
			vendorRoute.GET("/:id", handler.GetVendorMeta)
			vendorRoute.POST("/", middleware.RootAuth(), handler.CreateVendorMeta)
			vendorRoute.PUT("/", middleware.RootAuth(), handler.UpdateVendorMeta)
			vendorRoute.DELETE("/:id", middleware.RootAuth(), handler.DeleteVendorMeta)
		}

		// Global model catalog: reads feed every tenant's console (pricing,
		// missing-model hints), so they stay at admin. Catalog mutation and
		// upstream/channel sync are platform-wide side effects → operator-only.
		modelsRoute := apiRouter.Group("/models")
		modelsRoute.Use(middleware.AdminAuth())
		{
			modelsRoute.GET("/sync_upstream/preview", handler.SyncUpstreamPreview)
			modelsRoute.POST("/sync_upstream", middleware.RootAuth(), handler.SyncUpstreamModels)
			modelsRoute.POST("/sync_channels", middleware.RootAuth(), handler.SyncAllChannelsNow)
			modelsRoute.GET("/pricing_info", handler.GetModelsPricingInfo)
			modelsRoute.GET("/missing", handler.GetMissingModels)
			modelsRoute.GET("/", handler.GetAllModelsMeta)
			modelsRoute.GET("/search", handler.SearchModelsMeta)
			modelsRoute.GET("/:id", handler.GetModelMeta)
			modelsRoute.POST("/", middleware.RootAuth(), handler.CreateModelMeta)
			modelsRoute.PUT("/", middleware.RootAuth(), handler.UpdateModelMeta)
			modelsRoute.DELETE("/:id", middleware.RootAuth(), handler.DeleteModelMeta)
		}

		deploymentsRoute := apiRouter.Group("/deployments")
		deploymentsRoute.Use(middleware.AdminAuth())
		{
			deploymentsRoute.GET("/settings", handler.GetModelDeploymentSettings)
			deploymentsRoute.POST("/settings/test-connection", handler.TestIoNetConnection)
			deploymentsRoute.GET("/", handler.GetAllDeployments)
			deploymentsRoute.GET("/search", handler.SearchDeployments)
			deploymentsRoute.POST("/test-connection", handler.TestIoNetConnection)
			deploymentsRoute.GET("/hardware-types", handler.GetHardwareTypes)
			deploymentsRoute.GET("/locations", handler.GetLocations)
			deploymentsRoute.GET("/available-replicas", handler.GetAvailableReplicas)
			deploymentsRoute.POST("/price-estimation", handler.GetPriceEstimation)
			deploymentsRoute.GET("/check-name", handler.CheckClusterNameAvailability)
			// Deployment lifecycle spends real money on external GPU infra
			// (io.net) and is not tenant-scoped — provisioning is operator-only.
			deploymentsRoute.POST("/", middleware.RootAuth(), handler.CreateDeployment)
			deploymentsRoute.GET("/:id", handler.GetDeployment)
			deploymentsRoute.GET("/:id/logs", handler.GetDeploymentLogs)
			deploymentsRoute.GET("/:id/containers", handler.ListDeploymentContainers)
			deploymentsRoute.GET("/:id/containers/:container_id", handler.GetContainerDetails)
			deploymentsRoute.PUT("/:id", middleware.RootAuth(), handler.UpdateDeployment)
			deploymentsRoute.PUT("/:id/name", middleware.RootAuth(), handler.UpdateDeploymentName)
			deploymentsRoute.POST("/:id/extend", middleware.RootAuth(), handler.ExtendDeployment)
			deploymentsRoute.DELETE("/:id", middleware.RootAuth(), handler.DeleteDeployment)
		}

		// Internal-scope API keys grant cross-tenant access to the /internal
		// surface (quota/balance write, user read across tenants). Even LISTING
		// them exposes the platform's trust boundary, so the whole group —
		// read included — is operator-only (root), not tenant-admin.
		apiKeyRoute := apiRouter.Group("/api-keys")
		apiKeyRoute.Use(middleware.RootAuth())
		{
			apiKeyRoute.GET("/", handler.AdminListApiKeys)
			apiKeyRoute.GET("/scopes", handler.AdminGetApiKeyScopes)
			apiKeyRoute.POST("/", handler.AdminCreateApiKey)
			apiKeyRoute.PUT("/:id", handler.AdminUpdateApiKey)
			apiKeyRoute.DELETE("/:id", handler.AdminDeleteApiKey)
			apiKeyRoute.PUT("/:id/toggle", handler.AdminToggleApiKey)
		}

		// ================================================================
		// Root-only routes (session auth — requires root role)
		// ================================================================

		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", handler.GetOptions)
			optionRoute.PUT("/", handler.UpdateOption)
			optionRoute.POST("/rest_model_ratio", handler.ResetModelRatio)
			optionRoute.POST("/migrate_console_setting", handler.MigrateConsoleSetting)
		}

		ratioSyncRoute := apiRouter.Group("/ratio_sync")
		ratioSyncRoute.Use(middleware.RootAuth())
		{
			ratioSyncRoute.GET("/channels", handler.GetSyncableChannels)
			ratioSyncRoute.POST("/fetch", handler.FetchUpstreamRatios)
		}
	}
}
