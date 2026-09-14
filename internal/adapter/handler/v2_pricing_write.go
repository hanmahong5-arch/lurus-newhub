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
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// contextTierPatch is one rung of a model's declarative context-length
// pricing tier list (billing-pricing-14, ratio_setting.ContextTier) as sent
// by the console.
type contextTierPatch struct {
	ThresholdTokens int      `json:"threshold_tokens"`
	ModelRatio      *float64 `json:"model_ratio,omitempty"`
	CompletionRatio *float64 `json:"completion_ratio,omitempty"`
	CacheRatio      *float64 `json:"cache_ratio,omitempty"`
}

// updatePricingRequest is the per-model patch sent by the console.
// Whitelist: only the five admin-visible ratio/price/tier fields are
// accepted. ContextTiers is a pointer to a slice so an explicit empty array
// ("context_tiers":[]) — which clears the model's tier list — is
// distinguishable from the field being absent (nil, "not touched" by this
// batch): encoding/json only allocates the pointed-to slice when the key is
// present in the request body.
type updatePricingRequest struct {
	ModelName       string              `json:"model_name"`
	ModelRatio      *float64            `json:"model_ratio,omitempty"`
	CompletionRatio *float64            `json:"completion_ratio,omitempty"`
	ModelPrice      *float64            `json:"model_price,omitempty"`
	CacheRatio      *float64            `json:"cache_ratio,omitempty"`
	ContextTiers    *[]contextTierPatch `json:"context_tiers,omitempty"`
}

// contextTierPatchesToTiers converts the wire patch shape to
// ratio_setting.ContextTier (identical field set — the wire type exists
// separately only so this handler package, not ratio_setting, owns the JSON
// tags of the admin-facing request body).
func contextTierPatchesToTiers(patches []contextTierPatch) []ratio_setting.ContextTier {
	out := make([]ratio_setting.ContextTier, len(patches))
	for i, p := range patches {
		out[i] = ratio_setting.ContextTier{
			ThresholdTokens: p.ThresholdTokens,
			ModelRatio:      p.ModelRatio,
			CompletionRatio: p.CompletionRatio,
			CacheRatio:      p.CacheRatio,
		}
	}
	return out
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

// currentPricingVersion reads PricingVersion straight from the database
// (repo.GetOptionValue against repo.DB) rather than the per-process option
// cache. This replica's cache (common.OptionMap) is refreshed by its own
// writes and by the SYNC_FREQUENCY ticker (repo/option.go
// loadOptionsFromDatabase); a version committed by another replica reaches
// this replica's cache no sooner than that ticker, so a cache read here right
// after another replica's commit could answer a stale version and drive the
// console's 409-refetch-retry loop into hitting 409 again until the next
// resync. Defaults to 0 when the row has never been written.
func currentPricingVersion() int64 {
	raw, found, err := repo.GetOptionValue(repo.DB, "PricingVersion")
	if err != nil || !found {
		return 0
	}
	v, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil {
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
		if item.ContextTiers != nil {
			if err := ratio_setting.ValidateContextTierList(contextTierPatchesToTiers(*item.ContextTiers)); err != nil {
				return i, "INVALID_CONTEXT_TIERS", "context_tiers: " + err.Error(), false
			}
			// A tier list can never apply to a per-call priced model —
			// helper.ModelPriceHelper's tier-override branch only runs under
			// !UsePrice (price.go). Reject instead of silently storing and
			// echoing back a config that will never take effect (cycle-8
			// plan §8 L5 B-F6). Explicit [] (clearing) is always allowed,
			// including for a per-call model, since it removes rather than
			// adds an inert config. "Per-call" here means either this same
			// batch item sets model_price, or the model already resolves to
			// a per-call price today (ratio_setting.GetModelPrice).
			if len(*item.ContextTiers) > 0 {
				isPerCall := item.ModelPrice != nil
				if !isPerCall {
					_, isPerCall = ratio_setting.GetModelPrice(item.ModelName, false)
				}
				if isPerCall {
					return i, "INVALID_CONTEXT_TIERS", "context_tiers cannot be set on a per-call priced model (model_price); tiers only apply to ratio-based (model_ratio) pricing", false
				}
			}
		}
	}
	return -1, "", "", true
}

// pricingComputation is the result of applying a batch on top of the base
// copies of the pricing option maps a caller supplied: the candidate maps (for
// persistence), a diff per touched (model, field) pair (for the preview
// response and the audit details), and a touched flag per field so the
// caller persists only the maps the batch actually touched — "touched"
// meaning the batch carried a non-nil value for that field, not that the
// value differs from the map's current entry (a batch that re-submits an
// unchanged value still sets the touched flag and gets persisted).
type pricingComputation struct {
	Diffs                  []map[string]interface{}
	ModelRatioCopy         map[string]float64
	CompletionRatioCopy    map[string]float64
	ModelPriceCopy         map[string]float64
	CacheRatioCopy         map[string]float64
	ContextTiersCopy       map[string][]ratio_setting.ContextTier
	TouchedModelRatio      bool
	TouchedCompletionRatio bool
	TouchedModelPrice      bool
	TouchedCacheRatio      bool
	TouchedContextTiers    bool
	UpdatedCount           int
}

// computePricingDiffsFromMaps applies items on top of the base maps the
// caller supplies and returns the resulting computation. It takes the base
// maps as parameters — rather than reading ratio_setting's live copies
// itself — so UpdatePricingV2 can apply a batch on top of the maps it just
// read from the database inside its transaction (the write's true baseline
// on a multi-replica deployment), while PreviewPricingV2 (which does not
// read the database — see computePricingDiffs below) reuses the same merge
// logic on top of the live process maps instead. The base maps are mutated
// in place and returned as part of the computation; callers must pass
// copies they own.
func computePricingDiffsFromMaps(
	items []updatePricingRequest,
	modelRatioBase, completionRatioBase, modelPriceBase, cacheRatioBase map[string]float64,
	contextTiersBase map[string][]ratio_setting.ContextTier,
) pricingComputation {
	out := pricingComputation{
		ModelRatioCopy:      modelRatioBase,
		CompletionRatioCopy: completionRatioBase,
		ModelPriceCopy:      modelPriceBase,
		CacheRatioCopy:      cacheRatioBase,
		ContextTiersCopy:    contextTiersBase,
	}
	for _, item := range items {
		touched := false
		if item.ModelRatio != nil {
			old, explicit := effectiveOldModelRatio(out.ModelRatioCopy, item.ModelName)
			out.ModelRatioCopy[item.ModelName] = *item.ModelRatio
			out.Diffs = append(out.Diffs, pricingDiffEntry(item.ModelName, "model_ratio", old, *item.ModelRatio, explicit))
			out.TouchedModelRatio = true
			touched = true
		}
		if item.CompletionRatio != nil {
			old, explicit := effectiveOldCompletionRatio(out.CompletionRatioCopy, item.ModelName)
			out.CompletionRatioCopy[item.ModelName] = *item.CompletionRatio
			out.Diffs = append(out.Diffs, pricingDiffEntry(item.ModelName, "completion_ratio", old, *item.CompletionRatio, explicit))
			out.TouchedCompletionRatio = true
			touched = true
		}
		if item.ModelPrice != nil {
			old, explicit := effectiveOldModelPrice(out.ModelPriceCopy, item.ModelName)
			out.ModelPriceCopy[item.ModelName] = *item.ModelPrice
			out.Diffs = append(out.Diffs, pricingDiffEntry(item.ModelName, "model_price", old, *item.ModelPrice, explicit))
			out.TouchedModelPrice = true
			touched = true
		}
		if item.CacheRatio != nil {
			old, explicit := effectiveOldCacheRatio(out.CacheRatioCopy, item.ModelName)
			out.CacheRatioCopy[item.ModelName] = *item.CacheRatio
			out.Diffs = append(out.Diffs, pricingDiffEntry(item.ModelName, "cache_ratio", old, *item.CacheRatio, explicit))
			out.TouchedCacheRatio = true
			touched = true
		}
		if item.ContextTiers != nil {
			oldList := out.ContextTiersCopy[item.ModelName]
			newList := contextTierPatchesToTiers(*item.ContextTiers)
			if len(newList) == 0 {
				// Explicit empty list clears the model's tiers — delete
				// rather than store an empty slice so a marshaled map never
				// carries dead keys and GetContextLengthTier's "no list"
				// branch (len==0 either way) stays the only code path.
				delete(out.ContextTiersCopy, item.ModelName)
			} else {
				out.ContextTiersCopy[item.ModelName] = newList
			}
			out.Diffs = append(out.Diffs, contextTiersDiffEntry(item.ModelName, oldList, newList))
			out.TouchedContextTiers = true
			touched = true
		}
		if touched {
			out.UpdatedCount++
		}
	}
	return out
}

// contextTiersDiffEntry mirrors pricingDiffEntry's shape (model_name, field,
// old, new) for the one non-float field this handler writes: old/new are the
// tier lists themselves (nil/empty when the model had none), not a single
// number, so the preview/audit consumer reads a list under "old"/"new"
// instead of a float for this field. No old_explicit: unlike the four ratio
// maps there is no map-wide fallback default a tier list can fall back to —
// "no entry" and "empty list" are both simply "no tiers".
func contextTiersDiffEntry(modelName string, oldTiers, newTiers []ratio_setting.ContextTier) map[string]interface{} {
	return map[string]interface{}{
		"model_name": modelName,
		"field":      "context_tiers",
		"old":        oldTiers,
		"new":        newTiers,
	}
}

// computePricingDiffs is PreviewPricingV2's entry point: it applies items on
// top of the live process ratio maps (ratio_setting's Get*Copy functions),
// the same maps UpdatePricingV2 falls back to when the database has no
// baseline row yet. Preview does not read the database, so on a replica
// whose memory has not resynced from a recent write on another replica (see
// currentPricingVersion's comment) the diff it shows can lag what a
// concurrent write would actually apply.
func computePricingDiffs(items []updatePricingRequest) pricingComputation {
	return computePricingDiffsFromMaps(
		items,
		ratio_setting.GetModelRatioCopy(),
		ratio_setting.GetCompletionRatioCopy(),
		ratio_setting.GetModelPriceCopy(),
		ratio_setting.GetCacheRatioCopy(),
		ratio_setting.GetContextLengthTiersCopy(),
	)
}

// effectiveOldModelRatio reports the ratio a model was actually billed at
// before this batch: the base map's own entry when the admin (or a prior
// write) had configured one explicitly, otherwise whatever
// ratio_setting.GetModelRatio resolves — a family fallback or, for a model
// it does not know at all, the getter's default (37.5 today, the same number
// repo.GetPricing lists and the relay price helper bills) — so the
// diff/audit trail says "from the fallback" instead of a misleading "from 0"
// for a model nobody has priced explicitly. The getter's "found" flag is
// deliberately ignored here: it is false for the unknown-model default when
// self-use mode is off, but the catalogue and the relay use that default
// regardless. explicit is false whenever the value came from the getter
// rather than the base map.
func effectiveOldModelRatio(base map[string]float64, modelName string) (value float64, explicit bool) {
	if v, ok := base[modelName]; ok {
		return v, true
	}
	v, _, _ := ratio_setting.GetModelRatio(modelName)
	return v, false
}

// effectiveOldCompletionRatio mirrors effectiveOldModelRatio for
// completion_ratio. ratio_setting.GetCompletionRatio has no "found" return
// (it resolves to either a configured entry or a hard-coded fallback, with
// no way to tell the caller which), so explicit is derived from the base
// map lookup alone.
func effectiveOldCompletionRatio(base map[string]float64, modelName string) (value float64, explicit bool) {
	if v, ok := base[modelName]; ok {
		return v, true
	}
	return ratio_setting.GetCompletionRatio(modelName), false
}

// effectiveOldModelPrice mirrors effectiveOldModelRatio for model_price.
// ratio_setting.GetModelPrice has no family-fallback chain of its own; when
// the model has no explicit entry there either, the reported old value is 0.
func effectiveOldModelPrice(base map[string]float64, modelName string) (value float64, explicit bool) {
	if v, ok := base[modelName]; ok {
		return v, true
	}
	if v, found := ratio_setting.GetModelPrice(modelName, false); found {
		return v, false
	}
	return 0, false
}

// effectiveOldCacheRatio mirrors effectiveOldModelRatio for cache_ratio.
// ratio_setting.GetCacheRatio defaults an absent model to 1, matching the
// ratio actually applied at relay time (cache_ratio.go). explicit here means
// "present in the in-memory/DB map", which includes ratio_setting's shipped
// defaultCacheRatio entries seeded at boot (InitRatioSettings) — it does not
// mean an admin configured the value through this handler.
func effectiveOldCacheRatio(base map[string]float64, modelName string) (value float64, explicit bool) {
	if v, ok := base[modelName]; ok {
		return v, true
	}
	v, _ := ratio_setting.GetCacheRatio(modelName)
	return v, false
}

func pricingDiffEntry(modelName, field string, oldValue, newValue float64, oldExplicit bool) map[string]interface{} {
	return map[string]interface{}{
		"model_name":   modelName,
		"field":        field,
		"old":          oldValue,
		"new":          newValue,
		"old_explicit": oldExplicit,
	}
}

// readBaselineRatioMap reads key's option row inside tx with a row-level
// lock (repo.GetOptionValueForUpdate) and unmarshals it into a ratio map.
// When the row does not exist yet (no write has ever persisted this key),
// it returns fallback — a live ratio_setting copy — instead: there is
// nothing in the database to be stale against.
func readBaselineRatioMap(tx *gorm.DB, key string, fallback map[string]float64) (map[string]float64, error) {
	raw, found, err := repo.GetOptionValueForUpdate(tx, key)
	if err != nil {
		return nil, err
	}
	if !found {
		return fallback, nil
	}
	out := make(map[string]float64)
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// readBaselineContextTiersMap mirrors readBaselineRatioMap for the
// ContextLengthTiers option row, whose value is map[string][]ContextTier
// rather than map[string]float64.
func readBaselineContextTiersMap(tx *gorm.DB, fallback map[string][]ratio_setting.ContextTier) (map[string][]ratio_setting.ContextTier, error) {
	raw, found, err := repo.GetOptionValueForUpdate(tx, "ContextLengthTiers")
	if err != nil {
		return nil, err
	}
	if !found {
		return fallback, nil
	}
	out := make(map[string][]ratio_setting.ContextTier)
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
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
// write to the PricingVersion the caller last read. Inside one
// repo.DB.Transaction: the pricing option rows are read with a row-level
// lock (repo.GetOptionValueForUpdate) so the batch is applied on top of the
// database's committed baseline — not this process's possibly-stale
// in-memory copies (TestV2PricingWrite_BatchAppliesOnDBBaseline_NotStaleMemory)
// — then the touched maps plus the PricingVersion compare-and-swap are
// persisted; the in-memory ratio maps (and the option cache) are updated
// after that transaction commits, not before, so a failed persist leaves
// this process still serving the ratio the database holds
// (TestV2PricingWrite_DBFailure_LeavesMemoryUntouched).
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
	// Root, not tenant-admin: the writes below replace the process-wide ratio
	// maps and the single global option row (no tenant_id column on either),
	// so a write made through any one tenant's slug changes that model's
	// price regardless of which tenant's slug the request used.
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
			full = "item[" + strconv.Itoa(idx) + "]: " + msg
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

	var comp pricingComputation
	var modelRatioJSON, completionRatioJSON, modelPriceJSON, cacheRatioJSON, contextTiersJSON string
	var expectedVersion, newVersion int64

	txErr := repo.DB.Transaction(func(tx *gorm.DB) error {
		modelRatioBase, berr := readBaselineRatioMap(tx, "ModelRatio", ratio_setting.GetModelRatioCopy())
		if berr != nil {
			return berr
		}
		completionRatioBase, berr := readBaselineRatioMap(tx, "CompletionRatio", ratio_setting.GetCompletionRatioCopy())
		if berr != nil {
			return berr
		}
		modelPriceBase, berr := readBaselineRatioMap(tx, "ModelPrice", ratio_setting.GetModelPriceCopy())
		if berr != nil {
			return berr
		}
		cacheRatioBase, berr := readBaselineRatioMap(tx, "CacheRatio", ratio_setting.GetCacheRatioCopy())
		if berr != nil {
			return berr
		}
		contextTiersBase, berr := readBaselineContextTiersMap(tx, ratio_setting.GetContextLengthTiersCopy())
		if berr != nil {
			return berr
		}

		comp = computePricingDiffsFromMaps(items, modelRatioBase, completionRatioBase, modelPriceBase, cacheRatioBase, contextTiersBase)

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
		if comp.TouchedContextTiers {
			b, jerr := json.Marshal(comp.ContextTiersCopy)
			if jerr != nil {
				return jerr
			}
			contextTiersJSON = string(b)
			if err := repo.UpdateOptionTx(tx, "ContextLengthTiers", contextTiersJSON); err != nil {
				return err
			}
		}

		if hasHeader {
			// The client's expectation IS the CAS predicate: a stale header
			// must lose against the database's real value, not against a
			// possibly-stale in-memory cache read outside this transaction.
			expectedVersion = headerVersion
		} else {
			// Guard skipped (no header): read the baseline with a row-level
			// lock inside this same transaction rather than trust the outer
			// cache or a plain (non-locking) read. Under READ COMMITTED on
			// PostgreSQL a plain SELECT here could see the pre-commit value
			// of a concurrently mid-transaction writer; this SELECT ... FOR
			// UPDATE instead blocks until that writer commits or rolls back
			// and reads its result, so this write's CAS is evaluated
			// against the latest committed value rather than a
			// possibly-stale one, and — since the guard is off — is not
			// rejected just because a concurrent header-less writer got
			// there first. Not reproducible against SQLite (the hermetic
			// test tier serializes at the whole-database level regardless
			// of this lock) — PLAUSIBLE on PostgreSQL, unverified by a
			// red/green test in this lane.
			raw, _, gerr := repo.GetOptionValueForUpdate(tx, "PricingVersion")
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

	// Commit succeeded: apply the in-memory side effects after the
	// transaction, in the DB-first order this handler's predecessor (which
	// wrote memory before the database) got backwards —
	// TestV2PricingWrite_DBFailure_LeavesMemoryUntouched asserts memory stays
	// on the old value when the commit fails. Because the maps just
	// persisted were built on the database's committed baseline
	// (readBaselineRatioMap above), applying them here also heals this
	// replica's memory for any model another replica priced since this
	// process's last SYNC_FREQUENCY resync, for the fields this batch
	// touched (TestV2PricingWrite_BatchAppliesOnDBBaseline_NotStaleMemory).
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
	if comp.TouchedContextTiers {
		_ = ratio_setting.UpdateContextLengthTiersByJSONString(contextTiersJSON)
	}
	_ = repo.SetOptionMapValue("PricingVersion", strconv.FormatInt(newVersion, 10))

	// Drop the derived caches so the next read rebuilds from the maps just
	// applied: the exposed-ratio cache, and repo.GetPricing's ~1-minute
	// catalogue cache (repo/pricing.go) — without the second call the
	// console's post-save refetch could list pre-write ratios beside the new
	// version for up to a minute
	// (TestVersionedPricingWrite_InvalidatesPricingCache).
	ratio_setting.InvalidateExposedDataCache()
	repo.InvalidatePricingCache()

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
// but does not itself write it. Route: POST /api/v2/:tenant_slug/pricing/preview.
// Same auth chain as UpdatePricingV2 (root-gated for the same reason: the
// underlying maps are process-global). It does not call repo.UpdateOption,
// does not call ratio_setting.InvalidateExposedDataCache, and does not bump
// PricingVersion — TestV2PricingPreview_NeverPersists asserts the options
// row count and the version are unchanged after a call; the maps
// computePricingDiffs returns are local copies this handler simply
// discards.
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
			full = "item[" + strconv.Itoa(idx) + "]: " + msg
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
