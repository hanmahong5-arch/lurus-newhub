package app

// project_budget_test.go — enforceProjectBudget: the one place a project's
// monthly cap turns into a 402. Same fixture style as tenant_quota_test.go.
//
// Mutation oracle: drop the enforceProjectBudget call from PreConsumeQuota
// and TestPreConsumeQuota_ProjectBudget_DeniesOnTheRelayPath goes green-less
// (the request proceeds to the token freeze); flip the comparison to >= and
// the exact-fit case denies.

import (
	"net/http"
	"os"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func seedBudgetProject(t *testing.T, tenantID string, budget int64) int {
	t.Helper()
	// setupServiceTestDB migrates the service models only; projects is ours.
	if err := repo.DB.AutoMigrate(&entity.Project{}); err != nil {
		t.Fatalf("migrate projects: %v", err)
	}
	p, err := repo.CreateProject(tenantID, "Marketing "+time.Now().Format("150405.000000000"), "")
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := repo.SetProjectMonthlyBudget(tenantID, p.Id, budget); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	return p.Id
}

func seedProjectLog(t *testing.T, tenantID string, projectID, quota int, at time.Time) {
	t.Helper()
	log := repo.Log{
		TenantId:  tenantID,
		UserId:    1,
		Type:      repo.LogTypeConsume,
		Quota:     quota,
		ProjectId: projectID,
		CreatedAt: at.Unix(),
	}
	if err := repo.LOG_DB.Create(&log).Error; err != nil {
		t.Fatalf("seed project log: %v", err)
	}
}

func withProjectBudgetClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := projectBudgetNow
	projectBudgetNow = func() time.Time { return at }
	t.Cleanup(func() { projectBudgetNow = prev })
}

func TestEnforceProjectBudget_NoOps(t *testing.T) {
	setupServiceTestDB(t)
	tenantID := seedTestTenant(t, 0)
	// Untagged request: no lookup at all.
	if err := enforceProjectBudget(tenantID, 0, 1_000_000); err != nil {
		t.Fatalf("unassigned project must never be capped, got %v", err)
	}
	// Project without a cap.
	id := seedBudgetProject(t, tenantID, 0)
	seedProjectLog(t, tenantID, id, 5_000_000, time.Now().UTC())
	if err := enforceProjectBudget(tenantID, id, 1_000_000); err != nil {
		t.Fatalf("budget 0 means no cap, got %v", err)
	}
	// Unknown project: fail open, never 402.
	if err := enforceProjectBudget(tenantID, 999_999_999, 1); err != nil {
		t.Fatalf("a lookup miss must fail open, got %v", err)
	}
	// Empty tenant.
	if err := enforceProjectBudget("", id, 1); err != nil {
		t.Fatalf("empty tenant must skip, got %v", err)
	}
}

func TestEnforceProjectBudget_ExactFitAllows_OneOverDenies(t *testing.T) {
	setupServiceTestDB(t)
	tenantID := seedTestTenant(t, 0)
	id := seedBudgetProject(t, tenantID, 1000)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	withProjectBudgetClock(t, now)
	seedProjectLog(t, tenantID, id, 600, now.Add(-time.Hour))
	seedProjectLog(t, tenantID, id, 900, now.AddDate(0, -1, 0)) // last month: not counted

	if err := enforceProjectBudget(tenantID, id, 400); err != nil {
		t.Fatalf("600 used + 400 requested == 1000 budget must be allowed, got %v", err)
	}
	apiErr := enforceProjectBudget(tenantID, id, 401)
	if apiErr == nil {
		t.Fatal("600 used + 401 requested crosses the 1000 budget and must be denied")
	}
	if apiErr.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", apiErr.StatusCode)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeProjectBudgetExceeded {
		t.Fatalf("code = %s, want project_budget_exceeded", apiErr.GetErrorCode())
	}
	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix(); apiErr.RetryAfterUnix != want {
		t.Fatalf("RetryAfterUnix = %d, want first second of next month %d", apiErr.RetryAfterUnix, want)
	}
}

func TestEnforceProjectBudget_KillSwitch(t *testing.T) {
	setupServiceTestDB(t)
	tenantID := seedTestTenant(t, 0)
	id := seedBudgetProject(t, tenantID, 10)
	seedProjectLog(t, tenantID, id, 10, time.Now().UTC())

	prev := projectBudgetEnforcementEnabled
	projectBudgetEnforcementEnabled = false
	t.Cleanup(func() { projectBudgetEnforcementEnabled = prev })
	if err := enforceProjectBudget(tenantID, id, 1); err != nil {
		t.Fatalf("kill-switch off must skip, got %v", err)
	}
	projectBudgetEnforcementEnabled = true
	if err := enforceProjectBudget(tenantID, id, 1); err == nil {
		t.Fatal("with enforcement on the same request must be denied")
	}
}

// TestPreConsumeQuota_ProjectBudget_DeniesOnTheRelayPath drives the branch
// through PreConsumeQuota itself, the way TenantQuotaExceededRejects does:
// user and tenant checks pass, the token's project is over its cap, so the
// request is refused and nothing is pre-deducted.
func TestPreConsumeQuota_ProjectBudget_DeniesOnTheRelayPath(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	tenantID := seedTestTenant(t, 0)
	projectID := seedBudgetProject(t, tenantID, 1_000_000)
	seedProjectLog(t, tenantID, projectID, 950_000, time.Now().UTC())

	const start = 5_000_000
	userId := seedTestUser(t, db, start)
	key, tokenId := seedTestToken(t, db, userId, start, false)

	c := createTestGinContext()
	c.Set("tenant_id", tenantID)
	relayInfo := &relaycommon.RelayInfo{UserId: userId, TokenId: tokenId, TokenKey: key, ProjectId: projectID}

	apiErr := PreConsumeQuota(c, 200_000, relayInfo)
	if apiErr == nil {
		t.Fatal("950_000 used + 200_000 requested crosses the 1_000_000 project budget; expected a rejection")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeProjectBudgetExceeded {
		t.Fatalf("code = %s, want project_budget_exceeded", apiErr.GetErrorCode())
	}
	if got := userQuota(t, db, userId); got != start {
		t.Errorf("user quota = %d, want %d (untouched on project-budget reject)", got, start)
	}

	// The same request from an untagged token of the same tenant proceeds.
	relayInfo = &relaycommon.RelayInfo{UserId: userId, TokenId: tokenId, TokenKey: key}
	if apiErr := PreConsumeQuota(c, 200_000, relayInfo); apiErr != nil {
		t.Fatalf("untagged token must not be capped by a project budget, got %v", apiErr)
	}
}

func TestParseProjectBudgetEnforcementEnabled_DefaultsOn(t *testing.T) {
	prev, had := os.LookupEnv("PROJECT_BUDGET_ENFORCEMENT_ENABLED")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("PROJECT_BUDGET_ENFORCEMENT_ENABLED", prev)
		} else {
			_ = os.Unsetenv("PROJECT_BUDGET_ENFORCEMENT_ENABLED")
		}
	})
	_ = os.Unsetenv("PROJECT_BUDGET_ENFORCEMENT_ENABLED")
	if !parseProjectBudgetEnforcementEnabled() {
		t.Fatal("a budget an admin set must be enforced by default")
	}
	_ = os.Setenv("PROJECT_BUDGET_ENFORCEMENT_ENABLED", "false")
	if parseProjectBudgetEnforcementEnabled() {
		t.Fatal("PROJECT_BUDGET_ENFORCEMENT_ENABLED=false must switch it off")
	}
}
