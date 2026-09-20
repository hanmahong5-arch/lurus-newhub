package repo

// credit_pool_adjustment_test.go — CreditPoolAdjustment is the credit
// counterpart of DebitPool (cycle-13 L1): the money-moving paths that have to
// undo a post-consume debit (PostConsumeQuota's Phase 3 compensation, async
// task refunds, video re-settlement) all go through it, so its skip and
// failure branches decide whether a hand-back lands or is silently lost.

import (
	"errors"
	"strings"
	"testing"
)

func TestCreditPoolAdjustment_CreditsBalanceAndWritesDraw(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-adjust", 1, 10_000, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := TopupPool(pool.ID, "t-adjust", 5_000, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}
	if err := DebitPool(pool.ID, "t-adjust", 1_200, 7, 0); err != nil {
		t.Fatalf("debit: %v", err)
	}

	if err := CreditPoolAdjustment("t-adjust", 1_200, "task_refund"); err != nil {
		t.Fatalf("CreditPoolAdjustment: %v", err)
	}

	var after TenantCreditPool
	if err := DB.First(&after, pool.ID).Error; err != nil {
		t.Fatalf("read pool: %v", err)
	}
	if after.CurrentBalance != 5_000 {
		t.Errorf("balance = %d, want 5000 — a hand-back that does not land leaves the tenant paying "+
			"for work that was refunded everywhere else", after.CurrentBalance)
	}

	var draws []TenantCreditPoolDraw
	if err := DB.Where("pool_id = ? AND direction = ? AND amount = ?",
		pool.ID, PoolDrawDirectionCredit, 1_200).Find(&draws).Error; err != nil {
		t.Fatalf("read draws: %v", err)
	}
	if len(draws) != 1 {
		t.Fatalf("credit draws of 1200 = %d, want 1 — the balance must be explainable from the ledger", len(draws))
	}
	if !strings.HasPrefix(draws[0].Reason, PoolDrawReasonAdjustment) {
		t.Errorf("draw reason = %q, want it to start with %q", draws[0].Reason, PoolDrawReasonAdjustment)
	}
	if !strings.Contains(draws[0].Reason, "task_refund") {
		t.Errorf("draw reason = %q, want the caller's detail in it", draws[0].Reason)
	}
}

func TestCreditPoolAdjustment_SkipsWhatWasNeverDebited(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	// No pool row at all: the pool gate reads that as unlimited, so the debit
	// never happened and there is nothing to hand back.
	if err := CreditPoolAdjustment("t-no-pool", 500, "task_refund"); err != nil {
		t.Errorf("tenant without a pool row: %v, want nil (no debit ever happened)", err)
	}

	unlimited, err := CreateTenantCreditPool("t-unlimited", 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := CreditPoolAdjustment("t-unlimited", 500, "task_refund"); err != nil {
		t.Errorf("unlimited pool: %v, want nil", err)
	}
	var after TenantCreditPool
	if err := DB.First(&after, unlimited.ID).Error; err != nil {
		t.Fatalf("read pool: %v", err)
	}
	if after.CurrentBalance != 0 {
		t.Errorf("unlimited pool balance = %d, want 0 — crediting a pool the relay never debits invents balance",
			after.CurrentBalance)
	}
}

func TestCreditPoolAdjustment_RejectsUnusableInput(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	if err := CreditPoolAdjustment("t-input", 0, "task_refund"); err == nil {
		t.Error("amount 0 must be rejected")
	}
	if err := CreditPoolAdjustment("t-input", -5, "task_refund"); err == nil {
		t.Error("negative amount must be rejected")
	}
	if err := CreditPoolAdjustment("", 5, "task_refund"); err == nil {
		t.Error("empty tenant must be rejected — a hand-back with no pool to land on is a lost credit, not a no-op")
	}
}

func TestCreditPoolAdjustment_CeilingRejectionIsReported(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-ceiling", 1, 1_000, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := TopupPool(pool.ID, "t-ceiling", 1_000, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	// The pool is at its ceiling (a topup landed while the request was in
	// flight): the hand-back no longer fits, and the caller has to hear about
	// it rather than read silence as success.
	err = CreditPoolAdjustment("t-ceiling", 100, "task_refund")
	if !errors.Is(err, ErrPoolWouldExceedCeiling) {
		t.Errorf("err = %v, want ErrPoolWouldExceedCeiling", err)
	}
}

func TestCreditPoolAdjustment_ReasonFitsTheColumn(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-reason", 1, 10_000, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := TopupPool(pool.ID, "t-reason", 5_000, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	// PostgreSQL rejects an over-long varchar(32) outright (22001), which
	// would turn a hand-back into a lost credit on the real database while
	// sqlite happily stored it.
	long := strings.Repeat("x", 200)
	if err := CreditPoolAdjustment("t-reason", 10, long); err != nil {
		t.Fatalf("CreditPoolAdjustment: %v", err)
	}
	var draw TenantCreditPoolDraw
	if err := DB.Where("pool_id = ? AND amount = ?", pool.ID, 10).First(&draw).Error; err != nil {
		t.Fatalf("read draw: %v", err)
	}
	if n := len([]rune(draw.Reason)); n > poolDrawReasonMaxLen {
		t.Errorf("draw reason is %d characters, want <= %d (the column width)", n, poolDrawReasonMaxLen)
	}
}
