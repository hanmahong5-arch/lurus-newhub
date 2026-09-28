package app

import (
	"fmt"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// project_budget.go — per-project monthly spending cap on the pre-consume
// path (projects.monthly_budget_quota, migration 043). Shape and failure
// policy mirror enforceTenantQuota: same UTC calendar month, fail OPEN on any
// lookup error (a budget check must never turn a database hiccup into a 402
// for a paying customer), 402 with Retry-After = the next month's first
// second on denial.
//
// Unlike the tenant cap this is ON by default: a budget only exists because a
// tenant admin typed one on the Projects page, and a cap that is set but
// silently not enforced is the dead configuration this repo's structural
// tests police. PROJECT_BUDGET_ENFORCEMENT_ENABLED=false is the kill-switch.
//
// COST: one primary-key read of the project per project-tagged request, plus
// one SUM over the project's rows for the month when a cap is set. Untagged
// tokens (project_id 0, the majority today) pay nothing.

var projectBudgetEnforcementEnabled = parseProjectBudgetEnforcementEnabled()

func parseProjectBudgetEnforcementEnabled() bool {
	return common.GetEnvOrDefaultBool("PROJECT_BUDGET_ENFORCEMENT_ENABLED", true)
}

// projectBudgetNow is the clock the month window is taken from; a seam so
// tests can sit at a month boundary.
var projectBudgetNow = time.Now

// enforceProjectBudget answers nil when the request may proceed and a typed
// 402 when adding preConsumedQuota to the project's spend this month would
// cross its cap. No-ops: enforcement off, untagged request, no cap set.
func enforceProjectBudget(tenantID string, projectID int, preConsumedQuota int) *types.NewAPIError {
	if !projectBudgetEnforcementEnabled || projectID == entity.ProjectUnassigned || tenantID == "" {
		return nil
	}
	project, err := repo.GetProjectByID(tenantID, projectID)
	if err != nil {
		// Fail open. A deleted project's tokens were detached at delete time,
		// so a not-found here is a race, not a policy decision.
		common.SysError(fmt.Sprintf("enforceProjectBudget: project lookup failed tenant_id=%s project_id=%d err=%s", tenantID, projectID, err.Error()))
		return nil
	}
	if project.MonthlyBudgetQuota <= 0 {
		return nil
	}

	now := projectBudgetNow().UTC()
	period := now.Format("2006-01")
	used, err := repo.GetProjectMonthlyQuotaUsed(tenantID, projectID, period)
	if err != nil {
		common.SysError(fmt.Sprintf("enforceProjectBudget: monthly spend query failed tenant_id=%s project_id=%d period=%s err=%s",
			tenantID, projectID, period, err.Error()))
		return nil
	}

	if used+int64(preConsumedQuota) > project.MonthlyBudgetQuota {
		common.SysLogf(`{"event":"project_budget_denied","tenant_id":"%s","project_id":%d,"current_used":%d,"monthly_budget":%d,"requested":%d,"decision":"denied"}`,
			tenantID, projectID, used, project.MonthlyBudgetQuota, preConsumedQuota)
		apiErr := types.NewErrorWithStatusCode(
			fmt.Errorf("project monthly budget exceeded: used %d, budget %d, requested %d",
				used, project.MonthlyBudgetQuota, preConsumedQuota),
			types.ErrorCodeProjectBudgetExceeded,
			http.StatusPaymentRequired,
			types.ErrOptionWithSkipRetry(),
			types.ErrOptionWithNoRecordErrorLog(),
		)
		apiErr.RetryAfterUnix = nextMonthStartUnix(now)
		return apiErr
	}
	return nil
}
