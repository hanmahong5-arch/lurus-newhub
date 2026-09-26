package entity

import "time"

// BillingOutbox records pending billing settlement actions that must be retried
// until confirmed by the platform. Implements the transactional outbox pattern
// to prevent revenue leakage when platform calls fail.
type BillingOutbox struct {
	ID         int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	AccountID  int64     `json:"account_id" gorm:"not null;index"`
	PreAuthID  int64     `json:"preauth_id" gorm:"not null;uniqueIndex:idx_preauth_action"`
	Action     string    `json:"action" gorm:"type:varchar(16);not null;uniqueIndex:idx_preauth_action"` // "settle" or "release"
	AmountLB   float64   `json:"amount_lb" gorm:"not null;default:0"`
	Status     string    `json:"status" gorm:"type:varchar(16);not null;default:pending;index"`
	RetryCount int       `json:"retry_count" gorm:"not null;default:0"`
	NextRetry  time.Time `json:"next_retry" gorm:"index"`
	Error      string    `json:"error" gorm:"type:text"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (BillingOutbox) TableName() string {
	return "billing_outbox"
}

// BillingDebitOutbox holds direct wallet debits (the legacy branch that skips
// pre-authorization) whose first attempt failed. A debit has no pre-auth id to
// key on, so it gets its own table keyed by the idempotency reference the
// platform deduplicates on: retrying the same RefID can never charge twice.
type BillingDebitOutbox struct {
	ID          int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	AccountID   int64     `json:"account_id" gorm:"not null;index"`
	RefID       string    `json:"ref_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	AmountLB    float64   `json:"amount_lb" gorm:"not null"`
	TxType      string    `json:"tx_type" gorm:"type:varchar(32);not null"`
	Description string    `json:"description" gorm:"type:varchar(255)"`
	ProductID   string    `json:"product_id" gorm:"type:varchar(64)"`
	Status      string    `json:"status" gorm:"type:varchar(16);not null;default:pending;index"`
	RetryCount  int       `json:"retry_count" gorm:"not null;default:0"`
	NextRetry   time.Time `json:"next_retry" gorm:"index"`
	Error       string    `json:"error" gorm:"type:text"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (BillingDebitOutbox) TableName() string {
	return "billing_debit_outbox"
}
