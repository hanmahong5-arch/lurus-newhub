package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// GetPricingV2 returns the public pricing catalogue for a tenant's users.
// Route (registered by Opus): GET /api/v2/:tenant_slug/pricing
// Auth: UserAuth middleware (OIDC JWT).
//
// Fields returned are an explicit whitelist — no admin-only fields such as
// usable_group or auto_groups are exposed here.
func GetPricingV2(c *gin.Context) {
	slug := c.Param("tenant_slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug required",
			"error_code": "INVALID_TENANT_SLUG",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	rawPricing := repo.GetPricing()
	type pricingItem struct {
		ModelName              interface{} `json:"model_name"`
		Vendor                 interface{} `json:"vendor"`
		QuotaType              interface{} `json:"quota_type"`
		ModelRatio             interface{} `json:"model_ratio"`
		ModelPrice             interface{} `json:"model_price"`
		EnableGroups           interface{} `json:"enable_groups"`
		SupportedEndpointTypes interface{} `json:"supported_endpoint_types"`
	}

	// Build vendor id→name lookup once.
	vendors := repo.GetVendors()
	vendorByID := make(map[int]string, len(vendors))
	for _, v := range vendors {
		vendorByID[v.ID] = v.Name
	}

	pricing := make([]pricingItem, 0, len(rawPricing))
	for _, p := range rawPricing {
		pricing = append(pricing, pricingItem{
			ModelName:              p.ModelName,
			Vendor:                 vendorByID[p.VendorID],
			QuotaType:              p.QuotaType,
			ModelRatio:             p.ModelRatio,
			ModelPrice:             p.ModelPrice,
			EnableGroups:           p.EnableGroup,
			SupportedEndpointTypes: p.SupportedEndpointTypes,
		})
	}

	// Build group_ratio scoped to all groups (no per-user narrowing — this is
	// a public catalogue; per-user narrowing is v1 GetPricing territory).
	groupRatio := make(map[string]float64)
	for k, v := range ratio_setting.GetGroupRatioCopy() {
		groupRatio[k] = v
	}

	vendorNames := make([]string, 0, len(vendors))
	for _, v := range vendors {
		vendorNames = append(vendorNames, v.Name)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"pricing":     pricing,
			"vendors":     vendorNames,
			"group_ratio": groupRatio,
		},
	})
}
