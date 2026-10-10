package common

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/capability"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// GetEndpointTypesByChannelType 获取渠道最优先端点类型（所有的渠道都支持 OpenAI 端点）
func GetEndpointTypesByChannelType(channelType int, modelName string) []constant.EndpointType {
	var endpointTypes []constant.EndpointType
	// A model that is positively an embedding or rerank model is reached only
	// through its own endpoint; listing it under openai/anthropic would invite
	// chat calls the modality filter now refuses. A merely-defaulted chat guess
	// (Source == default) falls through to the legacy per-type answer.
	// Decision channels (TypeSafe / System One) are handled below.
	if inf := capability.Infer("", channelType, modelName); inf.Source != capability.SourceDefault {
		switch inf.Modality {
		case capability.Rerank:
			return []constant.EndpointType{constant.EndpointTypeJinaRerank}
		case capability.Embedding:
			if channelType != constant.ChannelTypeTypeSafe && channelType != constant.ChannelTypeSystemOneCompatible {
				return []constant.EndpointType{constant.EndpointTypeEmbeddings}
			}
		}
	}
	switch channelType {
	case constant.ChannelTypeJina:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeJinaRerank}
	//case constant.ChannelTypeMidjourney, constant.ChannelTypeMidjourneyPlus:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeMidjourney}
	//case constant.ChannelTypeSunoAPI:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeSuno}
	//case constant.ChannelTypeKling:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeKling}
	//case constant.ChannelTypeJimeng:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeJimeng}
	case constant.ChannelTypeAws:
		fallthrough
	case constant.ChannelTypeAnthropic:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeVertexAi:
		fallthrough
	case constant.ChannelTypeGemini:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeOpenRouter: // OpenRouter 只支持 OpenAI 端点
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
	case constant.ChannelTypeSora:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
	case constant.ChannelTypeTypeSafe, constant.ChannelTypeSystemOneCompatible:
		// Return early: the image-generation prefix below keys off the model
		// name, and these channels never serve an image or chat endpoint.
		return []constant.EndpointType{constant.EndpointTypeSystemOne}
	default:
		if IsOpenAIResponseOnlyModel(modelName) {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIResponse}
		} else {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
		}
	}
	if IsImageGenerationModel(modelName) {
		// add to first
		endpointTypes = append([]constant.EndpointType{constant.EndpointTypeImageGeneration}, endpointTypes...)
	}
	return endpointTypes
}
