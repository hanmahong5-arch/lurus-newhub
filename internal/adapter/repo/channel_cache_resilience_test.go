package repo

// channel_cache_resilience_test.go — cycle-11 L8. Two independent failure
// modes in the channel cache sync path:
//
//   - InitChannelCache pre-seeds its group->model->channels map only from
//     Ability rows, then iterates every channel's OWN Group string to fill
//     it in. A channel whose group has no Ability row yet (abilities lag
//     channel creation) hits a nil inner map on write and panics.
//   - The periodic sync loop (SyncChannelCacheWithContext) called
//     InitChannelCache directly with no recovery of its own. That loop runs
//     inside an errgroup.Group.Go goroutine (cmd/server/main.go), and
//     errgroup deliberately does not recover a panicking goroutine — so a
//     panic there (the one above, or anything else) crashed the whole
//     process, loud and k8s-visible, not a silent per-goroutine death.
//     syncChannelCacheOnce now recovers each tick instead, trading that
//     loud crash-and-restart for a quiet counter increment; see its doc
//     comment in channel_cache.go for the full trade-off.
//
// Cycle-12 L8 adds a third failure mode to the same file:
//
//   - Both of InitChannelCache's reads (channels, abilities) discarded their
//     error, so a failed query produced an EMPTY result slice that the
//     rebuild then swapped into the live routing table. One failed read =
//     that replica relays nothing until the next successful sync, with no
//     signal anywhere. The rebuild now aborts and keeps the previous table.
//
// The tests below drive the real functions (InitChannelCache /
// syncChannelCacheOnce / SyncChannelCacheWithContext), not a
// reimplementation of their logic.

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// snapshotChannelCacheGlobals copies the package-level cache maps
// InitChannelCache writes and restores them in t.Cleanup. Without this a
// test that seeds a throwaway channel/group into the process-wide cache
// (as TestInitChannelCache_ToleratesGroupWithoutAbilityRows does) leaks
// that state into every later test in this package that reads the cache.
func snapshotChannelCacheGlobals(t *testing.T) {
	t.Helper()
	channelSyncLock.RLock()
	prevGroup2model2channels := group2model2channels
	prevChannelsIDM := channelsIDM
	channelSyncLock.RUnlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		group2model2channels = prevGroup2model2channels
		channelsIDM = prevChannelsIDM
		channelSyncLock.Unlock()
	})
}

// TestInitChannelCache_ToleratesGroupWithoutAbilityRows seeds an enabled
// channel in a group that has zero Ability rows (the group set
// InitChannelCache pre-seeds its map from) and asserts InitChannelCache
// does not panic — and that the channel is actually routable afterwards,
// not just silently dropped.
func TestInitChannelCache_ToleratesGroupWithoutAbilityRows(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })
	snapshotChannelCacheGlobals(t)

	ch := &Channel{
		Type: 1, Status: common.ChannelStatusEnabled,
		Name: "orphan-group-channel", Models: "model-x", Group: "orphan-group",
		TenantId: "default",
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	// Deliberately no Ability row for "orphan-group" — that's the gap.

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("InitChannelCache panicked on a group with no Ability rows: %v", r)
		}
	}()
	InitChannelCache()

	got, err := GetRandomSatisfiedChannelForTenant("", "orphan-group", "model-x", 1)
	if err != nil || got == nil || got.Id != ch.Id {
		t.Fatalf("GetRandomSatisfiedChannelForTenant(orphan-group, model-x) = %+v, %v; want channel %d", got, err, ch.Id)
	}
}

// TestSyncChannelCacheOnce_RecoversPanic injects a panic behind the
// initChannelCacheFn seam and asserts syncChannelCacheOnce recovers it
// (does not propagate) and records it under the channel_cache_sync source.
func TestSyncChannelCacheOnce_RecoversPanic(t *testing.T) {
	prevFn := initChannelCacheFn
	t.Cleanup(func() { initChannelCacheFn = prevFn })
	initChannelCacheFn = func() {
		panic("injected channel cache sync panic")
	}

	before := testutil.ToFloat64(metrics.PanicsRecovered.WithLabelValues("channel_cache_sync"))

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("syncChannelCacheOnce did not recover the injected panic: %v", r)
			}
		}()
		syncChannelCacheOnce()
	}()

	after := testutil.ToFloat64(metrics.PanicsRecovered.WithLabelValues("channel_cache_sync"))
	if after-before != 1 {
		t.Fatalf("PanicsRecovered{source=channel_cache_sync} did not increment by 1: before=%f after=%f", before, after)
	}
}

// TestSyncChannelCacheWithContext_RecoversPanic drives
// SyncChannelCacheWithContext itself — the production ticker loop
// cmd/server/main.go calls — rather than syncChannelCacheOnce directly.
// Mutation target: reverting the ticker case in SyncChannelCacheWithContext
// to a bare `common.SysLog(...); InitChannelCache()` call (no recover,
// bypassing the initChannelCacheFn seam) turns this red —
// TestSyncChannelCacheOnce_RecoversPanic alone does not call
// SyncChannelCacheWithContext.
func TestSyncChannelCacheWithContext_RecoversPanic(t *testing.T) {
	prevFn := initChannelCacheFn
	t.Cleanup(func() { initChannelCacheFn = prevFn })
	initChannelCacheFn = func() {
		panic("injected channel cache sync panic (SyncChannelCacheWithContext)")
	}

	before := testutil.ToFloat64(metrics.PanicsRecovered.WithLabelValues("channel_cache_sync"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		SyncChannelCacheWithContext(ctx, 1) // 1s ticker — the minimum NewTicker accepts
	}()

	// Wait for the first tick's recovered panic to land, then cancel — this
	// is "the context is cancelled after the first tick" the operator
	// decision specified.
	deadline := time.Now().Add(5 * time.Second)
	for testutil.ToFloat64(metrics.PanicsRecovered.WithLabelValues("channel_cache_sync")) == before {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("PanicsRecovered{source=channel_cache_sync} never incremented via SyncChannelCacheWithContext within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SyncChannelCacheWithContext did not return after context cancellation (panic propagated out of the goroutine instead of being recovered)")
	}

	after := testutil.ToFloat64(metrics.PanicsRecovered.WithLabelValues("channel_cache_sync"))
	if after-before < 1 {
		t.Fatalf("PanicsRecovered{source=channel_cache_sync} did not increment via SyncChannelCacheWithContext: before=%f after=%f", before, after)
	}
}

// TestInitChannelCache_KeepsPreviousTableWhenQueryFails is the cycle-12 L8
// oracle: a rebuild whose database read fails must leave the previously
// built routing table in place and record the failure, instead of swapping
// in the empty result of the failed query.
//
// The failure is injected by closing the pool underneath the live *gorm.DB,
// which is what a repo-tier (hermetic SQLite) test can reproduce of "the
// database went away mid-sync". Pre-fix this test fails on the routability
// assertion: GORM leaves `channels` empty on the failed Find, the rebuild
// treats that as "there are no channels", and the swap wipes the table.
//
// Mutation targets, both measured 2026-09-19 rather than predicted:
// reverting the channels read alone to a bare `DB.Find(&channels)` turns
// the counter assertion red but NOT the routability one (the abilities read
// then aborts the pass and the table survives by accident); reverting both
// reads turns both assertions red. The counter assertion is what makes the
// single-read mutation detectable at all.
func TestInitChannelCache_KeepsPreviousTableWhenQueryFails(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })
	snapshotChannelCacheGlobals(t)

	ch := &Channel{
		Type: 1, Status: common.ChannelStatusEnabled,
		Name: "keep-me-on-failure", Models: "model-keep", Group: "keep-group",
		TenantId: "default",
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	InitChannelCache()
	if got, err := GetRandomSatisfiedChannelForTenant("", "keep-group", "model-keep", 1); err != nil || got == nil {
		t.Fatalf("precondition: channel not routable after a healthy sync: %+v, %v", got, err)
	}

	// Take the database away. Every later query on this *gorm.DB returns
	// "sql: database is closed".
	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("DB.DB(): %v", err)
	}
	if cerr := sqlDB.Close(); cerr != nil {
		t.Fatalf("close pool: %v", cerr)
	}

	before := testutil.ToFloat64(metrics.ChannelCacheSyncFailedTotal.WithLabelValues("channels"))
	InitChannelCache()
	after := testutil.ToFloat64(metrics.ChannelCacheSyncFailedTotal.WithLabelValues("channels"))

	got, rerr := GetRandomSatisfiedChannelForTenant("", "keep-group", "model-keep", 1)
	if rerr != nil || got == nil || got.Id != ch.Id {
		t.Errorf("after a failed sync the previous routing table was lost: "+
			"GetRandomSatisfiedChannelForTenant(keep-group, model-keep) = %+v, %v; want channel %d. "+
			"A failed read must abort the rebuild, not swap in its empty result.", got, rerr, ch.Id)
	}
	if after-before != 1 {
		t.Errorf("ChannelCacheSyncFailedTotal{query=channels} did not increment by 1: before=%f after=%f", before, after)
	}
}

// TestInitChannelCacheQuery_ReturnsTheFailure covers the error value itself:
// InitChannelCache keeps its func() signature (see its doc comment for why),
// so the abandoned-rebuild reason is observable only through the unexported
// worker it delegates to. Without this, "the rebuild aborted" and "the
// rebuild silently did nothing" would be indistinguishable from inside the
// package.
func TestInitChannelCacheQuery_ReturnsTheFailure(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })
	snapshotChannelCacheGlobals(t)

	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("DB.DB(): %v", err)
	}
	if cerr := sqlDB.Close(); cerr != nil {
		t.Fatalf("close pool: %v", cerr)
	}

	if rerr := rebuildChannelCache(); rerr == nil {
		t.Fatal("rebuildChannelCache() returned nil on a closed pool; want the read's error")
	}
}
