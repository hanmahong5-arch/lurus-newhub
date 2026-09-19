package repo

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app/hub"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/pool"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
var channelSyncLock sync.RWMutex

// InitChannelCache rebuilds the in-memory channel routing table from the
// database. A read that fails leaves the previous table in place (see
// rebuildChannelCache).
//
// It keeps its func() signature — the error rebuildChannelCache returns is
// logged here rather than propagated — because every call site is
// fire-and-forget. The boot path, the periodic sync ticker, the post-write
// channel refreshes in the v1 and v2 handlers and the abilities self-heal in
// this package (FixAbility) all call it as a bare statement
// (grep -rn 'InitChannelCache()' --include=*.go . | grep -v _test.go),
// and none of them has any recovery to run for a failed refresh: the next tick
// rebuilds. Propagating the error would add an unchecked-error lint finding at
// each of them and change nothing else. Callers that do need the reason call
// rebuildChannelCache directly.
//
// No call-site count is given here on purpose: it moves whenever a handler
// adds or drops a refresh, and a stale number in a comment misleads worse than
// no number (this comment previously claimed four sites; there were twenty).
// `grep -rn "InitChannelCache()" --include=*.go .` is today's answer.
func InitChannelCache() {
	if err := rebuildChannelCache(); err != nil {
		common.SysError("channel cache sync aborted, previous routing table kept: " + err.Error())
	}
}

// rebuildChannelCache builds a fresh routing table and swaps it in.
//
// Either read failing aborts the pass: it returns the error WITHOUT touching
// the live maps, so relay keeps routing on the last table that was built from
// a complete read. Before this, both reads discarded their error and GORM's
// empty-on-failure result slice was swapped in wholesale, which emptied that
// replica's routing table on a single failed query.
//
// The trade-off is the same shape as syncChannelCacheOnce's recover below: a
// read that keeps failing leaves the table STALE indefinitely (a channel
// disabled in the database keeps serving) rather than empty. The counter is
// what makes that visible — lurus_gateway_channel_cache_sync_failed_total,
// which the netdata alarm newhub_channel_cache_stale watches
// (deploy/r6-host-netdata/health.d/newhub.conf) and
// doc/runbook/db-pool-saturation.md documents. That alarm is added in-repo
// only; whether it has been installed onto the host is recorded in the conf
// file's own dated STATUS header, which is the truth about it, not this line.
func rebuildChannelCache() error {
	if !common.MemoryCacheEnabled {
		return nil
	}
	newChannelId2channel := make(map[int]*Channel)
	var channels []*Channel
	if err := DB.Find(&channels).Error; err != nil {
		metrics.RecordChannelCacheSyncFailed("channels")
		return fmt.Errorf("load channels for cache rebuild: %w", err)
	}
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
	}
	var abilities []*Ability
	if err := DB.Find(&abilities).Error; err != nil {
		metrics.RecordChannelCacheSyncFailed("abilities")
		return fmt.Errorf("load abilities for cache rebuild: %w", err)
	}
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			// A channel's own Group can name a group with no Ability rows
			// yet (abilities lag channel creation, or were never synced for
			// this group) — the outer map above is only pre-seeded from
			// abilities, so newGroup2model2channels[group] can still be nil
			// here. Create it lazily instead of assuming the pre-seed
			// covered every group, otherwise the write below panics
			// ("assignment to entry in nil map").
			if newGroup2model2channels[group] == nil {
				newGroup2model2channels[group] = make(map[string][]int)
			}
			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]int, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel.Id)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	// MultiKeyPollingIndex is the one field of a cached channel that relay
	// goroutines mutate in place, and they do it under the per-channel polling
	// lock (GetNextEnabledKey in channel.go). Reading it here under
	// channelSyncLock alone would be two different mutexes guarding one word.
	//
	// Snapshot it under the writer's own lock, and do that BEFORE taking
	// channelSyncLock — never while holding it. GetNextEnabledKey takes the
	// polling lock and its caller then briefly takes channelSyncLock.RLock, so
	// acquiring the pair in the other order here would invert the ordering.
	// Nothing below holds two of these locks at once.
	carriedPollingIndex := carryOverPollingIndices()

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	for _, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
				if idx, ok := carriedPollingIndex[channel.Id]; ok {
					channel.ChannelInfo.MultiKeyPollingIndex = idx
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channelSyncLock.Unlock()
	common.SysLog("channels synced from database")
	return nil
}

// carryOverPollingIndices reads the current polling position of every cached
// multi-key polling channel, each under that channel's own polling lock so the
// read is ordered against GetNextEnabledKey's write. The cache map is copied
// under a short read lock which is released before any polling lock is taken,
// so the two locks are never held simultaneously.
func carryOverPollingIndices() map[int]int {
	channelSyncLock.RLock()
	live := make([]*Channel, 0, len(channelsIDM))
	for _, channel := range channelsIDM {
		live = append(live, channel)
	}
	channelSyncLock.RUnlock()

	carried := make(map[int]int, len(live))
	for _, channel := range live {
		lock := GetChannelPollingLock(channel.Id)
		lock.Lock()
		if channel.ChannelInfo.IsMultiKey && channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
			carried[channel.Id] = channel.ChannelInfo.MultiKeyPollingIndex
		}
		lock.Unlock()
	}
	return carried
}

// initChannelCacheFn is the seam syncChannelCacheOnce calls through. Tests
// swap it to inject a panic without needing InitChannelCache itself to
// panic (e.g. exercising the nil-map class of bug above without depending
// on it staying reproducible).
var initChannelCacheFn = InitChannelCache

// syncChannelCacheOnce runs one InitChannelCache pass with its own panic
// recovery; SyncChannelCacheWithContext's ticker loop below calls it.
//
// Pre-fix, the ticker loop called InitChannelCache directly with no
// recovery of its own. The loop runs inside an errgroup.Group.Go goroutine
// (cmd/server/main.go), and errgroup deliberately does not recover a
// panicking goroutine's func (x/sync/errgroup) — so a panic there crashed
// the whole process, not just this goroutine. That crash was loud (visible
// as a pod restart in k8s), and on the next boot the startup path's own
// recover (cmd/server/main.go, around the InitChannelCache call) caught the
// same panic once and called repo.FixAbility() to try to repair the
// abilities table before giving up.
//
// Post-fix, a panic on a tick is recovered: it increments
// PanicsRecovered{source="channel_cache_sync"} and logs the stack, and the
// goroutine keeps ticking — no process crash, so no pod restart, and no
// FixAbility() self-heal runs from this loop (FixAbility only runs from the
// boot-time recover above, which now fires strictly less often since the
// process stops crashing here). A sync that keeps failing therefore leaves
// the channel routing table stale indefinitely instead of restarting the
// pod; nothing pages on the counter today (no netdata alarm exists for
// panics_recovered_total as of this change — grepped
// deploy/r6-host-netdata/health.d/*.conf, zero hits).
func syncChannelCacheOnce() {
	defer func() {
		if r := recover(); r != nil {
			metrics.RecordPanic("channel_cache_sync")
			common.SysError(fmt.Sprintf("channel cache sync panic: %v\n%s", r, debug.Stack()))
		}
	}()
	common.SysLog("syncing channels from database")
	initChannelCacheFn()
}

// SyncChannelCacheWithContext syncs channel cache with context cancellation support.
func SyncChannelCacheWithContext(ctx context.Context, frequency int) {
	ticker := time.NewTicker(time.Duration(frequency) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			common.SysLog("channel cache sync stopped")
			return
		case <-ticker.C:
			syncChannelCacheOnce()
		}
	}
}

// GetRandomSatisfiedChannel is the tenant-blind selector kept for existing
// callers (internal tooling, admin flows) that intentionally need visibility
// across every tenant's channels. It is exactly
// GetRandomSatisfiedChannelForTenant with no tenant filter applied.
func GetRandomSatisfiedChannel(group string, model string, retry int) (*Channel, error) {
	return GetRandomSatisfiedChannelForTenant("", group, model, retry)
}

// filterChannelsForTenant drops candidate channel ids owned by a tenant other
// than tenantID, keeping platform-shared channels (TenantId "" or "default")
// plus tenantID's own. tenantID == "" disables filtering entirely.
//
// An id with no entry in channelsIDM is left in rather than dropped — that is
// a cache/DB consistency error, not a tenant mismatch, and the existing
// "数据库一致性错误" detection further down the caller still needs to see it.
//
// Caller must hold channelSyncLock (read or write).
func filterChannelsForTenant(ids []int, tenantID string) []int {
	if tenantID == "" {
		return ids
	}
	filtered := make([]int, 0, len(ids))
	for _, id := range ids {
		channel, ok := channelsIDM[id]
		if !ok {
			filtered = append(filtered, id)
			continue
		}
		owner := channel.TenantId
		if owner == "" || owner == "default" || owner == tenantID {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// GetRandomSatisfiedChannelForTenant is the tenant-scoped channel selector.
//
// Policy: a channel whose TenantId is "default" or "" is platform-shared and
// may serve any tenant; a channel owned by any other tenant serves only
// callers of that tenant. tenantID == "" performs no filtering at all (see
// GetRandomSatisfiedChannel) — callers that resolve a real caller tenant must
// pass it here, not leave it empty, or this guard does nothing for them.
//
// Filtering happens BEFORE the priority/weight bucketing below runs, so a
// foreign-tenant channel can never win the weighted draw even if it would
// otherwise have the highest priority or the largest weight in the group.
func GetRandomSatisfiedChannelForTenant(tenantID string, group string, model string, retry int) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannelForTenant(group, model, retry, tenantID)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels := group2model2channels[group][model]

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels = group2model2channels[group][normalizedModel]
	}

	channels = filterChannelsForTenant(channels, tenantID)

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return channel, nil
		}
		return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channels[0])
	}

	// Use pooled map to reduce allocations in hot path
	uniquePriorities := pool.GetIntBoolMap()
	defer pool.PutIntBoolMap(uniquePriorities)

	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			uniquePriorities[int(channel.GetPriority())] = true
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}

	// Use pooled slice for sorting priorities
	sortedUniquePrioritiesPtr := pool.GetIntSlice()
	defer pool.PutIntSlice(sortedUniquePrioritiesPtr)
	sortedUniquePriorities := *sortedUniquePrioritiesPtr

	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry >= len(uniquePriorities) {
		retry = len(uniquePriorities) - 1
	}
	targetPriority := int64(sortedUniquePriorities[retry])

	// Update the pooled slice before returning
	*sortedUniquePrioritiesPtr = sortedUniquePriorities

	// get the priority for the given retry number
	var sumWeight = 0
	var targetChannels []*Channel
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.GetPriority() == targetPriority {
				sumWeight += channel.GetWeight()
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}

	if len(targetChannels) == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority))
	}

	// Hub smart routing: adjust weights based on real-time channel performance scores.
	// Collect channel IDs and weights for Hub scoring adjustment.
	channelIDs := make([]int, len(targetChannels))
	originalWeights := make([]int, len(targetChannels))
	for i, ch := range targetChannels {
		channelIDs[i] = ch.Id
		originalWeights[i] = ch.GetWeight()
	}
	adjustedWeights := hub.AdjustWeights(channelIDs, originalWeights)

	// smoothing factor and adjustment
	smoothingFactor := 1
	smoothingAdjustment := 0

	// Recalculate sumWeight if Hub adjusted the weights
	if adjustedWeights != nil {
		sumWeight = 0
		for _, w := range adjustedWeights {
			sumWeight += w
		}
	}

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = len(targetChannels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(targetChannels) < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Intn(totalWeight) // #nosec G404 — weighted channel selection; math/rand is sufficient for load-balancing.

	// Find a channel based on its weight (use Hub-adjusted weights if available)
	for i, channel := range targetChannels {
		w := channel.GetWeight()
		if adjustedWeights != nil {
			w = adjustedWeights[i]
		}
		randomWeight -= w*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel, nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel == nil {
		return
	}

	channelsIDM[channel.Id] = channel
}
