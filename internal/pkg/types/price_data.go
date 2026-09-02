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
	return fmt.Sprintf("ModelPrice: %f, ModelRatio: %f, CompletionRatio: %f, CacheRatio: %f, GroupRatio: %f, UsePrice: %t, CacheCreationRatio: %f, CacheCreation5mRatio: %f, CacheCreation1hRatio: %f, QuotaToPreConsume: %d, ImageRatio: %f, AudioRatio: %f, AudioCompletionRatio: %f", p.ModelPrice, p.ModelRatio, p.CompletionRatio, p.CacheRatio, p.GroupRatioInfo.GroupRatio, p.UsePrice, p.CacheCreationRatio, p.CacheCreation5mRatio, p.CacheCreation1hRatio, p.QuotaToPreConsume, p.ImageRatio, p.AudioRatio, p.AudioCompletionRatio)
}
