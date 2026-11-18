package repo

import (
	"errors"

	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/gorm"
)

// GetUserDailyQuotaInfo retrieves daily quota information for a user.
func GetUserDailyQuotaInfo(userId int) (*DailyQuotaInfo, error) {
	var user User
	err := DB.Select("id, daily_quota, daily_used, last_daily_reset, base_group, fallback_group, \"group\"").
		Where("id = ?", userId).First(&user).Error
	if err != nil {
		return nil, err
	}

	info := &DailyQuotaInfo{
		UserID:         user.Id,
		DailyQuota:     user.DailyQuota,
		DailyUsed:      user.DailyUsed,
		LastDailyReset: user.LastDailyReset,
		BaseGroup:      user.BaseGroup,
		FallbackGroup:  user.FallbackGroup,
		CurrentGroup:   user.Group,
	}

	if user.DailyQuota > 0 {
		info.DailyRemaining = user.DailyQuota - user.DailyUsed
		if info.DailyRemaining < 0 {
			info.DailyRemaining = 0
		}
	} else {
		info.DailyRemaining = -1
	}

	info.IsUsingFallback = user.FallbackGroup != "" && user.Group == user.FallbackGroup
	info.NeedsReset = NeedsDailyReset(user.LastDailyReset)

	return info, nil
}

// NeedsDailyReset delegates to entity.NeedsDailyReset.
var NeedsDailyReset = entity.NeedsDailyReset

// NeedsDailyResetAt delegates to entity.NeedsDailyResetAt.
var NeedsDailyResetAt = entity.NeedsDailyResetAt

// IncreaseDailyUsed increases the daily used quota for a user.
func IncreaseDailyUsed(userId int, amount int) error {
	if amount < 0 {
		return errors.New("amount cannot be negative")
	}

	if common.RedisEnabled {
		AsyncGo(func() {
			if err := cacheIncrUserDailyUsed(userId, int64(amount)); err != nil {
				common.SysLog("failed to increase user daily used cache: " + err.Error())
			}
		})
	}

	return DB.Model(&User{}).Where("id = ?", userId).
		Update("daily_used", gorm.Expr("daily_used + ?", amount)).Error
}

// ResetDailyQuota resets daily used quota if last_daily_reset is before today's UTC midnight.
func ResetDailyQuota(userId int) error {
	now := common.GetTimestamp()
	todayStart := (now / 86400) * 86400

	result := DB.Model(&User{}).
		Where("id = ? AND last_daily_reset < ?", userId, todayStart).
		Updates(map[string]interface{}{
			"daily_used":       0,
			"last_daily_reset": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}

	if common.RedisEnabled {
		AsyncGo(func() {
			if err := updateUserDailyUsedCache(userId, 0); err != nil {
				common.SysLog("failed to update user daily used cache: " + err.Error())
			}
			if err := updateUserLastDailyResetCache(userId, now); err != nil {
				common.SysLog("failed to update user last daily reset cache: " + err.Error())
			}
		})
	}

	return nil
}

// SwitchToFallbackGroup switches user to fallback group when daily quota is exhausted.
func SwitchToFallbackGroup(userId int) error {
	var user User
	if err := DB.Select("id, fallback_group, \"group\"").Where("id = ?", userId).First(&user).Error; err != nil {
		return err
	}
	if user.FallbackGroup == "" || user.Group == user.FallbackGroup {
		return nil
	}
	if err := DB.Model(&User{}).Where("id = ?", userId).Update("group", user.FallbackGroup).Error; err != nil {
		return err
	}
	if common.RedisEnabled {
		AsyncGo(func() {
			if err := updateUserGroupCache(userId, user.FallbackGroup); err != nil {
				common.SysLog("failed to update user group cache: " + err.Error())
			}
		})
	}
	return nil
}

// RestoreToBaseGroup restores user to base group after daily reset.
func RestoreToBaseGroup(userId int) error {
	var user User
	if err := DB.Select("id, base_group, \"group\"").Where("id = ?", userId).First(&user).Error; err != nil {
		return err
	}
	if user.BaseGroup == "" || user.Group == user.BaseGroup {
		return nil
	}
	if err := DB.Model(&User{}).Where("id = ?", userId).Update("group", user.BaseGroup).Error; err != nil {
		return err
	}
	if common.RedisEnabled {
		AsyncGo(func() {
			if err := updateUserGroupCache(userId, user.BaseGroup); err != nil {
				common.SysLog("failed to update user group cache: " + err.Error())
			}
		})
	}
	return nil
}

// GetUsersNeedingDailyReset returns users who need daily quota reset.
func GetUsersNeedingDailyReset(limit int) ([]*User, error) {
	var users []*User
	now := common.GetTimestamp()
	todayStart := (now / 86400) * 86400

	err := DB.Select("id, daily_quota, daily_used, last_daily_reset, base_group, fallback_group, \"group\"").
		Where("daily_quota > 0 AND (last_daily_reset < ? OR last_daily_reset = 0)", todayStart).
		Limit(limit).
		Find(&users).Error

	return users, err
}

// ProcessDailyQuotaReset resets daily quota and restores base group for a user.
func ProcessDailyQuotaReset(userId int) error {
	if err := ResetDailyQuota(userId); err != nil {
		return err
	}
	return RestoreToBaseGroup(userId)
}
