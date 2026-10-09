package app

// channel_cooldown_test.go — supply-side 429 cooldown for single-key channels
// (plan 2.5): the chan_cd:{id}:{keyIdx} store, the cooldown predicate ANDed
// with the provider filter on both selection paths, the dedicated
// all-channels-cooling sentinel, and used-channel exclusion on retry.

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

const (
	cdChanA  = 9601
	cdChanB  = 9602
	cdModel  = "cd-model"
	cdGroup  = "default"
	cdSetEU  = `{"region":"eu"}`
	cdSetUS  = `{"region":"us"}`
	cdEpoch  = int64(1_800_000_000)
	cdMinute = int64(60)
)

// cdClock pins the cooldown clock and wipes the store so a test starts clean
// and never leaks entries into the next one.
func cdClock(t *testing.T) func(d time.Duration) {
	t.Helper()
	now := time.Unix(cdEpoch, 0)
	prev := cooldownNow
	cooldownNow = func() time.Time { return now }
	ClearChannelCooldowns()
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = nil, false
	t.Cleanup(func() {
		cooldownNow = prev
		common.RDB, common.RedisEnabled = prevRDB, prevEnabled
		ClearChannelCooldowns()
	})
	return func(d time.Duration) { now = now.Add(d) }
}

func seedCooldownChannels(t *testing.T, settings ...string) {
	t.Helper()
	db := setupServiceTestDB(t)
	if err := db.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("automigrate abilities: %v", err)
	}
	withMemoryCacheDisabled(t)
	resetAffinityMemForTest()
	ids := []int{cdChanA, cdChanB}
	for i, id := range ids {
		w := uint(1)
		ch := &repo.Channel{
			Id: id, Type: 1, Status: common.ChannelStatusEnabled, Name: "cd-test",
			Models: cdModel, Group: cdGroup, TenantId: "default", Weight: &w,
		}
		if i < len(settings) {
			ch.Setting = common.GetPointer(settings[i])
		}
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %d: %v", id, err)
		}
		if err := db.Create(&repo.Ability{Group: cdGroup, Model: cdModel, ChannelId: id, Enabled: true, Weight: w}).Error; err != nil {
			t.Fatalf("seed ability %d: %v", id, err)
		}
	}
}

func cdSelect(t *testing.T, used ...int) (*repo.Channel, error) {
	t.Helper()
	c := affinitySelectCtx(t, "")
	if len(used) > 0 {
		s := make([]string, 0, len(used))
		for _, id := range used {
			s = append(s, strconv.Itoa(id))
		}
		c.Set("use_channel", s)
	}
	retry := 0
	ch, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: cdGroup, ModelName: cdModel, Retry: &retry})
	return ch, err
}

func TestChannelCooldown_SingleKeySkippedThenRecovers(t *testing.T) {
	advance := cdClock(t)
	seedCooldownChannels(t)

	MarkChannelCooldown(cdChanA, 0, cdEpoch+cdMinute)
	for i := 0; i < 40; i++ {
		ch, err := cdSelect(t)
		if err != nil || ch == nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if ch.Id != cdChanB {
			t.Fatalf("iteration %d: cooling channel #%d was selected", i, ch.Id)
		}
	}

	advance(61 * time.Second)
	seen := map[int]bool{}
	for i := 0; i < 80 && len(seen) < 2; i++ {
		ch, err := cdSelect(t)
		if err != nil || ch == nil {
			t.Fatalf("after expiry: %v", err)
		}
		seen[ch.Id] = true
	}
	if !seen[cdChanA] {
		t.Fatalf("channel A never came back after its cooldown expired; saw %v", seen)
	}
}

func TestChannelCooldown_MemoryCachePathSkipsCooling(t *testing.T) {
	cdClock(t)
	seedCooldownChannels(t)
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prev })
	repo.InitChannelCache()

	MarkChannelCooldown(cdChanB, 0, cdEpoch+cdMinute)
	for i := 0; i < 40; i++ {
		ch, err := cdSelect(t)
		if err != nil || ch == nil || ch.Id != cdChanA {
			t.Fatalf("iteration %d: got %v err=%v, want channel A", i, ch, err)
		}
	}
}

func TestChannelCooldown_AllCoolingIsDedicatedSentinel(t *testing.T) {
	cdClock(t)
	seedCooldownChannels(t)

	MarkChannelCooldown(cdChanA, 0, cdEpoch+120)
	MarkChannelCooldown(cdChanB, 0, cdEpoch+45)
	ch, err := cdSelect(t)
	if ch != nil {
		t.Fatalf("selected #%d while everything cools", ch.Id)
	}
	if !errors.Is(err, ErrAllChannelsCooling) {
		t.Fatalf("err = %v, want ErrAllChannelsCooling", err)
	}
	if errors.Is(err, repo.ErrNoChannelSatisfiesPredicate) || strings.Contains(err.Error(), "provider filter") {
		t.Fatalf("cooling miss reuses the provider-filter sentinel/text: %v", err)
	}
	if got := CoolingRetryAfterUnix(err); got != cdEpoch+45 {
		t.Fatalf("RetryAfterUnix = %d, want the earliest recovery %d", got, cdEpoch+45)
	}
}

func TestChannelCooldown_ANDsWithProviderFilter(t *testing.T) {
	cdClock(t)
	seedCooldownChannels(t, cdSetEU, cdSetUS)

	run := func(region string) (*repo.Channel, error) {
		c := affinitySelectCtx(t, "")
		common.SetContextKey(c, constant.ContextKeyProviderFilter, dto.ProviderFilter{Region: region})
		retry := 0
		ch, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: cdGroup, ModelName: cdModel, Retry: &retry})
		return ch, err
	}

	// The only EU channel cools: the US one passes the cooldown but fails the
	// filter, so the answer is "cooling", not a filter miss and not channel B.
	MarkChannelCooldown(cdChanA, 0, cdEpoch+cdMinute)
	ch, err := run("eu")
	if ch != nil || !errors.Is(err, ErrAllChannelsCooling) {
		t.Fatalf("eu while cooling: ch=%v err=%v, want ErrAllChannelsCooling", ch, err)
	}
	// A region nobody serves stays a filter miss even though a channel cools.
	ch, err = run("cn")
	if ch != nil || err == nil || errors.Is(err, ErrAllChannelsCooling) || !strings.Contains(err.Error(), "provider filter") {
		t.Fatalf("cn: ch=%v err=%v, want the provider-filter miss", ch, err)
	}
	// The US channel is unaffected by A's cooldown.
	ch, err = run("us")
	if err != nil || ch == nil || ch.Id != cdChanB {
		t.Fatalf("us: ch=%v err=%v, want channel B", ch, err)
	}
}

func TestChannelCooldown_RetryExcludesUsedChannels(t *testing.T) {
	cdClock(t)
	seedCooldownChannels(t)

	for i := 0; i < 40; i++ {
		ch, err := cdSelect(t, cdChanA)
		if err != nil || ch == nil || ch.Id != cdChanB {
			t.Fatalf("iteration %d: got %v err=%v, want B (A already failed)", i, ch, err)
		}
	}
	// Nothing unused left: fall back to the full set instead of failing the
	// request, which is what a single-channel model did before exclusion.
	ch, err := cdSelect(t, cdChanA, cdChanB)
	if err != nil || ch == nil {
		t.Fatalf("all used: %v", err)
	}
}

func TestChannelCooldown_RedisSharedAcrossReplicas(t *testing.T) {
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

	MarkChannelCooldown(cdChanA, 0, cdEpoch+90)
	if !mr.Exists("chan_cd:9601:0") {
		t.Fatalf("redis key chan_cd:9601:0 missing; keys=%v", mr.Keys())
	}
	if ttl := mr.TTL("chan_cd:9601:0"); ttl < 85*time.Second || ttl > 90*time.Second {
		t.Fatalf("redis TTL = %v, want ~90s (the cooldown deadline)", ttl)
	}

	// Another replica: no process-local entry, only Redis.
	resetLocalChannelCooldowns()
	advance(3 * time.Second) // past the snapshot refresh interval
	for i := 0; i < 30; i++ {
		ch, err := cdSelect(t)
		if err != nil || ch == nil || ch.Id != cdChanB {
			t.Fatalf("iteration %d: got %v err=%v, want B", i, ch, err)
		}
	}

	// Redis expires the key; the next snapshot lets A back in.
	mr.FastForward(2 * time.Minute)
	// The deadline index is scored in wall-clock seconds, so the test clock moves too.
	advance(2*time.Minute + 3*time.Second)
	seen := map[int]bool{}
	for i := 0; i < 80 && !seen[cdChanA]; i++ {
		ch, err := cdSelect(t)
		if err != nil || ch == nil {
			t.Fatal(err)
		}
		seen[ch.Id] = true
	}
	if !seen[cdChanA] {
		t.Fatal("channel A still excluded after the redis key expired")
	}
}

func TestChannelCooldown_NoActiveCooldownKeepsNilPredicate(t *testing.T) {
	cdClock(t)
	// Hot-path guard: with nothing cooling no predicate is built, so selection
	// keeps the unfiltered fast path.
	probe := &cooldownProbe{}
	if p := cooldownPredicate(probe); p != nil {
		t.Fatal("cooldownPredicate must be nil when no cooldown is active")
	}
	MarkChannelCooldown(cdChanA, 0, cdEpoch+cdMinute)
	if p := cooldownPredicate(probe); p == nil {
		t.Fatal("cooldownPredicate must exist once a cooldown is active")
	}
}

// The deadline index must outlive the longest cooldown, even when a later,
// shorter cooldown is the last write.
func TestChannelCooldown_RedisIndexOutlivesLongCooldown(t *testing.T) {
	cdClock(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	common.RDB, common.RedisEnabled = client, true

	MarkChannelCooldown(cdChanA, 0, cdEpoch+24*3600)
	MarkChannelCooldown(cdChanB, 0, cdEpoch+30) // the last write is a short one
	if ttl := mr.TTL(channelCooldownIndexKey); ttl < 24*time.Hour {
		t.Fatalf("index TTL = %v, want at least the 24h cooldown cap", ttl)
	}
}
