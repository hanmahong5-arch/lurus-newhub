package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// V2 Platform Admin Controllers
// Platform-level administration operations
// These controllers use v1 session authentication with root role requirement
// ============================================================================

// ListUserMappingsV2 lists all user identity mappings (platform admin only)
// Route: GET /api/v2/admin/mappings
func ListUserMappingsV2(c *gin.Context) {
	// Get user ID from session (v1 auth)
	userId := c.GetInt("id")
	role := c.GetInt("role")

	// Check root role
	if role < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Platform admin role required",
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

	offset := (page - 1) * pageSize

	// Parse filter parameters. Accept the canonical idp_subject query param and
	// fall back to the deprecated zitadel_user_id for back-compat.
	tenantID := c.Query("tenant_id")
	idpSubject := c.Query("idp_subject")
	if idpSubject == "" {
		idpSubject = c.Query("zitadel_user_id")
	}

	var mappings []*repo.UserIdentityMapping
	var total int64
	var err error

	if idpSubject != "" {
		// List by OIDC subject across all tenants
		mappings, err = repo.ListUserMappingsByIDPSubject(idpSubject)
		if err != nil {
			common.SysError("Failed to list user mappings: " + err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to retrieve user mappings",
			})
			return
		}
		total = int64(len(mappings))
		// Manual pagination
		end := offset + pageSize
		if end > len(mappings) {
			end = len(mappings)
		}
		if offset < len(mappings) {
			mappings = mappings[offset:end]
		} else {
			mappings = []*repo.UserIdentityMapping{}
		}
	} else if tenantID != "" {
		// List by tenant
		mappings, total, err = repo.ListUserMappingsByTenant(tenantID, offset, pageSize)
	} else {
		// List all mappings (use system DB to bypass tenant isolation)
		err = repo.GetSystemDB().Model(&repo.UserIdentityMapping{}).
			Where("is_active = ?", true).
			Count(&total).Error
		if err == nil {
			err = repo.GetSystemDB().Model(&repo.UserIdentityMapping{}).
				Where("is_active = ?", true).
				Order("created_at DESC").
				Offset(offset).
				Limit(pageSize).
				Find(&mappings).Error
		}
	}

	if err != nil {
		common.SysError("Failed to list user mappings: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve user mappings",
		})
		return
	}

	_ = userId // Suppress unused variable warning

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"mappings":  mappings,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetUserMappingV2 retrieves a specific user mapping (platform admin only)
// Route: GET /api/v2/admin/mappings/:id
func GetUserMappingV2(c *gin.Context) {
	// Check root role
	role := c.GetInt("role")
	if role < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Platform admin role required",
		})
		return
	}

	// Get mapping ID from URL
	mappingID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid mapping ID",
		})
		return
	}

	// Get mapping from system DB
	var mapping repo.UserIdentityMapping
	err = repo.GetSystemDB().Where("id = ?", mappingID).First(&mapping).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User mapping not found",
		})
		return
	}

	// Get associated user info
	user, err := repo.GetUserById(mapping.LurusUserID, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"mapping": mapping,
				"user":    nil,
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"mapping": mapping,
			"user": gin.H{
				"id":           user.Id,
				"username":     user.Username,
				"display_name": user.DisplayName,
				"email":        user.Email,
				"role":         user.Role,
				"status":       user.Status,
				"tenant_id":    user.TenantId,
			},
		},
	})
}

// DeleteUserMappingV2 deletes/deactivates a user mapping (platform admin only)
// Route: DELETE /api/v2/admin/mappings/:id
func DeleteUserMappingV2(c *gin.Context) {
	// Check root role
	role := c.GetInt("role")
	if role < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Platform admin role required",
		})
		return
	}

	// Get mapping ID from URL
	mappingID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid mapping ID",
		})
		return
	}

	// Check if mapping exists
	var mapping repo.UserIdentityMapping
	err = repo.GetSystemDB().Where("id = ?", mappingID).First(&mapping).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "User mapping not found",
		})
		return
	}

	// Parse delete mode
	hardDelete := c.Query("hard") == "true"

	if hardDelete {
		// Hard delete the mapping
		err = repo.DeleteUserMapping(mappingID)
	} else {
		// Soft delete (deactivate) the mapping
		err = repo.DeactivateUserMapping(mappingID)
	}

	if err != nil {
		common.SysError("Failed to delete user mapping: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete user mapping",
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionTenantMappingDeleted, governance.ResourceTenant, mappingID,
		fmt.Sprintf(`{"hard_delete":%t,"lurus_user_id":%d}`, hardDelete, mapping.LurusUserID)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User mapping deleted successfully",
		"data": gin.H{
			"hard_delete": hardDelete,
		},
	})
}

// GetSystemStatsV2 retrieves system-wide statistics (platform admin only)
// Route: GET /api/v2/admin/stats
func GetSystemStatsV2(c *gin.Context) {
	// Check root role
	role := c.GetInt("role")
	if role < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Platform admin role required",
		})
		return
	}

	// Get system-wide statistics
	var totalUsers int64
	var totalTokens int64
	var totalChannels int64
	var totalTenants int64
	var totalMappings int64
	var totalRedemptions int64

	db := repo.GetSystemDB()

	// Count users
	db.Model(&repo.User{}).Count(&totalUsers)

	// Count tokens
	db.Model(&repo.Token{}).Count(&totalTokens)

	// Count channels
	totalChannels, _ = repo.CountAllChannels()

	// Count tenants
	db.Model(&repo.Tenant{}).Count(&totalTenants)

	// Count user mappings
	db.Model(&repo.UserIdentityMapping{}).Where("is_active = ?", true).Count(&totalMappings)

	// Count redemptions
	db.Model(&repo.Redemption{}).Count(&totalRedemptions)

	// Get quota statistics
	var quotaStats struct {
		TotalQuota int64 `gorm:"column:total_quota"`
		UsedQuota  int64 `gorm:"column:used_quota"`
	}
	db.Model(&repo.User{}).Select("SUM(quota) as total_quota, SUM(used_quota) as used_quota").Scan(&quotaStats)

	// Get channel statistics by type
	channelsByType, _ := repo.CountChannelsGroupByType()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"users": gin.H{
				"total": totalUsers,
			},
			"tokens": gin.H{
				"total": totalTokens,
			},
			"channels": gin.H{
				"total":   totalChannels,
				"by_type": channelsByType,
			},
			"tenants": gin.H{
				"total": totalTenants,
			},
			"mappings": gin.H{
				"active": totalMappings,
			},
			"quota": gin.H{
				"total": quotaStats.TotalQuota,
				"used":  quotaStats.UsedQuota,
			},
			"billing": gin.H{
				"redemptions_total": totalRedemptions,
			},
		},
	})
}
