package openrouter_pool

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

// openRouterPoolReapTaskName is the "task" label this job stamps on
// metrics.LeaderTaskLastSuccess and registers under in taskreg.
const openRouterPoolReapTaskName = "openrouter-pool-reap"

// reaperInterval governs how often the reaper scans for expired cooldowns.
// 30s strikes a balance between recovery latency and DB churn — even with
// ~200 multi-key OpenRouter channels (well above any realistic deployment),
// a single sweep does ~1ms of work.
const reaperInterval = 30 * time.Second

// AutoReapWithContext is a master-only ticker that re-enables OpenRouter
// pool keys whose cooldowns have expired. Failures during a single tick are
// logged but never stop the loop.
//
// Caller (cmd/server/main.go) must guard with common.IsMasterNode.
func AutoReapWithContext(ctx context.Context) {
	common.SysLog("openrouter pool reaper: started, interval=" + reaperInterval.String())

	metrics.LeaderTaskLastSuccess.WithLabelValues(openRouterPoolReapTaskName).Set(0)
	taskreg.Register(openRouterPoolReapTaskName, func() time.Duration { return reaperInterval }, true, nil)

	// Run once on startup so a freshly booted master doesn't wait the full
	// interval before recovering keys whose cooldowns expired during downtime —
	// but only if this node already holds leadership at boot.
	if common.IsLeader() {
		if err := ReapOnce(ctx, time.Now); err != nil {
			common.SysLog("openrouter pool reaper initial run failed: " + err.Error())
		}
	}

	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			common.SysLog("openrouter pool reaper: stopped")
			return
		case <-ticker.C:
			// HA: only the leader reaps; non-leaders idle until they win the lease.
			if !common.IsLeader() {
				continue
			}
			if err := ReapOnce(ctx, time.Now); err != nil {
				common.SysLog("openrouter pool reaper failed: " + err.Error())
			}
		}
	}
}

// ReapOnce performs a single reaper pass. Exposed for testing with an
// injectable clock; in production AutoReapWithContext drives it.
//
// L3 heartbeat: a nil return stamps metrics.LeaderTaskLastSuccess(openrouter-pool-reap)
// via a defer, so every successful exit — including the "nothing to do"
// early return below — counts, not just the loop's tail.
func ReapOnce(ctx context.Context, now func() time.Time) (err error) {
	defer func() {
		if err == nil {
			metrics.RecordLeaderTaskSuccess(openRouterPoolReapTaskName)
		}
	}()

	channels, listErr := repo.ListOpenRouterMultiKeyChannels()
	if listErr != nil {
		err = fmt.Errorf("list channels: %w", listErr)
		return err
	}
	if len(channels) == 0 {
		return nil
	}

	totalRecovered := 0
	for _, ch := range channels {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return err
		default:
		}
		recovered, err := reapChannel(ch, now())
		if err != nil {
			common.SysLog(fmt.Sprintf("openrouter pool reaper: channel %d: %s", ch.Id, err.Error()))
			continue
		}
		if recovered > 0 {
			totalRecovered += recovered
			common.SysLog(fmt.Sprintf("openrouter pool reaper: channel %d recovered %d key(s)", ch.Id, recovered))
		}
	}
	if totalRecovered > 0 {
		common.SysLog(fmt.Sprintf("openrouter pool reaper: total recovered=%d", totalRecovered))
	}
	return nil
}

// reapChannel inspects one channel's cooldown map and re-enables expired keys.
// Returns the number of keys actually recovered. The per-channel polling lock
// is held for the brief mutation; readers (GetNextEnabledKey) won't see a
// half-updated state.
func reapChannel(channel *repo.Channel, now time.Time) (int, error) {
	if !channel.ChannelInfo.IsMultiKey {
		return 0, nil
	}
	cooldowns := channel.ChannelInfo.MultiKeyCooldownUntil
	if len(cooldowns) == 0 {
		return 0, nil
	}

	pollingLock := repo.GetChannelPollingLock(channel.Id)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	// Re-read inside the lock — another goroutine may have just written.
	cooldowns = channel.ChannelInfo.MultiKeyCooldownUntil

	nowUnix := now.Unix()
	recovered := 0
	for idx, until := range cooldowns {
		if until <= 0 || nowUnix < until {
			continue
		}
		repo.ClearMultiKeyCooldown(channel, idx)
		recovered++
	}
	if recovered == 0 {
		return 0, nil
	}

	// If channel was AutoDisabled solely because all keys were down, and now at
	// least one key is enabled again, flip the channel back to Enabled.
	channelStatusChanged := false
	if channel.Status == common.ChannelStatusAutoDisabled {
		// Count how many keys are still in StatusList (i.e., not enabled).
		stillDown := len(channel.ChannelInfo.MultiKeyStatusList)
		if stillDown < channel.ChannelInfo.MultiKeySize {
			channel.Status = common.ChannelStatusEnabled
			info := channel.GetOtherInfo()
			info["status_reason"] = "Pool keys recovered"
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			channelStatusChanged = true
		}
	}

	if err := channel.SaveWithoutKey(); err != nil {
		return recovered, fmt.Errorf("save channel: %w", err)
	}
	if channelStatusChanged {
		if err := repo.UpdateAbilityStatus(channel.Id, true); err != nil {
			return recovered, fmt.Errorf("update ability status: %w", err)
		}
	}
	return recovered, nil
}
