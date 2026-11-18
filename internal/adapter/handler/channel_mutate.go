package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

// ChannelBatch binds bulk channel ID arrays for batch delete and tag assignment.
type ChannelBatch struct {
	Ids []int   `json:"ids"`
	Tag *string `json:"tag"`
}

// PatchChannel binds channel update payloads with optional multi-key merge modes.
type PatchChannel struct {
	repo.Channel
	MultiKeyMode *string `json:"multi_key_mode"`
	KeyMode      *string `json:"key_mode"`
}

// DeleteChannel deletes one channel within the caller's tenant scope and records an audit event.
func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	existing, err := repo.GetChannelById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if enforceTenantScope(c, existing.TenantId) {
		return
	}
	channel := repo.Channel{Id: id}
	if err := channel.Delete(); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionChannelDeleted, governance.ResourceChannel, id, ""))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// DeleteDisabledChannel purges disabled channels globally for root or within caller tenant for tenant admins.
func DeleteDisabledChannel(c *gin.Context) {
	var rows int64
	var err error
	if c.GetInt("role") >= common.RoleRootUser {
		rows, err = repo.DeleteDisabledChannel()
	} else {
		rows, err = repo.DeleteDisabledChannelByTenant(c.GetString("tenant_id"))
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": rows})
}

// DeleteChannelBatch deletes multiple channels filtered to the caller's tenant scope.
func DeleteChannelBatch(c *gin.Context) {
	channelBatch := ChannelBatch{}
	if err := c.ShouldBindJSON(&channelBatch); err != nil || len(channelBatch.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	if c.GetInt("role") < common.RoleRootUser {
		channelBatch.Ids = filterChannelIdsByTenant(channelBatch.Ids, c.GetString("tenant_id"))
	}
	if err := repo.BatchDeleteChannels(channelBatch.Ids); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	batchBytes, _ := json.Marshal(map[string]interface{}{"count": len(channelBatch.Ids)})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionChannelBatchDeleted, governance.ResourceChannel, 0, string(batchBytes)))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": len(channelBatch.Ids)})
}

// UpdateChannel enforces tenant ownership, sensitive-write authorization, and config validation before persisting updates.
func UpdateChannel(c *gin.Context) {
	channel := PatchChannel{}
	if err := c.ShouldBindJSON(&channel); err != nil {
		common.ApiError(c, err)
		return
	}

	originChannel, err := repo.GetChannelById(channel.Id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if enforceTenantScope(c, originChannel.TenantId) {
		return
	}

	tenantReassignAttempt := ""
	if c.GetInt("role") < common.RoleRootUser {
		if channel.TenantId != "" && channel.TenantId != originChannel.TenantId {
			tenantReassignAttempt = channel.TenantId
		}
		channel.TenantId = originChannel.TenantId
	}

	if enforceChannelSensitiveWrite(c, originChannel, &channel.Channel, originChannel.Id) {
		return
	}
	if err := validateChannel(&channel.Channel, false); err != nil {
		c.JSON(http.StatusOK, legacyChannelConfigErrorResponse(err))
		return
	}

	channel.ChannelInfo = originChannel.ChannelInfo
	if channel.MultiKeyMode != nil && *channel.MultiKeyMode != "" {
		channel.ChannelInfo.MultiKeyMode = constant.MultiKeyMode(*channel.MultiKeyMode)
	}

	if errMsg := mergePatchedMultiKeys(&channel, originChannel); errMsg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": errMsg})
		return
	}

	if err := channel.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	if channel.Status == common.ChannelStatusEnabled && originChannel.Status != common.ChannelStatusEnabled {
		app.ClearChannelCooldown(channel.Id)
	}
	app.ResetProxyClientCache()

	auditDetails := ""
	if tenantReassignAttempt != "" {
		detailBytes, _ := json.Marshal(map[string]interface{}{
			"tenant_id_change_ignored": true,
			"requested_tenant_id":      tenantReassignAttempt,
			"tenant_id":                originChannel.TenantId,
		})
		auditDetails = string(detailBytes)
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionChannelUpdated, governance.ResourceChannel, channel.Id, auditDetails))
	channel.Key = ""
	clearChannelInfo(&channel.Channel)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": channel})
}

func mergePatchedMultiKeys(channel *PatchChannel, origin *repo.Channel) string {
	if channel.KeyMode == nil || !channel.ChannelInfo.IsMultiKey || *channel.KeyMode != "append" || origin.Key == "" {
		return ""
	}
	existingKeys := parseExistingChannelKeys(origin.Key)
	var newKeys []string
	if channel.Type == constant.ChannelTypeVertexAi && channel.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey {
		if strings.HasPrefix(strings.TrimSpace(channel.Key), "[") {
			array, err := getVertexArrayKeys(channel.Key)
			if err != nil {
				return "追加密钥解析失败: " + err.Error()
			}
			newKeys = array
		} else {
			newKeys = []string{channel.Key}
		}
	} else {
		for _, key := range strings.Split(channel.Key, "\n") {
			if trimmed := strings.TrimSpace(key); trimmed != "" {
				newKeys = append(newKeys, trimmed)
			}
		}
	}
	channel.Key = strings.Join(append(existingKeys, newKeys...), "\n")
	return ""
}

func parseExistingChannelKeys(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			out := make([]string, len(arr))
			for i, v := range arr {
				out[i] = string(v)
			}
			return out
		}
	}
	return strings.Split(strings.Trim(raw, "\n"), "\n")
}
