package entity

import (
	"encoding/json"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"gorm.io/gorm"
)

// User is the core user entity. Auth is delegated to the OIDC provider; billing is delegated to lurus-platform.
type User struct {
	Id       int    `json:"id"`
	TenantId string `json:"tenant_id" gorm:"type:varchar(36);index;uniqueIndex:uk_users_tenant_username,priority:1;default:'default'"` // Tenant isolation
	// Username is unique PER TENANT (composite uk_users_tenant_username with
	// TenantId, migration 025) — auth is OIDC-sub based, so no lookup needs a
	// global username. The tag must stay in sync with the repo-local User
	// struct (adapter/repo/user.go): AutoMigrate runs over BOTH, and a stray
	// single-column `unique` tag on either side would re-create the dropped
	// global unique every boot.
	Username       string         `json:"username" gorm:"index;uniqueIndex:uk_users_tenant_username,priority:2" validate:"max=20"`
	DisplayName    string         `json:"display_name" gorm:"index" validate:"max=20"`
	Role           int            `json:"role" gorm:"type:int;default:1"`   // admin, common
	Status         int            `json:"status" gorm:"type:int;default:1"` // enabled, disabled
	Email          string         `json:"email" gorm:"index" validate:"max=50"`
	AccessToken    *string        `json:"access_token" gorm:"type:char(32);column:access_token;uniqueIndex"` // system management token
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
	// LurusAccountID is the lurus-platform account integer ID. Nullable
	// because pre-Layer-C users were created via direct registration and
	// have no platform binding. Bridge endpoint (Layer C) sets it when a
	// newhub user is first bound to a platform account; uniqueIndex
	// prevents double-binding.
	LurusAccountID *int64 `json:"lurus_account_id,omitempty" gorm:"type:bigint;column:lurus_account_id;uniqueIndex"`
}

func (user *User) ToBaseUser() *UserBase {
	return &UserBase{
		Id:             user.Id,
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
}

func (user *User) GetAccessToken() string {
	if user.AccessToken == nil {
		return ""
	}
	return *user.AccessToken
}

func (user *User) SetAccessToken(token string) {
	user.AccessToken = &token
}

func (user *User) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := json.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
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

// IsSubscriber checks if user has subscriber role or higher
func (user *User) IsSubscriber() bool {
	return user.Role >= common.RoleSubscriberUser
}

// UserBase is a lightweight view of User for caching
type UserBase struct {
	Id             int    `json:"id"`
	TenantId       string `json:"tenant_id"`
	Group          string `json:"group"`
	Email          string `json:"email"`
	Quota          int    `json:"quota"`
	Status         int    `json:"status"`
	Username       string `json:"username"`
	Setting        string `json:"setting"`
	DailyQuota     int    `json:"daily_quota"`
	DailyUsed      int    `json:"daily_used"`
	LastDailyReset int64  `json:"last_daily_reset"`
	BaseGroup      string `json:"base_group"`
	FallbackGroup  string `json:"fallback_group"`
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := json.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

// DailyQuotaInfo represents daily quota status for a user
type DailyQuotaInfo struct {
	UserID          int    `json:"user_id"`
	DailyQuota      int    `json:"daily_quota"`
	DailyUsed       int    `json:"daily_used"`
	DailyRemaining  int    `json:"daily_remaining"`
	LastDailyReset  int64  `json:"last_daily_reset"`
	BaseGroup       string `json:"base_group"`
	FallbackGroup   string `json:"fallback_group"`
	CurrentGroup    string `json:"current_group"`
	IsUsingFallback bool   `json:"is_using_fallback"`
	NeedsReset      bool   `json:"needs_reset"`
}

// NeedsDailyReset reports whether the daily quota should be reset, using the
// current wall-clock time. Day boundaries are UTC midnight (unix-day = ts/86400).
func NeedsDailyReset(lastResetTimestamp int64) bool {
	return NeedsDailyResetAt(lastResetTimestamp, common.GetTimestamp())
}

// NeedsDailyResetAt is the pure, clock-injectable core of NeedsDailyReset: it
// reports whether a reset is due as of nowTimestamp. Both args are unix seconds
// and the day boundary is UTC midnight (ts/86400). Exposed so the reset decision
// can be tested deterministically — callers that build "now-relative" timestamps
// against the real clock (e.g. now-1h) otherwise flake near a UTC day boundary.
func NeedsDailyResetAt(lastResetTimestamp, nowTimestamp int64) bool {
	if lastResetTimestamp == 0 {
		return true
	}
	return nowTimestamp/86400 > lastResetTimestamp/86400
}
