package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// UserBase is a type alias for entity.UserBase (canonical definition in domain/entity/user.go)
// GetSetting method is inherited through alias.
type UserBase = entity.UserBase

// UserBaseWriteContext writes user context to gin.Context.
// Converted from method to function because Go type aliases cannot have new methods.
func UserBaseWriteContext(user *UserBase, c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

// invalidateUserCache clears user cache
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisDelKey(context.TODO(), getUserCacheKey(userId))
}

// updateUserCache updates all user cache fields using hash
func updateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}

	return common.RedisHSetObj(
		context.TODO(),
		getUserCacheKey(user.Id),
		user.ToBaseUser(),
		time.Duration(common.RedisKeyCacheSeconds())*time.Second,
	)
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (userCache *UserBase, err error) {
	var user *User
	var fromDB bool
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) && user != nil {
			AsyncGo(func() {
				if err := updateUserCache(*user); err != nil {
					common.SysLog("failed to update user status cache: " + err.Error())
				}
			})
		}
	}()

	// Try getting from Redis first
	userCache, err = cacheGetUserBase(userId)
	if err == nil {
		return userCache, nil
	}

	// If Redis fails, get from DB
	fromDB = true
	user, err = GetUserById(userId, false)
	if err != nil {
		return nil, err // Return nil and error if DB lookup fails
	}

	// Create cache object from user data
	userCache = &UserBase{
		Id:             user.Id,
		TenantId:       user.TenantId,
		Group:          user.Group,
		Quota:          user.Quota,
		Status:         user.Status,
		Username:       user.Username,
		Setting:        user.Setting,
		Email:          user.Email,
		DailyQuota:     user.DailyQuota,
		DailyUsed:      user.DailyUsed,
		LastDailyReset: user.LastDailyReset,
		BaseGroup:      user.BaseGroup,
		FallbackGroup:  user.FallbackGroup,
	}

	return userCache, nil
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var userCache UserBase
	// Try getting from Redis first
	err := common.RedisHGetObj(context.TODO(), getUserCacheKey(userId), &userCache)
	if err != nil {
		return nil, err
	}
	return &userCache, nil
}

// Add atomic quota operations using hash fields
func cacheIncrUserQuota(userId int, delta int64) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHIncrBy(context.TODO(), getUserCacheKey(userId), "Quota", delta)
}

func cacheDecrUserQuota(userId int, delta int64) error {
	return cacheIncrUserQuota(userId, -delta)
}

// Helper functions to get individual fields if needed
func getUserGroupCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Group, nil
}

func getUserQuotaCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Quota, nil
}

func getUserStatusCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Status, nil
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

// New functions for individual field updates
func updateUserStatusCache(userId int, status bool) error {
	if !common.RedisEnabled {
		return nil
	}
	statusInt := common.UserStatusEnabled
	if !status {
		statusInt = common.UserStatusDisabled
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "Status", fmt.Sprintf("%d", statusInt))
}

func updateUserQuotaCache(userId int, quota int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "Quota", fmt.Sprintf("%d", quota))
}

func updateUserGroupCache(userId int, group string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "Group", group)
}

func updateUserNameCache(userId int, username string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "Username", username)
}

func updateUserSettingCache(userId int, setting string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "Setting", setting)
}

// Daily quota cache functions
func updateUserDailyQuotaCache(userId int, dailyQuota int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "DailyQuota", fmt.Sprintf("%d", dailyQuota))
}

func updateUserDailyUsedCache(userId int, dailyUsed int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "DailyUsed", fmt.Sprintf("%d", dailyUsed))
}

func cacheIncrUserDailyUsed(userId int, delta int64) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHIncrBy(context.TODO(), getUserCacheKey(userId), "DailyUsed", delta)
}

func updateUserBaseGroupCache(userId int, baseGroup string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "BaseGroup", baseGroup)
}

func updateUserFallbackGroupCache(userId int, fallbackGroup string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "FallbackGroup", fallbackGroup)
}

func updateUserLastDailyResetCache(userId int, lastDailyReset int64) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(context.TODO(), getUserCacheKey(userId), "LastDailyReset", fmt.Sprintf("%d", lastDailyReset))
}
