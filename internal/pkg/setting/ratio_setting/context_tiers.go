package ratio_setting

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ContextTier is one declarative context-length pricing rung
// (billing-pricing-14): when a call's prompt token count is >=
// ThresholdTokens, the non-nil ratio fields here override the model's flat
// ratio for that call; a nil field falls through to the model's normal
// (non-tiered) ratio. This is a declarative substitute for upstream's
// billing-expression engine — newhub's need (per-vendor >N-token context
// surcharges) does not require an interpreter.
type ContextTier struct {
	ThresholdTokens int      `json:"threshold_tokens"`
	ModelRatio      *float64 `json:"model_ratio,omitempty"`
	CompletionRatio *float64 `json:"completion_ratio,omitempty"`
	CacheRatio      *float64 `json:"cache_ratio,omitempty"`
}

// maxContextTiersPerModel bounds one model's tier list (spec: <=8 rungs).
const maxContextTiersPerModel = 8

// defaultContextTiers ships empty: real vendor thresholds/ratios need
// finance sign-off (owner item O6, cycle-8 plan). The mechanism is inert
// until an admin writes a tier list for a model.
var defaultContextTiers = map[string][]ContextTier{}

var (
	contextTiersMap      map[string][]ContextTier
	contextTiersMapMutex sync.RWMutex
)

// GetContextLengthTier returns the highest-threshold tier whose
// ThresholdTokens <= promptTokens for model (ThresholdTokens == promptTokens
// qualifies — the boundary is inclusive), or nil when model has no tier
// list, an empty one, or no tier qualifies.
func GetContextLengthTier(model string, promptTokens int) *ContextTier {
	contextTiersMapMutex.RLock()
	defer contextTiersMapMutex.RUnlock()
	tiers, ok := contextTiersMap[model]
	if !ok || len(tiers) == 0 {
		return nil
	}
	var best *ContextTier
	for i := range tiers {
		t := tiers[i]
		if t.ThresholdTokens > promptTokens {
			continue
		}
		if best == nil || t.ThresholdTokens > best.ThresholdTokens {
			tCopy := t
			best = &tCopy
		}
	}
	return best
}

// HasContextLengthTiers reports whether model has a non-empty tier list
// configured. Settlement (helper.ResettleContextTier) uses this to
// short-circuit for every model the mechanism has not touched, so a model
// with no tiers configured is never re-derived from the live ratio maps at
// settlement time — only a model an admin actually gave a tier list can see
// its settled ratio move between pre-consume and settlement.
func HasContextLengthTiers(model string) bool {
	contextTiersMapMutex.RLock()
	defer contextTiersMapMutex.RUnlock()
	return len(contextTiersMap[model]) > 0
}

// GetContextLengthTiersCopy returns a defensive copy of the whole map, read
// the same way the other four ratio maps expose their live copy
// (ratio_setting.Get*Copy) — used by the versioned pricing write to read a
// baseline and by PreviewPricingV2 to diff against the live process state.
func GetContextLengthTiersCopy() map[string][]ContextTier {
	contextTiersMapMutex.RLock()
	defer contextTiersMapMutex.RUnlock()
	out := make(map[string][]ContextTier, len(contextTiersMap))
	for k, v := range contextTiersMap {
		cp := make([]ContextTier, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// ContextLengthTiers2JSONString serialises the live map, matching the
// Ratio2JSONString convention of the other maps in this package.
func ContextLengthTiers2JSONString() string {
	contextTiersMapMutex.RLock()
	defer contextTiersMapMutex.RUnlock()
	b, err := json.Marshal(contextTiersMap)
	if err != nil {
		common.SysLog("error marshalling context length tiers: " + err.Error())
	}
	return string(b)
}

// ValidateContextTierList enforces the tier-list invariants for one model's
// list: thresholds strictly ascending and >=0, any present ratio >0, at most
// maxContextTiersPerModel entries. An explicit empty list is valid — that is
// how an admin clears a model's tiers. Exported so the admin pricing write
// (handler.validatePricingBatch, internal/adapter/handler/v2_pricing_write.go)
// can validate a single model's patch the same way the option-row loader
// does, without round-tripping through JSON.
func ValidateContextTierList(list []ContextTier) error {
	if len(list) > maxContextTiersPerModel {
		return fmt.Errorf("at most %d context tiers allowed, got %d", maxContextTiersPerModel, len(list))
	}
	prevThreshold := -1
	for i, t := range list {
		if t.ThresholdTokens < 0 {
			return fmt.Errorf("tier[%d] threshold_tokens must be >= 0", i)
		}
		if t.ThresholdTokens <= prevThreshold {
			return fmt.Errorf("tier[%d] threshold_tokens must be strictly ascending", i)
		}
		prevThreshold = t.ThresholdTokens
		if t.ModelRatio != nil && *t.ModelRatio <= 0 {
			return fmt.Errorf("tier[%d] model_ratio must be > 0", i)
		}
		if t.CompletionRatio != nil && *t.CompletionRatio <= 0 {
			return fmt.Errorf("tier[%d] completion_ratio must be > 0", i)
		}
		if t.CacheRatio != nil && *t.CacheRatio <= 0 {
			return fmt.Errorf("tier[%d] cache_ratio must be > 0", i)
		}
	}
	return nil
}

// validateContextTiersMap enforces ValidateContextTierList for every model in
// tiers.
func validateContextTiersMap(tiers map[string][]ContextTier) error {
	for model, list := range tiers {
		if err := ValidateContextTierList(list); err != nil {
			return fmt.Errorf("model %s: %w", model, err)
		}
	}
	return nil
}

// ValidateContextLengthTiersJSONString parses and validates s without
// applying it to the live map. pricing_write_legacy.go's probe calls this
// before opening a transaction, matching the other pricingOptionKeys'
// "reject before touching the database" contract.
func ValidateContextLengthTiersJSONString(s string) (map[string][]ContextTier, error) {
	tmp := make(map[string][]ContextTier)
	if err := json.Unmarshal([]byte(s), &tmp); err != nil {
		return nil, err
	}
	if err := validateContextTiersMap(tmp); err != nil {
		return nil, err
	}
	return tmp, nil
}

// UpdateContextLengthTiersByJSONString parses, validates and — only when
// valid — replaces the live map, mirroring UpdateCacheRatioByJSONString: a
// malformed or out-of-contract payload must never partially apply and must
// leave whatever tiers were previously effective billing exactly as before.
func UpdateContextLengthTiersByJSONString(s string) error {
	tmp, err := ValidateContextLengthTiersJSONString(s)
	if err != nil {
		return err
	}
	contextTiersMapMutex.Lock()
	contextTiersMap = tmp
	contextTiersMapMutex.Unlock()
	InvalidateExposedDataCache()
	return nil
}
