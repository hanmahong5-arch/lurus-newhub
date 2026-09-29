package repo

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Type aliases pointing to entity package
type Ability = entity.Ability
type AbilityWithChannel = entity.AbilityWithChannel

// GetAllEnableAbilityWithChannels feeds the pricing catalogue (updatePricing).
// The join is INNER on purpose: an ability whose channel row is gone is
// routable by nobody (route-time selection resolves ownership through the same
// channel_id — abilityTenantScope below), so it must not enter the catalogue
// either. With the left join this used to be, such a row arrived with
// channel_tenant_id "" and the catalogue counted "" as platform-shared, which
// published the orphan's model name to anonymous callers — the opposite of what
// the four discovery endpoints do with the same row. The deletion paths that
// can leave one behind are DeleteDisabledChannel / DeleteDisabledChannelByTenant
// (channel.go) and Channel.Delete()'s two non-transactional deletes.
//
// coalesce still guards the column itself: a real channel row with a NULL
// tenant_id reads as "", which IS platform-shared (sharedChannelTenantIDs).
func GetAllEnableAbilityWithChannels() ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := DB.Table("abilities").
		Select("abilities.*, channels.type as channel_type, coalesce(channels.tenant_id, '') as channel_tenant_id").
		Joins("join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ?", true).
		Scan(&abilities).Error
	return abilities, err
}

// GetEnabledModelsForTenant is GetEnabledModels narrowed to the channels
// abilityTenantScope makes visible to this tenant, for the v1 admin
// discovery endpoint GET /api/channel/models_enabled. tenantID == ""
// reproduces GetEnabledModels (abilityTenantScope's own contract), so the
// caller — not this function — decides that a blank tenant means "no filter".
func GetEnabledModelsForTenant(tenantID string) []string {
	var models []string
	scope, scopeArgs := abilityTenantScope(tenantID)
	where := "enabled = ?" + scope
	args := append([]interface{}{true}, scopeArgs...)
	DB.Table("abilities").Where(where, args...).Distinct("model").Pluck("model", &models)
	return models
}

func GetGroupEnabledModels(group string) []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where(commonGroupCol+" = ? and enabled = ?", group, true).Distinct("model").Pluck("model", &models)
	return models
}

// GetGroupEnabledModelsForTenant is GetGroupEnabledModels narrowed to the
// channels abilityTenantScope makes visible to this tenant (the same scope
// route-time selection uses), so /v1/models discovery (handler/model.go)
// lists only those instead of every model any tenant's channel happens to
// serve. Route-time selection also applies channel status, priority,
// pinning and the token gate, none of which this function touches; the
// token model_limit gate and the tenant allow-list are applied by the
// caller (handler.visibleModels), not here. tenantID == "" reproduces
// GetGroupEnabledModels byte-for-byte (abilityTenantScope's own contract) —
// kept as a separate function, not a default-arg rename, because
// GetGroupEnabledModels still has its own tenant-blind caller (user.go:336).
func GetGroupEnabledModelsForTenant(group, tenantID string) []string {
	var models []string
	scope, scopeArgs := abilityTenantScope(tenantID)
	where := commonGroupCol + " = ? and enabled = ?" + scope
	args := append([]interface{}{group, true}, scopeArgs...)
	DB.Table("abilities").Where(where, args...).Distinct("model").Pluck("model", &models)
	return models
}

func GetEnabledModels() []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where("enabled = ?", true).Distinct("model").Pluck("model", &models)
	return models
}

func GetAllEnableAbilities() []Ability {
	var abilities []Ability
	DB.Find(&abilities, "enabled = ?", true)
	return abilities
}

// abilityTenantScope narrows an abilities query to channels visible to
// tenantID: platform-shared channels (channels.tenant_id set to 'default' or
// left as the empty string) plus channels owned by tenantID itself. abilities carries no tenant column
// of its own, so the restriction goes through a channel_id subquery.
//
// tenantID == "" means "no tenant filter" — this is what GetChannel's
// tenant-blind signature (kept for its existing callers) passes, and it
// reproduces the exact query this function ran before tenant scoping existed.
func abilityTenantScope(tenantID string) (string, []interface{}) {
	if tenantID == "" {
		return "", nil
	}
	return " and channel_id in (select id from channels where tenant_id in (?, ?, ?))",
		[]interface{}{"default", "", tenantID}
}

// abilityBaseCondition builds the (group, model, enabled[, tenant]) predicate
// shared by getPriority and getChannelQuery, so the two never drift apart on
// what "the same query, scoped to a retry/priority" means.
func abilityBaseCondition(group string, model string, tenantID string) (string, []interface{}) {
	scope, scopeArgs := abilityTenantScope(tenantID)
	cond := commonGroupCol + " = ? and model = ? and enabled = ?" + scope
	args := append([]interface{}{group, model, true}, scopeArgs...)
	return cond, args
}

func getPriority(group string, model string, retry int, tenantID string) (int, error) {
	cond, args := abilityBaseCondition(group, model, tenantID)

	var priorities []int
	err := DB.Model(&Ability{}).
		Select("DISTINCT(priority)").
		Where(cond, args...).
		Order("priority DESC").              // 按优先级降序排序
		Pluck("priority", &priorities).Error // Pluck用于将查询的结果直接扫描到一个切片中

	if err != nil {
		// 处理错误
		return 0, err
	}

	if len(priorities) == 0 {
		// 如果没有查询到优先级，则返回错误
		return 0, errors.New("database consistency violated: ability rows disagree with the channel table")
	}

	// 确定要使用的优先级
	var priorityToUse int
	if retry >= len(priorities) {
		// 如果重试次数大于优先级数，则使用最小的优先级
		priorityToUse = priorities[len(priorities)-1]
	} else {
		priorityToUse = priorities[retry]
	}
	return priorityToUse, nil
}

func getChannelQuery(group string, model string, retry int, tenantID string) (*gorm.DB, error) {
	baseCond, baseArgs := abilityBaseCondition(group, model, tenantID)
	maxPrioritySubQuery := DB.Model(&Ability{}).Select("MAX(priority)").Where(baseCond, baseArgs...)
	channelQuery := DB.Where(baseCond+" and priority = (?)", append(append([]interface{}{}, baseArgs...), maxPrioritySubQuery)...)
	if retry != 0 {
		priority, err := getPriority(group, model, retry, tenantID)
		if err != nil {
			return nil, err
		} else {
			channelQuery = DB.Where(baseCond+" and priority = ?", append(append([]interface{}{}, baseArgs...), priority)...)
		}
	}

	return channelQuery, nil
}

// GetChannel is the tenant-blind lookup kept for existing callers; it is
// exactly GetChannelForTenant with no tenant filter applied.
func GetChannel(group string, model string, retry int) (*Channel, error) {
	return GetChannelForTenant(group, model, retry, "")
}

// GetChannelForTenant is GetChannel's DB-fallback counterpart to
// GetRandomSatisfiedChannelForTenant (channel_cache.go): it is used when the
// in-memory cache is disabled, so the SQL query itself must do the tenant
// filtering that the memory-cache path does in Go.
func GetChannelForTenant(group string, model string, retry int, tenantID string) (*Channel, error) {
	var abilities []Ability

	channelQuery, err := getChannelQuery(group, model, retry, tenantID)
	if err != nil {
		return nil, err
	}
	err = channelQuery.Order("weight DESC").Find(&abilities).Error
	if err != nil {
		return nil, err
	}
	channel := Channel{}
	if len(abilities) > 0 {
		// Randomly choose one
		weightSum := uint(0)
		for _, ability_ := range abilities {
			weightSum += ability_.Weight + 10
		}
		// Randomly choose one
		weight := common.GetRandomInt(int(weightSum))
		for _, ability_ := range abilities {
			weight -= int(ability_.Weight) + 10
			//log.Printf("weight: %d, ability weight: %d", weight, *ability_.Weight)
			if weight <= 0 {
				channel.Id = ability_.ChannelId
				break
			}
		}
	} else {
		return nil, nil
	}
	err = DB.First(&channel, "id = ?", channel.Id).Error
	return &channel, err
}

// autoDisabledModelsForChannel returns the set of models the prober
// (internal/app/modelprobe) has auto-disabled on this channel, queried
// through db — the SAME handle the caller (AddAbilities/UpdateAbilities) is
// already using, package DB or an in-flight transaction — not the package
// global via LoadAutoDisabledModelPairs. That matters for two reasons:
// AddAbilities(tx) is called by fixtures/bootstrap code before repo.DB is
// even published (a nil package DB there would panic, not error), and a
// query inside the caller's own transaction must not read past it.
//
// Fails open (returns an empty, non-nil set and logs) on a query error — a
// channel save must not be blocked, and worse, must not error the whole
// request, just because the model_health read failed; the next probe pass
// or FixAbility resync catches up.
func autoDisabledModelsForChannel(db *gorm.DB, channelID int) map[string]bool {
	// On PostgreSQL a failed statement aborts the surrounding transaction:
	// querying a missing model_health table inside UpdateAbilities(tx) would
	// make every later INSERT in that tx fail (SQLSTATE 25P02), so "fail open"
	// must not even issue the query when the table is absent. SQLite does not
	// abort, which is why only the PG suite caught this.
	if !db.Migrator().HasTable(&entity.ModelHealth{}) {
		return map[string]bool{}
	}
	var models []string
	err := db.Model(&entity.ModelHealth{}).
		Where("channel_id = ? AND auto_disabled = ?", channelID, true).
		Pluck("model", &models).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("autoDisabledModelsForChannel(%d): query failed, failing open (treating as none auto-disabled): %s", channelID, err.Error()))
		return map[string]bool{}
	}
	out := make(map[string]bool, len(models))
	for _, m := range models {
		out[m] = true
	}
	return out
}

// SetChannelModelAbilityEnabled flips every abilities row for
// (channelID, model) — across every group the channel serves it in — to
// enabled. It is the DB-fallback routing lever the model prober
// (internal/app/modelprobe) calls on a disable/recover transition, mirroring
// what the in-memory cache rebuild (channel_cache.go) does for the
// MemoryCacheEnabled path.
//
// Enabling is guarded: it must not enable ability rows belonging to a
// channel whose own Status is not enabled — a channel an operator has
// disabled must not have the prober's recovery quietly turn traffic back on
// for it.
func SetChannelModelAbilityEnabled(channelID int, model string, enabled bool) error {
	if enabled {
		var channel Channel
		if err := DB.Select("status").First(&channel, "id = ?", channelID).Error; err != nil {
			return err
		}
		if channel.Status != common.ChannelStatusEnabled {
			return nil
		}
	}
	return DB.Model(&Ability{}).
		Where("channel_id = ? AND model = ?", channelID, model).
		Update("enabled", enabled).Error
}

func (channel *Channel) AddAbilities(tx *gorm.DB) error {
	// choose DB or provided tx — resolved up front so the auto-disabled
	// lookup below reads through the same handle the Create calls use.
	useDB := DB
	if tx != nil {
		useDB = tx
	}

	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	autoDisabled := autoDisabledModelsForChannel(useDB, channel.Id)
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled && !autoDisabled[model],
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}
	if len(abilities) == 0 {
		return nil
	}
	for _, chunk := range lo.Chunk(abilities, 50) {
		err := useDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {
	isNewTx := false
	// 如果没有传入事务，创建新的事务
	if tx == nil {
		tx = DB.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		isNewTx = true
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()
	}

	// First delete all abilities of this channel
	err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
	if err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}

	// Then add new abilities
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	autoDisabled := autoDisabledModelsForChannel(tx, channel.Id)
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled && !autoDisabled[model],
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}

	if len(abilities) > 0 {
		for _, chunk := range lo.Chunk(abilities, 50) {
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
			if err != nil {
				if isNewTx {
					tx.Rollback()
				}
				return err
			}
		}
	}

	// 如果是新创建的事务，需要提交
	if isNewTx {
		return tx.Commit().Error
	}

	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityStatusByTag(tag string, status bool) error {
	return DB.Model(&Ability{}).Where("tag = ?", tag).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityByTag(tag string, newTag *string, priority *int64, weight *uint) error {
	ability := Ability{}
	if newTag != nil {
		ability.Tag = newTag
	}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return DB.Model(&Ability{}).Where("tag = ?", tag).Updates(ability).Error
}

// UpdateAbilityByChannelIds mirrors UpdateAbilityByTag but scopes the update to a
// fixed set of channel ids. Tenant-scoped tag edits use it because the abilities
// table has no tenant_id column — channel_id is the tenant boundary.
func UpdateAbilityByChannelIds(ids []int, newTag *string, priority *int64, weight *uint) error {
	if len(ids) == 0 {
		return nil
	}
	ability := Ability{}
	if newTag != nil {
		ability.Tag = newTag
	}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return DB.Model(&Ability{}).Where("channel_id in (?)", ids).Updates(ability).Error
}

var fixLock = sync.Mutex{}

func FixAbility() (int, int, error) {
	lock := fixLock.TryLock()
	if !lock {
		return 0, 0, errors.New("a fix-abilities task is already running, try again later")
	}
	defer fixLock.Unlock()

	// truncate abilities table
	// SQLite arm exists only for the hermetic glebarez unit-test tier
	// (no TRUNCATE in SQLite); runtime is always PostgreSQL.
	if common.UsingSQLite {
		err := DB.Exec("DELETE FROM abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	} else {
		err := DB.Exec("TRUNCATE TABLE abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Truncate abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	}
	var channels []*Channel
	// Find all channels
	err := DB.Model(&Channel{}).Find(&channels).Error
	if err != nil {
		return 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, nil
	}
	successCount := 0
	failCount := 0
	for _, chunk := range lo.Chunk(channels, 50) {
		ids := lo.Map(chunk, func(c *Channel, _ int) int { return c.Id })
		// Delete all abilities of this channel
		err = DB.Where("channel_id IN ?", ids).Delete(&Ability{}).Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			failCount += len(chunk)
			continue
		}
		// Then add new abilities
		for _, channel := range chunk {
			err = channel.AddAbilities(nil)
			if err != nil {
				common.SysLog(fmt.Sprintf("Add abilities for channel %d failed: %s", channel.Id, err.Error()))
				failCount++
			} else {
				successCount++
			}
		}
	}
	InitChannelCache()
	return successCount, failCount, nil
}
