package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func mountRelayTaskAndGeminiRoutes(router *gin.Engine) {
	registerMjRouterGroup(router.Group("/mj"))
	registerMjRouterGroup(router.Group("/:mode/mj"))

	relaySunoRouter := router.Group("/suno")
	relaySunoRouter.Use(
		middleware.TokenAuth(),
		middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(),
		middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(),
		middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(),
		middleware.Distribute(),
	)
	relaySunoRouter.POST("/submit/:action", handler.RelayTask)
	relaySunoRouter.POST("/fetch", handler.RelayTask)
	relaySunoRouter.GET("/fetch/:id", handler.RelayTask)

	relayMusicRouter := router.Group("/v1/audio")
	relayMusicRouter.Use(
		middleware.TokenAuth(),
		middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(),
		middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(),
		middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(),
		middleware.Distribute(),
	)
	relayMusicRouter.POST("/music", handler.RelayTask)
	relayMusicRouter.GET("/music/:task_id", handler.RelayTask)

	relayGeminiRouter := router.Group("/v1beta")
	relayGeminiRouter.Use(
		middleware.StampRelayFormat(),
		middleware.TokenAuth(),
		middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(),
		middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(),
		middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(),
	)
	geminiHTTPRouter := relayGeminiRouter.Group("")
	geminiHTTPRouter.Use(middleware.Distribute(), middleware.BusinessModelRateLimit())
	geminiHTTPRouter.POST("/models/*path", func(c *gin.Context) {
		handler.Relay(c, types.RelayFormatGemini)
	})
}

func registerMjRouterGroup(relayMjRouter *gin.RouterGroup) {
	relayMjRouter.Use(middleware.TokenAuth(), middleware.PoolBalanceCheck())

	imageGroup := relayMjRouter.Group("")
	imageGroup.Use(middleware.Distribute())
	imageGroup.GET("/image/:id", relay.RelayMidjourneyImage)

	submitGroup := relayMjRouter.Group("")
	submitGroup.Use(
		middleware.CostSpikeLimit(),
		middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(),
		middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(),
		middleware.Distribute(),
	)
	submitGroup.POST("/submit/action", handler.RelayMidjourney)
	submitGroup.POST("/submit/shorten", handler.RelayMidjourney)
	submitGroup.POST("/submit/modal", handler.RelayMidjourney)
	submitGroup.POST("/submit/imagine", handler.RelayMidjourney)
	submitGroup.POST("/submit/change", handler.RelayMidjourney)
	submitGroup.POST("/submit/simple-change", handler.RelayMidjourney)
	submitGroup.POST("/submit/describe", handler.RelayMidjourney)
	submitGroup.POST("/submit/blend", handler.RelayMidjourney)
	submitGroup.POST("/submit/edits", handler.RelayMidjourney)
	submitGroup.POST("/submit/video", handler.RelayMidjourney)
	submitGroup.POST("/notify", handler.RelayMidjourney)
	submitGroup.GET("/task/:id/fetch", handler.RelayMidjourney)
	submitGroup.GET("/task/:id/image-seed", handler.RelayMidjourney)
	submitGroup.POST("/task/list-by-condition", handler.RelayMidjourney)
	submitGroup.POST("/insight-face/swap", handler.RelayMidjourney)
	submitGroup.POST("/submit/upload-discord-images", handler.RelayMidjourney)
}
