package repo

// channel_cache_polling_race_test.go — Channel.ChannelInfo.MultiKeyPollingIndex
// is one plain int inside a shared *Channel. GetNextEnabledKey advances it under
// the per-channel polling lock; InitChannelCache used to read it while holding
// only channelSyncLock. Two mutexes over one word is a data race under the Go
// memory model, and the sync ticker runs against live relay traffic every
// SYNC_FREQUENCY seconds.
//
// The concurrent test below is what makes the race observable at all: every
// pre-existing test drives both sides serially, so the detector never sees the
// two accesses. NOTE the detector itself only reports under `go test -race`,
// which is a CI-only gate here; run without -race this test still proves the
// carry-over behaviour is preserved and that the new lock ordering cannot
// deadlock.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func seedMultiKeyPollingChannel(t *testing.T, id int, keys []string) *Channel {
	t.Helper()
	ch := &Channel{
		Id:       id,
		Type:     1,
		Status:   common.ChannelStatusEnabled,
		Name:     "polling-cc",
		Models:   "gpt-4o",
		Group:    "default",
		TenantId: "default",
		Key:      strings.Join(keys, "\n"),
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: len(keys),
			MultiKeyMode: constant.MultiKeyModePolling,
		},
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed multi-key channel: %v", err)
	}
	if err := DB.Create(&Ability{Group: "default", Model: "gpt-4o", ChannelId: id, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	return ch
}

// TestInitChannelCache_CarriesPollingIndexAcrossSync locks the behaviour the
// snapshot must preserve: a sync must not reset the rotation to zero.
func TestInitChannelCache_CarriesPollingIndexAcrossSync(t *testing.T) {
	setupSQLiteDB(t)
	withMemoryCache(t)

	keys := []string{"k-one", "k-two", "k-three"}
	seedMultiKeyPollingChannel(t, 8701, keys)
	InitChannelCache()

	cached, err := CacheGetChannel(8701)
	if err != nil || cached == nil {
		t.Fatalf("CacheGetChannel(8701) = %+v, %v", cached, err)
	}
	// Advance the rotation twice, so the index is unambiguously non-zero.
	for i := 0; i < 2; i++ {
		if _, _, apiErr := cached.GetNextEnabledKey(); apiErr != nil {
			t.Fatalf("GetNextEnabledKey: %v", apiErr)
		}
	}
	before := cached.ChannelInfo.MultiKeyPollingIndex
	if before == 0 {
		t.Fatalf("precondition: polling index still 0 after two rotations")
	}

	InitChannelCache()

	after, err := CacheGetChannel(8701)
	if err != nil || after == nil {
		t.Fatalf("CacheGetChannel after resync = %+v, %v", after, err)
	}
	if after.ChannelInfo.MultiKeyPollingIndex != before {
		t.Errorf("polling index = %d after resync, want %d carried over",
			after.ChannelInfo.MultiKeyPollingIndex, before)
	}
}

// TestInitChannelCache_ConcurrentWithKeyRotation runs the two sides against each
// other. Under -race it is the only test that can observe the unsynchronized
// access; without it, it still fails if the snapshot introduced a lock-ordering
// deadlock (GetNextEnabledKey takes the polling lock, and its caller then takes
// channelSyncLock.RLock — so the snapshot must never hold channelSyncLock while
// reaching for a polling lock).
func TestInitChannelCache_ConcurrentWithKeyRotation(t *testing.T) {
	setupSQLiteDB(t)
	withMemoryCache(t)

	keys := []string{"k-one", "k-two", "k-three"}
	seedMultiKeyPollingChannel(t, 8702, keys)
	InitChannelCache()

	const iterations = 300
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			cached, err := CacheGetChannel(8702)
			if err != nil || cached == nil {
				continue
			}
			_, _, _ = cached.GetNextEnabledKey()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			InitChannelCache()
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("rotation and cache sync deadlocked against each other")
	}

	final, err := CacheGetChannel(8702)
	if err != nil || final == nil {
		t.Fatalf("CacheGetChannel after the concurrent run = %+v, %v", final, err)
	}
	if idx := final.ChannelInfo.MultiKeyPollingIndex; idx < 0 || idx >= len(keys) {
		t.Errorf("polling index %d is outside [0,%d)", idx, len(keys))
	}
}
