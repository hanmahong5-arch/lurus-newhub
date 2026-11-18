package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func mountV2AuthAndBridge(apiV2 *gin.RouterGroup) {
	apiV2.GET("/:tenant_slug/auth/login", handler.OIDCLoginRedirect)
	apiV2.GET("/oauth/callback", handler.OIDCCallback)
	apiV2.GET("/auth/session-info", handler.GetSessionInfo)
	apiV2.POST("/oauth/logout", handler.OIDCLogout)

	apiV2.GET("/auth/zita-login", handler.ZitaLogin)
	apiV2.GET("/auth/zita-logout", handler.ZitaLogout)
	apiV2.POST("/auth/zita-logout", handler.ZitaLogout)
	if common.ZitaClient != nil {
		apiV2.GET("/me/zita", common.ZitaClient.AuthMiddleware(), handler.GetZitaIdentity)
		apiV2.POST("/auth/zita-bootstrap", middleware.BootstrapRateLimit(), common.ZitaClient.AuthMiddleware(), handler.ZitaBootstrap)
	}

	if handler.BridgeEnabled() {
		apiV2.POST("/bridge/exchange", middleware.BootstrapRateLimit(), handler.BridgeExchange)
	}

	apiV2.POST("/desk/device/code", middleware.BootstrapRateLimit(), handler.StartDeskDeviceCode)
	apiV2.POST("/desk/device/token", middleware.BootstrapRateLimit(), handler.PollDeskDeviceToken)
	apiV2.POST("/desk/device/approve", middleware.UserAuth(), handler.ApproveDeskDeviceCode)

	if handler.FaultSimEnabled() {
		apiV2.POST("/faultsim/v1/chat/completions", handler.FaultSimChatCompletions)
		apiV2.POST("/faultsim/v1/responses", handler.FaultSimResponses)
		apiV2.POST("/faultsim/v1/messages", handler.FaultSimMessages)
		apiV2.POST("/faultsim/v1/systemone", handler.FaultSimSystemOne)
		apiV2.POST("/faultsim/v1/rerank", handler.FaultSimRerank)
		apiV2.POST("/faultsim/v1/embeddings", handler.FaultSimEmbeddings)
		apiV2.POST("/faultsim/suno/submit/:action", handler.FaultSimTaskSubmit)
		apiV2.POST("/faultsim/suno/fetch", handler.FaultSimTaskFetch)
	}
}

func mountV2LutuSearch(apiV2 *gin.RouterGroup) {
	apiV2.POST("/lutu/search", middleware.RequireOIDCToken(), handler.PostWebSearch)
}
