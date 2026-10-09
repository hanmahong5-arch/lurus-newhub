package planquota

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// LatestSnapshot returns the last quota probe result of a channel (Redis, then
// process memory), read-only. ok=false when the channel was never probed.
func LatestSnapshot(channelID int) (*Snapshot, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return defaultStore.Get(ctx, channelID)
}

// LatestSnapshots reads the last probe result of many channels with ONE Redis
// round trip (MGET); channels missing from Redis fall back to process memory.
// Channels never probed are absent from the result.
func LatestSnapshots(channelIDs []int) map[int]*Snapshot {
	out := make(map[int]*Snapshot, len(channelIDs))
	if len(channelIDs) == 0 {
		return out
	}
	if common.RedisEnabled && common.RDB != nil {
		keys := make([]string, len(channelIDs))
		for i, id := range channelIDs {
			keys[i] = snapshotKeyPrefix + strconv.Itoa(id)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		vals, err := common.RDB.MGet(ctx, keys...).Result()
		cancel()
		if err == nil {
			for i, v := range vals {
				s, ok := v.(string)
				if !ok {
					continue
				}
				var snap Snapshot
				if json.Unmarshal([]byte(s), &snap) == nil {
					out[channelIDs[i]] = &snap
				}
			}
		}
	}
	for _, id := range channelIDs {
		if _, ok := out[id]; ok {
			continue
		}
		if snap, ok := memSnapshot(id); ok {
			out[id] = snap
		}
	}
	return out
}

// WindowView renders a snapshot in the shape stored under other_info.plan_quota
// ({kind, windows:[{name,used_pct,reset_at}], fetched_at, error}). nil when
// there is no snapshot.
func WindowView(s *Snapshot) map[string]interface{} {
	if s == nil {
		return nil
	}
	wins := make([]map[string]interface{}, 0, len(s.Windows))
	for _, w := range s.Windows {
		wins = append(wins, map[string]interface{}{"name": w.Name, "used_pct": w.UsedPct, "reset_at": w.ResetAt})
	}
	v := map[string]interface{}{"kind": s.Kind, "windows": wins, "fetched_at": s.FetchedAt}
	if s.Error != "" {
		v["error"] = s.Error
	}
	return v
}

func memSnapshot(id int) (*Snapshot, bool) {
	if rs, ok := defaultStore.(*redisStore); ok {
		return rs.getMem(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return defaultStore.Get(ctx, id)
}

// StoreSnapshot records a probe result in the default store (the runner's own
// write path, exposed so read-model tests can seed a snapshot).
func StoreSnapshot(s *Snapshot) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	defaultStore.Put(ctx, s)
}
