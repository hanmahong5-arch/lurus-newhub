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
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// fixPricingIsolateRatioState snapshots the process-wide ratio settings (and the
// option map that repo.UpdateOption writes through) and restores them after the
// test so a persisted ratio cannot leak into sibling tests.
func fixPricingIsolateRatioState(t *testing.T) {
	t.Helper()

	prevModelRatio := ratio_setting.ModelRatio2JSONString()
	prevCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	prevModelPrice := ratio_setting.ModelPrice2JSONString()

	common.OptionMapRWMutex.Lock()
	prevOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(prevModelRatio)
		_ = ratio_setting.UpdateCompletionRatioByJSONString(prevCompletionRatio)
		_ = ratio_setting.UpdateModelPriceByJSONString(prevModelPrice)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = prevOptionMap
		common.OptionMapRWMutex.Unlock()
	})
}

// fixPricingReadOption returns the stored value of an option row.
func fixPricingReadOption(t *testing.T, ctx *modelsWriteTestCtx, key string) string {
	t.Helper()
	var opt repo.Option
	if err := ctx.db.Where("key = ?", key).First(&opt).Error; err != nil {
		t.Fatalf("option %s not persisted: %v", key, err)
	}
	return opt.Value
}

// Per-token model: the submitted model_ratio / completion_ratio must actually
// drive billing after creation.
func TestFixModelsPricing_PerTokenRatioIsPersisted(t *testing.T) {
	fixPricingIsolateRatioState(t)
	ctx := setupModelsWriteRouter(t)

	const modelName = "fix-pricing-per-token-model"

	w := postModel(ctx, map[string]interface{}{
		"model_name":       modelName,
		"quota_type":       0,
		"model_ratio":      999.5,
		"completion_ratio": 2.5,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}

	ratio, ok, _ := ratio_setting.GetModelRatio(modelName)
	if !ok || ratio != 999.5 {
		t.Fatalf("model ratio = %v (found=%v), want 999.5 — submitted ratio was dropped", ratio, ok)
	}
	if src := ratio_setting.GetModelPricingSource(modelName); src.Source != "explicit" {
		t.Errorf("pricing source = %q, want explicit (fallback means the ratio was never stored)", src.Source)
	}
	if got := ratio_setting.GetCompletionRatio(modelName); got != 2.5 {
		t.Errorf("completion ratio = %v, want 2.5", got)
	}

	// The ratio must survive a restart, i.e. be written to the option row.
	if stored := fixPricingReadOption(t, ctx, "ModelRatio"); !strings.Contains(stored, modelName) {
		t.Errorf("ModelRatio option does not contain %q: %s", modelName, stored)
	}
	if stored := fixPricingReadOption(t, ctx, "CompletionRatio"); !strings.Contains(stored, modelName) {
		t.Errorf("CompletionRatio option does not contain %q: %s", modelName, stored)
	}
}

// Per-call model: model_price must be stored, which is also what makes the
// derived quota type come back as 1 (repo.GetPricing keys off the price entry).
func TestFixModelsPricing_PerCallPriceIsPersisted(t *testing.T) {
	fixPricingIsolateRatioState(t)
	ctx := setupModelsWriteRouter(t)

	const modelName = "fix-pricing-per-call-model"

	w := postModel(ctx, map[string]interface{}{
		"model_name":  modelName,
		"quota_type":  1,
		"model_price": 3.3,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}

	price, found := ratio_setting.GetModelPrice(modelName, false)
	if !found || price != 3.3 {
		t.Fatalf("model price = %v (found=%v), want 3.3 — submitted price was dropped", price, found)
	}
	if stored := fixPricingReadOption(t, ctx, "ModelPrice"); !strings.Contains(stored, modelName) {
		t.Errorf("ModelPrice option does not contain %q: %s", modelName, stored)
	}
}

// Choosing pay-per-call without a price cannot be honoured (there is nothing to
// charge per call), so it must be rejected instead of silently created as a
// per-token model.
func TestFixModelsPricing_PerCallWithoutPriceRejected(t *testing.T) {
	fixPricingIsolateRatioState(t)
	ctx := setupModelsWriteRouter(t)

	const modelName = "fix-pricing-per-call-no-price"

	w := postModel(ctx, map[string]interface{}{
		"model_name": modelName,
		"quota_type": 1,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d — body: %s", w.Code, w.Body.String())
	}
	out := parseWriteResp(t, w)
	if out["error_code"] != "MISSING_MODEL_PRICE" {
		t.Errorf("error_code = %v, want MISSING_MODEL_PRICE", out["error_code"])
	}

	var count int64
	if err := ctx.db.Model(&repo.Model{}).Where("model_name = ?", modelName).Count(&count).Error; err != nil {
		t.Fatalf("count models: %v", err)
	}
	if count != 0 {
		t.Errorf("model row count = %d, want 0 (rejected request must not create a catalogue entry)", count)
	}
}

// enable_groups has no write path — it is derived from the channels serving the
// model — so accepting it would be a silent lie.
func TestFixModelsPricing_EnableGroupsRejected(t *testing.T) {
	fixPricingIsolateRatioState(t)
	ctx := setupModelsWriteRouter(t)

	w := postModel(ctx, map[string]interface{}{
		"model_name":    "fix-pricing-enable-groups",
		"enable_groups": []string{"default", "vip"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d — body: %s", w.Code, w.Body.String())
	}
	out := parseWriteResp(t, w)
	if out["error_code"] != "ENABLE_GROUPS_UNSUPPORTED" {
		t.Errorf("error_code = %v, want ENABLE_GROUPS_UNSUPPORTED", out["error_code"])
	}
}

// A negative ratio would invert billing; it must never be stored.
func TestFixModelsPricing_NegativeRatioRejected(t *testing.T) {
	fixPricingIsolateRatioState(t)
	ctx := setupModelsWriteRouter(t)

	const modelName = "fix-pricing-negative-ratio"

	w := postModel(ctx, map[string]interface{}{
		"model_name":  modelName,
		"model_ratio": -1.5,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d — body: %s", w.Code, w.Body.String())
	}
	out := parseWriteResp(t, w)
	if out["error_code"] != "INVALID_RATIO" {
		t.Errorf("error_code = %v, want INVALID_RATIO", out["error_code"])
	}
	if ratio, ok, _ := ratio_setting.GetModelRatio(modelName); ok && ratio < 0 {
		t.Errorf("negative ratio %v was stored", ratio)
	}
}

// Omitting pricing keeps the platform default — the create path must not write
// a zero ratio for models whose pricing the admin did not touch.
func TestFixModelsPricing_NoPricingSubmittedLeavesDefaults(t *testing.T) {
	fixPricingIsolateRatioState(t)
	ctx := setupModelsWriteRouter(t)

	const modelName = "fix-pricing-untouched-model"

	w := postModel(ctx, map[string]interface{}{"model_name": modelName})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", w.Code, w.Body.String())
	}
	if src := ratio_setting.GetModelPricingSource(modelName); src.Source == "explicit" {
		t.Errorf("pricing source = explicit, want a fallback source for a model created without pricing")
	}
	if _, found := ratio_setting.GetModelPrice(modelName, false); found {
		t.Errorf("model price entry created for a model with no submitted price")
	}
}
