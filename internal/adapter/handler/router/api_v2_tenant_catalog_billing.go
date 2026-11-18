package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

func mountV2TenantCatalogAndBilling(apiV2 *gin.RouterGroup) {
	tenantModels := apiV2.Group("/:tenant_slug/models")
	tenantModels.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantModels.GET("", handler.ListModelsV2)
	tenantModels.POST("", handler.CreateModelV2)
	tenantModels.DELETE("/:id", handler.DeleteModelV2)
	tenantModels.GET("/routable", handler.ListRoutableModelsV2)
	tenantModels.GET("/performance", handler.ListModelPerformanceV2)
	tenantModels.GET("/allowlist", handler.GetTenantModelAllowlistV2)
	tenantModels.PUT("/allowlist", handler.PutTenantModelAllowlistV2)

	tenantAudit := apiV2.Group("/:tenant_slug/audit")
	tenantAudit.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantAudit.GET("", handler.ListTenantAuditV2)
	tenantAudit.GET("/export.csv", middleware.CriticalRateLimit(), handler.ExportTenantAuditCSVV2)

	tenantPricing := apiV2.Group("/:tenant_slug/pricing")
	tenantPricing.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantPricing.GET("", handler.GetPricingV2)
	tenantPricing.POST("", handler.UpdatePricingV2)
	tenantPricing.POST("/preview", handler.PreviewPricingV2)

	tenantBilling := apiV2.Group("/:tenant_slug/billing")
	tenantBilling.Use(middleware.UserAuth(), middleware.TenantSlugGuard())
	tenantBilling.GET("/invoices", handler.ListInvoicesV2)
	tenantBilling.GET("/statement", middleware.CriticalRateLimit(), handler.GetBillingStatementV2)
	tenantBilling.GET("/topups", handler.GetTopUpsV2)
}

func mountV2PlatformUserBilling(apiV2 *gin.RouterGroup) {
	platformUser := apiV2.Group("/user")
	platformUser.Use(middleware.OIDCAuth())
	platformUser.GET("/identity-overview", handler.GetIdentityOverview)

	billingRoute := platformUser.Group("/billing")
	billingRoute.GET("/summary", handler.GetBillingSummary)
	billingRoute.GET("/payment-methods", handler.GetBillingPaymentMethods)
	billingRoute.POST("/checkout", handler.CreateBillingCheckout)
	billingRoute.GET("/checkout/:order_no/status", handler.GetBillingCheckoutStatus)
}
