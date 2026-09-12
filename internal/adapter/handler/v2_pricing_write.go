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
	"errors"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// updatePricingRequest is the per-model patch sent by the console.
// Whitelist: only the four admin-visible ratio/price fields are accepted.
type updatePricingRequest struct {
	ModelName       string   `json:"model_name"`
	ModelRatio      *float64 `json:"model_ratio,omitempty"`
	CompletionRatio *float64 `json:"completion_ratio,omitempty"`
	ModelPrice      *float64 `json:"model_price,omitempty"`
	CacheRatio      *float64 `json:"cache_ratio,omitempty"`
}

// updatePricingResponse is the view struct returned on success.
// No internal fields escape through this type.
type updatePricingResponse struct {
	UpdatedCount int   `json:"updated_count"`
	NewVersion   int64 `json:"new_version"`
}

// pricingVersionHeader is the optional optimistic-lock header the console
// sends back from its last GET. Absent means the guard is skipped this
// release (rollout compatibility, owner decision O1) — the release after the
// console change ships it becomes mandatory and this comment/the
// no-header test are deleted together.
const pricingVersionHeader = "If-Match-Pricing-Version"

// errPricingVersionConflict signals a lost PricingVersion CAS from inside a
// repo.DB.Transaction closure; the caller translates it to 409
// PRICING_VERSION_CONFLICT after the transaction rolls back.
var errPricingVersionConflict = errors.New("pricing version conflict")

// currentPricingVersion reads PricingVersion from the in-memory option cache
// that every other option getter reads (common.OptionMap), defaulting to 0
// when the key has never been written — GET pricing's documented default for
// a brand-new deployment.
func currentPricingVersion() int64 {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap["PricingVersion"]
	common.OptionMapRWMutex.RUnlock()
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// validatePricingBatch validates the whole array before any handler applies
// or previews a mutation, so UpdatePricingV2 and the read-only preview route
// reject the same bad input the same way. index is -1 for the "empty batch"
// case (no single item to blame).
func validatePricingBatch(items []updatePricingRequest) (index int, errCode, msg string, ok bool) {
	if len(items) == 0 {
		return -1, "EMPTY_BATCH", "request body must contain at least one item", false
	}
	for i, item := range items {
		if item.ModelName == "" {
			return i, "MISSING_MODEL_NAME", "model_name is required", false
		}
		if item.ModelRatio != nil && *item.ModelRatio <= 0 {
			return i, "INVALID_RATIO", "model_ratio must be > 0", false
		}
		if item.CompletionRatio != nil && *item.CompletionRatio <= 0 {
			return i, "INVALID_RATIO", "completion_ratio must be > 0", false
		}
		if item.ModelPrice != nil && *item.ModelPrice <= 0 {
			return i, "INVALID_RATIO", "model_price must be > 0", false
		}
		if item.CacheRatio != nil && *item.CacheRatio <= 0 {
			return i, "INVALID_RATIO", "cache_ratio must be > 0", false
		}
	}
	return -1, "", "", true
}

// pricingComputation is the result of applying a batch on top of copies of
// the four live ratio maps: the candidate maps (for persistence), a diff per
// touched (model, field) pair (for the preview response and the audit
// details), and a touched flag per field (so the caller persists only the
// maps the batch actually changed, matching the original
// persistRatioMapIfChanged behaviour).
type pricingComputation struct {
	Diffs                  []map[string]interface{}
	ModelRatioCopy         map[string]float64
	CompletionRatioCopy    map[string]float64
	ModelPriceCopy         map[string]float64
	CacheRatioCopy         map[string]float64
	TouchedModelRatio      bool
	TouchedCompletionRatio bool
	TouchedModelPrice      bool
	TouchedCacheRatio      bool
	UpdatedCount           int
}

// computePricingDiffs is the single computation UpdatePricingV2 and
// PreviewPricingV2 both call: preview provably shows exactly what write
// would do because they share this function rather than two hand-maintained
// copies of the same loop.
func computePricingDiffs(items []updatePricingRequest) pricingComputation {
	out := pricingComputation{
		ModelRatioCopy:      ratio_setting.GetModelRatioCopy(),
		CompletionRatioCopy: ratio_setting.GetCompletionRatioCopy(),
		ModelPriceCopy:      ratio_setting.GetModelPriceCopy(),
		CacheRatioCopy:      ratio_setting.GetCacheRatioCopy(),
	}
	for _, item := range items {
		touched := false
		if item.ModelRatio != nil {
			old := out.ModelRatioCopy[item.ModelName]
			out.ModelRatioCopy[item.ModelName] = *item.ModelRatio
			out.Diffs = append(out.Diffs, pricingDiffEntry(item.ModelName, "model_ratio", old, *item.ModelRatio))
			out.TouchedModelRatio = true
			touched = true
		}
		if item.CompletionRatio != nil {
			old := out.CompletionRatioCopy[item.ModelName]
			out.CompletionRatioCopy[item.ModelName] = *item.CompletionRatio
			out.Diffs = append(out.Diffs, pricingDiffEntry(item.ModelName, "completion_ratio", old, *item.CompletionRatio))
			out.TouchedCompletionRatio = true
			touched = true
		}
		if item.ModelPrice != nil {
			old := out.ModelPriceCopy[item.ModelName]
			out.ModelPriceCopy[item.ModelName] = *item.ModelPrice
			out.Diffs = append(out.Diffs, pricingDiffEntry(item.ModelName, "model_price", old, *item.ModelPrice))
			out.TouchedModelPrice = true
			touched = true
		}
		if item.CacheRatio != nil {
			old := out.CacheRatioCopy[item.ModelName]
			out.CacheRatioCopy[item.ModelName] = *item.CacheRatio
			out.Diffs = append(out.Diffs, pricingDiffEntry(item.ModelName, "cache_ratio", old, *item.CacheRatio))
			out.TouchedCacheRatio = true
			touched = true
		}
		if touched {
			out.UpdatedCount++
		}
	}
	return out
}

func pricingDiffEntry(modelName, field string, oldValue, newValue float64) map[string]interface{} {
	return map[string]interface{}{
		"model_name": modelName,
		"field":      field,
		"old":        oldValue,
		"new":        newValue,
	}
}

// UpdatePricingV2 handles bulk pricing patches for the model catalogue.
// Route: POST /api/v2/:tenant_slug/pricing
// Auth: UserAuth middleware + platform root (requirePlatformRoot) — the write is
// global, the tenant slug only selects the route.
//
// Request body: JSON array of updatePricingRequest.
// At least one item is required; model_name is mandatory per item; ratios must be > 0.
//
// Optimistic lock: the optional If-Match-Pricing-Version header pins the
// write to the PricingVersion the caller last read. The four ratio-map
// persists plus the PricingVersion compare-and-swap commit inside one
// repo.DB.Transaction; the in-memory ratio maps (and the option cache) are
// only updated after that transaction commits, so a failed persist never
// leaves this process serving a ratio the database does not hold.
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

	if idx, code, msg, ok := validatePricingBatch(items); !ok {
		full := msg
		if idx >= 0 {
			full = "item[" + itoa(idx) + "]: " + msg
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    full,
			"error_code": code,
		})
		return
	}

	var headerVersion int64
	hasHeader := false
	if raw := c.GetHeader(pricingVersionHeader); raw != "" {
		v, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "invalid " + pricingVersionHeader + " header",
				"error_code": "INVALID_VERSION_HEADER",
			})
			return
		}
		headerVersion = v
		hasHeader = true
	}

	comp := computePricingDiffs(items)

	var modelRatioJSON, completionRatioJSON, modelPriceJSON, cacheRatioJSON string
	var expectedVersion, newVersion int64

	txErr := repo.DB.Transaction(func(tx *gorm.DB) error {
		if comp.TouchedModelRatio {
			b, jerr := json.Marshal(comp.ModelRatioCopy)
			if jerr != nil {
				return jerr
			}
			modelRatioJSON = string(b)
			if err := repo.UpdateOptionTx(tx, "ModelRatio", modelRatioJSON); err != nil {
				return err
			}
		}
		if comp.TouchedCompletionRatio {
			b, jerr := json.Marshal(comp.CompletionRatioCopy)
			if jerr != nil {
				return jerr
			}
			completionRatioJSON = string(b)
			if err := repo.UpdateOptionTx(tx, "CompletionRatio", completionRatioJSON); err != nil {
				return err
			}
		}
		if comp.TouchedModelPrice {
			b, jerr := json.Marshal(comp.ModelPriceCopy)
			if jerr != nil {
				return jerr
			}
			modelPriceJSON = string(b)
			if err := repo.UpdateOptionTx(tx, "ModelPrice", modelPriceJSON); err != nil {
				return err
			}
		}
		if comp.TouchedCacheRatio {
			b, jerr := json.Marshal(comp.CacheRatioCopy)
			if jerr != nil {
				return jerr
			}
			cacheRatioJSON = string(b)
			if err := repo.UpdateOptionTx(tx, "CacheRatio", cacheRatioJSON); err != nil {
				return err
			}
		}

		if hasHeader {
			// The client's expectation IS the CAS predicate: a stale header
			// must lose against the database's real value, not against a
			// possibly-stale in-memory cache read outside this transaction.
			expectedVersion = headerVersion
		} else {
			// Guard skipped (no header): read the baseline inside this same
			// transaction rather than trust the outer cache, then always
			// win the CAS against that freshly-read value — the write must
			// not be rejected just because the rollout guard is off.
			raw, _, gerr := repo.GetOptionValue(tx, "PricingVersion")
			if gerr != nil {
				return gerr
			}
			parsed, perr := strconv.ParseInt(raw, 10, 64)
			if perr != nil {
				parsed = 0
			}
			expectedVersion = parsed
		}
		newVersion = expectedVersion + 1

		won, casErr := repo.CASPricingVersionTx(tx, expectedVersion, newVersion)
		if casErr != nil {
			return casErr
		}
		if !won {
			return errPricingVersionConflict
		}
		return nil
	})

	if txErr != nil {
		if errors.Is(txErr, errPricingVersionConflict) {
			currentRaw, _, _ := repo.GetOptionValue(repo.DB, "PricingVersion")
			current, _ := strconv.ParseInt(currentRaw, 10, 64)
			c.JSON(http.StatusConflict, gin.H{
				"success":         false,
				"message":         "pricing has changed since you last loaded it",
				"error_code":      "PRICING_VERSION_CONFLICT",
				"current_version": current,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to persist pricing: " + txErr.Error(),
			"error_code": "PERSIST_FAILED",
		})
		return
	}

	// Commit succeeded: only now apply the in-memory side effects, so a
	// reader in this process never observes a ratio the database does not
	// (yet) hold — the DB-first ordering this replaces the old
	// memory-first persistRatioMapIfChanged with.
	if comp.TouchedModelRatio {
		_ = ratio_setting.UpdateModelRatioByJSONString(modelRatioJSON)
	}
	if comp.TouchedCompletionRatio {
		_ = ratio_setting.UpdateCompletionRatioByJSONString(completionRatioJSON)
	}
	if comp.TouchedModelPrice {
		_ = ratio_setting.UpdateModelPriceByJSONString(modelPriceJSON)
	}
	if comp.TouchedCacheRatio {
		_ = ratio_setting.UpdateCacheRatioByJSONString(cacheRatioJSON)
	}
	_ = repo.SetOptionMapValue("PricingVersion", strconv.FormatInt(newVersion, 10))

	// Force pricing cache refresh on next read.
	ratio_setting.InvalidateExposedDataCache()

	details, _ := json.Marshal(gin.H{
		"diffs":        comp.Diffs,
		"from_version": expectedVersion,
		"to_version":   newVersion,
	})
	governance.RecordAuditEvent(governance.NewAuditEvent(
		c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionPricingUpdated, governance.ResourcePricing, 0, string(details),
	))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": updatePricingResponse{
			UpdatedCount: comp.UpdatedCount,
			NewVersion:   newVersion,
		},
	})
}

// PreviewPricingV2 shows the diff a POST with the same body would apply,
// without ever writing it. Route: POST /api/v2/:tenant_slug/pricing/preview.
// Same auth chain as UpdatePricingV2 (root-gated for the same reason: the
// underlying maps are process-global). It never calls repo.UpdateOption,
// never calls ratio_setting.InvalidateExposedDataCache, and never bumps
// PricingVersion — computePricingDiffs' returned maps are local copies the
// caller here simply discards.
func PreviewPricingV2(c *gin.Context) {
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

	if idx, code, msg, ok := validatePricingBatch(items); !ok {
		full := msg
		if idx >= 0 {
			full = "item[" + itoa(idx) + "]: " + msg
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    full,
			"error_code": code,
		})
		return
	}

	comp := computePricingDiffs(items)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"version":       currentPricingVersion(),
			"diffs":         comp.Diffs,
			"updated_count": comp.UpdatedCount,
		},
	})
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
