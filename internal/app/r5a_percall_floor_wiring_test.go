package app

// r5a_percall_floor_wiring_test.go — G1 (lane L-A, 2026-08-27 live-defect
// round). This round's hard requirement is that a wiring test must not prove
// the fix by hand-building types.PriceData and calling the guarded function
// directly — it must observe the REAL producer (helper.ModelPriceHelper)
// feeding the REAL consumer (PostClaudeConsumeQuota). Every other floor test
// in this package (l1_quota_claude_round_test.go) builds PriceData by hand,
// which cannot catch a bug that lives entirely in how PriceData gets
// populated in the first place — which is exactly where this defect lived
// (helper.ModelPriceHelper's UsePrice branch never assigns ModelRatio; see
// internal/app/r5a_price_floor.go's header comment for the full chain).

import (
	"encoding/json"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// r5aSeedModelPrice installs a single per-call model price for the duration
// of a test and restores whatever was live before, so other tests in this
// package (and other _test.go files sharing the same process-global
// ratio_setting maps) are unaffected. Mirrors the save/restore idiom already
// used by internal/app/relay/helper/price_test.go's seedRatios.
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

// TestR5APriceHelper_UsePriceLeavesModelRatioZero pins the ROOT CAUSE itself:
// helper.ModelPriceHelper (internal/app/relay/helper/price.go), the real
// producer of types.PriceData, must report UsePrice==true and
// ModelRatio==0.0 for a per-call-priced model. If a future change starts
// assigning ModelRatio in the UsePrice branch, this test — not just the
// downstream settlement tests below — is the one that catches it, because it
// asserts the producer's output directly rather than inferring it from a
// debited amount.
func TestR5APriceHelper_UsePriceLeavesModelRatioZero(t *testing.T) {
	const model = "r5a-percall-model-root"
	r5aSeedModelPrice(t, model, 0.0000001) // 1e-7: rounds to 0 quota at QuotaPerUnit=500000, groupRatio=1

	c := createTestGinContext()
	info := &relaycommon.RelayInfo{OriginModelName: model}

	priceData, err := helper.ModelPriceHelper(c, info, 10, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("ModelPriceHelper: %v", err)
	}
	if !priceData.UsePrice {
		t.Fatalf("UsePrice = false, want true (model was seeded via price map, not ratio map)")
	}
	if priceData.ModelRatio != 0 {
		t.Fatalf("ModelRatio = %v, want exactly 0 — if this now fails, ModelPriceHelper's UsePrice branch has started assigning ModelRatio and the r5a_price_floor.go predicate choice needs revisiting", priceData.ModelRatio)
	}
	if priceData.ModelPrice != 0.0000001 {
		t.Errorf("ModelPrice = %v, want 1e-7", priceData.ModelPrice)
	}
}

// TestR5AClaudeNativePerCallFloor_WiredThroughRealProducer drives the full
// chain: helper.ModelPriceHelper -> relayInfo.PriceData -> PostClaudeConsumeQuota.
// A sub-$0.000001 per-call price must still debit exactly 1 quota unit, not 0.
func TestR5AClaudeNativePerCallFloor_WiredThroughRealProducer(t *testing.T) {
	const model = "r5a-percall-model-claude"
	r5aSeedModelPrice(t, model, 0.0000001)

	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now().Add(-time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}

	priceData, err := helper.ModelPriceHelper(c, relayInfo, 10, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("ModelPriceHelper: %v", err)
	}
	if !priceData.UsePrice || priceData.ModelRatio != 0 {
		t.Fatalf("precondition not met: UsePrice=%v ModelRatio=%v, want UsePrice=true ModelRatio=0", priceData.UsePrice, priceData.ModelRatio)
	}
	relayInfo.PriceData = priceData

	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	}

	const wantQuota = 1 // 1e-7 * 500000 * groupRatio(1) = 0.05 -> rounds to 0 -> floor lifts to 1

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if got := before - after; got != wantQuota {
		t.Errorf("Claude-native debited %d, want %d (sub-$0.000001 per-call price must still charge 1, not settle free)", got, wantQuota)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	if logRow.Quota != wantQuota {
		t.Errorf("consume log quota = %d, want %d", logRow.Quota, wantQuota)
	}
}

// TestR5AClaudeNativePerCallFloor_GenuinelyFreeModelStaysFree is the negative
// lock: modelPrice==0 through the SAME real producer must still settle to 0,
// so the floor added for G1 cannot start charging models that are actually
// configured as free.
func TestR5AClaudeNativePerCallFloor_GenuinelyFreeModelStaysFree(t *testing.T) {
	const model = "r5a-percall-model-free"
	r5aSeedModelPrice(t, model, 0)

	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now().Add(-time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}

	priceData, err := helper.ModelPriceHelper(c, relayInfo, 10, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("ModelPriceHelper: %v", err)
	}
	relayInfo.PriceData = priceData

	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	}

	const wantQuota = 0 // genuinely free (price=0): the floor must NOT fire

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if got := before - after; got != wantQuota {
		t.Errorf("Claude-native debited %d, want %d (a genuinely free per-call model must not be charged by the new floor)", got, wantQuota)
	}
}
