package lifecycle

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
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

var auditCleanupDBCounter atomic.Int64

func openCleanupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := fmt.Sprintf("file:auditcleanup%d?mode=memory&cache=shared", auditCleanupDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.AuditEvent{}); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("migrate: %v", err)
	}

	prev := repo.DB
	repo.DB = db
	return db, func() {
		repo.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

// TestAuditCleanupInterval pins the env override contract: a positive
// AUDIT_CLEANUP_INTERVAL_SECONDS wins, mirroring
// CREDIT_POOL_RECONCILE_INTERVAL_SECONDS's resolution in
// credit_pool_reconcile.go. Before this change the env var appeared only in
// a doc comment — StartAuditCleanupWithContext always used the 24h default.
func TestAuditCleanupInterval(t *testing.T) {
	t.Setenv("AUDIT_CLEANUP_INTERVAL_SECONDS", "1")
	if got := AuditCleanupInterval(); got != time.Second {
		t.Errorf("AuditCleanupInterval() = %s, want 1s", got)
	}
}

// TestAuditCleanupInterval_DefaultsWhenUnset asserts the 24h fallback for
// both the unset and the invalid/non-positive cases.
func TestAuditCleanupInterval_DefaultsWhenUnset(t *testing.T) {
	for _, raw := range []string{"", "0", "-5", "not-a-number"} {
		t.Setenv("AUDIT_CLEANUP_INTERVAL_SECONDS", raw)
		if got := AuditCleanupInterval(); got != auditCleanupDefaultInterval {
			t.Errorf("AUDIT_CLEANUP_INTERVAL_SECONDS=%q: AuditCleanupInterval() = %s, want default %s", raw, got, auditCleanupDefaultInterval)
		}
	}
}

// TestStartAuditCleanupWithContext_UsesResolvedInterval locks L3 finding #3:
// a regression that reverts StartAuditCleanupWithContext's `interval :=
// AuditCleanupInterval()` back to a hardcoded constant must go red here even
// though TestAuditCleanupInterval above (which calls the resolver directly,
// not through the production entry point) would stay green. Starts the real
// goroutine and reads back the interval it actually plumbed into its ticker.
func TestStartAuditCleanupWithContext_UsesResolvedInterval(t *testing.T) {
	_, cleanup := openCleanupTestDB(t)
	defer cleanup()

	t.Setenv("AUDIT_CLEANUP_INTERVAL_SECONDS", "1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartAuditCleanupWithContext(ctx)

	if got := AuditCleanupActiveInterval(); got != time.Second {
		t.Errorf("StartAuditCleanupWithContext used interval %s, want 1s (AUDIT_CLEANUP_INTERVAL_SECONDS=1)", got)
	}
}

// TestRunAuditCleanup_DeletesExpired exercises the single-tick branch of
// the lifecycle cleanup. Three rows seeded: 2 expired, 1 future. After
// runAuditCleanup the future row must survive.
func TestRunAuditCleanup_DeletesExpired(t *testing.T) {
	db, cleanup := openCleanupTestDB(t)
	defer cleanup()

	// "now" is in the past so we don't have to fake the clock; the
	// production GetTimestamp call inside runAuditCleanup uses wall time.
	// Seed retention_until well in the past for "expired" rows and well
	// in the future for "survives".
	past := int64(1_000_000) // far past
	future := int64(1 << 62) // far future

	seeds := []entity.AuditEvent{
		{Action: "expired-a", Timestamp: past - 100, RetentionUntil: past, TenantID: "default"},
		{Action: "expired-b", Timestamp: past - 100, RetentionUntil: past + 1, TenantID: "default"},
		{Action: "survives", Timestamp: past - 100, RetentionUntil: future, TenantID: "default"},
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seeds[i].Action, err)
		}
	}

	runAuditCleanup(context.Background())

	var remaining []entity.AuditEvent
	if err := db.Find(&remaining).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Action != "survives" {
		t.Errorf("expected only 'survives' to remain, got %+v", remaining)
	}
}

// TestRunAuditCleanup_NoExpired_NoOp confirms the task is a no-op when
// nothing is overdue. Important for the daily ticker — a clean DB should
// not generate log spam or unnecessary DELETE statements.
func TestRunAuditCleanup_NoExpired_NoOp(t *testing.T) {
	db, cleanup := openCleanupTestDB(t)
	defer cleanup()

	future := int64(1 << 62)
	if err := db.Create(&entity.AuditEvent{
		Action: "future", Timestamp: 1, RetentionUntil: future, TenantID: "default",
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Create(&entity.AuditEvent{
		Action: "no-expiry", Timestamp: 1, RetentionUntil: 0, TenantID: "default",
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	runAuditCleanup(context.Background())

	var count int64
	if err := db.Model(&entity.AuditEvent{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 surviving rows, got %d", count)
	}
}

// TestAuditCleanup_SuccessfulTickStampsHeartbeat is the L3 heartbeat oracle:
// a successful runAuditCleanup pass must advance
// metrics.LeaderTaskLastSuccess{task="audit-cleanup"} to "now". Before this
// change audit-cleanup was one of five raw ticker loops with no last-run
// signal at all — this reads the real GaugeVec through the client library,
// not a hand-built stand-in.
func TestAuditCleanup_SuccessfulTickStampsHeartbeat(t *testing.T) {
	_, cleanup := openCleanupTestDB(t)
	defer cleanup()

	before := time.Now().Unix()
	runAuditCleanup(context.Background())
	after := time.Now().Unix()

	got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("audit-cleanup"))
	if got < float64(before) || got > float64(after) {
		t.Errorf("LeaderTaskLastSuccess{task=audit-cleanup} = %v, want within [%d, %d]", got, before, after)
	}
}

// TestAuditCleanup_FailedTickDoesNotStamp: a tick whose delete fails must
// leave the heartbeat exactly where it was — advancing it regardless of
// outcome would hide the very stuck-task condition the gauge exists to
// surface.
func TestAuditCleanup_FailedTickDoesNotStamp(t *testing.T) {
	db, cleanup := openCleanupTestDB(t)
	defer cleanup()

	// Sabotage: drop the audit_events table so DeleteExpiredAuditEvents
	// errors instead of deleting zero rows.
	if err := db.Migrator().DropTable(&entity.AuditEvent{}); err != nil {
		t.Fatalf("drop audit_events: %v", err)
	}

	baseline := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("audit-cleanup"))
	runAuditCleanup(context.Background())
	got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("audit-cleanup"))

	if got != baseline {
		t.Errorf("LeaderTaskLastSuccess{task=audit-cleanup} moved from %v to %v after a failed tick, want unchanged", baseline, got)
	}
}

// TestAuditCleanup_StartRegistersHeartbeat is the A-F1 oracle: the
// boot-time Set(0) and taskreg.Register calls inside
// StartAuditCleanupWithContext are otherwise deletable with every test in
// this package staying green (they only take effect through a goroutine no
// other test observes before it ticks). Drives the real entry point with an
// already-cancelled context — so no tick can race the assertions below —
// and common.IsLeader() forced false so the "run once on startup" branch
// (also gated on IsLeader) cannot fire and restamp the gauge before this
// reads it.
func TestAuditCleanup_StartRegistersHeartbeat(t *testing.T) {
	_, cleanup := openCleanupTestDB(t)
	defer cleanup()

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	// Pre-stamp a distinctive non-zero value so the assertion below cannot
	// pass merely because a GaugeVec's first-ever WithLabelValues access
	// defaults to 0 — it must observe the boot-time Set(0) actively
	// resetting an already-nonzero series.
	metrics.LeaderTaskLastSuccess.WithLabelValues(auditCleanupTaskName).Set(999999999)

	before := len(taskreg.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartAuditCleanupWithContext(ctx)

	if got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues(auditCleanupTaskName)); got != 0 {
		t.Errorf("LeaderTaskLastSuccess{task=audit-cleanup} = %v immediately after StartAuditCleanupWithContext, want 0 (boot-time Set(0) resetting a pre-stamped series)", got)
	}

	snap := taskreg.Snapshot()
	if len(snap) <= before {
		t.Fatalf("taskreg.Snapshot() length did not grow: before=%d after=%d", before, len(snap))
	}
	found := false
	for _, task := range snap {
		if task.Name == auditCleanupTaskName {
			found = true
			if !task.LeaderOnly {
				t.Errorf("%s task.LeaderOnly = false, want true", auditCleanupTaskName)
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q after StartAuditCleanupWithContext", auditCleanupTaskName)
	}
}
