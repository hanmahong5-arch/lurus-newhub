/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package handler

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// createModelV2Req is the whitelist input struct for POST /api/v2/:tenant_slug/models.
// Fields not listed here are deliberately ignored — callers cannot set internal
// enrichment fields (VendorID, CreatedTime, DeletedAt) directly via the API.
type createModelV2Req struct {
	ModelName       string   `json:"model_name" binding:"required"`
	Vendor          string   `json:"vendor"`
	QuotaType       int      `json:"quota_type"`
	ModelRatio      float64  `json:"model_ratio"`
	CompletionRatio float64  `json:"completion_ratio"`
	ModelPrice      float64  `json:"model_price"`
	EnableGroups    []string `json:"enable_groups"`
	Description     string   `json:"description"`
}

// CreateModelV2 handles POST /api/v2/:tenant_slug/models.
// Requires admin role — same level as channel management.
func CreateModelV2(c *gin.Context) {
	slug := c.Param("tenant_slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug required",
			"error_code": "INVALID_TENANT_SLUG",
		})
		return
	}

	if _, err := repo.GetTenantBySlug(slug); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	tenantCtx, ctxErr := middleware.GetTenantContext(c)
	if ctxErr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return
	}

	var req createModelV2Req
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "invalid request: " + err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}

	// Duplicate name check — model names are globally unique (not per-tenant).
	if dup, err := repo.IsModelNameDuplicated(0, req.ModelName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to check model name: " + err.Error(),
		})
		return
	} else if dup {
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"message":    "model name already exists: " + req.ModelName,
			"error_code": "MODEL_NAME_CONFLICT",
		})
		return
	}

	// Resolve vendor ID from name if provided; create the vendor record if it
	// doesn't exist yet so the caller doesn't have to pre-register vendors.
	var vendorID int
	if req.Vendor != "" {
		if id, err := repo.GetOrCreateVendorByName(req.Vendor); err == nil {
			vendorID = id
		}
	}

	m := &repo.Model{
		ModelName:   req.ModelName,
		Description: req.Description,
		VendorID:    vendorID,
		Status:      1, // 1 = enabled (gorm default; no separate ModelStatusEnabled constant)
	}
	if err := repo.ModelInsert(m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to create model: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": modelV2View{
			ID:          m.Id,
			ModelName:   m.ModelName,
			Vendor:      req.Vendor,
			Status:      m.Status,
			CreatedTime: m.CreatedTime,
		},
	})
}

// DeleteModelV2 handles DELETE /api/v2/:tenant_slug/models/:id.
// Soft-deletes via GORM DeletedAt. Tenant is validated but models are global
// catalog entries — only admins can delete from the catalog.
func DeleteModelV2(c *gin.Context) {
	slug := c.Param("tenant_slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug required",
			"error_code": "INVALID_TENANT_SLUG",
		})
		return
	}

	if _, err := repo.GetTenantBySlug(slug); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	tenantCtx, ctxErr := middleware.GetTenantContext(c)
	if ctxErr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "invalid model id",
			"error_code": "INVALID_MODEL_ID",
		})
		return
	}

	var m repo.Model
	if err := repo.DB.First(&m, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "model not found",
			"error_code": "MODEL_NOT_FOUND",
		})
		return
	}

	if err := repo.ModelDelete(&m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to delete model: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "model deleted",
	})
}
