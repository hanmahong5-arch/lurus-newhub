package repo

// cov_repo_billing_checkout_test.go — coverage for the (order_no -> account_id)
// ownership record that guards the wallet-topup status-poll endpoint. The
// safety property under test is the one spelled out in the source doc comment:
// a retried create must never reassign an order to a different account, and a
// missing ownership row must fail closed (found=false), never be inferred.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func repoSetupBillingCheckoutPG(t *testing.T) {
	t.Helper()
	SetupTestDB(t)
	if err := DB.AutoMigrate(&entity.BillingCheckoutOrder{}); err != nil {
		t.Fatalf("migrate billing_checkout_orders: %v", err)
	}
}

func TestRecordCheckoutOrder_EmptyOrderNoRejected(t *testing.T) {
	repoSetupBillingCheckoutPG(t)

	if err := RecordCheckoutOrder("", 1, 9.9, 1000); err == nil {
		t.Fatal("want error for empty order_no")
	}

	var cnt int64
	DB.Model(&BillingCheckoutOrder{}).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("rejected call must not persist a row, got %d", cnt)
	}
}

func TestRecordCheckoutOrder_HappyPathThenLookup(t *testing.T) {
	repoSetupBillingCheckoutPG(t)

	if err := RecordCheckoutOrder("order-1", 42, 100.5, 123456); err != nil {
		t.Fatalf("record: %v", err)
	}

	acct, found, err := CheckoutOrderAccount("order-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !found {
		t.Fatal("want found=true for a recorded order")
	}
	if acct != 42 {
		t.Fatalf("want account 42, got %d", acct)
	}

	var row BillingCheckoutOrder
	if err := DB.Where("order_no = ?", "order-1").First(&row).Error; err != nil {
		t.Fatalf("readback: %v", err)
	}
	if row.AmountCNY != 100.5 || row.CreatedAt != 123456 {
		t.Fatalf("stored row mismatch: %+v", row)
	}
}

// TestRecordCheckoutOrder_RetryDoesNotReassignOwner is the money-path invariant
// from the source doc: a duplicate platform response replaying the SAME
// order_no with a DIFFERENT account_id must leave the first owner intact
// (DoNothing on conflict), never overwrite it. If this regresses, a second
// caller's retried checkout could steal a first caller's order ownership.
func TestRecordCheckoutOrder_RetryDoesNotReassignOwner(t *testing.T) {
	repoSetupBillingCheckoutPG(t)

	if err := RecordCheckoutOrder("order-retry", 1, 10, 1); err != nil {
		t.Fatalf("first record: %v", err)
	}
	// Simulated retry: same order_no, attacker/other-account tries to claim it.
	if err := RecordCheckoutOrder("order-retry", 999, 10, 1); err != nil {
		t.Fatalf("retry record must not error: %v", err)
	}

	acct, found, err := CheckoutOrderAccount("order-retry")
	if err != nil || !found {
		t.Fatalf("lookup after retry: acct=%d found=%v err=%v", acct, found, err)
	}
	if acct != 1 {
		t.Fatalf("original owner must be retained: got account %d, want 1 (retry must not reassign)", acct)
	}

	var cnt int64
	DB.Model(&BillingCheckoutOrder{}).Where("order_no = ?", "order-retry").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("want exactly 1 row for order-retry after duplicate create, got %d", cnt)
	}
}

func TestCheckoutOrderAccount_NotFoundFailsClosed(t *testing.T) {
	repoSetupBillingCheckoutPG(t)

	acct, found, err := CheckoutOrderAccount("no-such-order")
	if err != nil {
		t.Fatalf("want nil error on not-found (caller fails closed on found=false), got %v", err)
	}
	if found {
		t.Fatal("want found=false for an unrecorded order_no")
	}
	if acct != 0 {
		t.Fatalf("want zero-value account on not-found, got %d", acct)
	}
}
