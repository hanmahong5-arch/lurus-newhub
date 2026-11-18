package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

func mountV2AdminGovernanceAndOps(adminRoute *gin.RouterGroup) {
	adminRoute.GET("/content-rules", handler.ListPlatformContentRulesV2)
	adminRoute.POST("/content-rules", handler.CreatePlatformContentRuleV2)
	adminRoute.PUT("/content-rules/:id", handler.UpdatePlatformContentRuleV2)
	adminRoute.DELETE("/content-rules/:id", handler.DeletePlatformContentRuleV2)
	adminRoute.GET("/channel-templates", handler.ListChannelTemplatesV2)
	adminRoute.POST("/channel-templates", handler.CreateChannelTemplateV2)
	adminRoute.PUT("/channel-templates/:id", handler.UpdateChannelTemplateV2)
	adminRoute.DELETE("/channel-templates/:id", handler.DeleteChannelTemplateV2)
	adminRoute.POST("/channel-templates/:id/apply", handler.ApplyChannelTemplateV2)
	adminRoute.GET("/channel-templates/:id/applications", handler.ListChannelTemplateApplicationsV2)

	adminRoute.GET("/options", handler.ListAdminOptionsV2)
	adminRoute.PUT("/options", handler.UpdateAdminOptionV2)
	adminRoute.GET("/stats", handler.GetSystemStatsV2)
	adminRoute.POST("/switch/presets", handler.CreateSwitchPreset)
	adminRoute.GET("/whitelabel/hmac-key", handler.GetWhiteLabelHMACKey)

	govRoute := adminRoute.Group("/governance")
	govRoute.Use(middleware.CriticalRateLimit())
	govRoute.GET("/channels", handler.GetGovernanceChannelDistribution)
	govRoute.GET("/fingerprints", handler.GetGovernanceFingerprintStats)
	govRoute.GET("/latency", handler.GetGovernanceLatencyStats)
	govRoute.GET("/efficiency", handler.GetGovernanceEfficiencyStats)
	govRoute.GET("/savings", handler.GetGovernanceSavings)

	adminRoute.GET("/audit/coverage", handler.GetAuditCoverageV2)
	adminRoute.GET("/gateway/health", handler.GetGatewayHealthV2)
	adminRoute.GET("/gateway/relay-engine", handler.GetRelayEngineV2)

	adminRoute.GET("/routing/affinity", handler.GetAffinityStatsV2)
	adminRoute.DELETE("/routing/affinity", handler.PurgeAllAffinityBindingsV2)
	adminRoute.DELETE("/routing/affinity/:key", handler.PurgeAffinityBindingV2)

	adminRoute.GET("/analytics/model-performance", middleware.CriticalRateLimit(), handler.GetModelPerformanceV2)
	adminRoute.GET("/model-pools", middleware.CriticalRateLimit(), handler.GetModelPoolsV2)
	adminRoute.GET("/analytics/rankings", middleware.CriticalRateLimit(), handler.GetRankingsV2)
	adminRoute.GET("/logs/export", middleware.CriticalRateLimit(), handler.ExportAdminLogsV2)
	adminRoute.GET("/logs/:request_id/body", handler.GetAdminLogBodyV2)

	adminRoute.GET("/security/totp-stats", middleware.CriticalRateLimit(), handler.GetAdminTotpStatsV2)
	adminRoute.POST("/security/users/:id/totp/force-disable",
		middleware.CriticalRateLimit(), middleware.SecureVerificationRequired(), handler.ForceDisableTotpV2)
	adminRoute.GET("/system/tasks", handler.GetSystemTasksV2)

	authzRoute := adminRoute.Group("/authz")
	authzRoute.GET("/grants", handler.ListGrantsV2)
	authzRoute.POST("/grants", handler.CreateGrantV2)
	authzRoute.DELETE("/grants/:id", handler.RevokeGrantV2)
	authzRoute.GET("/catalog", handler.GetAuthzCatalogV2)
}

func mountV2DelegatedAudit(apiV2 *gin.RouterGroup) {
	auditRoute := apiV2.Group("/admin/audit")
	auditRoute.Use(middleware.RootOrGranted("audit", "read"))
	auditRoute.GET("/events", middleware.CriticalRateLimit(), handler.GetAuditEvents)
	auditRoute.GET("/actions", handler.ListAuditActionsV2)
	auditRoute.GET("/export", middleware.CriticalRateLimit(), handler.ExportAuditEventsV2)
	auditRoute.GET("/chain-verify", middleware.CriticalRateLimit(), handler.VerifyAuditChainV2)
}
