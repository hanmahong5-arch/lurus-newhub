package bifrosteng

import (
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/maximhq/bifrost/core/schemas"
)

// Coverage marks whether a newhub ApiType is owned by Bifrost, falls back to
// legacy, or is explicitly deferred (CN long-tail / missing Bifrost surface).
type Coverage string

const (
	CoverageOwned    Coverage = "bifrost-owned"
	CoverageLegacy   Coverage = "legacy-fallback"
	CoverageDeferred Coverage = "deferred"
)

// baseProvider maps a newhub APIType onto Bifrost's protocol family.
// Unmapped types return ("", false) and must take the legacy adaptor path.
func baseProvider(apiType int) (schemas.ModelProvider, bool) {
	switch apiType {
	case constant.APITypeOpenAI,
		constant.APITypeOpenRouter,
		constant.APITypeDeepSeek,
		constant.APITypeSiliconFlow,
		constant.APITypeMoonshot,
		constant.APITypeXai,
		constant.APITypeMistral,
		constant.APITypeXinference,
		constant.APITypePerplexity,
		constant.APITypeSubmodel,
		constant.APITypeMiniMax,
		constant.APITypeZhipuV4,
		constant.APITypeBaiduV2,
		constant.APITypeVolcEngine,
		constant.APITypeAli,
		constant.APITypeAIProxyLibrary:
		return schemas.OpenAI, true
	case constant.APITypeAnthropic:
		return schemas.Anthropic, true
	case constant.APITypeGemini:
		return schemas.Gemini, true
	case constant.APITypeAws:
		return schemas.Bedrock, true
	case constant.APITypeVertexAi:
		return schemas.Vertex, true
	case constant.APITypeOllama:
		return schemas.Ollama, true
	case constant.APITypeCohere:
		return schemas.Cohere, true
	default:
		return "", false
	}
}

// IsMapped reports whether apiType can ride Bifrost (else legacy fallback).
func IsMapped(apiType int) bool {
	_, ok := baseProvider(apiType)
	return ok
}

// ProviderKey builds a per-channel Bifrost ModelProvider so each channel can
// carry its own BaseURL via CustomProviderConfig without racing on a shared
// openai NetworkConfig.
func ProviderKey(channelID int) schemas.ModelProvider {
	return schemas.ModelProvider(fmt.Sprintf("nh-ch-%d", channelID))
}

// CoverageOf returns the coverage label for docs / metrics.
func CoverageOf(apiType int) Coverage {
	if IsMapped(apiType) {
		return CoverageOwned
	}
	switch apiType {
	case constant.APITypeBaidu,
		constant.APITypeXunfei,
		constant.APITypeZhipu,
		constant.APITypeTencent,
		constant.APITypePaLM,
		constant.APITypeDify,
		constant.APITypeJina,
		constant.APITypeCloudflare,
		constant.APITypeCoze,
		constant.APITypeJimeng,
		constant.APITypeMokaAI,
		constant.APITypeReplicate:
		// CN / long-tail vendors without a Bifrost baseProvider mapping.
		return CoverageDeferred
	default:
		return CoverageLegacy
	}
}
