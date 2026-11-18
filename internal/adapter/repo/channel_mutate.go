package repo

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateChannelModels updates only the models field for a given channel.
func UpdateChannelModels(channelId int, models string) error {
	return DB.Model(&Channel{}).Where("id = ?", channelId).Update("models", models).Error
}

// GetChannelForUpdate fetches a channel within a transaction with row-level lock.
func GetChannelForUpdate(tx *gorm.DB, channelId int) (*Channel, error) {
	var c Channel
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", channelId).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateChannelSyncState atomically updates the fields touched by the OpenRouter sync engine.
func UpdateChannelSyncState(tx *gorm.DB, channelId int, modelsCSV string, managedJSON string, lastFetchCount int) error {
	return tx.Model(&Channel{}).Where("id = ?", channelId).Updates(map[string]interface{}{
		"models":                 modelsCSV,
		"managed_models_by_sync": managedJSON,
		"last_sync_fetch_count":  lastFetchCount,
	}).Error
}

func BatchInsertChannels(channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	tx := WithTenantID(DB, channels[0].TenantId).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for _, chunk := range lo.Chunk(channels, 50) {
		if err := tx.Create(&chunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for _, channel_ := range chunk {
			if err := channel_.AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit().Error
}

func BatchDeleteChannels(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	for _, chunk := range lo.Chunk(ids, 200) {
		if err := tx.Where("id in (?)", chunk).Delete(&Channel{}).Error; err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Where("channel_id in (?)", chunk).Delete(&Ability{}).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

func (channel *Channel) Insert() error {
	if err := WithTenantID(DB, channel.TenantId).Create(channel).Error; err != nil {
		return err
	}
	return channel.AddAbilities(nil)
}

func (channel *Channel) Update() error {
	if channel.ChannelInfo.IsMultiKey {
		var keyStr string
		if channel.Key != "" {
			keyStr = channel.Key
		} else if existing, err := GetChannelById(channel.Id, true); err == nil {
			keyStr = existing.Key
		}
		keys := []string{}
		if keyStr != "" {
			trimmed := strings.TrimSpace(keyStr)
			if strings.HasPrefix(trimmed, "[") {
				var arr []json.RawMessage
				if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
					keys = make([]string, len(arr))
					for i, v := range arr {
						keys[i] = string(v)
					}
				}
			}
			if len(keys) == 0 {
				keys = strings.Split(strings.Trim(keyStr, "\n"), "\n")
			}
		}
		channel.ChannelInfo.MultiKeySize = len(keys)
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			for idx := range channel.ChannelInfo.MultiKeyStatusList {
				if idx >= channel.ChannelInfo.MultiKeySize {
					delete(channel.ChannelInfo.MultiKeyStatusList, idx)
				}
			}
		}
	}
	if err := DB.Model(channel).Updates(channel).Error; err != nil {
		return err
	}
	DB.Model(channel).First(channel, "id = ?", channel.Id)
	return channel.UpdateAbilities(nil)
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     common.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update response time: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: common.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update balance: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) Delete() error {
	if err := DB.Delete(channel).Error; err != nil {
		return err
	}
	return channel.DeleteAbilities()
}

func UpdateChannelUsedQuota(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
		return
	}
	updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int) {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel used quota: channel_id=%d, delta_quota=%d, error=%v", id, quota, err))
	}
}

func DeleteChannelByStatus(status int64) (int64, error) {
	result := DB.Where("status = ?", status).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

func DeleteDisabledChannel() (int64, error) {
	result := DB.Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

// DeleteDisabledChannelByTenant is the tenant-scoped sibling of DeleteDisabledChannel.
func DeleteDisabledChannelByTenant(tenantID string) (int64, error) {
	result := DB.Where("(status = ? or status = ?) AND tenant_id = ?",
		common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled, tenantID).Delete(&Channel{})
	return result.RowsAffected, result.Error
}
