package entity

import "time"

// ProvisionedRedemptionBatch records a completed idempotent distributor
// batch-issuance of redemption codes via the internal provisioning API
// (POST /internal/v1/provisioning/tenants/:slug/redemptions). The EventID
// column carries the platform-side event_id as a unique key so replayed calls
// return the first batch without minting codes twice — same ledger pattern as
// CreditPoolFundEvent.
//
// Codes stores the generated redemption keys as a JSON array (TEXT) so a
// replay can read the original codes straight back from this row instead of
// reverse-querying the redemptions table for batch membership.
//
// Schema is managed by migration 027 (provisioned_redemption_batches) plus
// AutoMigrate (repo.migrateDB); index names in both paths match so whichever
// runs first the other is a no-op.
type ProvisionedRedemptionBatch struct {
	ID           int64     `json:"id"             gorm:"primaryKey;autoIncrement"`
	EventID      string    `json:"event_id"       gorm:"type:varchar(128);not null;uniqueIndex:uk_provisioned_redemption_batches_event_id"`
	TenantID     string    `json:"tenant_id"      gorm:"type:varchar(36);not null;index:idx_provisioned_redemption_batches_tenant"`
	BatchSize    int       `json:"batch_size"     gorm:"not null"`
	QuotaPerCode int64     `json:"quota_per_code" gorm:"type:bigint;not null"`
	Source       string    `json:"source"         gorm:"type:varchar(64);not null"`
	Codes        string    `json:"codes"          gorm:"type:text;not null"`
	CreatedAt    time.Time `json:"created_at"     gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (ProvisionedRedemptionBatch) TableName() string {
	return "provisioned_redemption_batches"
}
