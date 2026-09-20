package openrouter_pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// These tests drive the reaper against a real PostgreSQL database, exercising
// the persistence + ability-sync paths that the in-memory reaper_test.go cannot
// reach. Business contract under test: cooldown reaping controls which pool keys
// serve traffic. An expired cooldown MUST be cleared (capacity returns); a
// not-yet-expired cooldown MUST be left (prevents 429 storms); and a channel
// auto-disabled because all keys were cooling MUST flip back to Enabled — with
// its abilities re-enabled — once a key recovers.

// fixedNow returns a clock function pinned to t.
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestReapOnce_ReactivatesExpiredKeyOnly(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// idx0 expired (should recover); idx1 still cooling (should remain).
	chID := seedMultiKeyChannel(t, 3, common.ChannelStatusEnabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
		1: now.Add(10 * time.Minute).Unix(),
	})

	if err := ReapOnce(context.Background(), fixedNow(now)); err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}

	ch := reloadChannel(t, chID)
	cd := ch.ChannelInfo.MultiKeyCooldownUntil
	if _, still := cd[0]; still {
		t.Errorf("idx0 cooldown should have been cleared (expired), still present: %v", cd)
	}
	if _, still := ch.ChannelInfo.MultiKeyStatusList[0]; still {
		t.Errorf("idx0 should have been re-enabled in status list")
	}
	if _, still := cd[1]; !still {
		t.Errorf("idx1 cooldown should remain (not yet expired)")
	}
	if _, still := ch.ChannelInfo.MultiKeyStatusList[1]; !still {
		t.Errorf("idx1 should remain disabled in status list")
	}
}

func TestReapOnce_ExpiryBoundary_NowEqualsUntil(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// Deadline exactly equal to now: reapChannel uses `nowUnix < until` to skip,
	// so now == until means EXPIRED (should recover). This is the boundary that
	// separates a stuck key from a recovered one.
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{
		0: now.Unix(),
	})

	if err := ReapOnce(context.Background(), fixedNow(now)); err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}

	ch := reloadChannel(t, chID)
	if _, still := ch.ChannelInfo.MultiKeyCooldownUntil[0]; still {
		t.Errorf("cooldown at now==until must be treated as expired and cleared")
	}
}

func TestReapOnce_OneSecondBeforeExpiry_NotRecovered(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// until = now+1s: nowUnix < until is true → NOT expired, must stay cooling.
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{
		0: now.Add(1 * time.Second).Unix(),
	})

	if err := ReapOnce(context.Background(), fixedNow(now)); err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}

	ch := reloadChannel(t, chID)
	if _, still := ch.ChannelInfo.MultiKeyCooldownUntil[0]; !still {
		t.Errorf("cooldown one second before expiry must NOT be cleared (prevents 429 storm)")
	}
}

func TestReapOnce_FlipsAutoDisabledChannelBackToEnabled(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// Both keys cooling and channel AutoDisabled. idx0 expired → recovers → one
	// key enabled again → channel must flip Enabled and abilities re-enabled.
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusAutoDisabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
		1: now.Add(10 * time.Minute).Unix(),
	})

	// Seed a disabled ability row so we can prove the status flip re-enables it.
	prio := int64(0)
	ab := &repo.Ability{
		Group:     "default",
		Model:     "openrouter/some-model",
		ChannelId: chID,
		Enabled:   false,
		Priority:  &prio,
	}
	if err := repo.DB.Create(ab).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}

	if err := ReapOnce(context.Background(), fixedNow(now)); err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}

	ch := reloadChannel(t, chID)
	if ch.Status != common.ChannelStatusEnabled {
		t.Fatalf("channel should have flipped to Enabled, got status=%d", ch.Status)
	}
	info := ch.GetOtherInfo()
	if info["status_reason"] != "Pool keys recovered" {
		t.Errorf("status_reason not set on flip, got %v", info["status_reason"])
	}

	var reloaded repo.Ability
	if err := repo.DB.Where("channel_id = ?", chID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload ability: %v", err)
	}
	if !reloaded.Enabled {
		t.Errorf("ability should have been re-enabled after channel flip")
	}
}

func TestReapOnce_NoFlipWhenChannelAlreadyEnabled(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	chID := seedMultiKeyChannel(t, 3, common.ChannelStatusEnabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
	})

	// Ability starts disabled; because the channel is already Enabled, the
	// status-flip branch (which is the only thing that calls UpdateAbilityStatus)
	// must NOT run, so the ability stays disabled.
	prio := int64(0)
	ab := &repo.Ability{Group: "default", Model: "m", ChannelId: chID, Enabled: false, Priority: &prio}
	if err := repo.DB.Create(ab).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}

	if err := ReapOnce(context.Background(), fixedNow(now)); err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}

	var reloaded repo.Ability
	if err := repo.DB.Where("channel_id = ?", chID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload ability: %v", err)
	}
	if reloaded.Enabled {
		t.Errorf("ability should stay disabled: no channel status flip occurred")
	}
	ch := reloadChannel(t, chID)
	if ch.Status != common.ChannelStatusEnabled {
		t.Errorf("channel should remain Enabled")
	}
}

func TestReapOnce_AutoDisabledStaysDisabledWhenAllKeysStillDown(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// A 2-key channel, both cooling. idx0 expires and recovers, but stillDown
	// after removal is 1 which is < size 2, so it WOULD flip. To test the
	// "still all down" branch we make a 1-key channel where the single key
	// recovers → stillDown 0 < 1 → flips. Instead use a channel where recovery
	// leaves stillDown == size: seed 2 cooling but only clear via a still-future
	// entry so recovered==0 → early return, status untouched.
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusAutoDisabled, map[int]int64{
		0: now.Add(10 * time.Minute).Unix(),
		1: now.Add(20 * time.Minute).Unix(),
	})

	if err := ReapOnce(context.Background(), fixedNow(now)); err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}

	ch := reloadChannel(t, chID)
	if ch.Status != common.ChannelStatusAutoDisabled {
		t.Errorf("channel should stay AutoDisabled when nothing recovered, got %d", ch.Status)
	}
	if len(ch.ChannelInfo.MultiKeyCooldownUntil) != 2 {
		t.Errorf("no cooldown should have been cleared, got %d entries", len(ch.ChannelInfo.MultiKeyCooldownUntil))
	}
}

func TestReapOnce_NoChannels_NoError(t *testing.T) {
	setupPoolTestDB(t)
	if err := ReapOnce(context.Background(), time.Now); err != nil {
		t.Fatalf("ReapOnce with no channels should be a no-op, got %v", err)
	}
}

func TestReapOnce_SkipsChannelWithNoCooldowns(t *testing.T) {
	setupPoolTestDB(t)
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// Multi-key channel, all keys healthy (no cooldown map entries).
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{})

	if err := ReapOnce(context.Background(), fixedNow(now)); err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}
	ch := reloadChannel(t, chID)
	if ch.Status != common.ChannelStatusEnabled {
		t.Errorf("healthy channel must be untouched")
	}
}

func TestReapOnce_CancelledContext_ReturnsErr(t *testing.T) {
	setupPoolTestDB(t)
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the loop reaches the channel

	err := ReapOnce(ctx, fixedNow(now))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestListOpenRouterMultiKeyChannels_FiltersNonMultiKey(t *testing.T) {
	setupPoolTestDB(t)
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

	// One multi-key OpenRouter channel (should be listed).
	mkID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
	})

	// A single-key OpenRouter channel (IsMultiKey=false → filtered out).
	single := &repo.Channel{
		Type:   20,
		Key:    "sk-or-v1-single",
		Status: common.ChannelStatusEnabled,
		Name:   "single-key",
		ChannelInfo: repo.ChannelInfo{
			IsMultiKey: false,
		},
	}
	if err := repo.DB.Create(single).Error; err != nil {
		t.Fatalf("seed single: %v", err)
	}

	// A non-OpenRouter channel (wrong type → excluded by the SQL WHERE).
	other := &repo.Channel{
		Type:   1, // OpenAI
		Key:    "sk-openai",
		Status: common.ChannelStatusEnabled,
		Name:   "openai",
		ChannelInfo: repo.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 1,
		},
	}
	if err := repo.DB.Create(other).Error; err != nil {
		t.Fatalf("seed other: %v", err)
	}

	channels, err := repo.ListOpenRouterMultiKeyChannelsForReaper()
	if err != nil {
		t.Fatalf("ListOpenRouterMultiKeyChannelsForReaper: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("expected exactly 1 multi-key OpenRouter channel, got %d", len(channels))
	}
	if channels[0].Id != mkID {
		t.Errorf("listed wrong channel: got id=%d want %d", channels[0].Id, mkID)
	}
}
