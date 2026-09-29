package repo

import (
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// model_probe_cache_test.go — the in-memory routing cache rebuild
// (rebuildChannelCache) must skip a (channel, model) pair the prober
// (internal/app/modelprobe) has auto-disabled, and must fail open (route as
// before) when the model_health read itself fails. Hermetic sqlite, kept
// independent of any other test file's DB setup in this package.

var modelProbeCacheDBCounter atomic.Int64

func setupCacheProbeDB(t *testing.T, migrateModelHealth bool) {
	t.Helper()
	n := modelProbeCacheDBCounter.Add(1)
	dsn := "file:cacheprobe" + itoaAbility(n) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Channel{}, &Ability{}); err != nil {
		t.Fatalf("migrate channel/ability: %v", err)
	}
	if migrateModelHealth {
		if err := db.AutoMigrate(&entity.ModelHealth{}); err != nil {
			t.Fatalf("migrate model_health: %v", err)
		}
	}

	prevDB := DB
	prevMemCache := common.MemoryCacheEnabled
	prevGroup2Model := group2model2channels
	prevChannelsIDM := channelsIDM
	DB = db
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		DB = prevDB
		common.MemoryCacheEnabled = prevMemCache
		channelSyncLock.Lock()
		group2model2channels = prevGroup2Model
		channelsIDM = prevChannelsIDM
		channelSyncLock.Unlock()
	})
}

func TestRebuildChannelCache_SkipsAutoDisabledModelPair(t *testing.T) {
	setupCacheProbeDB(t, true)

	ch := &Channel{Status: common.ChannelStatusEnabled, Models: "m1,m2", Group: "free", TenantId: "default"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&entity.ModelHealth{ChannelId: ch.Id, Model: "m1", AutoDisabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	if err := rebuildChannelCache(); err != nil {
		t.Fatalf("rebuildChannelCache: %v", err)
	}

	channelSyncLock.RLock()
	m1Channels := append([]int(nil), group2model2channels["free"]["m1"]...)
	m2Channels := append([]int(nil), group2model2channels["free"]["m2"]...)
	channelSyncLock.RUnlock()

	for _, id := range m1Channels {
		if id == ch.Id {
			t.Errorf("channel %d routes m1 despite it being auto-disabled: %v", ch.Id, m1Channels)
		}
	}
	found := false
	for _, id := range m2Channels {
		if id == ch.Id {
			found = true
		}
	}
	if !found {
		t.Errorf("channel %d must still route m2 (only m1 is auto-disabled): %v", ch.Id, m2Channels)
	}
}

func TestRebuildChannelCache_KeepsOtherChannelsWhenOneHasAnAutoDisabledPair(t *testing.T) {
	setupCacheProbeDB(t, true)

	chA := &Channel{Status: common.ChannelStatusEnabled, Models: "shared", Group: "free", TenantId: "default"}
	chB := &Channel{Status: common.ChannelStatusEnabled, Models: "shared", Group: "free", TenantId: "default"}
	if err := DB.Create(chA).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(chB).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&entity.ModelHealth{ChannelId: chA.Id, Model: "shared", AutoDisabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	if err := rebuildChannelCache(); err != nil {
		t.Fatalf("rebuildChannelCache: %v", err)
	}

	channelSyncLock.RLock()
	ids := append([]int(nil), group2model2channels["free"]["shared"]...)
	channelSyncLock.RUnlock()

	hasA, hasB := false, false
	for _, id := range ids {
		if id == chA.Id {
			hasA = true
		}
		if id == chB.Id {
			hasB = true
		}
	}
	if hasA {
		t.Errorf("channel A must be excluded (auto-disabled for this model): %v", ids)
	}
	if !hasB {
		t.Errorf("channel B must still route the shared model: %v", ids)
	}
}

func TestRebuildChannelCache_FailsOpenWhenModelHealthLookupErrors(t *testing.T) {
	// model_health table deliberately not migrated.
	setupCacheProbeDB(t, false)

	ch := &Channel{Status: common.ChannelStatusEnabled, Models: "m1", Group: "free", TenantId: "default"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}

	if err := rebuildChannelCache(); err != nil {
		t.Fatalf("rebuildChannelCache must fail open on a model_health read error, got: %v", err)
	}

	channelSyncLock.RLock()
	ids := append([]int(nil), group2model2channels["free"]["m1"]...)
	channelSyncLock.RUnlock()

	found := false
	for _, id := range ids {
		if id == ch.Id {
			found = true
		}
	}
	if !found {
		t.Errorf("fail-open: routing must proceed as before when auto-disabled data cannot be read, got %v", ids)
	}
}
