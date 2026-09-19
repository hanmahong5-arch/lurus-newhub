package repo

import (
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
)

type BoundChannel = entity.BoundChannel
type Model = entity.Model

// Re-export name rule constants from entity
const (
	NameRuleExact    = entity.NameRuleExact
	NameRulePrefix   = entity.NameRulePrefix
	NameRuleContains = entity.NameRuleContains
	NameRuleSuffix   = entity.NameRuleSuffix
)

func ModelInsert(mi *Model) error {
	now := common.GetTimestamp()
	mi.CreatedTime = now
	mi.UpdatedTime = now
	return DB.Create(mi).Error
}

func IsModelNameDuplicated(id int, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	var cnt int64
	err := DB.Model(&Model{}).Where("model_name = ? AND id <> ?", name, id).Count(&cnt).Error
	return cnt > 0, err
}

func ModelUpdate(mi *Model) error {
	mi.UpdatedTime = common.GetTimestamp()
	return DB.Session(&gorm.Session{AllowGlobalUpdate: false, FullSaveAssociations: false}).
		Model(&Model{}).
		Where("id = ?", mi.Id).
		Omit("created_time").
		Select("*").
		Updates(mi).Error
}

func ModelDelete(mi *Model) error {
	return DB.Delete(mi).Error
}

func GetVendorModelCounts() (map[int64]int64, error) {
	var stats []struct {
		VendorID int64
		Count    int64
	}
	if err := DB.Model(&Model{}).
		Select("vendor_id as vendor_id, count(*) as count").
		Group("vendor_id").
		Scan(&stats).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(stats))
	for _, s := range stats {
		m[s.VendorID] = s.Count
	}
	return m, nil
}

func GetAllModels(offset int, limit int) ([]*Model, error) {
	var models []*Model
	err := DB.Order("id DESC").Offset(offset).Limit(limit).Find(&models).Error
	return models, err
}

// GetBoundChannelsByModelsMap is the platform-operator form: every tenant's
// channel that serves the named models. Tenant-facing callers must use
// GetBoundChannelsByModelsMapForTenant — a channel NAME is customer data
// (operators name channels after the account they belong to).
func GetBoundChannelsByModelsMap(modelNames []string) (map[string][]BoundChannel, error) {
	return boundChannelsByModels(modelNames, nil)
}

// GetBoundChannelsByModelsMapForTenant is GetBoundChannelsByModelsMap
// restricted to the channels tenantID can route through: the platform-shared
// ones (sharedChannelTenantIDs) plus its own, the same union
// abilityTenantScope applies to the model names themselves. tenantID "" means
// the platform-shared channels only — never "no filter" — because this
// function has no non-tenant caller to reproduce.
func GetBoundChannelsByModelsMapForTenant(modelNames []string, tenantID string) (map[string][]BoundChannel, error) {
	visible := append([]string{}, sharedChannelTenantIDs...)
	if tenantID != "" {
		visible = append(visible, tenantID)
	}
	return boundChannelsByModels(modelNames, visible)
}

// boundChannelsByModels is the shared query; visibleTenantIDs nil means no
// tenant predicate at all.
func boundChannelsByModels(modelNames []string, visibleTenantIDs []string) (map[string][]BoundChannel, error) {
	result := make(map[string][]BoundChannel)
	if len(modelNames) == 0 {
		return result, nil
	}
	type row struct {
		Model string
		Name  string
		Type  int
	}
	var rows []row
	tx := DB.Table("channels").
		Select("abilities.model as model, channels.name as name, channels.type as type").
		Joins("JOIN abilities ON abilities.channel_id = channels.id").
		Where("abilities.model IN ? AND abilities.enabled = ?", modelNames, true)
	if visibleTenantIDs != nil {
		tx = tx.Where("channels.tenant_id IN ?", visibleTenantIDs)
	}
	err := tx.Distinct().Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.Model] = append(result[r.Model], BoundChannel{Name: r.Name, Type: r.Type})
	}
	return result, nil
}

func SearchModels(keyword string, vendor string, offset int, limit int) ([]*Model, int64, error) {
	var models []*Model
	db := DB.Model(&Model{})
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("model_name LIKE ? OR description LIKE ? OR tags LIKE ?", like, like, like)
	}
	if vendor != "" {
		if vid, err := strconv.Atoi(vendor); err == nil {
			db = db.Where("models.vendor_id = ?", vid)
		} else {
			db = db.Joins("JOIN vendors ON vendors.id = models.vendor_id").Where("vendors.name LIKE ?", "%"+vendor+"%")
		}
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("models.id DESC").Offset(offset).Limit(limit).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	return models, total, nil
}
