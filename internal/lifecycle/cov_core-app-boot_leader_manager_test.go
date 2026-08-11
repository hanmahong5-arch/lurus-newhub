package lifecycle

// cov_core-app-boot_leader_manager_test.go — business-acceptance coverage for
// LeaderManager (leader_election.go), left at 0% by the existing
// leader_election_test.go which only drives the generic LeaderTask gate.
// Uses a hermetic per-test sqlite DB (same pattern as audit_cleanup_test.go)
// swapped into repo.DB, so the real repo.TryAcquireOrRenew/ReleaseLease SQL
// paths run without touching TEST_POSTGRES_DSN.
//
// Timing note: Run()'s ticker-driven renewal loop is exercised with a single
// short-but-generous wait (500ms budget for a 20ms tick), matching the style
// of the existing (documented-flaky-elsewhere, untouched) lifecycle tests —
// this file adds no new sub-10ms timing assertions.

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var coreAppBootLeaderDBCounter atomic.Int64

// core_app_boot_openLeaderTestDB swaps repo.DB for a throwaway in-memory
// sqlite DB migrated with the LeaderElection table, and restores it on
// cleanup. Distinct DSN per call keeps -count=1 safe across tests.
func core_app_boot_openLeaderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:coreappbootleader%d?mode=memory&cache=shared", coreAppBootLeaderDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.LeaderElection{}); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("migrate: %v", err)
	}

	prev := repo.DB
	repo.DB = db
	t.Cleanup(func() {
		repo.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// core_app_boot_saveRestoreLeaderFlag snapshots common.IsLeader() and
// restores it after the test so leakage doesn't affect sibling tests in this
// package (audit_cleanup / privacy_erasure / graceful_shutdown all read it).
func core_app_boot_saveRestoreLeaderFlag(t *testing.T) {
	t.Helper()
	prev := common.IsLeader()
	t.Cleanup(func() { common.SetLeader(prev) })
}

// TestCoreAppBootLeaderManager_NewLeaderManager_WiresFromEntityConstants
// asserts the constructor derives every field from the shared entity
// constants and common.NodeHolderID rather than hardcoding — a regression
// here would silently desync the boot-lease TTL from the runtime lease TTL.
func TestCoreAppBootLeaderManager_NewLeaderManager_WiresFromEntityConstants(t *testing.T) {
	m := NewLeaderManager()

	if m.name != entity.LeaderElectionName {
		t.Errorf("name = %q, want %q", m.name, entity.LeaderElectionName)
	}
	if m.ttlSeconds != entity.LeaderLeaseTTLSeconds {
		t.Errorf("ttlSeconds = %d, want %d", m.ttlSeconds, entity.LeaderLeaseTTLSeconds)
	}
	if m.holderId != common.NodeHolderID() {
		t.Errorf("holderId = %q, want %q", m.holderId, common.NodeHolderID())
	}
	if m.holderId == "" {
		t.Error("holderId must not be empty")
	}
	wantRenew := time.Duration(entity.LeaderLeaseTTLSeconds/renewDivisor) * time.Second
	if m.renewEvery != wantRenew {
		t.Errorf("renewEvery = %v, want %v (ttl/renewDivisor)", m.renewEvery, wantRenew)
	}
}

// TestCoreAppBootLeaderManager_Name locks the fixed Task name.
func TestCoreAppBootLeaderManager_Name(t *testing.T) {
	m := NewLeaderManager()
	if got := m.Name(); got != "leader-election" {
		t.Errorf("Name() = %q, want %q", got, "leader-election")
	}
}

// TestCoreAppBootLeaderManager_IsLeader verifies the passthrough to
// common.IsLeader() in both states.
func TestCoreAppBootLeaderManager_IsLeader(t *testing.T) {
	core_app_boot_saveRestoreLeaderFlag(t)
	m := NewLeaderManager()

	common.SetLeader(false)
	if m.IsLeader() {
		t.Error("expected IsLeader()=false")
	}
	common.SetLeader(true)
	if !m.IsLeader() {
		t.Error("expected IsLeader()=true")
	}
}

// TestCoreAppBootLeaderManager_Step_AcquireRenewAndHandoff drives step()
// directly (deterministic clock) across the full lifecycle: first acquire,
// renewal by the same holder, and losing the lease to a second holder once
// the first's lease has lapsed.
func TestCoreAppBootLeaderManager_Step_AcquireRenewAndHandoff(t *testing.T) {
	core_app_boot_openLeaderTestDB(t)
	core_app_boot_saveRestoreLeaderFlag(t)
	common.SetLeader(false)

	m1 := &LeaderManager{name: "test-lease", holderId: "holder-1", ttlSeconds: 5}
	m2 := &LeaderManager{name: "test-lease", holderId: "holder-2", ttlSeconds: 5}

	// t=1000: holder-1 acquires the fresh lease.
	if ok := m1.step(1000); !ok {
		t.Fatal("expected holder-1 to acquire the fresh lease")
	}
	if !common.IsLeader() {
		t.Error("expected common.IsLeader()=true after successful acquire")
	}
	if m1.localExpiry != 1005 {
		t.Errorf("localExpiry = %d, want 1005 (now+ttl)", m1.localExpiry)
	}

	// t=1002 (within TTL): holder-1 renews successfully.
	if ok := m1.step(1002); !ok {
		t.Fatal("expected holder-1 to renew its own still-valid lease")
	}

	// t=1002 (still within holder-1's lease): holder-2 must NOT take over.
	if ok := m2.step(1002); ok {
		t.Fatal("expected holder-2 to fail to acquire while holder-1's lease is valid")
	}

	// t=1010 (past holder-1's last renewed-at+ttl=1007): holder-2 takes over.
	if ok := m2.step(1010); !ok {
		t.Fatal("expected holder-2 to acquire after holder-1's lease lapsed")
	}

	// t=1010: holder-1 has lost it — its own step must now report false and
	// flip the shared common.IsLeader() cache to false.
	if ok := m1.step(1010); ok {
		t.Fatal("expected holder-1 to observe it no longer owns the lease")
	}
	if common.IsLeader() {
		t.Error("expected common.IsLeader()=false after holder-1 lost the lease")
	}
}

// TestCoreAppBootLeaderManager_Step_DBErrorHoldsThenExpiresLocally covers the
// transient-error branch: a DB failure while we still believe we're leader
// and haven't locally lapsed must NOT immediately demote us (absorbs
// transient blips), but once now >= our local lease estimate it must step
// down rather than hold leadership forever on a broken DB.
func TestCoreAppBootLeaderManager_Step_DBErrorHoldsThenExpiresLocally(t *testing.T) {
	db := core_app_boot_openLeaderTestDB(t)
	core_app_boot_saveRestoreLeaderFlag(t)
	common.SetLeader(false)

	m := &LeaderManager{name: "broken-lease", holderId: "holder-x", ttlSeconds: 5}

	// Successfully acquire first so localExpiry/common.IsLeader are populated.
	if ok := m.step(2000); !ok {
		t.Fatal("expected initial acquire to succeed")
	}
	if m.localExpiry != 2005 {
		t.Fatalf("localExpiry = %d, want 2005", m.localExpiry)
	}

	// Sabotage the DB: drop the table so subsequent queries error.
	if err := db.Migrator().DropTable(&entity.LeaderElection{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	// Before local expiry (2000 <= now < 2005): must hold leadership despite
	// the DB error (absorbing a transient blip).
	if ok := m.step(2003); !ok {
		t.Error("expected leadership to be held through a transient DB error before local expiry")
	}
	if !common.IsLeader() {
		t.Error("expected common.IsLeader() to remain true before local expiry, despite DB error")
	}

	// At/after local expiry (now=2005): must step down since the DB error
	// means we can no longer confirm the lease is renewed.
	if ok := m.step(2005); ok {
		t.Error("expected step() to report false once the local lease estimate has lapsed under a DB error")
	}
	if common.IsLeader() {
		t.Error("expected common.IsLeader()=false once locally lapsed under a DB error")
	}
}

// TestCoreAppBootLeaderManager_Run_AcquiresRenewsAndReleasesOnCancel drives
// the full Run loop against the hermetic DB: it must acquire immediately
// (single-node fast path), keep renewing on its ticker, and release the
// lease (expires_at=0) plus flip common.IsLeader() false when ctx is
// cancelled while holding leadership.
func TestCoreAppBootLeaderManager_Run_AcquiresRenewsAndReleasesOnCancel(t *testing.T) {
	db := core_app_boot_openLeaderTestDB(t)
	core_app_boot_saveRestoreLeaderFlag(t)
	common.SetLeader(false)

	m := &LeaderManager{
		name:       "run-lease",
		holderId:   "holder-run",
		ttlSeconds: 30,
		renewEvery: 20 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	// Wait for the immediate-acquire fast path to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !common.IsLeader() {
		time.Sleep(5 * time.Millisecond)
	}
	if !common.IsLeader() {
		t.Fatal("expected Run to acquire leadership promptly on the single-node fast path")
	}

	// Let at least one renewal tick pass, then cancel.
	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected Run to return ctx.Err() on cancellation, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	if common.IsLeader() {
		t.Error("expected common.IsLeader()=false after graceful release on shutdown")
	}

	var row entity.LeaderElection
	if err := db.Where("name = ?", "run-lease").First(&row).Error; err != nil {
		t.Fatalf("fetch lease row: %v", err)
	}
	if row.ExpiresAt != 0 {
		t.Errorf("expires_at = %d, want 0 (ReleaseLease must expire immediately so a successor takes over)", row.ExpiresAt)
	}
	if row.HolderId != "holder-run" {
		t.Errorf("holder_id = %q, want %q (release only clears expiry, not ownership)", row.HolderId, "holder-run")
	}
}
