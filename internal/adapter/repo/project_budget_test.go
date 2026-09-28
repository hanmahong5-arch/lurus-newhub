package repo

// project_budget_test.go — the cap's write side must persist 0 (a cleared
// budget is a real request GORM would otherwise skip), and the read side
// must count exactly the rows the spend report counts: this tenant, this
// project, consume type, this UTC month.

import (
	"errors"
	"testing"
	"time"
)

func TestSetProjectMonthlyBudget_PersistsAndClears(t *testing.T) {
	setupSQLiteDB(t)
	p := mustCreateProject(t, "tenant-a", "Marketing")
	if p.MonthlyBudgetQuota != 0 {
		t.Fatalf("new project budget = %d, want 0 (no cap)", p.MonthlyBudgetQuota)
	}

	row, err := SetProjectMonthlyBudget("tenant-a", p.Id, 250_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if row.MonthlyBudgetQuota != 250_000_000 {
		t.Fatalf("returned budget = %d", row.MonthlyBudgetQuota)
	}
	got, err := GetProjectByID("tenant-a", p.Id)
	if err != nil || got.MonthlyBudgetQuota != 250_000_000 {
		t.Fatalf("stored budget = %d err=%v", got.MonthlyBudgetQuota, err)
	}

	// Clearing must stick: Updates without Select would skip the zero.
	if _, err := SetProjectMonthlyBudget("tenant-a", p.Id, 0); err != nil {
		t.Fatal(err)
	}
	got, _ = GetProjectByID("tenant-a", p.Id)
	if got.MonthlyBudgetQuota != 0 {
		t.Fatalf("cleared budget still reads %d", got.MonthlyBudgetQuota)
	}

	if _, err := SetProjectMonthlyBudget("tenant-a", p.Id, -1); err == nil {
		t.Fatal("negative budget must be refused")
	}
	if _, err := SetProjectMonthlyBudget("tenant-b", p.Id, 5); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("another tenant's project must be not-found, got %v", err)
	}
}

func TestGetProjectMonthlyQuotaUsed_CountsExactlyTheReportedRows(t *testing.T) {
	setupSQLiteDB(t)
	p := mustCreateProject(t, "tenant-a", "Marketing")
	other := mustCreateProject(t, "tenant-a", "Research")

	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	in := month.Add(36 * time.Hour).Unix()
	seedSpendLog(t, "tenant-a", p.Id, 100, 1, 1, in)
	seedSpendLog(t, "tenant-a", p.Id, 250, 1, 1, month.AddDate(0, 1, 0).Add(-time.Second).Unix()) // last second of the month
	seedSpendLog(t, "tenant-a", other.Id, 1000, 1, 1, in)                                         // other project
	seedSpendLog(t, "tenant-b", p.Id, 1000, 1, 1, in)                                             // other tenant, same id
	seedSpendLog(t, "tenant-a", p.Id, 1000, 1, 1, month.AddDate(0, 1, 0).Unix())                  // first second of next month
	seedSpendLog(t, "tenant-a", p.Id, 1000, 1, 1, month.Add(-time.Second).Unix())                 // previous month
	refund := &Log{UserId: 1, TenantId: "tenant-a", Type: LogTypeRefund, CreatedAt: in, Quota: 1000, ProjectId: p.Id}
	if err := LOG_DB.Create(refund).Error; err != nil {
		t.Fatal(err)
	}

	used, err := GetProjectMonthlyQuotaUsed("tenant-a", p.Id, "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if used != 350 {
		t.Fatalf("used = %d, want 350 (only this tenant+project, consume type, inside the UTC month)", used)
	}
	if used, _ := GetProjectMonthlyQuotaUsed("tenant-a", p.Id, "2026-10"); used != 1000 {
		t.Fatalf("next month used = %d, want 1000", used)
	}
	if _, err := GetProjectMonthlyQuotaUsed("tenant-a", p.Id, "2026-9"); err == nil {
		t.Fatal("malformed period must error, not silently count nothing")
	}
}
