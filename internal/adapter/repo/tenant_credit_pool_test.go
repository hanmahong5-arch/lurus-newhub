package repo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupPoolTestDB swaps the package-level repo.DB with an in-memory sqlite
// instance for the duration of the test. Returns a cleanup that restores
// the previous DB so other tests in the package are unaffected.
//
// We auto-migrate only the two pool tables — they're self-contained and
// don't depend on Token / User / Tenant for these unit-level checks.
var poolTestDBCounter atomic.Int64

func setupPoolTestDB(t *testing.T) (cleanup func()) {
	t.Helper()
	n := poolTestDBCounter.Add(1)
	dsn := fmt.Sprintf("file:pool%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.TenantCreditPool{}, &entity.TenantCreditPoolDraw{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := DB
	DB = db
	return func() { DB = prev }
}

// Test 1: DebitPool happy path decrements balance and writes a draw.
func TestDebitPool_HappyPath(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-acme", 1, 1000, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Initial pool starts at 0 — topup first so we have something to debit.
	if _, err := TopupPool(pool.ID, "t-acme", 500, 1, "test seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	if err := DebitPool(pool.ID, "t-acme", 100, 42, 0); err != nil {
		t.Errorf("DebitPool happy path returned err: %v", err)
	}

	got, err := GetTenantCreditPool("t-acme")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 400 {
		t.Errorf("balance after debit = %d, want 400", got.CurrentBalance)
	}

	draws, total, err := ListPoolDraws(pool.ID, 0, 50)
	if err != nil {
		t.Fatalf("list draws: %v", err)
	}
	if total < 2 { // seed topup + this debit
		t.Errorf("draws total = %d, want >= 2", total)
	}
	if len(draws) == 0 || draws[0].Amount != 100 {
		t.Errorf("most recent draw amount = %v, want 100", draws)
	}
}

// Test 2: atomic race — 20 goroutines × 10 amount vs pool 50 → exactly 5
// succeed; current_balance never goes negative. This is the ADR §7 risk #1
// invariant.
func TestDebitPool_AtomicRace(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-race", 1, 1000, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Seed with exactly 50 — 5 debits of 10 should succeed, 15 must fail.
	if _, err := TopupPool(pool.ID, "t-race", 50, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	var wg sync.WaitGroup
	var ok, fail atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := DebitPool(pool.ID, "t-race", 10, 0, 0)
			if err == nil {
				ok.Add(1)
			} else if errors.Is(err, ErrPoolExhausted) {
				fail.Add(1)
			} else {
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	wg.Wait()

	if ok.Load() != 5 {
		t.Errorf("successful debits = %d, want exactly 5", ok.Load())
	}
	if fail.Load() != 15 {
		t.Errorf("rejected debits = %d, want exactly 15", fail.Load())
	}

	final, err := GetTenantCreditPool("t-race")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if final.CurrentBalance < 0 {
		t.Errorf("FAILED INVARIANT — balance went negative: %d", final.CurrentBalance)
	}
	if final.CurrentBalance != 0 {
		t.Errorf("final balance = %d, want 0 (50 - 5*10)", final.CurrentBalance)
	}
}

// Test 3: exhausted pool returns ErrPoolExhausted, balance untouched.
func TestDebitPool_Exhausted(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-empty", 1, 1000, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Current balance starts at 0; any debit must fail.
	err = DebitPool(pool.ID, "t-empty", 1, 0, 0)
	if !errors.Is(err, ErrPoolExhausted) {
		t.Errorf("expected ErrPoolExhausted, got %v", err)
	}
}

// Test 4: GetTenantCreditPool returns ErrPoolNotFound (not error) for absent rows.
// The relay gate relies on this distinction.
func TestGetTenantCreditPool_NotFound(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	_, err := GetTenantCreditPool("nonexistent")
	if !errors.Is(err, ErrPoolNotFound) {
		t.Errorf("expected ErrPoolNotFound, got %v", err)
	}
}

// Test 5: TopupPool rejects topups that would exceed ceiling.
func TestTopupPool_CeilingExceeded(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-cap", 1, 100, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Topup 80 → balance 80, ceiling 100, ok.
	if _, err := TopupPool(pool.ID, "t-cap", 80, 1, "first"); err != nil {
		t.Fatalf("first topup: %v", err)
	}
	// Topup 50 more → would be 130 > 100, must reject.
	if _, err := TopupPool(pool.ID, "t-cap", 50, 1, "overflow"); !errors.Is(err, ErrPoolWouldExceedCeiling) {
		t.Errorf("expected ErrPoolWouldExceedCeiling, got %v", err)
	}
	// Verify balance untouched at 80.
	got, _ := GetTenantCreditPool("t-cap")
	if got.CurrentBalance != 80 {
		t.Errorf("balance after rejected topup = %d, want 80 (unchanged)", got.CurrentBalance)
	}
}

// Test 6: unlimited pool accepts arbitrary topup, never rejects.
func TestTopupPool_Unlimited(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-inf", 1, PoolMaxBalanceUnlimited, PoolResetNone, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := TopupPool(pool.ID, "t-inf", 1_000_000_000, 1, "big"); err != nil {
		t.Errorf("unlimited pool should accept any topup, got %v", err)
	}
}

// timePtrEqual compares two *time.Time for the same instant, treating two
// nils as equal — used by the observe-mode byte-identical assertion below,
// where a naive `*a != *b` would compare struct pointer identity instead of
// the pointee value.
func timePtrEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// Test 7: ResetDuePools(enforce=true) refills a due pool to its ceiling,
// clears alert_fired_at, advances next_reset_at to the next period boundary,
// and writes exactly one PoolDrawReasonReset credit draw for the refill delta.
func TestResetDuePools_EnforceRefillsAndDraws(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-reset-enf", 1, 1000, PoolResetDaily, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now().UTC()
	due := now.Add(-time.Hour)
	fired := now.Add(-2 * time.Hour)
	if err := DB.Model(&TenantCreditPool{}).Where("id = ?", pool.ID).
		Updates(map[string]interface{}{
			"current_balance": 100,
			"next_reset_at":   due,
			"alert_fired_at":  fired,
		}).Error; err != nil {
		t.Fatalf("seed due state: %v", err)
	}

	results, err := ResetDuePools(context.Background(), now, true)
	if err != nil {
		t.Fatalf("ResetDuePools: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0]
	if r.Action != PoolResetActionReset || r.Delta != 900 || r.TenantID != "t-reset-enf" || r.PoolID != pool.ID {
		t.Errorf("result = %+v, want action=reset delta=900 tenant=t-reset-enf pool=%d", r, pool.ID)
	}

	got, err := GetTenantCreditPool("t-reset-enf")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 1000 {
		t.Errorf("balance = %d, want 1000 (refilled to ceiling)", got.CurrentBalance)
	}
	if got.AlertFiredAt != nil {
		t.Errorf("alert_fired_at = %v, want nil (reset clears it)", got.AlertFiredAt)
	}
	wantNext := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	if got.NextResetAt == nil || !got.NextResetAt.Equal(wantNext) {
		t.Errorf("next_reset_at = %v, want %v (next UTC midnight)", got.NextResetAt, wantNext)
	}

	draws, total, derr := ListPoolDraws(pool.ID, 0, 50)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	if total != 1 {
		t.Fatalf("draw count = %d, want 1", total)
	}
	if draws[0].Reason != PoolDrawReasonReset || draws[0].Amount != 900 || draws[0].Direction != PoolDrawDirectionCredit {
		t.Errorf("draw = %+v, want reason=reset amount=900 direction=credit", draws[0])
	}
}

// Test 8: ResetDuePools(enforce=false) reports the would-be delta but the
// pool row is byte-identical afterwards and zero draws are written — the
// rehearsal mode must be provably inert.
func TestResetDuePools_ObserveNoOp(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-reset-obs", 1, 1000, PoolResetDaily, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now().UTC()
	due := now.Add(-time.Hour)
	if err := DB.Model(&TenantCreditPool{}).Where("id = ?", pool.ID).
		Updates(map[string]interface{}{
			"current_balance": 100,
			"next_reset_at":   due,
		}).Error; err != nil {
		t.Fatalf("seed due state: %v", err)
	}
	before, err := GetTenantCreditPool("t-reset-obs")
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	results, err := ResetDuePools(context.Background(), now, false)
	if err != nil {
		t.Fatalf("ResetDuePools observe: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Action != PoolResetActionObserved || results[0].Delta != 900 {
		t.Errorf("result = %+v, want action=observed delta=900", results[0])
	}

	after, err := GetTenantCreditPool("t-reset-obs")
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if after.CurrentBalance != before.CurrentBalance ||
		after.MaxBalance != before.MaxBalance ||
		after.ResetPeriod != before.ResetPeriod ||
		!after.LastResetAt.Equal(before.LastResetAt) ||
		!timePtrEqual(after.NextResetAt, before.NextResetAt) ||
		after.AlertThresholdPct != before.AlertThresholdPct ||
		!timePtrEqual(after.AlertFiredAt, before.AlertFiredAt) {
		t.Errorf("observe mode wrote to the pool row: before=%+v after=%+v", *before, *after)
	}

	_, total, derr := ListPoolDraws(pool.ID, 0, 50)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	if total != 0 {
		t.Errorf("observe mode must write zero draws, got %d", total)
	}
}

// Test 9: reset_period "none" and max_balance -1 (unlimited) pools are never
// selected, in either mode — even when forced into a "due" state.
func TestResetDuePools_SkipsNoneAndUnlimitedPools(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	nonePool, err := CreateTenantCreditPool("t-reset-none", 1, 1000, PoolResetNone, 80)
	if err != nil {
		t.Fatalf("create none-period pool: %v", err)
	}
	unlimPool, err := CreateTenantCreditPool("t-reset-unlim", 1, PoolMaxBalanceUnlimited, PoolResetDaily, 80)
	if err != nil {
		t.Fatalf("create unlimited pool: %v", err)
	}

	now := time.Now().UTC()
	due := now.Add(-time.Hour)
	if err := DB.Model(&TenantCreditPool{}).Where("id = ?", nonePool.ID).
		Updates(map[string]interface{}{"current_balance": 50, "next_reset_at": due}).Error; err != nil {
		t.Fatalf("seed none pool: %v", err)
	}
	if err := DB.Model(&TenantCreditPool{}).Where("id = ?", unlimPool.ID).
		Updates(map[string]interface{}{"current_balance": 50, "next_reset_at": due}).Error; err != nil {
		t.Fatalf("seed unlimited pool: %v", err)
	}

	for _, enforce := range []bool{false, true} {
		results, rerr := ResetDuePools(context.Background(), now, enforce)
		if rerr != nil {
			t.Fatalf("ResetDuePools enforce=%v: %v", enforce, rerr)
		}
		if len(results) != 0 {
			t.Errorf("enforce=%v: results = %+v, want none (none-period / unlimited pools must be skipped)", enforce, results)
		}
	}

	gotNone, err := GetTenantCreditPool("t-reset-none")
	if err != nil {
		t.Fatalf("readback none pool: %v", err)
	}
	if gotNone.CurrentBalance != 50 {
		t.Errorf("none-period pool balance = %d, want 50 (untouched)", gotNone.CurrentBalance)
	}
	gotUnlim, err := GetTenantCreditPool("t-reset-unlim")
	if err != nil {
		t.Fatalf("readback unlimited pool: %v", err)
	}
	if gotUnlim.CurrentBalance != 50 {
		t.Errorf("unlimited pool balance = %d, want 50 (untouched)", gotUnlim.CurrentBalance)
	}
}

// Test 10: two concurrent enforce callers racing the same due pool must
// produce exactly one reset — the CAS on next_reset_at serializes them.
func TestResetDuePools_ConcurrentEnforceOnlyOneDraw(t *testing.T) {
	cleanup := setupPoolTestDB(t)
	defer cleanup()

	pool, err := CreateTenantCreditPool("t-reset-race", 1, 1000, PoolResetDaily, 80)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now().UTC()
	due := now.Add(-time.Hour)
	if err := DB.Model(&TenantCreditPool{}).Where("id = ?", pool.ID).
		Updates(map[string]interface{}{"current_balance": 100, "next_reset_at": due}).Error; err != nil {
		t.Fatalf("seed due state: %v", err)
	}

	const callers = 2
	var wg sync.WaitGroup
	errs := make([]error, callers)
	resultsPerCall := make([][]PoolResetResult, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, rerr := ResetDuePools(context.Background(), now, true)
			resultsPerCall[idx] = res
			errs[idx] = rerr
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("goroutine %d: %v", i, e)
		}
	}

	totalApplied := 0
	for _, res := range resultsPerCall {
		totalApplied += len(res)
	}
	if totalApplied != 1 {
		t.Errorf("total applied resets across %d concurrent callers = %d, want 1 (CAS must allow only one)", callers, totalApplied)
	}

	got, err := GetTenantCreditPool("t-reset-race")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 1000 {
		t.Errorf("balance = %d, want 1000", got.CurrentBalance)
	}

	_, total, derr := ListPoolDraws(pool.ID, 0, 50)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	if total != 1 {
		t.Errorf("draw rows = %d, want exactly 1 (no double reset)", total)
	}
}
