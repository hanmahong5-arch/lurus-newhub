package router

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func SetRelayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	router.Use(middleware.DecompressRequestMiddleware())
	router.Use(middleware.StatsMiddleware())
	// https://platform.openai.com/docs/api-reference/introduction
	modelsRouter := router.Group("/v1/models")
	modelsRouter.Use(middleware.TokenAuth())
	{
		modelsRouter.GET("", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				handler.ListModels(c, constant.ChannelTypeAnthropic)
			case c.GetHeader("x-goog-api-key") != "" || c.Query("key") != "": // 单独的适配
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
	}

	geminiRouter := router.Group("/v1beta/models")
	geminiRouter.Use(middleware.TokenAuth())
	{
		geminiRouter.GET("", func(c *gin.Context) {
			handler.ListModels(c, constant.ChannelTypeGemini)
		})
	}

	geminiCompatibleRouter := router.Group("/v1beta/openai/models")
	geminiCompatibleRouter.Use(middleware.TokenAuth())
	{
		geminiCompatibleRouter.GET("", func(c *gin.Context) {
			handler.ListModels(c, constant.ChannelTypeOpenAI)
		})
	}

	playgroundRouter := router.Group("/pg")
	playgroundRouter.Use(middleware.PlaygroundAuth(), middleware.Distribute())
	{
		playgroundRouter.POST("/chat/completions", handler.Playground)
	}
	// Self-service billing API (authenticated via TokenAuth, no distribution needed)
	billingRouter := router.Group("/v1/billing")
	billingRouter.Use(middleware.TokenAuth())
	{
		billingRouter.GET("/balance", handler.SelfBillingBalance)
		billingRouter.GET("/usage", handler.SelfBillingUsage)
	}

	relayV1Router := router.Group("/v1")
	relayV1Router.Use(middleware.TokenAuth())
	// Tenant credit-pool gate: after TokenAuth (need tenant_context),
	// before CostSpikeLimit so an exhausted pool short-circuits the chain
	// (ADR 2026-05-18 §5 enforcement order).
	relayV1Router.Use(middleware.PoolBalanceCheck())
	// Cost-spike protection runs after auth (needs user id) and before
	// entitlement/rate-limit so a runaway loop can't keep racking up checks.
	relayV1Router.Use(middleware.CostSpikeLimit())
	relayV1Router.Use(middleware.EntitlementCheck())
	relayV1Router.Use(middleware.ModelRequestRateLimit())
	relayV1Router.Use(middleware.BusinessRateLimit())
	// Occupancy cap (in-flight), complementing the per-minute limits above:
	// holds a lease for the whole request, so it must wrap everything after it.
	// Disabled unless RELAY_MAX_CONCURRENT_PER_TOKEN/_PER_TENANT is set.
	relayV1Router.Use(middleware.RelayConcurrencyLimit())
	{
		// WebSocket 路由（统一到 Relay）
		wsRouter := relayV1Router.Group("")
		// Per-model dimension after Distribute — the requested model name only
		// enters the context there (business_model_rate_limit.go).
		wsRouter.Use(middleware.Distribute(), middleware.BusinessModelRateLimit())
		wsRouter.GET("/realtime", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAIRealtime)
		})
	}
	{
		//http router
		httpRouter := relayV1Router.Group("")
		// Per-model dimension after Distribute (see wsRouter note above).
		httpRouter.Use(middleware.Distribute(), middleware.BusinessModelRateLimit())

		// claude related routes
		httpRouter.POST("/messages", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatClaude)
		})
		// Free, unmetered size query — same auth/routing/rate-limit chain as
		// /v1/messages, but never billed (see handler.RelayClaudeCountTokens).
		httpRouter.POST("/messages/count_tokens", handler.RelayClaudeCountTokens)

		// chat related routes
		httpRouter.POST("/completions", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAI)
		})
		httpRouter.POST("/chat/completions", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAI)
		})

		// response related routes
		httpRouter.POST("/responses", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAIResponses)
		})

		// image related routes
		httpRouter.POST("/edits", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/images/generations", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/images/edits", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAIImage)
		})

		// embedding related routes
		httpRouter.POST("/embeddings", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatEmbedding)
		})

		// audio related routes
		httpRouter.POST("/audio/transcriptions", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/translations", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/speech", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAIAudio)
		})

		// rerank related routes
		httpRouter.POST("/rerank", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatRerank)
		})

		// gemini relay routes
		httpRouter.POST("/engines/:model/embeddings", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatGemini)
		})
		httpRouter.POST("/models/*path", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatGemini)
		})

		// other relay routes
		httpRouter.POST("/moderations", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatOpenAI)
		})

		// not implemented
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
	}

	relayMjRouter := router.Group("/mj")
	registerMjRouterGroup(relayMjRouter)

	relayMjModeRouter := router.Group("/:mode/mj")
	registerMjRouterGroup(relayMjModeRouter)
	//relayMjRouter.Use()

	relaySunoRouter := router.Group("/suno")
	relaySunoRouter.Use(middleware.TokenAuth(), middleware.PoolBalanceCheck(), middleware.Distribute())
	{
		relaySunoRouter.POST("/submit/:action", handler.RelayTask)
		relaySunoRouter.POST("/fetch", handler.RelayTask)
		relaySunoRouter.GET("/fetch/:id", handler.RelayTask)
	}

	// OpenAI-compatible music generation routes (used by lurus-creator)
	relayMusicRouter := router.Group("/v1/audio")
	relayMusicRouter.Use(middleware.TokenAuth(), middleware.PoolBalanceCheck(), middleware.Distribute())
	{
		relayMusicRouter.POST("/music", handler.RelayTask)
		relayMusicRouter.GET("/music/:task_id", handler.RelayTask)
	}

	relayGeminiRouter := router.Group("/v1beta")
	relayGeminiRouter.Use(middleware.TokenAuth())
	relayGeminiRouter.Use(middleware.PoolBalanceCheck())
	relayGeminiRouter.Use(middleware.ModelRequestRateLimit())
	relayGeminiRouter.Use(middleware.Distribute())
	{
		// Gemini API 路径格式: /v1beta/models/{model_name}:{action}
		relayGeminiRouter.POST("/models/*path", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatGemini)
		})
	}
}

func registerMjRouterGroup(relayMjRouter *gin.RouterGroup) {
	relayMjRouter.Use(middleware.TokenAuth(), middleware.PoolBalanceCheck(), middleware.Distribute())
	// Image proxy requires TokenAuth but not Distribute; registered after .Use()
	// so Gin applies the middleware. Distribute is a no-op for GET-only proxy.
	relayMjRouter.GET("/image/:id", relay.RelayMidjourneyImage)
	{
		relayMjRouter.POST("/submit/action", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/shorten", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/modal", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/imagine", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/change", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/simple-change", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/describe", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/blend", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/edits", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/video", handler.RelayMidjourney)
		relayMjRouter.POST("/notify", handler.RelayMidjourney)
		relayMjRouter.GET("/task/:id/fetch", handler.RelayMidjourney)
		relayMjRouter.GET("/task/:id/image-seed", handler.RelayMidjourney)
		relayMjRouter.POST("/task/list-by-condition", handler.RelayMidjourney)
		relayMjRouter.POST("/insight-face/swap", handler.RelayMidjourney)
		relayMjRouter.POST("/submit/upload-discord-images", handler.RelayMidjourney)
	}
}
