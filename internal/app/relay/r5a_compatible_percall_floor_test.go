package relay

// r5a_compatible_percall_floor_test.go — G1 (lane L-A, 2026-08-27 live-defect
// round), OpenAI-compatible sibling of internal/app's
// r5a_percall_floor_wiring_test.go. Same hard requirement: the test must
// drive the REAL producer (helper.ModelPriceHelper) instead of hand-building
// types.PriceData, because the defect lived in how PriceData gets populated
// (helper.ModelPriceHelper's UsePrice branch never assigns ModelRatio — see
// internal/app/r5a_price_floor.go's header comment), not in postConsumeQuota
// itself. Every existing postConsumeQuota test in this package
// (postconsume_claude_fees_test.go, postconsume_extra_test.go) builds
// PriceData by hand and would stay green even if ModelPriceHelper regressed.

import (
	"encoding/json"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// r5aSeedModelPrice installs a single per-call model price for the duration
// of a test and restores whatever was live before. Mirrors
// internal/app/r5a_percall_floor_wiring_test.go's helper of the same name
// (different package, no collision).
func r5aSeedModelPrice(t *testing.T, modelName string, price float64) {
	t.Helper()
	prev := ratio_setting.ModelPrice2JSONString()
	raw, err := json.Marshal(map[string]float64{modelName: price})
	if err != nil {
		t.Fatalf("marshal seed price: %v", err)
	}
	if err := ratio_setting.UpdateModelPriceByJSONString(string(raw)); err != nil {
		t.Fatalf("seed model price: %v", err)
	}
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(prev)
	})
}

// TestR5ACompatiblePerCallFloor_WiredThroughRealProducer drives
// helper.ModelPriceHelper -> relayInfo.PriceData -> postConsumeQuota. A
// sub-$0.000001 per-call price must still bill exactly 1 quota unit.
func TestR5ACompatiblePerCallFloor_WiredThroughRealProducer(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	const model = "r5a-compat-percall-model"
	r5aSeedModelPrice(t, model, 0.0000001) // 1e-7: rounds to 0 quota at QuotaPerUnit=500000, groupRatio=1

	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}

	const wantQuota = 1 // 1e-7 * 500000 * groupRatio(1) = 0.05 -> rounds to 0 -> floor lifts to 1
	got := runPostConsumeQuota(t, "compat-percall", func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = model
		priceData, err := helper.ModelPriceHelper(c, info, usage.PromptTokens, &types.TokenCountMeta{})
		if err != nil {
			t.Fatalf("ModelPriceHelper: %v", err)
		}
		if !priceData.UsePrice || priceData.ModelRatio != 0 {
			t.Fatalf("precondition not met: UsePrice=%v ModelRatio=%v, want UsePrice=true ModelRatio=0", priceData.UsePrice, priceData.ModelRatio)
		}
		info.PriceData = priceData
	}, usage)

	if got != wantQuota {
		t.Errorf("billed quota = %d, want %d (sub-$0.000001 per-call price must still charge 1, not settle free)", got, wantQuota)
	}
}

// TestR5ACompatiblePerCallFloor_GenuinelyFreeModelStaysFree is the negative
// lock: modelPrice==0 through the same real producer must still settle to 0.
func TestR5ACompatiblePerCallFloor_GenuinelyFreeModelStaysFree(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	const model = "r5a-compat-percall-free"
	r5aSeedModelPrice(t, model, 0)

	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}

	const wantQuota = 0
	got := runPostConsumeQuota(t, "compat-percall-free", func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = model
		priceData, err := helper.ModelPriceHelper(c, info, usage.PromptTokens, &types.TokenCountMeta{})
		if err != nil {
			t.Fatalf("ModelPriceHelper: %v", err)
		}
		info.PriceData = priceData
	}, usage)

	if got != wantQuota {
		t.Errorf("billed quota = %d, want %d (a genuinely free per-call model must not be charged by the new floor)", got, wantQuota)
	}
}

