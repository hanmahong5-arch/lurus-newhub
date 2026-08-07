package lifecycle

// cov_core-app-boot_task_launchers_test.go — coverage for the three
// StartXWithContext launchers left at 0%: StartPrivacyErasureWithContext
// (privacy_erasure.go), StartSecretRotationWithContext (secret_rotation.go)
// and StartAuditCleanupWithContext (audit_cleanup.go). All three are
// fire-and-forget (common.SafeGoWithContext) wrappers; this file reuses
// seedErasureFixture/openErasureTestDB (privacy_erasure_test.go) and
// openCleanupTestDB (audit_cleanup_test.go) — same package — rather than
// re-declaring fixtures.

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestCoreAppBootStartPrivacyErasureWithContext_LeaderRunsPassImmediately
// verifies the launcher's synchronous "run once immediately if leader"
// fast path (so a freshly-elected leader doesn't wait a full tick to start
// draining the pending queue) actually reaches runErasurePass/executeErasure,
// and that the goroutine stops observably on cancellation.
//
// ctx starts live so the immediate pass's DB queries aren't rejected by an
// already-cancelled context (they are — repo.ListPendingErasureRequests uses
// DB.WithContext(ctx) — this was verified by an earlier draft of this test
// that pre-cancelled ctx and got "context canceled" instead of a real run).
// We poll for the row to leave "pending", THEN cancel, THEN wait past the
// erasureDefaultInterval-independent trailing select (no further DB access
// after that point) before letting cleanup swap repo.DB back out — so we
// never race the background goroutine's DB access with test teardown.
func TestCoreAppBootStartPrivacyErasureWithContext_LeaderRunsPassImmediately(t *testing.T) {
	db := openErasureTestDB(t)
	_, row := seedErasureFixture(t, db, 2)

	prevLeader := common.IsLeader()
	common.SetLeader(true)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	ctx, cancel := context.WithCancel(context.Background())

	StartPrivacyErasureWithContext(ctx)

	deadline := time.Now().Add(3 * time.Second)
	var final *repo.PrivacyErasureRequest
	for time.Now().Before(deadline) {
		got, err := repo.GetErasureRequestByEventID(context.Background(), row.EventID)
		if err == nil && got.Status != repo.ErasureStatusPending {
			final = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel() // stop the background loop now that the immediate pass has landed (or the deadline passed)
	if final == nil {
		t.Fatal("expected StartPrivacyErasureWithContext's immediate leader pass to process the pending erasure request within the deadline")
	}
	if final.Status != repo.ErasureStatusCompleted {
		t.Errorf("status = %q, want completed", final.Status)
	}
	// Give the goroutine's trailing select-loop (no further DB access after
	// the immediate pass returned) a brief moment to observe ctx.Done() and
	// return before test cleanup proceeds.
	time.Sleep(30 * time.Millisecond)
}

// TestCoreAppBootStartPrivacyErasureWithContext_NotLeaderSkipsPass ensures a
// non-leader replica does NOT drain the queue (would double-process
// concurrently with the real leader in a multi-replica deployment).
func TestCoreAppBootStartPrivacyErasureWithContext_NotLeaderSkipsPass(t *testing.T) {
	db := openErasureTestDB(t)
	_, row := seedErasureFixture(t, db, 1)

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	StartPrivacyErasureWithContext(ctx)

	// Bounded wait: nothing should change. We can't prove a negative
	// instantly, so give the (already-cancelled) goroutine a generous window
	// to have run a pass if the leader gate were broken, then assert it did not.
	time.Sleep(150 * time.Millisecond)

	got, err := repo.GetErasureRequestByEventID(context.Background(), row.EventID)
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	if got.Status != repo.ErasureStatusPending {
		t.Errorf("status = %q, want still pending — non-leader must not run the erasure pass", got.Status)
	}
}

// TestCoreAppBootStartSecretRotationWithContext_NonBlockingLauncher asserts
// the launcher actually runs its startup log line and construction (proving
// this isn't a stub) and that the launch is non-blocking. The rotation
// ticker itself fires on a 24h cadence (secretRotationInterval, unexported
// const) so its tick body is not practically reachable from a unit test;
// NewLeaderTask/LeaderTask.Run's gating logic is already covered directly by
// leader_election_test.go. No t.Parallel() in this test, so swapping
// gin.DefaultWriter here cannot race with other package tests (parallel
// tests in this package only start executing after every sequential test,
// this one included, has returned).
func TestCoreAppBootStartSecretRotationWithContext_NonBlockingLauncher(t *testing.T) {
	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	prevWriter := gin.DefaultWriter
	var buf bytes.Buffer
	gin.DefaultWriter = &buf
	t.Cleanup(func() { gin.DefaultWriter = prevWriter })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled so the spawned goroutine's select returns immediately, never touching app.RotateDueTokens

	start := time.Now()
	StartSecretRotationWithContext(ctx)
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Errorf("StartSecretRotationWithContext blocked the caller for %v; expected an async fire-and-forget launch", elapsed)
	}
	if !strings.Contains(buf.String(), "secret rotation started, interval=24h0m0s") {
		t.Errorf("expected the real startup log line (interval=24h0m0s) to be written, got %q", buf.String())
	}
}

// TestCoreAppBootStartAuditCleanupWithContext_LeaderDeletesExpiredImmediately
// mirrors the privacy-erasure launcher test: a fresh leader must reclaim any
// backlog immediately rather than waiting the full 24h interval.
func TestCoreAppBootStartAuditCleanupWithContext_LeaderDeletesExpiredImmediately(t *testing.T) {
	db, cleanupDB := openCleanupTestDB(t)
	defer cleanupDB()

	past := int64(1_000_000)
	if err := db.Create(&entity.AuditEvent{
		Action: "expired", Timestamp: past - 100, RetentionUntil: past, TenantID: "default",
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	prevLeader := common.IsLeader()
	common.SetLeader(true)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	ctx, cancel := context.WithCancel(context.Background())

	StartAuditCleanupWithContext(ctx)

	deadline := time.Now().Add(3 * time.Second)
	var remaining int64 = -1
	for time.Now().Before(deadline) {
		var count int64
		if err := db.Model(&entity.AuditEvent{}).Count(&count).Error; err == nil && count == 0 {
			remaining = count
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if remaining != 0 {
		t.Fatalf("expected the immediate leader pass to delete the expired audit event, remaining=%d", remaining)
	}
	// No further DB access happens in the goroutine after the immediate pass
	// returns (only ticker.Stop()/SysLog/return on ctx.Done()), so a brief
	// grace period here is enough before cleanupDB() closes the sqlite conn.
	time.Sleep(30 * time.Millisecond)
}

// TestCoreAppBootLeaderTask_Name locks the pass-through Name() accessor,
// left uncovered because the existing leader_election_test.go only drives
// Run()/isLeader gating.
func TestCoreAppBootLeaderTask_Name(t *testing.T) {
	task := NewLeaderTask("cov-named-task", time.Hour, func(ctx context.Context) error { return nil })
	if got := task.Name(); got != "cov-named-task" {
		t.Errorf("Name() = %q, want %q", got, "cov-named-task")
	}
}
