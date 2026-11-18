package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

func mountV2TenantChannelsAndLogs(apiV2 *gin.RouterGroup) {
	tenantChannels := apiV2.Group("/:tenant_slug/channels")
	tenantChannels.Use(middleware.AdminSessionAuth(), middleware.TenantSlugGuard())
	tenantChannels.GET("", handler.ListChannelsV2)
	tenantChannels.GET("/:id", handler.GetChannelV2)
	tenantChannels.POST("", handler.CreateChannelV2)
	tenantChannels.PUT("/:id", handler.UpdateChannelV2)
	tenantChannels.DELETE("/:id", handler.DeleteChannelV2)
	tenantChannels.POST("/:id/test", handler.TestChannelV2)
	tenantChannels.GET("/:id/upstream-models", handler.FetchUpstreamModelsV2)
	tenantChannels.POST("/import", handler.ImportChannelsV2)
	tenantChannels.GET("/:id/health", handler.GetChannelHealthV2)
	tenantChannels.GET("/health-summary", handler.GetChannelHealthSummaryV2)
	tenantChannels.POST("/:id/keys/:idx/test", handler.TestChannelKeyV2)
	tenantChannels.POST("/:id/keys/:idx/restore", handler.RestoreChannelKeyV2)
	tenantChannels.PUT("/:id/keys/:idx/settings", handler.UpdateChannelKeySettingsV2)
	tenantChannels.GET("/usage-summary", handler.GetChannelUsageSummaryV2)
	tenantChannels.GET("/:id/usage", handler.GetChannelUsageV2)

	tenantLogs := apiV2.Group("/:tenant_slug/logs")
	tenantLogs.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantLogs.GET("", handler.GetLogsV2)
	tenantLogs.GET("/all", handler.GetAllLogsV2)
	tenantLogs.GET("/cluster", handler.GetLogClusterV2)
	tenantLogs.GET("/stat", middleware.CriticalRateLimit(), handler.GetLogStatV2)
	tenantLogs.GET("/stat/all", middleware.CriticalRateLimit(), handler.GetAllLogStatV2)
	tenantLogs.GET("/export", middleware.CriticalRateLimit(), handler.ExportLogsV2)
	tenantLogs.GET("/:request_id/body", handler.GetLogBodyV2)

	tenantAnalytics := apiV2.Group("/:tenant_slug/analytics")
	tenantAnalytics.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantAnalytics.GET("/rankings", middleware.CriticalRateLimit(), handler.GetTenantRankingsV2)

	tenantRedemptions := apiV2.Group("/:tenant_slug/redemptions")
	tenantRedemptions.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantRedemptions.GET("", handler.ListRedemptionsV2)
	tenantRedemptions.POST("", handler.CreateRedemptionV2)
	tenantRedemptions.DELETE("/:id", handler.DeleteRedemptionV2)

	apiV2.POST("/:tenant_slug/redeem", middleware.UserAuth(), middleware.TenantSlugGuard(), middleware.RedemptionRateLimit(), handler.RedeemCodeV2)
	apiV2.POST("/:tenant_slug/provision", middleware.BootstrapRateLimit(), handler.ProvisionV2)
	apiV2.POST("/:tenant_slug/plan-grant", middleware.PlanGrantRateLimit(), handler.PlanGrantV2)

	tenantSessions := apiV2.Group("/:tenant_slug/sessions")
	tenantSessions.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantSessions.GET("", handler.ListSessionsV2)
	tenantSessions.DELETE("/current", handler.RevokeCurrentSessionV2)
	tenantSessions.DELETE("/others", handler.RevokeOtherSessionsV2)
	tenantSessions.DELETE("/:id", handler.RevokeSessionByIDV2)
}
