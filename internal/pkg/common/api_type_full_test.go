package common

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// TestChannelType2APIType_FullMatrix asserts every mapped channel type resolves
// to its dedicated API type with ok=true — a mis-mapping here would route a
// relay request to the wrong provider adapter.
func TestChannelType2APIType_FullMatrix(t *testing.T) {
	cases := map[int]int{
		constant.ChannelTypeOpenAI:         constant.APITypeOpenAI,
		constant.ChannelTypeAnthropic:      constant.APITypeAnthropic,
		constant.ChannelTypeBaidu:          constant.APITypeBaidu,
		constant.ChannelTypePaLM:           constant.APITypePaLM,
		constant.ChannelTypeZhipu:          constant.APITypeZhipu,
		constant.ChannelTypeAli:            constant.APITypeAli,
		constant.ChannelTypeXunfei:         constant.APITypeXunfei,
		constant.ChannelTypeAIProxyLibrary: constant.APITypeAIProxyLibrary,
		constant.ChannelTypeTencent:        constant.APITypeTencent,
		constant.ChannelTypeGemini:         constant.APITypeGemini,
		constant.ChannelTypeZhipu_v4:       constant.APITypeZhipuV4,
		constant.ChannelTypeOllama:         constant.APITypeOllama,
		constant.ChannelTypePerplexity:     constant.APITypePerplexity,
		constant.ChannelTypeAws:            constant.APITypeAws,
		constant.ChannelTypeCohere:         constant.APITypeCohere,
		constant.ChannelTypeDify:           constant.APITypeDify,
		constant.ChannelTypeJina:           constant.APITypeJina,
		constant.ChannelCloudflare:         constant.APITypeCloudflare,
		constant.ChannelTypeSiliconFlow:    constant.APITypeSiliconFlow,
		constant.ChannelTypeVertexAi:       constant.APITypeVertexAi,
		constant.ChannelTypeMistral:        constant.APITypeMistral,
		constant.ChannelTypeDeepSeek:       constant.APITypeDeepSeek,
		constant.ChannelTypeMokaAI:         constant.APITypeMokaAI,
		constant.ChannelTypeVolcEngine:     constant.APITypeVolcEngine,
		constant.ChannelTypeBaiduV2:        constant.APITypeBaiduV2,
		constant.ChannelTypeOpenRouter:     constant.APITypeOpenRouter,
		constant.ChannelTypeXinference:     constant.APITypeXinference,
		constant.ChannelTypeXai:            constant.APITypeXai,
		constant.ChannelTypeCoze:           constant.APITypeCoze,
		constant.ChannelTypeJimeng:         constant.APITypeJimeng,
		constant.ChannelTypeMoonshot:       constant.APITypeMoonshot,
		constant.ChannelTypeSubmodel:       constant.APITypeSubmodel,
		constant.ChannelTypeMiniMax:        constant.APITypeMiniMax,
		constant.ChannelTypeReplicate:      constant.APITypeReplicate,

		constant.ChannelTypeTypeSafe:            constant.APITypeSystemOne,
		constant.ChannelTypeSystemOneCompatible: constant.APITypeSystemOne,
	}
	for channel, wantAPI := range cases {
		got, ok := ChannelType2APIType(channel)
		if !ok {
			t.Errorf("channel %d: ok=false, expected a mapping", channel)
		}
		if got != wantAPI {
			t.Errorf("channel %d: got api %d, want %d", channel, got, wantAPI)
		}
	}
}
