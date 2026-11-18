package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// ChannelTag binds tag-wide channel mutation payloads.
type ChannelTag struct {
	Tag            string  `json:"tag"`
	NewTag         *string `json:"new_tag"`
	Priority       *int64  `json:"priority"`
	Weight         *uint   `json:"weight"`
	ModelMapping   *string `json:"model_mapping"`
	Models         *string `json:"models"`
	Groups         *string `json:"groups"`
	ParamOverride  *string `json:"param_override"`
	HeaderOverride *string `json:"header_override"`
}

// DisableTagChannels disables all channels carrying the requested tag within the caller's tenant scope.
func DisableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	if err := c.ShouldBindJSON(&channelTag); err != nil || channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	var err error
	if c.GetInt("role") >= common.RoleRootUser {
		err = repo.DisableChannelByTag(channelTag.Tag)
	} else {
		err = repo.DisableChannelByTagAndTenant(c.GetString("tenant_id"), channelTag.Tag)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	tagBytes, _ := json.Marshal(map[string]string{"tag": channelTag.Tag})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionChannelTagDisabled, governance.ResourceChannel, 0, string(tagBytes)))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// EnableTagChannels enables all channels carrying the requested tag within the caller's tenant scope.
func EnableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	if err := c.ShouldBindJSON(&channelTag); err != nil || channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	var err error
	if c.GetInt("role") >= common.RoleRootUser {
		err = repo.EnableChannelByTag(channelTag.Tag)
	} else {
		err = repo.EnableChannelByTagAndTenant(c.GetString("tenant_id"), channelTag.Tag)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	refreshAfterTagEnable(c, channelTag.Tag)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// EditTagChannels validates overrides and updates all channels matching Tag within the caller's tenant scope.
func EditTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	if err := c.ShouldBindJSON(&channelTag); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	if channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "tag不能为空"})
		return
	}
	if errMsg := validateAndNormalizeTagOverrides(&channelTag); errMsg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": errMsg})
		return
	}

	if enforceChannelSensitiveWriteDecided(c, channelTag.ParamOverride != nil || channelTag.HeaderOverride != nil, 0) {
		return
	}

	var err error
	if c.GetInt("role") >= common.RoleRootUser {
		err = repo.EditChannelByTag(channelTag.Tag, channelTag.NewTag, channelTag.ModelMapping, channelTag.Models, channelTag.Groups, channelTag.Priority, channelTag.Weight, channelTag.ParamOverride, channelTag.HeaderOverride)
	} else {
		err = repo.EditChannelByTagAndTenant(c.GetString("tenant_id"), channelTag.Tag, channelTag.NewTag, channelTag.ModelMapping, channelTag.Models, channelTag.Groups, channelTag.Priority, channelTag.Weight, channelTag.ParamOverride, channelTag.HeaderOverride)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func validateAndNormalizeTagOverrides(tag *ChannelTag) string {
	if tag.ParamOverride != nil {
		trimmed := strings.TrimSpace(*tag.ParamOverride)
		if err := relaycommon.ValidateParamOverride(trimmed); err != nil {
			return "参数覆盖格式错误：" + err.Error()
		}
		tag.ParamOverride = common.GetPointer[string](trimmed)
	}
	if tag.HeaderOverride != nil {
		trimmed := strings.TrimSpace(*tag.HeaderOverride)
		if err := relaycommon.ValidateHeaderOverride(trimmed); err != nil {
			return "请求头覆盖格式错误：" + err.Error()
		}
		tag.HeaderOverride = common.GetPointer[string](trimmed)
	}
	if tag.ModelMapping != nil {
		trimmed := strings.TrimSpace(*tag.ModelMapping)
		if err := relaycommon.ValidateModelMapping(trimmed); err != nil {
			return "模型映射格式错误：" + err.Error()
		}
		tag.ModelMapping = common.GetPointer[string](trimmed)
	}
	return ""
}

// BatchSetChannelTag assigns Tag across a tenant-filtered list of channel IDs.
func BatchSetChannelTag(c *gin.Context) {
	channelBatch := ChannelBatch{}
	if err := c.ShouldBindJSON(&channelBatch); err != nil || len(channelBatch.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	if c.GetInt("role") < common.RoleRootUser {
		channelBatch.Ids = filterChannelIdsByTenant(channelBatch.Ids, c.GetString("tenant_id"))
	}
	if err := repo.BatchSetChannelTag(channelBatch.Ids, channelBatch.Tag); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": len(channelBatch.Ids)})
}

// GetTagModels returns the widest model CSV configured among channels sharing a tag within the caller's discovery scope.
func GetTagModels(c *gin.Context) {
	tag := c.Query("tag")
	if tag == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tag不能为空"})
		return
	}

	tenantID, isRoot := tenantScopeForDiscovery(c)
	var channels []*repo.Channel
	var err error
	if isRoot {
		channels, err = repo.GetChannelsByTag(tag, false, false)
	} else {
		channels, err = repo.GetChannelsByTagAndTenant(tenantID, tag, false)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	var longestModels string
	maxLength := 0
	for _, channel := range channels {
		if channel.Models != "" {
			currentModels := strings.Split(channel.Models, ",")
			if len(currentModels) > maxLength {
				maxLength = len(currentModels)
				longestModels = channel.Models
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": longestModels})
}
