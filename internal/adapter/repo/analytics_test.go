package repo

// analytics_test.go — GetRankings, the period-over-period model/vendor
// leaderboard. Hermetic SQLite tier — see sqlite_testutil_test.go.

import (
	"math"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// seedRankingLog inserts one consume-type log row with the fields GetRankings
// aggregates over: model name, channel_type (vendor), tenant, tokens, quota
// and timestamp.
func seedRankingLog(t *testing.T, tenantID, model string, channelType int, prompt, completion, quota int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenantID,
		Type:             LogTypeConsume,
		ModelName:        model,
		ChannelType:      channelType,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
	}
	if err := LOG_DB.Create(l).Error; err != nil {
		t.Fatalf("seed ranking log: %v", err)
	}
}

// TestGetRankings_RankDeltaSign: model A overtakes model B between the
// previous window (B ahead) and the current window (A ahead). rank_delta
// must be +1 for A (moved up) and -1 for B (moved down).
func TestGetRankings_RankDeltaSign(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	currStart, currEnd := now-3600, now
	prevEnd := currStart - 1
	prevStart := prevEnd - 3600

	// Current window: A (3000 tokens across 3 requests) ahead of B (1000
	// tokens, 1 request). The extra 2 low-token A rows exist only to make
	// A's current-window request count (3) differ from its previous-window
	// request count (1), so requests_growth_pct has a non-trivial value to
	// assert on: (3-1)/1*100 = 200.
	seedRankingLog(t, "t1", "model-a", 1, 2000, 1000, 100, currStart+10)
	seedRankingLog(t, "t1", "model-a", 1, 1, 1, 1, currStart+11)
	seedRankingLog(t, "t1", "model-a", 1, 1, 1, 1, currStart+12)
	seedRankingLog(t, "t1", "model-b", 1, 600, 400, 50, currStart+20)
	// Previous window: B (3000 tokens) ahead of A (1000 tokens); A has a
	// single request, so its previous-window request count is 1.
	seedRankingLog(t, "t1", "model-a", 1, 600, 400, 50, prevStart+10)
	seedRankingLog(t, "t1", "model-b", 1, 2000, 1000, 100, prevStart+20)

	rows, _, _, err := GetRankings(currStart, currEnd, "t1", "model", 20)
	if err != nil {
		t.Fatalf("GetRankings: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(rows), rows)
	}
	byName := map[string]RankingRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	a, ok := byName["model-a"]
	if !ok {
		t.Fatalf("model-a missing from rows: %+v", rows)
	}
	b, ok := byName["model-b"]
	if !ok {
		t.Fatalf("model-b missing from rows: %+v", rows)
	}
	if a.Rank != 1 || b.Rank != 2 {
		t.Fatalf("want a=rank1 b=rank2, got a=%d b=%d", a.Rank, b.Rank)
	}
	if a.RankDelta != 1 {
		t.Errorf("model-a rank_delta = %d, want +1", a.RankDelta)
	}
	if b.RankDelta != -1 {
		t.Errorf("model-b rank_delta = %d, want -1", b.RankDelta)
	}
	if a.IsNew || b.IsNew {
		t.Errorf("neither row should be new: a.IsNew=%v b.IsNew=%v", a.IsNew, b.IsNew)
	}
	// model-a: 3 requests this window vs 1 previously -> (3-1)/1*100 = 200.
	if a.RequestsGrowthPct == nil || *a.RequestsGrowthPct != 200 {
		t.Errorf("model-a requests_growth_pct = %v, want 200", a.RequestsGrowthPct)
	}
	// model-b: 1 request in both windows -> 0 growth, not nil.
	if b.RequestsGrowthPct == nil || *b.RequestsGrowthPct != 0 {
		t.Errorf("model-b requests_growth_pct = %v, want 0", b.RequestsGrowthPct)
	}
}

// TestGetRankings_SharesSumTo100: with no previous-window data at all (every
// row is_new), token_share_pct and quota_share_pct across all returned rows
// must each sum to ~100%.
func TestGetRankings_SharesSumTo100(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	seedRankingLog(t, "t1", "alpha", 1, 300, 200, 100, start+10)
	seedRankingLog(t, "t1", "beta", 1, 100, 100, 50, start+20)
	seedRankingLog(t, "t1", "gamma", 1, 50, 50, 25, start+30)

	rows, _, _, err := GetRankings(start, end, "t1", "model", 20)
	if err != nil {
		t.Fatalf("GetRankings: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	var tokenSum, quotaSum float64
	for _, r := range rows {
		tokenSum += r.TokenSharePct
		quotaSum += r.QuotaSharePct
		if !r.IsNew {
			t.Errorf("row %q should be is_new (no previous window seeded)", r.Name)
		}
		if r.RequestsGrowthPct != nil {
			t.Errorf("row %q requests_growth_pct should be nil with no previous baseline, got %v", r.Name, *r.RequestsGrowthPct)
		}
	}
	if math.Abs(tokenSum-100) > 0.01 {
		t.Errorf("token_share_pct sum = %v, want ~100", tokenSum)
	}
	if math.Abs(quotaSum-100) > 0.01 {
		t.Errorf("quota_share_pct sum = %v, want ~100", quotaSum)
	}
}

// TestGetRankings_VendorGroupsByChannelType: by="vendor" groups by
// channel_type (resolved to its display name), not model_name — two models
// on the same channel_type collapse into one vendor row, and a
// channel_type=0 row (recorded before the governance column existed) is
// excluded from the vendor view but still counts as its own model.
func TestGetRankings_VendorGroupsByChannelType(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	// channel_type=1 (openai): two different models, same vendor.
	seedRankingLog(t, "t1", "gpt-4o", 1, 100, 50, 30, start+10)
	seedRankingLog(t, "t1", "gpt-4o-mini", 1, 40, 20, 10, start+20)
	// channel_type=14 (constant.ChannelTypeAnthropic): a distinct vendor.
	seedRankingLog(t, "t1", "claude-3.5", constant.ChannelTypeAnthropic, 60, 30, 20, start+30)
	// channel_type=0 (legacy row, no governance column at record time): its
	// own model row, but excluded from every vendor row.
	seedRankingLog(t, "t1", "legacy-model", 0, 20, 10, 5, start+40)

	byModel, _, _, err := GetRankings(start, end, "t1", "model", 20)
	if err != nil {
		t.Fatalf("GetRankings by=model: %v", err)
	}
	if len(byModel) != 4 {
		t.Fatalf("by=model: want 4 rows (one per model, including the channel_type=0 one), got %d: %+v", len(byModel), byModel)
	}

	byVendor, _, _, err := GetRankings(start, end, "t1", "vendor", 20)
	if err != nil {
		t.Fatalf("GetRankings by=vendor: %v", err)
	}
	if len(byVendor) != 2 {
		t.Fatalf("by=vendor: want 2 rows (channel_type 1 and 14 collapsed, channel_type=0 excluded), got %d: %+v", len(byVendor), byVendor)
	}
	for _, row := range byVendor {
		if row.Name == "legacy-model" {
			t.Fatalf("channel_type=0 row must not surface as a vendor row: %+v", byVendor)
		}
	}
	var channel1Row *RankingRow
	for i := range byVendor {
		if byVendor[i].Requests == 2 {
			channel1Row = &byVendor[i]
		}
	}
	if channel1Row == nil {
		t.Fatalf("no vendor row with requests=2 (the two channel_type=1 models merged): %+v", byVendor)
	}
	if channel1Row.TotalTokens != 210 { // (100+50)+(40+20)
		t.Errorf("merged vendor row total_tokens = %d, want 210", channel1Row.TotalTokens)
	}
}

// TestGetRankings_TenantIsolation: tenant A's rankings must never include
// tenant B's usage, even when they share a model name and channel_type.
// Mutation target: drop the tenant_id WHERE clause in GetModelUsageTotals /
// getVendorUsageTotals -> this test goes red.
func TestGetRankings_TenantIsolation(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	// Same model name, same channel_type, two tenants — the only thing that
	// can prevent cross-tenant leakage is the tenant_id filter itself.
	seedRankingLog(t, "tenant-a", "shared-model", 1, 100, 50, 30, start+10)
	seedRankingLog(t, "tenant-b", "shared-model", 1, 100_000, 50_000, 30_000, start+20)

	rowsModel, _, _, err := GetRankings(start, end, "tenant-a", "model", 20)
	if err != nil {
		t.Fatalf("GetRankings by=model: %v", err)
	}
	if len(rowsModel) != 1 {
		t.Fatalf("want 1 row scoped to tenant-a, got %d: %+v", len(rowsModel), rowsModel)
	}
	if got := rowsModel[0].TotalTokens; got != 150 {
		t.Errorf("tenant-a total_tokens = %d, want 150 (tenant-b's 150000 leaked in)", got)
	}
	if got := rowsModel[0].Quota; got != 30 {
		t.Errorf("tenant-a quota = %d, want 30 (tenant-b's 30000 leaked in)", got)
	}

	rowsVendor, _, _, err := GetRankings(start, end, "tenant-a", "vendor", 20)
	if err != nil {
		t.Fatalf("GetRankings by=vendor: %v", err)
	}
	if len(rowsVendor) != 1 {
		t.Fatalf("want 1 vendor row scoped to tenant-a, got %d: %+v", len(rowsVendor), rowsVendor)
	}
	if got := rowsVendor[0].TotalTokens; got != 150 {
		t.Errorf("tenant-a vendor total_tokens = %d, want 150 (tenant-b's leaked in)", got)
	}
}

// TestGetRankings_TotalsSpanEveryGroupNotJustReturnedRows: the returned
// totalTokens/totalQuota must be the full current-window sum across every
// group, not the sum of only the `limit`-capped returned rows (mutation
// target: summing `out` instead of `current` before capping -> red).
func TestGetRankings_TotalsSpanEveryGroupNotJustReturnedRows(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	// 3 models, but limit=2 so one of them (gamma) never makes the returned
	// rows — the totals must still include it.
	seedRankingLog(t, "t1", "alpha", 1, 300, 200, 100, start+10)
	seedRankingLog(t, "t1", "beta", 1, 100, 100, 50, start+20)
	seedRankingLog(t, "t1", "gamma", 1, 50, 50, 25, start+30)

	rows, totalTokens, totalQuota, err := GetRankings(start, end, "t1", "model", 2)
	if err != nil {
		t.Fatalf("GetRankings: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 returned rows (limit=2), got %d: %+v", len(rows), rows)
	}
	// Full window: (300+200)+(100+100)+(50+50) = 800 tokens, 100+50+25=175 quota.
	if totalTokens != 800 {
		t.Errorf("totalTokens = %d, want 800 (must include gamma, which limit=2 excludes from rows)", totalTokens)
	}
	if totalQuota != 175 {
		t.Errorf("totalQuota = %d, want 175 (must include gamma, which limit=2 excludes from rows)", totalQuota)
	}
}
