package entity

// UserTOTPBackupCode is a one-time recovery code for the TOTP step-up
// factor: totp.BackupCodeCount are minted on TotpConfirm success and on
// each call to the regenerate endpoint, returned to the caller once, and
// consumed one at a time via UniversalVerify's method:"totp_backup" branch.
//
// CodeHash is a one-way SHA-256 digest of "<user_id>:<code>" (see
// internal/app/totp.HashBackupCode) — not reversible the way
// UserTOTP.SecretEncrypted's AES-256-GCM ciphertext is (that field must be
// reversible because the server has to recover the live TOTP secret to
// validate a freshly-generated 6-digit code; a backup code is presented
// once and consumed, so the server never needs the plaintext back). This is
// one-way in construction, not a secrecy guarantee by itself: the code
// space is 31^8 (backupCodeAlphabet, 8 chars) ≈ 2^40, so a DB reader (or
// backup holder) who obtains code_hash can brute-force an unused code
// offline in commodity time — treat this column as sensitive, the same as
// a password-hash column, not as something an unkeyed SHA-256 alone makes
// safe to leak. The user id is mixed into the hash input as a domain
// separator so identical codes minted for two different users cannot
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
