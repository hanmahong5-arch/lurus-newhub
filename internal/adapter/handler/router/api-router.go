package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// SetApiRouter configures the legacy `/api` group (carrying V1DeprecationHeaders,
// BrowserOriginGuard, GlobalAPIRateLimit, and RequestBodySizeLimit) and delegates
// public, user, and admin route registration.
func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.CORS())
	apiRouter.Use(middleware.V1DeprecationHeaders())
	apiRouter.Use(middleware.BrowserOriginGuard())
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	apiRouter.Use(middleware.RequestBodySizeLimit())

	logRoute, dataRoute, mjRoute, taskRoute := mountV1PublicAndUserRoutes(apiRouter)
	mountV1AdminRoutes(apiRouter, logRoute, dataRoute, mjRoute, taskRoute)
}

func mountV1PublicAndUserRoutes(apiRouter *gin.RouterGroup) (logRoute, dataRoute, mjRoute, taskRoute *gin.RouterGroup) {
	apiRouter.GET("/setup", handler.GetSetup)
	apiRouter.POST("/setup", handler.PostSetup)
	apiRouter.GET("/status", handler.GetStatus)
	apiRouter.POST("/client-error", middleware.ClientErrorReportRateLimit(), handler.ReportClientError)
	apiRouter.GET("/health", handler.GetHealthDetailed)
	apiRouter.GET("/uptime/status", handler.GetUptimeKumaStatus)
	apiRouter.GET("/notice", handler.GetNotice)
	apiRouter.GET("/user-agreement", handler.GetUserAgreement)
	apiRouter.GET("/privacy-policy", handler.GetPrivacyPolicy)
	apiRouter.GET("/about", handler.GetAbout)
	apiRouter.GET("/home_page_content", handler.GetHomePageContent)
	apiRouter.GET("/pricing", handler.GetPricing)
	apiRouter.GET("/v1/models", handler.ListPublicModels)
	apiRouter.GET("/v1/models/*model", handler.GetPublicModel)
	apiRouter.GET("/ratio_config", middleware.CriticalRateLimit(), handler.GetRatioConfig)

	releaseRoute := apiRouter.Group("/releases")
	releaseRoute.GET("/", handler.ListReleases)
	releaseRoute.GET("/latest/:product_id", handler.GetLatestRelease)
	releaseRoute.GET("/:id", handler.GetReleaseByID)
	releaseRoute.GET("/:id/changelog", handler.GetChangelog)
	releaseRoute.GET("/:id/download/:artifact_id", middleware.DownloadRateLimit(), middleware.ReleaseDownloadGate(), handler.DownloadArtifact)

	apiRouter.GET("/user/self", middleware.UserAuth(), handler.GetSelf)
	apiRouter.PUT("/user/self", middleware.UserAuth(), handler.UpdateSelf)
	apiRouter.GET("/user/self/groups", middleware.UserAuth(), handler.GetUserGroups)
	apiRouter.GET("/user/token", middleware.UserAuth(), handler.GenerateAccessToken)
	apiRouter.PUT("/user/setting", middleware.UserAuth(), handler.UpdateUserSetting)
	apiRouter.POST("/user/topup", middleware.UserAuth(), middleware.RedemptionRateLimit(), handler.RedeemCodeV2)
	apiRouter.GET("/user/models", middleware.UserAuth(), handler.GetUserModels)

	apiRouter.POST("/verify", middleware.UserAuth(), handler.UniversalVerify)
	apiRouter.GET("/verify/status", middleware.UserAuth(), handler.GetVerificationStatus)

	apiRouter.GET("/user/totp/status", middleware.UserAuth(), handler.GetTotpStatus)
	apiRouter.POST("/user/totp/enroll", middleware.UserAuth(), middleware.CriticalRateLimit(), handler.TotpEnroll)
	apiRouter.POST("/user/totp/confirm", middleware.UserAuth(), middleware.CriticalRateLimit(), handler.TotpConfirm)
	apiRouter.POST("/user/totp/disable", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.SecureVerificationRequired(), handler.TotpDisable)
	apiRouter.POST("/user/totp/backup-codes/regenerate", middleware.UserAuth(), middleware.TotpBackupCodesRateLimit(), middleware.SecureVerificationRequired(), handler.RegenerateTotpBackupCodes)

	apiRouter.GET("/wallet/info", middleware.UserAuth(), handler.GetWalletInfo)
	apiRouter.GET("/wallet/transactions", middleware.UserAuth(), handler.GetWalletTransactions)

	tokenRoute := apiRouter.Group("/token")
	tokenRoute.Use(middleware.UserAuth())
	tokenRoute.GET("/", handler.GetAllTokens)
	tokenRoute.GET("/search", handler.SearchTokens)
	tokenRoute.GET("/:id", handler.GetToken)
	tokenRoute.POST("/", handler.AddToken)
	tokenRoute.PUT("/", handler.UpdateToken)
	tokenRoute.DELETE("/:id", handler.DeleteToken)
	tokenRoute.POST("/batch", handler.DeleteTokenBatch)

	logRoute = apiRouter.Group("/log")
	logRoute.GET("/self/", middleware.UserAuth(), handler.GetUserLogs)
	logRoute.GET("/self/stat", middleware.UserAuth(), handler.GetLogsSelfStat)
	logRoute.GET("/self/search", middleware.UserAuth(), handler.SearchUserLogs)
	logRoute.Use(middleware.CORS())
	logRoute.GET("/token", middleware.TokenAuth(), handler.GetLogByKey)

	dataRoute = apiRouter.Group("/data")
	dataRoute.GET("/self/", middleware.UserAuth(), handler.GetUserQuotaDates)

	mjRoute = apiRouter.Group("/mj")
	mjRoute.GET("/self/", middleware.UserAuth(), handler.GetUserMidjourney)

	taskRoute = apiRouter.Group("/task")
	taskRoute.GET("/self/", middleware.UserAuth(), handler.GetUserTask)

	usageRoute := apiRouter.Group("/usage")
	usageRoute.Use(middleware.CriticalRateLimit())
	tokenUsageRoute := usageRoute.Group("/token")
	tokenUsageRoute.Use(middleware.TokenAuth())
	tokenUsageRoute.GET("/", handler.GetTokenUsage)

	return logRoute, dataRoute, mjRoute, taskRoute
}
