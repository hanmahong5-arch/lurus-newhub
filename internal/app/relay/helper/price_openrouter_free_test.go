package helper

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// TestModelPriceHelper_OpenRouterFreeModel covers the free-model-sync case:
// an OpenRouter channel importing a ":free" model that has no configured
// price or ratio must be priced as free (no error), while every neighboring
// case (wrong channel, no ":free" suffix, or a configured ratio) keeps the
// pre-lane behavior.
func TestModelPriceHelper_OpenRouterFreeModel(t *testing.T) {
	// Disable free-model pre-consume so FreeModel/QuotaToPreConsume=0 is
	// actually observable, matching the convention in
	// TestModelPriceHelper_FreeModel/TestModelPriceHelper_FreeByZeroPrice.
	qs := operation_setting.GetQuotaSetting()
	prev := qs.EnableFreeModelPreConsume
	qs.EnableFreeModelPreConsume = false
	t.Cleanup(func() { qs.EnableFreeModelPreConsume = prev })

	t.Run("openrouter unpriced :free model is free, no error", func(t *testing.T) {
		seedRatios(t, `{}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
		info := &relaycommon.RelayInfo{
			OriginModelName: "google/gemma-4-31b-it:free",
			UsingGroup:      "default",
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter},
		}
		pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !pd.FreeModel {
			t.Error("FreeModel should be true for an unpriced OpenRouter :free model")
		}
		if pd.QuotaToPreConsume != 0 {
			t.Errorf("QuotaToPreConsume = %d, want 0", pd.QuotaToPreConsume)
		}
		if pd.ModelRatio != 0 {
			t.Errorf("ModelRatio = %v, want 0", pd.ModelRatio)
		}
	})

	t.Run("openrouter unpriced model without :free suffix still rejected", func(t *testing.T) {
		seedRatios(t, `{}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
		info := &relaycommon.RelayInfo{
			OriginModelName: "openrouter-unknown-family-zzz-9999",
			UsingGroup:      "default",
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter},
		}
		_, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
		if err == nil {
			t.Fatal("expected unset-ratio error for a non-:free unpriced model")
		}
	})

	t.Run("non-openrouter channel with unpriced :free model still rejected", func(t *testing.T) {
		seedRatios(t, `{}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
		info := &relaycommon.RelayInfo{
			OriginModelName: "google/gemma-4-31b-it:free",
			UsingGroup:      "default",
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		}
		_, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
		if err == nil {
			t.Fatal("expected unset-ratio error when the channel is not OpenRouter")
		}
	})

	t.Run("openrouter :free model with a configured non-zero ratio is not free", func(t *testing.T) {
		seedRatios(t, `{"google/gemma-4-31b-it:free":3.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
		info := &relaycommon.RelayInfo{
			OriginModelName: "google/gemma-4-31b-it:free",
			UsingGroup:      "default",
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter},
		}
		pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if pd.FreeModel {
			t.Error("FreeModel should be false when a non-zero ratio is configured")
		}
		if pd.ModelRatio != 3.0 {
			t.Errorf("ModelRatio = %v, want 3.0 (configured ratio must win)", pd.ModelRatio)
		}
	})

	t.Run("mapped origin name resolves to a :free upstream model", func(t *testing.T) {
		seedRatios(t, `{}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
		info := &relaycommon.RelayInfo{
			OriginModelName: "gemma-free-alias",
			UsingGroup:      "default",
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeOpenRouter,
				UpstreamModelName: "google/gemma-4-31b-it:free",
			},
		}
		pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !pd.FreeModel {
			t.Error("FreeModel should be true when the mapped upstream model name ends with :free")
		}
		if pd.QuotaToPreConsume != 0 {
			t.Errorf("QuotaToPreConsume = %d, want 0", pd.QuotaToPreConsume)
		}
	})
}
