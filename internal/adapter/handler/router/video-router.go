package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.TokenAuth(), middleware.PoolBalanceCheck(), middleware.Distribute())
	{
		videoV1Router.GET("/videos/:task_id/content", handler.VideoProxy)
		videoV1Router.POST("/video/generations", handler.RelayTask)
		videoV1Router.GET("/video/generations/:task_id", handler.RelayTask)
		videoV1Router.POST("/videos/:video_id/remix", handler.RelayTask)
	}
	// openai compatible API video routes
	// docs: https://platform.openai.com/docs/api-reference/videos/create
	{
		videoV1Router.POST("/videos", handler.RelayTask)
		// TokenHub 0.10 / OpenAI-shaped alias of POST /videos
		videoV1Router.POST("/videos/generations", handler.RelayTask)
		videoV1Router.GET("/videos/:task_id", handler.RelayTask)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.KlingRequestConvert(), middleware.TokenAuth(), middleware.PoolBalanceCheck(), middleware.Distribute())
	{
		klingV1Router.POST("/videos/text2video", handler.RelayTask)
		klingV1Router.POST("/videos/image2video", handler.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", handler.RelayTask)
		klingV1Router.GET("/videos/image2video/:task_id", handler.RelayTask)
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.JimengRequestConvert(), middleware.TokenAuth(), middleware.PoolBalanceCheck(), middleware.Distribute())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", handler.RelayTask)
	}
}
