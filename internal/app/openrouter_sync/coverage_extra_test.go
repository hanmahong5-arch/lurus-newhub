package openrouter_sync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openrouter"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// AggregateOpenRouterUsage — real GROUP BY against seeded logs.
// Business acceptance: only OpenRouter `:free` rows inside the 24h window are
// counted and grouped so the ranker sees real volume. The query + row-building
// path is exercised here; the final ON-CONFLICT upsert into model_usage_stats
// is a PG-only path (glebarez SQLite rejects the `excluded.count_24h`
// assignment the OnConflict clause emits — see the package ceiling note at the
// bottom of sync_db_test.go), so we assert the upsert error is surfaced.
// ---------------------------------------------------------------------------

func TestAggregateOpenRouterUsage_QuerySucceedsThenUpsertSurfacesError(t *testing.T) {
	cleanup := setupSyncDB(t, &repo.Log{}, &repo.ModelUsageStat{})
	defer cleanup()

	now := time.Now()
	recent := now.Add(-1 * time.Hour).Unix()
	old := now.Add(-48 * time.Hour).Unix()

	logs := []repo.Log{
		// x:free — 2 calls in window
		{ChannelType: constant.ChannelTypeOpenRouter, ModelName: "x:free", CreatedAt: recent},
		{ChannelType: constant.ChannelTypeOpenRouter, ModelName: "x:free", CreatedAt: recent},
		// y:free — 1 call in window
		{ChannelType: constant.ChannelTypeOpenRouter, ModelName: "y:free", CreatedAt: recent},
		// noise: paid model (not :free) — excluded by LIKE '%:free'
		{ChannelType: constant.ChannelTypeOpenRouter, ModelName: "paid-model", CreatedAt: recent},
		// noise: wrong channel type — excluded
		{ChannelType: 1, ModelName: "z:free", CreatedAt: recent},
		// noise: too old — excluded by created_at > cutoff
		{ChannelType: constant.ChannelTypeOpenRouter, ModelName: "x:free", CreatedAt: old},
	}
	if err := repo.DB.Create(&logs).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	// The GROUP BY must find >0 free/in-window OpenRouter rows (else we'd get a
	// nil "no usage rows" return); it then builds ModelUsageStat rows and calls
	// UpsertModelUsageStats, whose OnConflict assignment is PG-only. On the
	// SQLite hermetic tier that upsert fails and the error is wrapped.
	err := AggregateOpenRouterUsage(context.Background())
	if err == nil {
		t.Fatal("expected upsert error on SQLite tier (PG-only ON CONFLICT), got nil — did the query find rows?")
	}
	if !strings.Contains(err.Error(), "aggregator: upsert stats") {
		t.Fatalf("error = %q, want it to wrap 'aggregator: upsert stats' (proves query produced rows then upsert ran)", err.Error())
	}
}

// TestAggregateOpenRouterUsage_OnlyNoiseRowsReturnsNil proves the filter logic
// end-to-end without depending on the PG-only upsert: when every seeded log is
// noise (paid model / wrong channel type / stale), the GROUP BY returns zero
// rows and the function takes the "no usage rows" nil-return path (no upsert).
func TestAggregateOpenRouterUsage_OnlyNoiseRowsReturnsNil(t *testing.T) {
	cleanup := setupSyncDB(t, &repo.Log{}, &repo.ModelUsageStat{})
	defer cleanup()

	now := time.Now()
	recent := now.Add(-1 * time.Hour).Unix()
	old := now.Add(-48 * time.Hour).Unix()

	logs := []repo.Log{
		// paid model — not :free
		{ChannelType: constant.ChannelTypeOpenRouter, ModelName: "paid-model", CreatedAt: recent},
		// :free but wrong channel type
		{ChannelType: 1, ModelName: "z:free", CreatedAt: recent},
		// :free + right channel but stale (>24h)
		{ChannelType: constant.ChannelTypeOpenRouter, ModelName: "old:free", CreatedAt: old},
	}
	if err := repo.DB.Create(&logs).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	if err := AggregateOpenRouterUsage(context.Background()); err != nil {
		t.Fatalf("expected nil (no matching rows -> no upsert), got %v", err)
	}
	stats, err := repo.GetModelUsageStatsByChannelType(constant.ChannelTypeOpenRouter)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("noise-only logs must produce no usage stats, got %d", len(stats))
	}
}

// TestAggregateOpenRouterUsage_QueryErrorMissingLogsTable drives the query-error
// branch: with no `logs` table migrated, the GROUP BY Scan fails and the
// function wraps it as "aggregator: query logs".
func TestAggregateOpenRouterUsage_QueryErrorMissingLogsTable(t *testing.T) {
	cleanup := setupSyncDB(t, &repo.ModelUsageStat{}) // deliberately no Log table
	defer cleanup()

	err := AggregateOpenRouterUsage(context.Background())
	if err == nil {
		t.Fatal("expected query error when logs table is absent, got nil")
	}
	if !strings.Contains(err.Error(), "aggregator: query logs") {
		t.Errorf("error = %q, want it to wrap 'aggregator: query logs'", err.Error())
	}
}

// TestLoadUsageStats_QueryError drives the LoadUsageStats error path: with no
// model_usage_stats table, the underlying Find fails and the error propagates.
func TestLoadUsageStats_QueryError(t *testing.T) {
	cleanup := setupSyncDB(t, &repo.Log{}) // deliberately no ModelUsageStat table
	defer cleanup()

	_, err := LoadUsageStats()
	if err == nil {
		t.Fatal("expected error from LoadUsageStats when stats table is absent, got nil")
	}
}

// ---------------------------------------------------------------------------
// Engine.Run — error branches
// ---------------------------------------------------------------------------

// TestEngine_Run_ListJobsError covers the "list jobs" failure branch: with no
// OpenRouterSyncJob table, ListEnabledOpenRouterSyncJobs errors and Run returns
// a wrapped error before any fetch happens.
func TestEngine_Run_ListJobsError(t *testing.T) {
	cleanup := setupSyncDB(t, &repo.Channel{}) // no OpenRouterSyncJob table
	defer cleanup()

	eng := &Engine{
		HTTPClient: func(_ context.Context) ([]openrouter.Model, error) {
			t.Fatal("fetch must not run when job listing fails")
			return nil, nil
		},
		UsageFn: func() ([]Stat, error) { return nil, nil },
		Now:     time.Now,
	}
	_, err := eng.Run(context.Background(), nil, false)
	if err == nil || !strings.Contains(err.Error(), "list jobs") {
		t.Fatalf("expected 'list jobs' error, got %v", err)
	}
}

// TestEngine_Run_ChannelForUpdateError covers applyChannel's
// GetChannelForUpdate failure: a due job targets a channel id that does not
// exist, so the per-channel transaction fails, the error is persisted on the
// job's LastError, and Run still returns a (non-skipped) result with no adds.
func TestEngine_Run_ChannelForUpdateError(t *testing.T) {
	cleanup := setupSyncDB(t, &repo.OpenRouterSyncJob{}, &repo.Channel{})
	defer cleanup()

	// Job targets channel 999 which is never created.
	job := &repo.OpenRouterSyncJob{
		Name: "orphan", TargetChannelId: 999,
		Categories: `["llm_reasoning"]`, Schedule: "manual", Enabled: true,
	}
	if err := repo.DB.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	eng := &Engine{
		HTTPClient: func(_ context.Context) ([]openrouter.Model, error) {
			return []openrouter.Model{freeModel("a:free", 1)}, nil
		},
		UsageFn:   func() ([]Stat, error) { return nil, nil },
		Now:       time.Now,
		UpdateAbs: func(c *repo.Channel, tx *gorm.DB) error { return nil },
	}

	res, err := eng.Run(context.Background(), []int{job.Id}, true /*force*/)
	if err != nil {
		t.Fatalf("Run should not hard-error on a per-channel failure: %v", err)
	}
	if res.Skipped {
		t.Errorf("run should not be skipped, got %+v", res)
	}
	if len(res.Added) != 0 {
		t.Errorf("no models should be added when channel load fails, got %v", res.Added)
	}
	var reload repo.OpenRouterSyncJob
	if err := repo.DB.First(&reload, job.Id).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if !strings.Contains(reload.LastError, "load channel for update") {
		t.Errorf("job LastError should record channel load failure, got %q", reload.LastError)
	}
}

// TestEngine_Run_UpdateAbilitiesError covers applyChannel's "update abilities"
// failure branch: the injected UpdateAbs seam returns an error, which rolls
// back the channel transaction and is persisted as the job's LastError; the
// channel's models must remain untouched.
func TestEngine_Run_UpdateAbilitiesError(t *testing.T) {
	cleanup := setupSyncDB(t,
		&repo.OpenRouterSyncJob{}, &repo.Channel{}, &repo.ModelUsageStat{},
		&repo.Model{}, &repo.Vendor{}, &repo.Ability{},
	)
	defer cleanup()

	ch := &repo.Channel{Id: 10, Name: "target", Models: "keep-me"}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	job := &repo.OpenRouterSyncJob{
		Name: "j", TargetChannelId: 10, Categories: `["llm_reasoning"]`,
		Schedule: "manual", Enabled: true,
	}
	if err := repo.DB.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	eng := &Engine{
		HTTPClient: func(_ context.Context) ([]openrouter.Model, error) {
			return []openrouter.Model{freeModel("a:free", 1)}, nil
		},
		UsageFn: func() ([]Stat, error) { return nil, nil },
		Now:     time.Now,
		UpdateAbs: func(c *repo.Channel, tx *gorm.DB) error {
			return errors.New("abilities write boom")
		},
	}

	res, err := eng.Run(context.Background(), []int{job.Id}, true /*force*/)
	if err != nil {
		t.Fatalf("Run should not hard-error: %v", err)
	}
	if len(res.Added) != 0 {
		t.Errorf("no adds should be reported when the tx rolls back, got %v", res.Added)
	}

	// Channel models untouched (transaction rolled back).
	var got repo.Channel
	if err := repo.DB.First(&got, 10).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if got.Models != "keep-me" {
		t.Errorf("channel models should be unchanged after rollback, got %q", got.Models)
	}
	var reload repo.OpenRouterSyncJob
	repo.DB.First(&reload, job.Id)
	if !strings.Contains(reload.LastError, "update abilities") {
		t.Errorf("job LastError should record abilities failure, got %q", reload.LastError)
	}
}

// ---------------------------------------------------------------------------
// ensureModelMetadataEntries — direct unit coverage of guard/error branches.
// ---------------------------------------------------------------------------

// TestEnsureModelMetadataEntries_EmptyIsNoop covers the len==0 early return.
func TestEnsureModelMetadataEntries_EmptyIsNoop(t *testing.T) {
	// No DB touched — must return before any repo call.
	ensureModelMetadataEntries(nil)
	ensureModelMetadataEntries([]string{})
}

// TestEnsureModelMetadataEntries_VendorError covers the GetOrCreateVendorByName
// failure branch: with no Vendor table migrated, vendor lookup/insert fails and
// the function logs and returns without creating models.
func TestEnsureModelMetadataEntries_VendorError(t *testing.T) {
	cleanup := setupSyncDB(t) // no tables at all -> vendor ops fail
	defer cleanup()

	// Must not panic; simply returns after the vendor error is logged.
	ensureModelMetadataEntries([]string{"foo:free"})
}

// TestEnsureModelMetadataEntries_DupCheckError covers the IsModelNameDuplicated
// failure branch: the Vendor table exists (so vendor resolves) but the Model
// table does not, so the duplicate check errors and the model is skipped.
func TestEnsureModelMetadataEntries_DupCheckError(t *testing.T) {
	cleanup := setupSyncDB(t, &repo.Vendor{}) // Vendor only, no Model table
	defer cleanup()

	ensureModelMetadataEntries([]string{"foo:free"})

	// Sanity: vendor was created as a side effect of GetOrCreateVendorByName.
	var vcount int64
	repo.DB.Model(&repo.Vendor{}).Where("name = ?", "OpenRouter").Count(&vcount)
	if vcount != 1 {
		t.Errorf("expected OpenRouter vendor row created, got %d", vcount)
	}
}

// ---------------------------------------------------------------------------
// Preview — nil-UsageFn default wiring (LoadUsageStats path).
// ---------------------------------------------------------------------------

// TestEngine_Preview_NilUsageFnWiresLoadUsageStats leaves UsageFn nil so Preview
// must wire the default LoadUsageStats (which reads model_usage_stats from DB).
func TestEngine_Preview_NilUsageFnWiresLoadUsageStats(t *testing.T) {
	cleanup := setupSyncDB(t, &repo.ModelUsageStat{})
	defer cleanup()

	// Seed one usage row so the default LoadUsageStats returns real data.
	if err := repo.DB.Create(&repo.ModelUsageStat{
		ModelName: "a:free", ChannelType: constant.ChannelTypeOpenRouter,
		Count24h: 500, LastUpdatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed stat: %v", err)
	}

	eng := &Engine{
		HTTPClient: func(_ context.Context) ([]openrouter.Model, error) {
			return []openrouter.Model{
				freeModel("a:free", 100),
				freeModel("b:free", 200),
			}, nil
		},
		// UsageFn nil -> Preview wires LoadUsageStats
		// Now nil     -> Preview wires time.Now
	}

	job := &repo.OpenRouterSyncJob{Categories: `["llm_reasoning"]`, TopN: 0}
	result, err := eng.Preview(context.Background(), job)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 free text models, got %d", len(result))
	}
	// a:free has usage 500 (fresh); it must outrank b:free despite older Created.
	if result[0].ID != "a:free" {
		t.Errorf("top model = %q, want a:free (usage-ranked via default LoadUsageStats)", result[0].ID)
	}
	if eng.UsageFn == nil {
		t.Error("Preview should have wired the default UsageFn")
	}
	if eng.Now == nil {
		t.Error("Preview should have wired the default Now clock")
	}
}
