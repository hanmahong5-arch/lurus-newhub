package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// GetSwitchPricing returns the public pricing catalogue consumed by the Switch
// desktop client's cost dashboard.
//
// Route: GET /api/v2/switch/pricing (no auth — public, cacheable).
//
// It mirrors GetPricingV2's field whitelist (no admin-only usable_group /
// auto_groups) but takes no tenant slug: it is one public catalogue, and the
// catalogue it publishes is the platform-shared one (channels whose tenant_id
// is "default" or empty — repo.GetPricingForTenant("")), not the union of
// every tenant's models. It ADDS two fields so the client can derive
// USD-per-token faithfully instead of guessing:
//
//	completion_ratio — scales output rate off model_ratio
//	quota_per_unit   — the quota↔USD scale (newapi default 500000 quota = $1)
//
// so the client computes:
//
//	input_per_mtok  = model_ratio × (1e6 / quota_per_unit)
//	output_per_mtok = model_ratio × completion_ratio × (1e6 / quota_per_unit)
//
// Read-only; no DB migration.
func GetSwitchPricing(c *gin.Context) {
	// Public and unauthenticated: the platform-shared catalogue only. Passing
	// "" here is what makes GetPricingForTenant drop every model that is only
	// served by some tenant's own channels.
	rawPricing := repo.GetPricingForTenant("")

	type pricingItem struct {
		ModelName              interface{} `json:"model_name"`
		Vendor                 interface{} `json:"vendor"`
		QuotaType              interface{} `json:"quota_type"`
		ModelRatio             interface{} `json:"model_ratio"`
		CompletionRatio        interface{} `json:"completion_ratio"`
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
			CompletionRatio:        p.CompletionRatio,
			ModelPrice:             p.ModelPrice,
			EnableGroups:           p.EnableGroup,
			SupportedEndpointTypes: p.SupportedEndpointTypes,
		})
	}

	// group_ratio narrowed to the groups an anonymous caller may actually
	// use, the same narrowing v1 GetPricing applies (pricing.go). Publishing
	// the raw map put every configured group name — including ones set up for
	// a single tenant's channels — in an unauthenticated response.
	usableGroup := app.GetUserUsableGroups("")
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
			"pricing":        pricing,
			"vendors":        vendorNames,
			"group_ratio":    groupRatio,
			"quota_per_unit": common.QuotaPerUnit,
		},
	})
}
