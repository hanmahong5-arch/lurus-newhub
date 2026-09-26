package app

import (
	"context"
	"errors"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

type debitCall struct {
	account int64
	amount  float64
	ref     string
}

// TestLegacyDebit_FailureIsParkedAndRetriedUnderTheSameRef locks the legacy
// (no pre-auth) branch's failure arm. It used to log and drop the debit, so a
// platform outage made every request admitted on that branch free. Now the
// failed debit lands in billing_debit_outbox and the worker retries it with
// the SAME idempotency reference, which is what makes the retry safe.
func TestLegacyDebit_FailureIsParkedAndRetriedUnderTheSameRef(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)
	legacyDebitEnv(t)
	if err := InitBillingOutbox(db); err != nil {
		t.Fatalf("init outbox: %v", err)
	}
	prevOutbox := billingOutboxDB
	t.Cleanup(func() { billingOutboxDB = prevOutbox })

	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	var calls []debitCall
	fail := true
	prevDebit := debitWalletGRPC
	debitWalletGRPC = func(_ context.Context, account int64, amount float64, _, _, _, ref string) (*common.DebitWalletResult, error) {
		calls = append(calls, debitCall{account, amount, ref})
		if fail {
			return nil, errors.New("platform unavailable")
		}
		return &common.DebitWalletResult{}, nil
	}
	t.Cleanup(func() { debitWalletGRPC = prevDebit })

	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)
	relayInfo := &relaycommon.RelayInfo{
		UserId: userId, TokenId: tokenId, TokenKey: key,
		IdentityAccountID: 57, PlatformPreAuthID: 0, // legacy branch
	}
	if err := PostConsumeQuota(relayInfo, 700, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("debit attempts = %d, want 1", len(calls))
	}

	var rows []entity.BillingDebitOutbox
	db.Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("parked debits = %d, want 1 — the failed debit was dropped", len(rows))
	}
	row := rows[0]
	if row.AccountID != 57 || row.RefID != calls[0].ref || row.AmountLB != calls[0].amount || row.AmountLB <= 0 {
		t.Fatalf("parked row %+v does not match the failed attempt %+v", row, calls[0])
	}

	// The platform is back: the worker retries under the same reference.
	fail = false
	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("ProcessBillingOutbox: %v", err)
	}
	if len(calls) != 2 || calls[1] != calls[0] {
		t.Fatalf("retry = %+v, want exactly one more call identical to %+v", calls[1:], calls[0])
	}
	db.First(&row, row.ID)
	if row.Status != outboxStatusDone {
		t.Fatalf("status after successful retry = %q, want %q", row.Status, outboxStatusDone)
	}

	// Done rows are not claimed again.
	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("ProcessBillingOutbox: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("a settled debit was retried again (%d calls)", len(calls))
	}
}

// TestDebitOutbox_BacksOffThenGivesUp: a debit that keeps failing is retried
// with backoff and ends in "failed" after outboxMaxRetries, never "done".
func TestDebitOutbox_BacksOffThenGivesUp(t *testing.T) {
	db := setupServiceTestDB(t)
	if err := InitBillingOutbox(db); err != nil {
		t.Fatalf("init outbox: %v", err)
	}
	prevOutbox := billingOutboxDB
	t.Cleanup(func() { billingOutboxDB = prevOutbox })
	prevDebit := debitWalletGRPC
	debitWalletGRPC = func(context.Context, int64, float64, string, string, string, string) (*common.DebitWalletResult, error) {
		return nil, errors.New("still down")
	}
	t.Cleanup(func() { debitWalletGRPC = prevDebit })

	if err := EnqueueDebit(9, 1.25, "llm_usage", "d", "p", "llm-usage:test"); err != nil {
		t.Fatalf("EnqueueDebit: %v", err)
	}
	for i := 0; i < outboxMaxRetries; i++ {
		db.Model(&entity.BillingDebitOutbox{}).Where("ref_id = ?", "llm-usage:test").Update("next_retry", "1970-01-02 00:00:00")
		processDebitOutbox(context.Background())
	}
	var row entity.BillingDebitOutbox
	db.Where("ref_id = ?", "llm-usage:test").First(&row)
	if row.Status != outboxStatusFailed || row.RetryCount != outboxMaxRetries {
		t.Fatalf("after %d failures: status=%q retries=%d, want failed/%d", outboxMaxRetries, row.Status, row.RetryCount, outboxMaxRetries)
	}
}
