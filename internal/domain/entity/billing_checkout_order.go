package entity

// BillingCheckoutOrder is the ownership record for a v2 wallet-topup checkout
// order. CreateBillingCheckout writes one row (order_no -> account_id) after
// lurus-platform mints the order; GetBillingCheckoutStatus reads it back to
// prove the polling caller owns the order before forwarding to the platform's
// order-keyed status endpoint (which itself takes no account parameter).
//
// Schema is managed by migration 028 (create_billing_checkout_orders) plus
// AutoMigrate (repo.migrateDB); the index name in both paths matches so
// whichever runs first the other is a no-op.
type BillingCheckoutOrder struct {
	OrderNo   string  `json:"order_no"   gorm:"column:order_no;type:varchar(128);primaryKey"`
	AccountID int64   `json:"account_id" gorm:"column:account_id;type:bigint;not null;index:idx_billing_checkout_orders_account"`
	AmountCNY float64 `json:"amount_cny" gorm:"column:amount_cny;not null;default:0"`
	CreatedAt int64   `json:"created_at" gorm:"column:created_at;not null;default:0"`
}

// TableName overrides the default GORM table name.
func (BillingCheckoutOrder) TableName() string {
	return "billing_checkout_orders"
}
