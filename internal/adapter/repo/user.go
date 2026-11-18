package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"gorm.io/gorm"
)

// DailyQuotaInfo is aliased from the canonical definition in domain/entity/user.go.
type DailyQuotaInfo = entity.DailyQuotaInfo

// User is the core user persistence entity mirroring domain/entity/user.go.
type User struct {
	Id             int            `json:"id"`
	TenantId       string         `json:"tenant_id" gorm:"type:varchar(36);index;uniqueIndex:uk_users_tenant_username,priority:1;default:'default'"`
	Username       string         `json:"username" gorm:"index;uniqueIndex:uk_users_tenant_username,priority:2" validate:"max=20"`
	DisplayName    string         `json:"display_name" gorm:"index" validate:"max=20"`
	Role           int            `json:"role" gorm:"type:int;default:1"`
	Status         int            `json:"status" gorm:"type:int;default:1"`
	Email          string         `json:"email" gorm:"index" validate:"max=50"`
	AccessToken    *string        `json:"access_token" gorm:"type:char(32);column:access_token;uniqueIndex"`
	Quota          int            `json:"quota" gorm:"type:int;default:0"`
	UsedQuota      int            `json:"used_quota" gorm:"type:int;default:0;column:used_quota"`
	RequestCount   int            `json:"request_count" gorm:"type:int;default:0;"`
	Group          string         `json:"group" gorm:"type:varchar(64);default:'default'"`
	DailyQuota     int            `json:"daily_quota" gorm:"type:int;default:0;column:daily_quota"`
	DailyUsed      int            `json:"daily_used" gorm:"type:int;default:0;column:daily_used"`
	LastDailyReset int64          `json:"last_daily_reset" gorm:"type:bigint;default:0;column:last_daily_reset"`
	BaseGroup      string         `json:"base_group" gorm:"type:varchar(64);column:base_group"`
	FallbackGroup  string         `json:"fallback_group" gorm:"type:varchar(64);column:fallback_group"`
	DeletedAt      gorm.DeletedAt `gorm:"index"`
	Setting        string         `json:"setting" gorm:"type:text;column:setting"`
	Remark         string         `json:"remark,omitempty" gorm:"type:varchar(255)" validate:"max=255"`
	LurusAccountID *int64         `json:"lurus_account_id,omitempty" gorm:"type:bigint;column:lurus_account_id;uniqueIndex"`
	TenantRole     string         `json:"tenant_role" gorm:"type:varchar(16);not null;default:''"`
}

func errEmptyUserID() error {
	return errors.New("id 为空！")
}

func (user *User) ToBaseUser() *UserBase {
	return &UserBase{
		Id:             user.Id,
		Group:          user.Group,
		Quota:          user.Quota,
		Status:         user.Status,
		Role:           user.Role,
		TenantId:       user.TenantId,
		TenantRole:     user.TenantRole,
		Username:       user.Username,
		Setting:        user.Setting,
		Email:          user.Email,
		DailyQuota:     user.DailyQuota,
		DailyUsed:      user.DailyUsed,
		LastDailyReset: user.LastDailyReset,
		BaseGroup:      user.BaseGroup,
		FallbackGroup:  user.FallbackGroup,
	}
}

func (user *User) GetAccessToken() string {
	if user.AccessToken == nil {
		return ""
	}
	return *user.AccessToken
}

func (user *User) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		if err := json.Unmarshal([]byte(user.Setting), &setting); err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

func (user *User) SetSetting(setting dto.UserSetting) {
	settingBytes, err := json.Marshal(setting)
	if err != nil {
		common.SysLog("failed to marshal setting: " + err.Error())
		return
	}
	user.Setting = string(settingBytes)
}

func generateDefaultSidebarConfigForRole(userRole int) string {
	defaultConfig := map[string]interface{}{
		"chat": map[string]interface{}{
			"enabled":    true,
			"playground": true,
			"chat":       true,
		},
		"console": map[string]interface{}{
			"enabled":    true,
			"detail":     true,
			"token":      true,
			"log":        true,
			"midjourney": true,
			"task":       true,
		},
		"personal": map[string]interface{}{
			"enabled":  true,
			"topup":    true,
			"personal": true,
		},
	}

	if userRole == common.RoleAdminUser || userRole == common.RoleRootUser {
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    userRole == common.RoleRootUser,
		}
	}

	configBytes, err := json.Marshal(defaultConfig)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to generate default sidebar config: %v", err))
		return ""
	}
	return string(configBytes)
}

func (user *User) Insert() error {
	user.Quota = common.QuotaForNewUser
	if user.Setting == "" {
		user.SetSetting(dto.UserSetting{})
	}

	result := WithTenantID(DB, user.TenantId).Create(user)
	if result.Error != nil {
		return result.Error
	}

	var createdUser User
	if err := DB.Where("id = ?", user.Id).First(&createdUser).Error; err == nil {
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			_ = createdUser.Update()
			common.SysLog(fmt.Sprintf("initialized sidebar config for new user %s (role: %d)", createdUser.Username, createdUser.Role))
		}
	}

	if common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}
	return nil
}

// Update saves the user and refreshes the cache.
func (user *User) Update(_ ...bool) error {
	newUser := *user
	DB.First(&user, user.Id)
	if err := DB.Model(user).Updates(newUser).Error; err != nil {
		return err
	}
	return updateUserCache(*user)
}

func (user *User) Delete() error {
	if user.Id == 0 {
		return errEmptyUserID()
	}
	if err := DB.Delete(user).Error; err != nil {
		return err
	}
	return invalidateUserCache(user.Id)
}

func (user *User) HardDelete() error {
	if user.Id == 0 {
		return errEmptyUserID()
	}
	return DB.Unscoped().Delete(user).Error
}

func (user *User) FillUserById() error {
	if user.Id == 0 {
		return errEmptyUserID()
	}
	DB.Where(User{Id: user.Id}).First(user)
	return nil
}

func (user *User) FillUserByEmail() error {
	if user.Email == "" {
		return errors.New("email 为空！")
	}
	DB.Where(User{Email: user.Email}).First(user)
	return nil
}

// DeleteUserById soft-deletes the user and revokes their relay tokens, sessions, and permission grants.
func DeleteUserById(id int) (err error) {
	if id == 0 {
		return errEmptyUserID()
	}

	var tokens []Token
	if err := DB.Where("user_id = ?", id).Find(&tokens).Error; err != nil {
		return fmt.Errorf("load user's tokens: %w", err)
	}
	for i := range tokens {
		if err := tokens[i].Delete(); err != nil {
			return fmt.Errorf("revoke token id=%d: %w", tokens[i].Id, err)
		}
	}

	if _, sessErr := HardDeleteUserSessions(context.Background(), id); sessErr != nil {
		common.SysLog(fmt.Sprintf("DeleteUserById: hard delete user sessions failed for id=%d: %v", id, sessErr))
	}

	if _, grantErr := RevokePermissionGrantsForUser(id); grantErr != nil {
		common.SysLog(fmt.Sprintf("DeleteUserById: revoke permission grants failed for id=%d: %v", id, grantErr))
	}

	user := User{Id: id}
	return user.Delete()
}

func HardDeleteUserById(id int) error {
	if id == 0 {
		return errEmptyUserID()
	}
	return DB.Unscoped().Delete(&User{}, "id = ?", id).Error
}

// DisableUserById sets a user's status to UserStatusDisabled and invalidates user cache.
func DisableUserById(id int) error {
	if id == 0 {
		return errEmptyUserID()
	}
	if err := DB.Model(&User{}).Where("id = ?", id).Update("status", common.UserStatusDisabled).Error; err != nil {
		return err
	}
	if err := invalidateUserCache(id); err != nil {
		common.SysLog("disable user cache invalidation failed: " + err.Error())
	}
	return nil
}

// IsSubscriber checks if user has subscriber role or higher.
func (user *User) IsSubscriber() bool {
	return user.Role >= common.RoleSubscriberUser
}
