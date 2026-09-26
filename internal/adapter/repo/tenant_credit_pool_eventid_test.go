package repo

// tenant_credit_pool_eventid_test.go — the width of the column the idempotency
// key is stored in.
//
// credit_pool_fund_events.event_id is VARCHAR(128) (migrations/019:16 and
// migrations/021:53). FundPoolIdempotentWithReason writes it inside the same
// transaction as the pool credit, and its admin caller reaches that point only
// AFTER the platform wallet has been debited. On PostgreSQL an over-long value
// is not truncated — the INSERT fails with 22001 and aborts the whole
// transaction, so a caller-chosen key long enough to overflow turns a paid
// topup into a rollback plus a best-effort refund. The value is rejected up
// front instead, before the pool row is touched.
//
// BLIND SPOT: this tier is sqlite, which does NOT enforce varchar width — it
// stores a 200-character value in a VARCHAR(128) without complaint. So these
// tests prove the Go guard fires and that the legal boundary still works; they
// do NOT execute PostgreSQL's 22001. That behaviour is read off the column
// declaration in 019/021, not demonstrated here. A test asserting "PG rejects
// it" would have to live in the PG tier (credit_pool_fund_pg_test.go).

import (
	"context"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// setupFundEventDB is setupPoolTestDB plus the fund-events table.
func setupFundEventDB(t *testing.T) func() {
	t.Helper()
	cleanup := setupPoolTestDB(t)
	if err := DB.AutoMigrate(&entity.CreditPoolFundEvent{}); err != nil {
		cleanup()
		t.Fatalf("migrate fund events: %v", err)
	}
	return cleanup
}

// TestFundPoolIdempotentWithReason_RejectsEventIDWiderThanColumn pins the
// boundary: 128 characters is the widest legal key and must still fund;
// 129 must be refused without touching the pool.
func TestFundPoolIdempotentWithReason_RejectsEventIDWiderThanColumn(t *testing.T) {
	defer setupFundEventDB(t)()
	ctx := context.Background()

	pool, err := CreateTenantCreditPool("t-evtwidth", 1, PoolMaxBalanceUnlimited, PoolResetNone, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	tooLong := strings.Repeat("e", 129)
	ev, err := FundPoolIdempotentWithReason(ctx, pool.ID, "t-evtwidth", 1000, tooLong, "billing-outbox", 7, "")
	if err == nil {
		t.Fatalf("129-character event_id was accepted (event = %+v); PostgreSQL would have aborted the transaction with 22001", ev)
	}
	if ev != nil {
		t.Errorf("rejected call returned an event: %+v", ev)
	}
	if !strings.Contains(err.Error(), "128") {
		t.Errorf("error = %q, want it to name the 128-character limit so the caller can act on it", err.Error())
	}

	got, err := GetTenantCreditPool("t-evtwidth")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 0 {
		t.Errorf("pool balance = %d, want 0 — the rejected fund must not credit", got.CurrentBalance)
	}
	var rows int64
	if err := DB.Model(&entity.CreditPoolFundEvent{}).Count(&rows).Error; err != nil {
		t.Fatalf("count fund events: %v", err)
	}
	if rows != 0 {
		t.Errorf("fund event rows = %d, want 0", rows)
	}

	// The widest legal key still funds — a guard that rejected 128 would be a
	// new defect, not a fix.
	atLimit := strings.Repeat("e", 128)
	ev2, err := FundPoolIdempotentWithReason(ctx, pool.ID, "t-evtwidth", 1000, atLimit, "billing-outbox", 7, "")
	if err != nil {
		t.Fatalf("128-character event_id: %v", err)
	}
	if ev2 == nil || ev2.NewBalance != 1000 {
		t.Fatalf("128-character fund = %+v, want new_balance 1000", ev2)
	}
}

// TestFundPoolIdempotentWithReason_RejectsEventIDPostgresCannotStore covers
// the other two values a text column refuses: an embedded NUL byte (rejected
// outright by PostgreSQL text types) and a byte sequence that is not valid UTF-8
// ("invalid byte sequence for encoding UTF8"). Both would abort the same
// post-debit transaction as an over-long key.
func TestFundPoolIdempotentWithReason_RejectsEventIDPostgresCannotStore(t *testing.T) {
	defer setupFundEventDB(t)()
	ctx := context.Background()

	pool, err := CreateTenantCreditPool("t-evtbytes", 1, PoolMaxBalanceUnlimited, PoolResetNone, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	for name, eventID := range map[string]string{
		"nul byte":     "evt\x00id",
		"invalid utf8": "evt-\xff\xfe",
	} {
		ev, err := FundPoolIdempotentWithReason(ctx, pool.ID, "t-evtbytes", 1000, eventID, "billing-outbox", 7, "")
		if err == nil {
			t.Errorf("%s: accepted (event = %+v)", name, ev)
		}
		if ev != nil {
			t.Errorf("%s: rejected call returned an event: %+v", name, ev)
		}
	}

	got, err := GetTenantCreditPool("t-evtbytes")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 0 {
		t.Errorf("pool balance = %d, want 0", got.CurrentBalance)
	}
}
