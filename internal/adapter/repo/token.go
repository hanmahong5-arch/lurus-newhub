package repo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

// Canonical definition: domain/entity/token.go
type Token struct {
	Id                 int     `json:"id"`
	UserId             int     `json:"user_id" gorm:"index;index:idx_tenant_user,priority:2"`
	TenantId           string  `json:"tenant_id" gorm:"type:varchar(36);index;index:idx_tenant_user,priority:1;default:'default'"` // Tenant isolation
	Key                string  `json:"key" gorm:"type:char(48);uniqueIndex"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index" `
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	AccessedTime       int64   `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64   `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int     `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool    `json:"unlimited_quota"`
	ModelLimitsEnabled bool    `json:"model_limits_enabled"`
	ModelLimits        string  `json:"model_limits" gorm:"type:varchar(1024);default:''"`
	AllowIps           *string `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int     `json:"used_quota" gorm:"default:0"` // used quota
	Group              string  `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool    `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	// Scopes is a comma-separated allowlist of relay scopes (see
	// pkg/types/token_scope.go). Empty = no restriction (backward compat).
	// Migration 015 introduced the column. ADR Phase E2.
	Scopes            string `json:"scopes" gorm:"type:varchar(255);default:''"`
	IdentityAccountID int64  `json:"identity_account_id" gorm:"default:0;index:idx_identity_account,where:identity_account_id > 0"` // lurus-platform account ID
	// CreatorUserId is users.id of the Reseller who issued this key via the
	// Provisioning API. 0 = legacy / non-provisioned. ADR 2026-05-18 §3.3.
	CreatorUserId int `json:"creator_user_id" gorm:"default:0;index:idx_tokens_creator_user_id,where:creator_user_id > 0"`
	// LastUsedAt is updated on relay hit for Provisioning-issued keys.
	// Unix seconds. 0 = never used.
	LastUsedAt int64 `json:"last_used_at" gorm:"default:0"`
	// AutoRotateDays enables scheduled key rotation: when > 0 the key is
	// automatically rotated every AutoRotateDays days by the secret-rotation
	// lifecycle task. 0 disables it. Migration 017, ADR Phase H1.4.
	AutoRotateDays int `json:"auto_rotate_days" gorm:"default:0"`
	// RotatedAt is the unix-second time of the last automatic rotation; 0 means
	// never auto-rotated (the rotation interval is then measured from
	// CreatedTime).
	RotatedAt int64 `json:"rotated_at" gorm:"default:0"`
	// RateLimitRPM / RateLimitTPM mirror domain/entity/token.go (canonical
	// docs live there): per-minute request / LLM-token caps enforced by
	// middleware.BusinessRateLimit, 0 = unlimited (migration 023 columns).
	// Duplicated here because the token create/update handlers bind and
	// persist through this independent struct.
	RateLimitRPM int `json:"rate_limit_rpm" gorm:"column:rpm_limit;default:0"`
	RateLimitTPM int `json:"rate_limit_tpm" gorm:"column:tpm_limit;default:0"`
	// ProjectId mirrors domain/entity/token.go (canonical docs live there):
	// cost-attribution label, 0 = unassigned (migration 029). Duplicated here
	// because the token create/update handlers bind and persist through this
	// independent struct — the tag MUST stay byte-identical to the entity one.
	ProjectId int            `json:"project_id" gorm:"not null;default:0"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (token *Token) Clean() {
	token.Key = ""
}

// QuotaAvailable reports whether the token's OWN spending cap still has
// room: an unlimited token always has room; a limited one needs
// RemainQuota > 0. ValidateUserToken's Status==TokenStatusExhausted branch
// below and middleware.TokenAuth's 402-hint branch both call this single
// definition so "does this token still have quota" can never drift apart
// between the two files — a prior defect had each site re-derive the same
// boolean expression independently, and deleting one copy's UnlimitedQuota
// check left the other package's tests fully green.
func (token *Token) QuotaAvailable() bool {
	return token.UnlimitedQuota || token.RemainQuota > 0
}

func (token *Token) GetIpLimits() []string {
	// delete empty spaces
	//split with \n
	ipLimits := make([]string, 0)
	if token.AllowIps == nil {
		return ipLimits
	}
	cleanIps := strings.ReplaceAll(*token.AllowIps, " ", "")
	if cleanIps == "" {
		return ipLimits
	}
	ips := strings.Split(cleanIps, "\n")
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		ip = strings.ReplaceAll(ip, ",", "")
		if ip != "" {
			ipLimits = append(ipLimits, ip)
		}
	}
	return ipLimits
}

// GetScopes returns the token's scope allowlist as a slice with whitespace
// trimmed and empty entries dropped. nil/empty result means no restriction.
func (token *Token) GetScopes() []string {
	if token.Scopes == "" {
		return nil
	}
	parts := strings.Split(token.Scopes, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// HasScope reports whether the token is authorized for the given scope.
// An empty Scopes field is treated as "no restriction" (backward compat
// with every token issued before migration 015) — HasScope returns true.
func (token *Token) HasScope(scope string) bool {
	scopes := token.GetScopes()
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

func SearchUserTokens(userId int, keyword string, token string) (tokens []*Token, err error) {
	if token != "" {
		token = strings.Trim(token, "sk-")
	}
	err = DB.Where("user_id = ?", userId).Where("name LIKE ?", "%"+keyword+"%").Where(commonKeyCol+" LIKE ?", "%"+token+"%").Find(&tokens).Error
	return tokens, err
}

// ErrTokenQuotaExhausted is the sentinel for both quota-exhaustion paths in
// ValidateUserToken (Status==TokenStatusExhausted and the live
// !UnlimitedQuota && RemainQuota<=0 downgrade). Callers use errors.Is to
// detect it and answer with 402 instead of the generic 401 the other
// ValidateUserToken failures get — see middleware.TokenAuth. Both paths still
// wrap this sentinel even when the Status==TokenStatusExhausted branch's
// RemainQuota is actually positive (see the branch below) — the caller still
// needs the 402 token-management guidance, just with a different message.
var ErrTokenQuotaExhausted = errors.New("令牌不可用")

// tokenExhaustedMessage builds the human-readable 402 guidance for a token
// that has genuinely run out of its own spending cap (QuotaAvailable() ==
// false). Both the Status==TokenStatusExhausted branch below and the live
// RemainQuota<=0 downgrade call this single definition so the two call
// sites can never render diverging text for what must be the identical
// caller-facing state (TestL3ValidateUserToken_BothBranches_SameSuffix
// pins the two outputs equal). remainQuota is embedded as a raw integer —
// same figure/unit as the metadata's token_remain_quota_units — so the
// wire message itself carries a number instead of forcing the caller to
// parse metadata for one.
func tokenExhaustedMessage(remainQuota int) error {
	return fmt.Errorf("%w（该令牌可用额度已用尽 [剩余 %d]，请修改令牌剩余额度或设置为无限额度）", ErrTokenQuotaExhausted, remainQuota)
}

func ValidateUserToken(key string) (token *Token, err error) {
	if key == "" {
		return nil, errors.New("未提供令牌")
	}
	token, err = GetTokenByKey(key, false)
	if err == nil {
		if token.Status == common.TokenStatusExhausted {
			// Status==TokenStatusExhausted does NOT imply RemainQuota<=0: an
			// admin can raise remain_quota (handler/token.go's
			// app.ApplyTokenUpdate copies RemainQuota from the update
			// request) without also flipping Status back to Enabled —
			// CanEnableToken/the enable transition only runs when the
			// request body explicitly sets status=Enabled (token.go:230).
			// Asserting "额度已用尽，请修改剩余额度" in that state is false
			// (the metadata this error feeds — see
			// types.ErrOptionWithTokenDisabledHint — already reports the
			// raised remain_quota) and points the caller at a field that's
			// already fine; the real remedy is re-enabling the token.
			if token.QuotaAvailable() {
				remainDesc := "无限额度"
				if !token.UnlimitedQuota {
					remainDesc = fmt.Sprintf("%d", token.RemainQuota)
				}
				return token, fmt.Errorf("%w（该令牌剩余额度充足 [%s]，但令牌当前处于已禁用状态，请前往令牌管理页重新启用）", ErrTokenQuotaExhausted, remainDesc)
			}
			return token, tokenExhaustedMessage(token.RemainQuota)
		} else if token.Status == common.TokenStatusExpired {
			return token, errors.New("该令牌已过期")
		}
		if token.Status != common.TokenStatusEnabled {
			return token, errors.New("该令牌状态不可用")
		}
		if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
			if !common.RedisEnabled {
				token.Status = common.TokenStatusExpired
				err := token.SelectUpdate()
				if err != nil {
					common.SysLog("failed to update token status" + err.Error())
				}
			}
			return token, errors.New("该令牌已过期")
		}
		if !token.UnlimitedQuota && token.RemainQuota <= 0 {
			if !common.RedisEnabled {
				// in this case, we can make sure the token is exhausted
				token.Status = common.TokenStatusExhausted
				err := token.SelectUpdate()
				if err != nil {
					common.SysLog("failed to update token status" + err.Error())
				}
			}
			return token, tokenExhaustedMessage(token.RemainQuota)
		}
		return token, nil
	}
	common.SysLog("ValidateUserToken: failed to get token: " + err.Error())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("无效的令牌")
	} else {
		return nil, errors.New("无效的令牌，数据库查询出错，请联系管理员")
	}
}

func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	err := DB.First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	token := Token{Id: id}
	err := DB.First(&token, "id = ?", id).Error
	if shouldUpdateRedis(true, err) {
		gopool.Go(func() {
			if err := cacheSetToken(token); err != nil {
				common.SysLog("failed to update user status cache: " + err.Error())
			}
		})
	}
	return &token, err
}

func GetTokenByKey(key string, fromDB bool) (token *Token, err error) {
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) && token != nil {
			gopool.Go(func() {
				if err := cacheSetToken(*token); err != nil {
					common.SysLog("failed to update user status cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		// Try Redis first
		token, err := cacheGetTokenByKey(key)
		if err == nil {
			return token, nil
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	err = DB.Where(commonKeyCol+" = ?", key).First(&token).Error
	return token, err
}

func (token *Token) Insert() error {
	var err error
	err = WithTenantID(DB, token.TenantId).Create(token).Error
	return err
}

// AutoCreateDefaultToken creates a default unlimited-quota token for a user
// so the playground and other session-based features work without manual setup.
func AutoCreateDefaultToken(userId int) (*Token, error) {
	key, err := common.GenerateRandomKey(48)
	if err != nil {
		return nil, fmt.Errorf("generate token key: %w", err)
	}
	token := &Token{
		UserId:         userId,
		TenantId:       "default",
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "default",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1, // never expires
		RemainQuota:    0,
		UnlimitedQuota: true,
		Group:          "",
	}
	if err := token.Insert(); err != nil {
		return nil, fmt.Errorf("insert token: %w", err)
	}
	return token, nil
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() (err error) {
	defer func() {
		if shouldUpdateRedis(true, err) {
			gopool.Go(func() {
				err := cacheSetToken(*token)
				if err != nil {
					common.SysLog("failed to update token cache: " + err.Error())
				}
			})
		}
	}()
	// NOTE: "project_id" MUST stay in this allow-list. Omitting it does not
	// fail — it silently drops the write, while the deferred cacheSetToken
	// above still pushes the NEW value into Redis. The relay would then
	// attribute spend to the new project until the cache entry expires and
	// then flip back to the stale DB value: non-deterministic attribution
	// drift that is close to unreproducible.
	err = DB.Model(token).Select("name", "status", "expired_time", "remain_quota", "unlimited_quota",
		"model_limits_enabled", "model_limits", "allow_ips", "group", "cross_group_retry", "scopes",
		"rpm_limit", "tpm_limit", "project_id").Updates(token).Error
	return err
}

func (token *Token) SelectUpdate() (err error) {
	defer func() {
		if shouldUpdateRedis(true, err) {
			gopool.Go(func() {
				err := cacheSetToken(*token)
				if err != nil {
					common.SysLog("failed to update token cache: " + err.Error())
				}
			})
		}
	}()
	// This can update zero values
	return DB.Model(token).Select("accessed_time", "status").Updates(token).Error
}

// RotateKey replaces the token's key atomically, updating the cache entry. The
// manual rotation path deliberately leaves rotated_at untouched.
func (token *Token) RotateKey(newKey string) error {
	return token.rotateKey(newKey, map[string]interface{}{"key": newKey})
}

// RotateKeyWithTimestamp rotates the key and stamps rotated_at in a single
// update. Used by the automatic rotation task so the next rotation is measured
// from this moment.
func (token *Token) RotateKeyWithTimestamp(newKey string, rotatedAt int64) error {
	token.RotatedAt = rotatedAt
	return token.rotateKey(newKey, map[string]interface{}{"key": newKey, "rotated_at": rotatedAt})
}

// ErrRotationRaceLost is returned by RotateKeyWithTimestampCAS when the
// compare-and-swap guard found that another writer already advanced the
// token's rotated_at past the observed baseline — i.e. this rotation lost the
// race and must NOT rotate again. Callers treat it as a benign skip, not a
// failure: the key was already rotated by the winning writer.
var ErrRotationRaceLost = errors.New("token rotation race lost: rotated_at advanced concurrently")

// RotateKeyWithTimestampCAS is the concurrency-safe variant of
// RotateKeyWithTimestamp. It only rotates if the token's persisted rotated_at
// still equals prevRotatedAt (the value the caller observed before deciding
// the token was due; pass the raw persisted RotatedAt — which is 0 for a
// never-rotated token — not the CreatedTime fallback used for due-ness). The
// UPDATE carries that guard in its WHERE clause, so two racing rotators —
// e.g. the scheduled leader task and a manual /internal/admin/rotate-due-tokens
// trigger, or a brief split-brain where two replicas both believe they are
// leader — cannot both rotate the same token: the conditional UPDATE
// serializes them and the loser sees RowsAffected == 0.
//
// On a successful swap the in-memory token.Key / token.RotatedAt are advanced
// and the cache is refreshed (same as rotateKey). On a lost race nothing is
// mutated and the old cache entry is left intact (the winner refreshed it).
func (token *Token) RotateKeyWithTimestampCAS(newKey string, prevRotatedAt, newRotatedAt int64) error {
	oldKey := token.Key
	result := DB.Model(&Token{}).
		Where("id = ? AND rotated_at = ?", token.Id, prevRotatedAt).
		Updates(map[string]interface{}{"key": newKey, "rotated_at": newRotatedAt})
	if result.Error != nil {
		return fmt.Errorf("rotate token CAS update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// Either the row vanished or rotated_at no longer matches the baseline:
		// a concurrent writer won. Do not mutate in-memory state or cache.
		return ErrRotationRaceLost
	}

	// CAS won: advance in-memory state and refresh the cache for old+new key.
	token.Key = newKey
	token.RotatedAt = newRotatedAt
	if shouldUpdateRedis(true, nil) {
		gopool.Go(func() {
			_ = cacheDeleteToken(oldKey)
			if e := cacheSetToken(*token); e != nil {
				common.SysLog("failed to update token cache after CAS rotation: " + e.Error())
			}
		})
	}
	return nil
}

// rotateKey applies the given column updates while swapping token.Key in place,
// then invalidates the token cache for the old and new key. Shared by RotateKey
// and RotateKeyWithTimestamp so the cache-invalidation logic lives in one place.
func (token *Token) rotateKey(newKey string, updates map[string]interface{}) (err error) {
	oldKey := token.Key
	token.Key = newKey
	defer func() {
		if shouldUpdateRedis(true, err) {
			gopool.Go(func() {
				_ = cacheDeleteToken(oldKey)
				if e := cacheSetToken(*token); e != nil {
					common.SysLog("failed to update token cache after rotation: " + e.Error())
				}
			})
		}
	}()
	err = DB.Model(token).Updates(updates).Error
	return err
}

// ListAutoRotateTokens returns enabled tokens that have auto-rotation enabled
// (auto_rotate_days > 0). The secret-rotation lifecycle task scans these to
// find the ones due for rotation. Bounded by the number of opted-in tokens.
func ListAutoRotateTokens() ([]*Token, error) {
	var tokens []*Token
	err := DB.Where("auto_rotate_days > 0").
		Where("status = ?", common.TokenStatusEnabled).
		Find(&tokens).Error
	return tokens, err
}

func (token *Token) Delete() (err error) {
	defer func() {
		if shouldUpdateRedis(true, err) {
			gopool.Go(func() {
				err := cacheDeleteToken(token.Key)
				if err != nil {
					common.SysLog("failed to delete token cache: " + err.Error())
				}
			})
		}
	}()
	err = DB.Delete(token).Error
	return err
}

func (token *Token) IsModelLimitsEnabled() bool {
	return token.ModelLimitsEnabled
}

func (token *Token) GetModelLimits() []string {
	if token.ModelLimits == "" {
		return []string{}
	}
	return strings.Split(token.ModelLimits, ",")
}

func (token *Token) GetModelLimitsMap() map[string]bool {
	limits := token.GetModelLimits()
	limitsMap := make(map[string]bool)
	for _, limit := range limits {
		limitsMap[limit] = true
	}
	return limitsMap
}

func DisableModelLimits(tokenId int) error {
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	return token.Update()
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	err = DB.Where(token).First(&token).Error
	if err != nil {
		return err
	}
	return token.Delete()
}

func IncreaseTokenQuota(id int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			err := cacheIncrTokenQuota(key, int64(quota))
			if err != nil {
				common.SysLog("failed to increase token quota: " + err.Error())
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, id, quota)
		return nil
	}
	return increaseTokenQuota(id, quota)
}

func increaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota + ?", quota),
			"used_quota":    gorm.Expr("used_quota - ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

func DecreaseTokenQuota(id int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			err := cacheDecrTokenQuota(key, int64(quota))
			if err != nil {
				common.SysLog("failed to decrease token quota: " + err.Error())
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, id, -quota)
		return nil
	}
	return decreaseTokenQuota(id, quota)
}

func decreaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

// DecreaseTokenQuotaIfEnough atomically debits a token's remain_quota ONLY when
// the row still holds at least `quota`, mirroring DebitPoolInTx's conditional
// UPDATE (WHERE ... >= ?) + RowsAffected guard. It is the pre-consume gate's
// race-safe replacement for the read-compare-then-DecreaseTokenQuota sequence
// (the token-gate TOCTOU): N concurrent pre-consume calls can no longer all
// read the same remain_quota, all pass a Go-level check, and all debit past
// zero.
//
// Returns ok=false (NOT err) when the balance was insufficient (RowsAffected
// == 0) — the caller maps that to the existing insufficient-quota rejection.
// A real DB failure returns err.
//
// This is intentionally NOT a drop-in for the general DecreaseTokenQuota, which
// stays unconditional because PostConsumeQuota's settlement/compensation must
// debit the ACTUAL cost even past a cap (overdraft is recorded, never refused).
//
// Cache / batch scope (honest):
//   - The conditional UPDATE always runs directly on the DB, even when
//     BatchUpdateEnabled — a batch-buffered decrement has no committed row to
//     guard, so buffering it would defeat the atomicity. Decrements still
//     pending in the batch buffer are invisible to the WHERE clause, the same
//     staleness the pre-batch read already carried; BatchUpdateEnabled is off
//     by default for this money path.
//   - Under Redis the cache is decremented on success to stay consistent with
//     the DB; a concurrent caller's cache read may be momentarily stale, but
//     the DB conditional UPDATE is the authoritative guard that keeps
//     remain_quota from going negative regardless.
func DecreaseTokenQuotaIfEnough(id int, key string, quota int) (ok bool, err error) {
	if quota < 0 {
		return false, errors.New("quota 不能为负数！")
	}
	result := DB.Model(&Token{}).
		Where("id = ? AND remain_quota >= ?", id, quota).
		Updates(map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			if cErr := cacheDecrTokenQuota(key, int64(quota)); cErr != nil {
				common.SysLog("failed to decrease token quota cache: " + cErr.Error())
			}
		})
	}
	return true, nil
}

// CountUserTokens returns total number of tokens for the given user, used for pagination
func CountUserTokens(userId int) (int64, error) {
	var total int64
	err := DB.Model(&Token{}).Where("user_id = ?", userId).Count(&total).Error
	return total, err
}

// GetUserTokensByTenant returns tokens for the given user scoped to a specific
// tenant. This is the v2 list function — the explicit tenant_id clause is
// defence-in-depth (TenantPlugin also filters, but the explicit clause keeps
// hermetic tests honest without the plugin registered).
func GetUserTokensByTenant(userID int, tenantID string, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	err := DB.Where("user_id = ? AND tenant_id = ?", userID, tenantID).
		Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

// CountUserTokensByTenant returns the total token count for the given user
// within the specified tenant. Pairs with GetUserTokensByTenant for pagination.
func CountUserTokensByTenant(userID int, tenantID string) (int64, error) {
	var total int64
	err := DB.Model(&Token{}).Where("user_id = ? AND tenant_id = ?", userID, tenantID).Count(&total).Error
	return total, err
}

// ProvisionedAllCreators is the creatorUserID sentinel meaning "no creator
// filter" — reserved for ScopeAll (platform-admin) callers, whose LIST must
// match their revoke reach (RevokeProvisionedKey skips the creator check for
// ScopeAll). 0 cannot be the sentinel: it is a real stored value (legacy
// tokens), so filtering by 0 must keep meaning "creator_user_id = 0".
const ProvisionedAllCreators = -1

// GetProvisionedTokensByTenant returns Tokens issued via the Provisioning API
// for a specific (Reseller user, tenant) pair, ordered by id DESC. Used by
// GET /internal/v1/provisioning/tenants/:slug/keys (Tier 1.1, 2026-05-19).
//
// Filters:
//   - creator_user_id = creatorUserID limits results to keys minted by THIS
//     Reseller — keeps one Reseller's LIST output from leaking another's
//     keys even when both happen to write to the same tenant via separate
//     Provisioning keys. Legacy tokens (creator_user_id = 0) never show up.
//     ProvisionedAllCreators (-1) disables the filter (ScopeAll callers) so
//     the list reach stays symmetric with the revoke reach.
//   - tenant_id = tenantID narrows to the URL-bound tenant scope.
//   - includeRevoked=false adds status = TokenStatusEnabled. GORM's
//     soft-delete (DeletedAt) is always applied by Find — revoked rows
//     would otherwise leak via re-enabled status anomalies.
//
// limit / offset are clamped at the handler; the repo trusts the window.
func GetProvisionedTokensByTenant(creatorUserID int, tenantID string, includeRevoked bool, offset, limit int) ([]*Token, error) {
	var tokens []*Token
	q := DB.Where("tenant_id = ?", tenantID)
	if creatorUserID == ProvisionedAllCreators {
		// Still a PROVISIONED-keys listing: creator_user_id > 0 keeps
		// user-created tokens (creator 0) out even for ScopeAll callers.
		q = q.Where("creator_user_id > 0")
	} else {
		q = q.Where("creator_user_id = ?", creatorUserID)
	}
	if !includeRevoked {
		q = q.Where("status = ?", common.TokenStatusEnabled)
	}
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&tokens).Error
	return tokens, err
}

// CountProvisionedTokensByTenant returns the total row count matching
// GetProvisionedTokensByTenant — used for pagination metadata in the
// LIST response. Filter semantics MUST stay in sync with the Get above.
func CountProvisionedTokensByTenant(creatorUserID int, tenantID string, includeRevoked bool) (int64, error) {
	var total int64
	q := DB.Model(&Token{}).Where("tenant_id = ?", tenantID)
	if creatorUserID == ProvisionedAllCreators {
		q = q.Where("creator_user_id > 0")
	} else {
		q = q.Where("creator_user_id = ?", creatorUserID)
	}
	if !includeRevoked {
		q = q.Where("status = ?", common.TokenStatusEnabled)
	}
	err := q.Count(&total).Error
	return total, err
}

// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, errors.New("ids 不能为空！")
	}

	tx := DB.Begin()

	var tokens []Token
	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Find(&tokens).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Delete(&Token{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	if common.RedisEnabled {
		gopool.Go(func() {
			for _, t := range tokens {
				_ = cacheDeleteToken(t.Key)
			}
		})
	}

	return len(tokens), nil
}
