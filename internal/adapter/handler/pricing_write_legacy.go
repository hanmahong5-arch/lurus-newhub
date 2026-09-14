package handler

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// pricingOptionKeys are the option rows the versioned pricing write
// (UpdatePricingV2) guards with the PricingVersion compare-and-swap. The
// legacy root routes that can rewrite the same rows whole — PUT /api/option/
// (handler.UpdateOption, also reached through PUT /api/v2/admin/options) and
// POST /api/option/rest_model_ratio (ResetModelRatio) — route these keys
// through writePricingOptionVersioned so a legacy write cannot change a ratio
// map without bumping the version and leaving a pricing.updated audit row.
var pricingOptionKeys = map[string]bool{
	"ModelRatio":      true,
	"CompletionRatio": true,
	"ModelPrice":      true,
	"CacheRatio":      true,
}

// writePricingOptionVersioned replaces one pricing option row (the whole
// map, which is what the legacy routes send) inside the same kind of
// transaction UpdatePricingV2 uses: the PricingVersion row is read with a
// row-level lock, the option row is saved, and the version is advanced by
// compare-and-swap; only after commit are this process's ratio maps and
// option cache refreshed and the pricing caches invalidated. source names the
// legacy entry point in the audit row's details so a reviewer can tell these
// writes apart from console batches.
//
// The value is validated as a JSON object of numbers before anything is
// persisted; a malformed map is rejected without touching the database,
// which is stricter than repo.UpdateOption (that persisted first and
// reported the in-memory apply failure afterwards).
func writePricingOptionVersioned(c *gin.Context, key, value, source string) error {
	if !pricingOptionKeys[key] {
		return fmt.Errorf("%s is not a pricing option", key)
	}
	var probe map[string]float64
	if err := json.Unmarshal([]byte(value), &probe); err != nil {
		return fmt.Errorf("%s must be a JSON object of numbers: %w", key, err)
	}

	var expected, next int64
	txErr := repo.DB.Transaction(func(tx *gorm.DB) error {
		raw, _, gerr := repo.GetOptionValueForUpdate(tx, "PricingVersion")
		if gerr != nil {
			return gerr
		}
		parsed, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil {
			parsed = 0
		}
		expected = parsed
		next = expected + 1
		if err := repo.UpdateOptionTx(tx, key, value); err != nil {
			return err
		}
		won, casErr := repo.CASPricingVersionTx(tx, expected, next)
		if casErr != nil {
			return casErr
		}
		if !won {
			return errPricingVersionConflict
		}
		return nil
	})
	if txErr != nil {
		return txErr
	}

	// Commit succeeded: refresh this process (SetOptionMapValue dispatches the
	// key to the matching ratio_setting.Update*ByJSONString, the same path a
	// plain repo.UpdateOption takes after its own write) and drop the derived
	// caches so the next GET reflects the new map.
	if err := repo.SetOptionMapValue(key, value); err != nil {
		return err
	}
	_ = repo.SetOptionMapValue("PricingVersion", strconv.FormatInt(next, 10))
	ratio_setting.InvalidateExposedDataCache()
	repo.InvalidatePricingCache()

	details, _ := json.Marshal(gin.H{
		"source":       source,
		"key":          key,
		"from_version": expected,
		"to_version":   next,
	})
	governance.RecordAuditEvent(governance.NewAuditEvent(
		c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionPricingUpdated, governance.ResourcePricing, 0, string(details),
	))
	return nil
}
