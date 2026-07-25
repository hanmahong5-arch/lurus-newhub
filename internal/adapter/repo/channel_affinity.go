package repo

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// GetSatisfiedChannelByID resolves one specific channel, but ONLY if it is
// still a legitimate candidate for (group, model) — i.e. exactly the set
// GetRandomSatisfiedChannel would have drawn from, filtered to a single id.
//
// This exists for session affinity: re-pinning a conversation to the channel
// that served its first turn keeps the upstream prompt cache warm, but it must
// never widen who can reach what. Affinity is allowed to bias the CHOICE among
// already-eligible channels; it is not allowed to resurrect a channel that has
// since been disabled, un-grouped, or dropped the model. Callers treat
// (nil, nil) as "affinity expired, fall back to normal selection".
//
// Deliberately ignores priority tiers: a pinned channel stays valid even if a
// higher-priority channel appeared later, because moving an in-flight
// conversation costs a full cache miss. New conversations still land on the
// top tier via the normal path.
func GetSatisfiedChannelByID(group string, model string, channelId int) (*Channel, error) {
	if channelId <= 0 || group == "" || model == "" {
		return nil, nil
	}

	if !common.MemoryCacheEnabled {
		var ability Ability
		// enabled=true mirrors the ability filter in getChannelQuery; the row is
		// deleted/flipped whenever the channel is disabled or loses the model.
		err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and channel_id = ?",
			group, model, true, channelId).First(&ability).Error
		if err != nil {
			// Not found (or any lookup error) => no affinity. Fail open.
			return nil, nil //nolint:nilerr // absence is the answer, not an error
		}
		channel := Channel{}
		if err := DB.First(&channel, "id = ?", channelId).Error; err != nil {
			return nil, nil //nolint:nilerr // fail open to normal selection
		}
		if channel.Status != common.ChannelStatusEnabled {
			return nil, nil
		}
		return &channel, nil
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	candidates := group2model2channels[group][model]
	if len(candidates) == 0 {
		// Same normalized-name fallback as GetRandomSatisfiedChannel, so an
		// affinity recorded under one spelling survives the other.
		candidates = group2model2channels[group][ratio_setting.FormatMatchingModelName(model)]
	}
	for _, id := range candidates {
		if id != channelId {
			continue
		}
		if channel, ok := channelsIDM[id]; ok {
			return channel, nil
		}
		// Index/entity skew: treat as a miss rather than erroring the request.
		return nil, nil
	}
	return nil, nil
}
