package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

func mountV2AdminRoot(apiV2 *gin.RouterGroup) {
	adminRoute := apiV2.Group("/admin")
	adminRoute.Use(middleware.RootJWTAuth(), middleware.AuditWriteGuard())

	mountV2AdminTenantsAndUsers(adminRoute)
	mountV2AdminGovernanceAndOps(adminRoute)
}

func mountV2AdminTenantsAndUsers(adminRoute *gin.RouterGroup) {
	tenantMgmt := adminRoute.Group("/tenants")
	tenantMgmt.GET("", handler.ListTenants)
	tenantMgmt.POST("", handler.CreateTenant)
	tenantMgmt.GET("/:id", handler.GetTenant)
	tenantMgmt.PUT("/:id", handler.UpdateTenant)
	tenantMgmt.DELETE("/:id", handler.DeleteTenant)
	tenantMgmt.POST("/:id/enable", handler.EnableTenant)
	tenantMgmt.POST("/:id/disable", handler.DisableTenant)
	tenantMgmt.POST("/:id/suspend", handler.SuspendTenant)
	tenantMgmt.GET("/:id/stats", handler.GetTenantStats)
	tenantMgmt.GET("/:id/model-limits", handler.ListTenantModelLimits)
	tenantMgmt.PUT("/:id/model-limits", handler.UpsertTenantModelLimit)
	tenantMgmt.DELETE("/:id/model-limits", handler.DeleteTenantModelLimit)
	tenantMgmt.GET("/:id/model-allowlist", handler.ListTenantModelAllowlist)
	tenantMgmt.PUT("/:id/model-allowlist", handler.UpsertTenantModelAllowlist)
	tenantMgmt.DELETE("/:id/model-allowlist", handler.DeleteTenantModelAllowlist)
	tenantMgmt.PUT("/:id/switch-teampack", handler.PutSwitchTeamPack)
	tenantMgmt.DELETE("/:id/switch-teampack", handler.DeleteSwitchTeamPack)
	tenantMgmt.POST("/:id/credit-pool", handler.CreateCreditPool)
	tenantMgmt.GET("/:id/credit-pool", handler.GetCreditPool)
	tenantMgmt.POST("/:id/credit-pool/topup", handler.TopupCreditPool)
	tenantMgmt.GET("/:id/credit-pool/usage", handler.ListCreditPoolUsage)
	tenantMgmt.DELETE("/:id/credit-pool", handler.DeleteCreditPool)
	tenantMgmt.POST("/:id/invites", handler.IssueTenantInvite)
	tenantMgmt.GET("/:id/invites", handler.ListTenantInvites)
	tenantMgmt.DELETE("/:id/invites/:invite_id", handler.RevokeTenantInvite)
	tenantMgmt.PUT("/:id/members/:user_id/role", handler.SetTenantMemberRoleAdmin)

	mappingRoute := adminRoute.Group("/mappings")
	mappingRoute.GET("", handler.ListUserMappingsV2)
	mappingRoute.GET("/:id", handler.GetUserMappingV2)
	mappingRoute.DELETE("/:id", handler.DeleteUserMappingV2)

	internalKeysRoute := adminRoute.Group("/internal-keys")
	internalKeysRoute.Use(middleware.CriticalRateLimit())
	internalKeysRoute.GET("", handler.ListInternalApiKeysV2)
	internalKeysRoute.GET("/:id/tenants", handler.ListInternalApiKeyTenantsV2)
	internalKeysRoute.POST("/:id/tenants", handler.GrantInternalApiKeyTenantV2)
	internalKeysRoute.DELETE("/:id/tenants/:tenant_id", handler.RevokeInternalApiKeyTenantV2)

	adminUsers := adminRoute.Group("/users")
	adminUsers.GET("", handler.ListAdminUsersV2)
	adminUsers.GET("/:id", handler.GetAdminUserV2)
	adminUsers.PUT("/:id", handler.UpdateAdminUserV2)
	adminUsers.DELETE("/:id", handler.DeleteAdminUserV2)
	adminUsers.DELETE("/:id/sessions", handler.RevokeUserSessionsAdminV2)
}
