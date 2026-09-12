package repo

import (
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

// user_totp_backup_code.go — persistence for TOTP recovery codes
// (entity.UserTOTPBackupCode). Table created lazily on first use, same
// per-feature pattern as user_totp.go's ensureUserTOTPTable.

func ensureUserTOTPBackupCodeTable() error {
	if DB.Migrator().HasTable(&entity.UserTOTPBackupCode{}) {
		return nil
	}
	return DB.AutoMigrate(&entity.UserTOTPBackupCode{})
}

// ReplaceUserTOTPBackupCodes deletes every existing code for the user and
// inserts rows in their place inside one transaction — replace, not append,
// so TotpConfirm's first issuance and the regenerate endpoint share one code
// path and a reader can never observe a lingering old code beside a fresh
// set. rows must already carry UserId/CodeHash/CreatedAt (UsedAt left at the
// zero value); the caller (handler) owns timestamp generation, mirroring how
// UpsertUserTOTP's caller sets CreatedAt/ConfirmedAt.
func ReplaceUserTOTPBackupCodes(userId int, rows []entity.UserTOTPBackupCode) error {
	if err := ensureUserTOTPBackupCodeTable(); err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userId).Delete(&entity.UserTOTPBackupCode{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

// DeleteUserTOTPBackupCodes removes all of the user's backup codes (used and
// unused) — called from TotpDisable (self-service) and the admin
// force-disable path so neither leaves stray recovery codes behind for a
// factor that no longer exists.
func DeleteUserTOTPBackupCodes(userId int) error {
	if err := ensureUserTOTPBackupCodeTable(); err != nil {
		return err
	}
	return DB.Where("user_id = ?", userId).Delete(&entity.UserTOTPBackupCode{}).Error
}

// CountUnusedUserTOTPBackupCodes returns how many of the user's backup codes
// remain unconsumed (used_at = 0) — GetTotpStatus's backup_codes_remaining.
func CountUnusedUserTOTPBackupCodes(userId int) (int64, error) {
	if err := ensureUserTOTPBackupCodeTable(); err != nil {
		return 0, err
	}
	var n int64
	err := DB.Model(&entity.UserTOTPBackupCode{}).
		Where("user_id = ? AND used_at = 0", userId).Count(&n).Error
	return n, err
}

// ConsumeUserTOTPBackupCode atomically marks one backup code used. It
// returns true only when exactly one row was updated — no read-then-write —
// so a replay race between two requests holding the same code flips at most
// one of them to success.
func ConsumeUserTOTPBackupCode(userId int, codeHash string, usedAt int64) (bool, error) {
	if err := ensureUserTOTPBackupCodeTable(); err != nil {
		return false, err
	}
	res := DB.Model(&entity.UserTOTPBackupCode{}).
		Where("code_hash = ? AND user_id = ? AND used_at = 0", codeHash, userId).
		Update("used_at", usedAt)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// totpTenantFilteredQuery returns a fresh query joining user_totps to users,
// optionally filtered to one tenant. Used by GetTOTPAdoptionStats — a new
// *gorm.DB session per call, so the returned builder is never shared/reused
// across the several counts the caller runs.
func totpTenantFilteredQuery(tenantID string) *gorm.DB {
	q := DB.Table("user_totps AS ut").Joins("JOIN users u ON u.id = ut.user_id")
	if tenantID != "" {
		q = q.Where("u.tenant_id = ?", tenantID)
	}
	return q
}

// TOTPAdoptionStats is the admin security-review payload: how many users
// have TOTP enrolled/pending, and how the enrolled population's backup-code
// coverage splits — issued-none (enrolled before backup codes existed, or
// never regenerated) vs exhausted (every issued code has been consumed).
// There is deliberately no backfill (§8 O2/L6): a user enrolled before this
// lane simply reports NoCodesIssued until they call regenerate.
type TOTPAdoptionStats struct {
	Enrolled      int64
	Pending       int64
	TotalUsers    int64
	NoCodesIssued int64
	Exhausted     int64
}

// GetTOTPAdoptionStats aggregates TOTP adoption, optionally scoped to one
// tenant (tenantID == "" means every tenant).
func GetTOTPAdoptionStats(tenantID string) (*TOTPAdoptionStats, error) {
	if err := ensureUserTOTPTable(); err != nil {
		return nil, err
	}
	if err := ensureUserTOTPBackupCodeTable(); err != nil {
		return nil, err
	}
	stats := &TOTPAdoptionStats{}

	userQuery := DB.Model(&entity.User{})
	if tenantID != "" {
		userQuery = userQuery.Where("tenant_id = ?", tenantID)
	}
	if err := userQuery.Count(&stats.TotalUsers).Error; err != nil {
		return nil, err
	}

	if err := totpTenantFilteredQuery(tenantID).Where("ut.enabled = ?", true).
		Count(&stats.Enrolled).Error; err != nil {
		return nil, err
	}
	if err := totpTenantFilteredQuery(tenantID).Where("ut.enabled = ?", false).
		Count(&stats.Pending).Error; err != nil {
		return nil, err
	}

	// Per enrolled user: total codes ever issued vs still-unused codes. A
	// LEFT JOIN so an enrolled user with zero backup-code rows still
	// produces one group (total_codes=0) instead of vanishing from the set.
	type row struct {
		TotalCodes  int64
		UnusedCodes int64
	}
	var rows []row
	err := totpTenantFilteredQuery(tenantID).
		Joins("LEFT JOIN user_totp_backup_codes bc ON bc.user_id = ut.user_id").
		Where("ut.enabled = ?", true).
		Select("COUNT(bc.id) AS total_codes, COUNT(CASE WHEN bc.used_at = 0 THEN 1 END) AS unused_codes").
		Group("ut.user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		switch {
		case r.TotalCodes == 0:
			stats.NoCodesIssued++
		case r.UnusedCodes == 0:
			stats.Exhausted++
		}
	}
	return stats, nil
}
