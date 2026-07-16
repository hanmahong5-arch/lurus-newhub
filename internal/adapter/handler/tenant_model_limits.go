package handler

import (
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Per-model rate-limit admin (Platform Admin only) — CRUD over
// entity.ModelRateLimit rows (migration 026), the config surface for
// middleware.BusinessModelRateLimit. Matching is by exact model name.

// requireModelLimitTenant validates the :id tenant for the model-limit
// endpoints. The bootstrap "default" tenant is allowed WITHOUT a tenants row
// — it predates the tenants table on some deployments (same carve-out as the
// relay middleware's tenant handling) and single-tenant installs are exactly
// where per-model caps get set first. Returns false after writing the 404.
func requireModelLimitTenant(c *gin.Context, tenantID string) bool {
	if tenantID == "default" {
		return true
	}
	if _, err := repo.GetTenantByID(tenantID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Tenant not found",
		})
		return false
	}
	return true
}

// ListTenantModelLimits lists a tenant's per-model rate limits.
// Route: GET /api/v2/admin/tenants/:id/model-limits
func ListTenantModelLimits(c *gin.Context) {
	tenantID := c.Param("id")
	if !requireModelLimitTenant(c, tenantID) {
		return
	}

	rows, err := repo.ListModelRateLimits(tenantID)
	if err != nil {
		common.SysError("Failed to list model rate limits: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve model rate limits",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    rows,
	})
}

// UpsertTenantModelLimit creates or updates one (tenant, model) limit.
// Route: PUT /api/v2/admin/tenants/:id/model-limits
func UpsertTenantModelLimit(c *gin.Context) {
	tenantID := c.Param("id")

	var req struct {
		Model string `json:"model" binding:"required"`
		// Per-minute caps for this model within the tenant
		// (middleware.BusinessModelRateLimit). 0 = unlimited.
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

	model := strings.TrimSpace(req.Model)
	if model == "" || len(model) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "model 名称无效（须为 1–255 字符的精确模型名）",
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
	if !requireModelLimitTenant(c, tenantID) {
		return
	}

	row, err := repo.UpsertModelRateLimit(tenantID, model, req.RateLimitRPM, req.RateLimitTPM)
	if err != nil {
		common.SysError("Failed to upsert model rate limit: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to save model rate limit",
		})
		return
	}

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, "model-limit set: "+tenantID+"/"+model))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Model rate limit saved",
		"data":    row,
	})
}

// DeleteTenantModelLimit removes one (tenant, model) limit. The model comes
// from ?model= because model names contain '/' (vendor/model), which a path
// parameter cannot carry.
// Route: DELETE /api/v2/admin/tenants/:id/model-limits?model=<name>
func DeleteTenantModelLimit(c *gin.Context) {
	tenantID := c.Param("id")
	model := strings.TrimSpace(c.Query("model"))
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "missing required query parameter: model",
		})
		return
	}
	if !requireModelLimitTenant(c, tenantID) {
		return
	}

	deleted, err := repo.DeleteModelRateLimit(tenantID, model)
	if err != nil {
		common.SysError("Failed to delete model rate limit: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete model rate limit",
		})
		return
	}
	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Model rate limit not found",
		})
		return
	}

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, "model-limit deleted: "+tenantID+"/"+model))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Model rate limit deleted",
	})
}
