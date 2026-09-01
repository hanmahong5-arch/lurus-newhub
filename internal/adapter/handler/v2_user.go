package handler

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// emailRegex is a simple regex for basic email validation
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

// GetSelfV2 retrieves the current user's information (v2 API with tenant context)
// Route: GET /api/v2/:tenant_slug/user/me
func GetSelfV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Get user from database
	user, err := repo.GetUserById(tenantCtx.UserID, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// Token count is TENANT-SCOPED. CountUserTokens counts every token the user
	// owns across all tenants, which on a multi-tenant hub both over-reports the
	// dashboard's "active keys" card and leaks a cross-tenant cardinality.
	tokenCount, _ := repo.CountUserTokensByTenant(user.Id, tenantCtx.TenantID)

	// Get daily quota info
	dailyQuotaInfo, _ := repo.GetUserDailyQuotaInfo(user.Id)

	// Build daily quota response
	var dailyQuota interface{}
	if dailyQuotaInfo != nil {
		dailyQuota = gin.H{
			"limit":             dailyQuotaInfo.DailyQuota,
			"used":              dailyQuotaInfo.DailyUsed,
			"remaining":         dailyQuotaInfo.DailyRemaining,
			"last_reset":        dailyQuotaInfo.LastDailyReset,
			"is_using_fallback": dailyQuotaInfo.IsUsingFallback,
		}
	}

	// This response is a strict SUPERSET of the v1 GetSelf projection, because
	// the v2 route used to be wired to GetSelf and two legacy-shell consumers
	// (components/topup/index.jsx and hooks/dashboard/useDashboardData.js) push
	// the whole payload into the global user state — dropping setting /
	// sidebar_modules / permissions here would break the legacy top-up page.
	userSetting := user.GetSetting()
	permissions := calculateUserPermissions(user.Role)
	// Admin remarks are not the user's to see (same rule as GetSelf).
	user.Remark = ""

	// Build response (exclude sensitive fields)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"role":         user.Role,
			"status":       user.Status,
			"quota":        user.Quota,
			"used_quota":   user.UsedQuota,
			// `quota` IS the spendable balance — the relay path decrements it and
			// increments used_quota on the same settlement, so remaining is quota
			// itself. The previous `user.Quota - user.UsedQuota` subtracted the
			// spend a second time and under-reported the balance by exactly the
			// lifetime spend (measured on a live account: quota=19998688,
			// used_quota=1312, funded 20000000 — the sum, not the difference, is
			// the invariant).
			"remaining_quota": user.Quota,
			"request_count":   user.RequestCount,
			"group":           user.Group,
			"tenant_id":       tenantCtx.TenantID,
			"token_count":     tokenCount,
			"idp_user":        tenantCtx.IDPSubject,
			"roles":           tenantCtx.Roles,
			"daily_quota":     dailyQuota,
			// v1 GetSelf parity (legacy shell reads these off the shared store)
			"setting":         user.Setting,
			"sidebar_modules": userSetting.SidebarModules,
			"permissions":     permissions,
		},
	})
}

// UpdateSelfV2 updates the current user's information (v2 API with tenant context)
// Route: PUT /api/v2/:tenant_slug/user/me
func UpdateSelfV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Parse request body
	var req struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Get current user
	user, err := repo.GetUserById(tenantCtx.UserID, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User not found",
		})
		return
	}

	// Update fields if provided
	if req.DisplayName != "" {
		if err := app.ValidateDisplayName(req.DisplayName); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		user.DisplayName = req.DisplayName
	}

	if req.Email != "" {
		// Validate email format
		email := strings.TrimSpace(req.Email)
		if !emailRegex.MatchString(email) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "Invalid email format",
			})
			return
		}
		user.Email = email
	}

	// Save changes
	err = user.Update(false)
	if err != nil {
		common.SysError("Failed to update user: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update user",
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, user.Id,
		governance.ActionUserSelfUpdated, governance.ResourceUser, user.Id,
		fmt.Sprintf(`{"display_name_changed":%t,"email_changed":%t}`,
			req.DisplayName != "", req.Email != "")))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User updated successfully",
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
		},
	})
}
