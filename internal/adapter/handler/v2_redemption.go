package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// V2 Redemption Controllers
// Redemption code management with tenant isolation
// ============================================================================

// redemptionView is the field-whitelisted projection returned by
// ListRedemptionsV2. Excludes user_id (creator), tenant_id (implicit from
// route), and deleted_at (soft-delete marker) so the wire shape is the
// minimum the v2 console needs.
type redemptionView struct {
	Id           int    `json:"id"`
	Name         string `json:"name"`
	Key          string `json:"key"`
	Status       int    `json:"status"`
	Quota        int    `json:"quota"`
	CreatedTime  int64  `json:"created_time"`
	ExpiredTime  int64  `json:"expired_time"`
	RedeemedTime int64  `json:"redeemed_time"`
	UsedUserId   int    `json:"used_user_id"`
}

// RedeemCodeV2 redeems a code for quota
// Route: POST /api/v2/:tenant_slug/redeem
func RedeemCodeV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Parse request body — accept both "key" (frontend/v1 compat) and "code"
	var req struct {
		Key  string `json:"key"`
		Code string `json:"code"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Use "key" if provided, fall back to "code"
	redeemCode := req.Key
	if redeemCode == "" {
		redeemCode = req.Code
	}
	if redeemCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Redemption code is required",
		})
		return
	}

	// Validate code format
	if len(redeemCode) != 32 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid redemption code format",
		})
		return
	}

	// Redeem the code. repo.RedemptionErrorMessage/RedemptionErrorCode (not
	// err.Error() directly) — cycle13 L3: a genuine transaction/driver
	// failure inside repo.Redeem used to reach this response body verbatim
	// (constraint/column names included); repo.Redeem itself no longer
	// returns that raw text, and RedemptionErrorMessage is defence in depth
	// on top. error_code lets a v2 console/API caller branch without
	// string-matching the (Chinese, switch-contract-pinned) message.
	quota, err := repo.Redeem(redeemCode, tenantCtx.UserID)
	if err != nil {
		// 400 for the caller's mistakes, 500 for a hub-side fault
		// (REDEMPTION_FAILED) — see redemptionFailureStatus.
		c.JSON(redemptionFailureStatus(err, http.StatusBadRequest), gin.H{
			"success":    false,
			"message":    repo.RedemptionErrorMessage(err),
			"error_code": repo.RedemptionErrorCode(err),
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionRedemptionRedeemed, governance.ResourceRedemption, 0,
		fmt.Sprintf(`{"quota_added":%d}`, quota)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Code redeemed successfully",
		"data": gin.H{
			"quota_added": quota,
		},
	})
}

// ListRedemptionsV2 lists redemption codes (admin only)
// Route: GET /api/v2/:tenant_slug/redemptions
func ListRedemptionsV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Check admin role
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Parse pagination parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	startIdx := (page - 1) * pageSize

	// Parse search keyword
	keyword := c.Query("keyword")

	var redemptions []*repo.Redemption
	var total int64

	if keyword != "" {
		redemptions, total, err = repo.SearchRedemptionsByTenant(tenantCtx.TenantID, keyword, startIdx, pageSize)
	} else {
		redemptions, total, err = repo.GetRedemptionsByTenant(tenantCtx.TenantID, startIdx, pageSize)
	}

	if err != nil {
		common.SysError("Failed to get redemptions: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve redemptions",
		})
		return
	}

	items := make([]redemptionView, 0, len(redemptions))
	for _, r := range redemptions {
		key := r.Key
		if r.Status == common.RedemptionCodeStatusEnabled {
			key = maskRedemptionKey(key)
		}
		items = append(items, redemptionView{
			Id:           r.Id,
			Name:         r.Name,
			Key:          key,
			Status:       r.Status,
			Quota:        r.Quota,
			CreatedTime:  r.CreatedTime,
			ExpiredTime:  r.ExpiredTime,
			RedeemedTime: r.RedeemedTime,
			UsedUserId:   r.UsedUserId,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"redemptions": items,
			"total":       total,
			"page":        page,
			"page_size":   pageSize,
		},
	})
}

// CreateRedemptionV2 creates new redemption codes (admin only)
// Route: POST /api/v2/:tenant_slug/redemptions
func CreateRedemptionV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Check admin role
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Parse request body
	var req struct {
		Name        string `json:"name" binding:"required"`
		Quota       int    `json:"quota" binding:"required,min=1"`
		Count       int    `json:"count" binding:"required,min=1,max=100"` // Number of codes to generate
		ExpiredTime int64  `json:"expired_time"`                           // 0 means never expires
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Validate name length
	if len(req.Name) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Name too long (max 50 characters)",
		})
		return
	}

	// Validate quota value
	maxQuota := int(1000000000 * common.QuotaPerUnit)
	if req.Quota > maxQuota {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Quota value exceeds maximum allowed",
		})
		return
	}

	// Validate expired time
	if req.ExpiredTime != 0 && req.ExpiredTime < common.GetTimestamp() {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Expiration time must be in the future",
		})
		return
	}

	// Generate redemption codes
	createdCodes := make([]gin.H, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		key := common.GetRandomString(32)

		redemption := &repo.Redemption{
			UserId:      tenantCtx.UserID,
			TenantId:    tenantCtx.TenantID,
			Key:         key,
			Name:        req.Name,
			Quota:       req.Quota,
			Status:      common.RedemptionCodeStatusEnabled,
			CreatedTime: common.GetTimestamp(),
			ExpiredTime: req.ExpiredTime,
		}

		if err := repo.RedemptionInsert(redemption); err != nil {
			common.SysError("Failed to create redemption: " + err.Error())
			// Continue with remaining codes
			continue
		}

		createdCodes = append(createdCodes, gin.H{
			"id":   redemption.Id,
			"key":  key, // Return full key on creation
			"name": redemption.Name,
		})
	}

	if len(createdCodes) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create any redemption codes",
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionRedemptionCreated, governance.ResourceRedemption, 0,
		fmt.Sprintf(`{"name":%q,"count":%d,"quota":%d,"created":%d}`,
			req.Name, req.Count, req.Quota, len(createdCodes))))

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Redemption codes created successfully",
		"data": gin.H{
			"codes":     createdCodes,
			"count":     len(createdCodes),
			"requested": req.Count,
		},
	})
}

// DeleteRedemptionV2 deletes a redemption code (admin only)
// Route: DELETE /api/v2/:tenant_slug/redemptions/:id
func DeleteRedemptionV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Check admin role
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Get redemption ID from URL
	redemptionID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid redemption ID",
		})
		return
	}

	// Get existing redemption to verify it exists
	redemption, err := repo.GetRedemptionById(redemptionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Redemption code not found",
		})
		return
	}

	// Verify tenant ownership
	if redemption.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Access denied",
		})
		return
	}

	// Delete redemption
	if err := repo.RedemptionDelete(redemption); err != nil {
		common.SysError("Failed to delete redemption: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete redemption code",
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionRedemptionDeleted, governance.ResourceRedemption, redemptionID,
		fmt.Sprintf(`{"name":%q}`, redemption.Name)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Redemption code deleted successfully",
	})
}

// maskRedemptionKey masks a redemption key for display
func maskRedemptionKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "************************" + key[len(key)-4:]
}
