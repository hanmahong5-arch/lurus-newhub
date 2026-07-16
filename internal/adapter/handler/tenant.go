package handler

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ListTenants retrieves all tenants (Platform Admin only)
// Route: GET /api/v2/admin/tenants
func ListTenants(c *gin.Context) {
	// Parse pagination parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	// Get tenants from database
	tenants, total, err := repo.ListTenants(offset, pageSize, status)
	if err != nil {
		common.SysError("Failed to list tenants: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve tenants",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenants":   tenants,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetTenant retrieves a single tenant by ID (Platform Admin only)
// Route: GET /api/v2/admin/tenants/:id
func GetTenant(c *gin.Context) {
	tenantID := c.Param("id")

	tenant, err := repo.GetTenantByID(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Tenant not found",
		})
		return
	}

	// Get tenant statistics
	userCount, _ := repo.GetTenantUserCount(tenantID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenant":     tenant,
			"user_count": userCount,
		},
	})
}

// CreateTenant creates a new tenant (Platform Admin only)
// Route: POST /api/v2/admin/tenants
func CreateTenant(c *gin.Context) {
	var req struct {
		// IDPOrgID is the upstream OIDC org id. JSON wire key stays zitadel_org_id
		// for back-compat (= physical column; admin clients/tests still post it).
		// TODO(idp-migration): flip wire key to idp_org_id alongside the DB column
		// rename (owner-gated migration).
		IDPOrgID string `json:"zitadel_org_id" binding:"required"`
		Slug     string `json:"slug" binding:"required"`
		Name     string `json:"name" binding:"required"`
		PlanType string `json:"plan_type"`
		MaxUsers int    `json:"max_users"`
		MaxQuota int64  `json:"max_quota"`
		// Aggregate per-minute request / LLM-token caps across all the
		// tenant's tokens (middleware.BusinessRateLimit). 0 = unlimited.
		RateLimitRPM int `json:"rate_limit_rpm"`
		RateLimitTPM int `json:"rate_limit_tpm"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	if err := app.ValidateRateLimits(req.RateLimitRPM, req.RateLimitTPM); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Set defaults
	if req.PlanType == "" {
		req.PlanType = repo.TenantPlanFree
	}
	if req.MaxUsers == 0 {
		req.MaxUsers = 100
	}
	if req.MaxQuota == 0 {
		req.MaxQuota = 1000000
	}

	// Create tenant
	tenant, err := repo.CreateTenantFromIDP(req.IDPOrgID, req.Slug, req.Name)
	if err != nil {
		common.SysError("Failed to create tenant: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create tenant",
			"error":   err.Error(),
		})
		return
	}

	// Update tenant with additional fields
	err = repo.UpdateTenant(tenant.Id, map[string]interface{}{
		"plan_type": req.PlanType,
		"max_users": req.MaxUsers,
		"max_quota": req.MaxQuota,
		"rpm_limit": req.RateLimitRPM,
		"tpm_limit": req.RateLimitTPM,
	})
	if err != nil {
		common.SysError("Failed to update tenant: " + err.Error())
	} else {
		// The response must reflect what was persisted: `tenant` is the
		// pre-update snapshot, so mirror the applied fields before marshal.
		tenant.PlanType = req.PlanType
		tenant.MaxUsers = req.MaxUsers
		tenant.MaxQuota = req.MaxQuota //nolint:staticcheck // SA1019: legacy field stays part of the create API until its Q4 removal (ADR 2026-05-18)
		tenant.RateLimitRPM = req.RateLimitRPM
		tenant.RateLimitTPM = req.RateLimitTPM
	}

	// Initialize default configs for new tenant
	err = repo.InitializeDefaultTenantConfigs(tenant.Id)
	if err != nil {
		common.SysError("Failed to initialize tenant configs: " + err.Error())
	}

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantCreated, governance.ResourceTenant, 0, tenant.Id))

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Tenant created successfully",
		"data":    tenant,
	})
}

// UpdateTenant updates tenant information (Platform Admin only)
// Route: PUT /api/v2/admin/tenants/:id
func UpdateTenant(c *gin.Context) {
	tenantID := c.Param("id")

	var req struct {
		Name     string `json:"name"`
		Status   int    `json:"status"`
		PlanType string `json:"plan_type"`
		MaxUsers int    `json:"max_users"`
		MaxQuota int64  `json:"max_quota"`
		// Pointers, unlike the legacy fields above: an explicit 0 must be
		// distinguishable from "field omitted" so admins can RESET a limit
		// back to unlimited (0) — the non-zero-only map pattern can't.
		RateLimitRPM *int `json:"rate_limit_rpm"`
		RateLimitTPM *int `json:"rate_limit_tpm"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	if req.RateLimitRPM != nil || req.RateLimitTPM != nil {
		rpm, tpm := 0, 0
		if req.RateLimitRPM != nil {
			rpm = *req.RateLimitRPM
		}
		if req.RateLimitTPM != nil {
			tpm = *req.RateLimitTPM
		}
		if err := app.ValidateRateLimits(rpm, tpm); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}

	// Build updates map (only include non-zero fields)
	updates := make(map[string]interface{})
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Status > 0 {
		updates["status"] = req.Status
	}
	if req.PlanType != "" {
		updates["plan_type"] = req.PlanType
	}
	if req.MaxUsers > 0 {
		updates["max_users"] = req.MaxUsers
	}
	if req.MaxQuota > 0 {
		updates["max_quota"] = req.MaxQuota
	}
	if req.RateLimitRPM != nil {
		updates["rpm_limit"] = *req.RateLimitRPM
	}
	if req.RateLimitTPM != nil {
		updates["tpm_limit"] = *req.RateLimitTPM
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "No fields to update",
		})
		return
	}

	// Update tenant
	err := repo.UpdateTenant(tenantID, updates)
	if err != nil {
		common.SysError("Failed to update tenant: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update tenant",
			"error":   err.Error(),
		})
		return
	}

	// Retrieve updated tenant
	tenant, err := repo.GetTenantByID(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Tenant not found after update",
		})
		return
	}

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, tenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Tenant updated successfully",
		"data":    tenant,
	})
}

// DeleteTenant soft deletes a tenant (Platform Admin only)
// Route: DELETE /api/v2/admin/tenants/:id
func DeleteTenant(c *gin.Context) {
	tenantID := c.Param("id")

	// Prevent deletion of default tenant
	if tenantID == "default" {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Cannot delete default tenant",
		})
		return
	}

	// Check if tenant exists
	tenant, err := repo.GetTenantByID(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Tenant not found",
		})
		return
	}

	// Soft delete tenant
	err = repo.DeleteTenant(tenantID)
	if err != nil {
		common.SysError("Failed to delete tenant: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete tenant",
			"error":   err.Error(),
		})
		return
	}

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantDeleted, governance.ResourceTenant, 0, tenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Tenant deleted successfully",
		"data":    tenant,
	})
}

// EnableTenant enables a tenant (Platform Admin only)
// Route: POST /api/v2/admin/tenants/:id/enable
func EnableTenant(c *gin.Context) {
	tenantID := c.Param("id")

	err := repo.EnableTenant(tenantID)
	if err != nil {
		common.SysError("Failed to enable tenant: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to enable tenant",
			"error":   err.Error(),
		})
		return
	}

	// No dedicated "tenant.enabled" action constant exists yet — ActionTenantUpdated
	// is the closest fit (a status-field update); details disambiguates the verb.
	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, "enable: "+tenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Tenant enabled successfully",
	})
}

// DisableTenant disables a tenant (Platform Admin only)
// Route: POST /api/v2/admin/tenants/:id/disable
func DisableTenant(c *gin.Context) {
	tenantID := c.Param("id")

	// Prevent disabling default tenant
	if tenantID == "default" {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Cannot disable default tenant",
		})
		return
	}

	err := repo.DisableTenant(tenantID)
	if err != nil {
		common.SysError("Failed to disable tenant: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to disable tenant",
			"error":   err.Error(),
		})
		return
	}

	// No dedicated "tenant.disabled" action constant exists yet — ActionTenantUpdated
	// is the closest fit (a status-field update); details disambiguates the verb.
	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, "disable: "+tenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Tenant disabled successfully",
	})
}

// SuspendTenant suspends a tenant (Platform Admin only)
// Route: POST /api/v2/admin/tenants/:id/suspend
func SuspendTenant(c *gin.Context) {
	tenantID := c.Param("id")

	// Prevent suspending default tenant
	if tenantID == "default" {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Cannot suspend default tenant",
		})
		return
	}

	err := repo.SuspendTenant(tenantID)
	if err != nil {
		common.SysError("Failed to suspend tenant: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to suspend tenant",
			"error":   err.Error(),
		})
		return
	}

	// No dedicated "tenant.suspended" action constant exists yet — ActionTenantUpdated
	// is the closest fit (a status-field update); details disambiguates the verb.
	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, "suspend: "+tenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Tenant suspended successfully",
	})
}

// GetTenantStats retrieves comprehensive statistics for a tenant (Platform Admin only)
// Route: GET /api/v2/admin/tenants/:id/stats
func GetTenantStats(c *gin.Context) {
	tenantID := c.Param("id")

	// Get comprehensive tenant statistics
	stats, err := repo.GetTenantStats(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Tenant not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// GetTenantConfigs retrieves all configurations for a tenant
// Route: GET /api/v2/:tenant_slug/config
func GetTenantConfigs(c *gin.Context) {
	// Get tenant ID from context
	tenantID, err := repo.GetTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Get all configs (exclude system configs for non-admin users)
	includeSystem := c.Query("include_system") == "true"
	configs, err := repo.ListTenantConfigs(tenantID, includeSystem)
	if err != nil {
		common.SysError("Failed to list tenant configs: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve configurations",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    configs,
	})
}

// UpdateTenantConfig updates a single tenant configuration
// Route: PUT /api/v2/:tenant_slug/config/:key
func UpdateTenantConfig(c *gin.Context) {
	// Get tenant ID from context
	tenantID, err := repo.GetTenantID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	configKey := c.Param("key")

	var req struct {
		Value       string `json:"value" binding:"required"`
		ConfigType  string `json:"config_type"`
		Description string `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Set defaults
	if req.ConfigType == "" {
		req.ConfigType = repo.ConfigTypeString
	}

	// Check if config exists and is not a system config
	existingConfig, err := repo.GetTenantConfig(tenantID, configKey)
	if err == nil && existingConfig.IsSystem {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Cannot modify system configuration",
		})
		return
	}

	// Set config
	err = repo.SetTenantConfig(tenantID, configKey, req.Value, req.ConfigType, req.Description, false)
	if err != nil {
		common.SysError("Failed to set tenant config: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update configuration",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Configuration updated successfully",
	})
}
