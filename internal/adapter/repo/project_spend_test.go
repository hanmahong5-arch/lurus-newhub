package repo

// project_spend_test.go — HERMETIC coverage for GetSpendByProject and the
// LogQueryParams.ProjectID filter (migration 029). SQLite tier for the same
// reason as project_test.go: the CI coverage gate runs `go test -short` with
// no TEST_POSTGRES_DSN.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func seedSpendLog(t *testing.T, tenantID string, projectID, quota, prompt, completion int, createdAt int64) {
	t.Helper()
	row := &Log{
		UserId:           1,
		TenantId:         tenantID,
		Type:             LogTypeConsume,
		CreatedAt:        createdAt,
		ModelName:        "gpt-4",
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		ProjectId:        projectID,
	}
	if err := LOG_DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

// TestGetSpendByProject_SumsToTenantTotal is the invariant the whole report
// rests on: every consume row in the window lands in exactly one bucket, and
// the buckets add up to the tenant's total. That is why project_id = 0 is
// emitted as a first-class "unassigned" row instead of being filtered away.
func TestGetSpendByProject_SumsToTenantTotal(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := common.GetTimestamp()
	marketing := mustCreateProject(t, "tenant-a", "Marketing")
	research := mustCreateProject(t, "tenant-a", "Research")

	seedSpendLog(t, "tenant-a", marketing.Id, 100, 10, 20, now)
	seedSpendLog(t, "tenant-a", marketing.Id, 50, 5, 6, now)
	seedSpendLog(t, "tenant-a", research.Id, 30, 3, 4, now)
	seedSpendLog(t, "tenant-a", 0, 7, 1, 1, now) // unassigned

	rows, err := GetSpendByProject(ForTenant("tenant-a"), 0, 0)
	if err != nil {
		t.Fatalf("GetSpendByProject: %v", err)
	}

	byID := map[int]ProjectSpendRow{}
	var sum int64
	for _, r := range rows {
		byID[r.ProjectId] = r
		sum += r.TotalQuota
	}
	if sum != 187 {
		t.Errorf("sum of buckets = %d, want 187", sum)
	}
	if got := byID[marketing.Id]; got.TotalQuota != 150 || got.Count != 2 || got.Name != "Marketing" {
		t.Errorf("Marketing bucket = %+v, want quota 150 / count 2 / name Marketing", got)
	}
	if got := byID[marketing.Id]; got.PromptTokens != 15 || got.CompletionTokens != 26 {
		t.Errorf("Marketing tokens = %d/%d, want 15/26", got.PromptTokens, got.CompletionTokens)
	}
	unassigned, ok := byID[0]
	if !ok {
		t.Fatal("no project_id = 0 bucket — the per-project figures would not sum to the tenant total")
	}
	if !unassigned.Unassigned || unassigned.TotalQuota != 7 {
		t.Errorf("unassigned bucket = %+v, want Unassigned=true quota=7", unassigned)
	}
	// Ordered by spend so the console can render a top-N without re-sorting.
	if len(rows) > 0 && rows[0].ProjectId != marketing.Id {
		t.Errorf("first row = %d, want the biggest spender (%d)", rows[0].ProjectId, marketing.Id)
	}
}

func TestGetSpendByProject_TenantIsolationAndNameWithholding(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := common.GetTimestamp()
	mine := mustCreateProject(t, "tenant-a", "Marketing")
	theirs := mustCreateProject(t, "tenant-b", "Project Zeus")

	seedSpendLog(t, "tenant-a", mine.Id, 100, 0, 0, now)
	// Same numeric id under another tenant AND that tenant's own project —
	// neither may appear in tenant A's report.
	seedSpendLog(t, "tenant-b", mine.Id, 8888, 0, 0, now)
	seedSpendLog(t, "tenant-b", theirs.Id, 9999, 0, 0, now)

	rows, err := GetSpendByProject(ForTenant("tenant-a"), 0, 0)
	if err != nil {
		t.Fatalf("GetSpendByProject: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly tenant-a's single bucket", rows)
	}
	if rows[0].TotalQuota != 100 {
		t.Errorf("tenant-a total = %d, want 100 — another tenant's rows folded in", rows[0].TotalQuota)
	}
	for _, r := range rows {
		if r.Name == "Project Zeus" {
			t.Fatal("LEAK: another tenant's project name appeared in this tenant's report")
		}
	}

	// Zero TenantScope is fail-closed: no tenant decision => no rows.
	var zero TenantScope
	empty, err := GetSpendByProject(zero, 0, 0)
	if err != nil {
		t.Fatalf("GetSpendByProject(zero scope): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("zero TenantScope returned %d rows, want 0 (fail-closed)", len(empty))
	}
}

func TestGetSpendByProject_TimeWindowAndEmptyResult(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")
	seedSpendLog(t, "tenant-a", p.Id, 100, 0, 0, 1000)
	seedSpendLog(t, "tenant-a", p.Id, 200, 0, 0, 5000)

	rows, err := GetSpendByProject(ForTenant("tenant-a"), 2000, 6000)
	if err != nil {
		t.Fatalf("GetSpendByProject: %v", err)
	}
	if len(rows) != 1 || rows[0].TotalQuota != 200 {
		t.Errorf("windowed rows = %+v, want a single 200 bucket", rows)
	}

	// A window with no rows returns an empty slice, never nil — the JSON
	// encoder must emit [] so the console does not have to special-case null.
	none, err := GetSpendByProject(ForTenant("tenant-a"), 90000, 99000)
	if err != nil {
		t.Fatalf("GetSpendByProject(empty window): %v", err)
	}
	if none == nil {
		t.Error("empty result is nil; want an allocated empty slice")
	}
	if len(none) != 0 {
		t.Errorf("empty window returned %d rows", len(none))
	}
}

// TestGetSpendByProject_SoftDeletedProjectKeepsNameAndSpend: retiring a project
// must not drop its history from the report (that would under-report the
// tenant total) nor render it as a bare integer.
func TestGetSpendByProject_SoftDeletedProjectKeepsNameAndSpend(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")
	seedSpendLog(t, "tenant-a", p.Id, 100, 0, 0, common.GetTimestamp())
	if _, err := SoftDeleteProject("tenant-a", p.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}

	rows, err := GetSpendByProject(ForTenant("tenant-a"), 0, 0)
	if err != nil {
		t.Fatalf("GetSpendByProject: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want 1", rows)
	}
	if rows[0].TotalQuota != 100 {
		t.Errorf("retired project spend = %d, want 100", rows[0].TotalQuota)
	}
	if rows[0].Name != "Marketing" {
		t.Errorf("retired project name = %q, want Marketing", rows[0].Name)
	}
}

// TestLogQueryParams_ProjectIDFilter: > 0 filters, 0 means "no filter" (NOT
// "unassigned only" — overloading it that way would make every caller that
// forgets the field silently return only untagged traffic).
func TestLogQueryParams_ProjectIDFilter(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := common.GetTimestamp()
	p := mustCreateProject(t, "tenant-a", "Marketing")
	seedSpendLog(t, "tenant-a", p.Id, 100, 0, 0, now)
	seedSpendLog(t, "tenant-a", 0, 7, 0, 0, now)

	t.Run("user scope filters by project", func(t *testing.T) {
		logs, total, err := GetUserLogsWithParams(ForTenant("tenant-a"),
			&LogQueryParams{UserID: 1, ProjectID: p.Id, Limit: 50})
		if err != nil {
			t.Fatalf("GetUserLogsWithParams: %v", err)
		}
		if total != 1 || len(logs) != 1 || logs[0].ProjectId != p.Id {
			t.Errorf("filtered rows = %d (total %d), want exactly the tagged row", len(logs), total)
		}
	})

	t.Run("zero means no filter", func(t *testing.T) {
		_, total, err := GetUserLogsWithParams(ForTenant("tenant-a"),
			&LogQueryParams{UserID: 1, Limit: 50})
		if err != nil {
			t.Fatalf("GetUserLogsWithParams: %v", err)
		}
		if total != 2 {
			t.Errorf("unfiltered total = %d, want 2 — ProjectID 0 must not be read as \"unassigned only\"", total)
		}
	})

	t.Run("tenant scope filters by project", func(t *testing.T) {
		logs, total, err := GetTenantLogsWithParams(ForTenant("tenant-a"),
			&LogQueryParams{ProjectID: p.Id, Limit: 50})
		if err != nil {
			t.Fatalf("GetTenantLogsWithParams: %v", err)
		}
		if total != 1 || len(logs) != 1 {
			t.Errorf("filtered rows = %d (total %d), want 1", len(logs), total)
		}
	})

	t.Run("tenant scope zero means no filter", func(t *testing.T) {
		_, total, err := GetTenantLogsWithParams(ForTenant("tenant-a"),
			&LogQueryParams{Limit: 50})
		if err != nil {
			t.Fatalf("GetTenantLogsWithParams: %v", err)
		}
		if total != 2 {
			t.Errorf("unfiltered total = %d, want 2", total)
		}
	})
}
