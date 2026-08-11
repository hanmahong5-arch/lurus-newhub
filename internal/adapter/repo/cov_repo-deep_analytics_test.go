package repo

// cov_repo-deep_analytics_test.go — coverage for analytics.go: the model
// performance dashboard aggregate (GetModelPerformance + its private
// latencyPercentileNearestRank helper) and the admin CSV export cursor
// pagination (CountAdminExportLogs / ExportAdminLogsBatch / adminExportFilter).

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func repoDeepSeedAnalyticsLog(t *testing.T, tenantID, model string, logType int, quota, prompt, completion, latencyMs int, createdAt int64) *entity.Log {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenantID,
		Username:         "u",
		Type:             logType,
		CreatedAt:        createdAt,
		ModelName:        model,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalLatencyMs:   latencyMs,
	}
	if err := LOG_DB.Create(l).Error; err != nil {
		t.Fatalf("seed analytics log: %v", err)
	}
	return l
}

func TestGetModelPerformance_AggregatesConsumeAndErrorRows(t *testing.T) {
	SetupTestDB(t)
	start := common.GetTimestamp() - 3600
	end := common.GetTimestamp() + 3600

	// gpt-4: 2 consume rows (latency 100, 200) + 1 error row.
	repoDeepSeedAnalyticsLog(t, "default", "gpt-4", LogTypeConsume, 100, 10, 20, 100, start+10)
	repoDeepSeedAnalyticsLog(t, "default", "gpt-4", LogTypeConsume, 200, 5, 15, 200, start+20)
	repoDeepSeedAnalyticsLog(t, "default", "gpt-4", LogTypeError, 0, 0, 0, 0, start+30)
	// claude: 1 consume row, higher total requests must NOT outrank gpt-4
	// (gpt-4 has 3 requests vs claude's 1) — verifies ORDER BY requests DESC.
	repoDeepSeedAnalyticsLog(t, "default", "claude-3", LogTypeConsume, 500, 50, 50, 50, start+15)
	// A row of an unrelated type (topup) must be excluded entirely.
	repoDeepSeedAnalyticsLog(t, "default", "gpt-4", LogTypeTopup, 9999, 999, 999, 999, start+5)

	stats, err := GetModelPerformance(start, end, "", "")
	if err != nil {
		t.Fatalf("GetModelPerformance: %v", err)
	}
	if len(stats) < 2 {
		t.Fatalf("want at least 2 model groups, got %+v", stats)
	}
	// requests DESC → gpt-4 (3) must come before claude-3 (1).
	if stats[0].ModelName != "gpt-4" {
		t.Fatalf("want gpt-4 first by request count, got %q", stats[0].ModelName)
	}
	gpt4 := stats[0]
	if gpt4.Requests != 3 {
		t.Fatalf("gpt-4 requests must count consume+error (2+1=3), got %d", gpt4.Requests)
	}
	if gpt4.Errors != 1 {
		t.Fatalf("gpt-4 errors must be 1, got %d", gpt4.Errors)
	}
	if gpt4.ErrorRate != float64(1)/float64(3) {
		t.Fatalf("gpt-4 error rate wrong: got %v", gpt4.ErrorRate)
	}
	if gpt4.PromptTokens != 15 || gpt4.CompletionTokens != 35 || gpt4.TotalTokens != 50 {
		t.Fatalf("gpt-4 token sums wrong: %+v", gpt4)
	}
	if gpt4.Quota != 300 {
		t.Fatalf("gpt-4 quota sum must exclude the topup row (100+200=300), got %d", gpt4.Quota)
	}
	if gpt4.LatencySamples != 2 {
		t.Fatalf("gpt-4 latency samples must count only consume rows with latency>0, got %d", gpt4.LatencySamples)
	}
	if gpt4.AvgLatencyMs != 150 {
		t.Fatalf("gpt-4 avg latency must be (100+200)/2=150, got %v", gpt4.AvgLatencyMs)
	}
}

func TestGetModelPerformance_LatencyPercentilesNearestRank(t *testing.T) {
	SetupTestDB(t)
	start := common.GetTimestamp() - 3600
	end := common.GetTimestamp() + 3600

	// 10 consume rows with latencies 10,20,...,100 (ascending), all latency>0.
	for i := 1; i <= 10; i++ {
		repoDeepSeedAnalyticsLog(t, "default", "perf-model", LogTypeConsume, 1, 1, 1, i*10, start+int64(i))
	}
	// A zero-latency row (pre-governance-column legacy row) must be excluded
	// from the percentile sample set entirely.
	repoDeepSeedAnalyticsLog(t, "default", "perf-model", LogTypeConsume, 1, 1, 1, 0, start+50)

	stats, err := GetModelPerformance(start, end, "", "perf-model")
	if err != nil {
		t.Fatalf("GetModelPerformance: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("want exactly 1 model (filtered), got %+v", stats)
	}
	s := stats[0]
	if s.LatencySamples != 10 {
		t.Fatalf("zero-latency row must be excluded from samples, want 10 got %d", s.LatencySamples)
	}
	// Nearest-rank P50 of 10 ascending values = value at ceil(0.5*10)=5th (1-indexed) = 50.
	if s.P50LatencyMs != 50 {
		t.Fatalf("P50 nearest-rank wrong: want 50, got %d", s.P50LatencyMs)
	}
	// P95 = ceil(0.95*10)=10th value = 100.
	if s.P95LatencyMs != 100 {
		t.Fatalf("P95 nearest-rank wrong: want 100, got %d", s.P95LatencyMs)
	}
}

func TestGetModelPerformance_TenantAndModelFiltersScope(t *testing.T) {
	SetupTestDB(t)
	start := common.GetTimestamp() - 3600
	end := common.GetTimestamp() + 3600

	repoDeepSeedAnalyticsLog(t, "tenant-a", "shared-model", LogTypeConsume, 100, 1, 1, 10, start+1)
	repoDeepSeedAnalyticsLog(t, "tenant-b", "shared-model", LogTypeConsume, 200, 1, 1, 10, start+2)

	statsA, err := GetModelPerformance(start, end, "tenant-a", "")
	if err != nil {
		t.Fatalf("GetModelPerformance tenant-a: %v", err)
	}
	if len(statsA) != 1 || statsA[0].Quota != 100 {
		t.Fatalf("tenant filter must exclude tenant-b's rows, got %+v", statsA)
	}

	// Outside the time window entirely → no rows.
	statsOOR, err := GetModelPerformance(end+1000, end+2000, "", "")
	if err != nil {
		t.Fatalf("GetModelPerformance out-of-range: %v", err)
	}
	if len(statsOOR) != 0 {
		t.Fatalf("out-of-window query must return no groups, got %+v", statsOOR)
	}
}

func TestCountAdminExportLogs_And_ExportAdminLogsBatch_CursorPagination(t *testing.T) {
	SetupTestDB(t)
	start := common.GetTimestamp() - 3600
	end := common.GetTimestamp() + 3600

	// 5 matching consume rows for tenant "default"/model "gpt-4", plus noise
	// rows that the filters must exclude.
	var seeded []*entity.Log
	for i := 0; i < 5; i++ {
		seeded = append(seeded, repoDeepSeedAnalyticsLog(t, "default", "gpt-4", LogTypeConsume, 10, 1, 1, 5, start+int64(i)))
	}
	repoDeepSeedAnalyticsLog(t, "other-tenant", "gpt-4", LogTypeConsume, 10, 1, 1, 5, start+1) // wrong tenant
	repoDeepSeedAnalyticsLog(t, "default", "claude-3", LogTypeConsume, 10, 1, 1, 5, start+1)   // wrong model
	repoDeepSeedAnalyticsLog(t, "default", "gpt-4", LogTypeError, 10, 1, 1, 5, start+1)         // wrong type

	total, err := CountAdminExportLogs("default", LogTypeConsume, "gpt-4", start, end)
	if err != nil {
		t.Fatalf("CountAdminExportLogs: %v", err)
	}
	if total != 5 {
		t.Fatalf("count must match only the 5 filtered rows, got %d", total)
	}

	// Page through with limit=2 using the id cursor; must return all 5 rows,
	// in ascending id order, with no duplicates and no gaps.
	var collected []int
	afterID := 0
	for i := 0; i < 10; i++ { // bounded loop guard against an infinite-pagination bug
		page, err := ExportAdminLogsBatch(afterID, "default", LogTypeConsume, "gpt-4", start, end, 2)
		if err != nil {
			t.Fatalf("ExportAdminLogsBatch: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, row := range page {
			collected = append(collected, row.Id)
			if row.Id <= afterID {
				t.Fatalf("cursor must be strictly increasing: prev afterID=%d got id=%d", afterID, row.Id)
			}
			afterID = row.Id
		}
	}
	if len(collected) != 5 {
		t.Fatalf("pagination must collect exactly the 5 filtered rows, got %d: %v", len(collected), collected)
	}
	wantIDs := map[int]bool{}
	for _, s := range seeded {
		wantIDs[s.Id] = true
	}
	for _, id := range collected {
		if !wantIDs[id] {
			t.Fatalf("pagination leaked a row outside the filter, id=%d", id)
		}
	}
}

// adminExportFilter with all-zero/empty selectors must apply NO filter
// (returns everything) — this is exercised indirectly via Count with the
// most permissive call shape.
func TestAdminExportFilter_EmptySelectorsMatchEverything(t *testing.T) {
	SetupTestDB(t)
	repoDeepSeedAnalyticsLog(t, "tenant-x", "any-model", LogTypeConsume, 1, 1, 1, 1, 1)
	repoDeepSeedAnalyticsLog(t, "tenant-y", "other-model", LogTypeError, 1, 1, 1, 1, 2)

	total, err := CountAdminExportLogs("", 0, "", 0, 0)
	if err != nil {
		t.Fatalf("CountAdminExportLogs unfiltered: %v", err)
	}
	if total != 2 {
		t.Fatalf("unfiltered count must include every row regardless of tenant/type/model/time, got %d", total)
	}
}
