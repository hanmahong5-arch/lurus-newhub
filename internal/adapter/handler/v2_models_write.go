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
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// Billing type of a catalogue model. It is not stored on the model row: it is
// derived by repo.GetPricing from whether the model has a per-call price entry
// (price present => 1, otherwise the token ratio applies => 0).
const (
	createModelQuotaTypePerToken = 0
	createModelQuotaTypePerCall  = 1
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

	// Pricing validation — reject anything that cannot be honoured instead of
	// accepting it and dropping it on the floor (the caller would then believe
	// the model bills at the submitted rate while it falls back to the global
	// default). Zero means "not supplied".
	if req.ModelRatio < 0 || req.CompletionRatio < 0 || req.ModelPrice < 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "model_ratio, completion_ratio and model_price must be > 0",
			"error_code": "INVALID_RATIO",
		})
		return
	}
	if req.QuotaType != createModelQuotaTypePerToken && req.QuotaType != createModelQuotaTypePerCall {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "quota_type must be 0 (per token) or 1 (per call)",
			"error_code": "INVALID_QUOTA_TYPE",
		})
		return
	}
	if req.QuotaType == createModelQuotaTypePerCall && req.ModelPrice <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "model_price is required when quota_type is 1 (per call)",
			"error_code": "MISSING_MODEL_PRICE",
		})
		return
	}
	// enable_groups is derived from the channels that serve the model, it has no
	// write path here — accepting it silently would be a lie.
	if len(req.EnableGroups) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "enable_groups cannot be set on model creation; it is derived from the channels serving the model",
			"error_code": "ENABLE_GROUPS_UNSUPPORTED",
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

	// Persist the submitted pricing. The model row has no ratio/price columns —
	// pricing lives in the ratio settings, keyed by model name.
	if err := persistCreatedModelPricing(&req); err != nil {
		// Roll back the catalogue row so the caller can retry the whole create
		// instead of being left with a model billing at the global default.
		_ = repo.ModelDelete(m)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to persist model pricing: " + err.Error(),
			"error_code": "PRICING_PERSIST_FAILED",
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

// persistCreatedModelPricing stores the pricing submitted alongside a model
// creation. entity.Model has no ratio/price columns — pricing lives in the
// ratio settings keyed by model name. Fields left at zero keep the platform
// default (family markup / self-use fallback).
func persistCreatedModelPricing(req *createModelV2Req) error {
	if req.ModelRatio > 0 {
		ratios := ratio_setting.GetModelRatioCopy()
		ratios[req.ModelName] = req.ModelRatio
		if err := saveCreatedModelRatioOption("ModelRatio", ratios); err != nil {
			return err
		}
	}
	if req.CompletionRatio > 0 {
		ratios := ratio_setting.GetCompletionRatioCopy()
		ratios[req.ModelName] = req.CompletionRatio
		if err := saveCreatedModelRatioOption("CompletionRatio", ratios); err != nil {
			return err
		}
	}
	if req.ModelPrice > 0 {
		prices := ratio_setting.GetModelPriceCopy()
		prices[req.ModelName] = req.ModelPrice
		if err := saveCreatedModelRatioOption("ModelPrice", prices); err != nil {
			return err
		}
	}
	return nil
}

// saveCreatedModelRatioOption persists one ratio map. repo.UpdateOption writes
// the option row and refreshes the matching in-memory ratio map.
func saveCreatedModelRatioOption(optionKey string, ratioMap map[string]float64) error {
	jsonBytes, err := json.Marshal(ratioMap)
	if err != nil {
		return err
	}
	return repo.UpdateOption(optionKey, string(jsonBytes))
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
