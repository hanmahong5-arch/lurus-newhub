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

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// updatePricingRequest is the per-model patch sent by the console.
// Whitelist: only the four admin-visible ratio/price fields are accepted.
type updatePricingRequest struct {
	ModelName       string   `json:"model_name"`
	ModelRatio      *float64 `json:"model_ratio,omitempty"`
	CompletionRatio *float64 `json:"completion_ratio,omitempty"`
	ModelPrice      *float64 `json:"model_price,omitempty"`
}

// updatePricingResponse is the view struct returned on success.
// No internal fields escape through this type.
type updatePricingResponse struct {
	UpdatedCount int `json:"updated_count"`
}

// UpdatePricingV2 handles bulk pricing patches for the model catalogue.
// Route: POST /api/v2/:tenant_slug/pricing
// Auth: UserAuth middleware + platform root (requirePlatformRoot) — the write is
// global, the tenant slug only selects the route.
//
// Request body: JSON array of updatePricingRequest.
// At least one item is required; model_name is mandatory per item; ratios must be > 0.
func UpdatePricingV2(c *gin.Context) {
	slug := c.Param("tenant_slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug required",
			"error_code": "INVALID_TENANT_SLUG",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	tenantCtx, ctxErr := middleware.GetTenantContext(c)
	if ctxErr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	// Root, not tenant-admin: the writes below replace the process-wide ratio maps
	// and the single global option row, so they reprice every tenant at once.
	if !requirePlatformRoot(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Root role required"})
		return
	}

	var items []updatePricingRequest
	if err := c.ShouldBindJSON(&items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "invalid request body: " + err.Error(),
			"error_code": "INVALID_BODY",
		})
		return
	}

	if len(items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "request body must contain at least one item",
			"error_code": "EMPTY_BATCH",
		})
		return
	}

	// Validate all items before applying any mutation (fail-fast, all-or-nothing).
	for i, item := range items {
		if item.ModelName == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "item[" + itoa(i) + "]: model_name is required",
				"error_code": "MISSING_MODEL_NAME",
			})
			return
		}
		if item.ModelRatio != nil && *item.ModelRatio <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "item[" + itoa(i) + "]: model_ratio must be > 0",
				"error_code": "INVALID_RATIO",
			})
			return
		}
		if item.CompletionRatio != nil && *item.CompletionRatio <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "item[" + itoa(i) + "]: completion_ratio must be > 0",
				"error_code": "INVALID_RATIO",
			})
			return
		}
		if item.ModelPrice != nil && *item.ModelPrice <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "item[" + itoa(i) + "]: model_price must be > 0",
				"error_code": "INVALID_RATIO",
			})
			return
		}
	}

	updated := 0

	// Apply changes: mutate in-memory ratio maps then persist via repo.UpdateOption.
	modelRatioCopy := ratio_setting.GetModelRatioCopy()
	completionRatioCopy := ratio_setting.GetCompletionRatioCopy()
	modelPriceCopy := ratio_setting.GetModelPriceCopy()

	for _, item := range items {
		touched := false
		if item.ModelRatio != nil {
			modelRatioCopy[item.ModelName] = *item.ModelRatio
			touched = true
		}
		if item.CompletionRatio != nil {
			completionRatioCopy[item.ModelName] = *item.CompletionRatio
			touched = true
		}
		if item.ModelPrice != nil {
			modelPriceCopy[item.ModelName] = *item.ModelPrice
			touched = true
		}
		if touched {
			updated++
		}
	}

	// Persist each map that changed.
	if err := persistRatioMapIfChanged(items, "ModelRatio", modelRatioCopy, ratio_setting.UpdateModelRatioByJSONString); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to persist model_ratio: " + err.Error(),
			"error_code": "PERSIST_FAILED",
		})
		return
	}
	if err := persistRatioMapIfChanged(items, "CompletionRatio", completionRatioCopy, ratio_setting.UpdateCompletionRatioByJSONString); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to persist completion_ratio: " + err.Error(),
			"error_code": "PERSIST_FAILED",
		})
		return
	}
	if err := persistRatioMapIfChanged(items, "ModelPrice", modelPriceCopy, ratio_setting.UpdateModelPriceByJSONString); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to persist model_price: " + err.Error(),
			"error_code": "PERSIST_FAILED",
		})
		return
	}

	// Force pricing cache refresh on next read.
	ratio_setting.InvalidateExposedDataCache()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": updatePricingResponse{
			UpdatedCount: updated,
		},
	})
}

// persistRatioMapIfChanged serialises the map and saves it via repo.UpdateOption
// if any item in the batch touched this field.
func persistRatioMapIfChanged(
	items []updatePricingRequest,
	optionKey string,
	ratioMap map[string]float64,
	updateFn func(string) error,
) error {
	// Check whether this field was touched.
	touched := false
	for _, item := range items {
		switch optionKey {
		case "ModelRatio":
			if item.ModelRatio != nil {
				touched = true
			}
		case "CompletionRatio":
			if item.CompletionRatio != nil {
				touched = true
			}
		case "ModelPrice":
			if item.ModelPrice != nil {
				touched = true
			}
		}
		if touched {
			break
		}
	}
	if !touched {
		return nil
	}

	jsonBytes, err := json.Marshal(ratioMap)
	if err != nil {
		return err
	}
	jsonStr := string(jsonBytes)
	// Update in-memory first.
	if err := updateFn(jsonStr); err != nil {
		return err
	}
	// Persist to DB (best-effort; if this fails the in-memory state is still updated).
	return repo.UpdateOption(optionKey, jsonStr)
}

// itoa converts a non-negative integer to its decimal string representation.
// stdlib strconv.Itoa is identical; this avoids an import for a single callsite.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
