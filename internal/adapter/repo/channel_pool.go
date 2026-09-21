package repo

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// MarkMultiKeyCooldown disables a single key in a multi-key channel and tags it
// with a future Unix-second deadline. The pool reaper will re-enable the key
// when now >= cooldownUntil. No-op when the channel is not multi-key or the key
// is not found.
//
// Mirrors UpdateChannelStatus's cache + persistence flow but additionally
// writes ChannelInfo.MultiKeyCooldownUntil[idx] = cooldownUntil.
func MarkMultiKeyCooldown(channelId int, usingKey string, cooldownUntil int64, reason string) bool {
	if cooldownUntil <= 0 {
		return false
	}

	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()

		cached, _ := CacheGetChannel(channelId)
		if cached == nil {
			return false
		}
		if !cached.ChannelInfo.IsMultiKey {
			return false
		}
		pollingLock := GetChannelPollingLock(channelId)
		pollingLock.Lock()
		applyMultiKeyCooldown(cached, usingKey, cooldownUntil, reason)
		pollingLock.Unlock()
	}

	channel, err := GetChannelById(channelId, true)
	if err != nil {
		return false
	}
	if !channel.ChannelInfo.IsMultiKey {
		return false
	}

	beforeStatus := channel.Status
	pollingLock := GetChannelPollingLock(channelId)
	pollingLock.Lock()
	applyMultiKeyCooldown(channel, usingKey, cooldownUntil, reason)
	pollingLock.Unlock()

	if err := channel.SaveWithoutKey(); err != nil {
		common.SysLog("MarkMultiKeyCooldown: save channel failed: " + err.Error())
		return false
	}
	if beforeStatus != channel.Status {
		// Channel went auto-disabled because all keys are cooling — sync abilities so
		// the dispatcher stops routing to it until the reaper recovers a key.
		if err := UpdateAbilityStatus(channelId, channel.Status == common.ChannelStatusEnabled); err != nil {
			common.SysLog("MarkMultiKeyCooldown: ability sync failed: " + err.Error())
		}
	}
	return true
}

// applyMultiKeyCooldown is the in-place mutation shared by the cache and DB paths.
// Caller MUST hold the per-channel polling lock.
func applyMultiKeyCooldown(channel *Channel, usingKey string, cooldownUntil int64, reason string) {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		return
	}
	keyIndex := -1
	for i, k := range keys {
		if k == usingKey {
			keyIndex = i
			break
		}
	}
	if keyIndex < 0 {
		return
	}
	if channel.ChannelInfo.MultiKeyStatusList == nil {
		channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
	}
	if channel.ChannelInfo.MultiKeyDisabledReason == nil {
		channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
	}
	if channel.ChannelInfo.MultiKeyDisabledTime == nil {
		channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
	}
	if channel.ChannelInfo.MultiKeyCooldownUntil == nil {
		channel.ChannelInfo.MultiKeyCooldownUntil = make(map[int]int64)
	}
	channel.ChannelInfo.MultiKeyStatusList[keyIndex] = common.ChannelStatusAutoDisabled
	channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
	channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
	channel.ChannelInfo.MultiKeyCooldownUntil[keyIndex] = cooldownUntil

	if len(channel.ChannelInfo.MultiKeyStatusList) >= channel.ChannelInfo.MultiKeySize {
		channel.Status = common.ChannelStatusAutoDisabled
		info := channel.GetOtherInfo()
		info["status_reason"] = "All keys are cooling or disabled"
		info["status_time"] = common.GetTimestamp()
		channel.SetOtherInfo(info)
	}
}

// ClearMultiKeyCooldown re-enables a single key whose cooldown has expired.
// Caller MUST hold the per-channel polling lock. Used by the reaper.
func ClearMultiKeyCooldown(channel *Channel, keyIndex int) {
	if channel.ChannelInfo.MultiKeyStatusList != nil {
		delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
	}
	if channel.ChannelInfo.MultiKeyDisabledReason != nil {
		delete(channel.ChannelInfo.MultiKeyDisabledReason, keyIndex)
	}
	if channel.ChannelInfo.MultiKeyDisabledTime != nil {
		delete(channel.ChannelInfo.MultiKeyDisabledTime, keyIndex)
	}
	if channel.ChannelInfo.MultiKeyCooldownUntil != nil {
		delete(channel.ChannelInfo.MultiKeyCooldownUntil, keyIndex)
	}
}

// ListOpenRouterMultiKeyChannelsForReaper returns the OpenRouter channels
// that have multi-key mode active, of any status and in any tenant. Used by
// the reaper (internal/app/openrouter_pool/reaper.go) to scan for expired
// cooldowns — cooldown recovery is platform-wide housekeeping, not a
// caller-facing view, so this stays deliberately tenant-blind. Returns
// []*Channel (clones, safe to read without locks for the inspection pass;
// mutations require the per-channel polling lock).
//
// The name carries "ForReaper" so a caller-facing surface cannot pick the
// tenant-blind variant up by autocomplete (cycle 13, V1DOORS/SECURITY-14):
// GetOpenRouterApiPoolStatus used to call the unscoped function directly, so
// any AdminAuth-level (not just root) caller could read every other tenant's
// OpenRouter channel names and masked key prefixes. Callers that have a
// tenant context take ListOpenRouterMultiKeyChannelsForScope below.
func ListOpenRouterMultiKeyChannelsForReaper() ([]*Channel, error) {
	return ListOpenRouterMultiKeyChannelsForScope(AllTenantsForAdmin())
}

// ListOpenRouterMultiKeyChannelsForScope is the tenant-scoped variant for
// caller-facing admin reads (GetOpenRouterApiPoolStatus,
// GET /api/openrouter-sync/api-pool). Pass ForTenant(callerTenantID) for a
// non-root caller and AllTenantsForAdmin() only for a platform root — the
// same convention as handler.DeleteHistoryLogs / repo.GetAllLogs. scope.apply
// filters on the channels table's tenant_id column the same way it already
// filters the logs table in log.go; the two tables share the column name and
// TenantScope carries no table-specific state.
func ListOpenRouterMultiKeyChannelsForScope(scope TenantScope) ([]*Channel, error) {
	var channels []*Channel
	err := scope.apply(DB.Where("type = ?", constant.ChannelTypeOpenRouter)).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	out := make([]*Channel, 0, len(channels))
	for _, c := range channels {
		if c.ChannelInfo.IsMultiKey {
			out = append(out, c)
		}
	}
	return out, nil
}
