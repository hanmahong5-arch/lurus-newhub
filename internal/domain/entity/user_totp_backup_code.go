package entity

// UserTOTPBackupCode is a one-time recovery code for the TOTP step-up
// factor: 10 are minted on TotpConfirm success (and on every regenerate),
// returned to the caller once, and consumed one at a time via
// UniversalVerify's method:"totp_backup" branch.
//
// CodeHash is a one-way SHA-256 digest of "<user_id>:<code>" (see
// internal/app/totp.HashBackupCode) — never reversible. This is
// deliberately different from UserTOTP.SecretEncrypted, which is AES-256-GCM
// (reversible) because the server must recover the live TOTP secret to
// validate a freshly-generated 6-digit code; a backup code is presented
// once and consumed, so the server never needs the plaintext back, and a
// one-way hash means not even an admin with the deployment's CRYPTO_SECRET
// can recover an unused code. The user id is mixed into the hash input as a
// domain separator so identical codes minted for two different users cannot
// collide on the unique index below.
//
// Table user_totp_backup_codes, created lazily by
// internal/adapter/repo/user_totp_backup_code.go on first use — same
// per-feature AutoMigrate pattern as its sibling, entity/user_totp.go.
type UserTOTPBackupCode struct {
	Id        int64  `json:"id" gorm:"primaryKey;autoIncrement;column:id"`
	UserId    int    `json:"user_id" gorm:"column:user_id;index"`
	CodeHash  string `json:"-" gorm:"column:code_hash;type:varchar(64);uniqueIndex"`
	CreatedAt int64  `json:"created_at" gorm:"column:created_at;type:bigint"`
	// UsedAt is 0 while the code is still redeemable. UniversalVerify's
	// backup-code branch consumes a code with a single
	// UPDATE … SET used_at=? WHERE code_hash=? AND user_id=? AND used_at=0,
	// requiring rows-affected 1 — no read-then-write, so a replay race
	// between two requests holding the same code can flip at most one of
	// them to success (fail-closed anti-replay).
	UsedAt int64 `json:"used_at" gorm:"column:used_at;type:bigint;default:0"`
}

func (UserTOTPBackupCode) TableName() string {
	return "user_totp_backup_codes"
}
