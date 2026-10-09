package handler

// GET /api/v2/:tenant_slug/channels/:id/health
//
// One read-only view that answers "why is this account not taking traffic".
// It adds no storage: it merges the channel row (status, per-key status /
// disabled reason / cooldown deadline), the live cooldown table and the
// operator-set key metadata.

import (
	"net/http"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// Reasons a channel or key is not routable.
const (
	ReasonDisabledManual    = "disabled_manual"
	ReasonAutoDisabled      = "auto_disabled"
	ReasonCooling429        = "cooling_429"
	ReasonPlanWindowExhaust = "plan_window_exhausted"
	ReasonExpired           = "expired"
	ReasonBalanceLow        = "balance_low"
	ReasonAuthFailed        = "auth_failed"
)

// ChannelWindowProvider lets the plan-window lane plug its quota snapshot in:
// it returns the window description for one key or nil when none is known.
// Unset (nil) means every window field is empty.
var ChannelWindowProvider func(channelID, keyIdx int) map[string]interface{}

type keyHealth struct {
	Index         int                    `json:"index"`
	Name          string                 `json:"name,omitempty"`
	Fingerprint   string                 `json:"fingerprint"`
	Routable      bool                   `json:"routable"`
	Reasons       []string               `json:"reasons"`
	CooldownUntil int64                  `json:"cooldown_until,omitempty"`
	Window        map[string]interface{} `json:"window"`
	LastError     string                 `json:"last_error,omitempty"`
	LastSuccessAt *int64                 `json:"last_success_at"`
	Weight        *int                   `json:"weight,omitempty"`
	HasProxy      bool                   `json:"has_proxy,omitempty"`
}

type channelHealth struct {
	ChannelID     int                    `json:"channel_id"`
	Routable      bool                   `json:"routable"`
	Reasons       []string               `json:"reasons"`
	CooldownUntil int64                  `json:"cooldown_until,omitempty"`
	Window        map[string]interface{} `json:"window"`
	LastError     string                 `json:"last_error,omitempty"`
	LastSuccessAt *int64                 `json:"last_success_at"`
	Keys          []keyHealth            `json:"keys"`
}

// classifyDisabledReason maps the free-text reason stored when a key or
// channel was switched off to one of the stable reason codes.
func classifyDisabledReason(reason string) string {
	r := strings.ToLower(reason)
	switch {
	case strings.HasPrefix(r, "quota_window") || strings.Contains(r, "window exhausted"):
		return ReasonPlanWindowExhaust
	case strings.HasPrefix(r, "balance_low") || strings.Contains(r, "insufficient") ||
		strings.Contains(r, "balance") || strings.Contains(r, "quota exceeded") || strings.Contains(r, "quota_exceeded"):
		return ReasonBalanceLow
	case strings.Contains(r, "expire"):
		return ReasonExpired
	case strings.Contains(r, "401") || strings.Contains(r, "unauthorized") ||
		strings.Contains(r, "invalid api key") || strings.Contains(r, "invalid_api_key") ||
		strings.Contains(r, "authentication") || strings.Contains(r, "incorrect api key"):
		return ReasonAuthFailed
	}
	return ReasonAutoDisabled
}

func addReason(rs []string, r string) []string {
	for _, e := range rs {
		if e == r {
			return rs
		}
	}
	return append(rs, r)
}

// buildKeyHealth evaluates one key at instant now (Unix seconds).
func buildKeyHealth(ch *repo.Channel, idx int, now int64) keyHealth {
	st := snapshotKeyState(ch, idx)
	kh := keyHealth{
		Index:       idx,
		Fingerprint: keyFingerprint(channelKeyAt(ch, idx)),
		Reasons:     []string{},
	}
	meta := ch.ChannelInfo.MultiKeyMeta[idx]
	kh.Name = meta.Name
	if ch.ChannelInfo.IsMultiKey {
		kh.HasProxy = ch.ChannelInfo.KeyProxy(idx) != ""
		if w, ok := ch.ChannelInfo.MultiKeyWeight[idx]; ok || ch.ChannelInfo.MultiKeyMode == constant.MultiKeyModeWeighted {
			if !ok {
				w = ch.ChannelInfo.KeyWeight(idx)
			}
			kh.Weight = &w
		}
	}
	cooling := st.CooldownUntil > now
	if cooling {
		kh.CooldownUntil = st.CooldownUntil
	}
	// A channel-level switch-off is a reason for every key behind it, but only
	// for a multi-key channel (a single-key channel's key status IS the channel status).
	if ch.ChannelInfo.IsMultiKey && st.ChannelStatus != common.ChannelStatusEnabled {
		kh.Reasons = addReason(kh.Reasons, channelStatusReason(st.ChannelStatus))
	}
	switch st.KeyStatus {
	case common.ChannelStatusManuallyDisabled:
		kh.Reasons = addReason(kh.Reasons, ReasonDisabledManual)
	case common.ChannelStatusAutoDisabled:
		if cooling {
			kh.Reasons = addReason(kh.Reasons, ReasonCooling429)
		} else {
			kh.Reasons = addReason(kh.Reasons, classifyDisabledReason(st.Reason))
		}
		kh.LastError = truncateText(st.Reason, 200)
	default:
		if cooling {
			kh.Reasons = addReason(kh.Reasons, ReasonCooling429)
		}
	}
	if meta.ExpiresAt > 0 && meta.ExpiresAt <= now {
		kh.Reasons = addReason(kh.Reasons, ReasonExpired)
	}
	if ch.ChannelInfo.MultiKeyMode == constant.MultiKeyModeWeighted && ch.ChannelInfo.KeyWeight(idx) <= 0 {
		kh.Reasons = addReason(kh.Reasons, ReasonDisabledManual)
	}
	if ChannelWindowProvider != nil {
		kh.Window = ChannelWindowProvider(ch.Id, idx)
	}
	kh.Routable = len(kh.Reasons) == 0
	return kh
}

func channelStatusReason(status int) string {
	if status == common.ChannelStatusManuallyDisabled {
		return ReasonDisabledManual
	}
	return ReasonAutoDisabled
}

// buildChannelHealth aggregates the channel and all of its keys.
func buildChannelHealth(ch *repo.Channel, now int64) channelHealth {
	h := channelHealth{ChannelID: ch.Id, Reasons: []string{}, Keys: []keyHealth{}}
	n := channelKeyCount(ch)
	anyRoutable := false
	for i := 0; i < n; i++ {
		kh := buildKeyHealth(ch, i, now)
		h.Keys = append(h.Keys, kh)
		anyRoutable = anyRoutable || kh.Routable
		if kh.LastError != "" && h.LastError == "" {
			h.LastError = kh.LastError
		}
	}
	if ch.Status != common.ChannelStatusEnabled {
		h.Reasons = addReason(h.Reasons, channelStatusReason(ch.Status))
		if ch.Status == common.ChannelStatusAutoDisabled && !ch.ChannelInfo.IsMultiKey && len(h.Keys) == 1 {
			// For a single-key channel the key reason carries the specific cause.
			h.Reasons = h.Keys[0].Reasons
		}
	}
	if until := app.ChannelCoolingUntil(ch.Id, 0); until > now {
		h.CooldownUntil = until
		h.Reasons = addReason(h.Reasons, ReasonCooling429)
	}
	if !anyRoutable {
		for _, kh := range h.Keys {
			for _, r := range kh.Reasons {
				h.Reasons = addReason(h.Reasons, r)
			}
			if kh.CooldownUntil > 0 && (h.CooldownUntil == 0 || kh.CooldownUntil < h.CooldownUntil) {
				h.CooldownUntil = kh.CooldownUntil
			}
		}
	}
	h.Routable = ch.Status == common.ChannelStatusEnabled && anyRoutable && h.CooldownUntil <= now
	if h.Routable {
		h.Reasons = []string{}
	}
	if ChannelWindowProvider != nil {
		h.Window = ChannelWindowProvider(ch.Id, -1)
	}
	return h
}

// GetChannelHealthV2 serves the aggregated health view.
//
// Route: GET /api/v2/:tenant_slug/channels/:id/health
func GetChannelHealthV2(c *gin.Context) {
	_, ch, ok := loadStaffChannel(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": buildChannelHealth(ch, time.Now().Unix())})
}
