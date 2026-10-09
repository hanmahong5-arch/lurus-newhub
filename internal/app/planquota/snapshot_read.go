package planquota

import (
	"context"
	"time"
)

// LatestSnapshot returns the last quota probe result of a channel (Redis, then
// process memory), read-only. ok=false when the channel was never probed.
func LatestSnapshot(channelID int) (*Snapshot, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return defaultStore.Get(ctx, channelID)
}
