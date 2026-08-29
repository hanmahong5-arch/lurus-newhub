package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func GetAllRedemptions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	var redemptions []*repo.Redemption
	var total int64
	var err error
	// Non-root callers see only their own tenant's redemption codes.
	if c.GetInt("role") >= common.RoleRootUser {
		redemptions, total, err = repo.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	} else {
		redemptions, total, err = repo.GetRedemptionsByTenant(c.GetString("tenant_id"), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	pageInfo := common.GetPageQuery(c)
	var redemptions []*repo.Redemption
	var total int64
	var err error
	if c.GetInt("role") >= common.RoleRootUser {
		redemptions, total, err = repo.SearchRedemptions(keyword, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	} else {
		redemptions, total, err = repo.SearchRedemptionsByTenant(c.GetString("tenant_id"), keyword, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption, err := repo.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if enforceTenantScope(c, redemption.TenantId) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    redemption,
	})
	return
}

func AddRedemption(c *gin.Context) {
	redemption := repo.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "兑换码名称长度必须在1-20之间",
		})
		return
	}
	if redemption.Count <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "兑换码个数必须大于0",
		})
		return
	}
	if redemption.Count > 100 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "一次兑换码批量生成的个数不能大于 100",
		})
		return
	}
	if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	// Get tenant ID from context (defaults to "default" for v1 API)
	tenantId := common.GetContextKeyString(c, "tenant_id")
	if tenantId == "" {
		tenantId = "default"
	}

	var keys []string
	for i := 0; i < redemption.Count; i++ {
		key := common.GetUUID()
		cleanRedemption := repo.Redemption{
			UserId:      c.GetInt("id"),
			TenantId:    tenantId,
			Name:        redemption.Name,
			Key:         key,
			CreatedTime: common.GetTimestamp(),
			Quota:       redemption.Quota,
			ExpiredTime: redemption.ExpiredTime,
		}
		err = repo.RedemptionInsert(&cleanRedemption)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
				"data":    keys,
			})
			return
		}
		keys = append(keys, key)
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionRedemptionCreated, governance.ResourceRedemption, 0,
		fmt.Sprintf(`{"name":%q,"count":%d,"quota":%d}`, redemption.Name, redemption.Count, redemption.Quota)))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    keys,
	})
	return
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	existing, err := repo.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if enforceTenantScope(c, existing.TenantId) {
		return
	}
	err = repo.DeleteRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionRedemptionDeleted, governance.ResourceRedemption, id, ""))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateRedemption(c *gin.Context) {
	statusOnly := c.Query("status_only")
	redemption := repo.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cleanRedemption, err := repo.GetRedemptionById(redemption.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if enforceTenantScope(c, cleanRedemption.TenantId) {
		return
	}
	if statusOnly == "" {
		if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		// If you add more fields, please also update redemption.Update()
		cleanRedemption.Name = redemption.Name
		cleanRedemption.Quota = redemption.Quota
		cleanRedemption.ExpiredTime = redemption.ExpiredTime
	}
	if statusOnly != "" {
		// A used code is terminal. RedemptionUpdate persists the status column
		// (repo/redemption.go Select list) and Redeem()'s only gate is
		// Status == Enabled, so letting status be reset here would let a
		// tenant admin replay an already-redeemed code to mint quota in a
		// loop.
		if cleanRedemption.Status == common.RedemptionCodeStatusUsed {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "已使用的兑换码状态不可修改",
			})
			return
		}
		// Only enabled/disabled are settable here; reject unknown values so a
		// code can never be forced into an out-of-band state.
		if redemption.Status != common.RedemptionCodeStatusEnabled &&
			redemption.Status != common.RedemptionCodeStatusDisabled {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的兑换码状态",
			})
			return
		}
		cleanRedemption.Status = redemption.Status
	}
	err = repo.RedemptionUpdate(cleanRedemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionRedemptionUpdated, governance.ResourceRedemption, cleanRedemption.Id,
		fmt.Sprintf(`{"status_only":%q,"name":%q,"status":%d}`, statusOnly, cleanRedemption.Name, cleanRedemption.Status)))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanRedemption,
	})
	return
}

func DeleteInvalidRedemption(c *gin.Context) {
	// AdminAuth is satisfied by a per-tenant admin too, so a non-root prune must
	// stay inside the caller's tenant; root keeps the global sweep. Without this
	// split a role-10 tenant admin purged every tenant's spent/expired codes.
	var rows int64
	var err error
	if c.GetInt("role") >= common.RoleRootUser {
		rows, err = repo.DeleteInvalidRedemptions()
	} else {
		rows, err = repo.DeleteInvalidRedemptionsByTenant(c.GetString("tenant_id"))
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionRedemptionInvalidDeleted, governance.ResourceRedemption, 0,
		fmt.Sprintf(`{"rows":%d}`, rows)))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func validateExpiredTime(expired int64) error {
	if expired != 0 && expired < common.GetTimestamp() {
		return errors.New("过期时间不能早于当前时间")
	}
	return nil
}
