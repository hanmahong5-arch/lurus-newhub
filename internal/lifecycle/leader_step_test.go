package lifecycle

// leader_step_test.go — LeaderManager.step is the promotion/demotion decision
// that every leader-gated background task keys off (reaper, aggregator, audit
// cleanup, secret rotation). repo.TryAcquireOrRenew is covered on its own, but
// the step around it — when the cached common.IsLeader flag flips, and the
// grace window that keeps a leader through a transient DB blip — was not.
//
// Hermetic: an in-memory SQLite database standing in for the lease table, and an
// injected clock, so no PG DSN and no wall-clock sleeps are involved.

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var leaderStepDBCounter atomic.Int64

// leaderStepEnv points repo.DB at a fresh in-memory database holding only the
// lease table, and restores repo.DB plus the cached leadership flag afterwards.
func leaderStepEnv(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:leaderstep%d?mode=memory&cache=shared", leaderStepDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Discard, // the error-path cases log expected failures
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.LeaderElection{}); err != nil {
		t.Fatalf("automigrate leader_elections: %v", err)
	}

	prevDB, prevLeader := repo.DB, common.IsLeader()
	repo.DB = db
	common.SetLeader(false)
	t.Cleanup(func() {
		repo.DB = prevDB
		common.SetLeader(prevLeader)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func leaderStepManager() *LeaderManager {
	return &LeaderManager{
		name:       "leader-step-test",
		holderId:   "node-under-test",
		ttlSeconds: 30,
	}
}

// A node that wins the free lease must be promoted immediately — the cached
// flag is what gates every leader-only task, so a step that acquires the lease
// but leaves IsLeader() false would silently stall all of them.
func TestLeaderManagerStep_AcquiresFreeLeaseAndPromotes(t *testing.T) {
	db := leaderStepEnv(t)
	m := leaderStepManager()

	if !m.step(1000) {
		t.Fatal("step on a free lease returned false, want the lease to be acquired")
	}
	if !common.IsLeader() {
		t.Error("common.IsLeader() = false after acquiring the lease")
	}
	if m.localExpiry != 1000+m.ttlSeconds {
		t.Errorf("localExpiry = %d, want %d", m.localExpiry, 1000+m.ttlSeconds)
	}

	var lease entity.LeaderElection
	if err := db.First(&lease, "name = ?", m.name).Error; err != nil {
		t.Fatalf("lease row not persisted: %v", err)
	}
	if lease.HolderId != m.holderId {
		t.Errorf("holder = %q, want %q", lease.HolderId, m.holderId)
	}

	// Renewing pushes the local estimate forward without a leadership flap.
	if !m.step(1010) {
		t.Fatal("renew step returned false while still holding the lease")
	}
	if m.localExpiry != 1010+m.ttlSeconds {
		t.Errorf("localExpiry after renew = %d, want %d", m.localExpiry, 1010+m.ttlSeconds)
	}
}

// Losing the race to a live holder must demote, even if this node believed it
// was the leader a moment ago — two leaders running the reaper concurrently is
// exactly what the lease exists to prevent.
func TestLeaderManagerStep_DemotesWhenAnotherHolderOwnsTheLease(t *testing.T) {
	db := leaderStepEnv(t)
	m := leaderStepManager()

	if err := db.Create(&entity.LeaderElection{
		Name:       m.name,
		HolderId:   "another-node",
		AcquiredAt: 900,
		RenewedAt:  900,
		ExpiresAt:  2000, // still valid at now=1000
	}).Error; err != nil {
		t.Fatalf("seed foreign lease: %v", err)
	}
	common.SetLeader(true) // stale belief from a previous term

	if m.step(1000) {
		t.Fatal("step returned true against a live lease held by another node")
	}
	if common.IsLeader() {
		t.Error("common.IsLeader() = true while another node holds a valid lease")
	}
}

// An expired foreign lease is up for grabs: the takeover path (conditional
// UPDATE, not INSERT) must both win and promote.
func TestLeaderManagerStep_TakesOverExpiredLease(t *testing.T) {
	db := leaderStepEnv(t)
	m := leaderStepManager()

	if err := db.Create(&entity.LeaderElection{
		Name:       m.name,
		HolderId:   "dead-node",
		AcquiredAt: 100,
		RenewedAt:  100,
		ExpiresAt:  500, // lapsed before now=1000
	}).Error; err != nil {
		t.Fatalf("seed expired lease: %v", err)
	}

	if !m.step(1000) {
		t.Fatal("step did not take over an expired lease")
	}
	if !common.IsLeader() {
		t.Error("common.IsLeader() = false after taking over an expired lease")
	}

	var lease entity.LeaderElection
	if err := db.First(&lease, "name = ?", m.name).Error; err != nil {
		t.Fatalf("reload lease: %v", err)
	}
	if lease.HolderId != m.holderId {
		t.Errorf("holder = %q, want the taking-over node %q", lease.HolderId, m.holderId)
	}
}

// A transient DB failure must not cause failover churn: the leader keeps its
// term until the lease it already holds would have lapsed anyway.
func TestLeaderManagerStep_DBErrorKeepsLeadershipInsideLocalLease(t *testing.T) {
	db := leaderStepEnv(t)
	m := leaderStepManager()

	if !m.step(1000) {
		t.Fatal("precondition: initial acquire failed")
	}
	if err := db.Migrator().DropTable(&entity.LeaderElection{}); err != nil {
		t.Fatalf("drop lease table: %v", err)
	}

	// now < localExpiry (1030): the blip is absorbed.
	if !m.step(1005) {
		t.Error("step reported loss of leadership on a transient DB error inside the local lease window")
	}
	if !common.IsLeader() {
		t.Error("common.IsLeader() = false after a transient DB error inside the local lease window")
	}
}

// Once the local estimate of our own lease has lapsed we can no longer assume
// anyone still recognises us, so a continuing DB failure must step us down —
// otherwise a partitioned pod keeps running leader-only work forever.
func TestLeaderManagerStep_DBErrorStepsDownAfterLocalLeaseLapses(t *testing.T) {
	db := leaderStepEnv(t)
	m := leaderStepManager()

	if !m.step(1000) {
		t.Fatal("precondition: initial acquire failed")
	}
	if err := db.Migrator().DropTable(&entity.LeaderElection{}); err != nil {
		t.Fatalf("drop lease table: %v", err)
	}

	// now >= localExpiry (1030).
	if m.step(1031) {
		t.Error("step still reported leadership after the local lease lapsed under a DB error")
	}
	if common.IsLeader() {
		t.Error("common.IsLeader() = true after the local lease lapsed under a DB error")
	}
}

// A malformed manager (empty holder id) fails validation inside
// TryAcquireOrRenew rather than reaching the database; a non-leader must simply
// stay a non-leader instead of being promoted by an error path.
func TestLeaderManagerStep_ValidationErrorLeavesNonLeaderUnpromoted(t *testing.T) {
	leaderStepEnv(t)
	m := &LeaderManager{name: "leader-step-test", holderId: "", ttlSeconds: 30}

	if m.step(1000) {
		t.Error("step returned true for a manager with no holder id")
	}
	if common.IsLeader() {
		t.Error("common.IsLeader() = true after a validation error")
	}
}

// Guard for the test itself: the SQLite duplicate-key text this suite relies on
// must still be recognised as "someone else holds it" rather than surfacing as
// an error, or TestLeaderManagerStep_DemotesWhenAnotherHolderOwnsTheLease would
// pass through the error branch instead of the contended one.
func TestLeaderManagerStep_ContendedInsertIsNotAnError(t *testing.T) {
	db := leaderStepEnv(t)
	m := leaderStepManager()

	if err := db.Create(&entity.LeaderElection{
		Name:       m.name,
		HolderId:   "another-node",
		AcquiredAt: 900,
		RenewedAt:  900,
		ExpiresAt:  2000,
	}).Error; err != nil {
		t.Fatalf("seed foreign lease: %v", err)
	}

	ok, err := repo.TryAcquireOrRenew(m.name, m.holderId, m.ttlSeconds, 1000)
	if err != nil {
		t.Fatalf("contended acquire returned an error instead of ok=false: %v", err)
	}
	if ok {
		t.Fatal("contended acquire returned ok=true")
	}

	// And the validation failure this suite also exercises really is an error.
	if _, err := repo.TryAcquireOrRenew(m.name, "", m.ttlSeconds, 1000); err == nil ||
		!strings.Contains(err.Error(), "holderId") {
		t.Errorf("empty holderId error = %v, want a validation error naming holderId", err)
	}
}
