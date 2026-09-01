package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

// Subtypes aliased from the canonical definition in domain/entity/user.go
type DailyQuotaInfo = entity.DailyQuotaInfo

// User is the core user entity. Auth is delegated to the OIDC provider; billing is delegated to lurus-platform.
type User struct {
	Id       int    `json:"id"`
	TenantId string `json:"tenant_id" gorm:"type:varchar(36);index;uniqueIndex:uk_users_tenant_username,priority:1;default:'default'"` // Tenant isolation
	// Username is unique PER TENANT (composite uk_users_tenant_username with
	// TenantId, migration 025); mirrors entity.User — both tags must move
	// together or AutoMigrate re-creates the dropped global unique at boot.
	Username     string  `json:"username" gorm:"index;uniqueIndex:uk_users_tenant_username,priority:2" validate:"max=20"`
	DisplayName  string  `json:"display_name" gorm:"index" validate:"max=20"`
	Role         int     `json:"role" gorm:"type:int;default:1"`   // admin, common
	Status       int     `json:"status" gorm:"type:int;default:1"` // enabled, disabled
	Email        string  `json:"email" gorm:"index" validate:"max=50"`
	AccessToken  *string `json:"access_token" gorm:"type:char(32);column:access_token;uniqueIndex"` // system management token
	Quota        int     `json:"quota" gorm:"type:int;default:0"`
	UsedQuota    int     `json:"used_quota" gorm:"type:int;default:0;column:used_quota"`
	RequestCount int     `json:"request_count" gorm:"type:int;default:0;"`
	Group        string  `json:"group" gorm:"type:varchar(64);default:'default'"`
	// Subscription-based daily quota management
	DailyQuota     int            `json:"daily_quota" gorm:"type:int;default:0;column:daily_quota"`
	DailyUsed      int            `json:"daily_used" gorm:"type:int;default:0;column:daily_used"`
	LastDailyReset int64          `json:"last_daily_reset" gorm:"type:bigint;default:0;column:last_daily_reset"`
	BaseGroup      string         `json:"base_group" gorm:"type:varchar(64);column:base_group"`
	FallbackGroup  string         `json:"fallback_group" gorm:"type:varchar(64);column:fallback_group"`
	DeletedAt      gorm.DeletedAt `gorm:"index"`
	Setting        string         `json:"setting" gorm:"type:text;column:setting"`
	Remark         string         `json:"remark,omitempty" gorm:"type:varchar(255)" validate:"max=255"`
	// LurusAccountID maps to lurus-platform account integer ID. Nullable for
	// pre-Layer-C users (created via direct registration); set when the
	// bridge endpoint binds a newhub user to a platform account. Mirrors
	// entity.User; both must move together until the duplicate is collapsed.
	LurusAccountID *int64 `json:"lurus_account_id,omitempty" gorm:"type:bigint;column:lurus_account_id;uniqueIndex"`
}

func (user *User) ToBaseUser() *UserBase {
	cache := &UserBase{
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
	return cache
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

// generateDefaultSidebarConfigForRole generates default sidebar config based on user role
func generateDefaultSidebarConfigForRole(userRole int) string {
	defaultConfig := map[string]interface{}{}

	defaultConfig["chat"] = map[string]interface{}{
		"enabled":    true,
		"playground": true,
		"chat":       true,
	}

	defaultConfig["console"] = map[string]interface{}{
		"enabled":    true,
		"detail":     true,
		"token":      true,
		"log":        true,
		"midjourney": true,
		"task":       true,
	}

	defaultConfig["personal"] = map[string]interface{}{
		"enabled":  true,
		"topup":    true,
		"personal": true,
	}

	if userRole == common.RoleAdminUser {
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    false,
		}
	} else if userRole == common.RoleRootUser {
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    true,
		}
	}

	configBytes, err := json.Marshal(defaultConfig)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to generate default sidebar config: %v", err))
		return ""
	}

	return string(configBytes)
}

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

	err = tx.Unscoped().Model(&User{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	err = tx.Unscoped().Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

func SearchUsers(keyword string, group string, startIdx int, num int) ([]*User, int64, error) {
	var users []*User
	var total int64
	var err error

	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Unscoped().Model(&User{})

	likeCondition := "username LIKE ? OR email LIKE ? OR display_name LIKE ?"

	keywordInt, err := strconv.Atoi(keyword)
	if err == nil {
		likeCondition = "id = ? OR " + likeCondition
		if group != "" {
			query = query.Where("("+likeCondition+") AND "+commonGroupCol+" = ?",
				keywordInt, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", group)
		} else {
			query = query.Where(likeCondition,
				keywordInt, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
		}
	} else {
		if group != "" {
			query = query.Where("("+likeCondition+") AND "+commonGroupCol+" = ?",
				"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", group)
		} else {
			query = query.Where(likeCondition,
				"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
		}
	}

	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// GetUsersByTenant lists users belonging to a single tenant, for the v1 admin
// console when the caller is a tenant admin rather than a platform root
// operator. Mirrors GetAllUsers but constrains the result set to one tenant.
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

// SearchUsersByTenant is the tenant-scoped sibling of SearchUsers used by the
// v1 admin console for tenant-admin callers.
func SearchUsersByTenant(tenantID string, keyword string, group string, startIdx int, num int) ([]*User, int64, error) {
	var users []*User
	var total int64

	query := DB.Unscoped().Model(&User{}).Where("tenant_id = ?", tenantID)

	likeCondition := "username LIKE ? OR email LIKE ? OR display_name LIKE ?"
	if keywordInt, convErr := strconv.Atoi(keyword); convErr == nil {
		likeCondition = "id = ? OR " + likeCondition
		if group != "" {
			query = query.Where("("+likeCondition+") AND "+commonGroupCol+" = ?",
				keywordInt, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", group)
		} else {
			query = query.Where(likeCondition,
				keywordInt, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
		}
	} else {
		if group != "" {
			query = query.Where("("+likeCondition+") AND "+commonGroupCol+" = ?",
				"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", group)
		} else {
			query = query.Where(likeCondition,
				"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
		}
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id desc").Limit(num).Offset(startIdx).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// GetUserById fetches a user by ID. The optional bool argument is accepted but ignored
// (kept for backward compatibility while callers are updated).
func GetUserById(id int, _ ...bool) (*User, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	user := User{Id: id}
	err := DB.First(&user, "id = ?", id).Error
	return &user, err
}

// GetUserByLurusAccountID resolves a newhub user by the lurus-platform
// account integer id. Returns gorm.ErrRecordNotFound if no user is bound,
// which the bridge endpoint converts into an auto-create.
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

// DeleteUserById soft-deletes the user AND revokes every one of their relay
// tokens. Before this fix it only called User.Delete() (a bare soft delete
// of the users row): middleware.TokenAuth resolves a token to its owner via
// repo.GetTokenByKey (token.go:264), never by looking the user up first, so
// an already-issued token kept authenticating for as long as its row (or a
// stale Redis cache entry) survived — see
// TestUserDelete_TokensSurviveSoftDeleteAlone in
// r5d_user_delete_cascade_test.go for the pinned pre-fix behavior of the
// (still-unchanged) User.Delete() method in isolation.
//
// Tokens are revoked one at a time through Token.Delete() (token.go:404)
// instead of a single bulk `DB.Where("user_id = ?").Delete(&Token{})`: the
// bulk form never runs cacheDeleteToken per key (token.go:408), so a
// Redis-cached token would keep validating from cache until its TTL even
// after the DB row was gone. Tokens are revoked BEFORE the user row so a
// mid-failure leaves a still-enabled user with fewer tokens rather than a
// deleted user with a live token — the direction this fix closes.
//
// The only production caller is the internal API's user:delete scope
// (internal_api_ext.go:248); the userRoute group (router/api-router.go:
// 230-236) has no DELETE, so this path is unreachable from the console.
func DeleteUserById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
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

	user := User{Id: id}
	return user.Delete()
}

func HardDeleteUserById(id int) error {
	if id == 0 {
		return errors.New("id 为空！")
	}
	err := DB.Unscoped().Delete(&User{}, "id = ?", id).Error
	return err
}

// DisableUserById flips a user's status to UserStatusDisabled. Used by the
// cost-spike protection middleware as the corrective action when the 5-min
// quota window is exceeded — keeps the runaway loop from draining the wallet
// further until an admin reviews. Idempotent.
func DisableUserById(id int) error {
	if id == 0 {
		return errors.New("id 为空！")
	}
	return DB.Model(&User{}).Where("id = ?", id).Update("status", common.UserStatusDisabled).Error
}

// AdminListUsers returns a paginated, optionally-filtered list of users across
// ALL tenants (platform-admin view — bypasses tenant isolation). keyword matches
// id/username/email/display_name; group and status (when > 0) are exact filters.
// Soft-deleted rows are excluded.
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

// AdminUpdateUser updates only the platform-admin-editable fields (role, status,
// quota, group) for a user, bypassing tenant isolation, and refreshes the user
// cache so relay/auth observe the change immediately. Fields are passed
// explicitly (not via a struct) so a zero value — e.g. quota=0 — is honoured
// rather than silently skipped by GORM's struct-update semantics.
func AdminUpdateUser(id, role, status, quota int, group string) (*User, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
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

// AdminDeleteUser soft-deletes a user across tenant boundaries (platform-admin)
// and invalidates the user cache.
func AdminDeleteUser(id int) error {
	if id == 0 {
		return errors.New("id 为空！")
	}
	if err := GetSystemDB().Delete(&User{}, "id = ?", id).Error; err != nil {
		return err
	}
	return invalidateUserCache(id)
}

// CountUsersByRole counts non-deleted users holding the given role across all
// tenants. Used by the admin delete guard to refuse removing the last root.
func CountUsersByRole(role int) (int64, error) {
	var total int64
	err := GetSystemDB().Model(&User{}).Where("role = ?", role).Count(&total).Error
	return total, err
}

func (user *User) Insert() error {
	user.Quota = common.QuotaForNewUser

	if user.Setting == "" {
		defaultSetting := dto.UserSetting{}
		user.SetSetting(defaultSetting)
	}

	result := WithTenantID(DB, user.TenantId).Create(user)
	if result.Error != nil {
		return result.Error
	}

	// Initialize sidebar config based on role after user creation. Re-fetch by
	// primary key (Create populates user.Id): username is only per-tenant
	// unique since migration 025, so a bare username lookup could land on
	// another tenant's row.
	var createdUser User
	if err := DB.Where("id = ?", user.Id).First(&createdUser).Error; err == nil {
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			createdUser.Update()
			common.SysLog(fmt.Sprintf("initialized sidebar config for new user %s (role: %d)", createdUser.Username, createdUser.Role))
		}
	}

	if common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}

	return nil
}

// Update saves the user and updates the cache. The optional bool argument is accepted but
// ignored (kept for backward compatibility while callers are updated).
func (user *User) Update(_ ...bool) error {
	newUser := *user
	DB.First(&user, user.Id)
	if err := DB.Model(user).Updates(newUser).Error; err != nil {
		return err
	}
	return updateUserCache(*user)
}

func (user *User) Edit() error {
	newUser := *user
	updates := map[string]interface{}{
		"username":     newUser.Username,
		"display_name": newUser.DisplayName,
		"group":        newUser.Group,
		"quota":        newUser.Quota,
		"remark":       newUser.Remark,
	}
	// Edit is only reachable from the admin update path (handler.UpdateUser), which
	// has already enforced CheckPermission/CheckRolePromotion; the self-service path
	// (handler.UpdateSelf) goes through Update() with a clean struct and can never
	// reach here. Zero means "not provided" in the decoded JSON payload (valid
	// values start at 1, see common.UserStatusEnabled), so skip it to avoid
	// resetting role/status on partial updates.
	if newUser.Role != 0 {
		updates["role"] = newUser.Role
	}
	if newUser.Status != 0 {
		updates["status"] = newUser.Status
	}

	DB.First(&user, user.Id)
	if err := DB.Model(user).Updates(updates).Error; err != nil {
		return err
	}
	return updateUserCache(*user)
}

func (user *User) Delete() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	if err := DB.Delete(user).Error; err != nil {
		return err
	}
	return invalidateUserCache(user.Id)
}

func (user *User) HardDelete() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	return DB.Unscoped().Delete(user).Error
}

func (user *User) FillUserById() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
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

func IsEmailAlreadyTaken(email string) bool {
	return DB.Unscoped().Where("email = ?", email).Find(&User{}).RowsAffected == 1
}

func IsAdmin(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	err := DB.Where("id = ?", userId).Select("role").Find(&user).Error
	if err != nil {
		common.SysLog("no such user " + err.Error())
		return false
	}
	return user.Role >= common.RoleAdminUser
}

// IsRoot reports whether the user holds the root (cross-tenant operator) role.
// Mirrors IsAdmin; used to decide whether a specific-channel relay override may
// target a channel owned by a different tenant.
func IsRoot(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	err := DB.Where("id = ?", userId).Select("role").Find(&user).Error
	if err != nil {
		common.SysLog("no such user " + err.Error())
		return false
	}
	return user.Role >= common.RoleRootUser
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

// GetUserQuota gets quota from Redis first, falls back to DB if needed
func GetUserQuota(id int, fromDB bool) (quota int, err error) {
	defer func() {
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := updateUserQuotaCache(id, quota); err != nil {
					common.SysLog("failed to update user quota cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		quota, err := getUserQuotaCache(id)
		if err == nil {
			return quota, nil
		}
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select("quota").Find(&quota).Error
	if err != nil {
		return 0, err
	}
	return quota, nil
}

func GetUserUsedQuota(id int) (quota int, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("used_quota").Find(&quota).Error
	return quota, err
}

func GetUserEmail(id int) (email string, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("email").Find(&email).Error
	return email, err
}

// GetUserGroup gets group from Redis first, falls back to DB if needed
func GetUserGroup(id int, fromDB bool) (group string, err error) {
	defer func() {
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := updateUserGroupCache(id, group); err != nil {
					common.SysLog("failed to update user group cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		group, err := getUserGroupCache(id)
		if err == nil {
			return group, nil
		}
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select(commonGroupCol).Find(&group).Error
	if err != nil {
		return "", err
	}
	return group, nil
}

// GetUserSetting gets setting from Redis first, falls back to DB if needed
func GetUserSetting(id int, fromDB bool) (settingMap dto.UserSetting, err error) {
	var setting string
	defer func() {
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := updateUserSettingCache(id, setting); err != nil {
					common.SysLog("failed to update user setting cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		setting, err := getUserSettingCache(id)
		if err == nil {
			return setting, nil
		}
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select("setting").Find(&setting).Error
	if err != nil {
		return settingMap, err
	}
	userBase := &UserBase{
		Setting: setting,
	}
	return userBase.GetSetting(), nil
}

func IncreaseUserQuota(id int, quota int, db bool) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	// Read RedisEnabled on the caller's goroutine and gate the cache-refresh
	// spawn on it: the detached pool goroutine must not read mutable globals
	// (tests rewrite RedisEnabled/DB, which the race detector flags), and the
	// refresh is a no-op when Redis is off. Prod behavior is unchanged —
	// RedisEnabled is set once at startup.
	if common.RedisEnabled {
		gopool.Go(func() {
			err := cacheIncrUserQuota(id, int64(quota))
			if err != nil {
				common.SysLog("failed to increase user quota: " + err.Error())
			}
		})
	}
	if !db && common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUserQuota, id, quota)
		return nil
	}
	return increaseUserQuota(id, quota)
}

func increaseUserQuota(id int, quota int) (err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Update("quota", gorm.Expr("quota + ?", quota)).Error
	if err != nil {
		return err
	}
	return err
}

func DecreaseUserQuota(id int, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	// gate cache spawn on RedisEnabled (see IncreaseUserQuota)
	if common.RedisEnabled {
		gopool.Go(func() {
			err := cacheDecrUserQuota(id, int64(quota))
			if err != nil {
				common.SysLog("failed to decrease user quota: " + err.Error())
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUserQuota, id, -quota)
		return nil
	}
	return decreaseUserQuota(id, quota)
}

func decreaseUserQuota(id int, quota int) (err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Update("quota", gorm.Expr("quota - ?", quota)).Error
	if err != nil {
		return err
	}
	return err
}

// DecreaseUserQuotaIfEnough atomically debits a user's quota ONLY when the row
// still holds at least `quota` (WHERE quota >= ?), the symmetric counterpart of
// DecreaseTokenQuotaIfEnough and DebitPoolInTx. It closes the pre-consume
// user-gate TOCTOU: concurrent callers can no longer all pass the
// read-then-compare check and all debit past zero.
//
// Returns ok=false (NOT err) on insufficient balance (RowsAffected == 0). Only
// the NON-ADVISORY pre-consume path calls this; the advisory shadow ledger
// deliberately keeps the unconditional DecreaseUserQuota so its balance may
// still go negative (that drift is what the rollout measures). The general
// DecreaseUserQuota likewise stays unconditional for post-consume
// settlement/compensation.
//
// Cache / batch scope is identical to DecreaseTokenQuotaIfEnough: the
// conditional UPDATE runs directly on the DB (never batch-buffered, which
// cannot be guarded atomically); under Redis the cache is decremented on
// success. The DB row is the authoritative guard.
func DecreaseUserQuotaIfEnough(id int, quota int) (ok bool, err error) {
	if quota < 0 {
		return false, errors.New("quota 不能为负数！")
	}
	result := DB.Model(&User{}).
		Where("id = ? AND quota >= ?", id, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			if cErr := cacheDecrUserQuota(id, int64(quota)); cErr != nil {
				common.SysLog("failed to decrease user quota cache: " + cErr.Error())
			}
		})
	}
	return true, nil
}

func DeltaUpdateUserQuota(id int, delta int) (err error) {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		return IncreaseUserQuota(id, delta, false)
	} else {
		return DecreaseUserQuota(id, -delta)
	}
}

func GetRootUser() (user *User) {
	DB.Where("role = ?", common.RoleRootUser).First(&user)
	return user
}

func UpdateUserUsedQuotaAndRequestCount(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUsedQuota, id, quota)
		addNewRecord(BatchUpdateTypeRequestCount, id, 1)
		return
	}
	updateUserUsedQuotaAndRequestCount(id, quota, 1)
}

// UpdateUserUsedQuota adjusts only the cumulative used_quota, leaving
// request_count untouched. Refunds need exactly this: the async-task submission
// path already counted the request (UpdateUserUsedQuotaAndRequestCount), so a
// later refund must reverse the spend WITHOUT reversing the attempt — the
// request really did happen.
//
// Without this, every failed async task permanently inflated used_quota: the
// refund restored `quota` (the spendable balance) but left the matching
// used_quota increment in place, so `quota + used_quota` drifted above the
// amount the user was ever funded, and every report derived from used_quota
// (dashboard spend, per-channel cost) over-stated reality.
func UpdateUserUsedQuota(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUsedQuota, id, quota)
		return
	}
	updateUserUsedQuota(id, quota)
}

func updateUserUsedQuotaAndRequestCount(id int, quota int, count int) {
	err := DB.Model(&User{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"request_count": gorm.Expr("request_count + ?", count),
		},
	).Error
	if err != nil {
		common.SysLog("failed to update user used quota and request count: " + err.Error())
		return
	}
}

func updateUserUsedQuota(id int, quota int) {
	err := DB.Model(&User{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"used_quota": gorm.Expr("used_quota + ?", quota),
		},
	).Error
	if err != nil {
		common.SysLog("failed to update user used quota: " + err.Error())
	}
}

func updateUserRequestCount(id int, count int) {
	err := DB.Model(&User{}).Where("id = ?", id).Update("request_count", gorm.Expr("request_count + ?", count)).Error
	if err != nil {
		common.SysLog("failed to update user request count: " + err.Error())
	}
}

// GetUsernameById gets username from Redis first, falls back to DB if needed
func GetUsernameById(id int, fromDB bool) (username string, err error) {
	defer func() {
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := updateUserNameCache(id, username); err != nil {
					common.SysLog("failed to update user name cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		username, err := getUserNameCache(id)
		if err == nil {
			return username, nil
		}
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select("username").Find(&username).Error
	if err != nil {
		return "", err
	}
	return username, nil
}

func RootUserExists() bool {
	var user User
	err := DB.Where("role = ?", common.RoleRootUser).First(&user).Error
	if err != nil {
		return false
	}
	return true
}

// ===== Daily Quota Management Functions =====

// GetUserDailyQuotaInfo retrieves daily quota information for a user
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
		info.DailyRemaining = -1 // -1 means unlimited
	}

	info.IsUsingFallback = user.FallbackGroup != "" && user.Group == user.FallbackGroup
	info.NeedsReset = NeedsDailyReset(user.LastDailyReset)

	return info, nil
}

// NeedsDailyReset delegates to entity.NeedsDailyReset
var NeedsDailyReset = entity.NeedsDailyReset

// NeedsDailyResetAt delegates to entity.NeedsDailyResetAt — the clock-injectable
// variant, used for deterministic tests of the daily-reset boundary.
var NeedsDailyResetAt = entity.NeedsDailyResetAt

// IncreaseDailyUsed increases the daily used quota for a user
func IncreaseDailyUsed(userId int, amount int) error {
	if amount < 0 {
		return errors.New("amount cannot be negative")
	}

	// gate cache spawn on RedisEnabled (see IncreaseUserQuota)
	if common.RedisEnabled {
		gopool.Go(func() {
			if err := cacheIncrUserDailyUsed(userId, int64(amount)); err != nil {
				common.SysLog("failed to increase user daily used cache: " + err.Error())
			}
		})
	}

	return DB.Model(&User{}).Where("id = ?", userId).
		Update("daily_used", gorm.Expr("daily_used + ?", amount)).Error
}

// ResetDailyQuota resets daily used quota for a user.
// Idempotent: only resets if last_daily_reset is before today's UTC midnight.
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

	// gate cache spawn on RedisEnabled (see IncreaseUserQuota)
	if common.RedisEnabled {
		gopool.Go(func() {
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

// SwitchToFallbackGroup switches user to fallback group when daily quota exhausted
func SwitchToFallbackGroup(userId int) error {
	var user User
	err := DB.Select("id, fallback_group, \"group\"").Where("id = ?", userId).First(&user).Error
	if err != nil {
		return err
	}

	if user.FallbackGroup == "" || user.Group == user.FallbackGroup {
		return nil
	}

	err = DB.Model(&User{}).Where("id = ?", userId).Update("group", user.FallbackGroup).Error
	if err != nil {
		return err
	}

	// gate cache spawn on RedisEnabled (see IncreaseUserQuota)
	if common.RedisEnabled {
		gopool.Go(func() {
			if err := updateUserGroupCache(userId, user.FallbackGroup); err != nil {
				common.SysLog("failed to update user group cache: " + err.Error())
			}
		})
	}

	return nil
}

// RestoreToBaseGroup restores user to base group (typically after daily reset)
func RestoreToBaseGroup(userId int) error {
	var user User
	err := DB.Select("id, base_group, \"group\"").Where("id = ?", userId).First(&user).Error
	if err != nil {
		return err
	}

	if user.BaseGroup == "" || user.Group == user.BaseGroup {
		return nil
	}

	err = DB.Model(&User{}).Where("id = ?", userId).Update("group", user.BaseGroup).Error
	if err != nil {
		return err
	}

	// gate cache spawn on RedisEnabled (see IncreaseUserQuota)
	if common.RedisEnabled {
		gopool.Go(func() {
			if err := updateUserGroupCache(userId, user.BaseGroup); err != nil {
				common.SysLog("failed to update user group cache: " + err.Error())
			}
		})
	}

	return nil
}

// GetUsersNeedingDailyReset returns users who need daily quota reset
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

// ProcessDailyQuotaReset resets daily quota and restores base group for a user
func ProcessDailyQuotaReset(userId int) error {
	if err := ResetDailyQuota(userId); err != nil {
		return err
	}
	return RestoreToBaseGroup(userId)
}

// IsSubscriber checks if user has subscriber role or higher
func (user *User) IsSubscriber() bool {
	return user.Role >= common.RoleSubscriberUser
}

// GetUserRole returns user role by ID
func GetUserRole(id int) (int, error) {
	var role int
	err := DB.Model(&User{}).Where("id = ?", id).Select("role").Scan(&role).Error
	return role, err
}

// UpdateUserRole updates user role
func UpdateUserRole(id int, role int) error {
	if !common.IsValidateRole(role) {
		return errors.New("invalid role")
	}
	return DB.Model(&User{}).Where("id = ?", id).Update("role", role).Error
}
