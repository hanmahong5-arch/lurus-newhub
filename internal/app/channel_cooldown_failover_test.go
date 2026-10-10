package app

// channel_cooldown_failover_test.go - acceptance fixes for plan 2.5: priority
// tiers must not be skipped once used channels are excluded, a model served only
// by fully-cooling multi-key channels must answer "all cooling" instead of
// "unknown model", and auto groups must look for a fresh channel in later
// groups before reusing a failed one.

import (
	"errors"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
)

const (
	tierA = 9701
	tierB = 9702
	tierC = 9703
)

func seedTieredChannels(t *testing.T) {
	t.Helper()
	db := setupServiceTestDB(t)
	if err := db.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("automigrate abilities: %v", err)
	}
	withMemoryCacheDisabled(t)
	resetAffinityMemForTest()
	for id, prio := range map[int]int64{tierA: 10, tierB: 5, tierC: 1} {
		w, p := uint(1), prio
		ch := &repo.Channel{
			Id: id, Type: 1, Status: common.ChannelStatusEnabled, Name: "tier",
			Models: cdModel, Group: cdGroup, TenantId: "default", Weight: &w, Priority: &p,
		}
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %d: %v", id, err)
		}
		if err := db.Create(&repo.Ability{Group: cdGroup, Model: cdModel, ChannelId: id, Enabled: true, Weight: w, Priority: &p}).Error; err != nil {
			t.Fatalf("seed ability %d: %v", id, err)
		}
	}
}

func selectWithRetry(t *testing.T, retry int, used ...int) (*repo.Channel, error) {
	t.Helper()
	c := affinitySelectCtx(t, "")
	s := make([]string, 0, len(used))
	for _, id := range used {
		s = append(s, strconv.Itoa(id))
	}
	c.Set("use_channel", s)
	ch, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: cdGroup, ModelName: cdModel, Retry: &retry})
	return ch, err
}

func enableMemoryCache(t *testing.T) {
	t.Helper()
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prev })
	repo.InitChannelCache()
}

// After A (top tier) failed, retry=1 must land on B, not skip to C. Both the
// in-memory cache and the DB fallback have to agree.
func TestChannelFailover_RetryAfterTopTierPicksNextTier(t *testing.T) {
	for _, mem := range []bool{false, true} {
		name := map[bool]string{false: "db_fallback", true: "memory_cache"}[mem]
		t.Run(name, func(t *testing.T) {
			cdClock(t)
			seedTieredChannels(t)
			if mem {
				enableMemoryCache(t)
			}
			for i := 0; i < 20; i++ {
				ch, err := selectWithRetry(t, 1, tierA)
				if err != nil || ch == nil || ch.Id != tierB {
					t.Fatalf("iteration %d: got %v err=%v, want tier B after A failed", i, ch, err)
				}
			}
			ch, err := selectWithRetry(t, 2, tierA, tierB)
			if err != nil || ch == nil || ch.Id != tierC {
				t.Fatalf("after A and B failed: got %v err=%v, want C", ch, err)
			}
		})
	}
}

// A fully cooling top tier must fall through to the next tier on the DB path,
// as the memory path always did.
func TestChannelFailover_DBPathCoolingTopTierFallsThrough(t *testing.T) {
	cdClock(t)
	seedTieredChannels(t)
	MarkChannelCooldown(tierA, 0, cdEpoch+cdMinute)
	for i := 0; i < 20; i++ {
		ch, err := selectWithRetry(t, 0)
		if err != nil || ch == nil || ch.Id != tierB {
			t.Fatalf("iteration %d: got %v err=%v, want B while A cools", i, ch, err)
		}
	}
}

func seedMultiKeyAllCooling(t *testing.T, cooldowns map[int]int64, size int) {
	t.Helper()
	db := setupServiceTestDB(t)
	if err := db.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("automigrate abilities: %v", err)
	}
	withMemoryCacheDisabled(t)
	resetAffinityMemForTest()
	w := uint(1)
	ch := &repo.Channel{
		Id: cdChanA, Type: 1, Status: common.ChannelStatusAutoDisabled, Name: "mk",
		Models: cdModel, Group: cdGroup, TenantId: "default", Weight: &w, Key: "k1\nk2",
	}
	ch.ChannelInfo = repo.ChannelInfo{IsMultiKey: true, MultiKeySize: size, MultiKeyCooldownUntil: cooldowns}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	// Disabled channel: its ability row is switched off, as UpdateAbilityStatus does.
	if err := db.Create(&repo.Ability{Group: cdGroup, Model: cdModel, ChannelId: cdChanA, Enabled: false, Weight: w}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
}

func TestChannelCooldown_MultiKeyAllKeysCoolingIsDedicatedSentinel(t *testing.T) {
	for _, mem := range []bool{false, true} {
		name := map[bool]string{false: "db_fallback", true: "memory_cache"}[mem]
		t.Run(name, func(t *testing.T) {
			cdClock(t)
			now := common.GetTimestamp()
			seedMultiKeyAllCooling(t, map[int]int64{0: now + 400, 1: now + 200}, 2)
			if mem {
				enableMemoryCache(t)
			}
			ch, err := selectWithRetry(t, 0)
			if ch != nil || !errors.Is(err, ErrAllChannelsCooling) {
				t.Fatalf("got ch=%v err=%v, want ErrAllChannelsCooling", ch, err)
			}
			if got := CoolingRetryAfterUnix(err); got != now+200 {
				t.Fatalf("RetryAfterUnix = %d, want earliest key recovery %d", got, now+200)
			}
		})
	}
}

// A multi-key channel that is disabled but NOT because every key cools (one key
// has no cooldown) is a plain outage / unknown model: the old (nil, nil).
func TestChannelCooldown_MultiKeyPartialCooldownStaysEmpty(t *testing.T) {
	cdClock(t)
	now := common.GetTimestamp()
	seedMultiKeyAllCooling(t, map[int]int64{0: now + 400}, 2)
	ch, err := selectWithRetry(t, 0)
	if ch != nil || err != nil {
		t.Fatalf("got ch=%v err=%v, want (nil, nil)", ch, err)
	}
}

// Auto groups: when the first group only has the channel that just failed, the
// retry must go to the next group's fresh channel rather than reuse it.
func TestChannelFailover_AutoGroupPrefersFreshChannelInLaterGroup(t *testing.T) {
	cdClock(t)
	db := setupServiceTestDB(t)
	repo.InitCol()
	if err := db.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	withMemoryCacheDisabled(t)
	withAutoGroups(t, `["groupA","groupB"]`)
	prevUsable := setting.UserUsableGroups2JSONString()
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"groupA":"A","groupB":"B"}`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = setting.UpdateUserUsableGroupsByJSONString(prevUsable) })
	seedAbility(t, db, 9711, "groupA", "fo-model")
	seedAbility(t, db, 9712, "groupB", "fo-model")

	c := createTestGinContext()
	common.SetContextKey(c, constant.ContextKeyUserGroup, "")
	c.Set("use_channel", []string{"9711"})
	retry := 0
	ch, group, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "fo-model", Retry: &retry})
	if err != nil || ch == nil || ch.Id != 9712 || group != "groupB" {
		t.Fatalf("got ch=%v group=%q err=%v, want fresh channel 9712 in groupB", ch, group, err)
	}

	// Nothing fresh anywhere: the failed channel is reused, as before.
	c2 := createTestGinContext()
	common.SetContextKey(c2, constant.ContextKeyUserGroup, "")
	c2.Set("use_channel", []string{"9711", "9712"})
	retry2 := 0
	ch, _, err = CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c2, TokenGroup: "auto", ModelName: "fo-model", Retry: &retry2})
	if err != nil || ch == nil {
		t.Fatalf("all used: got ch=%v err=%v, want a soft fallback", ch, err)
	}
}

func TestChannelCooldown_RedisIndexIsZSet(t *testing.T) {
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
	members, err := mr.ZMembers("chan_cd_idx")
	if err != nil || len(members) != 1 || members[0] != "9601:0" {
		t.Fatalf("deadline index = %v err=%v, want [9601:0]", members, err)
	}
}
