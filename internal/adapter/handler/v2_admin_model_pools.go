package handler

// v2_admin_model_pools.go — GET /api/v2/admin/model-pools. Root-only pool
// overview (RootJWTAuth on the adminRoute group, same as
// analytics/model-performance beside it): one section per routing group
// (a channel in "default,free" shows up under both), each channel's
// multi-key state and, per model it serves, 24h request/error counts joined
// with the active-probe health row (entity.ModelHealth, migration 044).
//
// Never returns channel key material — only derived counts. The per-key
// status derivation mirrors GetOpenRouterApiPoolStatus (openrouter_pool.go)
// but generalises it to every channel type, not only OpenRouter's, since a
// free pool can mix vendors under one group.

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

const modelPoolsUsageWindow = 24 * time.Hour

type modelPoolHealthView struct {
	Ok                  bool   `json:"ok"`
	LastProbeAt         int64  `json:"last_probe_at"`
	LatencyMs           int    `json:"latency_ms"`
	LastError           string `json:"last_error"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	AutoDisabled        bool   `json:"auto_disabled"`
}

type modelPoolModelView struct {
	Model       string               `json:"model"`
	Requests24h int64                `json:"requests_24h"`
	Errors24h   int64                `json:"errors_24h"`
	Health      *modelPoolHealthView `json:"health"`
}

type modelPoolKeyCounts struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Cooling  int `json:"cooling"`
	Disabled int `json:"disabled"`
}

type modelPoolChannelView struct {
	Id           int                  `json:"id"`
	Name         string               `json:"name"`
	Type         int                  `json:"type"`
	Status       string               `json:"status"`
	Priority     int64                `json:"priority"`
	Weight       uint                 `json:"weight"`
	TenantId     string               `json:"tenant_id"`
	TestTime     int64                `json:"test_time"`
	ResponseTime int                  `json:"response_time"`
	Balance      float64              `json:"balance"`
	Keys         modelPoolKeyCounts   `json:"keys"`
	Models       []modelPoolModelView `json:"models"`
}

type modelPoolGroupView struct {
	Group    string                 `json:"group"`
	Channels []modelPoolChannelView `json:"channels"`
}

type modelPoolUsage struct {
	Requests int64
	Errors   int64
}

// GetModelPoolsV2 handles GET /api/v2/admin/model-pools.
func GetModelPoolsV2(c *gin.Context) {
	channels, err := repo.GetAllChannels(0, 0, true, false)
	if err != nil {
		common.SysError("GetModelPoolsV2: list channels failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to list channels",
		})
		return
	}

	health, err := loadModelPoolsHealth()
	if err != nil {
		common.SysError("GetModelPoolsV2: list model health failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to load model health",
		})
		return
	}

	usage, err := loadModelPoolsUsage()
	if err != nil {
		common.SysError("GetModelPoolsV2: aggregate usage failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to aggregate usage",
		})
		return
	}

	now := time.Now().Unix()
	groups := map[string][]*repo.Channel{}
	for _, ch := range channels {
		for _, g := range ch.GetGroups() {
			groups[g] = append(groups[g], ch)
		}
	}

	groupNames := make([]string, 0, len(groups))
	for g := range groups {
		groupNames = append(groupNames, g)
	}
	sort.Strings(groupNames)

	pools := make([]modelPoolGroupView, 0, len(groupNames))
	for _, g := range groupNames {
		chans := groups[g]
		sort.Slice(chans, func(i, j int) bool {
			pi, pj := channelPriority(chans[i]), channelPriority(chans[j])
			if pi != pj {
				return pi > pj
			}
			return chans[i].Id < chans[j].Id
		})
		channelViews := make([]modelPoolChannelView, 0, len(chans))
		for _, ch := range chans {
			channelViews = append(channelViews, buildModelPoolChannelView(ch, now, health, usage))
		}
		pools = append(pools, modelPoolGroupView{Group: g, Channels: channelViews})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"generated_at": now,
			"pools":        pools,
		},
	})
}

func channelPriority(ch *repo.Channel) int64 {
	if ch.Priority == nil {
		return 0
	}
	return *ch.Priority
}

func channelWeight(ch *repo.Channel) uint {
	if ch.Weight == nil {
		return 0
	}
	return *ch.Weight
}

// loadModelPoolsHealth indexes every probed (channel, model) pair for O(1)
// lookup while building channel views.
func loadModelPoolsHealth() (map[int]map[string]entity.ModelHealth, error) {
	rows, err := repo.ListModelHealth(nil)
	if err != nil {
		return nil, err
	}
	out := make(map[int]map[string]entity.ModelHealth, len(rows))
	for _, r := range rows {
		if out[r.ChannelId] == nil {
			out[r.ChannelId] = make(map[string]entity.ModelHealth)
		}
		out[r.ChannelId][r.Model] = r
	}
	return out, nil
}

// loadModelPoolsUsage aggregates request/error counts per (channel, model)
// over the trailing 24h window — the same consume/error predicate
// repo.GetModelPerformance uses (internal/adapter/repo/analytics.go), grouped
// by channel instead of tenant.
func loadModelPoolsUsage() (map[int]map[string]modelPoolUsage, error) {
	since := time.Now().Add(-modelPoolsUsageWindow).Unix()
	type row struct {
		ChannelId int
		ModelName string
		Requests  int64
		Errors    int64
	}
	var rows []row
	err := repo.LOG_DB.Model(&entity.Log{}).
		Select(`channel_id, model_name,
			SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS requests,
			SUM(CASE WHEN type = ? THEN 1 ELSE 0 END) AS errors`,
			entity.LogTypeConsume, entity.LogTypeError).
		Where("type IN ?", []int{entity.LogTypeConsume, entity.LogTypeError}).
		Where("created_at >= ?", since).
		Group("channel_id, model_name").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int]map[string]modelPoolUsage, len(rows))
	for _, r := range rows {
		if out[r.ChannelId] == nil {
			out[r.ChannelId] = make(map[string]modelPoolUsage)
		}
		out[r.ChannelId][r.ModelName] = modelPoolUsage{Requests: r.Requests, Errors: r.Errors}
	}
	return out, nil
}

func buildModelPoolChannelView(ch *repo.Channel, now int64, health map[int]map[string]entity.ModelHealth, usage map[int]map[string]modelPoolUsage) modelPoolChannelView {
	models := dedupeModelNames(ch.GetModels())
	chHealth := health[ch.Id]
	chUsage := usage[ch.Id]
	modelViews := make([]modelPoolModelView, 0, len(models))
	for _, m := range models {
		mv := modelPoolModelView{Model: m}
		if u, ok := chUsage[m]; ok {
			mv.Requests24h = u.Requests
			mv.Errors24h = u.Errors
		}
		if h, ok := chHealth[m]; ok {
			mv.Health = &modelPoolHealthView{
				Ok:                  h.Ok,
				LastProbeAt:         h.LastProbeAt,
				LatencyMs:           h.LatencyMs,
				LastError:           h.LastError,
				ConsecutiveFailures: h.ConsecutiveFailures,
				AutoDisabled:        h.AutoDisabled,
			}
		}
		modelViews = append(modelViews, mv)
	}

	return modelPoolChannelView{
		Id:           ch.Id,
		Name:         ch.Name,
		Type:         ch.Type,
		Status:       channelStatusLabel(ch.Status),
		Priority:     channelPriority(ch),
		Weight:       channelWeight(ch),
		TenantId:     ch.TenantId,
		TestTime:     ch.TestTime,
		ResponseTime: ch.ResponseTime,
		Balance:      ch.Balance,
		Keys:         countChannelKeys(ch, now),
		Models:       modelViews,
	}
}

// countChannelKeys mirrors GetOpenRouterApiPoolStatus's per-key status
// derivation (openrouter_pool.go): a key whose status is not enabled but
// still carries a cooldown entry is "cooling", even once the deadline has
// passed — the pool reaper re-enables it on its next tick, so counting it
// as disabled would ask an operator to fix something that fixes itself.
// A single-key (non-multi-key) channel counts as one enabled key.
func countChannelKeys(ch *repo.Channel, now int64) modelPoolKeyCounts {
	keys := ch.GetKeys()
	counts := modelPoolKeyCounts{Total: len(keys)}
	if !ch.ChannelInfo.IsMultiKey {
		if len(keys) > 0 {
			counts.Enabled = len(keys)
		}
		return counts
	}
	statusList := ch.ChannelInfo.MultiKeyStatusList
	cooldowns := ch.ChannelInfo.MultiKeyCooldownUntil
	for i := range keys {
		rawStatus, hasStatus := statusList[i]
		if !hasStatus || rawStatus == common.ChannelStatusEnabled {
			counts.Enabled++
			continue
		}
		if until, hasCooldown := cooldowns[i]; hasCooldown && until > 0 {
			counts.Cooling++
			continue
		}
		counts.Disabled++
	}
	return counts
}

func dedupeModelNames(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}
