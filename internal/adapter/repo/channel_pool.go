package repo

import (
	"errors"

	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// MarkMultiKeyCooldown disables a single key in a multi-key channel and tags it
// with a future Unix-second deadline. The pool reaper will re-enable the key
// when now >= cooldownUntil. No-op when the channel is not multi-key or the key
// is not found.
//
// Mirrors UpdateChannelStatus's cache + persistence flow but additionally
// writes ChannelInfo.MultiKeyCooldownUntil[idx] = cooldownUntil.
func MarkMultiKeyCooldown(channelId int, usingKey string, cooldownUntil int64, reason string) bool {
	ok, _ := markMultiKeyCooldown(channelId, usingKey, cooldownUntil, reason, false)
	return ok
}

// MarkMultiKeyCooldownReport is the write the relay uses: besides parking the
// key it reports allParkedUntil, the earliest deadline at which a key of the
// channel comes back when this write left NO key usable (0 otherwise). The
// caller records that deadline as a channel-level cooldown so selection skips
// the channel on every replica, whether or not its status was flipped.
func MarkMultiKeyCooldownReport(channelId int, usingKey string, cooldownUntil int64, reason string, keepStatus bool) (ok bool, allParkedUntil int64) {
	return markMultiKeyCooldown(channelId, usingKey, cooldownUntil, reason, keepStatus)
}

func markMultiKeyCooldown(channelId int, usingKey string, cooldownUntil int64, reason string, keepStatus bool) (bool, int64) {
	if cooldownUntil <= 0 {
		return false, 0
	}

	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()

		cached, _ := CacheGetChannel(channelId)
		if cached == nil {
			return false, 0
		}
		if !cached.ChannelInfo.IsMultiKey {
			return false, 0
		}
		pollingLock := GetChannelPollingLock(channelId)
		pollingLock.Lock()
		beforeCached := cached.Status
		applyMultiKeyCooldownOpt(cached, usingKey, cooldownUntil, reason, keepStatus)
		afterCached := cached.Status
		pollingLock.Unlock()
		if beforeCached != afterCached && afterCached != common.ChannelStatusEnabled {
			// The apply above only flipped the cached struct; the routing index
			// still lists the channel until the next full sync (SYNC_FREQUENCY),
			// so selection would keep landing on a channel with no usable key.
			CacheUpdateChannelStatus(channelId, afterCached)
		}
	}

	channel, err := GetChannelById(channelId, true)
	if err != nil {
		return false, 0
	}
	if !channel.ChannelInfo.IsMultiKey {
		return false, 0
	}

	beforeStatus := channel.Status
	pollingLock := GetChannelPollingLock(channelId)
	pollingLock.Lock()
	allParkedUntil := applyMultiKeyCooldownOpt(channel, usingKey, cooldownUntil, reason, keepStatus)
	pollingLock.Unlock()

	if err := channel.SaveWithoutKey(); err != nil {
		common.SysLog("MarkMultiKeyCooldown: save channel failed: " + err.Error())
		return false, 0
	}
	if beforeStatus != channel.Status {
		// Channel went auto-disabled because all keys are cooling — sync abilities so
		// the dispatcher stops routing to it until the reaper recovers a key.
		if err := UpdateAbilityStatus(channelId, channel.Status == common.ChannelStatusEnabled); err != nil {
			common.SysLog("MarkMultiKeyCooldown: ability sync failed: " + err.Error())
		}
	}
	return true, allParkedUntil
}

// applyMultiKeyCooldown is the in-place mutation shared by the cache and DB paths.
// Caller MUST hold the per-channel polling lock.
func applyMultiKeyCooldown(channel *Channel, usingKey string, cooldownUntil int64, reason string) {
	applyMultiKeyCooldownOpt(channel, usingKey, cooldownUntil, reason, false)
}

// applyMultiKeyCooldownOpt is applyMultiKeyCooldown with keepStatus: when true
// the channel's own status is never touched. It returns the earliest cooldown
// deadline among the parked keys when no key of the channel is usable any more
// (0 while at least one key is still serving, or when the key is unknown).
func applyMultiKeyCooldownOpt(channel *Channel, usingKey string, cooldownUntil int64, reason string, keepStatus bool) int64 {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		return 0
	}
	keyIndex := -1
	for i, k := range keys {
		if k == usingKey {
			keyIndex = i
			break
		}
	}
	if keyIndex < 0 {
		return 0
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

	if len(channel.ChannelInfo.MultiKeyStatusList) < channel.ChannelInfo.MultiKeySize {
		return 0
	}
	if !keepStatus {
		channel.Status = common.ChannelStatusAutoDisabled
		info := channel.GetOtherInfo()
		info["status_reason"] = "All keys are cooling or disabled"
		info["status_time"] = common.GetTimestamp()
		channel.SetOtherInfo(info)
	}
	// Keys parked for a reason other than a cooldown have no deadline and are
	// skipped, so the result is when the first cooling key returns.
	earliest := int64(0)
	for idx, u := range channel.ChannelInfo.MultiKeyCooldownUntil {
		if channel.ChannelInfo.MultiKeyStatusList[idx] == common.ChannelStatusEnabled {
			continue
		}
		if earliest == 0 || u < earliest {
			earliest = u
		}
	}
	return earliest
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

// ListMultiKeyChannelsForReaper returns every multi-key channel of any type,
// status and tenant. The 429 cooldown now applies to all multi-key channels, so
// the reaper must scan all of them to re-enable expired keys. Tenant-blind on
// purpose (platform housekeeping), hence the "ForReaper" name; caller-facing
// views keep using the OpenRouter-scoped list below.
func ListMultiKeyChannelsForReaper() ([]*Channel, error) {
	// Only the columns the reaper reads; it writes back through
	// SaveChannelPoolState, which touches the same three, so the partially
	// loaded struct can never overwrite the rest of the row.
	q := multiKeyOnly(DB.Select("id", "status", "other_info", "channel_info"))
	var channels []*Channel
	if err := q.Find(&channels).Error; err != nil {
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

// multiKeyOnly narrows a channel query to multi-key channels in SQL.
func multiKeyOnly(q *gorm.DB) *gorm.DB {
	if DB.Name() == "postgres" {
		return q.Where("channel_info::jsonb @> ?::jsonb", `{"is_multi_key":true}`)
	}
	return q.Where("json_extract(channel_info, '$.is_multi_key') = 1")
}

// SaveChannelPoolState persists exactly the pool-recovery columns (status,
// other_info, channel_info) of a channel loaded by ListMultiKeyChannelsForReaper.
func SaveChannelPoolState(channel *Channel) error {
	if channel.Id == 0 {
		return errors.New("channel ID is 0")
	}
	return DB.Model(&Channel{}).Where("id = ?", channel.Id).
		Select("status", "other_info", "channel_info").
		Updates(&Channel{Status: channel.Status, OtherInfo: channel.OtherInfo, ChannelInfo: channel.ChannelInfo}).Error
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

// EarliestMultiKeyRecovery reports when the first multi-key channel that is
// serving (tenant, group, model) only through cooling keys will get a key back.
//
// A multi-key channel whose every key is cooling is flipped to AutoDisabled and
// its abilities switched off, so selection sees no candidate at all - the same
// shape as a model nobody configured. Callers use this on that empty result to
// tell "all keys rate-limited, retry shortly" from "no such model". ok is false
// when no channel qualifies: every key of a qualifying channel must hold a
// cooldown deadline, which excludes channels disabled for any other reason.
// pred (the request's provider filter) narrows the channels considered; nil
// accepts all.
func EarliestMultiKeyRecovery(tenantID, group, model string, pred ChannelPredicate) (until int64, ok bool) {
	normalized := ratio_setting.FormatMatchingModelName(model)
	now := common.GetTimestamp()
	// Candidates are copied out under channelSyncLock and inspected after it is
	// released: the per-channel polling lock must never be taken while
	// channelSyncLock is held (same order as carryOverPollingIndices).
	var candidates []*Channel
	// Only fields no cooldown or status writer touches (tenant, group, models,
	// the provider setting) are read here; Status / IsMultiKey / MultiKeySize /
	// the cooldown map are judged in the second phase, under both locks.
	collect := func(ch *Channel) {
		if tenantID != "" && ch.TenantId != "" && ch.TenantId != "default" && ch.TenantId != tenantID {
			return
		}
		if !channelServes(ch, group, model, normalized) || (pred != nil && !pred(ch)) {
			return
		}
		candidates = append(candidates, ch)
	}

	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		for _, ch := range channelsIDM {
			collect(ch)
		}
		channelSyncLock.RUnlock()
	} else {
		var channels []*Channel
		if err := multiKeyOnly(DB.Where("status = ?", common.ChannelStatusAutoDisabled)).Find(&channels).Error; err != nil {
			return 0, false
		}
		for _, ch := range channels {
			collect(ch)
		}
	}

	for _, ch := range candidates {
		pollingLock := GetChannelPollingLock(ch.Id)
		pollingLock.Lock()
		// Status is written by CacheUpdateChannelStatus under channelSyncLock only
		// and the cooldown fields by the apply path under the polling lock only,
		// so the read needs both. Polling -> sync is the order the cache already
		// allows (nothing takes a polling lock while holding the sync lock).
		channelSyncLock.RLock()
		earliest := int64(0)
		if ch.Status == common.ChannelStatusAutoDisabled && ch.ChannelInfo.IsMultiKey && ch.ChannelInfo.MultiKeySize > 0 {
			cd := ch.ChannelInfo.MultiKeyCooldownUntil
			if len(cd) >= ch.ChannelInfo.MultiKeySize {
				for _, u := range cd {
					if earliest == 0 || u < earliest {
						earliest = u
					}
				}
			}
		}
		channelSyncLock.RUnlock()
		pollingLock.Unlock()
		if earliest <= 0 {
			continue
		}
		if earliest <= now {
			earliest = now + 1
		}
		if !ok || earliest < until {
			until, ok = earliest, true
		}
	}
	return until, ok
}

func channelServes(ch *Channel, group, model, normalized string) bool {
	inGroup := false
	for _, g := range ch.GetGroups() {
		if g == group {
			inGroup = true
			break
		}
	}
	if !inGroup {
		return false
	}
	for _, m := range ch.GetModels() {
		if m == model || m == normalized {
			return true
		}
	}
	return false
}
