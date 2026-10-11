// Package capability answers "what kind of work can this (channel type,
// model) route do?" without importing any provider adapter.
//
// Why a static table and not the adapters themselves: the repo layer needs the
// answer on the channel-selection hot path, and repo cannot import
// internal/adapter/provider (import cycle). The table below is therefore
// duplicated knowledge; internal/app/relay/capability_consistency_test.go
// pins it to the real adapters so the two cannot drift silently.
//
// Dependencies: standard library and internal/pkg/constant only.
package capability

import (
	"os"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// Modality is the kind of request a route serves. The empty value means
// "unknown" and is always treated as compatible: failing to infer a modality
// must never be a reason to refuse traffic.
type Modality string

const (
	Chat      Modality = "chat"
	Embedding Modality = "embedding"
	Rerank    Modality = "rerank"
	Decision  Modality = "decision"
	Image     Modality = "image"
	Audio     Modality = "audio"
)

// Valid reports whether s is one of the storable modalities.
func Valid(s string) bool {
	switch Modality(s) {
	case Chat, Embedding, Rerank, Decision, Image, Audio:
		return true
	}
	return false
}

// Source says which rule produced an inference. Source == SourceDefault means
// nothing positively identified the route, so the Chat answer is a guess.
type Source string

const (
	SourceOverride    Source = "override"
	SourceChannelType Source = "channel_type"
	SourceName        Source = "name"
	SourceTable       Source = "table"
	SourceDefault     Source = "default"
)

// Inference is the result of Infer.
type Inference struct {
	Modality Modality
	Source   Source
}

// Stored is the value persisted in abilities.modality. A defaulted guess is
// stored as "" (unknown) so a later, better rule is not shadowed by a stale
// "chat" and so the router keeps treating the route as compatible.
func (i Inference) Stored() string {
	if i.Source == SourceDefault {
		return ""
	}
	return string(i.Modality)
}

// nameRules are matched, in order, against the lower-cased model name.
// Order matters: "rerank" must beat "embed" for names such as
// "bge-reranker-v2" (which also contains "bge").
var nameRules = []struct {
	modality Modality
	needles  []string
}{
	{Rerank, []string{"rerank"}},
	{Embedding, []string{"embed", "bge", "gte", "e5-", "voyage", "sentence"}},
	{Audio, []string{"tts", "whisper", "audio", "transcribe"}},
	{Image, []string{"dall-e", "image", "flux"}},
}

// InferFromName applies the name-pattern rules alone.
func InferFromName(modelName string) (Modality, bool) {
	name := strings.ToLower(modelName)
	for _, rule := range nameRules {
		for _, needle := range rule.needles {
			if strings.Contains(name, needle) {
				return rule.modality, true
			}
		}
	}
	return "", false
}

// typeOnlyModality lists channel types that can serve exactly one modality, so
// the type alone decides it when the model name says nothing.
var typeOnlyModality = map[int]Modality{
	constant.ChannelTypeMidjourney:     Image,
	constant.ChannelTypeMidjourneyPlus: Image,
	constant.ChannelTypeJina:           Embedding, // rerank is split off by name in Infer
}

// Infer resolves the modality of a route. Precedence, highest first:
// administrator override > channel type > model-name pattern > static type
// table > chat (flagged SourceDefault).
func Infer(override string, channelType int, modelName string) Inference {
	if Valid(override) {
		return Inference{Modality(override), SourceOverride}
	}
	switch channelType {
	case constant.ChannelTypeTypeSafe, constant.ChannelTypeSystemOneCompatible:
		return Inference{Decision, SourceChannelType}
	case constant.ChannelTypeJina:
		if strings.Contains(strings.ToLower(modelName), "rerank") {
			return Inference{Rerank, SourceChannelType}
		}
	}
	if m, ok := InferFromName(modelName); ok {
		return Inference{m, SourceName}
	}
	if m, ok := typeOnlyModality[channelType]; ok {
		return Inference{m, SourceTable}
	}
	return Inference{Chat, SourceDefault}
}

// Compatible reports whether a route with inference got can serve a request
// that needs required. required == "" (the relay mode has no modality
// requirement) and unknown or merely-guessed routes are always compatible.
//
// A chat request accepts chat, audio and image routes: multimodal chat models
// (*-audio-*, *-image-preview) legitimately carry those words in their
// names while being served through the chat endpoint. What a chat request
// must never reach is an embedding, rerank or decision route.
func Compatible(required Modality, got Inference) bool {
	if required == "" || got.Modality == "" || got.Source == SourceDefault {
		return true
	}
	if required == Chat {
		switch got.Modality {
		case Chat, Audio, Image:
			return true
		}
		return false
	}
	return got.Modality == required
}

// unsupportedRerank are channel types whose adapter has no rerank wire. Every
// type NOT listed is OpenAI-compatible, Cohere or Jina and does support it.
var unsupportedRerank = map[int]bool{
	constant.ChannelTypeAnthropic:           true,
	constant.ChannelTypeGemini:              true,
	constant.ChannelTypeVertexAi:            true,
	constant.ChannelTypeDeepSeek:            true,
	constant.ChannelTypeAws:                 true,
	constant.ChannelTypeBaidu:               true,
	constant.ChannelTypeDify:                true,
	constant.ChannelTypeMiniMax:             true,
	constant.ChannelTypeMistral:             true,
	constant.ChannelTypeMokaAI:              true,
	constant.ChannelTypeOllama:              true,
	constant.ChannelTypePaLM:                true,
	constant.ChannelTypePerplexity:          true,
	constant.ChannelTypeTencent:             true,
	constant.ChannelTypeVolcEngine:          true,
	constant.ChannelTypeXai:                 true,
	constant.ChannelTypeXunfei:              true,
	constant.ChannelTypeZhipu:               true,
	constant.ChannelTypeZhipu_v4:            true,
	constant.ChannelTypeTypeSafe:            true,
	constant.ChannelTypeSystemOneCompatible: true,
	constant.ChannelTypeBaiduV2:             true,
	constant.ChannelTypeCoze:                true,
	// Image / video / music platforms: they have no text-retrieval wire.
	constant.ChannelTypeMidjourney:     true,
	constant.ChannelTypeMidjourneyPlus: true,
	constant.ChannelTypeSunoAPI:        true,
	constant.ChannelTypeKling:          true,
	constant.ChannelTypeJimeng:         true,
	constant.ChannelTypeVidu:           true,
	constant.ChannelTypeDoubaoVideo:    true,
	constant.ChannelTypeSora:           true,
}

// SupportsRerank reports whether the channel type's adapter implements the
// rerank wire.
func SupportsRerank(channelType int) bool { return !unsupportedRerank[channelType] }

// AdapterSupports reports whether the channel type's adapter can serve the
// modality at all. Only rerank is tabulated today; every other modality is
// answered true (unknown = compatible) until a consistency test proves a gap.
func AdapterSupports(channelType int, m Modality) bool {
	if m == Rerank {
		return SupportsRerank(channelType)
	}
	return true
}

// Filter modes for ROUTING_MODALITY_FILTER.
const (
	FilterOff     = "off"
	FilterObserve = "observe"
	FilterEnforce = "enforce"

	filterEnvVar = "ROUTING_MODALITY_FILTER"
)

// FilterMode reads ROUTING_MODALITY_FILTER fresh on every call (no caching),
// mirroring tenantpolicy.Mode: a change takes effect on the next request.
// Only the literals "enforce" and "off" change behaviour; anything else,
// including a typo, observes, so a bad value can neither silently turn
// enforcement on nor switch the metric off.
func FilterMode() string {
	switch os.Getenv(filterEnvVar) {
	case FilterEnforce:
		return FilterEnforce
	case FilterOff:
		return FilterOff
	}
	return FilterObserve
}
