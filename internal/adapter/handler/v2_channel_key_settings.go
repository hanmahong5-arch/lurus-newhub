package handler

// PUT /api/v2/:tenant_slug/channels/:id/keys/:idx/settings
//
// Per-key proxy, weight and descriptive metadata of a multi-key channel. The
// proxy overrides the channel-level proxy for requests that pick this key and
// goes through the same SSRF guard as the channel proxy.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

type keySettingsRequest struct {
	Proxy     *string   `json:"proxy"`  // "" clears the override
	Weight    *int      `json:"weight"` // 0-100, explicit 0 = never pick
	Name      *string   `json:"name"`
	PlanKind  *string   `json:"plan_kind"`
	ExpiresAt *int64    `json:"expires_at"` // Unix seconds, 0 clears
	Tags      *[]string `json:"tags"`
}

// validKeyWeight reports whether w is within 0..MaxKeyWeight.
func validKeyWeight(w int) bool { return w >= 0 && w <= entity.MaxKeyWeight }

// applyKeySettings validates req and mutates info; it returns the first
// validation error without having changed anything.
func applyKeySettings(info *entity.ChannelInfo, idx int, req keySettingsRequest) error {
	if req.Proxy != nil {
		if err := app.ValidateOutboundProxy(*req.Proxy); err != nil {
			return fmt.Errorf("key proxy rejected: %w", err)
		}
	}
	if req.Weight != nil && !validKeyWeight(*req.Weight) {
		return fmt.Errorf("weight must be between 0 and %d", entity.MaxKeyWeight)
	}
	if req.Proxy != nil {
		if info.MultiKeyProxy == nil {
			info.MultiKeyProxy = map[int]string{}
		}
		if p := strings.TrimSpace(*req.Proxy); p == "" {
			delete(info.MultiKeyProxy, idx)
		} else {
			info.MultiKeyProxy[idx] = p
		}
	}
	if req.Weight != nil {
		if info.MultiKeyWeight == nil {
			info.MultiKeyWeight = map[int]int{}
		}
		info.MultiKeyWeight[idx] = *req.Weight
	}
	if req.Name != nil || req.PlanKind != nil || req.ExpiresAt != nil || req.Tags != nil {
		if info.MultiKeyMeta == nil {
			info.MultiKeyMeta = map[int]entity.KeyMeta{}
		}
		m := info.MultiKeyMeta[idx]
		if req.Name != nil {
			m.Name = strings.TrimSpace(*req.Name)
		}
		if req.PlanKind != nil {
			m.PlanKind = strings.TrimSpace(*req.PlanKind)
		}
		if req.ExpiresAt != nil {
			m.ExpiresAt = *req.ExpiresAt
		}
		if req.Tags != nil {
			m.Tags = *req.Tags
		}
		info.MultiKeyMeta[idx] = m
	}
	return nil
}

// UpdateChannelKeySettingsV2 sets the per-key proxy/weight/metadata.
func UpdateChannelKeySettingsV2(c *gin.Context) {
	tenantCtx, ch, ok := loadStaffChannel(c)
	if !ok {
		return
	}
	if !ch.ChannelInfo.IsMultiKey {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Per-key settings need a multi-key channel"})
		return
	}
	idx, ok := parseKeyIdx(c, ch)
	if !ok {
		return
	}
	var req keySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request parameters"})
		return
	}
	if req.Proxy != nil && enforceChannelSensitiveWriteDecided(c, true, ch.Id) {
		return
	}
	if err := applyKeySettings(&ch.ChannelInfo, idx, req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := ch.SaveChannelInfo(); err != nil {
		common.SysError("save key settings: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to save key settings"})
		return
	}
	AsyncGo(func() { repo.InitChannelCache() })
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionChannelKeySettings, governance.ResourceChannel, ch.Id,
		fmt.Sprintf(`{"key_index":%d,"fingerprint":%q,"proxy_changed":%t,"weight_changed":%t}`,
			idx, keyFingerprint(channelKeyAt(ch, idx)), req.Proxy != nil, req.Weight != nil)))
	c.JSON(http.StatusOK, gin.H{"success": true})
}
