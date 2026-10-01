package handler

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func fastTokenCountMetaForPricing(request dto.Request) *types.TokenCountMeta {
	if request == nil {
		return &types.TokenCountMeta{}
	}
	meta := &types.TokenCountMeta{
		TokenType: types.TokenTypeTokenizer,
	}
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		if r.MaxCompletionTokens > r.MaxTokens {
			meta.MaxTokens = int(r.MaxCompletionTokens)
		} else {
			meta.MaxTokens = int(r.MaxTokens)
		}
	case *dto.OpenAIResponsesRequest:
		meta.MaxTokens = int(r.MaxOutputTokens)
	case *dto.ClaudeRequest:
		meta.MaxTokens = int(r.MaxTokens)
	case *dto.ImageRequest:
		// Pricing for image requests depends on ImagePriceRatio; safe to compute even when CountToken is disabled.
		return r.GetTokenCountMeta()
	default:
		// Best-effort: leave CombineText empty to avoid large allocations.
	}
	return meta
}
