package entity

import (
	"time"

	"gorm.io/gorm"
)

// Tenant represents a multi-tenant SaaS tenant
type Tenant struct {
	Id string `json:"id" gorm:"primaryKey;size:36"`
	// IDPOrgID is the upstream OIDC organization identifier this tenant maps to.
	// TODO(idp-migration): the physical column (zitadel_org_id) is intentionally
	// NOT renamed here — a column rename needs a reserved migration ID (ledger,
	// owner-gated) + a live-DB rewrite. The gorm column tag and JSON wire field
	// stay zitadel_org_id until that migration lands; only the Go field name and
	// concept are vendor-neutralized.
	IDPOrgID string `json:"zitadel_org_id" gorm:"column:zitadel_org_id;size:128;unique;not null;index"`
	Slug     string `json:"slug" gorm:"size:64;unique;not null;index"`
	Name     string `json:"name" gorm:"size:128;not null"`
	Status   int    `json:"status" gorm:"type:int;default:1;index"`
	PlanType string `json:"plan_type" gorm:"size:32;default:'free';index"`
	MaxUsers int    `json:"max_users" gorm:"type:int;default:100"`
	// Deprecated: superseded by tenant_credit_pools.max_balance in ADR 2026-05-18
	// (Accepted §9 Q3). Kept for backward compatibility; removed in a Q4 migration.
	MaxQuota  int64          `json:"max_quota" gorm:"type:bigint;default:1000000"`
	// RateLimitRPM caps the tenant's aggregate relay requests per minute across
	// all of its tokens (sliding window, enforced by
	// middleware.BusinessRateLimit). 0 = unlimited (backward compat).
	RateLimitRPM int `json:"rate_limit_rpm" gorm:"column:rpm_limit;default:0"`
	// RateLimitTPM caps the tenant's aggregate settled LLM tokens per minute
	// (sliding window read by middleware.BusinessRateLimit, fed by
	// PostConsumeQuota — internal/app/business_tpm.go). 0 = unlimited.
	RateLimitTPM int `json:"rate_limit_tpm" gorm:"column:tpm_limit;default:0"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at,omitempty" gorm:"index"`
}

// Tenant status constants
const (
	TenantStatusEnabled   = 1
	TenantStatusDisabled  = 2
	TenantStatusSuspended = 3
)

// Tenant plan type constants
const (
	TenantPlanFree       = "free"
	TenantPlanPro        = "pro"
	TenantPlanEnterprise = "enterprise"
)

func (Tenant) TableName() string {
	return "tenants"
}

func (t *Tenant) IsEnabled() bool {
	return t.Status == TenantStatusEnabled
}

func (t *Tenant) IsDisabled() bool {
	return t.Status == TenantStatusDisabled || t.Status == TenantStatusSuspended
}

// TenantStats contains comprehensive statistics for a tenant
type TenantStats struct {
	TenantID            string  `json:"tenant_id"`
	UserCount           int64   `json:"user_count"`
	MaxUsers            int     `json:"max_users"`
	MaxQuota            int64   `json:"max_quota"`
	TokenCount          int64   `json:"token_count"`
	ChannelCount        int64   `json:"channel_count"`
	TotalQuotaUsed      int64   `json:"total_quota_used"`
	TotalQuotaRemaining int64   `json:"total_quota_remaining"`
	ActiveSubscriptions int64   `json:"active_subscriptions"`
	TotalTopUpAmount    float64 `json:"total_topup_amount"`
	TotalRedemptions    int64   `json:"total_redemptions"`
	LogCount            int64   `json:"log_count"`
	LastActivityAt      int64   `json:"last_activity_at"`
}

// TenantPlugin context key constants
const (
	TenantIDContextKey     = "tenant_id"
	SkipTenantIsolationKey = "skip_tenant_isolation"
)
