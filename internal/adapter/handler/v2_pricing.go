package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// pricingCallerGroup resolves the caller's user group for the group_ratio
// narrowing below, the same way v1 GetPricing does: from the user row behind
// the session id. An unresolvable caller (no id on the context — the v2 JWT
// path does not always carry one) resolves to "", which
// app.GetUserUsableGroups answers with the globally usable groups only, never
// with a group that exists solely for some tenant's channels.
func pricingCallerGroup(c *gin.Context) string {
	userId, exists := c.Get("id")
	if !exists {
		return ""
	}
	id, ok := userId.(int)
	if !ok {
		return ""
	}
	user, err := repo.GetUserCache(id)
	if err != nil || user == nil {
		return ""
	}
	return user.Group
}

// GetPricingV2 returns the public pricing catalogue for a tenant's users.
// Route: GET /api/v2/:tenant_slug/pricing
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

	// Projected onto the slug's tenant: the platform-shared channels plus
	// that tenant's own. The whole catalogue used to come back here too, so
	// this page listed models only another tenant's channels could serve.
	rawPricing := repo.GetPricingForTenant(tenant.Id)
	type pricingItem struct {
		ModelName  interface{} `json:"model_name"`
		Vendor     interface{} `json:"vendor"`
		QuotaType  interface{} `json:"quota_type"`
		ModelRatio interface{} `json:"model_ratio"`
		ModelPrice interface{} `json:"model_price"`
		// CacheRatio is nil (omitted) when the live cache_ratio map has no
		// entry for this model; when present it can be either an admin edit
		// or one of ratio_setting's shipped defaultCacheRatio entries seeded
		// at boot — not necessarily admin-configured. The console prefills
		// its editable input from this field either way, closing the
		// write-only gap the field used to have.
		CacheRatio *float64 `json:"cache_ratio,omitempty"`
		// ContextTiers is omitted for a model with no tier list — the
		// mechanism ships default-empty (billing-pricing-14), so this is
		// nil/omitted for every model until an admin writes one.
		ContextTiers           []ratio_setting.ContextTier `json:"context_tiers,omitempty"`
		EnableGroups           interface{}                 `json:"enable_groups"`
		SupportedEndpointTypes interface{}                 `json:"supported_endpoint_types"`
	}

	// Build vendor id→name lookup once.
	vendors := repo.GetVendors()
	vendorByID := make(map[int]string, len(vendors))
	for _, v := range vendors {
		vendorByID[v.ID] = v.Name
	}

	cacheRatios := ratio_setting.GetCacheRatioCopy()
	contextTiers := ratio_setting.GetContextLengthTiersCopy()
	pricing := make([]pricingItem, 0, len(rawPricing))
	for _, p := range rawPricing {
		item := pricingItem{
			ModelName:              p.ModelName,
			Vendor:                 vendorByID[p.VendorID],
			QuotaType:              p.QuotaType,
			ModelRatio:             p.ModelRatio,
			ModelPrice:             p.ModelPrice,
			EnableGroups:           p.EnableGroup,
			SupportedEndpointTypes: p.SupportedEndpointTypes,
		}
		if cr, ok := cacheRatios[p.ModelName]; ok {
			item.CacheRatio = &cr
		}
		if ct, ok := contextTiers[p.ModelName]; ok && len(ct) > 0 {
			item.ContextTiers = ct
		}
		pricing = append(pricing, item)
	}

	// group_ratio is narrowed to the groups this caller may actually use, the
	// same narrowing v1 GetPricing and GetSwitchPricing apply. The raw map is
	// process-global: publishing it handed every authenticated user of every
	// tenant the names of groups configured for one tenant's channels — the
	// disclosure this cycle removed from the model list right above.
	usableGroup := app.GetUserUsableGroups(pricingCallerGroup(c))
	groupRatio := make(map[string]float64)
	for k, v := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[k]; !ok {
			continue
		}
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
			"version":     currentPricingVersion(),
		},
	})
}
