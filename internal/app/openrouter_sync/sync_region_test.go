package openrouter_sync

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openrouter"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

// regionSyncCase is one sync run against a seeded target channel (id 10) with
// per-model probe rows already in model_health.
type regionSyncCase struct {
	topN       int
	currentCSV string   // channel.models before the run
	managedOld []string // channel.managed_models_by_sync before the run
	health     []entity.ModelHealth
	fetched    []openrouter.Model
}

// runRegionSync seeds the case, runs the engine once (forced, so the circuit
// breaker is out of the picture) and returns the reloaded channel plus the
// run summary.
func runRegionSync(t *testing.T, tc regionSyncCase) (*repo.Channel, *RunResult) {
	t.Helper()
	cleanup := setupSyncDB(t,
		&repo.OpenRouterSyncJob{}, &repo.Channel{}, &repo.ModelUsageStat{},
		&repo.Model{}, &repo.Vendor{}, &repo.Ability{}, &entity.ModelHealth{},
	)
	t.Cleanup(cleanup)

	ch := &repo.Channel{Id: 10, Name: "orsync-region", Models: tc.currentCSV}
	if err := ch.SetManagedModelsBySync(tc.managedOld); err != nil {
		t.Fatalf("encode managed: %v", err)
	}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	job := &repo.OpenRouterSyncJob{
		Name: "pool", TargetChannelId: 10, Categories: `["llm_reasoning"]`,
		Schedule: "manual", Enabled: true, TopN: tc.topN,
	}
	if err := repo.DB.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	for i := range tc.health {
		h := tc.health[i]
		if err := repo.DB.Create(&h).Error; err != nil {
			t.Fatalf("seed health: %v", err)
		}
	}

	eng := &Engine{
		HTTPClient: func(_ context.Context) ([]openrouter.Model, error) { return tc.fetched, nil },
		UsageFn:    func() ([]Stat, error) { return nil, nil },
		Now:        time.Now,
		UpdateAbs:  func(*repo.Channel, *gorm.DB) error { return nil },
	}
	res, err := eng.Run(context.Background(), []int{job.Id}, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got repo.Channel
	if err := repo.DB.First(&got, 10).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	return &got, res
}

func sorted(xs []string) []string {
	out := append([]string{}, xs...)
	sort.Strings(out)
	return out
}

func assertSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(sorted(got), sorted(want)) {
		t.Fatalf("%s = %v, want %v", what, sorted(got), sorted(want))
	}
}

func healthRow(model string, ok bool, lastErr string, autoDisabled bool) entity.ModelHealth {
	return entity.ModelHealth{
		ChannelId: 10, Model: model, Ok: ok, LastError: lastErr,
		AutoDisabled: autoDisabled, LastProbeAt: time.Now().Unix(),
	}
}

// A model whose probe came back with the region-block 403 is not offered in
// the pool: the channel lists it (so the prober can re-check it) but it is
// reported as blocked and the working models are still added.
func TestSync_RegionBlockedModelIsNotOffered(t *testing.T) {
	got, res := runRegionSync(t, regionSyncCase{
		topN:       2,
		currentCSV: "model-b:free",
		managedOld: []string{"model-b:free"},
		fetched: []openrouter.Model{
			freeModel("model-a:free", 300), freeModel("model-b:free", 200), freeModel("model-c:free", 100),
		},
		health: []entity.ModelHealth{healthRow("model-b:free", false, bodyRegionBlockJSON, true)},
	})
	assertSet(t, "channel models", got.GetModels(), []string{"model-a:free", "model-b:free", "model-c:free"})
	assertSet(t, "added", res.Added, []string{"model-a:free", "model-c:free"})
	assertSet(t, "region_blocked", res.RegionBlocked, []string{"model-b:free"})
}

// A stale blocked row for a model the channel no longer lists (delisted, then
// relisted; or removed by an admin) must not keep it out: the prober never
// visits unlisted models, so nothing could ever clear the row. The model is
// re-added so it gets re-probed, over several consecutive syncs.
func TestSync_StaleBlockedRowForUnlistedModelDoesNotBanIt(t *testing.T) {
	got, res := runRegionSync(t, regionSyncCase{
		topN:       2,
		currentCSV: "model-a:free",
		managedOld: []string{"model-a:free"},
		fetched: []openrouter.Model{
			freeModel("model-a:free", 300), freeModel("model-x:free", 200), freeModel("model-c:free", 100),
		},
		health: []entity.ModelHealth{healthRow("model-x:free", false, bodyRegionBlockJSON, true)},
	})
	assertSet(t, "managed", got.GetManagedModelsBySync(), []string{"model-a:free", "model-x:free"})
	assertSet(t, "added", res.Added, []string{"model-x:free"})
	assertSet(t, "region_blocked", res.RegionBlocked, nil)
}

// A region-blocked model that is already in the pool stays on the channel
// (the prober only re-checks models the channel lists, so dropping it would
// make the block permanent) but must not hold one of the TopN slots: the next
// healthy model takes it.
func TestSync_RegionBlockedManagedModelKeepsChannelButNotTopNSlot(t *testing.T) {
	got, res := runRegionSync(t, regionSyncCase{
		topN:       2,
		currentCSV: "model-a:free,model-b:free",
		managedOld: []string{"model-a:free", "model-b:free"},
		fetched: []openrouter.Model{
			freeModel("model-a:free", 300), freeModel("model-b:free", 200),
			freeModel("model-c:free", 100), freeModel("model-d:free", 50),
		},
		health: []entity.ModelHealth{healthRow("model-b:free", false, bodyRegionMsgOnly, true)},
	})
	// b quarantined (kept so it can be re-probed), c fills the slot b held, d is out.
	assertSet(t, "channel models", got.GetModels(), []string{"model-a:free", "model-b:free", "model-c:free"})
	assertSet(t, "managed", got.GetManagedModelsBySync(), []string{"model-a:free", "model-b:free", "model-c:free"})
	assertSet(t, "added", res.Added, []string{"model-c:free"})
	assertSet(t, "removed", res.Removed, nil)
	assertSet(t, "region_blocked", res.RegionBlocked, []string{"model-b:free"})
}

// Once a later probe succeeds the model competes for a slot again; stale
// error text on a healthy row must not keep it out.
func TestSync_RecoveredModelRejoinsRanking(t *testing.T) {
	got, res := runRegionSync(t, regionSyncCase{
		topN:       2,
		currentCSV: "model-a:free,model-c:free",
		managedOld: []string{"model-a:free", "model-c:free"},
		fetched: []openrouter.Model{
			freeModel("model-a:free", 300), freeModel("model-b:free", 200), freeModel("model-c:free", 100),
		},
		health: []entity.ModelHealth{healthRow("model-b:free", true, bodyRegionMsgOnly, false)},
	})
	assertSet(t, "managed", got.GetManagedModelsBySync(), []string{"model-a:free", "model-b:free"})
	assertSet(t, "added", res.Added, []string{"model-b:free"})
	assertSet(t, "removed", res.Removed, []string{"model-c:free"})
	assertSet(t, "region_blocked", res.RegionBlocked, nil)
}

// A 429 on a free model is transient: the model keeps its place in the pool
// whether or not the prober had it disabled before.
func TestSync_RateLimitedModelIsKept(t *testing.T) {
	got, res := runRegionSync(t, regionSyncCase{
		topN:       2,
		currentCSV: "model-a:free,model-b:free",
		managedOld: []string{"model-a:free", "model-b:free"},
		fetched: []openrouter.Model{
			freeModel("model-a:free", 300), freeModel("model-b:free", 200), freeModel("model-c:free", 100),
		},
		health: []entity.ModelHealth{
			healthRow("model-a:free", false, bodyRateLimited, false),
			healthRow("model-b:free", false, bodyRateLimited, true),
		},
	})
	assertSet(t, "managed", got.GetManagedModelsBySync(), []string{"model-a:free", "model-b:free"})
	assertSet(t, "removed", res.Removed, nil)
	assertSet(t, "region_blocked", res.RegionBlocked, nil)
}

// A 403 that is not a region block (bad key) is a channel problem, not a
// per-model one: no model may leave the pool because of it.
func TestSync_KeyRejectedProbesDoNotBlockModels(t *testing.T) {
	got, res := runRegionSync(t, regionSyncCase{
		currentCSV: "model-a:free,model-b:free",
		managedOld: []string{"model-a:free", "model-b:free"},
		fetched:    []openrouter.Model{freeModel("model-a:free", 300), freeModel("model-b:free", 200)},
		health: []entity.ModelHealth{
			healthRow("model-a:free", false, bodyForbiddenKey, false),
			healthRow("model-b:free", false, bodyBadKey, false),
		},
	})
	assertSet(t, "managed", got.GetManagedModelsBySync(), []string{"model-a:free", "model-b:free"})
	assertSet(t, "removed", res.Removed, nil)
	assertSet(t, "region_blocked", res.RegionBlocked, nil)
}

// A model absent from the catalogue is removed, blocked or not; a blocked
// model that is still listed is not.
func TestSync_DelistedModelsAreRemoved(t *testing.T) {
	got, res := runRegionSync(t, regionSyncCase{
		currentCSV: "model-a:free,model-x:free,model-y:free",
		managedOld: []string{"model-a:free", "model-x:free", "model-y:free"},
		fetched:    []openrouter.Model{freeModel("model-a:free", 300), freeModel("model-y:free", 100)},
		health: []entity.ModelHealth{
			healthRow("model-x:free", false, bodyRegionBlockJSON, true), // blocked AND delisted
			healthRow("model-y:free", false, bodyRegionBlockJSON, true), // blocked, still listed
		},
	})
	assertSet(t, "channel models", got.GetModels(), []string{"model-a:free", "model-y:free"})
	assertSet(t, "removed", res.Removed, []string{"model-x:free"})
	assertSet(t, "region_blocked", res.RegionBlocked, []string{"model-y:free"})
}

// Admin-added (manual) models are never touched by the region filter.
func TestSync_ManualModelsSurviveRegionFilter(t *testing.T) {
	got, _ := runRegionSync(t, regionSyncCase{
		currentCSV: "manual-model,model-b:free",
		managedOld: []string{"model-b:free"},
		fetched:    []openrouter.Model{freeModel("model-b:free", 200)},
		health: []entity.ModelHealth{
			healthRow("manual-model", false, bodyRegionBlockJSON, true),
			healthRow("model-b:free", false, bodyRegionBlockJSON, true),
		},
	})
	if !strings.Contains(","+strings.Join(got.GetModels(), ",")+",", ",manual-model,") {
		t.Fatalf("manual model dropped: %v", got.GetModels())
	}
}

// Probe rows of another channel say nothing about this one.
func TestSync_RegionBlockIsPerChannel(t *testing.T) {
	other := healthRow("model-b:free", false, bodyRegionBlockJSON, true)
	other.ChannelId = 11
	got, res := runRegionSync(t, regionSyncCase{
		fetched: []openrouter.Model{freeModel("model-b:free", 200)},
		health:  []entity.ModelHealth{other},
	})
	assertSet(t, "channel models", got.GetModels(), []string{"model-b:free"})
	assertSet(t, "region_blocked", res.RegionBlocked, nil)
}

// A blocked model the admin listed by hand (on the channel, not in the managed
// set) is still a current channel model: the sync must not adopt it into the
// managed ranking, and the block is reported.
func TestSync_BlockedManualModelIsNotAdoptedIntoManagedSet(t *testing.T) {
	got, res := runRegionSync(t, regionSyncCase{
		topN:       2,
		currentCSV: "model-b:free",
		fetched:    []openrouter.Model{freeModel("model-a:free", 300), freeModel("model-b:free", 200), freeModel("model-c:free", 100)},
		health:     []entity.ModelHealth{healthRow("model-b:free", false, bodyRegionBlockJSON, true)},
	})
	assertSet(t, "managed", got.GetManagedModelsBySync(), []string{"model-a:free", "model-c:free"})
	assertSet(t, "region_blocked", res.RegionBlocked, []string{"model-b:free"})
}
