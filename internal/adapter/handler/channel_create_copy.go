package handler

import (
	"fmt"
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

// AddChannelRequest binds single, batch, or multi-to-single channel creation payloads.
type AddChannelRequest struct {
	Mode                      string                `json:"mode"`
	MultiKeyMode              constant.MultiKeyMode `json:"multi_key_mode"`
	BatchAddSetKeyPrefix2Name bool                  `json:"batch_add_set_key_prefix_2_name"`
	Channel                   *repo.Channel         `json:"channel"`
}

// AddChannel validates, authorizes, and inserts one or more upstream channels.
func AddChannel(c *gin.Context) {
	addChannelRequest := AddChannelRequest{}
	if err := c.ShouldBindJSON(&addChannelRequest); err != nil {
		common.ApiError(c, err)
		return
	}
	if addChannelRequest.Channel == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "channel cannot be empty"})
		return
	}
	if err := validateChannel(addChannelRequest.Channel, true); err != nil {
		c.JSON(http.StatusOK, legacyChannelConfigErrorResponse(err))
		return
	}
	if enforceChannelSensitiveWrite(c, nil, addChannelRequest.Channel, 0) {
		return
	}

	addChannelRequest.Channel.CreatedTime = common.GetTimestamp()
	keys, errMsg := resolveAddChannelKeys(&addChannelRequest)
	if errMsg != "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": errMsg})
		return
	}

	tenantID := app.GetTenantIdFromContext(common.GetContextKeyString(c, "tenant_id"))
	channels := make([]repo.Channel, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		localChannel := *addChannelRequest.Channel
		localChannel.Key = key
		localChannel.TenantId = tenantID
		if addChannelRequest.BatchAddSetKeyPrefix2Name && len(keys) > 1 {
			keyPrefix := localChannel.Key
			if len(localChannel.Key) > 8 {
				keyPrefix = localChannel.Key[:8]
			}
			localChannel.Name = fmt.Sprintf("%s %s", localChannel.Name, keyPrefix)
		}
		channels = append(channels, localChannel)
	}
	if err := repo.BatchInsertChannels(channels); err != nil {
		common.ApiError(c, err)
		return
	}
	app.ResetProxyClientCache()
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionChannelCreated, governance.ResourceChannel, 0,
		fmt.Sprintf(`{"mode":%q,"count":%d,"type":%d}`,
			addChannelRequest.Mode, len(channels), addChannelRequest.Channel.Type)))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func resolveAddChannelKeys(req *AddChannelRequest) ([]string, string) {
	isVertexJSON := req.Channel.Type == constant.ChannelTypeVertexAi &&
		req.Channel.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey

	switch req.Mode {
	case "multi_to_single":
		req.Channel.ChannelInfo.IsMultiKey = true
		req.Channel.ChannelInfo.MultiKeyMode = req.MultiKeyMode
		if isVertexJSON {
			array, err := getVertexArrayKeys(req.Channel.Key)
			if err != nil {
				return nil, err.Error()
			}
			req.Channel.ChannelInfo.MultiKeySize = len(array)
			req.Channel.Key = strings.Join(array, "\n")
		} else {
			cleanKeys := make([]string, 0)
			for _, key := range strings.Split(req.Channel.Key, "\n") {
				if trimmed := strings.TrimSpace(key); trimmed != "" {
					cleanKeys = append(cleanKeys, trimmed)
				}
			}
			req.Channel.ChannelInfo.MultiKeySize = len(cleanKeys)
			req.Channel.Key = strings.Join(cleanKeys, "\n")
		}
		return []string{req.Channel.Key}, ""
	case "batch":
		if isVertexJSON {
			keys, err := getVertexArrayKeys(req.Channel.Key)
			if err != nil {
				return nil, err.Error()
			}
			return keys, ""
		}
		return strings.Split(req.Channel.Key, "\n"), ""
	case "single":
		return []string{req.Channel.Key}, ""
	default:
		return nil, "不支持的添加模式"
	}
}

// CopyChannel clones an existing channel within the caller's tenant scope.
func CopyChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}

	suffix := c.DefaultQuery("suffix", "_复制")
	resetBalance := true
	if rbStr := c.DefaultQuery("reset_balance", "true"); rbStr != "" {
		if v, pErr := strconv.ParseBool(rbStr); pErr == nil {
			resetBalance = v
		}
	}

	origin, err := repo.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if enforceTenantScope(c, origin.TenantId) {
		return
	}

	clone := *origin
	clone.Id = 0
	clone.CreatedTime = common.GetTimestamp()
	clone.Name = origin.Name + suffix
	clone.TestTime = 0
	clone.ResponseTime = 0
	if resetBalance {
		clone.Balance = 0
		clone.UsedQuota = 0
	}

	if enforceChannelSensitiveWrite(c, nil, &clone, 0) {
		return
	}
	if err := clone.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	repo.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"id": clone.Id}})
}
