package repo

// credit_pool_fund_pg_test.go — real-Postgres coverage for the idempotent
// pool-funding path (FundPoolIdempotent / topupPoolInTx / isUniqueViolation).
//
// This path MUST run on real Postgres: idempotency hinges on the UNIQUE index
// over credit_pool_fund_events.event_id and isUniqueViolation matching the PG
// SQLSTATE 23505 wrapper string. The sqlite pool tests cannot exercise that
// branch. Assertions are on concrete post-state: the pool balance is credited
// exactly once, a replay returns the first event without double-crediting, and
// concurrent replays converge to a single credit (conservation law).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func setupFundPG(t *testing.T) {
	t.Helper()
	SetupTestDB(t)
	if err := DB.AutoMigrate(&entity.TenantCreditPool{}, &entity.TenantCreditPoolDraw{}, &entity.CreditPoolFundEvent{}); err != nil {
		t.Fatalf("migrate pool tables: %v", err)
	}
}

func TestFundPoolIdempotent_FirstFundCreditsExactlyOnce(t *testing.T) {
	setupFundPG(t)
	ctx := context.Background()

	pool, err := CreateTenantCreditPool("t-fund", 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	ev, err := FundPoolIdempotent(ctx, pool.ID, "t-fund", 1000, "evt-1", "billing-outbox", 7)
	if err != nil {
		t.Fatalf("first fund: %v", err)
	}
	if ev == nil || ev.NewBalance != 1000 {
		t.Fatalf("want new_balance 1000, got %+v", ev)
	}

	// Pool balance credited.
	got, err := GetTenantCreditPool("t-fund")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 1000 {
		t.Fatalf("want balance 1000 after fund, got %d", got.CurrentBalance)
	}

	// A credit draw row was written.
	var drawCount int64
	if err := DB.Model(&TenantCreditPoolDraw{}).
		Where("pool_id = ? AND direction = ?", pool.ID, PoolDrawDirectionCredit).
		Count(&drawCount).Error; err != nil {
		t.Fatalf("count draws: %v", err)
	}
	if drawCount != 1 {
		t.Fatalf("want exactly 1 credit draw, got %d", drawCount)
	}
}

func TestFundPoolIdempotent_ReplaySameEventNoDoubleCredit(t *testing.T) {
	setupFundPG(t)
	ctx := context.Background()

	pool, err := CreateTenantCreditPool("t-replay", 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	first, err := FundPoolIdempotent(ctx, pool.ID, "t-replay", 500, "evt-dup", "src", 1)
	if err != nil {
		t.Fatalf("first fund: %v", err)
	}

	// Replay with the same event_id → ErrFundEventExists, no additional credit.
	replay, err := FundPoolIdempotent(ctx, pool.ID, "t-replay", 500, "evt-dup", "src", 1)
	if !errors.Is(err, ErrFundEventExists) {
		t.Fatalf("want ErrFundEventExists on replay, got %v", err)
	}
	if replay == nil || replay.ID != first.ID {
		t.Fatalf("replay must return the original event row, got %+v want id=%d", replay, first.ID)
	}

	got, err := GetTenantCreditPool("t-replay")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 500 {
		t.Fatalf("balance double-credited: want 500, got %d", got.CurrentBalance)
	}

	var eventCount int64
	if err := DB.Model(&entity.CreditPoolFundEvent{}).Where("event_id = ?", "evt-dup").Count(&eventCount).Error; err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("want exactly 1 fund event row, got %d", eventCount)
	}
}

func TestFundPoolIdempotent_CeilingRejected(t *testing.T) {
	setupFundPG(t)
	ctx := context.Background()

	// max_balance 100 → funding 150 must be rejected (ceiling), nothing credited.
	pool, err := CreateTenantCreditPool("t-ceil", 1, 100, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	_, err = FundPoolIdempotent(ctx, pool.ID, "t-ceil", 150, "evt-ceil", "src", 1)
	if !errors.Is(err, ErrPoolWouldExceedCeiling) {
		t.Fatalf("want ErrPoolWouldExceedCeiling, got %v", err)
	}

	got, err := GetTenantCreditPool("t-ceil")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 0 {
		t.Fatalf("ceiling reject must not credit; balance=%d", got.CurrentBalance)
	}
	// No fund event should have been persisted (tx rolled back).
	var cnt int64
	DB.Model(&entity.CreditPoolFundEvent{}).Where("event_id = ?", "evt-ceil").Count(&cnt)
	if cnt != 0 {
		t.Fatalf("rolled-back fund event leaked, count=%d", cnt)
	}
}

func TestFundPoolIdempotent_InputValidation(t *testing.T) {
	setupFundPG(t)
	ctx := context.Background()
	pool, err := CreateTenantCreditPool("t-val", 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := FundPoolIdempotent(ctx, pool.ID, "t-val", 0, "evt-x", "src", 1); err == nil {
		t.Error("want error for amount <= 0")
	}
	if _, err := FundPoolIdempotent(ctx, pool.ID, "t-val", 10, "", "src", 1); err == nil {
		t.Error("want error for empty event_id")
	}
}

// TestFundPoolIdempotent_ConcurrentReplayNeverDoubleCredits fires N concurrent
// funds with the SAME event_id against a real PG pool. The business-critical
// safety property is that the UNIQUE(event_id) index admits AT MOST ONE credit
// no matter how many callers race: the balance ends at a single 250 credit and
// exactly one fund_event row survives. The losing transactions roll back their
// in-tx topup, so no double credit can persist.
//
// Note: on Postgres, a losing goroutine that discovers the duplicate via the
// in-tx INSERT (rather than the fast-path pre-check) may surface a raw driver
// error instead of ErrFundEventExists, because the failed INSERT aborts the
// transaction before the in-tx re-fetch can run (SQLSTATE 25P02). That error
// classification is an accepted rough edge of the in-tx path; the invariant
// this test locks is "no double credit", which holds regardless. Run under
// -race.
func TestFundPoolIdempotent_ConcurrentReplayNeverDoubleCredits(t *testing.T) {
	setupFundPG(t)
	ctx := context.Background()

	pool, err := CreateTenantCreditPool("t-conc", 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	const workers = 12
	var firstWrites int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := FundPoolIdempotent(ctx, pool.ID, "t-conc", 250, "evt-race", "src", 1); err == nil {
				atomic.AddInt64(&firstWrites, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	// At most one goroutine may have committed a credit.
	if firstWrites > 1 {
		t.Fatalf("double-credit: %d goroutines committed a fund for the same event_id", firstWrites)
	}

	got, err := GetTenantCreditPool("t-conc")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 250 {
		t.Fatalf("concurrent idempotency broken: balance=%d want exactly one 250 credit", got.CurrentBalance)
	}
	var eventCount int64
	DB.Model(&entity.CreditPoolFundEvent{}).Where("event_id = ?", "evt-race").Count(&eventCount)
	if eventCount != 1 {
		t.Fatalf("want exactly 1 fund event after race, got %d", eventCount)
	}
}

// TestFundPoolIdempotent_TwoGoroutineBarrierRace pins the concurrency
// contract to the minimal reproduction: exactly two callers racing the SAME
// (tenant_id, event_id) pair through the repo-layer funding path
// (FundPoolIdempotent), released by a closed-channel barrier so both
// goroutines reach the pre-check (tenant_credit_pool.go:365) at nearly the
// same instant instead of serializing behind Go's scheduler — the scenario a
// duplicated BillingOutbox delivery produces. Assertions read the real
// database (fund_events row count, pool balance), not just the two returned
// (event, error) pairs, per the standing rule that concurrency claims must be
// checked against actual DB state.
func TestFundPoolIdempotent_TwoGoroutineBarrierRace(t *testing.T) {
	setupFundPG(t)
	ctx := context.Background()

	const (
		tenantID = "t-barrier"
		amount   = int64(300)
		eventID  = "evt-barrier"
	)
	pool, err := CreateTenantCreditPool(tenantID, 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	start := make(chan struct{})
	var ready, wg sync.WaitGroup
	type outcome struct {
		ev  *CreditPoolFundEvent
		err error
	}
	results := make([]outcome, 2)
	for i := 0; i < 2; i++ {
		ready.Add(1)
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Warm a pool connection BEFORE the barrier: without this the
			// second goroutine is still dialling while the first commits, and
			// the "race" decays into a sequential replay every single round.
			if err := DB.Exec("SELECT 1").Error; err != nil {
				results[idx] = outcome{nil, fmt.Errorf("conn warmup: %w", err)}
				ready.Done()
				return
			}
			ready.Done()
			<-start
			ev, err := FundPoolIdempotent(ctx, pool.ID, tenantID, amount, eventID, "billing-outbox", 1)
			results[idx] = outcome{ev, err}
		}(i)
	}
	ready.Wait()
	close(start)
	wg.Wait()

	// -------- DB-level truth: the load-bearing assertions --------
	var eventCount int64
	if err := DB.Model(&entity.CreditPoolFundEvent{}).
		Where("tenant_id = ? AND event_id = ?", tenantID, eventID).
		Count(&eventCount).Error; err != nil {
		t.Fatalf("count fund_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("fund_events rows for (tenant,event) = %d, want exactly 1", eventCount)
	}

	got, err := GetTenantCreditPool(tenantID)
	if err != nil {
		t.Fatalf("readback pool: %v", err)
	}
	if got.CurrentBalance != amount {
		t.Fatalf("pool balance = %d, want exactly %d (one credit, not zero or double)", got.CurrentBalance, amount)
	}

	// -------- return-value classification: real concurrency semantics --------
	// Measured behaviour (repeated -count runs, GORM logs inspected): the
	// loser usually resolves via the PRE-CHECK fast path — by the time it
	// looks, the winner's row is already committed, so it gets
	// ErrFundEventExists without ever opening its own transaction. The in-tx
	// arm (UNIQUE violation on the loser's own INSERT → the failed statement
	// aborts the tx, the in-tx re-fetch dies with it, and the "insert
	// conflict" wrapper surfaces) needs BOTH goroutines to pass the pre-check
	// before either commits; the warmed-connection barrier above makes that
	// window reachable, but scheduling still decides which arm fires, so both
	// stay accepted below. The DB assertions above are what actually prove no
	// double credit occurred, whichever arm the loser took.
	successes, replayLike := 0, 0
	for i, r := range results {
		switch {
		case r.err == nil:
			successes++
			if r.ev == nil || r.ev.NewBalance != amount {
				t.Errorf("goroutine %d: success result has wrong payload: %+v", i, r.ev)
			}
		case errors.Is(r.err, ErrFundEventExists):
			replayLike++
		case strings.Contains(r.err.Error(), "insert conflict"):
			replayLike++
		default:
			t.Errorf("goroutine %d: neither success nor a recognizable replay/conflict error: %v", i, r.err)
		}
	}
	if successes != 1 || replayLike != 1 {
		t.Fatalf("want exactly 1 success + 1 replay-or-conflict, got successes=%d replayLike=%d (err0=%v, err1=%v)",
			successes, replayLike, results[0].err, results[1].err)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"pg duplicate key", errors.New(`ERROR: duplicate key value violates unique constraint "x" (SQLSTATE 23505)`), true},
		{"sqlite unique", errors.New("UNIQUE constraint failed: credit_pool_fund_events.event_id"), true},
		{"raw sqlstate", errors.New("pq: 23505"), true},
		{"unrelated", errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUniqueViolation(tc.err); got != tc.want {
				t.Errorf("isUniqueViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
