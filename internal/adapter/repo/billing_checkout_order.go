package repo

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BillingCheckoutOrder = entity.BillingCheckoutOrder

// RecordCheckoutOrder persists the (order_no -> account_id) ownership pair for
// a freshly-created checkout order. Idempotent on order_no: a retried create
// (same order_no) leaves the first owner intact rather than erroring, so a
// duplicate platform response can never reassign an order to another account.
func RecordCheckoutOrder(orderNo string, accountID int64, amountCNY float64, createdAt int64) error {
	if orderNo == "" {
		return errors.New("order_no is empty")
	}
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&BillingCheckoutOrder{
		OrderNo:   orderNo,
		AccountID: accountID,
		AmountCNY: amountCNY,
		CreatedAt: createdAt,
	}).Error
}

// CheckoutOrderAccount returns the account that owns order_no. found is false
// when no ownership record exists — callers MUST fail closed on that (deny the
// status poll) rather than forwarding to the platform, because an order with
// no local owner record cannot be proven to belong to the caller.
func CheckoutOrderAccount(orderNo string) (accountID int64, found bool, err error) {
	var row BillingCheckoutOrder
	err = DB.Select("account_id").Where("order_no = ?", orderNo).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return row.AccountID, true, nil
}
