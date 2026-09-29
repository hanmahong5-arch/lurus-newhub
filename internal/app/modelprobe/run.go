package modelprobe

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// RunSummary is one RunOnce pass's tally, for logging/testing.
type RunSummary struct {
	Probed    int
	OK        int
	Failed    int
	Disabled  int
	Recovered int
}

// RunOnce runs one pass of the prober: reads the setting (a no-op when
// disabled), loads channels and existing health rows, selects the due
// pairs, probes them sequentially (SleepSeconds apart, ctx-cancellable),
// saves each result and — on a disable/recover transition — flips the
// DB-fallback ability rows so routing reacts immediately rather than
// waiting for the next cache rebuild.
func RunOnce(ctx context.Context, probe ProbeFunc, now func() time.Time) (RunSummary, error) {
	var summary RunSummary

	cfg := operation_setting.GetModelProbeSetting()
	if !cfg.Enabled {
		return summary, nil
	}

	channels, err := repo.GetAllChannels(0, 0, true, false)
	if err != nil {
		return summary, fmt.Errorf("model probe: load channels: %w", err)
	}
	health, err := repo.ListModelHealth(nil)
	if err != nil {
		return summary, fmt.Errorf("model probe: load model health: %w", err)
	}
	healthByKey := make(map[string]entity.ModelHealth, len(health))
	for _, h := range health {
		healthByKey[modelHealthKey(h.ChannelId, h.Model)] = h
	}

	due := SelectDue(channels, health, cfg, now())
	sleep := time.Duration(cfg.SleepSeconds * float64(time.Second))

	for i, pair := range due {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		var prevPtr *entity.ModelHealth
		if prev, ok := healthByKey[modelHealthKey(pair.Channel.Id, pair.Model)]; ok {
			prevPtr = &prev
		}

		res := probe(ctx, pair.Channel, pair.Model)
		summary.Probed++
		if res.OK {
			summary.OK++
		} else {
			summary.Failed++
		}

		next, transition := Apply(prevPtr, res, now(), cfg.DisableThreshold)
		next.ChannelId = pair.Channel.Id
		next.Model = pair.Model
		if err := repo.SaveModelHealth(&next); err != nil {
			common.SysError(fmt.Sprintf("model probe: save health row channel=%d model=%s failed: %s", pair.Channel.Id, pair.Model, err.Error()))
		}

		switch transition {
		case "disabled":
			summary.Disabled++
			if err := repo.SetChannelModelAbilityEnabled(pair.Channel.Id, pair.Model, false); err != nil {
				common.SysError(fmt.Sprintf("model probe: disable ability channel=%d model=%s failed: %s", pair.Channel.Id, pair.Model, err.Error()))
			}
			common.SysLog(fmt.Sprintf("model probe: auto-disabled channel=%d model=%s after %d consecutive failures: %s", pair.Channel.Id, pair.Model, next.ConsecutiveFailures, next.LastError))
		case "recovered":
			summary.Recovered++
			if err := repo.SetChannelModelAbilityEnabled(pair.Channel.Id, pair.Model, true); err != nil {
				common.SysError(fmt.Sprintf("model probe: re-enable ability channel=%d model=%s failed: %s", pair.Channel.Id, pair.Model, err.Error()))
			}
			common.SysLog(fmt.Sprintf("model probe: recovered channel=%d model=%s", pair.Channel.Id, pair.Model))
		}

		if i < len(due)-1 && sleep > 0 {
			select {
			case <-ctx.Done():
				return summary, ctx.Err()
			case <-time.After(sleep):
			}
		}
	}

	return summary, nil
}

// defaultLoopTick is the production tick interval; StartModelProber passes
// this, and tests pass a short duration of their own.
const defaultLoopTick = 30 * time.Minute

// Loop is the leader-gated background ticker: on every tick, only the
// current HA leader (common.IsLeader(), the same gate
// AutomaticallyTestChannelsWithContext uses) runs a RunOnce pass. Returns
// when ctx is done.
func Loop(ctx context.Context, probe ProbeFunc, tick time.Duration) {
	if tick <= 0 {
		tick = defaultLoopTick
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !common.IsLeader() {
				continue
			}
			if _, err := RunOnce(ctx, probe, time.Now); err != nil {
				common.SysError("model probe: run failed: " + err.Error())
			}
		}
	}
}
