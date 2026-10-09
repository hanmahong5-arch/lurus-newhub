package repo

import "github.com/LurusTech/lurus-hub/internal/pkg/common"

// ChannelsForMonitoring returns every channel (disabled ones included) for the
// metrics refresher and the public model-status view. It reads the in-memory
// routing cache when that is on (no query per refresh) and falls back to one
// database read otherwise. Callers must treat the result as read-only.
func ChannelsForMonitoring() ([]*Channel, error) {
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		out := make([]*Channel, 0, len(channelsIDM))
		for _, ch := range channelsIDM {
			out = append(out, ch)
		}
		channelSyncLock.RUnlock()
		return out, nil
	}
	return GetAllChannels(0, 0, true, true)
}
