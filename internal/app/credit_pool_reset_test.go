package app

// credit_pool_reset_test.go — app-layer coverage for the scheduled
// credit-pool reset wrapper: env/mode plumbing, metrics, and the audit row
// written on an actual enforce reset. The repo-layer CAS/conservation
// behaviour itself is covered by internal/adapter/repo/tenant_credit_pool_test.go
// (ResetDuePools); this file proves the app layer reads
// CREDIT_POOL_RESET_MODE (or the rehearsal endpoint's explicit mode),
// increments metrics.CreditPoolResetTotal per pool, and only touches
// metrics.CreditPoolBalance / writes a billing.pool_reset audit row when a
// reset actually happened (enforce mode, CAS won).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// seedDuePool creates a pool that is due for a scheduled reset right now:
// daily period, next_reset_at one hour in the past, ceiling 1000, current
// balance 100 (so a real reset would credit 900).
func seedDuePool(t *testing.T, tenantID string) *repo.TenantCreditPool {
	t.Helper()
	pool, err := repo.CreateTenantCreditPool(tenantID, 1, 1000, repo.PoolResetDaily, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	due := time.Now().UTC().Add(-time.Hour)
	if err := repo.DB.Model(&repo.TenantCreditPool{}).Where("id = ?", pool.ID).
		Updates(map[string]interface{}{"current_balance": 100, "next_reset_at": due}).Error; err != nil {
		t.Fatalf("seed due state: %v", err)
	}
	return pool
}

// TestResetDuePools_DefaultsToObserve proves CREDIT_POOL_RESET_MODE unset (or
// anything other than the literal "enforce") writes nothing: the byte-
// identical guarantee at the app layer, not just the repo layer.
func TestResetDuePools_DefaultsToObserve(t *testing.T) {
	setupServiceTestDB(t)
	seedPoolTables(t, repo.DB)
	governance.SetAuditWriter(discardAuditWriter{})
	t.Cleanup(func() { governance.SetAuditWriter(discardAuditWriter{}) })

	pool := seedDuePool(t, "t-app-reset-default")

	before := testutil.ToFloat64(metrics.CreditPoolResetTotal.WithLabelValues("t-app-reset-default", repo.PoolResetActionObserved))

	results, err := ResetDuePools(context.Background())
	if err != nil {
		t.Fatalf("ResetDuePools: %v", err)
	}
	if len(results) != 1 || results[0].Action != repo.PoolResetActionObserved {
		t.Fatalf("results = %+v, want one observed result (default mode)", results)
	}

	got, err := repo.GetTenantCreditPool("t-app-reset-default")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 100 {
		t.Errorf("balance = %d, want 100 (default mode must not write)", got.CurrentBalance)
	}

	if delta := testutil.ToFloat64(metrics.CreditPoolResetTotal.WithLabelValues("t-app-reset-default", repo.PoolResetActionObserved)) - before; delta != 1 {
		t.Errorf("observed counter delta = %v, want 1", delta)
	}

	_, total, derr := repo.ListPoolDraws(pool.ID, 0, 10)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	if total != 0 {
		t.Errorf("default mode wrote %d draws, want 0", total)
	}
}

// TestResetDuePools_EnvEnforceWritesAndAudits proves CREDIT_POOL_RESET_MODE=enforce
// applies the reset, bumps the CreditPoolResetTotal{reset} counter, sets
// CreditPoolBalance to the new ceiling, and writes exactly one
// billing.pool_reset audit event carrying the pool id / delta.
func TestResetDuePools_EnvEnforceWritesAndAudits(t *testing.T) {
	setupServiceTestDB(t)
	seedPoolTables(t, repo.DB)
	t.Setenv("CREDIT_POOL_RESET_MODE", "enforce")

	capw := &captureAuditWriter{events: make(chan *entity.AuditEvent, 4)}
	governance.SetAuditWriter(capw)
	t.Cleanup(func() { governance.SetAuditWriter(discardAuditWriter{}) })

	pool := seedDuePool(t, "t-app-reset-enforce")

	beforeReset := testutil.ToFloat64(metrics.CreditPoolResetTotal.WithLabelValues("t-app-reset-enforce", repo.PoolResetActionReset))

	results, err := ResetDuePools(context.Background())
	if err != nil {
		t.Fatalf("ResetDuePools: %v", err)
	}
	if len(results) != 1 || results[0].Action != repo.PoolResetActionReset || results[0].Delta != 900 {
		t.Fatalf("results = %+v, want one reset result with delta 900", results)
	}

	got, err := repo.GetTenantCreditPool("t-app-reset-enforce")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.CurrentBalance != 1000 {
		t.Errorf("balance = %d, want 1000 (enforce must refill to ceiling)", got.CurrentBalance)
	}

	if delta := testutil.ToFloat64(metrics.CreditPoolResetTotal.WithLabelValues("t-app-reset-enforce", repo.PoolResetActionReset)) - beforeReset; delta != 1 {
		t.Errorf("reset counter delta = %v, want 1", delta)
	}
	if got := testutil.ToFloat64(metrics.CreditPoolBalance.WithLabelValues("t-app-reset-enforce")); got != 1000 {
		t.Errorf("CreditPoolBalance gauge = %v, want 1000", got)
	}

	select {
	case ev := <-capw.events:
		if ev.Action != governance.ActionBillingPoolReset {
			t.Errorf("audit action = %q, want %q", ev.Action, governance.ActionBillingPoolReset)
		}
		if ev.ActorType != governance.ActorSystem {
			t.Errorf("audit actor = %q, want %q", ev.ActorType, governance.ActorSystem)
		}
		if ev.TenantID != "t-app-reset-enforce" {
			t.Errorf("audit tenant = %q, want t-app-reset-enforce", ev.TenantID)
		}
		if !strings.Contains(ev.Details, `"delta":900`) || !strings.Contains(ev.Details, `"pool_id":`) {
			t.Errorf("audit details missing pool_id/delta: %q", ev.Details)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for billing.pool_reset audit event")
	}

	_, total, derr := repo.ListPoolDraws(pool.ID, 0, 10)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	if total != 1 {
		t.Errorf("draw count = %d, want 1", total)
	}
}

// TestResetDuePoolsWithMode_QueryOverridesEnv proves the rehearsal endpoint's
// explicit mode wins over the environment in both directions: env=enforce +
// mode=observe must not write, and env unset + mode=enforce must write.
func TestResetDuePoolsWithMode_QueryOverridesEnv(t *testing.T) {
	setupServiceTestDB(t)
	seedPoolTables(t, repo.DB)
	governance.SetAuditWriter(discardAuditWriter{})
	t.Cleanup(func() { governance.SetAuditWriter(discardAuditWriter{}) })

	t.Setenv("CREDIT_POOL_RESET_MODE", "enforce")
	seedDuePool(t, "t-app-reset-override-a")
	results, err := ResetDuePoolsWithMode(context.Background(), "observe")
	if err != nil {
		t.Fatalf("ResetDuePoolsWithMode observe: %v", err)
	}
	if len(results) != 1 || results[0].Action != repo.PoolResetActionObserved {
		t.Fatalf("results = %+v, want observed (mode=observe must override env=enforce)", results)
	}
	got, err := repo.GetTenantCreditPool("t-app-reset-override-a")
	if err != nil {
		t.Fatalf("readback a: %v", err)
	}
	if got.CurrentBalance != 100 {
		t.Errorf("balance = %d, want 100 (mode=observe must not write despite env=enforce)", got.CurrentBalance)
	}

	t.Setenv("CREDIT_POOL_RESET_MODE", "")
	seedDuePool(t, "t-app-reset-override-b")
	// Tenant a's pool is still due (the observe call above did not advance
	// next_reset_at), so this pass legitimately picks up BOTH pools — the
	// assertion below is scoped to tenant b specifically rather than the
	// overall result count.
	results2, err := ResetDuePoolsWithMode(context.Background(), "enforce")
	if err != nil {
		t.Fatalf("ResetDuePoolsWithMode enforce: %v", err)
	}
	var bResult *repo.PoolResetResult
	for i := range results2 {
		if results2[i].TenantID == "t-app-reset-override-b" {
			bResult = &results2[i]
		}
	}
	if bResult == nil || bResult.Action != repo.PoolResetActionReset {
		t.Fatalf("results = %+v, want a reset result for tenant b (mode=enforce must override unset env)", results2)
	}
	got2, err := repo.GetTenantCreditPool("t-app-reset-override-b")
	if err != nil {
		t.Fatalf("readback b: %v", err)
	}
	if got2.CurrentBalance != 1000 {
		t.Errorf("balance = %d, want 1000 (mode=enforce must write despite unset env)", got2.CurrentBalance)
	}
}
