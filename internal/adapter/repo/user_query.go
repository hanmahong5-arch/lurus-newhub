package repo

import (
	"errors"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"gorm.io/gorm"
)

func GetMaxUserId() int {
	var user User
	DB.Unscoped().Last(&user)
	return user.Id
}

func GetAllUsers(pageInfo *common.PageInfo) (users []*User, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err = tx.Unscoped().Model(&User{}).Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Unscoped().Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&users).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func applyUserKeywordAndGroupFilter(query *gorm.DB, keyword string, group string) *gorm.DB {
	likeCondition := "username LIKE ? OR email LIKE ? OR display_name LIKE ?"
	like := "%" + keyword + "%"
	if keywordInt, err := strconv.Atoi(keyword); err == nil {
		likeCondition = "id = ? OR " + likeCondition
		if group != "" {
			return query.Where("("+likeCondition+") AND "+commonGroupCol+" = ?",
				keywordInt, like, like, like, group)
		}
		return query.Where(likeCondition, keywordInt, like, like, like)
	}
	if group != "" {
		return query.Where("("+likeCondition+") AND "+commonGroupCol+" = ?",
			like, like, like, group)
	}
	return query.Where(likeCondition, like, like, like)
}

func SearchUsers(keyword string, group string, startIdx int, num int) ([]*User, int64, error) {
	var users []*User
	var total int64

	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := applyUserKeywordAndGroupFilter(tx.Unscoped().Model(&User{}), keyword, group)

	if err := query.Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	if err := query.Order("id desc").Limit(num).Offset(startIdx).Find(&users).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// GetUsersByTenant lists users belonging to a single tenant.
func GetUsersByTenant(tenantID string, pageInfo *common.PageInfo) (users []*User, total int64, err error) {
	db := DB.Unscoped().Model(&User{}).Where("tenant_id = ?", tenantID)
	if err = db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = db.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// SearchUsersByTenant is the tenant-scoped sibling of SearchUsers.
func SearchUsersByTenant(tenantID string, keyword string, group string, startIdx int, num int) ([]*User, int64, error) {
	var users []*User
	var total int64

	query := applyUserKeywordAndGroupFilter(DB.Unscoped().Model(&User{}).Where("tenant_id = ?", tenantID), keyword, group)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id desc").Limit(num).Offset(startIdx).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// GetUserById fetches a user by ID.
func GetUserById(id int, _ ...bool) (*User, error) {
	if id == 0 {
		return nil, errEmptyUserID()
	}
	user := User{Id: id}
	err := DB.First(&user, "id = ?", id).Error
	return &user, err
}

func IsEmailAlreadyTaken(email string) bool {
	return DB.Unscoped().Where("email = ?", email).Find(&User{}).RowsAffected == 1
}

func ValidateAccessToken(token string) (user *User) {
	if token == "" {
		return nil
	}
	token = strings.Replace(token, "Bearer ", "", 1)
	user = &User{}
	if DB.Where("access_token = ?", token).First(user).RowsAffected == 1 {
		return user
	}
	return nil
}

func GetUserEmail(id int) (email string, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("email").Find(&email).Error
	return email, err
}

// GetUserGroup gets group from Redis first, falling back to DB if needed.
func GetUserGroup(id int, fromDB bool) (group string, err error) {
	defer func() {
		if shouldUpdateRedis(fromDB, err) {
			AsyncGo(func() {
				if err := updateUserGroupCache(id, group); err != nil {
					common.SysLog("failed to update user group cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		if group, err := getUserGroupCache(id); err == nil {
			return group, nil
		}
	}
	fromDB = true
	if err = DB.Model(&User{}).Where("id = ?", id).Select(commonGroupCol).Find(&group).Error; err != nil {
		return "", err
	}
	return group, nil
}

// GetUserSetting gets setting from Redis first, falling back to DB if needed.
func GetUserSetting(id int, fromDB bool) (settingMap dto.UserSetting, err error) {
	var setting string
	defer func() {
		if shouldUpdateRedis(fromDB, err) {
			AsyncGo(func() {
				if err := updateUserSettingCache(id, setting); err != nil {
					common.SysLog("failed to update user setting cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		if setting, err := getUserSettingCache(id); err == nil {
			return setting, nil
		}
	}
	fromDB = true
	if err = DB.Model(&User{}).Where("id = ?", id).Select("setting").Find(&setting).Error; err != nil {
		return settingMap, err
	}
	userBase := &UserBase{Setting: setting}
	return userBase.GetSetting(), nil
}

// GetUsernameById gets username from Redis first, falling back to DB if needed.
func GetUsernameById(id int, fromDB bool) (username string, err error) {
	defer func() {
		if shouldUpdateRedis(fromDB, err) {
			AsyncGo(func() {
				if err := updateUserNameCache(id, username); err != nil {
					common.SysLog("failed to update user name cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		if username, err := getUserNameCache(id); err == nil {
			return username, nil
		}
	}
	fromDB = true
	if err = DB.Model(&User{}).Where("id = ?", id).Select("username").Find(&username).Error; err != nil {
		return "", err
	}
	return username, nil
}

// GetUserRole returns user role by ID.
func GetUserRole(id int) (int, error) {
	var role int
	err := DB.Model(&User{}).Where("id = ?", id).Select("role").Scan(&role).Error
	return role, err
}

// UpdateUserRole updates user role.
func UpdateUserRole(id int, role int) error {
	if !common.IsValidateRole(role) {
		return errors.New("invalid role")
	}
	return DB.Model(&User{}).Where("id = ?", id).Update("role", role).Error
}
