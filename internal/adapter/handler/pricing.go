package handler

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// GetPricing serves the v1 price list at GET /api/pricing. The route carries
// no auth middleware (api-router.go:38), so the common caller is anonymous and
// gets the platform-shared catalogue only; a session that does reach here is
// answered with shared ∪ its own tenant, and the platform operator (root)
// keeps the global catalogue. Before this projection every tenant's private
// model names and channel group names were in the anonymous answer.
func GetPricing(c *gin.Context) {
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	for s, f := range ratio_setting.GetGroupRatioCopy() {
		groupRatio[s] = f
	}
	var group string
	var callerTenant string
	if exists {
		user, err := repo.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			callerTenant = user.TenantId
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	var pricing []repo.Pricing
	if c.GetInt("role") >= common.RoleRootUser {
		pricing = repo.GetPricing()
	} else {
		pricing = repo.GetPricingForTenant(callerTenant)
	}

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
