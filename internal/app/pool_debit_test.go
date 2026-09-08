package app

// pool_debit_test.go — P0-3: post-consume pool debit must never be silently
// dropped. Exhaustion falls back to OverdraftDebitPool (negative balance +
// relay_overdraft draw row); unlimited / absent pools skip the debit.
//
// The threshold-alert-hook tests below (TestDebitTenantPool_*ThresholdAlert*)
// cover Lane L4's Step D: the post-success/overdraft best-effort call into
// hubnats.PublishPoolThreshold. They drive it through the real debitTenantPool
// call (never a hand-built struct standing in for the hook) and assert on
// the two durable side effects — the billing.pool_threshold audit row and
// tenant_credit_pools.alert_fired_at — rather than on the NATS wire itself,
// since LLM_QUOTA_NATS_ENABLED is unset (false) in this hermetic tier, which
// is itself the "delivery=recorded_only" path pool_threshold.go's own tests
// cover directly.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"gorm.io/gorm"
)

// seedPoolTables adds the credit-pool tables to the shared service test DB
// (setupServiceTestDB only migrates user/token/log/channel/option/tenant).
func seedPoolTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&entity.TenantCreditPool{}, &entity.TenantCreditPoolDraw{}); err != nil {
		t.Fatalf("migrate pool tables: %v", err)
	}
}

// seedTenantToken creates a token bound to tenantID and returns its ID.
func seedTenantToken(t *testing.T, db *gorm.DB, userId int, tenantID string) int {
	t.Helper()
	token := repo.Token{
		UserId:         userId,
		Key:            common.GetRandomString(32),
		Status:         common.TokenStatusEnabled,
		Name:           "pool-test-token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
		TenantId:       tenantID,
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return token.Id
}

func TestDebitTenantPool_ExhaustedWritesOverdraft(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1000)
	tokenId := seedTenantToken(t, db, userId, "t-app-ovd")

	pool, err := repo.CreateTenantCreditPool("t-app-ovd", 1, 1000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, "t-app-ovd", 3, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	// Debit 10 against balance 3: DebitPool rejects, fallback must overdraft.
	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 10)

	got, err := repo.GetTenantCreditPool("t-app-ovd")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != -7 {
		t.Errorf("balance = %d, want -7 (3 - 10, debt recorded)", got.CurrentBalance)
	}

	draws, _, err := repo.ListPoolDraws(pool.ID, 0, 50)
	if err != nil {
		t.Fatalf("list draws: %v", err)
	}
	found := false
	for _, d := range draws {
		if d.Reason == repo.PoolDrawReasonOverdraft && d.Amount == 10 && d.TokenID == tokenId {
			found = true
		}
	}
	if !found {
		t.Errorf("no relay_overdraft draw row for token %d — debit was dropped", tokenId)
	}
}

func TestDebitTenantPool_NormalDebitWhenFunded(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1000)
	tokenId := seedTenantToken(t, db, userId, "t-app-ok")

	pool, err := repo.CreateTenantCreditPool("t-app-ok", 1, 1000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, "t-app-ok", 100, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 10)

	got, err := repo.GetTenantCreditPool("t-app-ok")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 90 {
		t.Errorf("balance = %d, want 90 (normal relay_debit path)", got.CurrentBalance)
	}
}

func TestDebitTenantPool_UnlimitedPoolSkips(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1000)
	tokenId := seedTenantToken(t, db, userId, "t-app-unl")

	pool, err := repo.CreateTenantCreditPool("t-app-unl", 1, repo.PoolMaxBalanceUnlimited, repo.PoolResetNone, 0)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 10)

	got, err := repo.GetTenantCreditPool("t-app-unl")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 0 {
		t.Errorf("unlimited pool balance = %d, want 0 (debit must be skipped)", got.CurrentBalance)
	}
	draws, total, err := repo.ListPoolDraws(pool.ID, 0, 10)
	if err != nil {
		t.Fatalf("list draws: %v", err)
	}
	if total != 0 || len(draws) != 0 {
		t.Errorf("unlimited pool has %d draws, want 0", total)
	}
}

func TestDebitTenantPool_NoPoolRowSkips(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1000)
	tokenId := seedTenantToken(t, db, userId, "t-app-nopool")

	// No pool row for the tenant: must no-op without error or draws.
	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 10)

	var count int64
	if err := db.Model(&entity.TenantCreditPoolDraw{}).Count(&count).Error; err != nil {
		t.Fatalf("count draws: %v", err)
	}
	if count != 0 {
		t.Errorf("draws written without a pool row: %d, want 0", count)
	}
}

// TestDebitTenantPool_CrossingThresholdFiresAlertOnce: pool max=1000,
// threshold_pct=80 (alert line = balance < 800), funded to 850 (above the
// line). A debit of 100 lands the balance at 750 — crossing the line — and
// must invoke the pool-threshold alert hook exactly once: one
// billing.pool_threshold audit event carrying balance=750, and
// tenant_credit_pools.alert_fired_at durably set.
func TestDebitTenantPool_CrossingThresholdFiresAlertOnce(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1000)
	tokenId := seedTenantToken(t, db, userId, "t-app-alert-cross")

	pool, err := repo.CreateTenantCreditPool("t-app-alert-cross", 1, 1000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, "t-app-alert-cross", 850, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	capw := &captureAuditWriter{events: make(chan *entity.AuditEvent, 4)}
	governance.SetAuditWriter(capw)
	t.Cleanup(func() { governance.SetAuditWriter(discardAuditWriter{}) })

	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 100)

	// Two audit events fire from one debitTenantPool call — the normal
	// billing.debit (always) and billing.pool_threshold (crossing) — both
	// via async RecordAuditEvent goroutines, so arrival order is not
	// guaranteed; drain and filter by action instead of assuming order.
	events := drainAuditEvents(t, capw, 2*time.Second, 2)
	thresholdEvents := filterAuditEvents(events, governance.ActionBillingPoolThreshold)
	if len(thresholdEvents) != 1 {
		t.Fatalf("billing.pool_threshold events = %d, want 1 (got %d total events: %+v)", len(thresholdEvents), len(events), events)
	}
	ev := thresholdEvents[0]
	if ev.TenantID != "t-app-alert-cross" {
		t.Errorf("audit tenant = %q, want t-app-alert-cross", ev.TenantID)
	}
	if !containsAll(ev.Details, `"balance":750`, `"max_balance":1000`, `"threshold_pct":80`) {
		t.Errorf("audit details missing balance/max_balance/threshold_pct: %q", ev.Details)
	}

	got, err := repo.GetTenantCreditPool("t-app-alert-cross")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.AlertFiredAt == nil {
		t.Error("alert_fired_at must be set after the threshold-crossing debit")
	}
}

// TestDebitTenantPool_SecondDebitWithinWindowNotInvoked chains two debits:
// the first crosses the threshold and fires (asserted above); the second,
// still below the line, must NOT invoke the hook again — the just-set
// alert_fired_at is inside hubnats.SchemaDedupWindow(), which
// maybeAlertPoolThreshold's own precheck must honor before ever calling the
// publisher.
func TestDebitTenantPool_SecondDebitWithinWindowNotInvoked(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1000)
	tokenId := seedTenantToken(t, db, userId, "t-app-alert-2nd")

	pool, err := repo.CreateTenantCreditPool("t-app-alert-2nd", 1, 1000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, "t-app-alert-2nd", 850, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	capw := &captureAuditWriter{events: make(chan *entity.AuditEvent, 8)}
	governance.SetAuditWriter(capw)
	t.Cleanup(func() { governance.SetAuditWriter(discardAuditWriter{}) })

	// First debit crosses (850 -> 750) and fires: billing.debit + billing.pool_threshold.
	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 100)
	firstEvents := drainAuditEvents(t, capw, 2*time.Second, 2)
	if got := filterAuditEvents(firstEvents, governance.ActionBillingPoolThreshold); len(got) != 1 {
		t.Fatalf("first debit: billing.pool_threshold events = %d, want 1 (events: %+v)", len(got), firstEvents)
	}

	// Second debit (750 -> 740) is still below the 800 line, but the alert
	// was just fired — must not invoke the hook again: only billing.debit.
	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 10)
	secondEvents := drainAuditEvents(t, capw, 500*time.Millisecond, 1)
	if got := filterAuditEvents(secondEvents, governance.ActionBillingPoolThreshold); len(got) != 0 {
		t.Fatalf("second debit fired again inside the dedup window: %+v", got)
	}

	got, err := repo.GetTenantCreditPool("t-app-alert-2nd")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 740 {
		t.Errorf("balance = %d, want 740", got.CurrentBalance)
	}
}

// TestDebitTenantPool_AlertFiredAtInsideWindowNeverInvoked: a pool that is
// already below the alert line AND already has a recent alert_fired_at must
// never invoke the hook on a debit that keeps it below the line — the
// precheck must short-circuit before calling hubnats.PublishPoolThreshold at
// all, not rely on the publisher's own dedup.
func TestDebitTenantPool_AlertFiredAtInsideWindowNeverInvoked(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1000)
	tokenId := seedTenantToken(t, db, userId, "t-app-alert-window")

	pool, err := repo.CreateTenantCreditPool("t-app-alert-window", 1, 1000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, "t-app-alert-window", 750, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}
	firedAt := time.Now().UTC().Add(-5 * time.Minute) // well inside the 1h window
	if err := db.Model(&entity.TenantCreditPool{}).Where("id = ?", pool.ID).
		Update("alert_fired_at", firedAt).Error; err != nil {
		t.Fatalf("seed alert_fired_at: %v", err)
	}

	capw := &captureAuditWriter{events: make(chan *entity.AuditEvent, 4)}
	governance.SetAuditWriter(capw)
	t.Cleanup(func() { governance.SetAuditWriter(discardAuditWriter{}) })

	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 10)

	events := drainAuditEvents(t, capw, 500*time.Millisecond, 1)
	if got := filterAuditEvents(events, governance.ActionBillingPoolThreshold); len(got) != 0 {
		t.Fatalf("hook fired despite alert_fired_at inside the window: %+v", got)
	}
}

// TestDebitTenantPool_OverdraftBranchFiresAlertOnce: findings L4 item 3 —
// the overdraft branch (pool already exhausted, debit falls back to
// OverdraftDebitPool) had no test asserting the alert hook is invoked.
// Fund the pool to 3, debit 10: DebitPool rejects (exhausted), the
// fallback records relay_overdraft and lands the balance at -7, which is
// far below the 80% line (800) — the hook must fire exactly once.
func TestDebitTenantPool_OverdraftBranchFiresAlertOnce(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	userId := seedTestUser(t, db, 1000)
	tokenId := seedTenantToken(t, db, userId, "t-app-overdraft-alert")

	pool, err := repo.CreateTenantCreditPool("t-app-overdraft-alert", 1, 1000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, "t-app-overdraft-alert", 3, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	capw := &captureAuditWriter{events: make(chan *entity.AuditEvent, 4)}
	governance.SetAuditWriter(capw)
	t.Cleanup(func() { governance.SetAuditWriter(discardAuditWriter{}) })

	debitTenantPool(&relaycommon.RelayInfo{TokenId: tokenId}, 10)

	// want=1: the overdraft branch writes exactly one audit event
	// (billing.pool_threshold from maybeAlertPoolThreshold) — unlike the
	// normal-debit branch it does NOT also write a billing.debit row.
	// want=2 here would make drainAuditEvents always burn the full 2s
	// deadline waiting for an event that never arrives (residuals round 2
	// nit).
	events := drainAuditEvents(t, capw, 2*time.Second, 1)
	thresholdEvents := filterAuditEvents(events, governance.ActionBillingPoolThreshold)
	if len(thresholdEvents) != 1 {
		t.Fatalf("billing.pool_threshold events = %d, want 1 (got %d total events: %+v)", len(thresholdEvents), len(events), events)
	}
	if !containsAll(thresholdEvents[0].Details, `"balance":-7`, `"max_balance":1000`, `"threshold_pct":80`) {
		t.Errorf("audit details missing overdraft balance: %q", thresholdEvents[0].Details)
	}

	got, err := repo.GetTenantCreditPool("t-app-overdraft-alert")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.AlertFiredAt == nil {
		t.Error("alert_fired_at must be set after the overdraft-branch debit")
	}
}

// TestMaybeAlertPoolThreshold_AuditRowWrittenDespitePublishError: findings
// L4 item 2 — when publishPoolThresholdSeam (hubnats.PublishPoolThreshold)
// returns fired=true alongside a non-nil error (schema mark landed, the
// NATS wire leg failed), the billing.pool_threshold audit row must still be
// written — the dedup window is already consumed either way, so skipping
// the audit row would leave the pool silently unobserved for the whole
// window. Revert the `if !fired { return }` reorder in
// maybeAlertPoolThreshold (checking err before fired) and this goes red.
func TestMaybeAlertPoolThreshold_AuditRowWrittenDespitePublishError(t *testing.T) {
	prevSeam := publishPoolThresholdSeam
	seamErr := errors.New("simulated nats wire failure")
	publishPoolThresholdSeam = func(_ context.Context, _ string, _ int64, _, _ int64, _ int) (bool, string, error) {
		return true, "recorded_only", seamErr
	}
	t.Cleanup(func() { publishPoolThresholdSeam = prevSeam })

	capw := &captureAuditWriter{events: make(chan *entity.AuditEvent, 4)}
	governance.SetAuditWriter(capw)
	t.Cleanup(func() { governance.SetAuditWriter(discardAuditWriter{}) })

	beforeErrCount := testutil.ToFloat64(metrics.CreditPoolAlertHookErrorTotal)

	pool := &repo.TenantCreditPool{
		ID:                99,
		TenantID:          "t-app-seam-err",
		MaxBalance:        1000,
		CurrentBalance:    850,
		AlertThresholdPct: 80,
		AlertFiredAt:      nil,
	}
	maybeAlertPoolThreshold(pool, 700) // 700 < 800 line -> ShouldAlert true

	events := drainAuditEvents(t, capw, 2*time.Second, 1)
	thresholdEvents := filterAuditEvents(events, governance.ActionBillingPoolThreshold)
	if len(thresholdEvents) != 1 {
		t.Fatalf("billing.pool_threshold events = %d, want 1 despite the seam error (events: %+v)", len(thresholdEvents), events)
	}
	if !containsAll(thresholdEvents[0].Details, `"balance":700`, `"delivery":"recorded_only"`, `"error":`) {
		t.Errorf("audit details missing error/delivery: %q", thresholdEvents[0].Details)
	}
	if delta := testutil.ToFloat64(metrics.CreditPoolAlertHookErrorTotal) - beforeErrCount; delta != 1 {
		t.Errorf("CreditPoolAlertHookErrorTotal delta = %v, want 1", delta)
	}
}

// containsAll reports whether s contains every substring in subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// drainAuditEvents collects audit events from capw until at least `want`
// have arrived or timeout elapses, then does one final short (50ms) grace
// drain to catch anything landing just afterward — audit writes are async
// (governance.RecordAuditEvent spawns one goroutine per event), so multiple
// events from one debitTenantPool call can arrive in either order.
func drainAuditEvents(t *testing.T, capw *captureAuditWriter, timeout time.Duration, want int) []*entity.AuditEvent {
	t.Helper()
	var events []*entity.AuditEvent
	deadline := time.After(timeout)
collect:
	for len(events) < want {
		select {
		case ev := <-capw.events:
			events = append(events, ev)
		case <-deadline:
			break collect
		}
	}
	grace := time.After(50 * time.Millisecond)
	for {
		select {
		case ev := <-capw.events:
			events = append(events, ev)
		case <-grace:
			return events
		}
	}
}

// filterAuditEvents returns the subset of events whose Action matches.
func filterAuditEvents(events []*entity.AuditEvent, action string) []*entity.AuditEvent {
	var out []*entity.AuditEvent
	for _, ev := range events {
		if ev.Action == action {
			out = append(out, ev)
		}
	}
	return out
}
