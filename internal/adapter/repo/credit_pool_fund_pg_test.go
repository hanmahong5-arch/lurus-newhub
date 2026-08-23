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
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
// Note: a losing goroutine resolves either via the fast-path pre-check or —
// when it passed the pre-check before the winner committed — via the in-tx
// UNIQUE violation, whose sentinel triggers a rollback and a re-fetch outside
// the transaction (the in-tx re-fetch it used to attempt was dead code on PG:
// the failed INSERT aborts the transaction, SQLSTATE 25P02 — see
// TestFundPoolIdempotent_InTxConflictArm_DeterministicLockWait). Both arms
// surface ErrFundEventExists; any other error class is exactly the raw error
// the HTTP layer would 500 on, so it fails this test. Run under -race.
func TestFundPoolIdempotent_ConcurrentReplayNeverDoubleCredits(t *testing.T) {
	setupFundPG(t)
	ctx := context.Background()

	pool, err := CreateTenantCreditPool("t-conc", 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	const workers = 12
	var firstWrites int64
	workerErrs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, werr := FundPoolIdempotent(ctx, pool.ID, "t-conc", 250, "evt-race", "src", 1)
			workerErrs[idx] = werr
			if werr == nil {
				atomic.AddInt64(&firstWrites, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	// At most one goroutine may have committed a credit.
	if firstWrites > 1 {
		t.Fatalf("double-credit: %d goroutines committed a fund for the same event_id", firstWrites)
	}
	// Every loser must classify as a replay — no error-string tolerance.
	for i, werr := range workerErrs {
		if werr != nil && !errors.Is(werr, ErrFundEventExists) {
			t.Errorf("worker %d: neither success nor ErrFundEventExists: %v", i, werr)
		}
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
	// ErrFundEventExists without ever opening its own transaction. When both
	// goroutines pass the pre-check before either commits, the loser's own
	// INSERT instead hits the UNIQUE index in-tx; that arm ALSO surfaces
	// ErrFundEventExists now, via the re-fetch after rollback (the in-tx
	// re-fetch was dead code on PG — the failed INSERT aborts the
	// transaction — see TestFundPoolIdempotent_InTxConflictArm_
	// DeterministicLockWait, which pins that arm deterministically).
	// Whichever arm fires, the loser must surface ErrFundEventExists and
	// nothing else: a raw error here is exactly what the HTTP layer would
	// 500 on, so no error-string tolerance is acceptable.
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
		default:
			t.Errorf("goroutine %d: neither success nor ErrFundEventExists: %v", i, r.err)
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

// TestFundPoolIdempotent_InTxConflictArm_DeterministicLockWait forces the
// in-transaction 23505 arm (tx.Create → isUniqueViolation → sentinel →
// post-rollback re-fetch) that
// TestFundPoolIdempotent_TwoGoroutineBarrierRace reaches only when the
// scheduler happens to interleave both goroutines past the pre-check — in
// measured runs the loser usually resolves via the pre-check fast path and
// the arm goes unobserved. Determinism here comes from lock ordering, not
// timing:
//
//  1. A raw transaction T1 replicates a winner's writes in production order —
//     UPDATE the pool balance (taking the row lock), then INSERT the fund
//     event (taking the unique-index entry) — and stays OPEN. Reversing that
//     order would deadlock against the loser, which acquires in production
//     order.
//  2. FundPoolIdempotent for the same (tenant, event_id) then runs: its
//     pre-check SELECT cannot see T1's uncommitted row (READ COMMITTED), so
//     it enters its transaction, where the pool UPDATE blocks on T1's row
//     lock.
//  3. T1 commits only after the test OBSERVES a backend blocked by T1's own
//     backend pid (pg_blocking_pids) — positive proof the call is past the
//     pre-check and inside its transaction, so the conflict can only resolve
//     through the in-tx arm.
//  4. The unblocked UPDATE re-evaluates its predicate and applies (+amount on
//     top of T1's — a transient double credit), the draw row is written, and
//     the event INSERT hits the now-committed unique index → SQLSTATE 23505
//     → the arm signals ErrFundEventExists (it cannot read the winner's row:
//     the failed INSERT aborted the transaction), the transaction rolls the
//     double credit and the draw back, and the post-rollback re-fetch
//     returns T1's row.
//
// T1 deliberately writes no draw row: the draw is irrelevant to the conflict
// arm, and its absence lets the final draw-count-zero assertion double as
// proof that the loser's rolled-back transaction left nothing behind.
func TestFundPoolIdempotent_InTxConflictArm_DeterministicLockWait(t *testing.T) {
	setupFundPG(t)
	ctx := context.Background()

	pool, err := CreateTenantCreditPool("t-intx", 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	const eventID = "evt-intx-arm"
	const amount = int64(900)

	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("unwrap sql.DB: %v", err)
	}
	t1, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin winner tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = t1.Rollback()
		}
	}()

	var t1PID int64
	if err := t1.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&t1PID); err != nil {
		t.Fatalf("read winner backend pid: %v", err)
	}

	if res, err := t1.ExecContext(ctx,
		`UPDATE tenant_credit_pools SET current_balance = current_balance + $1, updated_at = now() WHERE id = $2`,
		amount, pool.ID); err != nil {
		t.Fatalf("winner pool update: %v", err)
	} else if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("winner pool update touched %d rows, want 1 — seed drifted, the lock geometry below is void", n)
	}
	if _, err := t1.ExecContext(ctx,
		`INSERT INTO credit_pool_fund_events (event_id, tenant_id, pool_id, amount, new_balance, source, created_at)
		 VALUES ($1, $2, $3, $4, $5, 't1-winner', now())`,
		eventID, "t-intx", pool.ID, amount, amount); err != nil {
		t.Fatalf("winner event insert: %v", err)
	}

	type result struct {
		ev  *CreditPoolFundEvent
		err error
	}
	loserDone := make(chan result, 1)
	loserCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	go func() {
		ev, ferr := FundPoolIdempotent(loserCtx, pool.ID, "t-intx", amount, eventID, "loser", 9)
		loserDone <- result{ev, ferr}
	}()

	// A fatal while the loser is still blocked would let SetupTestDB's
	// cleanup DROP DATABASE race the loser's live connection (and its DB
	// reads race the global-restore) — cancel and drain first, then fail.
	failNow := func(format string, args ...any) {
		t.Helper()
		cancel()
		select {
		case <-loserDone:
		case <-time.After(10 * time.Second):
		}
		t.Fatalf(format, args...)
	}

	// Release T1 only once some backend is observed blocked BY T1's own
	// backend pid — T1 holds locks only on this test's pool row and event
	// index entry, so a blocked-by-T1 backend can only be the loser. Pinning
	// by pid (not query-text matching) stays deterministic even when other
	// packages share the PG cluster or GORM changes its SQL text.
	observed := false
	for i := 0; i < 400; i++ { // ≤10s
		var waiting int64
		if err := DB.Raw(
			`SELECT count(*) FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid))`, t1PID,
		).Scan(&waiting).Error; err != nil {
			failNow("poll pg_blocking_pids: %v", err)
		}
		if waiting >= 1 {
			observed = true
			break
		}
		select {
		case r := <-loserDone:
			t.Fatalf("loser finished before ever blocking (ev=%+v err=%v) — the in-tx arm was NOT exercised", r.ev, r.err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	if !observed {
		failNow("never observed the loser blocked by T1 (pid %d); cannot certify the in-tx arm fired", t1PID)
	}

	if err := t1.Commit(); err != nil {
		failNow("commit winner tx: %v", err)
	}
	committed = true

	r := <-loserDone
	if !errors.Is(r.err, ErrFundEventExists) {
		t.Fatalf("in-tx arm must surface ErrFundEventExists, got %v", r.err)
	}
	if r.ev == nil || r.ev.Source != "t1-winner" {
		t.Fatalf("in-tx arm must return the WINNER's row (source=t1-winner, proving the post-rollback re-fetch returned it), got %+v", r.ev)
	}
	if r.ev.NewBalance != amount {
		t.Fatalf("returned winner row new_balance = %d, want %d", r.ev.NewBalance, amount)
	}

	var evCount int64
	if err := DB.Model(&CreditPoolFundEvent{}).
		Where("tenant_id = ? AND event_id = ?", "t-intx", eventID).Count(&evCount).Error; err != nil {
		t.Fatalf("count events: %v", err)
	}
	if evCount != 1 {
		t.Fatalf("want exactly 1 fund event, got %d", evCount)
	}

	got, err := GetTenantCreditPool("t-intx")
	if err != nil {
		t.Fatalf("pool readback: %v", err)
	}
	if got.CurrentBalance != amount {
		t.Fatalf("pool balance = %d, want %d — the loser's transient double credit must roll back", got.CurrentBalance, amount)
	}

	var drawCount int64
	if err := DB.Model(&TenantCreditPoolDraw{}).
		Where("pool_id = ?", pool.ID).Count(&drawCount).Error; err != nil {
		t.Fatalf("count draws: %v", err)
	}
	if drawCount != 0 {
		t.Fatalf("want 0 draw rows (T1 wrote none; the loser's must roll back with its transaction), got %d", drawCount)
	}
}
