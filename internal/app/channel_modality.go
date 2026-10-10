package app

import (
	"errors"
	"fmt"
	"strconv"
	"sync"

	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/capability"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// Reason codes carried in the 501 body's error.details.reasons[].code.
const (
	ModalityReasonMismatch           = "modality_mismatch"
	ModalityReasonAdapterUnsupported = "adapter_unsupported"
)

// RequiredModalityForRelayMode maps a relay mode to the modality a route must
// serve and a stable metric label. A zero modality means the mode carries no
// requirement (moderations, midjourney, task APIs, the native alt-format wire, ...): those
// are never filtered, so adding the filter cannot change their behaviour.
//
// The mapping lives here rather than in internal/pkg/capability because the
// relay-mode constants belong to the provider layer, which the zero-dependency
// capability package must not import.
func RequiredModalityForRelayMode(mode int) (capability.Modality, string) {
	switch mode {
	case relayconstant.RelayModeChatCompletions, relayconstant.RelayModeCompletions,
		relayconstant.RelayModeEdits, relayconstant.RelayModeResponses,
		relayconstant.RelayModeResponsesCompact, relayconstant.RelayModeRealtime:
		return capability.Chat, "chat"
	case relayconstant.RelayModeEmbeddings:
		return capability.Embedding, "embeddings"
	case relayconstant.RelayModeRerank:
		return capability.Rerank, "rerank"
	case relayconstant.RelayModeSystemOne:
		return capability.Decision, "systemone"
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		return capability.Image, "images"
	case relayconstant.RelayModeAudioSpeech, relayconstant.RelayModeAudioTranscription,
		relayconstant.RelayModeAudioTranslation:
		return capability.Audio, "audio"
	}
	return "", ""
}

// ModalityMissReason is one entry of the 501 body's details.reasons. It names
// a cause and a count only: never a channel id, name, URL or key.
type ModalityMissReason struct {
	Code       string
	Message    string
	RouteCount int
}

// ModalityMissError is returned by CacheGetRandomSatisfiedChannel when the
// model had candidate routes but every one failed the modality filter. It
// satisfies errors.Is(err, repo.ErrNoChannelSupportsModality).
type ModalityMissError struct {
	Required           capability.Modality
	RelayMode          string
	Mismatched         int
	AdapterUnsupported int
}

func (e *ModalityMissError) Error() string {
	return fmt.Sprintf("no route supports %s requests", e.Required)
}

func (e *ModalityMissError) Is(target error) bool {
	return target == repo.ErrNoChannelSupportsModality
}

// Reasons renders the counted causes in a stable order.
func (e *ModalityMissError) Reasons() []ModalityMissReason {
	var out []ModalityMissReason
	if e.Mismatched > 0 {
		out = append(out, ModalityMissReason{
			Code:       ModalityReasonMismatch,
			Message:    fmt.Sprintf("the model is served only by routes of a different kind than this %s request", e.RelayMode),
			RouteCount: e.Mismatched,
		})
	}
	if e.AdapterUnsupported > 0 {
		out = append(out, ModalityMissReason{
			Code:       ModalityReasonAdapterUnsupported,
			Message:    fmt.Sprintf("the provider adapters serving the model do not implement %s", e.RelayMode),
			RouteCount: e.AdapterUnsupported,
		})
	}
	return out
}

// modalityProbe records what the modality predicate decided during ONE repo
// selection, so the caller can tell "the modality filter emptied the pool"
// (passed == 0, something rejected) from a provider-filter or cooldown miss.
// It is not safe for concurrent use; a selection runs it under one read lock.
type modalityProbe struct {
	passed             int
	mismatched         int
	adapterUnsupported int
}

// modalityLogged dedupes the observe-mode SysLog to once per route kind, so a
// hot model with a wrong-kind channel logs one line, not one per request.
var modalityLogged sync.Map

// modalityPredicate builds the route filter for a request, or nil when the
// filter is off or the request's relay mode has no modality requirement (the
// zero-cost, behaviour-unchanged path).
//
// It must be the FIRST predicate in the AND chain: the cooldown probe then
// counts only channels that already fit the request, and probe.passed == 0
// really means the modality filter emptied the pool.
//
// observe: counts + logs and lets the route through. enforce: drops it.
func modalityPredicate(c *gin.Context, model string, probe *modalityProbe) repo.ChannelPredicate {
	if c == nil || model == "" {
		return nil
	}
	mode := capability.FilterMode()
	if mode == capability.FilterOff {
		return nil
	}
	relayMode := common.GetContextKeyInt(c, constant.ContextKeyRelayMode)
	required, label := RequiredModalityForRelayMode(relayMode)
	if required == "" {
		return nil
	}
	override := repo.ModalityOverride(model)
	enforce := mode == capability.FilterEnforce
	return func(ch *repo.Channel) bool {
		inferred := capability.Infer(override, ch.Type, model)
		adapterOK := capability.AdapterSupports(ch.Type, required)
		if adapterOK && capability.Compatible(required, inferred) {
			probe.passed++
			return true
		}
		if adapterOK {
			probe.mismatched++
		} else {
			probe.adapterUnsupported++
		}
		metrics.RecordRoutingModalityMismatch(label)
		key := strconv.Itoa(ch.Id) + "|" + model + "|" + label
		if _, seen := modalityLogged.LoadOrStore(key, struct{}{}); !seen {
			common.SysLog(fmt.Sprintf("routing: channel %d (type %d) is a %s route for model %s but the request needs %s (mode=%s)",
				ch.Id, ch.Type, inferred.Modality, model, required, mode))
		}
		if enforce {
			return false
		}
		probe.passed++
		return true
	}
}

// classifyModalityMiss upgrades the repo's generic predicate miss to a
// ModalityMissError when the modality filter, not a later predicate, emptied
// the pool. Every other error passes through untouched.
func classifyModalityMiss(c *gin.Context, err error, probe *modalityProbe) error {
	if !errors.Is(err, repo.ErrNoChannelSatisfiesPredicate) || probe == nil || probe.passed > 0 ||
		probe.mismatched+probe.adapterUnsupported == 0 {
		return err
	}
	required, label := RequiredModalityForRelayMode(common.GetContextKeyInt(c, constant.ContextKeyRelayMode))
	return &ModalityMissError{
		Required:           required,
		RelayMode:          label,
		Mismatched:         probe.mismatched,
		AdapterUnsupported: probe.adapterUnsupported,
	}
}
