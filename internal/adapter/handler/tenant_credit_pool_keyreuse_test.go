package handler

// tenant_credit_pool_keyreuse_test.go — what the Idempotency-Key of an admin
// pool topup is allowed to mean.
//
// Routing the pool credit through the (tenant_id, event_id) unique index made
// "same key twice" a replay instead of a second credit. That is only safe if
// the handler can tell its OWN settled credit from every other thing that can
// occupy the same (tenant_id, event_id) slot, because a Reseller-authenticated
// caller chooses the key by hand:
//
//   - a fund event written by the platform BillingOutbox path, whose event_id
//     space migration 031's header says is shared with hand-typed runbook keys
//     ("smoke-1"). Reading one of those as "my credit already landed" debits
//     the wallet and credits nothing.
//   - the same key resubmitted with a DIFFERENT amount — a corrected typo, not
//     a retry of the original intent.
//   - a key whose first attempt failed and was refunded. Nothing was owed and
//     nothing was persisted, so re-using it credited the pool for free.
//   - a key too wide for credit_pool_fund_events.event_id VARCHAR(128).
//
// These drive the real handler over the hermetic sqlite tier and assert on the
// observable post-state: pool balance, wallet seam call counts, the durable
// fund-event rows, and what the background sweep finds afterwards.
//
// BLIND SPOT of this whole file: sqlite does not enforce varchar width, so the
// over-long-key test proves the Go guard fires, NOT that PostgreSQL would
// reject the value with 22001. The PG behaviour is the reason the guard
// exists; it is asserted from migrations/019 + 021's column declaration, not
// executed here. The wallet seams are stubs, so "the platform deduped the
// debit" is the scenario's premise, not a proven fact.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// seedForeignFundEvent writes a credit_pool_fund_events row that the admin
// topup endpoint did NOT create — the shape the platform BillingOutbox funding
// path leaves behind.
func seedForeignFundEvent(t *testing.T, ctx *adminPoolCtx, eventID, source string, amount, newBalance int64) {
	t.Helper()
	pool, err := repo.GetTenantCreditPool(ctx.tenantID)
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	row := &repo.CreditPoolFundEvent{
		EventID:    eventID,
		TenantID:   ctx.tenantID,
		PoolID:     pool.ID,
		Amount:     amount,
		NewBalance: newBalance,
		Source:     source,
		CreatedAt:  time.Now(),
	}
	if err := ctx.db.Create(row).Error; err != nil {
		t.Fatalf("seed foreign fund event: %v", err)
	}
}

// poolBalance reads the live pool balance.
func poolBalance(t *testing.T, tenantID string) int64 {
	t.Helper()
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		t.Fatalf("read pool %s: %v", tenantID, err)
	}
	return pool.CurrentBalance
}

// errorCodeOf pulls the top-level error_code out of a handler response.
func errorCodeOf(t *testing.T, body map[string]interface{}) string {
	t.Helper()
	code, _ := body["error_code"].(string)
	return code
}

// TestTopupCreditPool_ForeignSourceRowIsNotTreatedAsMyReplay: a fund event
// this endpoint did not write must never be read as "my credit already
// landed". Before the provenance gate the handler debited the wallet, skipped
// the credit, reverted nothing, recorded nothing and answered 200
// success=true replayed=true — the exact symptom migration 031's header
// describes, reintroduced within a single tenant.
func TestTopupCreditPool_ForeignSourceRowIsNotTreatedAsMyReplay(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	debitCalls, revertCalls := stubWalletSeams(t, nil)
	seedForeignFundEvent(t, ctx, "evt-outbox-collide", "platform-billing-outbox", 999, 999)

	w := topupWithKey(ctx, 300, "evt-outbox-collide")
	body := decodeJSON(t, w)

	if w.Code == http.StatusOK || body["success"] == true {
		t.Fatalf("a foreign fund-event row was accepted as this request's replay: status = %d, body = %s",
			w.Code, w.Body.String())
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (key collision), body: %s", w.Code, w.Body.String())
	}
	if got := errorCodeOf(t, body); got != "POOL_TOPUP_KEY_COLLISION" {
		t.Errorf("error_code = %q, want POOL_TOPUP_KEY_COLLISION", got)
	}
	// The refusal happens before any money moves: nothing to revert, nothing
	// to strand, nothing for an operator to reconcile by hand.
	if *debitCalls != 0 {
		t.Errorf("wallet debit calls = %d, want 0 — the collision is detectable before the debit", *debitCalls)
	}
	if *revertCalls != 0 {
		t.Errorf("wallet revert calls = %d, want 0", *revertCalls)
	}
	if b := poolBalance(t, ctx.tenantID); b != 0 {
		t.Errorf("pool balance = %d, want 0", b)
	}
	// The foreign row is somebody else's ledger entry — untouched.
	var seeded repo.CreditPoolFundEvent
	if err := ctx.db.Where("tenant_id = ? AND event_id = ?", ctx.tenantID, "evt-outbox-collide").First(&seeded).Error; err != nil {
		t.Fatalf("re-read seeded row: %v", err)
	}
	if seeded.Source != "platform-billing-outbox" || seeded.Amount != 999 || seeded.NewBalance != 999 {
		t.Errorf("seeded row was rewritten: %+v", seeded)
	}
}

// stubWalletSeamsWithDebitHook is stubWalletSeams whose debit seam runs a hook
// first. It exists to open the one window the pre-debit check cannot close: a
// row landing between the check and the credit.
func stubWalletSeamsWithDebitHook(t *testing.T, revertErr error, hook func()) (debitCalls, revertCalls *int) {
	t.Helper()
	debitCalls, revertCalls = new(int), new(int)
	prevDebit, prevCredit := debitWallet, creditWallet
	debitWallet = func(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) (*common.DebitWalletResult, error) {
		*debitCalls++
		hook()
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

// TestTopupCreditPool_ForeignRowRacingTheDebitIsCompensated closes the race
// arm: the pre-debit check saw nothing, the money left the wallet, and only
// then did a foreign row take the (tenant, key) slot. The pool cannot be
// credited under that key — so the debit must be given back, and when the
// revert fails too it must become a durable stranded event the sweep
// compensates. What must never happen is the silent skip: money gone, nothing
// credited, nothing recorded, 200 success.
func TestTopupCreditPool_ForeignRowRacingTheDebitIsCompensated(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	seeded := false
	debitCalls, revertCalls := stubWalletSeamsWithDebitHook(t, fmt.Errorf("simulated revert outage"), func() {
		if seeded {
			return
		}
		seeded = true
		seedForeignFundEvent(t, ctx, "evt-race-collide", "platform-billing-outbox", 999, 999)
	})

	w := topupWithKey(ctx, 300, "evt-race-collide")
	body := decodeJSON(t, w)
	if body["success"] == true {
		t.Fatalf("a colliding key answered success: status = %d, body = %s", w.Code, w.Body.String())
	}
	if *debitCalls != 1 {
		t.Fatalf("wallet debit calls = %d, want 1", *debitCalls)
	}
	if *revertCalls != 1 {
		t.Errorf("wallet revert calls = %d, want 1 — a debit that cannot be credited must be given back", *revertCalls)
	}

	// Revert failed, so the debit is stranded and must be durable. Its
	// event_id cannot be the caller's key (the foreign row owns that slot),
	// so assert on the state, not on the id.
	var stranded []repo.CreditPoolFundEvent
	if err := ctx.db.Where("tenant_id = ? AND source = ?", ctx.tenantID, app.FundEventSourceStranded).
		Find(&stranded).Error; err != nil {
		t.Fatalf("read stranded events: %v", err)
	}
	if len(stranded) != 1 {
		t.Fatalf("stranded fund events = %d, want 1 — the wallet was debited and nothing was credited", len(stranded))
	}
	if stranded[0].Amount != 300 {
		t.Errorf("stranded amount = %d, want 300", stranded[0].Amount)
	}

	// And the sweep must actually compensate it: the operator paid for 300.
	reconciled, failed, err := app.ReconcileStrandedTopups(context.Background())
	if err != nil || reconciled != 1 || failed != 0 {
		t.Fatalf("sweep = (%d, %d, %v), want (1, 0, nil)", reconciled, failed, err)
	}
	if b := poolBalance(t, ctx.tenantID); b != 300 {
		t.Errorf("pool balance after sweep = %d, want 300 — a paid topup was silently skipped", b)
	}
}

// TestTopupCreditPool_OverLongIdempotencyKeyRejectedBeforeAnyMoneyMoves:
// credit_pool_fund_events.event_id is VARCHAR(128) (migrations 019:16 and
// 021:53). Once the caller's key started landing in that column on the HAPPY
// path, an over-long key stopped being harmless: on PostgreSQL the INSERT
// fails with 22001 inside the transaction that had already been opened AFTER
// the wallet debit, turning a paid topup into a 500 plus a best-effort refund.
// The guard must fire before the debit.
func TestTopupCreditPool_OverLongIdempotencyKeyRejectedBeforeAnyMoneyMoves(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	debitCalls, revertCalls := stubWalletSeams(t, nil)

	tooLong := strings.Repeat("k", 129)
	w := topupWithKey(ctx, 300, tooLong)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("129-character key: status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if got := errorCodeOf(t, decodeJSON(t, w)); got != "POOL_TOPUP_KEY_INVALID" {
		t.Errorf("error_code = %q, want POOL_TOPUP_KEY_INVALID", got)
	}
	if *debitCalls != 0 || *revertCalls != 0 {
		t.Errorf("wallet calls = %d debits / %d reverts, want 0/0 — rejected before any money moves",
			*debitCalls, *revertCalls)
	}
	if b := poolBalance(t, ctx.tenantID); b != 0 {
		t.Errorf("pool balance = %d, want 0", b)
	}

	// Boundary, the other way: exactly 128 characters fits the column and must
	// still work. A guard that rejected the widest legal key would be a new
	// defect wearing a fix's clothes.
	ok := strings.Repeat("k", 128)
	w2 := topupWithKey(ctx, 300, ok)
	if w2.Code != http.StatusOK {
		t.Fatalf("128-character key: status = %d, want 200, body: %s", w2.Code, w2.Body.String())
	}
	if b := poolBalance(t, ctx.tenantID); b != 300 {
		t.Errorf("pool balance = %d, want 300", b)
	}
}

// TestTopupCreditPool_SameKeyDifferentAmountIsRefused: the replay arm compared
// nothing but (tenant_id, event_id), so an operator who corrected the amount
// and resubmitted was told "Topup successful. New balance: 300" (the console
// renders data.new_balance on success:true) while nothing was credited.
func TestTopupCreditPool_SameKeyDifferentAmountIsRefused(t *testing.T) {
	ctx := setupStrandedCtx(t, 100_000)
	stubWalletSeams(t, nil)

	if w := topupWithKey(ctx, 300, "idem-amount-change"); w.Code != http.StatusOK {
		t.Fatalf("first topup: status = %d, body: %s", w.Code, w.Body.String())
	}

	w := topupWithKey(ctx, 5000, "idem-amount-change")
	body := decodeJSON(t, w)
	if w.Code == http.StatusOK || body["success"] == true {
		t.Fatalf("same key with a different amount answered success: status = %d, body = %s",
			w.Code, w.Body.String())
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409, body: %s", w.Code, w.Body.String())
	}
	if got := errorCodeOf(t, body); got != "POOL_TOPUP_KEY_AMOUNT_MISMATCH" {
		t.Errorf("error_code = %q, want POOL_TOPUP_KEY_AMOUNT_MISMATCH", got)
	}
	if b := poolBalance(t, ctx.tenantID); b != 300 {
		t.Errorf("pool balance = %d, want 300 (the original credit, unchanged)", b)
	}
}

// TestTopupCreditPool_KeyOfARevertedAttemptCannotBeReused: when the credit
// fails and the revert SUCCEEDS — the ordinary ceiling case — the wallet is
// square and nothing used to be persisted. Re-submitting that key after the
// ceiling was raised then found no fund event, credited 300, and left net
// wallet movement of zero against a real pool credit: free money. The console
// drawer only re-mints its key after a success, so a 409 keeps the key and
// makes this the likely path, not the exotic one.
func TestTopupCreditPool_KeyOfARevertedAttemptCannotBeReused(t *testing.T) {
	ctx := setupStrandedCtx(t, 100) // ceiling 100 < 300 → credit fails
	debitCalls, revertCalls := stubWalletSeams(t, nil)

	if w := topupWithKey(ctx, 300, "idem-ceiling-then-retry"); w.Code != http.StatusConflict {
		t.Fatalf("ceiling attempt: status = %d, want 409, body: %s", w.Code, w.Body.String())
	}
	if *revertCalls != 1 {
		t.Fatalf("revert calls = %d, want 1 (the debit was given back)", *revertCalls)
	}

	if err := ctx.db.Model(&repo.TenantCreditPool{}).
		Where("tenant_id = ?", ctx.tenantID).
		Update("max_balance", 10_000).Error; err != nil {
		t.Fatalf("raise ceiling: %v", err)
	}

	w := topupWithKey(ctx, 300, "idem-ceiling-then-retry")
	body := decodeJSON(t, w)
	if w.Code == http.StatusOK || body["success"] == true {
		t.Fatalf("a refunded key credited the pool again: status = %d, body = %s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409, body: %s", w.Code, w.Body.String())
	}
	if got := errorCodeOf(t, body); got != "POOL_TOPUP_KEY_REVERTED" {
		t.Errorf("error_code = %q, want POOL_TOPUP_KEY_REVERTED", got)
	}
	if b := poolBalance(t, ctx.tenantID); b != 0 {
		t.Errorf("pool balance = %d, want 0 — the debit for this key was refunded, so nothing is owed", b)
	}
	if *debitCalls != 1 {
		t.Errorf("wallet debit calls = %d, want 1 — the reused key is refused before a second debit", *debitCalls)
	}

	// Over-dedupe guard: the operator's remedy (a NEW key) must work.
	w2 := topupWithKey(ctx, 300, "idem-ceiling-then-retry-2")
	if w2.Code != http.StatusOK {
		t.Fatalf("fresh key after a revert: status = %d, want 200, body: %s", w2.Code, w2.Body.String())
	}
	if b := poolBalance(t, ctx.tenantID); b != 300 {
		t.Errorf("pool balance = %d, want 300", b)
	}
}

// TestTopupCreditPool_RevertedMarkerIsInvisibleToTheSweep: the terminal row
// written when a revert succeeds shares a table with the stranded-debit state
// machine. If it wore a stranded source the background sweep would "compensate"
// a topup that was already refunded — paying the operator twice.
func TestTopupCreditPool_RevertedMarkerIsInvisibleToTheSweep(t *testing.T) {
	ctx := setupStrandedCtx(t, 100)
	stubWalletSeams(t, nil)

	if w := topupWithKey(ctx, 300, "idem-reverted-marker"); w.Code != http.StatusConflict {
		t.Fatalf("ceiling attempt: status = %d, want 409, body: %s", w.Code, w.Body.String())
	}

	var marker repo.CreditPoolFundEvent
	if err := ctx.db.Where("tenant_id = ? AND event_id = ?", ctx.tenantID, "idem-reverted-marker").
		First(&marker).Error; err != nil {
		t.Fatalf("the reverted attempt left no terminal row: %v", err)
	}
	switch marker.Source {
	case app.FundEventSourceStranded, app.FundEventSourceReconciled, app.FundEventSourceManuallyClosed:
		t.Errorf("reverted marker source = %q — the stranded state machine would act on it", marker.Source)
	}
	if marker.NewBalance != 0 {
		t.Errorf("reverted marker new_balance = %d, want 0 — nothing was credited", marker.NewBalance)
	}

	reconciled, failed, err := app.ReconcileStrandedTopups(context.Background())
	if err != nil || reconciled != 0 || failed != 0 {
		t.Errorf("sweep = (%d, %d, %v), want (0, 0, nil) — a refunded attempt is not a stranded debit",
			reconciled, failed, err)
	}
	if b := poolBalance(t, ctx.tenantID); b != 0 {
		t.Errorf("pool balance after sweep = %d, want 0", b)
	}
}

// TestTopupCreditPool_WhitespacePaddedKeyDedupesLikeTheTrimmedOne: the
// checkout handler trims both header spellings (v2_billing.go
// checkoutIdempotencyKey), and its doc comment claims the topup accepts "the
// same two-header fallback". A key that dedupes on one endpoint and not the
// other makes that claim false — and here the cost is a second pool credit.
func TestTopupCreditPool_WhitespacePaddedKeyDedupesLikeTheTrimmedOne(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	stubWalletSeams(t, nil)

	if w := topupWithKey(ctx, 300, "idem-trim-1"); w.Code != http.StatusOK {
		t.Fatalf("first topup: status = %d, body: %s", w.Code, w.Body.String())
	}
	w := topupWithKey(ctx, 300, "  idem-trim-1  ")
	if w.Code != http.StatusOK {
		t.Fatalf("padded retry: status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if data := decodeJSON(t, w)["data"].(map[string]interface{}); data["replayed"] != true {
		t.Errorf("padded key replayed = %v, want true", data["replayed"])
	}
	if b := poolBalance(t, ctx.tenantID); b != 300 {
		t.Errorf("pool balance = %d, want 300 — whitespace padding bought a second credit", b)
	}
}
