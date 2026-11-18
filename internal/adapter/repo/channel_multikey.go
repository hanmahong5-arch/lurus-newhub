package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func (channel *Channel) GetKeys() []string {
	if channel.Key == "" {
		return []string{}
	}
	if len(channel.Keys) > 0 {
		return channel.Keys
	}
	trimmed := strings.TrimSpace(channel.Key)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			res := make([]string, len(arr))
			for i, v := range arr {
				res[i] = string(v)
			}
			return res
		}
	}
	return strings.Split(strings.Trim(channel.Key, "\n"), "\n")
}

func (channel *Channel) GetNextEnabledKey() (string, int, *types.NewAPIError) {
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	keys := channel.GetKeys()
	if len(keys) == 0 {
		return "", 0, types.NewError(errors.New("no keys available"), types.ErrorCodeChannelNoAvailableKey)
	}

	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	statusList := channel.ChannelInfo.MultiKeyStatusList
	getStatus := func(idx int) int {
		if statusList == nil {
			return common.ChannelStatusEnabled
		}
		if status, ok := statusList[idx]; ok {
			return status
		}
		return common.ChannelStatusEnabled
	}

	enabledIdx := make([]int, 0, len(keys))
	for i := range keys {
		if getStatus(i) == common.ChannelStatusEnabled {
			enabledIdx = append(enabledIdx, i)
		}
	}
	if len(enabledIdx) == 0 {
		cooldowns := channel.ChannelInfo.MultiKeyCooldownUntil
		if len(cooldowns) > 0 {
			earliest := int64(0)
			allCooling := true
			for i := range keys {
				if getStatus(i) == common.ChannelStatusEnabled {
					continue
				}
				until, ok := cooldowns[i]
				if !ok || until <= 0 {
					allCooling = false
					break
				}
				if earliest == 0 || until < earliest {
					earliest = until
				}
			}
			if allCooling && earliest > 0 {
				apiErr := types.NewError(errors.New("all keys cooling"), types.ErrorCodeChannelAllKeysCooling)
				apiErr.RetryAfterUnix = earliest
				return "", 0, apiErr
			}
		}
		return "", 0, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
	}

	switch channel.ChannelInfo.MultiKeyMode {
	case constant.MultiKeyModeRandom:
		selectedIdx := enabledIdx[rand.Intn(len(enabledIdx))]
		return keys[selectedIdx], selectedIdx, nil
	case constant.MultiKeyModeWeighted:
		return pickWeightedKey(channel, keys, enabledIdx)
	case constant.MultiKeyModePolling:
		channelInfo, err := CacheGetChannelInfo(channel.Id)
		if err != nil {
			return "", 0, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		defer func() {
			if common.DebugEnabled {
				println(fmt.Sprintf("channel %d polling index: %d", channel.Id, channel.ChannelInfo.MultiKeyPollingIndex))
			}
			if !common.MemoryCacheEnabled {
				_ = channel.SaveChannelInfo()
			}
		}()
		start := channelInfo.MultiKeyPollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}
		for i := 0; i < len(keys); i++ {
			idx := (start + i) % len(keys)
			if getStatus(idx) == common.ChannelStatusEnabled {
				channel.ChannelInfo.MultiKeyPollingIndex = (idx + 1) % len(keys)
				return keys[idx], idx, nil
			}
		}
		return keys[enabledIdx[0]], enabledIdx[0], nil
	default:
		return keys[enabledIdx[0]], enabledIdx[0], nil
	}
}

var channelStatusLock sync.Mutex

var channelPollingLocks sync.Map

// GetChannelPollingLock returns or creates a mutex for the given channel ID.
func GetChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// CleanupChannelPollingLocks removes locks for channels that no longer exist.
func CleanupChannelPollingLocks() {
	var activeChannelIds []int
	DB.Model(&Channel{}).Pluck("id", &activeChannelIds)

	activeChannelSet := make(map[int]bool)
	for _, id := range activeChannelIds {
		activeChannelSet[id] = true
	}

	channelPollingLocks.Range(func(key, value interface{}) bool {
		channelId := key.(int)
		if !activeChannelSet[channelId] {
			channelPollingLocks.Delete(channelId)
		}
		return true
	})
}

func handlerMultiKeyUpdate(channel *Channel, usingKey string, status int, reason string) {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		channel.Status = status
		return
	}
	var keyIndex int
	for i, key := range keys {
		if key == usingKey {
			keyIndex = i
			break
		}
	}
	if channel.ChannelInfo.MultiKeyStatusList == nil {
		channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
	}
	if status == common.ChannelStatusEnabled {
		delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
	} else {
		channel.ChannelInfo.MultiKeyStatusList[keyIndex] = status
		if channel.ChannelInfo.MultiKeyDisabledReason == nil {
			channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
		}
		if channel.ChannelInfo.MultiKeyDisabledTime == nil {
			channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
		}
		channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
		channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
	}
	if len(channel.ChannelInfo.MultiKeyStatusList) >= channel.ChannelInfo.MultiKeySize {
		channel.Status = common.ChannelStatusAutoDisabled
		info := channel.GetOtherInfo()
		info["status_reason"] = "All keys are disabled"
		info["status_time"] = common.GetTimestamp()
		channel.SetOtherInfo(info)
	}
}

func UpdateChannelStatus(channelId int, usingKey string, status int, reason string) bool {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()

		channelCache, _ := CacheGetChannel(channelId)
		if channelCache == nil {
			return false
		}
		if channelCache.ChannelInfo.IsMultiKey {
			pollingLock := GetChannelPollingLock(channelId)
			pollingLock.Lock()
			handlerMultiKeyUpdate(channelCache, usingKey, status, reason)
			pollingLock.Unlock()
		} else {
			if channelCache.Status == status {
				return false
			}
			CacheUpdateChannelStatus(channelId, status)
		}
	}

	shouldUpdateAbilities := false
	defer func() {
		if shouldUpdateAbilities {
			if err := UpdateAbilityStatus(channelId, status == common.ChannelStatusEnabled); err != nil {
				common.SysLog(fmt.Sprintf("failed to update ability status: channel_id=%d, error=%v", channelId, err))
			}
		}
	}()
	channel, err := GetChannelById(channelId, true)
	if err != nil {
		return false
	}
	if channel.Status == status {
		return false
	}

	if channel.ChannelInfo.IsMultiKey {
		beforeStatus := channel.Status
		pollingLock := GetChannelPollingLock(channelId)
		pollingLock.Lock()
		handlerMultiKeyUpdate(channel, usingKey, status, reason)
		pollingLock.Unlock()
		if beforeStatus != channel.Status {
			shouldUpdateAbilities = true
		}
	} else {
		info := channel.GetOtherInfo()
		info["status_reason"] = reason
		info["status_time"] = common.GetTimestamp()
		channel.SetOtherInfo(info)
		channel.Status = status
		shouldUpdateAbilities = true
	}
	if err = channel.SaveWithoutKey(); err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel status: channel_id=%d, status=%d, error=%v", channel.Id, status, err))
		return false
	}
	return true
}
