package entity

import (
	"time"
)

// ModelRateLimit is one tenant's per-model RPM/TPM cap (migration 026) — the
// model dimension of the business rate limiter, alongside the token and
// tenant dimensions on tokens/tenants (migration 023).
//
// Matching is by EXACT model name as the client requested it (the same value
// Distribute stores under constant.ContextKeyOriginalModel and settlement
// carries as RelayInfo.OriginModelName) — no wildcards: the model list is
// finite and synced, and pattern precedence rules would make the effective
// limit ambiguous. A missing row means "no per-model limit"; 0 = unlimited
// for that dimension, mirroring the 023 columns.
type ModelRateLimit struct {
	Id       int    `json:"id" gorm:"primaryKey"`
	TenantId string `json:"tenant_id" gorm:"size:36;not null;default:'default';uniqueIndex:uk_model_rate_limits_tenant_model,priority:1"`
	Model    string `json:"model" gorm:"size:255;not null;uniqueIndex:uk_model_rate_limits_tenant_model,priority:2"`
	// RateLimitRPM caps this tenant's relay requests per minute for this model
	// (sliding window, middleware.BusinessModelRateLimit). 0 = unlimited.
	RateLimitRPM int `json:"rate_limit_rpm" gorm:"column:rpm_limit;default:0"`
	// RateLimitTPM caps this tenant's settled LLM token usage per minute for
	// this model (window fed by PostConsumeQuota). 0 = unlimited.
	RateLimitTPM int       `json:"rate_limit_tpm" gorm:"column:tpm_limit;default:0"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (ModelRateLimit) TableName() string {
	return "model_rate_limits"
}
