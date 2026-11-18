package repo

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/gorm"
)

// GetUserByLurusAccountID resolves a newhub user by the lurus-platform account integer id.
func GetUserByLurusAccountID(accountID int64) (*User, error) {
	if accountID <= 0 {
		return nil, errors.New("account_id must be > 0")
	}
	var user User
	err := WithoutTenantIsolation(DB).
		Where("lurus_account_id = ?", accountID).
		First(&user).Error
	return &user, err
}

// IdentityAccountIDForUser resolves the lurus-platform account id linked to a user, or 0 if unlinked.
func IdentityAccountIDForUser(userID int) int64 {
	owner, err := GetUserById(userID)
	if err != nil || owner.LurusAccountID == nil {
		return 0
	}
	return *owner.LurusAccountID
}

// LinkUserPlatformAccount binds userID to a lurus-platform accountID, optionally backfilling unlinked tokens.
func LinkUserPlatformAccount(userID int, accountID int64, backfillTokens bool) error {
	if accountID <= 0 {
		return nil
	}
	if existing, err := GetUserByLurusAccountID(accountID); err == nil && existing != nil && existing.Id != userID {
		common.SysLog(fmt.Sprintf(
			"LinkUserPlatformAccount: account %d already bound to user %d — skipping link for user %d",
			accountID, existing.Id, userID))
		return nil
	}
	if owner, err := GetUserById(userID, false); err == nil && owner != nil &&
		owner.LurusAccountID != nil && *owner.LurusAccountID > 0 && *owner.LurusAccountID != accountID {
		common.SysLog(fmt.Sprintf(
			"LinkUserPlatformAccount: user %d is already linked to account %d — skipping link to account %d",
			userID, *owner.LurusAccountID, accountID))
		return nil
	}
	if !backfillTokens {
		return DB.Model(&User{}).
			Where("id = ? AND lurus_account_id IS NULL", userID).
			Update("lurus_account_id", accountID).Error
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).
			Where("id = ? AND lurus_account_id IS NULL", userID).
			Update("lurus_account_id", accountID).Error; err != nil {
			return err
		}
		return tx.Model(&Token{}).
			Where("user_id = ? AND (identity_account_id = 0 OR identity_account_id IS NULL)", userID).
			Updates(map[string]interface{}{"identity_account_id": accountID, "unlimited_quota": true}).Error
	})
}

// AdminListUsers returns a paginated, optionally-filtered list of users across all tenants.
func AdminListUsers(keyword, group string, status, offset, limit int) ([]*User, int64, error) {
	query := GetSystemDB().Model(&User{})
	if keyword != "" {
		like := "%" + keyword + "%"
		if idVal, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR username LIKE ? OR email LIKE ? OR display_name LIKE ?",
				idVal, like, like, like)
		} else {
			query = query.Where("username LIKE ? OR email LIKE ? OR display_name LIKE ?", like, like, like)
		}
	}
	if group != "" {
		query = query.Where(commonGroupCol+" = ?", group)
	}
	if status > 0 {
		query = query.Where("status = ?", status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []*User
	if err := query.Order("id desc").Offset(offset).Limit(limit).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// AdminUpdateUser updates platform-admin-editable fields for a user across tenant boundaries.
func AdminUpdateUser(id, role, status, quota int, group string) (*User, error) {
	if id == 0 {
		return nil, errEmptyUserID()
	}
	var user User
	if err := GetSystemDB().First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	updates := map[string]interface{}{
		"role":   role,
		"status": status,
		"quota":  quota,
		"group":  group,
	}
	if err := GetSystemDB().Model(&User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, err
	}
	user.Role, user.Status, user.Quota, user.Group = role, status, quota, group
	if err := updateUserCache(user); err != nil {
		common.SysLog("admin update user cache refresh failed: " + err.Error())
	}
	return &user, nil
}

// AdminDeleteUser soft-deletes a user across tenant boundaries and invalidates user cache.
func AdminDeleteUser(id int) error {
	if id == 0 {
		return errEmptyUserID()
	}
	if err := GetSystemDB().Delete(&User{}, "id = ?", id).Error; err != nil {
		return err
	}
	return invalidateUserCache(id)
}

// CountUsersByRole counts non-deleted users holding the given role across all tenants.
func CountUsersByRole(role int) (int64, error) {
	var total int64
	err := GetSystemDB().Model(&User{}).Where("role = ?", role).Count(&total).Error
	return total, err
}

func IsAdmin(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	if err := DB.Where("id = ?", userId).Select("role").Find(&user).Error; err != nil {
		common.SysLog("no such user " + err.Error())
		return false
	}
	return user.Role >= common.RoleAdminUser
}

// IsRoot reports whether the user holds the root (cross-tenant operator) role.
func IsRoot(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	if err := DB.Where("id = ?", userId).Select("role").Find(&user).Error; err != nil {
		common.SysLog("no such user " + err.Error())
		return false
	}
	return user.Role >= common.RoleRootUser
}

func GetRootUser() (user *User) {
	DB.Where("role = ?", common.RoleRootUser).First(&user)
	return user
}

func RootUserExists() bool {
	var user User
	return DB.Where("role = ?", common.RoleRootUser).First(&user).Error == nil
}
