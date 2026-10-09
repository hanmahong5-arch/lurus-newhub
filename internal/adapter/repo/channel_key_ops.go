package repo

import (
	"errors"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// Account-pool helpers for multi-key channels: weighted key selection, the
// per-key routable test used by session stickiness, and the per-key proxy
// lookup. Callers of the exported helpers must NOT hold the channel polling
// lock; pickWeightedKey runs under it (GetNextEnabledKey holds it).

// weightedCursor is the smooth weighted round-robin state of one channel:
// current[idx] accumulates each key's weight per pick and the winner pays the
// total back, which spreads picks evenly instead of in bursts. Held in memory
// only; a restart just restarts the rotation.
var weightedCursor = struct {
	sync.Mutex
	state map[int]map[int]int
}{state: map[int]map[int]int{}}

// pickWeightedKey selects among the enabled keys by smooth weighted
// round-robin. A key with weight 0 is never picked; if every enabled key has
// weight 0 the channel has no usable key.
func pickWeightedKey(channel *Channel, keys []string, enabledIdx []int) (string, int, *types.NewAPIError) {
	idx, ok := nextWeighted(channel.Id, enabledIdx, channel.ChannelInfo.KeyWeight)
	if !ok {
		return "", 0, types.NewError(errors.New("no weighted key selectable"), types.ErrorCodeChannelNoAvailableKey)
	}
	return keys[idx], idx, nil
}

func nextWeighted(channelID int, candidates []int, weight func(int) int) (int, bool) {
	weightedCursor.Lock()
	defer weightedCursor.Unlock()
	cur := weightedCursor.state[channelID]
	if cur == nil {
		cur = map[int]int{}
		weightedCursor.state[channelID] = cur
	}
	live := make(map[int]struct{}, len(candidates))
	total, best, found := 0, 0, false
	for _, i := range candidates {
		w := weight(i)
		if w <= 0 {
			continue
		}
		live[i] = struct{}{}
		cur[i] += w
		total += w
		if !found || cur[i] > cur[best] {
			best, found = i, true
		}
	}
	// Forget keys that left the rotation (disabled, weight 0, deleted).
	for i := range cur {
		if _, ok := live[i]; !ok {
			delete(cur, i)
		}
	}
	if !found {
		return 0, false
	}
	cur[best] -= total
	return best, true
}

// ResetWeightedCursor drops the rotation state of a channel (tests, key list edits).
func ResetWeightedCursor(channelID int) {
	weightedCursor.Lock()
	delete(weightedCursor.state, channelID)
	weightedCursor.Unlock()
}

// KeyIfRoutable returns key idx when it is enabled and (in weighted mode) not
// weighted to zero. It is the validity test for a sticky binding: a bound key
// that fails it must be re-bound immediately.
func (channel *Channel) KeyIfRoutable(idx int) (string, bool) {
	if !channel.ChannelInfo.IsMultiKey {
		return "", false
	}
	keys := channel.GetKeys()
	if idx < 0 || idx >= len(keys) {
		return "", false
	}
	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()
	if st, ok := channel.ChannelInfo.MultiKeyStatusList[idx]; ok && st != common.ChannelStatusEnabled {
		return "", false
	}
	if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModeWeighted && channel.ChannelInfo.KeyWeight(idx) <= 0 {
		return "", false
	}
	return keys[idx], true
}

// KeyProxyOverride returns the proxy configured for key idx ("" when none).
func (channel *Channel) KeyProxyOverride(idx int) string {
	if !channel.ChannelInfo.IsMultiKey {
		return ""
	}
	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()
	return channel.ChannelInfo.KeyProxy(idx)
}
