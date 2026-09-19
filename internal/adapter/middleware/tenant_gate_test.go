package middleware

// tenant_gate_test.go — L9 (cycle 12): a token whose owning tenant no longer
// exists must be visible, and deniable.
//
// repo.DeleteTenant is a SOFT delete (tenants carries gorm.DeletedAt), and the
// three guards in auth.go were written as
// `tErr == nil && tenant != nil && tenant.IsDisabled()`. A soft-deleted tenant
// makes GetTenantByID return an error, so the condition was false and the
// request was admitted: the console failed closed (its own tenant lookups
// 404) while the money path failed open — the deleted tenant's tokens kept
// relaying and spending. The lookup error for a deleted row was also
// indistinguishable from a DB fault, which is why the fix is a shared
// repo.TenantGate that separates "no row" from "backend error" and puts the
// first behind TENANT_MISSING_MODE (default observe: count + log + admit).
//
// The gate is driven here through the real TokenAuth relay path
// (a1_provisioned_token_auth_test.go's harness), not by calling
// repo.TenantGate directly, plus one direct table test for the modes.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

// tenantGateToken builds a relay-ready token owned by tenantID.
func tenantGateToken(tenantID, key string) *repo.Token {
	return &repo.Token{
		UserId:         0, // provisioned/tenant-scoped: no user row needed
		TenantId:       tenantID,
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "tenant-gate-probe",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
}

// tenantGateCount reads the current value of one outcome series.
func tenantGateCount(t *testing.T, outcome string) float64 {
	t.Helper()
	return testutil.ToFloat64(metrics.TenantGateTotal.WithLabelValues(outcome))
}

// TestTokenAuth_SoftDeletedTenant_ObserveMode_AdmitsAndCounts is the headline
// observe-mode lock: the request still relays (the deliberate first step of the
// rollout) but the gate leaves a countable trace, which is what an operator
// reads before flipping the mode.
func TestTokenAuth_SoftDeletedTenant_ObserveMode_AdmitsAndCounts(t *testing.T) {
	t.Setenv(repo.TenantMissingModeEnv, "")

	key := common.GetRandomString(48)
	tenant := &entity.Tenant{
		Id: "t-gate-observe", IDPOrgID: "org-gate-observe", Slug: "gate-observe",
		Name: "Gate Observe", Status: entity.TenantStatusEnabled,
	}
	r, _, cleanup := setupProvisionedTestRouter(t, tenantGateToken(tenant.Id, key), tenant)
	defer cleanup()

	if err := repo.DeleteTenant(tenant.Id); err != nil {
		t.Fatalf("soft delete tenant: %v", err)
	}

	before := tenantGateCount(t, metrics.TenantGateOutcomeMissingObserved)
	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — observe mode must admit; body=%s", w.Code, w.Body.String())
	}
	if after := tenantGateCount(t, metrics.TenantGateOutcomeMissingObserved); after != before+1 {
		t.Errorf("tenant_gate_total{outcome=\"missing_observed\"} = %v, want %v — an admitted deleted-tenant request must still be counted",
			after, before+1)
	}
}

// TestTokenAuth_SoftDeletedTenant_EnforceMode_Denied is the enforce half: with
// TENANT_MISSING_MODE=enforce the same request is refused.
func TestTokenAuth_SoftDeletedTenant_EnforceMode_Denied(t *testing.T) {
	t.Setenv(repo.TenantMissingModeEnv, repo.TenantGateModeEnforce)

	key := common.GetRandomString(48)
	tenant := &entity.Tenant{
		Id: "t-gate-enforce", IDPOrgID: "org-gate-enforce", Slug: "gate-enforce",
		Name: "Gate Enforce", Status: entity.TenantStatusEnabled,
	}
	r, _, cleanup := setupProvisionedTestRouter(t, tenantGateToken(tenant.Id, key), tenant)
	defer cleanup()

	if err := repo.DeleteTenant(tenant.Id); err != nil {
		t.Fatalf("soft delete tenant: %v", err)
	}

	before := tenantGateCount(t, metrics.TenantGateOutcomeMissingDenied)
	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — enforce mode must deny a deleted tenant's token; body=%s",
			w.Code, w.Body.String())
	}
	if after := tenantGateCount(t, metrics.TenantGateOutcomeMissingDenied); after != before+1 {
		t.Errorf("tenant_gate_total{outcome=\"missing_denied\"} = %v, want %v", after, before+1)
	}
}

// TestTokenAuth_DisabledTenant_DeniedInBothModes pins the pre-existing
// behaviour: a row that exists and is disabled is refused whatever the missing
// mode says — the mode governs absent rows only.
func TestTokenAuth_DisabledTenant_DeniedInBothModes(t *testing.T) {
	for _, mode := range []string{"", repo.TenantGateModeEnforce} {
		t.Run("mode="+mode, func(t *testing.T) {
			t.Setenv(repo.TenantMissingModeEnv, mode)

			key := common.GetRandomString(48)
			tenant := &entity.Tenant{
				Id: "t-gate-disabled-" + mode, IDPOrgID: "org-gate-disabled-" + mode,
				Slug: "gate-disabled-" + mode, Name: "Gate Disabled",
				Status: entity.TenantStatusDisabled,
			}
			r, _, cleanup := setupProvisionedTestRouter(t, tenantGateToken(tenant.Id, key), tenant)
			defer cleanup()

			before := tenantGateCount(t, metrics.TenantGateOutcomeDisabledDenied)
			w := provisionedProbe(t, r, key)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 for a disabled tenant; body=%s", w.Code, w.Body.String())
			}
			if after := tenantGateCount(t, metrics.TenantGateOutcomeDisabledDenied); after != before+1 {
				t.Errorf("tenant_gate_total{outcome=\"disabled_denied\"} = %v, want %v", after, before+1)
			}
		})
	}
}

// TestTokenAuth_EnabledTenant_AdmittedInEnforceMode is the negative control:
// enforce mode must not deny a healthy tenant.
func TestTokenAuth_EnabledTenant_AdmittedInEnforceMode(t *testing.T) {
	t.Setenv(repo.TenantMissingModeEnv, repo.TenantGateModeEnforce)

	key := common.GetRandomString(48)
	tenant := &entity.Tenant{
		Id: "t-gate-healthy", IDPOrgID: "org-gate-healthy", Slug: "gate-healthy",
		Name: "Gate Healthy", Status: entity.TenantStatusEnabled,
	}
	r, _, cleanup := setupProvisionedTestRouter(t, tenantGateToken(tenant.Id, key), tenant)
	defer cleanup()

	w := provisionedProbe(t, r, key)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an enabled tenant under enforce; body=%s", w.Code, w.Body.String())
	}
}

var tenantGateFaultDBCounter int

// installBrokenTenantDB points repo.DB at a database whose connection is
// closed, so every query fails with a driver error that is neither nil nor
// gorm.ErrRecordNotFound — the shape a real backend fault (PG down, pool
// exhausted, network partition) produces. Restores the previous handle on
// cleanup.
func installBrokenTenantDB(t *testing.T) {
	t.Helper()
	tenantGateFaultDBCounter++
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:tenantgatefault%d?mode=memory&cache=shared", tenantGateFaultDBCounter)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.Tenant{}); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql handle: %v", err)
	}
	// Prove the handle really is poisoned before relying on it: a test that
	// silently got a working DB would assert nothing.
	if err := db.Where("id = ?", "probe").First(&repo.Tenant{}).Error; err == nil {
		t.Fatal("the poisoned handle still answers queries; this oracle needs a real backend fault")
	}
	prev := repo.DB
	repo.DB = db
	t.Cleanup(func() { repo.DB = prev })
}

// TestTenantGate_BackendFault_FailsOpen is the oracle for the one branch of
// the gate that admits on an ERROR rather than on a decision.
//
// Every comment in auth.go and repo/tenant.go calls this posture deliberate
// and load-bearing — a DB hiccup must not 403 every console request and every
// relay call at once — but nothing tested it, so a future "tidy-up" that
// folded the default branch into the not-found branch would have turned a
// database blip into a total outage under TENANT_MISSING_MODE=enforce, with
// all of this file still green.
//
// Two things are asserted, in both modes: the request is admitted, and NO
// outcome is counted. The second is what separates "fail-open on a fault"
// from "treated the fault as a missing tenant and happened to admit it":
// counting a fault as missing_observed would also poison the very series an
// operator reads to decide whether enforce is safe.
func TestTenantGate_BackendFault_FailsOpen(t *testing.T) {
	for _, mode := range []string{"", repo.TenantGateModeEnforce} {
		t.Run("mode="+mode, func(t *testing.T) {
			t.Setenv(repo.TenantMissingModeEnv, mode)
			installBrokenTenantDB(t)

			beforeObserved := tenantGateCount(t, metrics.TenantGateOutcomeMissingObserved)
			beforeDenied := tenantGateCount(t, metrics.TenantGateOutcomeMissingDenied)
			beforeDisabled := tenantGateCount(t, metrics.TenantGateOutcomeDisabledDenied)

			ok, reason := repo.TenantGate("t-gate-backend-fault")
			if !ok {
				t.Errorf("TenantGate under a DB fault = (%v, %q), want admitted — a backend fault must not 403 every request",
					ok, reason)
			}
			if reason != "" {
				t.Errorf("reason = %q, want empty — a fault is not a tenant decision", reason)
			}
			if got := tenantGateCount(t, metrics.TenantGateOutcomeMissingObserved); got != beforeObserved {
				t.Errorf("missing_observed moved %v -> %v on a DB fault; that series must only count tenants that really have no row",
					beforeObserved, got)
			}
			if got := tenantGateCount(t, metrics.TenantGateOutcomeMissingDenied); got != beforeDenied {
				t.Errorf("missing_denied moved %v -> %v on a DB fault", beforeDenied, got)
			}
			if got := tenantGateCount(t, metrics.TenantGateOutcomeDisabledDenied); got != beforeDisabled {
				t.Errorf("disabled_denied moved %v -> %v on a DB fault", beforeDisabled, got)
			}
		})
	}
}

// TestTenantGate_Table is the direct unit half: the ids that bypass the gate
// entirely, and the mode switch on an absent row.
func TestTenantGate_Table(t *testing.T) {
	key := common.GetRandomString(48)
	tenant := &entity.Tenant{
		Id: "t-gate-table", IDPOrgID: "org-gate-table", Slug: "gate-table",
		Name: "Gate Table", Status: entity.TenantStatusEnabled,
	}
	_, _, cleanup := setupProvisionedTestRouter(t, tenantGateToken(tenant.Id, key), tenant)
	defer cleanup()

	t.Run("empty id is admitted", func(t *testing.T) {
		t.Setenv(repo.TenantMissingModeEnv, repo.TenantGateModeEnforce)
		if ok, reason := repo.TenantGate(""); !ok {
			t.Errorf("TenantGate(\"\") = (%v, %q), want admitted — a legacy row carries no tenant id", ok, reason)
		}
	})

	t.Run("default tenant is exempt", func(t *testing.T) {
		t.Setenv(repo.TenantMissingModeEnv, repo.TenantGateModeEnforce)
		if ok, reason := repo.TenantGate("default"); !ok {
			t.Errorf("TenantGate(\"default\") = (%v, %q), want admitted — the bootstrap tenant has no row on some deployments",
				ok, reason)
		}
	})

	t.Run("absent row follows the mode", func(t *testing.T) {
		t.Setenv(repo.TenantMissingModeEnv, "")
		if ok, _ := repo.TenantGate("t-gate-never-existed"); !ok {
			t.Errorf("observe mode denied an absent tenant; want admitted")
		}
		t.Setenv(repo.TenantMissingModeEnv, repo.TenantGateModeEnforce)
		ok, reason := repo.TenantGate("t-gate-never-existed")
		if ok {
			t.Errorf("enforce mode admitted an absent tenant; want denied")
		}
		if reason != repo.TenantGateReasonMissing {
			t.Errorf("reason = %q, want %q", reason, repo.TenantGateReasonMissing)
		}
	})

	t.Run("garbage mode observes", func(t *testing.T) {
		t.Setenv(repo.TenantMissingModeEnv, "Enforce")
		if ok, _ := repo.TenantGate("t-gate-never-existed"); !ok {
			t.Errorf("mode %q was treated as enforce; only the exact value %q enforces",
				"Enforce", repo.TenantGateModeEnforce)
		}
	})
}
