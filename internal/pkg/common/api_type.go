package common

import "github.com/LurusTech/lurus-hub/internal/pkg/constant"

func ChannelType2APIType(channelType int) (int, bool) {
	apiType := -1
	switch channelType {
	case constant.ChannelTypeOpenAI:
		apiType = constant.APITypeOpenAI
	case constant.ChannelTypeAnthropic:
		apiType = constant.APITypeAnthropic
	case constant.ChannelTypeBaidu:
		apiType = constant.APITypeBaidu
	case constant.ChannelTypePaLM:
		apiType = constant.APITypePaLM
	case constant.ChannelTypeZhipu:
		apiType = constant.APITypeZhipu
	case constant.ChannelTypeAli:
		apiType = constant.APITypeAli
	case constant.ChannelTypeXunfei:
		apiType = constant.APITypeXunfei
	case constant.ChannelTypeAIProxyLibrary:
		apiType = constant.APITypeAIProxyLibrary
	case constant.ChannelTypeTencent:
		apiType = constant.APITypeTencent
	case constant.ChannelTypeGemini:
		apiType = constant.APITypeGemini
	case constant.ChannelTypeZhipu_v4:
		apiType = constant.APITypeZhipuV4
	case constant.ChannelTypeOllama:
		apiType = constant.APITypeOllama
	case constant.ChannelTypePerplexity:
		apiType = constant.APITypePerplexity
	case constant.ChannelTypeAws:
		apiType = constant.APITypeAws
	case constant.ChannelTypeCohere:
		apiType = constant.APITypeCohere
	case constant.ChannelTypeDify:
		apiType = constant.APITypeDify
	case constant.ChannelTypeJina:
		apiType = constant.APITypeJina
	case constant.ChannelCloudflare:
		apiType = constant.APITypeCloudflare
	case constant.ChannelTypeSiliconFlow:
		apiType = constant.APITypeSiliconFlow
	case constant.ChannelTypeVertexAi:
		apiType = constant.APITypeVertexAi
	case constant.ChannelTypeMistral:
		apiType = constant.APITypeMistral
	case constant.ChannelTypeDeepSeek:
		apiType = constant.APITypeDeepSeek
	case constant.ChannelTypeMokaAI:
		apiType = constant.APITypeMokaAI
	case constant.ChannelTypeVolcEngine:
		apiType = constant.APITypeVolcEngine
	case constant.ChannelTypeBaiduV2:
		apiType = constant.APITypeBaiduV2
	case constant.ChannelTypeOpenRouter:
		apiType = constant.APITypeOpenRouter
	case constant.ChannelTypeXinference:
		apiType = constant.APITypeXinference
	case constant.ChannelTypeXai:
		apiType = constant.APITypeXai
	case constant.ChannelTypeCoze:
		apiType = constant.APITypeCoze
	case constant.ChannelTypeJimeng:
		apiType = constant.APITypeJimeng
	case constant.ChannelTypeMoonshot:
		apiType = constant.APITypeMoonshot
	case constant.ChannelTypeSubmodel:
		apiType = constant.APITypeSubmodel
	case constant.ChannelTypeMiniMax:
		apiType = constant.APITypeMiniMax
	case constant.ChannelTypeReplicate:
		apiType = constant.APITypeReplicate
	}
	if apiType == -1 {
		return constant.APITypeOpenAI, false
	}
	return apiType, true
}

// responsesCompactSupportedChannels lists the channel types allowed to
// receive POST /v1/responses/compact (cycle-8 L6, wire-formats-03). This
// cycle: OpenAI only — the vendor whose wire the documented field subset was
// modelled against; channel types outside this map are refused before any
// upstream call (relay.ResponsesHelper).
var responsesCompactSupportedChannels = map[int]bool{
	constant.ChannelTypeOpenAI: true,
}

// SupportsResponsesCompact reports whether channelType may receive a
// /v1/responses/compact request.
func SupportsResponsesCompact(channelType int) bool {
	return responsesCompactSupportedChannels[channelType]
}

// responsesStatefulSupportedChannels lists the channel types eligible for
// the STATEFUL half of the OpenAI Responses API — the response_registry
// insert hook (relay.ResponsesHelper) and the read-time re-check in
// handler.RelayResponsesRetrieve/Delete (cycle-8 L7 repair round, finding
// B-F6). Deliberately a SEPARATE map from responsesCompactSupportedChannels
// above, even though both happen to be OpenAI-only this cycle: the two
// questions are not the same (one is "may receive /v1/responses/compact",
// the other is "may have its POST /v1/responses id pinned for later
// GET/DELETE"), and a future channel type could be added to one without
// belonging in the other — see openai/adaptor.go's Azure GetRequestURL
// branch, which has no handling for RelayModeResponsesRetrieve/Delete and
// would silently build the Azure response-CREATION URL (dropping the
// response_id) if a row were ever written for a non-OpenAI channel type —
// see handler/relay_responses_registry.go's read-time re-check comment for
// why: relayMode is never assigned onto info.RelayMode here, so it stays
// whatever Path2RelayMode derived from the URL (RelayModeResponses for
// "/v1/responses/<id>"), and GetRequestURL does have a case for that value
// on Azure.
var responsesStatefulSupportedChannels = map[int]bool{
	constant.ChannelTypeOpenAI: true,
}

// SupportsResponsesStateful reports whether channelType is eligible to have
// a POST /v1/responses id pinned in response_registry and later served by
// GET/DELETE /v1/responses/:response_id.
func SupportsResponsesStateful(channelType int) bool {
	return responsesStatefulSupportedChannels[channelType]
}
