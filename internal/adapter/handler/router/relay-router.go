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
	// StampRelayFormat first: a TokenAuth rejection on this group must answer
	// in Gemini's wire shape, not the OpenAI default (see wire_format.go).
	geminiRouter.Use(middleware.StampRelayFormat())
	geminiRouter.Use(middleware.TokenAuth())
	{
		geminiRouter.GET("", func(c *gin.Context) {
			handler.ListModels(c, constant.ChannelTypeGemini)
		})
	}

	geminiCompatibleRouter := router.Group("/v1beta/openai/models")
	// Under /v1beta/openai/ — relayFormatForPath resolves this to OpenAI's
	// wire, not Gemini's, matching the OpenAI-compatible handler below.
	geminiCompatibleRouter.Use(middleware.StampRelayFormat())
	geminiCompatibleRouter.Use(middleware.TokenAuth())
	{
		geminiCompatibleRouter.GET("", func(c *gin.Context) {
			handler.ListModels(c, constant.ChannelTypeOpenAI)
		})
	}

	playgroundRouter := router.Group("/pg")
	playgroundRouter.Use(middleware.PlaygroundAuth())
	// Tenant credit-pool gate: after PlaygroundAuth (need tenant_context),
	// before CostSpikeLimit so an exhausted pool short-circuits the chain —
	// same order as relayV1Router below (ADR 2026-05-18 §5 enforcement
	// order); the playground is a billed relay path like any other and must
	// not be exempt from the ceilings that path enforces.
	playgroundRouter.Use(middleware.PoolBalanceCheck())
	// Cost-spike protection runs after auth (needs user id) and before
	// entitlement/rate-limit so a runaway loop can't keep racking up checks.
	playgroundRouter.Use(middleware.CostSpikeLimit())
	playgroundRouter.Use(middleware.EntitlementCheck())
	playgroundRouter.Use(middleware.ModelRequestRateLimit())
	playgroundRouter.Use(middleware.BusinessRateLimit())
	// Occupancy cap (in-flight), complementing the per-minute limits above:
	// holds a lease for the whole request, so it must wrap everything after it.
	// Disabled unless RELAY_MAX_CONCURRENT_PER_TOKEN/_PER_TENANT is set.
	playgroundRouter.Use(middleware.RelayConcurrencyLimit())
	playgroundRouter.Use(middleware.Distribute())
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
	// StampRelayFormat must be the very first Use() on this group: every
	// middleware mounted below it (TokenAuth, PoolBalanceCheck, the rate
	// limiters) can reject the request, and gin snapshots the chain at
	// Group()/Use() time — stamping later would leave those rejections
	// still answering the OpenAI default.
	relayV1Router.Use(middleware.StampRelayFormat())
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
	// Same enforcement chain as relayV1Router (same order, minus the
	// post-Distribute model dimension — task routes have no per-model limits):
	// these task groups used to mount only TokenAuth+PoolBalanceCheck, so none
	// of the rate/cost ceilings applied to /suno at all. Every middleware here
	// no-ops or fails open when its context/config is absent, so a deployment
	// with the features off behaves exactly as before.
	relaySunoRouter.Use(middleware.TokenAuth(), middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(), middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(), middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(), middleware.Distribute())
	{
		relaySunoRouter.POST("/submit/:action", handler.RelayTask)
		relaySunoRouter.POST("/fetch", handler.RelayTask)
		relaySunoRouter.GET("/fetch/:id", handler.RelayTask)
	}

	// OpenAI-compatible music generation routes (used by lurus-creator)
	relayMusicRouter := router.Group("/v1/audio")
	// Enforcement chain: see the /suno comment above. NOTE this group is a
	// sibling of relayV1Router's /v1/audio/* routes, not a child — /v1/music
	// paths never passed through the /v1 chain's limiters.
	relayMusicRouter.Use(middleware.TokenAuth(), middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(), middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(), middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(), middleware.Distribute())
	{
		relayMusicRouter.POST("/music", handler.RelayTask)
		relayMusicRouter.GET("/music/:task_id", handler.RelayTask)
	}

	// Invariant (2026-08-31, closing the gap the /suno and /v1/audio/music
	// fixes above left open): every BILLED relay group mounts the FULL
	// governance chain, same order as relayV1Router — TokenAuth,
	// PoolBalanceCheck, CostSpikeLimit, EntitlementCheck,
	// ModelRequestRateLimit, BusinessRateLimit, RelayConcurrencyLimit,
	// Distribute, BusinessModelRateLimit. This native Gemini group used to
	// mount only TokenAuth+PoolBalanceCheck+ModelRequestRateLimit+Distribute,
	// so cost-spike protection, entitlement checks, per-token/tenant RPM+TPM,
	// the in-flight concurrency cap, and the per-model RPM+TPM dimension
	// never applied to /v1beta at all.
	relayGeminiRouter := router.Group("/v1beta")
	// StampRelayFormat first — same reasoning as relayV1Router above: every
	// middleware mounted on this group after it can reject the request.
	relayGeminiRouter.Use(middleware.StampRelayFormat())
	relayGeminiRouter.Use(middleware.TokenAuth())
	relayGeminiRouter.Use(middleware.PoolBalanceCheck())
	relayGeminiRouter.Use(middleware.CostSpikeLimit())
	relayGeminiRouter.Use(middleware.EntitlementCheck())
	relayGeminiRouter.Use(middleware.ModelRequestRateLimit())
	relayGeminiRouter.Use(middleware.BusinessRateLimit())
	relayGeminiRouter.Use(middleware.RelayConcurrencyLimit())
	{
		// BusinessModelRateLimit needs the model + tenant stash that only
		// exist once Distribute has parsed the request, so — like
		// relayV1Router's httpRouter/wsRouter sub-groups — it is mounted on
		// its own sub-group AFTER Distribute, not alongside the chain above.
		geminiHTTPRouter := relayGeminiRouter.Group("")
		geminiHTTPRouter.Use(middleware.Distribute(), middleware.BusinessModelRateLimit())
		// Gemini API 路径格式: /v1beta/models/{model_name}:{action}
		geminiHTTPRouter.POST("/models/*path", func(c *gin.Context) {
			handler.Relay(c, types.RelayFormatGemini)
		})
	}
}

func registerMjRouterGroup(relayMjRouter *gin.RouterGroup) {
	relayMjRouter.Use(middleware.TokenAuth(), middleware.PoolBalanceCheck())

	// Image proxy: chain unchanged from before the enforcement mount below
	// (TokenAuth + PoolBalanceCheck + Distribute; Distribute is a no-op for the
	// GET-only proxy). Deliberately kept OUTSIDE the enforcement chain: gallery
	// UIs burst-load images, and counting each thumbnail GET against the user's
	// request window would starve the actual submit calls. Pinned by
	// TestSetRelayRouter_MjImageProxy_NotRateLimited.
	imageGroup := relayMjRouter.Group("")
	imageGroup.Use(middleware.Distribute())
	imageGroup.GET("/image/:id", relay.RelayMidjourneyImage)

	// Same enforcement chain as relayV1Router, same order — in particular
	// BEFORE Distribute, so the ceilings still reject when no channel is
	// configured (a 503 from Distribute must not mask a 429). Before this,
	// none of the rate/cost ceilings applied to any /mj submit route.
	submitGroup := relayMjRouter.Group("")
	submitGroup.Use(middleware.CostSpikeLimit(), middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(), middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(), middleware.Distribute())
	{
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
}
