package repo

// analytics_test.go — GetRankings (period-over-period model/vendor
// leaderboard) and GetVendorPerformance (channel_type rollup of
// GetModelPerformance). Hermetic SQLite tier — see sqlite_testutil_test.go.

import (
	"math"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// seedRankingLog inserts one consume-type log row with the fields GetRankings
// / GetVendorPerformance aggregate over: model name, channel_type (vendor),
// tenant, tokens, quota and timestamp.
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

	// Current window: A (3000 tokens) ahead of B (1000 tokens).
	seedRankingLog(t, "t1", "model-a", 1, 2000, 1000, 100, currStart+10)
	seedRankingLog(t, "t1", "model-b", 1, 600, 400, 50, currStart+20)
	// Previous window: B (3000 tokens) ahead of A (1000 tokens).
	seedRankingLog(t, "t1", "model-a", 1, 600, 400, 50, prevStart+10)
	seedRankingLog(t, "t1", "model-b", 1, 2000, 1000, 100, prevStart+20)

	rows, err := GetRankings(currStart, currEnd, "t1", "model", 20)
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

	rows, err := GetRankings(start, end, "t1", "model", 20)
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
// on the same channel_type collapse into one vendor row.
func TestGetRankings_VendorGroupsByChannelType(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	// channel_type=1 (openai): two different models, same vendor.
	seedRankingLog(t, "t1", "gpt-4o", 1, 100, 50, 30, start+10)
	seedRankingLog(t, "t1", "gpt-4o-mini", 1, 40, 20, 10, start+20)
	// channel_type=14 (anthropic/claude, per constant.ChannelTypeAnthropic-ish
	// mapping): a distinct vendor.
	seedRankingLog(t, "t1", "claude-3.5", 14, 60, 30, 20, start+30)

	byModel, err := GetRankings(start, end, "t1", "model", 20)
	if err != nil {
		t.Fatalf("GetRankings by=model: %v", err)
	}
	if len(byModel) != 3 {
		t.Fatalf("by=model: want 3 rows (one per model), got %d: %+v", len(byModel), byModel)
	}

	byVendor, err := GetRankings(start, end, "t1", "vendor", 20)
	if err != nil {
		t.Fatalf("GetRankings by=vendor: %v", err)
	}
	if len(byVendor) != 2 {
		t.Fatalf("by=vendor: want 2 rows (channel_type 1 and 14 collapsed), got %d: %+v", len(byVendor), byVendor)
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

	rowsModel, err := GetRankings(start, end, "tenant-a", "model", 20)
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

	rowsVendor, err := GetRankings(start, end, "tenant-a", "vendor", 20)
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

// TestGetVendorPerformance_GroupsByChannelTypeWithLatency: sanity check that
// GetVendorPerformance (the full, latency-carrying vendor aggregate) groups
// by channel_type and resolves a non-empty channel name, mirroring
// GetModelPerformance's shape one level up (channel_type instead of model).
func TestGetVendorPerformance_GroupsByChannelTypeWithLatency(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now().Unix()
	start, end := now-3600, now
	l1 := &entity.Log{
		UserId: 1, TenantId: "t1", Type: LogTypeConsume, ModelName: "gpt-4o",
		ChannelType: 1, Quota: 30, PromptTokens: 100, CompletionTokens: 50,
		TotalLatencyMs: 200, CreatedAt: start + 10,
	}
	l2 := &entity.Log{
		UserId: 1, TenantId: "t1", Type: LogTypeConsume, ModelName: "gpt-4o-mini",
		ChannelType: 1, Quota: 10, PromptTokens: 40, CompletionTokens: 20,
		TotalLatencyMs: 100, CreatedAt: start + 20,
	}
	if err := LOG_DB.Create(l1).Error; err != nil {
		t.Fatalf("seed l1: %v", err)
	}
	if err := LOG_DB.Create(l2).Error; err != nil {
		t.Fatalf("seed l2: %v", err)
	}

	stats, err := GetVendorPerformance(start, end, "t1")
	if err != nil {
		t.Fatalf("GetVendorPerformance: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 vendor group (channel_type=1), got %d: %+v", len(stats), stats)
	}
	v := stats[0]
	if v.ChannelType != 1 {
		t.Errorf("channel_type = %d, want 1", v.ChannelType)
	}
	if v.ChannelName == "" {
		t.Errorf("channel_name must be resolved (non-empty), got %q", v.ChannelName)
	}
	if v.Requests != 2 {
		t.Errorf("requests = %d, want 2", v.Requests)
	}
	if v.TotalTokens != 210 {
		t.Errorf("total_tokens = %d, want 210", v.TotalTokens)
	}
	if v.LatencySamples != 2 {
		t.Errorf("latency_samples = %d, want 2", v.LatencySamples)
	}
	if v.P50LatencyMs == 0 {
		t.Errorf("p50_latency_ms should be nonzero with 2 latency samples")
	}
}
