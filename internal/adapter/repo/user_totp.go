package repo

import (
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm/clause"
)

// user_totp.go — persistence for the TOTP step-up factor (entity.UserTOTP).
//
// The table is created lazily on first use instead of being added to the
// central migrateDB list (same per-feature pattern as BillingOutbox's
// EnsureSchema): step-up TOTP is written on low-frequency security paths only,
// so a HasTable probe per call is cheap and keeps this feature self-contained.

func ensureUserTOTPTable() error {
	if DB.Migrator().HasTable(&entity.UserTOTP{}) {
		return nil
	}
	return DB.AutoMigrate(&entity.UserTOTP{})
}

// GetUserTOTP returns the user's TOTP record, or (nil, nil) when the user has
// no enrollment. Errors are only returned for real storage failures.
func GetUserTOTP(userId int) (*entity.UserTOTP, error) {
	if err := ensureUserTOTPTable(); err != nil {
		return nil, err
	}
	var rec entity.UserTOTP
	// Find (not First) so "no enrollment" is a plain empty result rather than
	// a gorm.ErrRecordNotFound that spams the SQL error log on every probe.
	res := DB.Where("user_id = ?", userId).Limit(1).Find(&rec)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &rec, nil
}

// UpsertUserTOTP inserts or replaces the user's TOTP record (one row per user).
func UpsertUserTOTP(rec *entity.UserTOTP) error {
	if err := ensureUserTOTPTable(); err != nil {
		return err
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		UpdateAll: true,
	}).Create(rec).Error
}

// DeleteUserTOTP removes the user's enrollment (disable flow).
func DeleteUserTOTP(userId int) error {
	if err := ensureUserTOTPTable(); err != nil {
		return err
	}
	return DB.Where("user_id = ?", userId).Delete(&entity.UserTOTP{}).Error
}
