package app

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestClearChannelCooldown_RemovesLocalIndexAndMirror(t *testing.T) {
	cdClock(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	common.RDB, common.RedisEnabled = client, true

	MarkChannelCooldown(cdChanA, 0, cdEpoch+90)
	MarkChannelCooldown(cdChanA, 1, cdEpoch+90)
	MarkChannelCooldown(cdChanB, 0, cdEpoch+90)

	ClearChannelCooldown(cdChanA)

	if coolingUntil(cdChanA, 0) != 0 || coolingUntil(cdChanA, 1) != 0 {
		t.Fatal("local entries of the cleared channel survived")
	}
	if coolingUntil(cdChanB, 0) == 0 {
		t.Fatal("another channel's cooldown was cleared")
	}
	members, _ := mr.ZMembers("chan_cd_idx")
	if len(members) != 1 || members[0] != "9602:0" {
		t.Fatalf("index members = %v, want [9602:0]", members)
	}
	if mr.Exists("chan_cd:9601:0") || mr.Exists("chan_cd:9601:1") {
		t.Fatal("mirror keys were not deleted")
	}
	if !mr.Exists("chan_cd:9602:0") {
		t.Fatal("other channel's mirror key was deleted")
	}
}

func TestClearChannelCooldown_SelectableImmediately(t *testing.T) {
	cdClock(t)
	seedCooldownChannels(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	common.RDB, common.RedisEnabled = client, true

	MarkChannelCooldown(cdChanA, 0, cdEpoch+3600)
	MarkChannelCooldown(cdChanB, 0, cdEpoch+3600)
	if _, err := cdSelect(t); err == nil {
		t.Fatal("expected all-cooling error before clearing")
	}

	ClearChannelCooldown(cdChanA)
	ch, err := cdSelect(t)
	if err != nil || ch == nil || ch.Id != cdChanA {
		t.Fatalf("after clear got %v err=%v, want channel A selectable at once", ch, err)
	}
}

// A manual lift runs on one replica; the others hold their own process-local
// entry for the same slot. Once their snapshot read shows the ZSET no longer
// carries the slot, they must stop honouring the local entry.
func TestClearChannelCooldown_OtherReplicaLocalEntryDropsAfterSnapshot(t *testing.T) {
	advance := cdClock(t)
	seedCooldownChannels(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	common.RDB, common.RedisEnabled = client, true

	MarkChannelCooldown(cdChanA, 0, cdEpoch+3600)
	MarkChannelCooldown(cdChanB, 0, cdEpoch+3600)
	if _, err := cdSelect(t); err == nil {
		t.Fatal("expected all-cooling error before the lift")
	}

	// Another replica lifts channel A through Redis only.
	other := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = other.Close() })
	if err := other.ZRem(context.Background(), "chan_cd_idx", "9601:0").Err(); err != nil {
		t.Fatal(err)
	}

	advance(cooldownSnapshotEvery + time.Second)
	ch, err := cdSelect(t)
	if err != nil || ch == nil || ch.Id != cdChanA {
		t.Fatalf("after remote lift got %v err=%v, want channel A selectable", ch, err)
	}
	if coolingUntil(cdChanB, 0) == 0 {
		t.Fatal("channel B is still indexed and must keep cooling")
	}
}

// A local entry whose Redis write failed is the only record of that cooldown;
// a later successful snapshot that lacks it must not erase it.
func TestCooldownSnapshot_KeepsLocalEntryWhoseRedisWriteFailed(t *testing.T) {
	advance := cdClock(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	common.RDB, common.RedisEnabled = client, true

	mr.SetError("down")
	MarkChannelCooldown(cdChanA, 0, cdEpoch+3600)
	mr.SetError("")

	advance(cooldownSnapshotEvery + time.Second)
	refreshCooldownSnapshot()
	if coolingUntil(cdChanA, 0) == 0 {
		t.Fatal("local-only cooldown was dropped by a snapshot that never saw it")
	}
}

// The ZSET, not this process's memory, is what a fresh replica reads: with the
// local maps wiped and the snapshot re-read, A must be selectable and B must
// still cool. Fails if the clear forgets the ZREM.
func TestClearChannelCooldown_ZsetDecidesForFreshReplica(t *testing.T) {
	advance := cdClock(t)
	seedCooldownChannels(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	common.RDB, common.RedisEnabled = client, true

	MarkChannelCooldown(cdChanA, 0, cdEpoch+3600)
	MarkChannelCooldown(cdChanB, 0, cdEpoch+3600)
	ClearChannelCooldown(cdChanA)

	ClearChannelCooldowns() // a replica that restarted: no local state at all
	advance(cooldownSnapshotEvery + time.Second)
	ch, err := cdSelect(t)
	if err != nil || ch == nil || ch.Id != cdChanA {
		t.Fatalf("fresh replica got %v err=%v, want channel A selectable", ch, err)
	}
	if coolingUntil(cdChanB, 0) == 0 {
		t.Fatal("channel B must still be cooling")
	}
}
