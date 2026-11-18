package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

func ensureMultiKeyMapsInitialized(channel *repo.Channel) {
	if channel.ChannelInfo.MultiKeyStatusList == nil {
		channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
	}
	if channel.ChannelInfo.MultiKeyDisabledTime == nil {
		channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
	}
	if channel.ChannelInfo.MultiKeyDisabledReason == nil {
		channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
	}
}

func validateMultiKeyIndex(c *gin.Context, channel *repo.Channel, idxPtr *int, missingMsg string) (int, bool) {
	if idxPtr == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": missingMsg})
		return 0, false
	}
	idx := *idxPtr
	if idx < 0 || idx >= channel.ChannelInfo.MultiKeySize {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "密钥索引超出范围"})
		return 0, false
	}
	return idx, true
}

func handleDisableMultiKey(c *gin.Context, channel *repo.Channel, request MultiKeyManageRequest) {
	keyIndex, ok := validateMultiKeyIndex(c, channel, request.KeyIndex, "未指定要禁用的密钥索引")
	if !ok {
		return
	}
	ensureMultiKeyMapsInitialized(channel)
	channel.ChannelInfo.MultiKeyStatusList[keyIndex] = 2

	if err := channel.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "密钥已禁用"})
}

func handleEnableMultiKey(c *gin.Context, channel *repo.Channel, request MultiKeyManageRequest) {
	keyIndex, ok := validateMultiKeyIndex(c, channel, request.KeyIndex, "未指定要启用的密钥索引")
	if !ok {
		return
	}
	if channel.ChannelInfo.MultiKeyStatusList != nil {
		delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
	}
	if channel.ChannelInfo.MultiKeyDisabledTime != nil {
		delete(channel.ChannelInfo.MultiKeyDisabledTime, keyIndex)
	}
	if channel.ChannelInfo.MultiKeyDisabledReason != nil {
		delete(channel.ChannelInfo.MultiKeyDisabledReason, keyIndex)
	}

	if err := channel.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	app.ClearChannelCooldown(channel.Id)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "密钥已启用"})
}

func handleEnableAllMultiKeys(c *gin.Context, channel *repo.Channel) {
	var enabledCount int
	if channel.ChannelInfo.MultiKeyStatusList != nil {
		enabledCount = len(channel.ChannelInfo.MultiKeyStatusList)
	}
	channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
	channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
	channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)

	if err := channel.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	app.ClearChannelCooldown(channel.Id)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": fmt.Sprintf("已启用 %d 个密钥", enabledCount)})
}

func handleDisableAllMultiKeys(c *gin.Context, channel *repo.Channel) {
	ensureMultiKeyMapsInitialized(channel)
	var disabledCount int
	for i := 0; i < channel.ChannelInfo.MultiKeySize; i++ {
		status := 1
		if s, exists := channel.ChannelInfo.MultiKeyStatusList[i]; exists {
			status = s
		}
		if status == 1 {
			channel.ChannelInfo.MultiKeyStatusList[i] = 2
			disabledCount++
		}
	}
	if disabledCount == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "没有可禁用的密钥"})
		return
	}
	if err := channel.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": fmt.Sprintf("已禁用 %d 个密钥", disabledCount)})
}

func handleDeleteMultiKey(c *gin.Context, channel *repo.Channel, request MultiKeyManageRequest) {
	if enforceChannelSensitiveWrite(c, channel, &repo.Channel{Key: "channel-key-delete"}, channel.Id) {
		return
	}
	keyIndex, ok := validateMultiKeyIndex(c, channel, request.KeyIndex, "未指定要删除的密钥索引")
	if !ok {
		return
	}

	remainingKeys, newStatus, newTime, newReason, _ := compactMultiKeys(channel, func(i, _ int) bool {
		return i == keyIndex
	})
	if len(remainingKeys) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "不能删除最后一个密钥"})
		return
	}

	applyCompactedMultiKeys(channel, remainingKeys, newStatus, newTime, newReason)
	if err := channel.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "密钥已删除"})
}

func handleDeleteAutoDisabledKeys(c *gin.Context, channel *repo.Channel) {
	if enforceChannelSensitiveWrite(c, channel, &repo.Channel{Key: "channel-key-delete"}, channel.Id) {
		return
	}
	remainingKeys, newStatus, newTime, newReason, deletedCount := compactMultiKeys(channel, func(_, status int) bool {
		return status == 3
	})
	if deletedCount == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "没有需要删除的自动禁用密钥"})
		return
	}

	applyCompactedMultiKeys(channel, remainingKeys, newStatus, newTime, newReason)
	if err := channel.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("已删除 %d 个自动禁用的密钥", deletedCount),
		"data":    deletedCount,
	})
}

func compactMultiKeys(
	channel *repo.Channel,
	shouldDrop func(index int, status int) bool,
) (remaining []string, statusMap map[int]int, timeMap map[int]int64, reasonMap map[int]string, dropped int) {
	keys := channel.GetKeys()
	statusMap = make(map[int]int)
	timeMap = make(map[int]int64)
	reasonMap = make(map[int]string)

	newIndex := 0
	for i, key := range keys {
		status := 1
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			if s, exists := channel.ChannelInfo.MultiKeyStatusList[i]; exists {
				status = s
			}
		}
		if shouldDrop(i, status) {
			dropped++
			continue
		}
		remaining = append(remaining, key)
		if status != 1 {
			statusMap[newIndex] = status
		}
		if channel.ChannelInfo.MultiKeyDisabledTime != nil {
			if t, exists := channel.ChannelInfo.MultiKeyDisabledTime[i]; exists && status != 1 {
				timeMap[newIndex] = t
			}
		}
		if channel.ChannelInfo.MultiKeyDisabledReason != nil {
			if r, exists := channel.ChannelInfo.MultiKeyDisabledReason[i]; exists && status != 1 {
				reasonMap[newIndex] = r
			}
		}
		newIndex++
	}
	return remaining, statusMap, timeMap, reasonMap, dropped
}

func applyCompactedMultiKeys(
	channel *repo.Channel,
	remaining []string,
	statusMap map[int]int,
	timeMap map[int]int64,
	reasonMap map[int]string,
) {
	channel.Key = strings.Join(remaining, "\n")
	channel.ChannelInfo.MultiKeySize = len(remaining)
	channel.ChannelInfo.MultiKeyStatusList = statusMap
	channel.ChannelInfo.MultiKeyDisabledTime = timeMap
	channel.ChannelInfo.MultiKeyDisabledReason = reasonMap
}
