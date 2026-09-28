package repo

import (
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// project_budget.go — the per-project monthly spending cap (migration 043).
//
// Two functions: the write side the console uses to set the cap, and the
// read side the pre-consume path uses to compare against it. The read mirrors
// GetTenantMonthlyQuotaUsed exactly (same UTC calendar month, same consume
// filter, same LOG_DB) so a project can never be denied for spend the tenant
// report would not show.

// SetProjectMonthlyBudget stores the cap (quota units, 0 = no cap) on one
// project of the caller's tenant. Select() is what makes writing 0 stick:
// GORM's Updates skips zero values otherwise, and "clear the budget" is a
// real request.
func SetProjectMonthlyBudget(tenantID string, id int, budget int64) (*entity.Project, error) {
	if budget < 0 {
		return nil, errors.New("project monthly budget must be zero or positive")
	}
	row, err := GetProjectByID(tenantID, id)
	if err != nil {
		return nil, err
	}
	row.MonthlyBudgetQuota = budget
	if err := DB.Model(row).
		Where("tenant_id = ?", tenantID).
		Select("monthly_budget_quota").
		Updates(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

// GetProjectMonthlyQuotaUsed sums the project's consume quota for the UTC
// calendar month `period` ("YYYY-MM"). Same window arithmetic as
// GetTenantMonthlyQuotaUsed; the extra project_id predicate rides the
// existing (tenant_id, created_at) index.
func GetProjectMonthlyQuotaUsed(tenantID string, projectID int, period string) (int64, error) {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return 0, fmt.Errorf("invalid period %q: %w", period, err)
	}
	startUnix := t.UTC().Unix()
	endUnix := t.AddDate(0, 1, 0).UTC().Unix()

	var used int64
	err = LOG_DB.Model(&Log{}).
		Select("COALESCE(SUM(quota), 0)").
		Where("tenant_id = ? AND project_id = ? AND type = ? AND created_at >= ? AND created_at < ?",
			tenantID, projectID, LogTypeConsume, startUnix, endUnix).
		Scan(&used).Error
	return used, err
}
