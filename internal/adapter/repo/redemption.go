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
		return nil, errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	err := DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
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
			return errors.New("无效的兑换码")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码已被使用")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("该兑换码已过期")
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
			return errors.New("用户不存在")
		}
		if user.TenantId != redemption.TenantId {
			common.SysError(fmt.Sprintf("Tenant mismatch in Redeem: redemption.TenantId=%s, user.TenantId=%s", redemption.TenantId, user.TenantId))
			return errors.New("该兑换码不属于当前租户")
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
		return 0, errors.New("兑换失败，" + err.Error())
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
		return errors.New("id 为空！")
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
