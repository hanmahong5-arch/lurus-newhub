package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var leaderCoverageDBCounter atomic.Int64

// openLeaderTestDB gives each test its own in-memory sqlite DB backing
// entity.LeaderElection, swapping it into repo.DB for the duration of the
// test and restoring the previous value on cleanup.
func openLeaderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:leaderelect%d?mode=memory&cache=shared", leaderCoverageDBCounter.Add(1))
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

// withLeaderStateReset snapshots the process-global common.IsLeader() flag
// and restores it after the test so state does not leak between tests.
func withLeaderStateReset(t *testing.T) {
	t.Helper()
	prev := common.IsLeader()
	t.Cleanup(func() { common.SetLeader(prev) })
}

// syncBuffer is a bytes.Buffer guarded by a mutex. common.SysLog writes to the
// process-global gin.DefaultWriter, and the privacy-erasure / secret-rotation
// executors log from a detached (SafeGoWithContext, no join) background goroutine
// that can outlive the spawning test. Two -race hazards follow: (1) the leaked
// goroutine's buffer Write races a test's String()/Reset(); (2) if each test
// swapped gin.DefaultWriter, the leaked goroutine's *read* of that global would
// race the next test's *write* of it. Both are solved by installing ONE shared
// syncBuffer as gin.DefaultWriter exactly once in TestMain (never swapped again)
// and serialising all buffer access through the mutex.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// sharedLogBuf is wired to gin.DefaultWriter once (TestMain) and never rebound,
// so no test writes the gin.DefaultWriter global after startup — the leaked
// executor goroutine only ever reads it, which is safe on a read-only-after-init
// global.
var sharedLogBuf = &syncBuffer{}

func TestMain(m *testing.M) {
	gin.DefaultWriter = sharedLogBuf
	os.Exit(m.Run())
}

// captureSysLog resets the shared mutex-guarded log buffer and returns it. It
// deliberately does NOT swap gin.DefaultWriter (that would race the leaked
// executor goroutine that reads it) — the buffer is already wired via TestMain.
// Stray lines a leaked goroutine appends from a prior test are harmless: each
// assertion uses strings.Contains on its own distinct message.
func captureSysLog(t *testing.T) *syncBuffer {
	t.Helper()
	sharedLogBuf.Reset()
	return sharedLogBuf
}

func TestNewLeaderManager_ConstructsExpectedFields(t *testing.T) {
	m := NewLeaderManager()

	if m.name != entity.LeaderElectionName {
		t.Errorf("name = %q, want %q", m.name, entity.LeaderElectionName)
	}
	if m.ttlSeconds != entity.LeaderLeaseTTLSeconds {
		t.Errorf("ttlSeconds = %d, want %d", m.ttlSeconds, entity.LeaderLeaseTTLSeconds)
	}
	// renewEvery = ttl/renewDivisor = 30/3 = 10s.
	if m.renewEvery != 10*time.Second {
		t.Errorf("renewEvery = %s, want 10s", m.renewEvery)
	}
	if m.holderId == "" {
		t.Error("holderId must not be empty")
	}
}

func TestLeaderManager_Step_AcquiresThenRenews(t *testing.T) {
	withLeaderStateReset(t)
	db := openLeaderTestDB(t)

	m := &LeaderManager{name: entity.LeaderElectionName, holderId: "holder-A", ttlSeconds: 30}

	if ok := m.step(1000); !ok {
		t.Fatal("expected first step to acquire the lease")
	}
	if !common.IsLeader() {
		t.Error("common.IsLeader() should be true after acquiring")
	}
	if m.localExpiry != 1030 {
		t.Errorf("localExpiry = %d, want 1030", m.localExpiry)
	}

	// Renew as the same holder 5s later.
	if ok := m.step(1005); !ok {
		t.Fatal("expected renew to succeed for the same holder")
	}
	if m.localExpiry != 1035 {
		t.Errorf("localExpiry after renew = %d, want 1035", m.localExpiry)
	}

	var row entity.LeaderElection
	if err := db.Where("name = ?", entity.LeaderElectionName).First(&row).Error; err != nil {
		t.Fatalf("query lease row: %v", err)
	}
	if row.HolderId != "holder-A" || row.RenewedAt != 1005 || row.ExpiresAt != 1035 {
		t.Errorf("lease row = %+v, want holder-A/renewed=1005/expires=1035", row)
	}
}

func TestLeaderManager_Step_LosesToLiveOtherHolder(t *testing.T) {
	withLeaderStateReset(t)
	db := openLeaderTestDB(t)

	if err := db.Create(&entity.LeaderElection{
		Name: entity.LeaderElectionName, HolderId: "other", AcquiredAt: 1, RenewedAt: 1, ExpiresAt: 100000,
	}).Error; err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	m := &LeaderManager{name: entity.LeaderElectionName, holderId: "me", ttlSeconds: 30}
	if ok := m.step(500); ok {
		t.Fatal("expected step to fail while another holder owns a live lease")
	}
	if common.IsLeader() {
		t.Error("common.IsLeader() should remain false when we don't hold the lease")
	}

	var row entity.LeaderElection
	if err := db.Where("name = ?", entity.LeaderElectionName).First(&row).Error; err != nil {
		t.Fatalf("query lease row: %v", err)
	}
	if row.HolderId != "other" {
		t.Errorf("holder_id = %q, want unchanged 'other'", row.HolderId)
	}
}

func TestLeaderManager_Step_TakesOverExpiredLease(t *testing.T) {
	withLeaderStateReset(t)
	db := openLeaderTestDB(t)

	if err := db.Create(&entity.LeaderElection{
		Name: entity.LeaderElectionName, HolderId: "stale-holder", AcquiredAt: 1, RenewedAt: 1, ExpiresAt: 10,
	}).Error; err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	m := &LeaderManager{name: entity.LeaderElectionName, holderId: "fresh-holder", ttlSeconds: 30}
	if ok := m.step(1000); !ok {
		t.Fatal("expected step to take over an expired lease")
	}
	if !common.IsLeader() {
		t.Error("common.IsLeader() should be true after takeover")
	}
	if m.localExpiry != 1030 {
		t.Errorf("localExpiry = %d, want 1030", m.localExpiry)
	}
}

func TestLeaderManager_Step_DBErrorKeepsLeadershipUntilLocalExpiry(t *testing.T) {
	withLeaderStateReset(t)
	openLeaderTestDB(t) // repo.DB must be non-nil even though this path never queries it.

	// Empty name makes repo.TryAcquireOrRenew return an error before touching
	// the DB, exercising the "transient DB error" branch of step().
	m := &LeaderManager{name: "", holderId: "x", ttlSeconds: 30, localExpiry: 2000}
	common.SetLeader(true)

	if ok := m.step(1000); !ok {
		t.Error("expected step to report still-leader while now < localExpiry")
	}
	if !common.IsLeader() {
		t.Error("leadership must be retained until localExpiry lapses")
	}

	if ok := m.step(2000); ok {
		t.Error("expected step to report not-leader once now >= localExpiry")
	}
	if common.IsLeader() {
		t.Error("leadership must be dropped once localExpiry lapses")
	}
}

func TestLeaderManager_IsLeader_ReflectsGlobalFlag(t *testing.T) {
	withLeaderStateReset(t)

	m := &LeaderManager{}
	common.SetLeader(true)
	if !m.IsLeader() {
		t.Error("IsLeader() = false, want true")
	}
	common.SetLeader(false)
	if m.IsLeader() {
		t.Error("IsLeader() = true, want false")
	}
}

func TestLeaderManager_Run_CancelReleasesLeaseWhenLeader(t *testing.T) {
	withLeaderStateReset(t)
	db := openLeaderTestDB(t)

	// Large renewEvery so the ticker never fires during this test.
	m := &LeaderManager{name: entity.LeaderElectionName, holderId: "run-holder", ttlSeconds: 30, renewEvery: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: Run's unconditional initial step still executes first.

	err := m.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() err = %v, want context.Canceled", err)
	}
	if common.IsLeader() {
		t.Error("leadership must be released on shutdown")
	}

	var row entity.LeaderElection
	if err := db.Where("name = ?", entity.LeaderElectionName).First(&row).Error; err != nil {
		t.Fatalf("query lease row: %v", err)
	}
	if row.ExpiresAt != 0 {
		t.Errorf("expires_at = %d, want 0 (released)", row.ExpiresAt)
	}
}

func TestLeaderManager_Run_CancelWhenNotLeader_NoRelease(t *testing.T) {
	withLeaderStateReset(t)
	db := openLeaderTestDB(t)

	// Run() drives step() off the real wall clock (common.GetTimestamp()), so
	// the other holder's lease must be valid against real "now", not a fixed
	// small fixture value.
	farFuture := common.GetTimestamp() + 100000
	if err := db.Create(&entity.LeaderElection{
		Name: entity.LeaderElectionName, HolderId: "other", AcquiredAt: 1, RenewedAt: 1, ExpiresAt: farFuture,
	}).Error; err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	m := &LeaderManager{name: entity.LeaderElectionName, holderId: "loser", ttlSeconds: 30, renewEvery: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() err = %v, want context.Canceled", err)
	}
	if common.IsLeader() {
		t.Error("common.IsLeader() must stay false; we never held the lease")
	}

	var row entity.LeaderElection
	if err := db.Where("name = ?", entity.LeaderElectionName).First(&row).Error; err != nil {
		t.Fatalf("query lease row: %v", err)
	}
	if row.HolderId != "other" || row.ExpiresAt != farFuture {
		t.Errorf("other holder's row must be untouched, got %+v", row)
	}
}

// TestStartAuditCleanupWithContext_LogsIntervalSynchronously exercises the
// synchronous setup portion of the wrapper (interval selection + SysLog),
// which runs on the caller's goroutine before the ticker loop is dispatched.
func TestStartAuditCleanupWithContext_LogsIntervalSynchronously(t *testing.T) {
	withLeaderStateReset(t)
	openCleanupTestDB(t)
	buf := captureSysLog(t)
	common.SetLeader(false) // avoid the leader-gated runAuditCleanup call touching real time-based rows

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartAuditCleanupWithContext(ctx)

	want := "audit retention cleanup started, interval=24h0m0s"
	if got := buf.String(); !strings.Contains(got, want) {
		t.Errorf("log output = %q, want to contain %q", got, want)
	}
}

func TestStartSecretRotationWithContext_LogsIntervalSynchronously(t *testing.T) {
	buf := captureSysLog(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartSecretRotationWithContext(ctx)

	want := "secret rotation started, interval=24h0m0s"
	if got := buf.String(); !strings.Contains(got, want) {
		t.Errorf("log output = %q, want to contain %q", got, want)
	}
}

func TestStartPrivacyErasureWithContext_DefaultInterval(t *testing.T) {
	withLeaderStateReset(t)
	openErasureTestDB(t)
	buf := captureSysLog(t)
	common.SetLeader(false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartPrivacyErasureWithContext(ctx)

	want := "privacy erasure executor started, interval=1m0s"
	if got := buf.String(); !strings.Contains(got, want) {
		t.Errorf("log output = %q, want to contain %q (default interval)", got, want)
	}
}

func TestStartPrivacyErasureWithContext_EnvOverride(t *testing.T) {
	withLeaderStateReset(t)
	openErasureTestDB(t)
	buf := captureSysLog(t)
	common.SetLeader(false)
	t.Setenv("PRIVACY_ERASURE_INTERVAL_SECONDS", "5")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartPrivacyErasureWithContext(ctx)

	want := "privacy erasure executor started, interval=5s"
	if got := buf.String(); !strings.Contains(got, want) {
		t.Errorf("log output = %q, want to contain %q (env override)", got, want)
	}
}

func TestStartPrivacyErasureWithContext_InvalidEnvFallsBackToDefault(t *testing.T) {
	withLeaderStateReset(t)
	openErasureTestDB(t)
	buf := captureSysLog(t)
	common.SetLeader(false)
	t.Setenv("PRIVACY_ERASURE_INTERVAL_SECONDS", "not-a-number")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartPrivacyErasureWithContext(ctx)

	want := "privacy erasure executor started, interval=1m0s"
	if got := buf.String(); !strings.Contains(got, want) {
		t.Errorf("log output = %q, want to contain %q (invalid env should fall back)", got, want)
	}
}
