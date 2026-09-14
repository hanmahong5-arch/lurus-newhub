package types

import "fmt"

type GroupRatioInfo struct {
	GroupRatio        float64
	GroupSpecialRatio float64
	HasSpecialRatio   bool
}

type PriceData struct {
	FreeModel            bool
	ModelPrice           float64
	ModelRatio           float64
	CompletionRatio      float64
	CacheRatio           float64
	CacheCreationRatio   float64
	CacheCreation5mRatio float64
	CacheCreation1hRatio float64
	// CacheCreationRatioDefaulted is true when CacheCreationRatio is the map
	// default because no per-model entry exists (set only by helper.ModelPriceHelper);
	// an explicitly set ratio is always honoured. See CacheCreationRatioForWire.
	CacheCreationRatioDefaulted bool
	ImageRatio                  float64
	AudioRatio                  float64
	AudioCompletionRatio        float64
	OtherRatios                 map[string]float64
	UsePrice                    bool
	QuotaToPreConsume           int // 预消耗额度
	GroupRatioInfo              GroupRatioInfo
	// ContextTierThreshold is the ThresholdTokens of the declarative
	// context-length pricing tier (billing-pricing-14,
	// ratio_setting.ContextTier) that overrode ModelRatio/CompletionRatio/
	// CacheRatio for this call; 0 means no tier applied. Debug/ToSetting only
	// — it is not projected into the consume-log "other" map (see the
	// no-new-other.*-key rule in the cycle-8 plan), the effect is already
	// visible in the logged ModelRatio itself.
	ContextTierThreshold int
	// BaseModelRatio/BaseCompletionRatio/BaseCacheRatio are the flat,
	// non-tiered ratios as they stood at pre-consume (set by
	// helper.ModelPriceHelper before any context-length tier override is
	// applied). helper.ResettleContextTier re-evaluates the tier at
	// settlement starting from THESE frozen values, not from a fresh read of
	// the live ratio_setting maps — an admin edit or a routine option-sync
	// tick landing between pre-consume and settlement must not re-price an
	// in-flight request. Zero-valued (and unused) for UsePrice models and for
	// any model ModelPriceHelper never ran against (e.g. a hand-built
	// PriceData in a test that does not go through ModelPriceHelper).
	BaseModelRatio      float64
	BaseCompletionRatio float64
	BaseCacheRatio      float64
	// BaseRatiosSet marks the three fields above as written by
	// helper.ModelPriceHelper. helper.ResettleContextTier refuses to overwrite
	// the live ratios from an unset snapshot, which would charge the call at
	// ratio 0; a zero value cannot say that on its own, because a free model
	// legitimately carries a zero ratio.
	BaseRatiosSet bool
}

func (p *PriceData) AddOtherRatio(key string, ratio float64) {
	if p.OtherRatios == nil {
		p.OtherRatios = make(map[string]float64)
	}
	if ratio <= 0 {
		return
	}
	p.OtherRatios[key] = ratio
}

// CacheCreationRatioForWire returns the ratio a cache write is billed at.
// promptIncludesCached is the wire flag stamped where the usage was parsed
// (dto.Usage.PromptTokensIncludeCached): true on the OpenAI/Gemini wire, false
// on the Anthropic wire. The map default behind CacheCreationRatio (1.25) is
// Anthropic's universal write surcharge and is the right answer for any
// unlisted Claude name. On the OpenAI wire (prompt_tokens_details /
// input_tokens_details .cache_write_tokens, parsed since 2026-09-02) a write
// costs the plain input rate unless the vendor says otherwise for that model
// (GPT-5.6 and later: 1.25x, seeded in ratio_setting), so an unlisted model on
// that wire bills writes at 1 instead of inheriting another vendor's surcharge.
func (p *PriceData) CacheCreationRatioForWire(promptIncludesCached bool) float64 {
	if promptIncludesCached && p.CacheCreationRatioDefaulted {
		return 1
	}
	return p.CacheCreationRatio
}

type PerCallPriceData struct {
	ModelPrice     float64
	Quota          int
	GroupRatioInfo GroupRatioInfo
}

func (p *PriceData) ToSetting() string {
	return fmt.Sprintf("ModelPrice: %f, ModelRatio: %f, CompletionRatio: %f, CacheRatio: %f, GroupRatio: %f, UsePrice: %t, CacheCreationRatio: %f, CacheCreation5mRatio: %f, CacheCreation1hRatio: %f, QuotaToPreConsume: %d, ImageRatio: %f, AudioRatio: %f, AudioCompletionRatio: %f, ContextTierThreshold: %d", p.ModelPrice, p.ModelRatio, p.CompletionRatio, p.CacheRatio, p.GroupRatioInfo.GroupRatio, p.UsePrice, p.CacheCreationRatio, p.CacheCreation5mRatio, p.CacheCreation1hRatio, p.QuotaToPreConsume, p.ImageRatio, p.AudioRatio, p.AudioCompletionRatio, p.ContextTierThreshold)
}
