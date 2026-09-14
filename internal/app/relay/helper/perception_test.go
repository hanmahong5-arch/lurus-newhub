package helper

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestEstimateQuotaFromUsage_NilInputs(t *testing.T) {
	if got := EstimateQuotaFromUsage(nil, &dto.Usage{}); got != 0 {
		t.Errorf("nil info: got %d, want 0", got)
	}
	if got := EstimateQuotaFromUsage(&relaycommon.RelayInfo{}, nil); got != 0 {
		t.Errorf("nil usage: got %d, want 0", got)
	}
}

func TestEstimateQuotaFromUsage_FixedPrice(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			UsePrice:   true,
			ModelPrice: 0.01,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.5,
			},
		},
	}
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 50}
	got := EstimateQuotaFromUsage(info, usage)
	// 0.01 * QuotaPerUnit * 1.5 = 0.01 * 500000 * 1.5 = 7500
	want := 7500
	if got != want {
		t.Errorf("fixed price: got %d, want %d", got, want)
	}
}

func TestEstimateQuotaFromUsage_TokenBased(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			ModelRatio:      2.0,
			CompletionRatio: 3.0,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.0,
			},
		},
	}
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
	}
	got := EstimateQuotaFromUsage(info, usage)
	// (100 + 50*3) * 2.0 * 1.0 = 250 * 2 = 500
	want := 500
	if got != want {
		t.Errorf("token based: got %d, want %d", got, want)
	}
}

func TestComputeLurusExtension_BillingModes(t *testing.T) {
	tests := []struct {
		name            string
		preAuthID       int64
		identityAcctID  int64
		wantBillingMode string
	}{
		{"pre_auth", 123, 456, "pre_auth"},
		{"trust_cache", 0, 456, "trust_cache"},
		{"legacy", 0, 0, "legacy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				PlatformPreAuthID: tt.preAuthID,
				IdentityAccountID: tt.identityAcctID,
				UserQuota:         1000000,
				PriceData: types.PriceData{
					ModelRatio: 1.0,
					GroupRatioInfo: types.GroupRatioInfo{
						GroupRatio: 1.0,
					},
				},
			}
			ext := ComputeLurusExtension(info, &dto.Usage{}, 500)
			if ext.BillingMode != tt.wantBillingMode {
				t.Errorf("billing mode: got %q, want %q", ext.BillingMode, tt.wantBillingMode)
			}
		})
	}
}

func TestComputeLurusExtension_CostAndBalance(t *testing.T) {
	info := &relaycommon.RelayInfo{
		UserQuota: int(common.QuotaPerUnit * 10), // 10 LB
		PriceData: types.PriceData{
			ModelRatio: 2.0,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.5,
			},
		},
	}
	usage := &dto.Usage{
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 42},
	}
	quota := int(common.QuotaPerUnit * 2) // 2 LB cost
	ext := ComputeLurusExtension(info, usage, quota)

	if ext.CostLB != 2.0 {
		t.Errorf("cost: got %f, want 2.0", ext.CostLB)
	}
	if ext.BalanceRemaining != 8.0 {
		t.Errorf("balance: got %f, want 8.0", ext.BalanceRemaining)
	}
	if ext.CachedTokens != 42 {
		t.Errorf("cached tokens: got %d, want 42", ext.CachedTokens)
	}
	if ext.ModelRatio != 2.0 {
		t.Errorf("model ratio: got %f, want 2.0", ext.ModelRatio)
	}
	if ext.GroupRatio != 1.5 {
		t.Errorf("group ratio: got %f, want 1.5", ext.GroupRatio)
	}
}

func TestComputeLurusExtension_NegativeBalance(t *testing.T) {
	info := &relaycommon.RelayInfo{
		UserQuota: 100,
		PriceData: types.PriceData{
			ModelRatio: 1.0,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.0,
			},
		},
	}
	ext := ComputeLurusExtension(info, &dto.Usage{}, 200)
	if ext.BalanceRemaining != 0 {
		t.Errorf("negative balance should be 0: got %f", ext.BalanceRemaining)
	}
}

// TestEstimateQuotaFromUsage_TierCrossingMatchesSettlement is the cycle-8 plan
// §8 L5 B-F2 oracle. The perception path runs while the response is being
// written, before postConsumeQuota settles the same request, so it used to
// report the pre-consume tier while the wallet was debited at the resettled
// one — a request-cost header worth half the real charge on a call whose
// actual prompt tokens cross a configured threshold. Both numbers are computed
// here from the same RelayInfo: the estimate a client sees, and the ratio the
// settlement path would use.
func TestEstimateQuotaFromUsage_TierCrossingMatchesSettlement(t *testing.T) {
	seedRatios(t, `{"perception-tier-model":1.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	seedContextTiers(t, `{"perception-tier-model":[{"threshold_tokens":0,"model_ratio":1.0},{"threshold_tokens":3000,"model_ratio":2.0}]}`)

	// Pre-consume: a small estimate, so ModelPriceHelper picks the base rung.
	info := &relaycommon.RelayInfo{OriginModelName: "perception-tier-model", UsingGroup: "default"}
	if _, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{}); err != nil {
		t.Fatalf("pre-consume: %v", err)
	}
	if info.PriceData.ModelRatio != 1.0 {
		t.Fatalf("pre-consume ModelRatio = %v, want 1.0 (the estimate is below the threshold)", info.PriceData.ModelRatio)
	}

	// The response carries 4000 actual prompt tokens, above the 3000 rung.
	usage := &dto.Usage{PromptTokens: 4000, CompletionTokens: 0, TotalTokens: 4000}
	got := EstimateQuotaFromUsage(info, usage)

	if info.PriceData.ModelRatio != 2.0 {
		t.Errorf("ModelRatio the client-facing cost is computed from = %v, want 2.0 — the perception path must resettle before reporting, or the header disagrees with the debit",
			info.PriceData.ModelRatio)
	}
	// 4000 prompt tokens at ratio 2.0 x group 1.0.
	if want := 8000; got != want {
		t.Errorf("estimated quota = %d, want %d (the higher tier's ratio)", got, want)
	}
}
