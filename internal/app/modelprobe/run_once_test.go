package modelprobe

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var runOnceDBCounter atomic.Int64

// setupRunOnceDB gives the test its own in-memory sqlite DB with the tables
// RunOnce touches, independent of any other package's test setup.
func setupRunOnceDB(t *testing.T) {
	t.Helper()
	n := runOnceDBCounter.Add(1)
	dsn := "file:modelproberunonce" + strconv.FormatInt(n, 10) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.Channel{}, &repo.Ability{}, &entity.ModelHealth{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := repo.DB
	repo.DB = db
	t.Cleanup(func() { repo.DB = prev })
}

// setModelProbeSetting publishes every field of ModelProbeSetting through the
// real config plumbing (config.GlobalConfig.LoadFromDB), the same path the
// option-sync tick uses — no test-only seam needed in operation_setting.
func setModelProbeSetting(t *testing.T, enabled bool, groups string, intervalHours float64, disableThreshold, maxProbesPerRun int, sleepSeconds float64) {
	t.Helper()
	err := config.GlobalConfig.LoadFromDB(map[string]string{
		"model_probe_setting.enabled":            strconv.FormatBool(enabled),
		"model_probe_setting.groups":             groups,
		"model_probe_setting.interval_hours":     strconv.FormatFloat(intervalHours, 'f', -1, 64),
		"model_probe_setting.disable_threshold":  strconv.Itoa(disableThreshold),
		"model_probe_setting.max_probes_per_run": strconv.Itoa(maxProbesPerRun),
		"model_probe_setting.sleep_seconds":      strconv.FormatFloat(sleepSeconds, 'f', -1, 64),
	})
	if err != nil {
		t.Fatalf("publish model_probe_setting: %v", err)
	}
}

func seedProbeChannel(t *testing.T, group, models string) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{Status: common.ChannelStatusEnabled, Group: group, Models: models, TenantId: "default"}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := ch.AddAbilities(nil); err != nil {
		t.Fatalf("seed abilities: %v", err)
	}
	return ch
}

func TestRunOnce_SkipsEntirelyWhenDisabled(t *testing.T) {
	setupRunOnceDB(t)
	setModelProbeSetting(t, false, "free", 24, 3, 40, 0)
	seedProbeChannel(t, "free", "m1")

	called := false
	probe := func(ctx context.Context, ch *repo.Channel, model string) Result {
		called = true
		return Result{OK: true}
	}

	summary, err := RunOnce(context.Background(), probe, time.Now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if called {
		t.Error("probe must never be called when the setting is disabled")
	}
	if summary != (RunSummary{}) {
		t.Errorf("summary = %+v, want zero value", summary)
	}
}

func TestRunOnce_FailureWritesRowAndDisablesAbility(t *testing.T) {
	setupRunOnceDB(t)
	setModelProbeSetting(t, true, "free", 24, 1 /* disable on first failure */, 40, 0)
	ch := seedProbeChannel(t, "free", "flaky")

	probe := func(ctx context.Context, c *repo.Channel, model string) Result {
		return Result{OK: false, StatusCode: 500, Err: "upstream 500", LatencyMs: 33}
	}

	summary, err := RunOnce(context.Background(), probe, func() time.Time { return time.Unix(1_700_000_000, 0) })
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary.Probed != 1 || summary.Failed != 1 || summary.Disabled != 1 || summary.OK != 0 || summary.Recovered != 0 {
		t.Fatalf("summary = %+v", summary)
	}

	row, err := repo.GetModelHealth(ch.Id, "flaky")
	if err != nil || row == nil {
		t.Fatalf("GetModelHealth: %+v %v", row, err)
	}
	if row.Ok || !row.AutoDisabled || row.ConsecutiveFailures != 1 || row.LatencyMs != 33 {
		t.Fatalf("row = %+v", row)
	}

	var ability repo.Ability
	if err := repo.DB.Where("channel_id = ? AND model = ?", ch.Id, "flaky").First(&ability).Error; err != nil {
		t.Fatal(err)
	}
	if ability.Enabled {
		t.Error("ability row must be disabled after the auto-disable transition")
	}
}

func TestRunOnce_RecoverySuccessEnablesAbility(t *testing.T) {
	setupRunOnceDB(t)
	setModelProbeSetting(t, true, "free", 24, 3, 40, 0)
	ch := seedProbeChannel(t, "free", "flaky")
	if err := repo.SaveModelHealth(&entity.ModelHealth{
		ChannelId: ch.Id, Model: "flaky", AutoDisabled: true, AutoDisabledAt: 1, ConsecutiveFailures: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetChannelModelAbilityEnabled(ch.Id, "flaky", false); err != nil {
		t.Fatal(err)
	}

	probe := func(ctx context.Context, c *repo.Channel, model string) Result {
		return Result{OK: true, LatencyMs: 5}
	}

	summary, err := RunOnce(context.Background(), probe, time.Now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary.Probed != 1 || summary.OK != 1 || summary.Recovered != 1 || summary.Disabled != 0 {
		t.Fatalf("summary = %+v", summary)
	}

	row, err := repo.GetModelHealth(ch.Id, "flaky")
	if err != nil || row == nil || row.AutoDisabled {
		t.Fatalf("row = %+v %v, want AutoDisabled cleared", row, err)
	}

	var ability repo.Ability
	if err := repo.DB.Where("channel_id = ? AND model = ?", ch.Id, "flaky").First(&ability).Error; err != nil {
		t.Fatal(err)
	}
	if !ability.Enabled {
		t.Error("ability row must be re-enabled after recovery")
	}
}

func TestRunOnce_ContextCancellationStopsTheRun(t *testing.T) {
	setupRunOnceDB(t)
	setModelProbeSetting(t, true, "free", 24, 3, 40, 0)
	ch := seedProbeChannel(t, "free", "a,b,c")

	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	probe := func(ctx context.Context, c *repo.Channel, model string) Result {
		calls++
		if calls == 1 {
			cancel()
		}
		return Result{OK: true}
	}

	summary, err := RunOnce(ctx, probe, time.Now)
	if err == nil {
		t.Error("RunOnce must report the cancellation")
	}
	if summary.Probed != 1 {
		t.Fatalf("Probed = %d, want exactly 1 (must stop after cancellation, before the 2nd/3rd pair)", summary.Probed)
	}
	_ = ch
}
