package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

func mountV2SwitchAndPublic(apiV2 *gin.RouterGroup) {
	switchGroup := apiV2.Group("/switch")
	{
		switchGroup.GET("/tools/versions", handler.GetToolVersions)
		switchGroup.GET("/presets", handler.ListSwitchPresets)
		switchGroup.POST("/redeem", middleware.RedemptionRateLimit(), handler.SwitchRedeemAnonymous)
		switchGroup.POST("/heartbeat", handler.UserHeartbeat)
		switchGroup.GET("/pricing", handler.GetSwitchPricing)
		switchGroup.POST("/reconciliation", handler.SwitchReconciliation)
		switchGroup.GET("/user/info", handler.GetSwitchUserInfo)
		switchGroup.POST("/user/topup", middleware.RedemptionRateLimit(), handler.SwitchUserTopup)
		switchGroup.GET("/app/releases/latest", middleware.FixedProductDownloadGate("lurus-switch"), handler.GetSwitchAppRelease)
	}

	apiV2.GET("/relays/recommended", handler.GetRecommendedRelays)
	apiV2.GET("/public/model-status", handler.GetPublicModelStatus)
	apiV2.POST("/:tenant_slug/user/heartbeat", handler.UserHeartbeat)
	apiV2.GET("/:tenant_slug/switch/teampack/meta", handler.GetSwitchTeamPackMeta)
	apiV2.GET("/:tenant_slug/switch/teampack", handler.GetSwitchTeamPackBlob)
	apiV2.GET("/:tenant_slug/switch/teampack.sig", handler.GetSwitchTeamPackSig)
	apiV2.GET("/tools/download-manifest", handler.GetToolDownloadManifest)
}
