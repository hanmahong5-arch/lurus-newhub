package handler

import (
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// GetOpenRouterApiPoolStatus handles GET /api/openrouter-sync/api-pool.
// Returns a snapshot of every multi-key OpenRouter channel's per-key state
// for admin monitoring. Key strings are masked (prefix only) — never returned
// in full. The route is AdminAuth-gated today (role >= admin, not
// necessarily root — W hand-off raises it to RootAuth), so a tenant admin can
// reach this handler; the scope below keeps that caller to their own
// tenant's channels the same way handler.DeleteHistoryLogs and
// handler.GetAllLogs already scope their reads (cycle 13, V1DOORS/SECURITY-14).
func GetOpenRouterApiPoolStatus(c *gin.Context) {
	scope := repo.AllTenantsForAdmin()
	if c.GetInt("role") < common.RoleRootUser {
		scope = repo.ForTenant(c.GetString("tenant_id"))
	}
	channels, err := repo.ListOpenRouterMultiKeyChannelsForScope(scope)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	now := time.Now().Unix()
	type keyStatus struct {
		Index                    int    `json:"index"`
		KeyPrefix                string `json:"key_prefix"`
		Status                   string `json:"status"` // "enabled" | "cooling" | "permanent_disabled"
		CooldownUntil            int64  `json:"cooldown_until,omitempty"`
		CooldownSecondsRemaining int64  `json:"cooldown_seconds_remaining,omitempty"`
		DisableReason            string `json:"disable_reason,omitempty"`
		LastDisabledAt           int64  `json:"last_disabled_at,omitempty"`
	}
	type channelSnapshot struct {
		ChannelId   int         `json:"channel_id"`
		ChannelName string      `json:"channel_name"`
		Status      string      `json:"status"` // "enabled" | "auto_disabled" | "manually_disabled"
		KeyCount    int         `json:"key_count"`
		EnabledN    int         `json:"enabled_count"`
		CoolingN    int         `json:"cooling_count"`
		DisabledN   int         `json:"permanent_disabled_count"`
		Keys        []keyStatus `json:"keys"`
	}

	out := make([]channelSnapshot, 0, len(channels))
	for _, ch := range channels {
		keys := ch.GetKeys()
		snap := channelSnapshot{
			ChannelId:   ch.Id,
			ChannelName: ch.Name,
			Status:      channelStatusLabel(ch.Status),
			KeyCount:    len(keys),
			Keys:        make([]keyStatus, 0, len(keys)),
		}
		statusList := ch.ChannelInfo.MultiKeyStatusList
		reasonMap := ch.ChannelInfo.MultiKeyDisabledReason
		timeMap := ch.ChannelInfo.MultiKeyDisabledTime
		cooldowns := ch.ChannelInfo.MultiKeyCooldownUntil

		for i, k := range keys {
			ks := keyStatus{Index: i, KeyPrefix: maskKeyPrefix(k)}
			rawStatus, hasStatus := statusList[i]
			if !hasStatus || rawStatus == common.ChannelStatusEnabled {
				ks.Status = "enabled"
				snap.EnabledN++
			} else if until, hasCooldown := cooldowns[i]; hasCooldown && until > 0 {
				ks.Status = "cooling"
				ks.CooldownUntil = until
				ks.CooldownSecondsRemaining = until - now
				if ks.CooldownSecondsRemaining < 0 {
					ks.CooldownSecondsRemaining = 0
				}
				snap.CoolingN++
			} else {
				ks.Status = "permanent_disabled"
				snap.DisabledN++
			}
			if r, ok := reasonMap[i]; ok {
				ks.DisableReason = r
			}
			if t, ok := timeMap[i]; ok {
				ks.LastDisabledAt = t
			}
			snap.Keys = append(snap.Keys, ks)
		}
		out = append(out, snap)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// maskKeyPrefix returns a safe representation: first 12 chars + "***".
// OpenRouter keys are typically `sk-or-v1-<32 hex>`, so the prefix is enough
// to disambiguate without exposing usable credentials.
func maskKeyPrefix(key string) string {
	const visible = 12
	if len(key) <= visible {
		return key + "***"
	}
	return key[:visible] + "***"
}

func channelStatusLabel(status int) string {
	switch status {
	case common.ChannelStatusEnabled:
		return "enabled"
	case common.ChannelStatusAutoDisabled:
		return "auto_disabled"
	case common.ChannelStatusManuallyDisabled:
		return "manually_disabled"
	default:
		return "unknown"
	}
}
