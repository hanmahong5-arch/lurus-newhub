package openrouter_pool

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

var c23ReaperDBSeq atomic.Int64

// c23OpenReaperDB opens a hermetic sqlite DB with the two tables the recovery
// path writes: channels (SaveChannelPoolState) and abilities (UpdateAbilityStatus).
func c23OpenReaperDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:c23reaper%d?mode=memory&cache=shared", c23ReaperDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&repo.Channel{}, &repo.Ability{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := repo.DB
	repo.DB = db
	t.Cleanup(func() {
		repo.DB = prev
		_ = sqlDB.Close()
	})
	return db
}

// c23SeedPoolChannel persists a 3-key AutoDisabled channel with one expired
// cooldown (idx 0), one still cooling (idx 1) and one permanent disable (idx 2),
// plus a disabled ability row so the status flip is observable.
func c23SeedPoolChannel(t *testing.T, db *gorm.DB, id int, now time.Time) *repo.Channel {
	t.Helper()
	ch := c23PoolChannel(3)
	ch.Id = id
	ch.Status = common.ChannelStatusAutoDisabled
	ch.Group = "default"
	ch.Models = "m1"
	c23Cool(ch, 0, now.Add(-time.Minute).Unix(), "rate limit")
	c23Cool(ch, 1, now.Add(10*time.Minute).Unix(), "rate limit")
	c23PermDisable(ch, 2, "401")
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&repo.Ability{Group: "default", Model: "m1", ChannelId: id, Enabled: false}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	return ch
}

func TestC23_ReapChannel_RecoversExpiredKeyPersistsAndReenablesChannel(t *testing.T) {
	db := c23OpenReaperDB(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ch := c23SeedPoolChannel(t, db, 4201, now)

	n, err := reapChannel(ch, now)
	if err != nil || n != 1 {
		t.Fatalf("reapChannel = (%d, %v), want (1, nil)", n, err)
	}
	if ch.Status != common.ChannelStatusEnabled {
		t.Errorf("in-memory status = %d, want enabled", ch.Status)
	}
	if reason, _ := ch.GetOtherInfo()["status_reason"].(string); reason != "Pool keys recovered" {
		t.Errorf("status_reason = %q", reason)
	}

	// The persisted row must reflect the same state: only idx 0 recovered.
	var got repo.Channel
	if err := db.First(&got, "id = ?", 4201).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != common.ChannelStatusEnabled {
		t.Errorf("persisted status = %d, want enabled", got.Status)
	}
	if _, still := got.ChannelInfo.MultiKeyStatusList[0]; still {
		t.Error("expired key 0 was not cleared from the persisted status list")
	}
	if _, still := got.ChannelInfo.MultiKeyStatusList[1]; !still {
		t.Error("still-cooling key 1 must stay disabled")
	}
	if _, still := got.ChannelInfo.MultiKeyStatusList[2]; !still {
		t.Error("permanently disabled key 2 must stay disabled")
	}
	if _, still := got.ChannelInfo.MultiKeyCooldownUntil[1]; !still {
		t.Error("cooldown of key 1 must be preserved")
	}

	var ab repo.Ability
	if err := db.First(&ab, "channel_id = ?", 4201).Error; err != nil {
		t.Fatal(err)
	}
	if !ab.Enabled {
		t.Error("ability row must be re-enabled together with the channel")
	}
}

func TestC23_ReapChannel_EnabledChannelStaysEnabledAndNothingRecoveredIsNoWrite(t *testing.T) {
	db := c23OpenReaperDB(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	// Nothing expired: no write, so a missing DB row cannot make this fail.
	future := c23PoolChannel(2)
	future.Id = 4202
	c23Cool(future, 0, now.Add(time.Hour).Unix(), "rate limit")
	if n, err := reapChannel(future, now); n != 0 || err != nil {
		t.Fatalf("nothing expired: got (%d, %v), want (0, nil)", n, err)
	}
	if len(future.ChannelInfo.MultiKeyStatusList) != 1 {
		t.Error("an unexpired cooldown must not be cleared")
	}

	// An Enabled channel with an expired key recovers the key but keeps its
	// status_reason untouched (no status flip happened).
	ch := c23SeedPoolChannel(t, db, 4203, now)
	ch.Status = common.ChannelStatusEnabled
	if err := db.Model(&repo.Channel{}).Where("id = ?", 4203).Update("status", common.ChannelStatusEnabled).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := reapChannel(ch, now); n != 1 || err != nil {
		t.Fatalf("reapChannel = (%d, %v), want (1, nil)", n, err)
	}
	if _, has := ch.GetOtherInfo()["status_reason"]; has {
		t.Error("a channel that was never auto-disabled must not get a recovery reason")
	}
	var ab repo.Ability
	if err := db.First(&ab, "channel_id = ?", 4203).Error; err != nil {
		t.Fatal(err)
	}
	if ab.Enabled {
		t.Error("abilities must not be touched when the channel status did not change")
	}
}

func TestC23_ReapChannel_SaveFailureStillReportsRecoveredCount(t *testing.T) {
	c23OpenReaperDB(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ch := c23PoolChannel(2)
	ch.Id = 0 // SaveChannelPoolState refuses a zero ID
	c23Cool(ch, 0, now.Add(-time.Minute).Unix(), "rate limit")
	n, err := reapChannel(ch, now)
	if err == nil {
		t.Fatal("expected a save error for channel ID 0")
	}
	if n != 1 {
		t.Errorf("recovered count must survive the save error, got %d", n)
	}
}

func TestC23_ReapChannel_AbilityUpdateFailureIsReported(t *testing.T) {
	db := c23OpenReaperDB(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ch := c23SeedPoolChannel(t, db, 4204, now)
	if err := db.Migrator().DropTable(&repo.Ability{}); err != nil {
		t.Fatal(err)
	}
	n, err := reapChannel(ch, now)
	if err == nil {
		t.Fatal("expected an error when the ability table is unusable")
	}
	if n != 1 {
		t.Errorf("recovered count = %d, want 1", n)
	}
}

func TestC23_ReapOnce_ContinuesPastAFailingChannel(t *testing.T) {
	db := c23OpenReaperDB(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	bad := c23PoolChannel(2)
	bad.Id = 0
	c23Cool(bad, 0, now.Add(-time.Minute).Unix(), "rate limit")
	good := c23SeedPoolChannel(t, db, 4205, now)

	prev := listReaperChannelsFn
	listReaperChannelsFn = func() ([]*repo.Channel, error) { return []*repo.Channel{bad, good}, nil }
	t.Cleanup(func() { listReaperChannelsFn = prev })

	if err := ReapOnce(context.Background(), func() time.Time { return now }); err != nil {
		t.Fatalf("a per-channel failure must not fail the pass: %v", err)
	}
	var got repo.Channel
	if err := db.First(&got, "id = ?", 4205).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != common.ChannelStatusEnabled {
		t.Error("the healthy channel after the failing one was not reaped")
	}
}

func TestC23_ReapOnce_CancelledContextStopsBeforeWork(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ch := c23PoolChannel(2)
	c23Cool(ch, 0, now.Add(-time.Minute).Unix(), "rate limit")

	prev := listReaperChannelsFn
	listReaperChannelsFn = func() ([]*repo.Channel, error) { return []*repo.Channel{ch}, nil }
	t.Cleanup(func() { listReaperChannelsFn = prev })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ReapOnce(ctx, func() time.Time { return now }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(ch.ChannelInfo.MultiKeyCooldownUntil) != 1 {
		t.Error("a cancelled pass must not mutate channels")
	}
}

func c23PoolChannel(size int) *repo.Channel {
	ch := &repo.Channel{Id: 42, Type: 20, Key: "sk-a\nsk-b\nsk-c\nsk-d"}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = size
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{}
	ch.ChannelInfo.MultiKeyDisabledReason = map[int]string{}
	ch.ChannelInfo.MultiKeyDisabledTime = map[int]int64{}
	ch.ChannelInfo.MultiKeyCooldownUntil = map[int]int64{}
	return ch
}

func c23Cool(ch *repo.Channel, idx int, until int64, reason string) {
	ch.ChannelInfo.MultiKeyStatusList[idx] = common.ChannelStatusAutoDisabled
	ch.ChannelInfo.MultiKeyDisabledReason[idx] = reason
	ch.ChannelInfo.MultiKeyDisabledTime[idx] = until - 60
	ch.ChannelInfo.MultiKeyCooldownUntil[idx] = until
}

// c23PermDisable has no cooldown entry, which is the marker for "permanent".
func c23PermDisable(ch *repo.Channel, idx int, reason string) {
	ch.ChannelInfo.MultiKeyStatusList[idx] = common.ChannelStatusAutoDisabled
	ch.ChannelInfo.MultiKeyDisabledReason[idx] = reason
	ch.ChannelInfo.MultiKeyDisabledTime[idx] = 1234567890
}
