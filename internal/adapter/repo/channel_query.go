package repo

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func GetAllChannels(startIdx int, num int, selectAll bool, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	var err error
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	if selectAll {
		err = DB.Order(order).Find(&channels).Error
	} else {
		err = DB.Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	return channels, err
}

func GetChannelsByTag(tag string, idSort bool, selectAll bool) ([]*Channel, error) {
	return getChannelsByTagScoped("", tag, idSort, selectAll)
}

// GetChannelsByTagAndTenant is the tenant-scoped sibling of GetChannelsByTag.
func GetChannelsByTagAndTenant(tenantID string, tag string, idSort bool) ([]*Channel, error) {
	return getChannelsByTagScoped(tenantID, tag, idSort, false)
}

func getChannelsByTagScoped(tenantID string, tag string, idSort bool, selectAll bool) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	query := DB.Where("tag = ?", tag).Order(order)
	if tenantID != "" {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if !selectAll {
		query = query.Omit("key")
	}
	err := query.Find(&channels).Error
	return channels, err
}

// GetChannelsByTenant lists channels for a single tenant with pagination.
func GetChannelsByTenant(tenantID string, startIdx int, num int, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := DB.Where("tenant_id = ?", tenantID).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

func SearchChannels(keyword string, group string, model string, idSort bool) ([]*Channel, error) {
	return searchChannelsScoped("", keyword, group, model, idSort)
}

// SearchChannelsByTenant is the tenant-scoped sibling of SearchChannels.
func SearchChannelsByTenant(tenantID string, keyword string, group string, model string, idSort bool) ([]*Channel, error) {
	return searchChannelsScoped(tenantID, keyword, group, model, idSort)
}

func searchChannelsScoped(tenantID string, keyword string, group string, model string, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	modelsCol := `"models"`
	baseURLCol := `"base_url"`

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	baseQuery := DB.Model(&Channel{}).Omit("key")
	if tenantID != "" {
		baseQuery = baseQuery.Where("tenant_id = ?", tenantID)
	}

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

	err := baseQuery.Where(whereClause, args...).Order(order).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return channels, nil
}

func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := &Channel{Id: id}
	var err error
	if selectAll {
		err = DB.First(channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(channel, "id = ?", id).Error
	}
	if err != nil {
		return nil, err
	}
	if channel == nil {
		return nil, errors.New("channel not found")
	}
	return channel, nil
}

func GetChannelsByIds(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("id in (?)", ids).Find(&channels).Error
	return channels, err
}

// CountAllChannels returns total channels in DB.
func CountAllChannels() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Count(&total).Error
	return total, err
}

// GetChannelsByType returns channels of the specified type with pagination.
func GetChannelsByType(startIdx int, num int, idSort bool, channelType int) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := DB.Where("type = ?", channelType).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

// CountChannelsByType counts channels of a specific type.
func CountChannelsByType(channelType int) (int64, error) {
	var count int64
	err := DB.Model(&Channel{}).Where("type = ?", channelType).Count(&count).Error
	return count, err
}

// CountChannelsGroupByType returns map[type]count for all channels.
func CountChannelsGroupByType() (map[int64]int64, error) {
	type result struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var results []result
	if err := DB.Model(&Channel{}).Select("type, count(*) as count").Group("type").Find(&results).Error; err != nil {
		return nil, err
	}
	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.Type] = r.Count
	}
	return counts, nil
}
