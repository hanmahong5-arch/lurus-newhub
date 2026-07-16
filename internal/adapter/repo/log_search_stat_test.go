package repo

// log_search_stat_test.go — regression tests for log keyword search and the
// group_by=day stat aggregation.
//
// Runs on the hermetic in-memory SQLite tier (setupSQLiteDB). The PostgreSQL
// dialect arms (no string binding against the integer `type` column; epoch
// bigint → TO_CHAR(TO_TIMESTAMP(...))) cannot execute on SQLite, so PG
// behavior is verified in the live PG retest phase; here we assert the shared
// guard logic and the SQLite arm.

import (
	"testing"
	"time"
)

func seedSearchLog(t *testing.T, userID, logType int, content string, createdAt int64) {
	t.Helper()
	l := &Log{
		UserId:    userID,
		TenantId:  "default",
		Type:      logType,
		Content:   content,
		ModelName: "gpt-4o",
		Quota:     100,
		CreatedAt: createdAt,
	}
	if err := LOG_DB.Create(l).Error; err != nil {
		t.Fatalf("seedSearchLog: %v", err)
	}
}

// TestSearchLogs_KeywordNotBoundToIntegerType guards the PG 22P02 regression:
// a non-numeric keyword must never be bound against the integer `type` column.
// On SQLite the old code passed silently (implicit coercion), so the assertion
// here is behavioral: non-numeric keywords match content, numeric keywords
// match type OR content prefix, and SearchUserLogs applies the content filter.
func TestSearchLogs_KeywordNotBoundToIntegerType(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	base := time.Now().Unix()
	seedSearchLog(t, 1, LogTypeConsume, "completion tokens exhausted", base)
	seedSearchLog(t, 1, LogTypeTopup, "topup via redemption", base+1)
	seedSearchLog(t, 2, LogTypeConsume, "completion ok", base+2)
	seedSearchLog(t, 2, LogTypeError, "upstream 500", base+3)

	tests := []struct {
		name    string
		keyword string
		userID  int // 0 = SearchAllLogs
		want    int
	}{
		{"all: non-numeric keyword matches content prefix", "completion", 0, 2},
		{"all: non-numeric keyword no match", "zzz-no-such", 0, 0},
		{"all: numeric keyword matches type", "2", 0, 2}, // LogTypeConsume == 2 → both consume rows
		{"user: non-numeric keyword scoped to user", "completion", 1, 1},
		{"user: content filter applies (regression: was type-only)", "topup", 1, 1},
		{"user: keyword not matching user's logs", "upstream", 1, 0},
		{"user: numeric keyword matches type for user", "2", 2, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				logs []*Log
				err  error
			)
			if tc.userID == 0 {
				logs, err = SearchAllLogs(AllTenantsForAdmin(), tc.keyword)
			} else {
				logs, err = SearchUserLogs(tc.userID, tc.keyword)
			}
			if err != nil {
				t.Fatalf("search(%q) error: %v", tc.keyword, err)
			}
			if len(logs) != tc.want {
				t.Errorf("search(%q) returned %d logs, want %d", tc.keyword, len(logs), tc.want)
			}
			if tc.userID != 0 {
				for _, l := range logs {
					if l.UserId != tc.userID {
						t.Errorf("SearchUserLogs leaked log of user %d", l.UserId)
					}
				}
			}
		})
	}
}

// TestGetUserLogStatInternal_GroupByDay guards the PG "function date(bigint)
// does not exist" regression: created_at is a unix-epoch bigint, so day
// grouping needs an epoch→date conversion per dialect. This exercises the
// SQLite arm; the TO_CHAR(TO_TIMESTAMP(...)) PG arm is covered by live retest.
func TestGetUserLogStatInternal_GroupByDay(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	day1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC).Unix()
	day2 := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC).Unix()
	seedSearchLog(t, 7, LogTypeConsume, "a", day1)
	seedSearchLog(t, 7, LogTypeConsume, "b", day1+3600)
	seedSearchLog(t, 7, LogTypeConsume, "c", day2)
	seedSearchLog(t, 8, LogTypeConsume, "other user", day1)

	entries, err := GetUserLogStatInternal(7, "day")
	if err != nil {
		t.Fatalf("GetUserLogStatInternal(day): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d day buckets, want 2: %+v", len(entries), entries)
	}
	got := map[string]int64{}
	for _, e := range entries {
		got[e.Key] = e.Count
	}
	if got["2026-06-01"] != 2 {
		t.Errorf("2026-06-01 count = %d, want 2 (buckets: %v)", got["2026-06-01"], got)
	}
	if got["2026-06-02"] != 1 {
		t.Errorf("2026-06-02 count = %d, want 1 (buckets: %v)", got["2026-06-02"], got)
	}

	// Sanity: the default (model) grouping still works.
	byModel, err := GetUserLogStatInternal(7, "model")
	if err != nil {
		t.Fatalf("GetUserLogStatInternal(model): %v", err)
	}
	if len(byModel) != 1 || byModel[0].Count != 3 {
		t.Errorf("model grouping = %+v, want single gpt-4o bucket with count 3", byModel)
	}
}
