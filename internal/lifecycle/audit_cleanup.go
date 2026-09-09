package lifecycle

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// auditCleanupBatchSize bounds the per-DELETE row count so a long-overdue
// cleanup doesn't lock the audit_events table in one giant transaction.
// 500 matches DeleteOldLog's batching convention.
const auditCleanupBatchSize = 500

// auditCleanupDefaultInterval is the wall-clock period between cleanup
// passes when AUDIT_CLEANUP_INTERVAL_SECONDS is unset. Daily is enough —
// retention deadlines are at-second precision but operators set them in
// years, so sub-day enforcement granularity carries no business value.
const auditCleanupDefaultInterval = 24 * time.Hour

// AuditCleanupInterval resolves the sweep period: AUDIT_CLEANUP_INTERVAL_SECONDS
// when set to a positive integer, else auditCleanupDefaultInterval. Mirrors
// CreditPoolReconcile's CREDIT_POOL_RECONCILE_INTERVAL_SECONDS resolution
// (credit_pool_reconcile.go) so the two operator-tunable background tasks
// behave the same way. Exported for the lifecycle test suite; production has
// one call site (StartAuditCleanupWithContext below).
func AuditCleanupInterval() time.Duration {
	if raw := os.Getenv("AUDIT_CLEANUP_INTERVAL_SECONDS"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return auditCleanupDefaultInterval
}

// auditCleanupActiveInterval records the interval the most recently started
// StartAuditCleanupWithContext goroutine is actually ticking on. A test that
// only called AuditCleanupInterval() directly could not tell whether the
// production entry point actually plumbed the result into its ticker — this
// lets a test start the real goroutine and read back what it resolved to.
var auditCleanupActiveInterval atomic.Int64 // nanoseconds

// AuditCleanupActiveInterval returns the interval most recently used by
// StartAuditCleanupWithContext, for tests.
func AuditCleanupActiveInterval() time.Duration {
	return time.Duration(auditCleanupActiveInterval.Load())
}

// StartAuditCleanupWithContext launches the daily audit retention sweep.
// On each tick it deletes audit_events rows whose retention_until has
// passed; rows with retention_until = 0 are preserved (treated as
// "no expiry"). The goroutine exits when ctx is cancelled.
//
// Phase E3: this enforces the per-row 7y default that
// governance.NewAuditEvent assigns, plus any shorter or longer
// retention_until callers override after construction.
func StartAuditCleanupWithContext(ctx context.Context) {
	interval := AuditCleanupInterval()
	auditCleanupActiveInterval.Store(int64(interval))
	common.SysLog(fmt.Sprintf("audit retention cleanup started, interval=%s", interval))

	ticker := time.NewTicker(interval)
	common.SafeGoWithContext(ctx, func(c context.Context) {
		defer ticker.Stop()
		// Run once on startup so a fresh deploy reclaims any backlog
		// without waiting a full day — but only if this node already holds
		// leadership at boot.
		if common.IsLeader() {
			runAuditCleanup(c)
		}
		for {
			select {
			case <-c.Done():
				common.SysLog("audit retention cleanup stopped")
				return
			case <-ticker.C:
				// HA: only the leader runs retention deletes; non-leaders idle.
				if !common.IsLeader() {
					continue
				}
				runAuditCleanup(c)
			}
		}
	})
}

// runAuditCleanup executes one pass. Errors are logged but do not stop
// the ticker — a transient DB hiccup should not silently disable
// retention enforcement.
func runAuditCleanup(ctx context.Context) {
	now := common.GetTimestamp()
	deleted, err := repo.DeleteExpiredAuditEvents(ctx, now, auditCleanupBatchSize)
	if err != nil {
		common.SysError(fmt.Sprintf("audit cleanup: delete expired events: %v", err))
		return
	}
	if deleted > 0 {
		common.SysLog(fmt.Sprintf("audit cleanup: deleted %d expired events", deleted))
	}
}
