package openrouter_pool

import (
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// These tests exercise the MaybeMarkCooldown happy path — every guard passes and
// the call reaches repo.MarkMultiKeyCooldown, which persists a cooldown row.
// Business contract: a 429 on a specific pool key must park that key in cooldown
// so the dispatcher stops routing to it until the reaper recovers it; the whole
// channel only auto-disables when the LAST key is parked.

func TestMaybeMarkCooldown_MarksCoolingKeyInDB(t *testing.T) {
	setupPoolTestDB(t)

	// 2-key channel, both keys healthy.
	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{})
	keys := keysOf(t, chID)

	ce := types.ChannelError{
		ChannelId:   chID,
		ChannelType: constant.ChannelTypeOpenRouter,
		IsMultiKey:  true,
		UsingKey:    keys[0],
	}
	h := http.Header{}
	h.Set("Retry-After", "120")
	ae := &types.NewAPIError{StatusCode: 429, UpstreamHeader: h}

	MaybeMarkCooldown(ce, ae)

	ch := reloadChannel(t, chID)
	until, ok := ch.ChannelInfo.MultiKeyCooldownUntil[0]
	if !ok {
		t.Fatalf("key 0 should have a cooldown entry after 429, map=%v", ch.ChannelInfo.MultiKeyCooldownUntil)
	}
	// Retry-After 120s → deadline ~now+120s (well within clamp bounds).
	if until <= time.Now().Unix() {
		t.Errorf("cooldown deadline must be in the future, got %d", until)
	}
	if st := ch.ChannelInfo.MultiKeyStatusList[0]; st != common.ChannelStatusAutoDisabled {
		t.Errorf("key 0 should be auto-disabled, got status=%d", st)
	}
	// Only one of two keys parked → channel must stay Enabled.
	if ch.Status != common.ChannelStatusEnabled {
		t.Errorf("channel with one healthy key left must stay Enabled, got %d", ch.Status)
	}
	if _, parked := ch.ChannelInfo.MultiKeyCooldownUntil[1]; parked {
		t.Errorf("key 1 must remain healthy")
	}
}

func TestMaybeMarkCooldown_LastKeyParked_AutoDisablesChannel(t *testing.T) {
	setupPoolTestDB(t)

	// 1-key channel: parking its only key must auto-disable the whole channel.
	chID := seedMultiKeyChannel(t, 1, common.ChannelStatusEnabled, map[int]int64{})
	keys := keysOf(t, chID)

	// Seed a matching enabled ability so we can prove the auto-disable sync
	// flips it off (MarkMultiKeyCooldown calls UpdateAbilityStatus on flip).
	prio := int64(0)
	ab := &repo.Ability{Group: "default", Model: "m", ChannelId: chID, Enabled: true, Priority: &prio}
	if err := repo.DB.Create(ab).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}

	ce := types.ChannelError{
		ChannelId:   chID,
		ChannelType: constant.ChannelTypeOpenRouter,
		IsMultiKey:  true,
		UsingKey:    keys[0],
	}
	h := http.Header{}
	h.Set("Retry-After", "300")
	MaybeMarkCooldown(ce, &types.NewAPIError{StatusCode: 429, UpstreamHeader: h})

	ch := reloadChannel(t, chID)
	if ch.Status != common.ChannelStatusAutoDisabled {
		t.Fatalf("channel with all keys parked must be AutoDisabled, got %d", ch.Status)
	}

	var reloaded repo.Ability
	if err := repo.DB.Where("channel_id = ?", chID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload ability: %v", err)
	}
	if reloaded.Enabled {
		t.Errorf("ability must be disabled once channel auto-disables")
	}
}

func TestMaybeMarkCooldown_UnknownKey_NoRowWritten(t *testing.T) {
	setupPoolTestDB(t)

	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{})

	// UsingKey not present in the channel's key pool → applyMultiKeyCooldown
	// finds keyIndex<0 and mutates nothing; the channel round-trips unchanged.
	ce := types.ChannelError{
		ChannelId:   chID,
		ChannelType: constant.ChannelTypeOpenRouter,
		IsMultiKey:  true,
		UsingKey:    "sk-or-v1-not-in-pool",
	}
	h := http.Header{}
	h.Set("Retry-After", "120")
	MaybeMarkCooldown(ce, &types.NewAPIError{StatusCode: 429, UpstreamHeader: h})

	ch := reloadChannel(t, chID)
	if len(ch.ChannelInfo.MultiKeyCooldownUntil) != 0 {
		t.Errorf("no cooldown should be written for an unknown key, got %v", ch.ChannelInfo.MultiKeyCooldownUntil)
	}
	if ch.Status != common.ChannelStatusEnabled {
		t.Errorf("channel must remain Enabled")
	}
}

func TestMaybeMarkCooldown_ChannelNotFound_NoOp(t *testing.T) {
	setupPoolTestDB(t)

	// No channel with this id exists → GetChannelById errors → MarkMultiKeyCooldown
	// returns false, MaybeMarkCooldown returns without panicking.
	ce := types.ChannelError{
		ChannelId:   999999,
		ChannelType: constant.ChannelTypeOpenRouter,
		IsMultiKey:  true,
		UsingKey:    "sk-or-v1-anything",
	}
	h := http.Header{}
	h.Set("Retry-After", "120")
	MaybeMarkCooldown(ce, &types.NewAPIError{StatusCode: 429, UpstreamHeader: h})
}

// First attempt: the ChannelError does not yet know the channel is multi-key.
// The real mode comes from the channel row, so only the used key is parked and
// the channel itself stays routable.
func TestMaybeMarkCooldown_FirstAttemptFlagFalse_CoolsOnlyTheKey(t *testing.T) {
	setupPoolTestDB(t)

	chID := seedMultiKeyChannel(t, 2, common.ChannelStatusEnabled, map[int]int64{})
	setChannelType(t, chID, constant.ChannelTypeOpenAI)
	keys := keysOf(t, chID)

	ce := types.ChannelError{
		ChannelId:   chID,
		ChannelType: constant.ChannelTypeOpenAI,
		IsMultiKey:  false,
		AutoBan:     true,
		UsingKey:    keys[1],
	}
	h := http.Header{}
	h.Set("Retry-After", "120")
	MaybeMarkCooldown(ce, &types.NewAPIError{StatusCode: 429, UpstreamHeader: h})

	ch := reloadChannel(t, chID)
	if _, ok := ch.ChannelInfo.MultiKeyCooldownUntil[1]; !ok {
		t.Fatalf("key 1 should be cooling, map=%v", ch.ChannelInfo.MultiKeyCooldownUntil)
	}
	if _, ok := ch.ChannelInfo.MultiKeyCooldownUntil[0]; ok {
		t.Error("key 0 must stay healthy")
	}
	if ch.Status != common.ChannelStatusEnabled {
		t.Errorf("channel must stay Enabled, got %d", ch.Status)
	}
}

// A non-OpenRouter channel excluded from auto-ban keeps its status even when
// its last key parks.
func TestMaybeMarkCooldown_NonOpenRouterAutoBanOff_KeepsChannelEnabled(t *testing.T) {
	setupPoolTestDB(t)

	chID := seedMultiKeyChannel(t, 1, common.ChannelStatusEnabled, map[int]int64{})
	setChannelType(t, chID, constant.ChannelTypeOpenAI)
	keys := keysOf(t, chID)

	ce := types.ChannelError{ChannelId: chID, ChannelType: constant.ChannelTypeOpenAI, AutoBan: false, UsingKey: keys[0]}
	h := http.Header{}
	h.Set("Retry-After", "120")
	MaybeMarkCooldown(ce, &types.NewAPIError{StatusCode: 429, UpstreamHeader: h})

	ch := reloadChannel(t, chID)
	if _, ok := ch.ChannelInfo.MultiKeyCooldownUntil[0]; !ok {
		t.Fatalf("last key should still be parked, map=%v", ch.ChannelInfo.MultiKeyCooldownUntil)
	}
	if ch.Status != common.ChannelStatusEnabled {
		t.Errorf("auto-ban-off channel must keep Enabled status, got %d", ch.Status)
	}
}
