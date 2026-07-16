package handler

// tenant_credit_pool_stranded_test.go — stranded-debit closed loop through the
// real TopupCreditPool handler. The wallet seams (debitWallet/creditWallet)
// simulate the cross-service half ("debit succeeded", "revert failed"); the
// pool-credit failure is NOT stubbed — it is produced by the genuine
// ErrPoolWouldExceedCeiling path in repo.TopupPool, so the test exercises the
// same conditional UPDATE the production incident would.
//
// Hermetic sqlite tier: -short green, no network, globals restored via
// t.Cleanup (tests in this package are not parallel).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// stubWalletSeams replaces the wallet call seams for one test and restores
// the real gRPC clients on cleanup. revertErr == nil means "revert succeeds".
func stubWalletSeams(t *testing.T, revertErr error) (debitCalls, revertCalls *int) {
	t.Helper()
	debitCalls, revertCalls = new(int), new(int)

	prevDebit, prevCredit := debitWallet, creditWallet
	debitWallet = func(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) (*common.DebitWalletResult, error) {
		*debitCalls++
		return &common.DebitWalletResult{Success: true, BalanceAfter: 999}, nil
	}
	creditWallet = func(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) error {
		*revertCalls++
		return revertErr
	}
	t.Cleanup(func() {
		debitWallet = prevDebit
		creditWallet = prevCredit
	})
	return debitCalls, revertCalls
}

// topupWithKey posts a topup with an explicit Idempotency-Key header
// (adminPoolCtx.do cannot set headers).
func topupWithKey(ctx *adminPoolCtx, amount int64, idemKey string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]interface{}{"amount": amount})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v2/admin/tenants/"+ctx.tenantID+"/credit-pool/topup", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// setupStrandedCtx builds the admin router (actor with wallet), migrates the
// fund-events table and creates a pool whose ceiling forces the topup credit
// to fail (max_balance < topup amount).
func setupStrandedCtx(t *testing.T, maxBalance int64) *adminPoolCtx {
	t.Helper()
	ctx := setupAdminPoolRouter(t, true)
	if err := ctx.db.AutoMigrate(&entity.CreditPoolFundEvent{}); err != nil {
		t.Fatalf("migrate fund events: %v", err)
	}
	w := ctx.do(http.MethodPost, "/api/v2/admin/tenants/"+ctx.tenantID+"/credit-pool",
		map[string]interface{}{"max_balance": maxBalance})
	if w.Code != http.StatusCreated {
		t.Fatalf("create pool: %d %s", w.Code, w.Body.String())
	}
	return ctx
}

func strandedEventCount(t *testing.T, ctx *adminPoolCtx, eventID string) int64 {
	t.Helper()
	var n int64
	if err := ctx.db.Model(&repo.CreditPoolFundEvent{}).
		Where("event_id = ? AND source = ?", eventID, app.FundEventSourceStranded).
		Count(&n).Error; err != nil {
		t.Fatalf("count stranded events: %v", err)
	}
	return n
}

// TestTopupCreditPool_StrandedDebitPersistsEvent: debit ok + pool credit
// fails (real ceiling rejection) + revert fails → the stranded debit must be
// durable in credit_pool_fund_events, not just a log line.
func TestTopupCreditPool_StrandedDebitPersistsEvent(t *testing.T) {
	ctx := setupStrandedCtx(t, 100) // ceiling 100 < amount 200 → TopupPool fails
	stubWalletSeams(t, fmt.Errorf("simulated revert outage"))

	w := topupWithKey(ctx, 200, "idem-stranded-1")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (ceiling), body: %s", w.Code, w.Body.String())
	}

	if n := strandedEventCount(t, ctx, "idem-stranded-1"); n != 1 {
		t.Errorf("stranded fund events = %d, want 1 — stranded debit was not persisted", n)
	}
	var evt repo.CreditPoolFundEvent
	if err := ctx.db.Where("event_id = ?", "idem-stranded-1").First(&evt).Error; err != nil {
		t.Fatalf("read stranded event: %v", err)
	}
	if evt.Amount != 200 || evt.TenantID != ctx.tenantID || evt.NewBalance != 0 {
		t.Errorf("stranded event = amount %d tenant %q new_balance %d, want 200/%q/0",
			evt.Amount, evt.TenantID, evt.NewBalance, ctx.tenantID)
	}
	// Pool untouched: the money is stranded, not silently credited.
	pool, _ := repo.GetTenantCreditPool(ctx.tenantID)
	if pool.CurrentBalance != 0 {
		t.Errorf("pool balance = %d, want 0", pool.CurrentBalance)
	}
}

// TestTopupCreditPool_RevertSuccessDoesNotStrand: when the wallet revert
// succeeds, the money is back in the wallet — recording a stranded event
// would later double-pay the customer via the reconcile sweep.
func TestTopupCreditPool_RevertSuccessDoesNotStrand(t *testing.T) {
	ctx := setupStrandedCtx(t, 100)
	_, revertCalls := stubWalletSeams(t, nil) // revert succeeds

	w := topupWithKey(ctx, 200, "idem-reverted-1")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body: %s", w.Code, w.Body.String())
	}
	if *revertCalls != 1 {
		t.Errorf("revert calls = %d, want 1", *revertCalls)
	}
	if n := strandedEventCount(t, ctx, "idem-reverted-1"); n != 0 {
		t.Errorf("stranded events = %d, want 0 — successful revert must not strand", n)
	}
}

// TestTopupCreditPool_RetrySettlesStrandedOnce: a client retry with the same
// Idempotency-Key (whose wallet debit is deduped upstream) must settle the
// stranded event exactly once — and a following background sweep must find
// nothing left, proving online retry and reconcile cannot double-credit.
func TestTopupCreditPool_RetrySettlesStrandedOnce(t *testing.T) {
	ctx := setupStrandedCtx(t, 100)
	debitCalls, _ := stubWalletSeams(t, fmt.Errorf("simulated revert outage"))

	// Strand it.
	if w := topupWithKey(ctx, 200, "idem-retry-1"); w.Code != http.StatusConflict {
		t.Fatalf("strand step: status = %d, body: %s", w.Code, w.Body.String())
	}

	// Operator raises the ceiling; client retries the same intent.
	if err := ctx.db.Model(&repo.TenantCreditPool{}).
		Where("tenant_id = ?", ctx.tenantID).
		Update("max_balance", 10_000).Error; err != nil {
		t.Fatalf("raise ceiling: %v", err)
	}
	w := topupWithKey(ctx, 200, "idem-retry-1")
	if w.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := decodeJSON(t, w)
	data := resp["data"].(map[string]interface{})
	if data["reconciled"] != true {
		t.Errorf("reconciled = %v, want true (settled via stranded event, not fresh credit)", data["reconciled"])
	}
	if nb := data["new_balance"].(float64); nb != 200 {
		t.Errorf("new_balance = %v, want 200", nb)
	}
	if *debitCalls != 2 {
		t.Errorf("debit calls = %d, want 2 (upstream dedupes by key; both return success)", *debitCalls)
	}

	pool, _ := repo.GetTenantCreditPool(ctx.tenantID)
	if pool.CurrentBalance != 200 {
		t.Fatalf("balance after retry = %d, want 200 (credited exactly once)", pool.CurrentBalance)
	}

	// Background sweep after the online settle: must be a no-op.
	reconciled, failed, err := app.ReconcileStrandedTopups(context.Background())
	if err != nil || reconciled != 0 || failed != 0 {
		t.Errorf("sweep = (%d, %d, %v), want (0, 0, nil)", reconciled, failed, err)
	}
	pool, _ = repo.GetTenantCreditPool(ctx.tenantID)
	if pool.CurrentBalance != 200 {
		t.Errorf("double credit: balance = %d, want 200", pool.CurrentBalance)
	}

	// Third identical retry: pure replay, still 200.
	w3 := topupWithKey(ctx, 200, "idem-retry-1")
	if w3.Code != http.StatusOK {
		t.Fatalf("replay status = %d, body: %s", w3.Code, w3.Body.String())
	}
	pool, _ = repo.GetTenantCreditPool(ctx.tenantID)
	if pool.CurrentBalance != 200 {
		t.Errorf("replay double credit: balance = %d, want 200", pool.CurrentBalance)
	}
}

// TestTopupCreditPool_FreshKeyStillCreditsNormally guards the normal path:
// a fresh Idempotency-Key with a healthy pool takes the plain TopupPool
// branch (no fund event involved).
func TestTopupCreditPool_FreshKeyStillCreditsNormally(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	stubWalletSeams(t, nil)

	w := topupWithKey(ctx, 300, "idem-fresh-1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := decodeJSON(t, w)
	data := resp["data"].(map[string]interface{})
	if nb := data["new_balance"].(float64); nb != 300 {
		t.Errorf("new_balance = %v, want 300", nb)
	}
	if _, hasFlag := data["reconciled"]; hasFlag {
		t.Errorf("normal path must not report reconciled flag, got %v", data["reconciled"])
	}
	var n int64
	ctx.db.Model(&repo.CreditPoolFundEvent{}).Count(&n)
	if n != 0 {
		t.Errorf("fund events = %d, want 0 on the normal path", n)
	}
}
