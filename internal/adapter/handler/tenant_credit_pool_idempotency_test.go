package handler

// tenant_credit_pool_idempotency_test.go — one wallet debit, one pool credit.
//
// The admin topup (POST /api/v2/admin/tenants/:id/credit-pool/topup) passes
// the caller's Idempotency-Key to the platform wallet debit, where it IS
// deduped — the money leaves the wallet exactly once per key. The local credit
// had no such guard: TryFinalizeStrandedTopup only recognises an intent that
// STRANDED (pool credit and wallet revert both failed), so a retry of a
// previously SUCCESSFUL topup fell through to repo.TopupPool and credited the
// pool a SECOND time. Same key, one debit, two credits.
//
// These tests drive the real handler over the hermetic sqlite tier and assert
// on the balance and the draw ledger — the two things a customer and an
// auditor can see — not on which repo function was called.
//
// What they do NOT cover: the platform side of the dedupe (the wallet seams
// are stubbed, so "the debit was deduped upstream" is an assumption of the
// scenario, not something proved here — it is the platform's contract), and
// the PostgreSQL unique-index race arm of repo.FundPoolIdempotent (sqlite
// serialises writers; that arm has its own PG-only tests in
// internal/adapter/repo/credit_pool_fund_pg_test.go).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
)

// topupWithKeyForTenant is topupWithKey for an arbitrary tenant id — the
// shared helper is pinned to ctx.tenantID and the per-tenant scope of the
// idempotency key is exactly what one of these tests is about.
func topupWithKeyForTenant(ctx *adminPoolCtx, tenantID string, amount int64, idemKey string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]interface{}{"amount": amount})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v2/admin/tenants/"+tenantID+"/credit-pool/topup", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// seedPoolTenant adds a second tenant with its own credit pool to the fixture
// database, so "same key, different tenant" can be exercised end to end.
func seedPoolTenant(t *testing.T, ctx *adminPoolCtx, tenantID, slug string, maxBalance int64) {
	t.Helper()
	now := time.Now()
	if err := ctx.db.Create(&repo.Tenant{
		Id: tenantID, Name: tenantID, Slug: slug,
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_" + slug,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed tenant %s: %v", tenantID, err)
	}
	if _, err := repo.CreateTenantCreditPool(tenantID, ctx.actorID, maxBalance, repo.PoolResetNone, 80); err != nil {
		t.Fatalf("seed pool for %s: %v", tenantID, err)
	}
}

// creditDrawCount counts credit-direction draws for a tenant — the ledger
// answer to "how many times was this pool credited".
func creditDrawCount(t *testing.T, ctx *adminPoolCtx, tenantID string) int64 {
	t.Helper()
	var n int64
	if err := ctx.db.Model(&repo.TenantCreditPoolDraw{}).
		Where("tenant_id = ? AND direction = ?", tenantID, repo.PoolDrawDirectionCredit).
		Count(&n).Error; err != nil {
		t.Fatalf("count credit draws: %v", err)
	}
	return n
}

// TestTopupCreditPool_SuccessfulTopupReplayDoesNotDoubleCredit is the oracle
// for the defect: retrying a SUCCESSFUL topup with the same Idempotency-Key
// must leave the balance where the original credit put it, and answer 200 with
// that original balance.
func TestTopupCreditPool_SuccessfulTopupReplayDoesNotDoubleCredit(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	debitCalls, revertCalls := stubWalletSeams(t, nil)

	first := topupWithKey(ctx, 300, "idem-dup-1")
	if first.Code != http.StatusOK {
		t.Fatalf("first topup: status = %d, want 200, body: %s", first.Code, first.Body.String())
	}
	firstData := decodeJSON(t, first)["data"].(map[string]interface{})
	if nb := firstData["new_balance"].(float64); nb != 300 {
		t.Fatalf("first new_balance = %v, want 300", nb)
	}
	if _, replayed := firstData["replayed"]; replayed {
		t.Errorf("a first, fresh topup must not be flagged replayed: %v", firstData["replayed"])
	}

	// Same intent, same key: the platform deduped the wallet debit, so no
	// second amount of money exists to back a second credit.
	second := topupWithKey(ctx, 300, "idem-dup-1")
	if second.Code != http.StatusOK {
		t.Fatalf("replay: status = %d, want 200, body: %s", second.Code, second.Body.String())
	}
	secondData := decodeJSON(t, second)["data"].(map[string]interface{})
	if nb := secondData["new_balance"].(float64); nb != 300 {
		t.Errorf("replay new_balance = %v, want 300 (the balance the ORIGINAL credit produced)", nb)
	}
	if secondData["replayed"] != true {
		t.Errorf("replayed = %v, want true — a replay must be distinguishable from a fresh topup", secondData["replayed"])
	}

	pool, err := repo.GetTenantCreditPool(ctx.tenantID)
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	if pool.CurrentBalance != 300 {
		t.Errorf("balance = %d, want 300 — one wallet debit produced %d credits", pool.CurrentBalance, pool.CurrentBalance/300)
	}
	if n := creditDrawCount(t, ctx, ctx.tenantID); n != 1 {
		t.Errorf("credit draws = %d, want 1 — the ledger must show a single credit for a single debit", n)
	}
	// One, not two: the replay is answered from the fund-event ledger BEFORE
	// any money moves (the pre-debit lookup in TopupCreditPool), so it never
	// depends on the platform's dedupe at all.
	if *debitCalls != 1 {
		t.Errorf("debit calls = %d, want 1 — a settled replay must be answered without touching the wallet", *debitCalls)
	}
	if *revertCalls != 0 {
		t.Errorf("revert calls = %d, want 0 — a replay is a success, nothing to revert", *revertCalls)
	}
}

// TestTopupCreditPool_DifferentKeysCreditTwice is the over-dedupe guard: two
// genuinely separate topup intents must both land. It is a regression guard,
// not an oracle for the defect — it passes on the unfixed code too, because
// the unfixed code deduped nothing at all. Its bite was shown by mutation:
// pinning the fund event id to a constant in the handler turns it red.
func TestTopupCreditPool_DifferentKeysCreditTwice(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	stubWalletSeams(t, nil)

	if w := topupWithKey(ctx, 300, "idem-distinct-a"); w.Code != http.StatusOK {
		t.Fatalf("first topup: status = %d, body: %s", w.Code, w.Body.String())
	}
	w2 := topupWithKey(ctx, 300, "idem-distinct-b")
	if w2.Code != http.StatusOK {
		t.Fatalf("second topup: status = %d, body: %s", w2.Code, w2.Body.String())
	}
	if data := decodeJSON(t, w2)["data"].(map[string]interface{}); data["new_balance"].(float64) != 600 {
		t.Errorf("second new_balance = %v, want 600", data["new_balance"])
	}

	pool, _ := repo.GetTenantCreditPool(ctx.tenantID)
	if pool.CurrentBalance != 600 {
		t.Errorf("balance = %d, want 600 — two distinct intents must both credit", pool.CurrentBalance)
	}
	if n := creditDrawCount(t, ctx, ctx.tenantID); n != 2 {
		t.Errorf("credit draws = %d, want 2", n)
	}
}

// TestTopupCreditPool_SameKeyDifferentTenantIsIndependentCredit pins the
// per-tenant scope migration 031 gave the idempotency index: two resellers
// reusing an event id (or an operator scripting the same key across tenants)
// are two independent fundings, not a replay. Also a guard rather than a
// defect oracle — the unfixed code deduped nothing — and its bite was shown by
// mutation: dropping the tenant_id term from repo.FundPoolIdempotentWithReason
// lookups turns it red.
func TestTopupCreditPool_SameKeyDifferentTenantIsIndependentCredit(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	stubWalletSeams(t, nil)
	const tenantB = "tenant-pooladmin-b"
	seedPoolTenant(t, ctx, tenantB, "pooladmin-b", 10_000)

	if w := topupWithKeyForTenant(ctx, ctx.tenantID, 300, "idem-shared-key"); w.Code != http.StatusOK {
		t.Fatalf("tenant A topup: status = %d, body: %s", w.Code, w.Body.String())
	}
	wb := topupWithKeyForTenant(ctx, tenantB, 300, "idem-shared-key")
	if wb.Code != http.StatusOK {
		t.Fatalf("tenant B topup: status = %d, body: %s", wb.Code, wb.Body.String())
	}
	dataB := decodeJSON(t, wb)["data"].(map[string]interface{})
	if _, replayed := dataB["replayed"]; replayed {
		t.Errorf("tenant B was flagged replayed = %v; the index is per-tenant on purpose", dataB["replayed"])
	}
	if nb := dataB["new_balance"].(float64); nb != 300 {
		t.Errorf("tenant B new_balance = %v, want 300", nb)
	}

	poolA, _ := repo.GetTenantCreditPool(ctx.tenantID)
	poolB, _ := repo.GetTenantCreditPool(tenantB)
	if poolA.CurrentBalance != 300 || poolB.CurrentBalance != 300 {
		t.Errorf("balances = A:%d B:%d, want 300/300", poolA.CurrentBalance, poolB.CurrentBalance)
	}
}

// TestTopupCreditPool_ReplayAuditEventSaysReplayed: a replay still moves no
// money, so the audit row for it must not read like a fresh 300-quota topup —
// an operator reading the trail would otherwise count the pool as funded
// twice, which is the same misreading the defect itself produced.
func TestTopupCreditPool_ReplayAuditEventSaysReplayed(t *testing.T) {
	ctx := setupStrandedCtx(t, 10_000)
	stubWalletSeams(t, nil)

	writer := &fundAuditRecorder{}
	governance.SetAuditWriter(writer)
	t.Cleanup(func() { governance.SetAuditWriter(inertAuditWriter{}) })

	if w := topupWithKey(ctx, 300, "idem-audit-1"); w.Code != http.StatusOK {
		t.Fatalf("first topup: status = %d, body: %s", w.Code, w.Body.String())
	}
	if w := topupWithKey(ctx, 300, "idem-audit-1"); w.Code != http.StatusOK {
		t.Fatalf("replay: status = %d, body: %s", w.Code, w.Body.String())
	}

	events := waitForFundAuditEvents(t, writer, 2)
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2 (one fresh, one replay)", len(events))
	}
	for i, ev := range events {
		if ev.Action != governance.ActionCreditPoolToppedUp {
			t.Errorf("event %d action = %q, want %q", i, ev.Action, governance.ActionCreditPoolToppedUp)
		}
	}
	if strings.Contains(events[0].Details, "\"replayed\":true") {
		t.Errorf("fresh topup audit details claim a replay: %s", events[0].Details)
	}
	if !strings.Contains(events[1].Details, "\"replayed\":true") {
		t.Errorf("replay audit details = %s, want to contain replayed:true", events[1].Details)
	}
}
