package repo

import (
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func EnableChannelByTag(tag string) error {
	if err := DB.Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusEnabled).Error; err != nil {
		return err
	}
	return UpdateAbilityStatusByTag(tag, true)
}

func DisableChannelByTag(tag string) error {
	if err := DB.Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
		return err
	}
	return UpdateAbilityStatusByTag(tag, false)
}

// EnableChannelByTagAndTenant is the tenant-scoped sibling of EnableChannelByTag.
func EnableChannelByTagAndTenant(tenantID string, tag string) error {
	return setChannelStatusByTagAndTenant(tenantID, tag, common.ChannelStatusEnabled, true)
}

// DisableChannelByTagAndTenant is the tenant-scoped sibling of DisableChannelByTag.
func DisableChannelByTagAndTenant(tenantID string, tag string) error {
	return setChannelStatusByTagAndTenant(tenantID, tag, common.ChannelStatusManuallyDisabled, false)
}

func setChannelStatusByTagAndTenant(tenantID string, tag string, status int, abilityEnabled bool) error {
	var ids []int
	if err := DB.Model(&Channel{}).Where("tag = ? AND tenant_id = ?", tag, tenantID).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if err := DB.Model(&Channel{}).Where("id in (?)", ids).Update("status", status).Error; err != nil {
		return err
	}
	return DB.Model(&Ability{}).Where("channel_id in (?)", ids).Select("enabled").Update("enabled", abilityEnabled).Error
}

func buildTagEditUpdate(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) (Channel, string, bool) {
	updateData := Channel{}
	shouldReCreateAbilities := false
	updatedTag := tag
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
		updatedTag = *newTag
	}
	if modelMapping != nil && *modelMapping != "" {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldReCreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		shouldReCreateAbilities = true
		updateData.Group = *group
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}
	if paramOverride != nil {
		updateData.ParamOverride = paramOverride
	}
	if headerOverride != nil {
		updateData.HeaderOverride = headerOverride
	}
	return updateData, updatedTag, shouldReCreateAbilities
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	updateData, updatedTag, shouldReCreateAbilities := buildTagEditUpdate(tag, newTag, modelMapping, models, group, priority, weight, paramOverride, headerOverride)

	if err := DB.Model(&Channel{}).Where("tag = ?", tag).Updates(updateData).Error; err != nil {
		return err
	}
	if shouldReCreateAbilities {
		channels, err := GetChannelsByTag(updatedTag, false, false)
		if err == nil {
			for _, channel := range channels {
				if err = channel.UpdateAbilities(nil); err != nil {
					common.SysLog(fmt.Sprintf("failed to update abilities: channel_id=%d, tag=%s, error=%v", channel.Id, channel.GetTag(), err))
				}
			}
		}
		return nil
	}
	return UpdateAbilityByTag(tag, newTag, priority, weight)
}

// EditChannelByTagAndTenant is the tenant-scoped sibling of EditChannelByTag.
func EditChannelByTagAndTenant(tenantID string, tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	var ids []int
	if err := DB.Model(&Channel{}).Where("tag = ? AND tenant_id = ?", tag, tenantID).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	updateData, updatedTag, shouldReCreateAbilities := buildTagEditUpdate(tag, newTag, modelMapping, models, group, priority, weight, paramOverride, headerOverride)

	if err := DB.Model(&Channel{}).Where("id in (?)", ids).Updates(updateData).Error; err != nil {
		return err
	}
	if shouldReCreateAbilities {
		channels, err := GetChannelsByTagAndTenant(tenantID, updatedTag, false)
		if err == nil {
			for _, channel := range channels {
				if err = channel.UpdateAbilities(nil); err != nil {
					common.SysLog(fmt.Sprintf("failed to update abilities: channel_id=%d, tag=%s, error=%v", channel.Id, channel.GetTag(), err))
				}
			}
		}
		return nil
	}
	return UpdateAbilityByChannelIds(ids, newTag, priority, weight)
}

func GetPaginatedTags(offset int, limit int) ([]*string, error) {
	var tags []*string
	err := DB.Model(&Channel{}).Select("DISTINCT tag").Where("tag != ''").Offset(offset).Limit(limit).Find(&tags).Error
	return tags, err
}

// GetPaginatedTagsByTenant is GetPaginatedTags restricted to one tenant's channels.
func GetPaginatedTagsByTenant(tenantID string, offset int, limit int) ([]*string, error) {
	var tags []*string
	err := DB.Model(&Channel{}).Select("DISTINCT tag").
		Where("tag != '' AND tenant_id = ?", tenantID).
		Offset(offset).Limit(limit).Find(&tags).Error
	return tags, err
}

func SearchTags(keyword string, group string, model string, idSort bool) ([]*string, error) {
	var tags []*string
	modelsCol := `"models"`
	baseURLCol := `"base_url"`

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	baseQuery := DB.Model(&Channel{}).Omit("key")

	var whereClause string
	var args []interface{}
	if group != "" && group != "null" {
		groupCondition := `(',' || ` + commonGroupCol + ` || ',') LIKE ?`
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + ` LIKE ? AND ` + groupCondition
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%", "%,"+group+",%")
	} else {
		whereClause = "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyword, "%"+keyword+"%", "%"+model+"%")
	}

	subQuery := baseQuery.Where(whereClause, args...).
		Select("tag").
		Where("tag != ''").
		Order(order)

	err := DB.Table("(?) as sub", subQuery).
		Select("DISTINCT tag").
		Find(&tags).Error
	if err != nil {
		return nil, err
	}
	return tags, nil
}

func BatchSetChannelTag(ids []int, tag *string) error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	if err := tx.Model(&Channel{}).Where("id in (?)", ids).Update("tag", tag).Error; err != nil {
		tx.Rollback()
		return err
	}

	var channels []*Channel
	if err := tx.Where("id in (?)", ids).Find(&channels).Error; err != nil {
		tx.Rollback()
		return err
	}

	for _, channel := range channels {
		if err := channel.UpdateAbilities(tx); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit().Error
}

// CountAllTags returns number of non-empty distinct tags.
func CountAllTags() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Where("tag is not null AND tag != ''").Distinct("tag").Count(&total).Error
	return total, err
}

// CountAllTagsByTenant is CountAllTags for one tenant's channels.
func CountAllTagsByTenant(tenantID string) (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).
		Where("tag is not null AND tag != '' AND tenant_id = ?", tenantID).
		Distinct("tag").Count(&total).Error
	return total, err
}
