package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// ManageMultiKeys dispatches status queries and per-key mutations for multi-key channels under a per-channel polling lock.
func ManageMultiKeys(c *gin.Context) {
	request := MultiKeyManageRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}

	channel, err := repo.GetChannelById(request.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "渠道不存在"})
		return
	}
	if enforceTenantScope(c, channel.TenantId) {
		return
	}
	if !channel.ChannelInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "该渠道不是多密钥模式"})
		return
	}

	lock := repo.GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	switch request.Action {
	case "get_key_status":
		handleGetMultiKeyStatus(c, channel, request)
	case "disable_key":
		handleDisableMultiKey(c, channel, request)
	case "enable_key":
		handleEnableMultiKey(c, channel, request)
	case "enable_all_keys":
		handleEnableAllMultiKeys(c, channel)
	case "disable_all_keys":
		handleDisableAllMultiKeys(c, channel)
	case "delete_key":
		handleDeleteMultiKey(c, channel, request)
	case "delete_disabled_keys":
		handleDeleteAutoDisabledKeys(c, channel)
	default:
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "不支持的操作"})
	}
}

func handleGetMultiKeyStatus(c *gin.Context, channel *repo.Channel, request MultiKeyManageRequest) {
	keys := channel.GetKeys()
	page := request.Page
	pageSize := request.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	var enabledCount, manualDisabledCount, autoDisabledCount int
	allKeyStatusList := make([]KeyStatus, 0, len(keys))

	for i, key := range keys {
		status := 1
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			if s, exists := channel.ChannelInfo.MultiKeyStatusList[i]; exists {
				status = s
			}
		}
		switch status {
		case 1:
			enabledCount++
		case 2:
			manualDisabledCount++
		case 3:
			autoDisabledCount++
		}

		var disabledTime int64
		var reason string
		if status != 1 {
			if channel.ChannelInfo.MultiKeyDisabledTime != nil {
				disabledTime = channel.ChannelInfo.MultiKeyDisabledTime[i]
			}
			if channel.ChannelInfo.MultiKeyDisabledReason != nil {
				reason = channel.ChannelInfo.MultiKeyDisabledReason[i]
			}
		}

		keyPreview := key
		if len(key) > 10 {
			keyPreview = key[:10] + "..."
		}
		allKeyStatusList = append(allKeyStatusList, KeyStatus{
			Index:        i,
			Status:       status,
			DisabledTime: disabledTime,
			Reason:       reason,
			KeyPreview:   keyPreview,
		})
	}

	filtered := allKeyStatusList
	if request.Status != nil {
		filtered = make([]KeyStatus, 0, len(allKeyStatusList))
		for _, ks := range allKeyStatusList {
			if ks.Status == *request.Status {
				filtered = append(filtered, ks)
			}
		}
	}

	filteredTotal := len(filtered)
	totalPages := (filteredTotal + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if end > filteredTotal {
		end = filteredTotal
	}
	var pageKeyStatusList []KeyStatus
	if start < filteredTotal {
		pageKeyStatusList = filtered[start:end]
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": MultiKeyStatusResponse{
			Keys:                pageKeyStatusList,
			Total:               filteredTotal,
			Page:                page,
			PageSize:            pageSize,
			TotalPages:          totalPages,
			EnabledCount:        enabledCount,
			ManualDisabledCount: manualDisabledCount,
			AutoDisabledCount:   autoDisabledCount,
		},
	})
}
