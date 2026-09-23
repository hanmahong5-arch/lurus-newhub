package entity

// PlanQuotaGrant records one credit of a Claude-Code-class (cc_*) plan's
// quota onto the buyer's OWN hub user balance, written by
// handler.PlanGrantV2 (POST /api/v2/:tenant_slug/plan-grant).
//
// Why it exists: relay pre-consume (internal/app/pre_consume_quota.go) gates
// on BOTH the tenant credit pool (which the platform funds when the plan is
// bought) AND the buyer's own users.quota. Provisioning (ProvisionV2) only
// mints a relay token, so the user balance stayed at the small welcome
// grant and a paying buyer hit 402 after a few requests. PlanGrantV2 closes
// that second ledger, and this table is what makes it idempotent.
//
// GrantKey is "<subscription_id>:<subscription_expires_at>" from the
// platform entitlement claims — one grant per subscription period. A
// renewal/extension changes expires_at, so it produces a new key and grants
// again; replaying the same entitlement (or a re-fetched one for the same
// period) hits the UNIQUE(tenant_id, grant_key) index and grants nothing.
//
// Schema is created BOTH by migration 040 (plan_quota_grants) and by
// repo.migrateDB's AutoMigrate call — same dual-creation pattern as
// 036/038; the migration's column types match what GORM derives here.
type PlanQuotaGrant struct {
	Id        int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantId  string `json:"tenant_id" gorm:"type:varchar(36);not null;uniqueIndex:uk_plan_quota_grants_tenant_key,priority:1"`
	AccountId int64  `json:"account_id" gorm:"not null"`
	UserId    int    `json:"user_id" gorm:"not null"`
	GrantKey  string `json:"grant_key" gorm:"type:varchar(128);not null;uniqueIndex:uk_plan_quota_grants_tenant_key,priority:2"`
	PlanCode  string `json:"plan_code" gorm:"type:varchar(64);not null"`
	Amount    int64  `json:"amount" gorm:"not null"`
	CreatedAt int64  `json:"created_at" gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (PlanQuotaGrant) TableName() string {
	return "plan_quota_grants"
}
