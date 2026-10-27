package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

// mountAdminStaffFacade registers platform-staff (/admin) routes that the
// console's `platformStaff` pages call: group pickers, prefill presets, and
// the thin /channels facade over the same handlers as deprecated /api/channel.
// Root-only side effects keep an extra RootAuth, matching v1.
func mountAdminStaffFacade(apiV2 *gin.RouterGroup) {
	adminStaff := apiV2.Group("/admin")
	adminStaff.Use(middleware.AdminJWTAuth())
	adminStaff.Use(middleware.AuditWriteGuard())
	{
		adminStaff.GET("/groups", handler.ListAdminGroupsV2)

		adminPrefill := adminStaff.Group("/prefill-groups")
		{
			adminPrefill.GET("", handler.GetPrefillGroups)
			adminPrefill.POST("", middleware.RootAuth(), handler.CreatePrefillGroup)
			adminPrefill.PUT("", middleware.RootAuth(), handler.UpdatePrefillGroup)
			adminPrefill.DELETE("/:id", middleware.RootAuth(), handler.DeletePrefillGroup)
		}

		adminChannels := adminStaff.Group("/channels")
		{
			adminChannels.GET("", handler.GetAllChannels)
			adminChannels.GET("/search", handler.SearchChannels)
			adminChannels.GET("/models", handler.ChannelListModels)
			adminChannels.GET("/models_enabled", handler.EnabledListModels)
			adminChannels.GET("/test", middleware.RootAuth(), handler.TestAllChannels)
			adminChannels.GET("/test/:id", handler.TestChannel)
			adminChannels.GET("/update_balance", middleware.RootAuth(), handler.UpdateAllChannelsBalance)
			adminChannels.GET("/update_balance/:id", handler.UpdateChannelBalance)
			adminChannels.POST("", handler.AddChannel)
			adminChannels.PUT("", handler.UpdateChannel)
			adminChannels.DELETE("/disabled", handler.DeleteDisabledChannel)
			adminChannels.POST("/tag/disabled", handler.DisableTagChannels)
			adminChannels.POST("/tag/enabled", handler.EnableTagChannels)
			adminChannels.PUT("/tag", handler.EditTagChannels)
			adminChannels.GET("/tag/models", handler.GetTagModels)
			adminChannels.POST("/batch", handler.DeleteChannelBatch)
			adminChannels.POST("/batch/tag", handler.BatchSetChannelTag)
			adminChannels.POST("/fix", middleware.RootAuth(), handler.FixChannelsAbilities)
			adminChannels.GET("/fetch_models/:id", handler.FetchUpstreamModels)
			adminChannels.POST("/fetch_models", handler.FetchModels)
			adminChannels.POST("/ollama/pull", handler.OllamaPullModel)
			adminChannels.POST("/ollama/pull/stream", handler.OllamaPullModelStream)
			adminChannels.DELETE("/ollama/delete", handler.OllamaDeleteModel)
			adminChannels.GET("/ollama/version/:id", handler.OllamaVersion)
			adminChannels.POST("/copy/:id", handler.CopyChannel)
			adminChannels.POST("/multi_key/manage", handler.ManageMultiKeys)
			adminChannels.GET("/:id", handler.GetChannel)
			adminChannels.POST("/:id/key", middleware.RootAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.SecureVerificationRequired(), handler.GetChannelKey)
			adminChannels.DELETE("/:id", handler.DeleteChannel)
		}
	}
}
