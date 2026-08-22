package entity

import (
	"time"
)

// Pool reset period enum for TenantCreditPool.ResetPeriod.
// "monthly" is the accepted default per ADR 2026-05-18 §9 Q1.
const (
	PoolResetNone    = "none"
	PoolResetDaily   = "daily"
	PoolResetWeekly  = "weekly"
	PoolResetMonthly = "monthly"
)

// PoolMaxBalanceUnlimited is the sentinel meaning "no ceiling". When
// MaxBalance equals this value the enforcement layer skips the pool gate.
const PoolMaxBalanceUnlimited int64 = -1

// Draw direction enum for TenantCreditPoolDraw.Direction.
const (
	PoolDrawDirectionDebit  int16 = 1
	PoolDrawDirectionCredit int16 = -1
)

// Draw reason enum for TenantCreditPoolDraw.Reason.
//
// "relay_overdraft" marks a post-consume debit that landed after the pool was
// already exhausted: the upstream tokens were burned and the user quota was
// charged, so the correct action is to record the debt (balance goes
// negative) rather than drop the debit. Conservation law
// `seed − Σdraws == balance` holds unconditionally; the relay gate
// (IsExhausted ⇒ balance <= 0) keeps rejecting until a topup repays the debt.
const (
	PoolDrawReasonRelayDebit = "relay_debit"
	PoolDrawReasonTopup      = "topup"
	PoolDrawReasonReset      = "reset"
	PoolDrawReasonAdjustment = "adjustment"
	PoolDrawReasonOverdraft  = "relay_overdraft"
)

// TenantCreditPool holds per-tenant pre-paid balance plus optional ceiling and
// reset schedule. One row per tenant; absence of a row is interpreted as
// "unlimited" by the enforcement layer (backward-compatible default).
//
// Canonical design: ADR 2026-05-18 (tenant-credit-pool) §3.1.
type TenantCreditPool struct {
	ID                int64      `json:"id"                  gorm:"primaryKey;autoIncrement"`
	TenantID          string     `json:"tenant_id"           gorm:"type:varchar(36);not null;uniqueIndex"`
	ParentTenantID    *string    `json:"parent_tenant_id"    gorm:"type:varchar(36);index"`
	CreatedByUserID   int        `json:"created_by_user_id"  gorm:"not null"`
	CurrentBalance    int64      `json:"current_balance"     gorm:"type:bigint;not null;default:0"`
	MaxBalance        int64      `json:"max_balance"         gorm:"type:bigint;not null;default:-1"`
	ResetPeriod       string     `json:"reset_period"        gorm:"type:varchar(16);not null;default:'monthly'"`
	LastResetAt       time.Time  `json:"last_reset_at"       gorm:"not null;default:CURRENT_TIMESTAMP"`
	NextResetAt       *time.Time `json:"next_reset_at"       gorm:"index"`
	AlertThresholdPct int        `json:"alert_threshold_pct" gorm:"not null;default:80"`
	AlertFiredAt      *time.Time `json:"alert_fired_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// TableName overrides the default GORM table name.
func (TenantCreditPool) TableName() string {
	return "tenant_credit_pools"
}

// IsUnlimited reports whether this pool waives the balance ceiling.
// Unlimited pools are treated as "no pool" by the enforcement layer.
func (p *TenantCreditPool) IsUnlimited() bool {
	return p.MaxBalance == PoolMaxBalanceUnlimited
}

// IsExhausted reports whether a finite-ceiling pool has zero remaining
// balance — the trigger for HTTP 402 in the relay enforcement layer.
func (p *TenantCreditPool) IsExhausted() bool {
	return !p.IsUnlimited() && p.CurrentBalance <= 0
}

// ShouldAlert reports whether the current balance has dipped under the
// configured alert threshold. Returns false for unlimited or zero-ceiling
// pools. Caller is responsible for deduplication via AlertFiredAt.
func (p *TenantCreditPool) ShouldAlert() bool {
	if p.IsUnlimited() || p.MaxBalance <= 0 {
		return false
	}
	threshold := p.MaxBalance * int64(p.AlertThresholdPct) / 100
	return p.CurrentBalance < threshold
}

// TenantCreditPoolDraw is the append-only audit ledger entry for every
// balance-changing event against a pool (debit, topup, scheduled reset,
// manual adjustment). Never updated in place.
//
// Canonical design: ADR 2026-05-18 (tenant-credit-pool) §3.2.
type TenantCreditPoolDraw struct {
	ID          int64     `json:"id"                       gorm:"primaryKey;autoIncrement"`
	PoolID      int64     `json:"pool_id"                  gorm:"not null;index:idx_pool_time,priority:1"`
	TenantID    string    `json:"tenant_id"                gorm:"type:varchar(36);not null;index:idx_tenant_time,priority:1"`
	TokenID     int       `json:"token_id,omitempty"`
	LogID       int64     `json:"log_id,omitempty"`
	Direction   int16     `json:"direction"                gorm:"not null"`
	Amount      int64     `json:"amount"                   gorm:"type:bigint;not null"`
	Reason      string    `json:"reason"                   gorm:"type:varchar(32);not null"`
	ActorUserID int       `json:"actor_user_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"               gorm:"index:idx_pool_time,priority:2;index:idx_tenant_time,priority:2"`
}

// TableName overrides the default GORM table name.
func (TenantCreditPoolDraw) TableName() string {
	return "tenant_credit_pool_draws"
}

// CreditPoolFundEvent records a completed idempotent fund operation from the
// platform BillingOutbox. The EventID column carries the outbox event_id as
// the idempotency key, scoped PER TENANT (tenant_id, event_id) so replayed
// calls return the first result without double-crediting — and so two
// tenants funding under the same event_id (real for hand-typed/smoke keys;
// never happens for real BillingOutbox UUIDs) credit independently instead of
// the second tenant's call being misjudged as a replay of the first's.
//
// Schema is managed by migration 019 (create) + 031 (per-tenant convergence).
// Design rationale: keeping fund events in a separate table from
// tenant_credit_pool_draws avoids polluting the relay audit ledger with
// cross-service provisioning semantics, and lets UNIQUE(tenant_id, event_id)
// be enforced without adding a nullable column to the append-only draws table.
type CreditPoolFundEvent struct {
	ID         int64     `json:"id"          gorm:"primaryKey;autoIncrement"`
	EventID    string    `json:"event_id"    gorm:"type:varchar(128);not null;uniqueIndex:uk_credit_pool_fund_events_tenant_event_id,priority:2"`
	TenantID   string    `json:"tenant_id"   gorm:"type:varchar(36);not null;index;uniqueIndex:uk_credit_pool_fund_events_tenant_event_id,priority:1"`
	PoolID     int64     `json:"pool_id"     gorm:"not null"`
	Amount     int64     `json:"amount"      gorm:"type:bigint;not null"`
	NewBalance int64     `json:"new_balance" gorm:"type:bigint;not null"`
	Source     string    `json:"source"      gorm:"type:varchar(64);not null"`
	CreatedAt  time.Time `json:"created_at"  gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (CreditPoolFundEvent) TableName() string {
	return "credit_pool_fund_events"
}
