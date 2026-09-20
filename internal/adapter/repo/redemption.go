package repo

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Redemption = entity.Redemption

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Build query based on keyword type
	query := tx.Model(&Redemption{})

	// Only try to convert to ID if the string represents a valid integer
	if id, err := strconv.Atoi(keyword); err == nil {
		query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
	} else {
		query = query.Where("name LIKE ?", keyword+"%")
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

// GetRedemptionsByTenant lists redemptions for a single tenant. The explicit
// WHERE clause is defence-in-depth — the TenantPlugin's auto-filter would
// also apply in production, but the explicit clause keeps the function
// correct under hermetic tests that don't register the plugin.
func GetRedemptionsByTenant(tenantID string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	db := DB.Model(&Redemption{}).Where("tenant_id = ?", tenantID)

	if err = db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err = db.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

// SearchRedemptionsByTenant is the tenant-scoped sibling of SearchRedemptions.
func SearchRedemptionsByTenant(tenantID string, keyword string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	query := DB.Model(&Redemption{}).Where("tenant_id = ?", tenantID)

	if id, err := strconv.Atoi(keyword); err == nil {
		query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
	} else {
		query = query.Where("name LIKE ?", keyword+"%")
	}

	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id is required")
	}
	redemption := Redemption{Id: id}
	err := DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

// Redemption error codes (cycle13 L3, ERRCODES-6-adjacent): the
// machine-readable taxonomy v2_redemption.go and switch_user_topup.go put on
// the wire alongside the (Chinese, switch-contract-pinned — see the sentinel
// vars below) message text, via RedemptionErrorCode. SwitchRedeemAnonymous
// does NOT send these: its client (the Switch desktop app) classifies by
// greeping the message text, not an error_code field.
const (
	RedemptionErrorCodeInvalid        = "REDEMPTION_INVALID"
	RedemptionErrorCodeUsed           = "REDEMPTION_USED"
	RedemptionErrorCodeExpired        = "REDEMPTION_EXPIRED"
	RedemptionErrorCodeTenantMismatch = "REDEMPTION_TENANT_MISMATCH"
	RedemptionErrorCodeFailed         = "REDEMPTION_FAILED"
)

// Package-level sentinel errors Redeem() returns. Each is returned verbatim
// (never wrapped further) so errors.Is can classify the failure and the
// three handlers (switch_redeem.go / v2_redemption.go / switch_user_topup.go)
// can share one mapping (RedemptionErrorCode / RedemptionErrorMessage) instead
// of three hand-rolled ones.
//
// Text is Chinese by contract, not oversight, for every sentinel EXCEPT
// ErrRedemptionFailed's fallback: the Switch desktop client
// (2c-gui-switch/internal/redemption/redeem.go's classifyRedeemFailure) greps
// these substrings out of the message to pick its localized UI copy, and has
// no error_code field to key off instead — do not translate them. Callers
// that want a machine-readable code use RedemptionErrorCode alongside this
// text; see the "message 文本对 switch 保持含分类子串" decision, cycle13 plan §2.
var (
	// ErrRedemptionInvalid marks a code that does not exist (the Key lookup
	// found no row).
	ErrRedemptionInvalid = errors.New("无效的兑换码")

	// ErrRedemptionUsed marks a code whose Status is not Enabled — already
	// redeemed, or manually disabled (the status check below does not
	// distinguish the two; that split predates this change and is not this
	// cycle's fix). Text is "该兑换码已使用", NOT the pre-cycle13
	// "该兑换码已被使用": the four-character "已被使用" does not contain the
	// three-character substring "已使用" the Switch classifier matches on
	// (已,被,使,用 has no contiguous 已,使,用 run) — every code caught by this
	// branch was misclassified by the client as "does not exist" instead of
	// "used". See cycle13 plan finding #7 and
	// TestRedeemMessagesKeepSwitchClassifierMarkers.
	ErrRedemptionUsed = errors.New("该兑换码已使用")

	// ErrRedemptionExpired marks a code past its ExpiredTime (0 = never
	// expires, handled by the caller before this sentinel is reachable).
	ErrRedemptionExpired = errors.New("该兑换码已过期")

	// ErrRedemptionUserNotFound marks a redeeming user id that does not
	// resolve to a row. Unreachable through the three live call sites today
	// (each passes an already-authenticated or just-created user id);
	// defence-in-depth for a caller that doesn't. Not in the
	// RedemptionErrorCode* four-code list — RedemptionErrorCode folds it into
	// RedemptionErrorCodeFailed, since it is not one of the four states a v2
	// console/API caller needs to branch on individually — but its own text
	// is preserved (the "不存在" substring cycle13 plan §2 protects).
	ErrRedemptionUserNotFound = errors.New("用户不存在")

	// ErrRedemptionWrongTenant marks a code whose TenantId does not match the
	// redeeming user's — see the long comment below for the blast-radius
	// analysis of removing the old "default"-tenant bypass. Note this text
	// does NOT contain any of the Switch classifier's substrings
	// (已使用/过期/禁用/不存在) — a documented, pre-existing gap (see
	// switch_redeem.go's G5a comment), not something this cycle fixes.
	ErrRedemptionWrongTenant = errors.New("该兑换码不属于当前租户")

	// ErrRedemptionFailed is the ONLY error Redeem() returns for anything
	// that is not one of the sentinels above — in particular, a raw
	// transaction/driver failure (a constraint violation, a dropped column, a
	// connection error). Before cycle13, Redeem() wrapped that raw error text
	// verbatim into the returned error's message ("兑换失败，"+err.Error()),
	// and two of its three callers (v2_redemption.go, switch_user_topup.go)
	// echoed err.Error() straight into the HTTP response body — a caller
	// could learn column/constraint names from a 400 response. The raw error
	// is now logged server-side only (common.SysError); every caller sees
	// this fixed, safe text instead. Text matches the pre-existing generic
	// fallback switch_redeem.go's sanitizeRedeemError used to hand-roll
	// ("服务暂不可用，请稍后重试") so TestSwitchRedeemAnonymous_RawDBErrorSanitized's
	// expectation does not change.
	ErrRedemptionFailed = errors.New("服务暂不可用，请稍后重试")
)

// knownRedemptionErrors is every sentinel Redeem() can legitimately return.
// redeemKnownError/RedemptionErrorMessage use it to tell a real sentinel
// apart from a raw driver/transaction error that must not reach a caller.
var knownRedemptionErrors = []error{
	ErrRedemptionInvalid,
	ErrRedemptionUsed,
	ErrRedemptionExpired,
	ErrRedemptionUserNotFound,
	ErrRedemptionWrongTenant,
	ErrRedemptionFailed,
}

// redeemKnownError reports whether err is (wraps) one of knownRedemptionErrors.
func redeemKnownError(err error) bool {
	for _, sentinel := range knownRedemptionErrors {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

// RedemptionErrorCode maps a Redeem() error to the machine-readable
// error_code the v2 console / switch_user_topup handlers put on the wire
// alongside the message text. Anything not in knownRedemptionErrors — which
// should not happen, since Redeem() only ever returns a sentinel from that
// set — also falls back to RedemptionErrorCodeFailed rather than returning "".
func RedemptionErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrRedemptionInvalid):
		return RedemptionErrorCodeInvalid
	case errors.Is(err, ErrRedemptionUsed):
		return RedemptionErrorCodeUsed
	case errors.Is(err, ErrRedemptionExpired):
		return RedemptionErrorCodeExpired
	case errors.Is(err, ErrRedemptionWrongTenant):
		return RedemptionErrorCodeTenantMismatch
	default:
		return RedemptionErrorCodeFailed
	}
}

// RedemptionErrorMessage returns the safe-to-display text for a Redeem()
// error: the sentinel's own text when err is one of knownRedemptionErrors, or
// ErrRedemptionFailed's generic text otherwise. Defense in depth only —
// Redeem() itself never returns anything else — so a future edit to Redeem()
// that starts leaking a raw error again fails safe here instead of leaking it
// to the three handlers that call this.
func RedemptionErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if redeemKnownError(err) {
		return err.Error()
	}
	return ErrRedemptionFailed.Error()
}

func Redeem(key string, userId int) (quota int, err error) {
	if key == "" {
		return 0, errors.New("未提供兑换码")
	}
	if userId == 0 {
		return 0, errors.New("无效的 user id")
	}
	redemption := &Redemption{}

	// PG-only runtime; the SQLite test tier also accepts double-quoted identifiers.
	keyCol := `"key"`
	common.RandomSleep()
	// Use WithoutTenantIsolation because this function does its own explicit tenant check
	err = WithoutTenantIsolation(DB).Transaction(func(tx *gorm.DB) error {
		// clause.Locking is the GORM v2 idiom for SELECT ... FOR UPDATE. The
		// legacy tx.Set("gorm:query_option", "FOR UPDATE") is a silent no-op in
		// v2 (no callback consumes that setting), so the row was never locked
		// and concurrent redeems of the same code could double-credit quota.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(keyCol+" = ?", key).First(redemption).Error
		if err != nil {
			return ErrRedemptionInvalid
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return ErrRedemptionUsed
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return ErrRedemptionExpired
		}

		// Verify user belongs to the same tenant as the redemption code.
		//
		// This used to skip the check entirely for redemption.TenantId ==
		// "default" ("v1 backward compatibility"), which made every code
		// sitting in the platform's own "default" tenant — including
		// anything created without an explicit tenant, since
		// entity.Redemption's TenantId column defaults to "default" — a
		// cross-tenant wildcard: any user of any tenant could redeem it.
		// "default" is not a neutral placeholder here, it's the platform's
		// own tenant, so the bypass amounted to "codes minted for the
		// platform's own console are globally redeemable."
		//
		// Grep of `repo.Redeem(` outside _test.go (2026-08-27) returns three
		// call sites in total — one of which IS the console:
		//   switch_redeem.go:218    POST /api/v2/switch/redeem (anonymous)
		//   switch_user_topup.go:56 POST /api/v2/switch/user/topup (token auth)
		//   v2_redemption.go:88     RedeemCodeV2, mounted twice — at
		//                           api-v2-router.go:188 (POST
		//                           /api/v2/:tenant_slug/redeem) and at
		//                           api-router.go:60 (v1-compat POST
		//                           /api/user/topup). This is the console path.
		// Only the first is gated ahead of this check — see
		// switchRedeemAllowDefaultTenant in switch_redeem.go, which refuses a
		// "default"-tenant code before it ever reaches here. The other two
		// have no such pre-gate: this unconditional check IS their only
		// tenant guard, and they have no SWITCH_REDEEM_ALLOW_DEFAULT_TENANT
		// escape hatch, so for them the change is not reversible without a
		// code change.
		//
		// BLAST RADIUS the operator must sign off on (measured 2026-08-27, not
		// inferred): the v1 admin console mints codes into "default"
		// unconditionally. handler/redemption.go's AddRedemption reads
		// tenant_id off the gin context and falls back to "default" when it is
		// absent; the only non-test writers of that context key are
		// middleware/oidc_auth.go (3 sites) and repo/tenant_context.go, and
		// `POST /api/redemption/` runs none of them — its chain is CORS +
		// GlobalAPIRateLimit + RequestBodySizeLimit + AdminAuth
		// (api-router.go's redemptionRoute), and AdminAuth delegates to
		// authHelper, which never sets tenant_id. So every code that route
		// produces carries TenantId="default", and after this change no user
		// outside the "default" tenant can redeem one through any of the three
		// call sites above. Minting for a reseller tenant has to go through a
		// tenant-scoped path.
		var user User
		if err := tx.Where("id = ?", userId).First(&user).Error; err != nil {
			return ErrRedemptionUserNotFound
		}
		if user.TenantId != redemption.TenantId {
			common.SysError(fmt.Sprintf("Tenant mismatch in Redeem: redemption.TenantId=%s, user.TenantId=%s", redemption.TenantId, user.TenantId))
			return ErrRedemptionWrongTenant
		}

		err = tx.Model(&User{}).Where("id = ?", userId).Update("quota", gorm.Expr("quota + ?", redemption.Quota)).Error
		if err != nil {
			return err
		}
		redemption.RedeemedTime = common.GetTimestamp()
		redemption.Status = common.RedemptionCodeStatusUsed
		redemption.UsedUserId = userId
		err = tx.Save(redemption).Error
		return err
	})
	if err != nil {
		if redeemKnownError(err) {
			return 0, err
		}
		// A raw transaction/driver failure — e.g. a constraint violation on
		// the Update/Save calls above. Log the real text server-side only;
		// see ErrRedemptionFailed's doc comment for why the caller must never
		// see it.
		common.SysError("redeem: transaction failed: " + err.Error())
		return 0, ErrRedemptionFailed
	}
	// The quota was written straight to the row inside the transaction, so the
	// cached copy is now stale-low and GetUserQuota(id, false) would keep
	// serving the pre-topup balance until the key expires — the user redeems a
	// code and still gets refused for insufficient quota. IncreaseUserQuota
	// (user.go) is the path that normally keeps the two in step; do the same
	// here, after the commit so a rollback can never leave the cache ahead of
	// the row. If the increment fails, drop the key so the next read falls
	// through to the database rather than trusting a stale hash.
	if common.RedisEnabled {
		if cacheErr := cacheIncrUserQuota(userId, int64(redemption.Quota)); cacheErr != nil {
			common.SysLog("redeem: failed to increase cached user quota: " + cacheErr.Error())
			if invErr := invalidateUserCache(userId); invErr != nil {
				common.SysLog("redeem: failed to invalidate stale user cache: " + invErr.Error())
			}
		}
	}
	RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码充值 %s，兑换码ID %d", logger.LogQuota(redemption.Quota), redemption.Id))
	return redemption.Quota, nil
}

func RedemptionInsert(redemption *Redemption) error {
	return WithTenantID(DB, redemption.TenantId).Create(redemption).Error
}

func RedemptionSelectUpdate(redemption *Redemption) error {
	// This can update zero values
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// RedemptionUpdate Make sure your token's fields is completed, because this will update non-zero values
func RedemptionUpdate(redemption *Redemption) error {
	return DB.Model(redemption).Select("name", "status", "quota", "redeemed_time", "expired_time").Updates(redemption).Error
}

func RedemptionDelete(redemption *Redemption) error {
	return DB.Delete(redemption).Error
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return errors.New("id is required")
	}
	redemption := Redemption{Id: id}
	err = DB.Where(redemption).First(&redemption).Error
	if err != nil {
		return err
	}
	return RedemptionDelete(&redemption)
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}

// DeleteInvalidRedemptionsByTenant is the tenant-scoped sibling of
// DeleteInvalidRedemptions: AdminAuth is satisfied by a per-tenant admin, so a
// non-root prune of spent/expired codes must stay inside the caller's tenant.
// Root uses the unscoped variant for a global sweep. Mirrors
// DeleteDisabledChannelByTenant.
func DeleteInvalidRedemptionsByTenant(tenantID string) (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("tenant_id = ? AND (status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?))", tenantID, []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
