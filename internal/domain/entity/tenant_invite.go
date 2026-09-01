package entity

import "time"

// TenantInvite status constants.
const (
	TenantInviteStatusPending  = 1 // usable
	TenantInviteStatusConsumed = 2 // spent — bound a new bridge login to TenantId
	TenantInviteStatusRevoked  = 3 // root killed it before it was ever used
)

// TenantInvite is a root-issued, one-time onboarding code that binds a
// newly-auto-created zita-bridge user to a specific B-end tenant instead of
// the "default" placeholder every bridge login lands in today (N2). It is
// consulted ONLY on first-ever login — handler.ZitaBootstrap's
// gorm.ErrRecordNotFound branch — so an existing bridged user's TenantId is
// never touched by an invite, no matter what code is presented alongside a
// repeat login.
//
// Schema is managed by migration 032 (tenant_invites) plus AutoMigrate
// (repo.migrateDB); index names in both paths match so whichever runs
// first, the other is a no-op.
//
// AutoMigrate note: a plain Go `int` field maps to Postgres BIGINT (not
// INT) under GORM's default dialector — every *_id / status column here is
// declared BIGINT in the SQL counterpart for the same reason
// provisioned_redemption_batches.batch_size is (migration 027 lesson).
type TenantInvite struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantId string `json:"tenant_id" gorm:"type:varchar(36);not null;index:idx_tenant_invites_tenant"`
	// Code is a 32-hex one-time token — same shape as Redemption.Key
	// (common.GetUUID() strips the UUID's dashes).
	Code string `json:"code" gorm:"type:varchar(32);not null;uniqueIndex:uk_tenant_invites_code"`
	// Status: pending=1 (usable) / consumed=2 (spent) / revoked=3.
	Status int `json:"status" gorm:"not null;default:1"`
	// ExpiredTime is a Unix-seconds timestamp; 0 means no expiry (mirrors
	// Redemption.ExpiredTime's own "0 = never" convention).
	ExpiredTime         int64      `json:"expired_time" gorm:"bigint;not null;default:0"`
	CreatedByUserId     int        `json:"created_by_user_id" gorm:"not null"`
	ConsumedByAccountId *int64     `json:"consumed_by_account_id"`
	ConsumedAt          *time.Time `json:"consumed_at"`
	CreatedAt           time.Time  `json:"created_at" gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (TenantInvite) TableName() string {
	return "tenant_invites"
}
