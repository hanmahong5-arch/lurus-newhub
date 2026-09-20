package openrouter_pool

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// These tests reach the persistence-error and driver-loop branches that the
// happy-path reaper_pg_test.go cannot: a broken abilities table (so the
// ability re-enable fails after a channel flip), a broken channels table (so
// SaveWithoutKey fails), the AutoReapWithContext ticker driver itself, and the
// ReapOnce per-channel error-continue path.

// TestReapChannel_UpdateAbilityStatusError surfaces the failure branch where a
// channel legitimately flips back to Enabled but the follow-up ability re-enable
// hits a DB error. Business impact: the operator must see the error rather than
// silently believing the pool recovered; a swallowed error would leave routing
// abilities disabled while the channel claims Enabled.
func TestReapChannel_UpdateAbilityStatusError(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// AutoDisabled channel, one expired key → will flip to Enabled and then try
	// UpdateAbilityStatus.
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusAutoDisabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
		1: now.Add(10 * time.Minute).Unix(),
	})

	// Drop the abilities table so UpdateAbilityStatus errors, but leave channels
	// intact so SaveWithoutKey succeeds and the flip is attempted.
	if err := repo.DB.Migrator().DropTable(&repo.Ability{}); err != nil {
		t.Fatalf("drop abilities table: %v", err)
	}

	err := ReapOnce(context.Background(), fixedNow(now))
	// ReapOnce logs-and-continues per channel, so it returns nil overall even
	// though reapChannel returned an error; the recovery still persisted.
	if err != nil {
		t.Fatalf("ReapOnce should swallow per-channel errors, got %v", err)
	}

	// The channel flip itself must still have persisted (SaveWithoutKey ran
	// before the ability update failed).
	ch := reloadChannel(t, chID)
	if ch.Status != common.ChannelStatusEnabled {
		t.Errorf("channel should have flipped Enabled before ability-update error, got %d", ch.Status)
	}
	if _, still := ch.ChannelInfo.MultiKeyCooldownUntil[0]; still {
		t.Errorf("expired cooldown should have been cleared even though ability-update failed")
	}
}

// TestReapChannel_SaveWithoutKeyError forces the save-channel failure branch:
// an expired cooldown is detected (recovered>0) but persistence fails. This is
// the path that returns a wrapped "save channel" error to ReapOnce, which then
// logs-and-continues.
func TestReapChannel_SaveWithoutKeyError(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
	})

	// Drop the channels table AFTER the reaper has listed it (we list first,
	// then drop, then reap the in-memory copy) so SaveWithoutKey fails.
	channels, err := repo.ListOpenRouterMultiKeyChannelsForReaper()
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	if err := repo.DB.Migrator().DropTable(&repo.Channel{}); err != nil {
		t.Fatalf("drop channels table: %v", err)
	}

	// reapChannel is unexported but callable within the package. It should
	// detect the expired key, attempt SaveWithoutKey, and return a wrapped error.
	recovered, rerr := reapChannel(channels[0], now)
	if rerr == nil {
		t.Fatalf("expected save-channel error, got nil (recovered=%d)", recovered)
	}
	if recovered != 1 {
		t.Errorf("recovered should count the key even when save fails, got %d", recovered)
	}
}

// TestReapOnce_LogsAndContinuesOnChannelError proves ReapOnce keeps sweeping the
// remaining channels after one channel's reap errors. A single bad channel must
// not stall recovery for the rest of the fleet.
func TestReapOnce_LogsAndContinuesOnChannelError(t *testing.T) {
	setupPoolTestDB(t)

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	// Two independent AutoDisabled channels, each with an expired key.
	seedMultiKeyChannel(t, 2, common.ChannelStatusAutoDisabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
		1: now.Add(10 * time.Minute).Unix(),
	})
	seedMultiKeyChannel(t, 2, common.ChannelStatusAutoDisabled, map[int]int64{
		0: now.Add(-2 * time.Minute).Unix(),
		1: now.Add(10 * time.Minute).Unix(),
	})

	// Drop abilities so every per-channel flip errors on UpdateAbilityStatus,
	// forcing the log-and-continue branch for the first channel and proving the
	// loop reaches the second.
	if err := repo.DB.Migrator().DropTable(&repo.Ability{}); err != nil {
		t.Fatalf("drop abilities: %v", err)
	}

	if err := ReapOnce(context.Background(), fixedNow(now)); err != nil {
		t.Fatalf("ReapOnce must not abort on per-channel error, got %v", err)
	}

	// Both channels should still have flipped/persisted their cooldown clearing.
	var chans []*repo.Channel
	chans, err := repo.ListOpenRouterMultiKeyChannelsForReaper()
	if err != nil {
		t.Fatalf("relist: %v", err)
	}
	for _, ch := range chans {
		if _, still := ch.ChannelInfo.MultiKeyCooldownUntil[0]; still {
			t.Errorf("channel %d expired key should have been cleared despite ability error", ch.Id)
		}
	}
}

// TestAutoReapWithContext_InitialRunAsLeader drives the ticker loop entry point.
// As leader it performs the immediate startup sweep (recovering an expired key)
// then exits promptly when the context is cancelled — proving both the initial
// ReapOnce call and the ctx.Done() shutdown branch.
func TestAutoReapWithContext_InitialRunAsLeader(t *testing.T) {
	setupPoolTestDB(t)

	prevLeader := common.IsLeader()
	common.SetLeader(true)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	// Seed an expired-cooldown channel so the initial sweep has work to do.
	now := time.Now()
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		AutoReapWithContext(ctx)
		close(done)
	}()

	// The initial sweep runs synchronously at startup before the ticker loop, so
	// by the time we cancel and the goroutine returns, the key must be recovered.
	// Give the goroutine a moment to execute the startup ReapOnce.
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AutoReapWithContext did not return after context cancel")
	}

	ch := reloadChannel(t, chID)
	if _, still := ch.ChannelInfo.MultiKeyCooldownUntil[0]; still {
		t.Errorf("initial leader sweep should have cleared the expired cooldown")
	}
}

// TestAutoReapWithContext_NonLeaderSkipsInitialRun proves a non-leader node does
// NOT run the startup sweep (HA guard): the expired key stays parked because
// only the leader reaps. This guards against every replica racing to mutate the
// same rows.
func TestAutoReapWithContext_NonLeaderSkipsInitialRun(t *testing.T) {
	setupPoolTestDB(t)

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	now := time.Now()
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{
		0: now.Add(-1 * time.Minute).Unix(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		AutoReapWithContext(ctx)
		close(done)
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AutoReapWithContext did not return after context cancel")
	}

	ch := reloadChannel(t, chID)
	if _, still := ch.ChannelInfo.MultiKeyCooldownUntil[0]; !still {
		t.Errorf("non-leader must NOT reap: expired cooldown should remain parked")
	}
}
