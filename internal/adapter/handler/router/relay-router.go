package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// SetRelayRouter mounts the data-plane LLM relay routes (`/v1`, `/v1beta`, `/pg`, `/mj`, `/suno`)
// with strict wire-format stamping, pool/cost-spike/entitlement/concurrency enforcement, and distribution.
func SetRelayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	router.Use(middleware.DecompressRequestMiddleware())
	router.Use(middleware.StatsMiddleware())

	modelsRouter := router.Group("/v1/models")
	modelsRouter.Use(middleware.TokenAuth())
	modelsRouter.GET("", func(c *gin.Context) {
		switch {
		case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
			handler.ListModels(c, constant.ChannelTypeAnthropic)
		case c.GetHeader("x-goog-api-key") != "" || c.Query("key") != "":
			handler.RetrieveModel(c, constant.ChannelTypeGemini)
		default:
			handler.ListModels(c, constant.ChannelTypeOpenAI)
		}
	})
	modelsRouter.GET("/:model", func(c *gin.Context) {
		switch {
		case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
			handler.RetrieveModel(c, constant.ChannelTypeAnthropic)
		default:
			handler.RetrieveModel(c, constant.ChannelTypeOpenAI)
		}
	})

	geminiRouter := router.Group("/v1beta/models")
	geminiRouter.Use(middleware.StampRelayFormat(), middleware.TokenAuth())
	geminiRouter.GET("", func(c *gin.Context) {
		handler.ListModels(c, constant.ChannelTypeGemini)
	})

	geminiCompatibleRouter := router.Group("/v1beta/openai/models")
	geminiCompatibleRouter.Use(middleware.StampRelayFormat(), middleware.TokenAuth())
	geminiCompatibleRouter.GET("", func(c *gin.Context) {
		handler.ListModels(c, constant.ChannelTypeOpenAI)
	})

	playgroundRouter := router.Group("/pg")
	playgroundRouter.Use(
		middleware.PlaygroundAuth(),
		middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(),
		middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(),
		middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(),
		middleware.Distribute(),
	)
	playgroundRouter.POST("/chat/completions", handler.Playground)

	billingRouter := router.Group("/v1/billing")
	billingRouter.Use(middleware.TokenAuth())
	billingRouter.GET("/balance", handler.SelfBillingBalance)
	billingRouter.GET("/usage", handler.SelfBillingUsage)

	mountRelayV1Core(router)
	mountRelayTaskAndGeminiRoutes(router)
}

func mountRelayV1Core(router *gin.Engine) {
	relayV1Router := router.Group("/v1")
	relayV1Router.Use(
		middleware.StampRelayFormat(),
		middleware.TokenAuth(),
		middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(),
		middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(),
		middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(),
	)

	wsRouter := relayV1Router.Group("")
	wsRouter.Use(middleware.Distribute(), middleware.BusinessModelRateLimit())
	wsRouter.GET("/realtime", func(c *gin.Context) {
		handler.Relay(c, types.RelayFormatOpenAIRealtime)
	})

	httpRouter := relayV1Router.Group("")
	httpRouter.Use(middleware.Distribute(), middleware.BusinessModelRateLimit())
	httpRouter.POST("/messages", func(c *gin.Context) { handler.Relay(c, types.RelayFormatClaude) })
	httpRouter.POST("/messages/count_tokens", handler.RelayClaudeCountTokens)
	httpRouter.POST("/completions", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAI) })
	httpRouter.POST("/chat/completions", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAI) })
	httpRouter.POST("/responses", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAIResponses) })
	httpRouter.POST("/responses/compact", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAIResponsesCompact) })
	httpRouter.POST("/edits", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAIImage) })
	httpRouter.POST("/images/generations", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAIImage) })
	httpRouter.POST("/images/edits", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAIImage) })
	httpRouter.POST("/embeddings", func(c *gin.Context) { handler.Relay(c, types.RelayFormatEmbedding) })
	httpRouter.POST("/audio/transcriptions", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAIAudio) })
	httpRouter.POST("/audio/translations", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAIAudio) })
	httpRouter.POST("/audio/speech", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAIAudio) })
	httpRouter.POST("/rerank", func(c *gin.Context) { handler.Relay(c, types.RelayFormatRerank) })
	httpRouter.POST("/systemone", func(c *gin.Context) { handler.Relay(c, types.RelayFormatSystemOne) })
	httpRouter.POST("/engines/:model/embeddings", func(c *gin.Context) { handler.Relay(c, types.RelayFormatGemini) })
	httpRouter.POST("/models/*path", func(c *gin.Context) { handler.Relay(c, types.RelayFormatGemini) })
	httpRouter.POST("/moderations", func(c *gin.Context) { handler.Relay(c, types.RelayFormatOpenAI) })

	httpRouter.POST("/images/variations", handler.RelayNotImplemented)
	httpRouter.GET("/files", handler.RelayNotImplemented)
	httpRouter.POST("/files", handler.RelayNotImplemented)
	httpRouter.DELETE("/files/:id", handler.RelayNotImplemented)
	httpRouter.GET("/files/:id", handler.RelayNotImplemented)
	httpRouter.GET("/files/:id/content", handler.RelayNotImplemented)
	httpRouter.POST("/fine-tunes", handler.RelayNotImplemented)
	httpRouter.GET("/fine-tunes", handler.RelayNotImplemented)
	httpRouter.GET("/fine-tunes/:id", handler.RelayNotImplemented)
	httpRouter.POST("/fine-tunes/:id/cancel", handler.RelayNotImplemented)
	httpRouter.GET("/fine-tunes/:id/events", handler.RelayNotImplemented)
	httpRouter.DELETE("/models/:model", handler.RelayNotImplemented)

	responsesStateRouter := router.Group("/v1/responses")
	responsesStateRouter.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.ResponsesStateRateLimit())
	responsesStateRouter.GET("/:response_id", handler.RelayResponsesRetrieve)
	responsesStateRouter.DELETE("/:response_id", handler.RelayResponsesDelete)
}
