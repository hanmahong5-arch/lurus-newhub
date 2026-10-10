package repo

import (
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func seedPoolChannel(t *testing.T, name string, typ int, multi bool, size int) *Channel {
	t.Helper()
	ch := &Channel{
		Name:     name,
		Type:     typ,
		Key:      "key-a\nkey-b",
		Models:   "pool-model",
		Group:    "default",
		Status:   common.ChannelStatusEnabled,
		TenantId: "default",
	}
	ch.ChannelInfo.IsMultiKey = multi
	ch.ChannelInfo.MultiKeySize = size
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	return ch
}

// The reaper must see multi-key channels of every type (the 429 cooldown is no
// longer OpenRouter-only), skip single-key ones, and load only its columns.
func TestListMultiKeyChannelsForReaper_AnyTypeMultiKeyOnly(t *testing.T) {
	setupSQLiteDB(t)
	openai := seedPoolChannel(t, "pool-openai", 1, true, 2)
	router := seedPoolChannel(t, "pool-router", 20, true, 2)
	seedPoolChannel(t, "single", 1, false, 0)

	got, err := ListMultiKeyChannelsForReaper()
	if err != nil {
		t.Fatalf("ListMultiKeyChannelsForReaper: %v", err)
	}
	ids := map[int]bool{}
	for _, c := range got {
		ids[c.Id] = true
		if c.Key != "" || c.Name != "" || c.Models != "" {
			t.Errorf("channel %d loaded columns beyond the reaper's own (key=%q name=%q)", c.Id, c.Key, c.Name)
		}
		if !c.ChannelInfo.IsMultiKey {
			t.Errorf("channel %d is not multi-key", c.Id)
		}
	}
	if len(got) != 2 || !ids[openai.Id] || !ids[router.Id] {
		t.Fatalf("listed ids = %v, want exactly {%d,%d}", ids, openai.Id, router.Id)
	}
}

// Writing back a partially loaded channel must touch only the pool columns.
func TestSaveChannelPoolState_LeavesOtherColumnsAlone(t *testing.T) {
	setupSQLiteDB(t)
	ch := seedPoolChannel(t, "keep-me", 1, true, 2)

	got, err := ListMultiKeyChannelsForReaper()
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v len=%d", err, len(got))
	}
	got[0].Status = common.ChannelStatusAutoDisabled
	got[0].ChannelInfo.MultiKeyCooldownUntil = map[int]int64{0: 123}
	if err := SaveChannelPoolState(got[0]); err != nil {
		t.Fatalf("SaveChannelPoolState: %v", err)
	}

	var back Channel
	if err := DB.First(&back, ch.Id).Error; err != nil {
		t.Fatal(err)
	}
	if back.Name != "keep-me" || back.Key != "key-a\nkey-b" || back.Models != "pool-model" || back.Group != "default" {
		t.Fatalf("unrelated columns were overwritten: %+v", back)
	}
	if back.Status != common.ChannelStatusAutoDisabled || back.ChannelInfo.MultiKeyCooldownUntil[0] != 123 {
		t.Fatalf("pool columns not persisted: status=%d cd=%v", back.Status, back.ChannelInfo.MultiKeyCooldownUntil)
	}
}

// keepStatus parks the last key without flipping the channel.
func TestApplyMultiKeyCooldownOpt_KeepStatusNeverFlipsChannel(t *testing.T) {
	for _, keep := range []bool{false, true} {
		ch := &Channel{Key: "key-a", Status: common.ChannelStatusEnabled}
		ch.ChannelInfo.IsMultiKey = true
		ch.ChannelInfo.MultiKeySize = 1
		applyMultiKeyCooldownOpt(ch, "key-a", time.Now().Unix()+60, "r", keep)
		if ch.ChannelInfo.MultiKeyCooldownUntil[0] == 0 {
			t.Fatalf("keep=%v: key not parked", keep)
		}
		flipped := ch.Status == common.ChannelStatusAutoDisabled
		if flipped == keep {
			t.Fatalf("keep=%v: flipped=%v", keep, flipped)
		}
	}
}

func cachedAutoDisabledPool(id int, until int64) *Channel {
	ch := &Channel{Id: id, Status: common.ChannelStatusAutoDisabled, Models: "pool-model", Group: "default", TenantId: "default"}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 1
	ch.ChannelInfo.MultiKeyCooldownUntil = map[int]int64{0: until}
	return ch
}

// EarliestMultiKeyRecovery must not hold channelSyncLock while it waits for a
// polling lock: a holder of the polling lock that then needs the sync write
// lock (the rebuild order) would deadlock against it.
func TestEarliestMultiKeyRecovery_NoLockOrderInversion(t *testing.T) {
	prevMem, prevIDM := common.MemoryCacheEnabled, channelsIDM
	t.Cleanup(func() { common.MemoryCacheEnabled = prevMem; channelsIDM = prevIDM })
	common.MemoryCacheEnabled = true
	until := common.GetTimestamp() + 120
	channelsIDM = map[int]*Channel{9801: cachedAutoDisabledPool(9801, until)}

	pollingLock := GetChannelPollingLock(9801)
	pollingLock.Lock()

	earliestDone := make(chan struct{})
	go func() {
		EarliestMultiKeyRecovery("default", "default", "pool-model", nil)
		close(earliestDone)
	}()
	time.Sleep(50 * time.Millisecond) // let it reach the polling lock

	writerDone := make(chan struct{})
	go func() {
		channelSyncLock.Lock() // what a cache rebuild does while a polling lock is held
		_ = struct{}{}         // the critical section is intentionally empty: only the acquisition order matters
		channelSyncLock.Unlock()
		close(writerDone)
	}()

	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("channelSyncLock write lock starved: EarliestMultiKeyRecovery holds it while waiting for a polling lock")
	}
	pollingLock.Unlock()
	select {
	case <-earliestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("EarliestMultiKeyRecovery did not finish after the polling lock was released")
	}
}

// Concurrent readers, a polling-lock writer and a cache writer; meant for
// `go test -race` on a host with a C toolchain.
func TestEarliestMultiKeyRecovery_ConcurrentRace(t *testing.T) {
	prevMem, prevIDM := common.MemoryCacheEnabled, channelsIDM
	t.Cleanup(func() { common.MemoryCacheEnabled = prevMem; channelsIDM = prevIDM })
	common.MemoryCacheEnabled = true
	until := common.GetTimestamp() + 120
	channelsIDM = map[int]*Channel{9802: cachedAutoDisabledPool(9802, until), 9803: cachedAutoDisabledPool(9803, until+5)}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if got, ok := EarliestMultiKeyRecovery("default", "default", "pool-model", nil); !ok || got != until {
					t.Errorf("recovery = %d,%v want %d,true", got, ok, until)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			pl := GetChannelPollingLock(9802)
			pl.Lock()
			channelsIDM[9802].ChannelInfo.MultiKeyCooldownUntil[0] = until
			pl.Unlock()
		}
	}()
	wg.Wait()
}

// The write reports the earliest recovery only once no key is left serving, and
// ignores keys that are parked without a deadline.
func TestApplyMultiKeyCooldownOpt_ReportsEarliestRecoveryWhenAllParked(t *testing.T) {
	for _, keep := range []bool{false, true} {
		ch := &Channel{Key: "key-a\nkey-b", Status: common.ChannelStatusEnabled}
		ch.ChannelInfo.IsMultiKey = true
		ch.ChannelInfo.MultiKeySize = 2
		now := time.Now().Unix()
		if got := applyMultiKeyCooldownOpt(ch, "key-a", now+100, "r", keep); got != 0 {
			t.Fatalf("keep=%v: one key still serving, got deadline %d", keep, got)
		}
		if got := applyMultiKeyCooldownOpt(ch, "key-b", now+50, "r", keep); got != now+50 {
			t.Fatalf("keep=%v: all parked, got %d want %d", keep, got, now+50)
		}
	}
}

// Last key parked with the status flip: the cached routing index must drop the
// channel immediately instead of at the next sync, and a status-preserving
// park must leave it routable (the channel-level slot handles that case).
func TestMarkMultiKeyCooldown_MemoryCache_LastKeyUpdatesRoutingIndex(t *testing.T) {
	for _, keep := range []bool{false, true} {
		setupSQLiteDB(t)
		withMemoryCache(t)
		ch := seedPoolChannel(t, "idx", 1, true, 1)
		ch.Key = "key-a"
		if err := DB.Save(ch).Error; err != nil {
			t.Fatal(err)
		}
		if err := DB.Create(&Ability{Group: "default", Model: "pool-model", ChannelId: ch.Id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		InitChannelCache()
		if got, err := GetRandomSatisfiedChannel("default", "pool-model", 0); err != nil || got == nil {
			t.Fatalf("precondition: channel must be routable, got %v %v", got, err)
		}

		until := time.Now().Unix() + 120
		ok, allUntil := MarkMultiKeyCooldownReport(ch.Id, "key-a", until, "r", keep)
		if !ok || allUntil != until {
			t.Fatalf("keep=%v: ok=%v allParkedUntil=%d want %d", keep, ok, allUntil, until)
		}
		got, _ := GetRandomSatisfiedChannel("default", "pool-model", 0)
		if keep && got == nil {
			t.Fatal("keep-status park must not remove the channel from the routing index")
		}
		if !keep && got != nil {
			t.Fatal("status-flipping park left the channel in the cached routing index")
		}
	}
}

// Status is written by CacheUpdateChannelStatus under channelSyncLock only, and
// by the cooldown apply under the polling lock only; the recovery scan must hold
// both while it reads. Meant for `go test -race` on a host with a C toolchain.
func TestEarliestMultiKeyRecovery_ConcurrentStatusFlip(t *testing.T) {
	prevMem, prevIDM := common.MemoryCacheEnabled, channelsIDM
	t.Cleanup(func() { common.MemoryCacheEnabled = prevMem; channelsIDM = prevIDM })
	common.MemoryCacheEnabled = true
	until := common.GetTimestamp() + 120
	channelsIDM = map[int]*Channel{9811: cachedAutoDisabledPool(9811, until)}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				EarliestMultiKeyRecovery("default", "default", "pool-model", nil)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 300; j++ {
			pl := GetChannelPollingLock(9811)
			pl.Lock()
			channelsIDM[9811].ChannelInfo.MultiKeySize = 1
			pl.Unlock()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 300; j++ {
			CacheUpdateChannelStatus(9811, common.ChannelStatusAutoDisabled)
		}
	}()
	wg.Wait()
}
