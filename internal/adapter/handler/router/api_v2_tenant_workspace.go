package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

func mountV2TenantWorkspace(apiV2 *gin.RouterGroup) {
	apiV2.GET("/:tenant_slug/user/me", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.GetSelfV2)
	apiV2.GET("/:tenant_slug/credit-pool/me", middleware.OIDCAuth(), handler.GetCreditPoolForEndUser)
	apiV2.POST("/:tenant_slug/playground/run", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.PlaygroundFanOut)

	playgroundPresets := apiV2.Group("/:tenant_slug/playground/presets")
	playgroundPresets.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	playgroundPresets.GET("", handler.ListPresetsV2)
	playgroundPresets.POST("", handler.CreatePresetV2)
	playgroundPresets.DELETE("/:id", handler.DeletePresetV2)

	tenantTokens := apiV2.Group("/:tenant_slug/tokens")
	tenantTokens.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantTokens.GET("", handler.ListTokensV2)
	tenantTokens.POST("", handler.CreateTokenV2)
	tenantTokens.POST("/batch-delete", handler.DeleteTokensV2)
	tenantTokens.POST("/batch", handler.BatchCreateTokensV2)
	tenantTokens.PUT("/:id", handler.UpdateTokenV2)
	tenantTokens.DELETE("/:id", handler.DeleteTokenV2)
	tenantTokens.POST("/:id/rotate", handler.RotateTokenV2)

	tenantProjects := apiV2.Group("/:tenant_slug/projects")
	tenantProjects.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantProjects.GET("", handler.ListProjectsV2)
	tenantProjects.GET("/spend", handler.GetProjectSpendV2)
	tenantProjects.GET("/:id", handler.GetProjectV2)
	tenantProjects.POST("", handler.CreateProjectV2)
	tenantProjects.PUT("/:id", handler.UpdateProjectV2)
	tenantProjects.DELETE("/:id", handler.DeleteProjectV2)
	tenantProjects.POST("/:id/restore", handler.RestoreProjectV2)
	tenantProjects.GET("/:id/members", handler.ListProjectMembersV2)
	tenantProjects.POST("/:id/members", handler.AddProjectMemberV2)
	tenantProjects.DELETE("/:id/members", handler.RemoveProjectMemberV2)

	tenantInvites := apiV2.Group("/:tenant_slug/invites")
	tenantInvites.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantInvites.POST("", handler.IssueMyTenantInviteV2)
	tenantInvites.GET("", handler.ListMyTenantInvitesV2)
	tenantInvites.POST("/redeem", handler.RedeemMyTenantInviteV2)
	tenantInvites.DELETE("/:id", handler.RevokeMyTenantInviteV2)

	tenantMembers := apiV2.Group("/:tenant_slug/members")
	tenantMembers.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantMembers.GET("", handler.ListTenantMembersV2)
	tenantMembers.PUT("/:user_id/role", handler.SetTenantMemberRoleV2)

	tenantRoutingPolicies := apiV2.Group("/:tenant_slug/routing-policies")
	tenantRoutingPolicies.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantRoutingPolicies.GET("/:model", handler.GetRoutingPolicyV2)
	tenantRoutingPolicies.PUT("/:model", handler.PutRoutingPolicyV2)
	tenantRoutingPolicies.DELETE("/:model", handler.DeleteRoutingPolicyV2)

	tenantDataPolicy := apiV2.Group("/:tenant_slug/data-policy")
	tenantDataPolicy.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantDataPolicy.GET("/retention", handler.GetContentRetentionV2)
	tenantDataPolicy.PUT("/retention", handler.PutContentRetentionV2)
	tenantDataPolicy.PUT("/tokens/:id/retention", handler.PutTokenContentRetentionV2)
	tenantDataPolicy.GET("/rules", handler.ListContentRulesV2)
	tenantDataPolicy.POST("/rules", handler.CreateContentRuleV2)
	tenantDataPolicy.PUT("/rules/:id", handler.UpdateContentRuleV2)
	tenantDataPolicy.DELETE("/rules/:id", handler.DeleteContentRuleV2)
	tenantDataPolicy.GET("/sedimentation", handler.GetSedimentationConsentV2)
	tenantDataPolicy.PUT("/sedimentation", handler.PutSedimentationConsentV2)
}

func mountV2TenantAccountAndChat(apiV2 *gin.RouterGroup) {
	tenantChat := apiV2.Group("/:tenant_slug/chat")
	tenantChat.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantChat.POST("/send", handler.ChatSend)
	tenantChat.GET("/sessions", handler.ListChatSessionsV2)
	tenantChat.POST("/sessions", handler.CreateChatSessionV2)
	tenantChat.GET("/sessions/:id", handler.GetChatSessionV2)
	tenantChat.PATCH("/sessions/:id", handler.UpdateChatSessionV2)
	tenantChat.DELETE("/sessions/:id", handler.DeleteChatSessionV2)

	apiV2.PUT("/:tenant_slug/user/me", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.UpdateSelfV2)
	apiV2.PUT("/:tenant_slug/user/setting", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.UpdateUserSetting)
	apiV2.GET("/:tenant_slug/user/totp/status", middleware.UserAuth(), middleware.TenantSlugGuard(), handler.GetTotpStatus)
	apiV2.POST("/:tenant_slug/user/totp/enroll", middleware.UserAuth(), middleware.TenantSlugGuard(), middleware.CriticalRateLimit(), handler.TotpEnroll)
	apiV2.POST("/:tenant_slug/user/totp/confirm", middleware.UserAuth(), middleware.TenantSlugGuard(), middleware.CriticalRateLimit(), handler.TotpConfirm)
	apiV2.POST("/:tenant_slug/user/totp/disable", middleware.UserAuth(), middleware.TenantSlugGuard(), middleware.CriticalRateLimit(), middleware.SecureVerificationRequired(), handler.TotpDisable)
	apiV2.POST("/:tenant_slug/user/totp/backup-codes/regenerate", middleware.UserAuth(), middleware.TenantSlugGuard(), middleware.TotpBackupCodesRateLimit(), middleware.SecureVerificationRequired(), handler.RegenerateTotpBackupCodes)
}
