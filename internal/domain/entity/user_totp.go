package entity

// UserTOTP stores the per-user TOTP (RFC 6238) enrollment used by the
// secure-verification step-up flow. The shared secret is NEVER stored in
// plaintext: SecretEncrypted holds an AES-256-GCM ciphertext produced by
// internal/app/totp (key derived from CRYPTO_SECRET/SESSION_SECRET).
//
// Lives in its own table (one row per user) instead of new columns on the
// users table so the security material stays out of the widely-selected
// user entity and its caches. The table is created lazily by
// internal/adapter/repo/user_totp.go on first use (same per-feature
// AutoMigrate pattern as BillingOutbox).
type UserTOTP struct {
	UserId          int    `json:"user_id" gorm:"primaryKey;column:user_id"`
	SecretEncrypted string `json:"-" gorm:"type:text;column:secret_encrypted"`
	// Enabled is false between enroll and confirm; only a confirmed (enabled)
	// enrollment changes the step-up policy for the user.
	Enabled     bool  `json:"enabled" gorm:"column:enabled;default:false"`
	CreatedAt   int64 `json:"created_at" gorm:"type:bigint;column:created_at"`
	ConfirmedAt int64 `json:"confirmed_at" gorm:"type:bigint;column:confirmed_at;default:0"`
}

func (UserTOTP) TableName() string {
	return "user_totps"
}
