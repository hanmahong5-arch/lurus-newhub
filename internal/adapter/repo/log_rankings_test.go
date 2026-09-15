package repo

// log_rankings_test.go — by=group leaderboard dimension for GetRankings
// (cycle-9 plan L7). The trap this file exists to catch: `group` is a SQL
// reserved word, so an unquoted `GROUP BY group` is a syntax error on
// Postgres but silently accepted by SQLite's more permissive parser — the
// hermetic tier every other test in this package runs on cannot see that
// failure at all, which is exactly the green-locally/red-in-production
// shape the plan calls out.
// TestRankings_ByGroup_QuotesTheReservedIdentifier runs against the real
// Postgres dialector in DryRun mode (newDryRunPG/renderSQL, defined in
// lock_forupdate_test.go and reused here) specifically to catch it, and is
// written before the query it guards.

import (
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

// TestRankings_ByGroup_QuotesTheReservedIdentifier is the trap oracle: it
// swaps LOG_DB for a real-Postgres-dialector DryRun handle, calls the real
// by=group query path, captures the actual SQL built, and asserts the
// `group` identifier is quoted. Against an unquoted `GROUP BY group` this
// is red; SQLite-only tests in this file cannot substitute for it.
func TestRankings_ByGroup_QuotesTheReservedIdentifier(t *testing.T) {
	db := newDryRunPG(t)
	var captured string
	if err := db.Callback().Query().After("gorm:query").
		Register("test:capture_rankings_group_sql", func(tx *gorm.DB) {
			captured = tx.Statement.SQL.String()
		}); err != nil {
		t.Fatalf("register capture callback: %v", err)
	}

	prevLogDB := LOG_DB
	LOG_DB = db
	defer func() { LOG_DB = prevLogDB }()

	if _, _, _, err := GetRankings(0, 3600, "", "group", 20); err != nil {
		t.Fatalf("GetRankings by=group (dry-run): %v", err)
	}
	if !strings.Contains(captured, `"group"`) {
		t.Fatalf("by=group query must quote the reserved identifier, got SQL: %s", captured)
	}
}

// TestGetRankings_ByGroup_GroupsByColumn: by="group" ranks by the logs
// table's `group` column, not model_name or channel_type.
func TestGetRankings_ByGroup_GroupsByColumn(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	seedRankingGroupLog(t, "t1", "premium", "model-a", 1000, 500, 100, start+10)
	seedRankingGroupLog(t, "t1", "default", "model-b", 200, 100, 20, start+20)

	rows, _, _, err := GetRankings(start, end, "t1", "group", 20)
	if err != nil {
		t.Fatalf("GetRankings by=group: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 group rows, got %d: %+v", len(rows), rows)
	}
	byName := map[string]RankingRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if _, ok := byName["premium"]; !ok {
		t.Fatalf("premium group missing from rows: %+v", rows)
	}
	if _, ok := byName["default"]; !ok {
		t.Fatalf("default group missing from rows: %+v", rows)
	}
	if got := byName["premium"].TotalTokens; got != 1500 {
		t.Errorf("premium total_tokens = %d, want 1500", got)
	}
}

// TestGetRankings_ByGroup_UngroupedBucket: rows with an empty group string
// must fold into one explicit labelled bucket, not vanish (a naive
// `GROUP BY "group"` would emit a row with name=="" and lose it in the UI,
// which renders `r.name` verbatim as the table-row React key).
func TestGetRankings_ByGroup_UngroupedBucket(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	seedRankingGroupLog(t, "t1", "", "model-a", 300, 100, 40, start+10)
	seedRankingGroupLog(t, "t1", "", "model-b", 100, 50, 10, start+20)
	seedRankingGroupLog(t, "t1", "premium", "model-c", 50, 20, 5, start+30)

	rows, _, _, err := GetRankings(start, end, "t1", "group", 20)
	if err != nil {
		t.Fatalf("GetRankings by=group: %v", err)
	}
	byName := map[string]RankingRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if _, ok := byName[""]; ok {
		t.Fatalf("an empty-string group name must not appear as its own row: %+v", rows)
	}
	bucket, ok := byName["(ungrouped)"]
	if !ok {
		t.Fatalf("both empty-group rows must fold into one labelled (ungrouped) bucket: %+v", rows)
	}
	// 300+100 (row1) + 100+50 (row2) = 550, one merged bucket, not two rows.
	if bucket.TotalTokens != 550 {
		t.Errorf("(ungrouped) total_tokens = %d, want 550 (both empty-group rows merged)", bucket.TotalTokens)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows ((ungrouped) + premium), got %d: %+v", len(rows), rows)
	}
}

// TestGetRankings_ByGroup_TenantIsolation: tenant A's by=group rankings
// must never include tenant B's groups or their quota, mirroring
// TestGetRankings_TenantIsolation for model/vendor.
func TestGetRankings_ByGroup_TenantIsolation(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	seedRankingGroupLog(t, "tenant-a", "premium", "model-a", 100, 50, 10, start+10)
	seedRankingGroupLog(t, "tenant-b", "premium", "model-a", 100_000, 50_000, 10_000, start+10)

	rows, _, _, err := GetRankings(start, end, "tenant-a", "group", 20)
	if err != nil {
		t.Fatalf("GetRankings by=group: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row scoped to tenant-a, got %d: %+v", len(rows), rows)
	}
	if got := rows[0].Quota; got != 10 {
		t.Errorf("quota = %d, want 10 (tenant-b's 10000 leaked in)", got)
	}
}

// seedRankingGroupLog inserts one consume-type log row carrying a `group`
// value, for the by=group aggregate tests above.
func seedRankingGroupLog(t *testing.T, tenantID, group, model string, prompt, completion, quota int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenantID,
		Type:             LogTypeConsume,
		ModelName:        model,
		Group:            group,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
	}
	if err := LOG_DB.Create(l).Error; err != nil {
		t.Fatalf("seed ranking group log: %v", err)
	}
}
