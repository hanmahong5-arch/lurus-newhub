package handler

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// GetPricing serves the v1 price list at GET /api/pricing. The route is
// mounted with NO auth middleware (api-router.go:38, in the "Public routes"
// block), so role and id are never set on it in production: this answers the
// platform-shared catalogue — channels whose tenant_id is "default" or empty —
// to every caller, including the console's legacy /pricing page. Before the
// projection it published every tenant's private model names and channel group
// names to anonymous readers.
//
// The tenant-aware price list is GET /api/v2/:tenant_slug/pricing
// (v2_pricing.go), which is behind UserAuth and answers shared ∪ that slug's
// tenant. A "logged-in caller sees more here" branch was written for this
// handler and then deleted: on this route it could not execute, and an
// unreachable branch is a worse answer than a documented one.
func GetPricing(c *gin.Context) {
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	for s, f := range ratio_setting.GetGroupRatioCopy() {
		groupRatio[s] = f
	}
	var group string
	if exists {
		user, err := repo.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	// "" = the platform-shared catalogue. See the doc comment above for why
	// there is no per-caller branch here.
	pricing := repo.GetPricingForTenant("")

	usableGroup = app.GetUserUsableGroups(group)
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            repo.GetVendors(),
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": repo.GetSupportedEndpointMap(),
		"auto_groups":        app.GetUserAutoGroup(group),
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	// A reset is a pricing change like any other: versioned and audited
	// (TestResetModelRatio_BumpsVersionAndAudits).
	err := writePricingOptionVersioned(c, "ModelRatio", defaultStr, "legacy_reset")
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "重置模型倍率成功",
	})
}
