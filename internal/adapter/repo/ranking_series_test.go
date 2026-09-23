package repo

import (
	"testing"
	"time"
)

// GetRankingSeries (cycle 16, P8): hourly usage for a leaderboard's named
// rows — hour buckets, per name, tenant-scoped, and only the names asked
// for.
func TestGetRankingSeries_HourlyPerNameForNamedRowsOnly(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	hour := (time.Now().Unix() / 3600) * 3600
	start, end := hour-3*3600, hour+3599
	// model-a: two rows in one hour, one row two hours earlier.
	seedRankingGroupLog(t, "t1", "default", "model-a", 100, 50, 7, hour+10)
	seedRankingGroupLog(t, "t1", "default", "model-a", 10, 5, 1, hour+20)
	seedRankingGroupLog(t, "t1", "default", "model-a", 1, 1, 1, hour-2*3600+5)
	// model-b: not asked for.
	seedRankingGroupLog(t, "t1", "default", "model-b", 999, 999, 9, hour+30)
	// another tenant's model-a: must not count.
	seedRankingGroupLog(t, "t2", "default", "model-a", 5000, 5000, 50, hour+40)

	pts, err := GetRankingSeries(start, end, "t1", "model", []string{"model-a"})
	if err != nil {
		t.Fatalf("GetRankingSeries: %v", err)
	}
	got := map[int64]RankingSeriesPoint{}
	for _, p := range pts {
		if p.Name != "model-a" {
			t.Fatalf("series carries %q, which was not asked for: %+v", p.Name, pts)
		}
		got[p.T] = p
	}
	if len(got) != 2 {
		t.Fatalf("want 2 hour buckets, got %d: %+v", len(got), pts)
	}
	if p := got[hour]; p.Tokens != 165 || p.Requests != 2 || p.Quota != 8 {
		t.Errorf("hour bucket = %+v, want tokens 165 requests 2 quota 8 (own tenant only)", p)
	}
	if p := got[hour-2*3600]; p.Tokens != 2 || p.Requests != 1 {
		t.Errorf("earlier bucket = %+v, want tokens 2 requests 1", p)
	}
}

func TestGetRankingSeries_ByGroupAndNoNames(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	seedRankingGroupLog(t, "t1", "premium", "m", 10, 10, 1, now-60)
	seedRankingGroupLog(t, "t1", "", "m", 1, 1, 1, now-60)

	pts, err := GetRankingSeries(now-3600, now, "t1", "group", []string{"premium", "(ungrouped)"})
	if err != nil {
		t.Fatalf("by=group: %v", err)
	}
	names := map[string]bool{}
	for _, p := range pts {
		names[p.Name] = true
	}
	if !names["premium"] || !names["(ungrouped)"] {
		t.Errorf("by=group series names = %v, want premium and (ungrouped)", names)
	}

	empty, err := GetRankingSeries(now-3600, now, "t1", "model", nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("no names → %v, %v; want an empty series and no query error", empty, err)
	}
}
