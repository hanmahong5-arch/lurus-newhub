package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ClearChannelCooldown lifts every cooldown slot of one channel: the local and
// cached-snapshot entries, the members of the chan_cd_idx ZSET (the source of
// truth every replica reads; they drop their own local copy at the next
// snapshot refresh) and the chan_cd:{id}:{idx} mirror keys. Call it
// when an operator re-enables a channel or key, or a test proves the channel
// healthy again; deleting only the mirror keys would not lift anything.
func ClearChannelCooldown(channelID int) {
	// Stamp the clear before and after the Redis leg: a snapshot read that began
	// before either stamp may carry the slot and must not be merged back.
	markCooldownCleared(channelID)
	defer markCooldownCleared(channelID)
	cooldownMu.Lock()
	for slot := range cooldownLocal {
		if slot.channelID == channelID {
			delete(cooldownLocal, slot)
			delete(cooldownSynced, slot)
		}
	}
	for slot := range cooldownRemote {
		if slot.channelID == channelID {
			delete(cooldownRemote, slot)
		}
	}
	cooldownMu.Unlock()

	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cooldownRedisTimeout)
	defer cancel()
	members, err := common.RDB.ZRange(ctx, channelCooldownIndexKey, 0, -1).Result()
	if err != nil {
		common.SysLog("channel cooldown: redis read failed on clear: " + err.Error())
		return
	}
	prefix := fmt.Sprintf("%d:", channelID)
	var zrem []interface{}
	var keys []string
	for _, m := range members {
		if !strings.HasPrefix(m, prefix) {
			continue
		}
		zrem = append(zrem, m)
		keys = append(keys, channelCooldownKeyPrefix+m)
	}
	if len(zrem) == 0 {
		return
	}
	pipe := common.RDB.Pipeline()
	pipe.ZRem(ctx, channelCooldownIndexKey, zrem...)
	pipe.Del(ctx, keys...)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysLog("channel cooldown: redis clear failed: " + err.Error())
	}
}

// markCooldownCleared advances the clear generation and records it for the
// channel, so refreshCooldownSnapshot can discard slots of that channel read
// from Redis before the clear completed.
func markCooldownCleared(channelID int) {
	cooldownMu.Lock()
	cooldownGen++
	cooldownClearedGen[channelID] = cooldownGen
	cooldownMu.Unlock()
}
