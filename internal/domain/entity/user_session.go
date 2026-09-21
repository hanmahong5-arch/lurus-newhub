package entity

// UserSession is one row per gin-contrib/sessions session belonging to a
// cookie-authenticated user — the per-device session registry behind
// SESSION_REGISTRY_ENABLED (L7, auth-security-08/26/29). SessionKey is the
// session store's own session.ID(); on the Redis-backed store (production,
// UAT) that is also the suffix of the Redis key "session_"+SessionKey
// (redistore.go's default keyPrefix), so a revoke can delete the
// authoritative session out of Redis using this column alone. On a
// cookie-only store (no Redis configured) session.ID() is always "" and no
// row is ever written — the feature is then a no-op, not a partial one.
//
// Schema is managed by migration 033 (user_sessions) plus AutoMigrate
// (repo.migrateDB); index names in both paths match so whichever runs
// first, the other is a no-op (same convention as TenantInvite/migration
// 032).
type UserSession struct {
	Id int `json:"id" gorm:"primaryKey;autoIncrement"`
	// SessionKey is unique per session store entry — see the type comment.
	SessionKey string `json:"session_key" gorm:"type:varchar(128);not null;uniqueIndex:uk_user_sessions_session_key"`
	UserId     int    `json:"user_id" gorm:"not null;index:idx_user_sessions_user_revoked,priority:1"`
	// TenantId is a snapshot of the user's tenant at first sight, purely
	// informational — a session belongs to one user account, and a user
	// account belongs to exactly one tenant for its whole life, so this is
	// never used as an isolation filter (ownership is checked by UserId).
	TenantId string `json:"tenant_id" gorm:"type:varchar(36);not null;default:'default'"`
	// IP and UserAgent are the raw values as seen at first sight ONLY — they
	// are never re-serialised raw to a client; every caller that surfaces
	// them (ListSessionsV2) MUST mask/coarsen first (see MaskIP /
	// UserAgentFamily in repo/user_session.go). Truncated to the column
	// width by the writer, never by the DB (Postgres would just error on
	// overflow instead of truncating).
	IP         string `json:"ip" gorm:"type:varchar(45);not null;default:''"`
	UserAgent  string `json:"user_agent" gorm:"type:varchar(255);not null;default:''"`
	AuthMethod string `json:"auth_method" gorm:"type:varchar(32);not null;default:''"`
	// CreatedAt/LastSeenAt are Unix-seconds, not time.Time — mirrors every
	// other *_at column in this repo that isn't a GORM soft-delete marker
	// (Token.CreatedTime, Redemption.CreatedTime, TenantInvite is the one
	// recent exception because it mirrors Redemption's own time.Time use;
	// this table follows the more common convention instead).
	CreatedAt  int64 `json:"created_at" gorm:"not null"`
	LastSeenAt int64 `json:"last_seen_at" gorm:"not null;index:idx_user_sessions_last_seen"`
	// RevokedAt is 0 while active; a nonzero Unix-seconds timestamp marks the
	// row dead. authHelper's defence-in-depth check reads this column
	// directly (see middleware/auth.go) — it must never be inferred from
	// RevokeReason being non-empty, since a future caller could set a reason
	// without also setting this.
	RevokedAt    int64  `json:"revoked_at" gorm:"not null;default:0;index:idx_user_sessions_user_revoked,priority:2"`
	RevokeReason string `json:"revoke_reason" gorm:"type:varchar(32);not null;default:''"`
}

// TableName overrides the default GORM table name.
func (UserSession) TableName() string {
	return "user_sessions"
}

// User session revoke-reason constants — the values RevokeUserSession*
// writers use for RevokeReason. Kept as plain strings (not an int enum,
// unlike TenantInvite's Status) because this column is read directly off the
// audit trail (governance.ActionAuthSessionRevoked details) where an
// operator or the export endpoint expects a human string, not a code to
// look up.
const (
	SessionRevokeReasonUserRevoked       = "user_revoked"
	SessionRevokeReasonUserRevokedOthers = "user_revoked_others"
	SessionRevokeReasonLogout            = "logout"
	SessionRevokeReasonAdminRevoked      = "admin_revoked"
	SessionRevokeReasonCapExceeded       = "cap_exceeded"
	// SessionRevokeReasonRotated marks the row of a session id that a login
	// (or the SDK-bridge self-heal) retired by minting a fresh id —
	// middleware.RotateSessionID. Deliberately distinct from "logout" and
	// "user_revoked": nobody signed out and nobody revoked anything, the row
	// simply describes a session key that no longer exists, and a support
	// reader asking "why did this device disappear" deserves the real
	// answer.
	SessionRevokeReasonRotated = "rotated"
)
