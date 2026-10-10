package entity

// LogBody is one archived request/response pair (migration 052, table
// log_bodies). It exists only for tenants that consented to body archiving
// (Tenant.SedimentationConsent) and only while their effective content
// retention is "full"; the full gate lives in repo.ShouldArchiveLogBody.
//
// RequestBody is the body AFTER the content rules (mask / reject) ran, never
// the caller's original bytes. Each text field is capped at 64KB and
// Truncated says whether either was cut. ResponseCaptured is false when the
// relay path did not hand the completed text over (streaming), so an empty
// ResponseText is distinguishable from "nothing to capture".
//
// The GORM tags and the SQL in migrations/052 agree on column types and index
// names, so AutoMigrate-first and migration-first both converge.
type LogBody struct {
	Id               int64  `json:"-" gorm:"primaryKey"`
	RequestId        string `json:"request_id" gorm:"type:varchar(64);not null;index:idx_log_bodies_request_id"`
	TenantId         string `json:"tenant_id" gorm:"type:varchar(36);not null;default:'';index:idx_log_bodies_tenant_created,priority:1"`
	UserId           int64  `json:"user_id" gorm:"type:bigint;not null;default:0"`
	TokenId          int64  `json:"token_id" gorm:"type:bigint;not null;default:0"`
	Model            string `json:"model" gorm:"type:varchar(255);not null;default:''"`
	CreatedAt        int64  `json:"created_at" gorm:"type:bigint;not null;default:0;index:idx_log_bodies_tenant_created,priority:2,sort:desc"`
	ExpiresAt        int64  `json:"expires_at" gorm:"type:bigint;not null;index:idx_log_bodies_expires_at"`
	RequestBody      string `json:"request_body" gorm:"type:text;not null;default:''"`
	ResponseText     string `json:"response_text" gorm:"type:text;not null;default:''"`
	ResponseCaptured bool   `json:"response_captured" gorm:"not null;default:false"`
	Truncated        bool   `json:"truncated" gorm:"not null;default:false"`
}

func (LogBody) TableName() string { return "log_bodies" }
