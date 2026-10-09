package app

import (
	"errors"
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/gin-gonic/gin"
)

type RetryParam struct {
	Ctx        *gin.Context
	TokenGroup string
	ModelName  string
	// TenantID scopes channel selection to platform-shared channels plus this
	// tenant's own (see repo.GetRandomSatisfiedChannelForTenant). "" performs
	// no tenant filtering — callers that have a resolved caller tenant must
	// set this, or selection stays tenant-blind for them.
	TenantID     string
	Retry        *int
	resetNextTry bool
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) SetRetry(retry int) {
	p.Retry = &retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.resetNextTry {
		p.resetNextTry = false
		return
	}
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

func (p *RetryParam) ResetRetryNextTry() {
	p.resetNextTry = true
}

// providerFilterPredicate turns the request's provider filter (set by the
// distributor from the body's `provider` object) into the repo predicate.
// A request without a filter gets nil, which is the unfiltered contract.
func providerFilterPredicate(c *gin.Context) (dto.ProviderFilter, repo.ChannelPredicate) {
	filter, _ := common.GetContextKeyType[dto.ProviderFilter](c, constant.ContextKeyProviderFilter)
	if filter.IsZero() {
		return filter, nil
	}
	return filter, func(ch *repo.Channel) bool { return filter.Matches(ch.GetSetting()) }
}

// describeProviderFilterMiss attaches the filter the caller sent to the repo's
// bare sentinel, so the wire sentence reads "no channel satisfies provider
// filter (region=eu, data_collection=deny)" and an EU customer can tell a
// region gap from a general outage. Any other error passes through.
func describeProviderFilterMiss(err error, filter dto.ProviderFilter) error {
	if errors.Is(err, repo.ErrNoChannelSatisfiesPredicate) {
		return fmt.Errorf("%w (%s)", repo.ErrNoChannelSatisfiesPredicate, filter.String())
	}
	return err
}

// CacheGetRandomSatisfiedChannel tries to get a random channel that satisfies the requirements.
// 尝试获取一个满足要求的随机渠道。
//
// For "auto" tokenGroup with cross-group Retry enabled:
// 对于启用了跨分组重试的 "auto" tokenGroup：
//
//   - Each group will exhaust all its priorities before moving to the next group.
//     每个分组会用完所有优先级后才会切换到下一个分组。
//
//   - Uses ContextKeyAutoGroupIndex to track current group index.
//     使用 ContextKeyAutoGroupIndex 跟踪当前分组索引。
//
//   - Uses ContextKeyAutoGroupRetryIndex to track the global Retry count when current group started.
//     使用 ContextKeyAutoGroupRetryIndex 跟踪当前分组开始时的全局重试次数。
//
//   - priorityRetry = Retry - startRetryIndex, represents the priority level within current group.
//     priorityRetry = Retry - startRetryIndex，表示当前分组内的优先级级别。
//
//   - When GetRandomSatisfiedChannel returns nil (priorities exhausted), moves to next group.
//     当 GetRandomSatisfiedChannel 返回 nil（优先级用完）时，切换到下一个分组。
//
// Example flow (2 groups, each with 2 priorities, RetryTimes=3):
// 示例流程（2个分组，每个有2个优先级，RetryTimes=3）：
//
//	Retry=0: GroupA, priority0 (startRetryIndex=0, priorityRetry=0)
//	         分组A, 优先级0
//
//	Retry=1: GroupA, priority1 (startRetryIndex=0, priorityRetry=1)
//	         分组A, 优先级1
//
//	Retry=2: GroupA exhausted → GroupB, priority0 (startRetryIndex=2, priorityRetry=0)
//	         分组A用完 → 分组B, 优先级0
//
//	Retry=3: GroupB, priority1 (startRetryIndex=2, priorityRetry=1)
//	         分组B, 优先级1
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*repo.Channel, string, error) {
	var channel *repo.Channel
	var err error
	selectGroup := param.TokenGroup
	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)

	// Session affinity, first attempt only. A retry means the pinned channel (or
	// another) just failed us, so the binding has lost its claim — fall straight
	// through to weighted selection and let the tail of this function re-pin
	// whatever actually works.
	// The provider filter (L8) applies to the pin too: a binding recorded by
	// an earlier, unconstrained turn must not carry a now-EU-only conversation
	// onto a US channel. A rejected pin falls through to filtered selection
	// and the deferred re-pin below moves the binding to whatever served.
	filter, filterPred := providerFilterPredicate(param.Ctx)
	// Cooling channels are as ineligible for a pinned conversation as for a
	// fresh draw, so the pin is checked against the same AND.
	refreshCooldownSnapshot()
	pred := andChannelPredicates(filterPred, cooldownPredicate(&cooldownProbe{}))
	used := usedChannelIDs(param.Ctx)
	affinityKey := common.GetContextKeyString(param.Ctx, constant.ContextKeySessionAffinity)
	if affinityKey != "" && param.GetRetry() == 0 {
		if pinned, group := lookupAffinityChannel(param, affinityKey); pinned != nil && (pred == nil || pred(pinned)) {
			return pinned, group, nil
		}
	}
	defer func() {
		// Re-pin on every successful selection, including after a failover: the
		// conversation's cache now lives on whichever channel actually served it.
		if affinityKey != "" && channel != nil && err == nil {
			affinityStore(param.Ctx, affinityKey, affinityRecord{ChannelID: channel.Id, Group: selectGroup})
		}
	}()

	if param.TokenGroup == "auto" {
		if len(setting.GetAutoGroups()) == 0 {
			return nil, selectGroup, errors.New("auto groups is not enabled")
		}
		autoGroups := GetUserAutoGroup(userGroup)

		// startGroupIndex: the group index to start searching from
		// startGroupIndex: 开始搜索的分组索引
		startGroupIndex := 0
		crossGroupRetry := common.GetContextKeyBool(param.Ctx, constant.ContextKeyTokenCrossGroupRetry)

		if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if idx, ok := lastGroupIndex.(int); ok {
				startGroupIndex = idx
			}
		}

		// A group whose every candidate the filter rejected is a miss for that
		// group but, if no later group serves either, the answer the caller
		// gets must name the filter rather than read as "model not found".
		origRetry := param.GetRetry()
		var filterMiss bool
		var coolingErr error
		// runGroups walks the auto groups once. strict excludes channels this
		// request already used with no soft fallback inside a group, so a group
		// holding only failed channels is a miss and the walk moves on to the
		// next group, where a fresh channel may exist.
		runGroups := func(strict bool) {
			filterMiss, coolingErr = false, nil
			for i := startGroupIndex; i < len(autoGroups); i++ {
				autoGroup := autoGroups[i]
				// Calculate priorityRetry for current group
				// 计算当前分组的 priorityRetry
				priorityRetry := param.GetRetry()
				// If moved to a new group, reset priorityRetry and update startRetryIndex
				// 如果切换到新分组，重置 priorityRetry 并更新 startRetryIndex
				if i > startGroupIndex {
					priorityRetry = 0
				}
				logger.LogDebug(param.Ctx, "Auto selecting group: %s, priorityRetry: %d", autoGroup, priorityRetry)

				if strict {
					channel, err = selectChannelOnce(param, autoGroup, priorityRetry, filterPred, used, true)
				} else {
					channel, err = selectChannelWhere(param, autoGroup, priorityRetry, filterPred, used)
				}
				if channel == nil {
					filterMiss = filterMiss || errors.Is(err, repo.ErrNoChannelSatisfiesPredicate)
					if errors.Is(err, ErrAllChannelsCooling) && (coolingErr == nil || CoolingRetryAfterUnix(err) < CoolingRetryAfterUnix(coolingErr)) {
						coolingErr = err
					}
					err = nil
					// Current group has no available channel for this model, try next group
					// 当前分组没有该模型的可用渠道，尝试下一个分组
					logger.LogDebug(param.Ctx, "No available channel in group %s for model %s at priorityRetry %d, trying next group", autoGroup, param.ModelName, priorityRetry)
					// 重置状态以尝试下一个分组
					common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
					common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
					// Reset retry counter so outer loop can continue for next group
					// 重置重试计数器，以便外层循环可以为下一个分组继续
					param.SetRetry(0)
					continue
				}
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
				selectGroup = autoGroup
				logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)

				// Prepare state for next retry
				// 为下一次重试准备状态
				if crossGroupRetry && priorityRetry >= common.RetryTimes {
					// Current group has exhausted all retries, prepare to switch to next group
					// This request still uses current group, but next retry will use next group
					// 当前分组已用完所有重试次数，准备切换到下一个分组
					// 本次请求仍使用当前分组，但下次重试将使用下一个分组
					logger.LogDebug(param.Ctx, "Current group %s retries exhausted (priorityRetry=%d >= RetryTimes=%d), preparing switch to next group for next retry", autoGroup, priorityRetry, common.RetryTimes)
					common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
					// Reset retry counter so outer loop can continue for next group
					// 重置重试计数器，以便外层循环可以为下一个分组继续
					param.SetRetry(0)
					param.ResetRetryNextTry()
				} else {
					// Stay in current group, save current state
					// 保持在当前分组，保存当前状态
					common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
				}
				break
			}
		}
		if len(used) > 0 {
			runGroups(true)
			if channel == nil {
				// Nothing fresh in any group: rewind the walk state the strict
				// pass advanced and allow a used channel again, as before.
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, startGroupIndex)
				param.SetRetry(origRetry)
				runGroups(false)
			}
		} else {
			runGroups(false)
		}
		// A cooling group outranks a filter miss: "retry shortly" is the
		// actionable answer, and the cooling channels did satisfy the filter.
		if channel == nil && coolingErr != nil {
			return nil, selectGroup, coolingErr
		}
		if channel == nil && filterMiss {
			err = describeProviderFilterMiss(repo.ErrNoChannelSatisfiesPredicate, filter)
			return nil, selectGroup, err
		}
	} else {
		channel, err = selectChannelWhere(param, param.TokenGroup, param.GetRetry(), filterPred, used)
		if err != nil {
			err = describeProviderFilterMiss(err, filter)
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}

// selectChannelWhere is one repo selection with the provider filter, the
// retry's used-channel exclusion and the cooldown predicate ANDed. Order: the
// filter first, so the cooldown probe only counts channels the caller could
// have been served by.
//
// Used channels are excluded softly. If excluding them leaves nothing, the
// selection runs again without the exclusion: a model served by one channel
// must still be retried on it (as it was before exclusion existed) rather
// than fail on the first transient error. Cooling is never relaxed.
func selectChannelWhere(param *RetryParam, group string, retry int, filterPred repo.ChannelPredicate, used map[int]struct{}) (*repo.Channel, error) {
	ch, err := selectChannelOnce(param, group, retry, filterPred, used, true)
	if ch == nil && len(used) > 0 && (errors.Is(err, repo.ErrNoChannelSatisfiesPredicate) || errors.Is(err, ErrAllChannelsCooling)) {
		if ch2, err2 := selectChannelOnce(param, group, retry, filterPred, used, false); ch2 != nil || err2 != nil {
			return ch2, err2
		}
	}
	return ch, err
}

// selectChannelOnce is a single repo selection. With excludeUsed the retry
// index restarts at 0: the repo applies the predicate before bucketing
// priorities, so after dropping the failed channels the remaining tiers have
// shifted up and the original index would skip the next-best tier.
//
// An empty result (nil, nil) is re-examined: multi-key channels whose every
// key is cooling are auto-disabled and so invisible to selection, and that
// must read as "all cooling" (503 + Retry-After), not as an unknown model.
func selectChannelOnce(param *RetryParam, group string, retry int, filterPred repo.ChannelPredicate, used map[int]struct{}, excludeUsed bool) (*repo.Channel, error) {
	probe := &cooldownProbe{}
	var usedPred repo.ChannelPredicate
	if excludeUsed && len(used) > 0 {
		usedPred = usedChannelPredicate(used)
		retry = 0
	}
	pred := andChannelPredicates(filterPred, usedPred, cooldownPredicate(probe))
	ch, err := repo.GetRandomSatisfiedChannelWhere(param.TenantID, group, param.ModelName, retry, pred)
	if ch == nil && err == nil {
		if until, ok := repo.EarliestMultiKeyRecovery(param.TenantID, group, param.ModelName, filterPred); ok {
			return nil, &AllChannelsCoolingError{RetryAfterUnix: until}
		}
	}
	return ch, classifySelectionMiss(err, probe)
}
