package app

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"gorm.io/gorm"
)

const (
	outboxActionSettle  = "settle"
	outboxActionRelease = "release"
	outboxMaxRetries    = 10

	outboxStatusPending    = "pending"
	outboxStatusProcessing = "processing"
	outboxStatusDone       = "done"
	outboxStatusFailed     = "failed"

	// outboxClaimLease bounds how long a claimed entry stays owned. A pod killed
	// between the claim and the terminal update would otherwise wedge its entries
	// in "processing" forever — nothing else moves them out of that status. Two
	// orders of magnitude above the 5s per-entry call timeout below, so a live
	// processor is never stolen from.
	outboxClaimLease = 5 * time.Minute
)

// The claim statement. It is a single UPDATE, not a SELECT, because the claim has
// to OUTLIVE the row locks: a bare SELECT ... FOR UPDATE SKIP LOCKED on the root
// handle runs in autocommit, so PostgreSQL releases the locks the instant the
// statement finishes and all three replicas' tickers hand the same entry to
// SettlePreAuthGRPC. Persisting the "processing" status inside the same statement
// moves mutual exclusion onto the row itself, where it survives the tick.
//
// The second WHERE arm re-claims entries whose lease expired (outboxClaimLease).
// Table name mirrors entity.BillingOutbox.TableName() — asserted in the tests.
const (
	outboxClaimHead = `UPDATE billing_outbox SET status = ?, updated_at = ?
WHERE id IN (
	SELECT id FROM billing_outbox
	WHERE (status = ? AND next_retry <= ?) OR (status = ? AND updated_at <= ?)
	ORDER BY next_retry ASC
	LIMIT 50`
	// PostgreSQL-only (runtime is PG-only): peers step over rows another claimer
	// is mid-UPDATE on instead of queueing behind them. The hermetic SQLite tier
	// has no row-level locking dialect — its single writer lock already makes the
	// statement atomic — so the clause is dropped there rather than weakened here.
	outboxClaimLocking = `
	FOR UPDATE SKIP LOCKED`
	outboxClaimTail = `
)
RETURNING *`
)

// billingOutboxDB is set during initialization and used by the outbox worker.
var billingOutboxDB *gorm.DB

// InitBillingOutbox sets the DB handle and auto-migrates the outbox table.
func InitBillingOutbox(db *gorm.DB) error {
	billingOutboxDB = db
	return db.AutoMigrate(&entity.BillingOutbox{})
}

// EnqueueSettle writes a settle action to the outbox for reliable retry.
func EnqueueSettle(accountID, preAuthID int64, amountLB float64) error {
	if billingOutboxDB == nil {
		slog.Error("billing outbox not initialized, settle lost", "preauth_id", preAuthID, "amount", amountLB)
		return fmt.Errorf("billing outbox not initialized")
	}
	entry := entity.BillingOutbox{
		AccountID: accountID,
		PreAuthID: preAuthID,
		Action:    outboxActionSettle,
		AmountLB:  amountLB,
		Status:    outboxStatusPending,
		NextRetry: time.Now(),
	}
	if err := billingOutboxDB.Create(&entry).Error; err != nil {
		slog.Error("billing outbox enqueue settle failed", "preauth_id", preAuthID, "err", err)
		return fmt.Errorf("enqueue settle: %w", err)
	}
	return nil
}

// EnqueueRelease writes a release action to the outbox for reliable retry.
func EnqueueRelease(accountID, preAuthID int64) error {
	if billingOutboxDB == nil {
		slog.Error("billing outbox not initialized, release lost", "preauth_id", preAuthID)
		return fmt.Errorf("billing outbox not initialized")
	}
	entry := entity.BillingOutbox{
		AccountID: accountID,
		PreAuthID: preAuthID,
		Action:    outboxActionRelease,
		AmountLB:  0,
		Status:    outboxStatusPending,
		NextRetry: time.Now(),
	}
	if err := billingOutboxDB.Create(&entry).Error; err != nil {
		slog.Error("billing outbox enqueue release failed", "preauth_id", preAuthID, "err", err)
		return fmt.Errorf("enqueue release: %w", err)
	}
	return nil
}

// claimBillingOutbox flips a bounded batch of due entries to "processing" and
// returns them. Entries it returns are owned by this caller until it writes a
// terminal status or its claim lease expires — no other replica will see them.
func claimBillingOutbox(ctx context.Context, now time.Time) ([]entity.BillingOutbox, error) {
	sql := outboxClaimHead + outboxClaimTail
	if billingOutboxDB.Dialector.Name() == "postgres" {
		sql = outboxClaimHead + outboxClaimLocking + outboxClaimTail
	}

	var entries []entity.BillingOutbox
	err := billingOutboxDB.WithContext(ctx).Raw(sql,
		outboxStatusProcessing, now,
		outboxStatusPending, now,
		outboxStatusProcessing, now.Add(-outboxClaimLease),
	).Scan(&entries).Error
	return entries, err
}

// ProcessBillingOutbox claims due entries and retries them.
// The claim is durable (status="processing"), so the 3 replicas' tickers cannot
// hand the same entry to the platform twice.
func ProcessBillingOutbox(ctx context.Context) error {
	if billingOutboxDB == nil {
		return nil
	}

	// Queue depth = everything not yet terminal. A claimed entry is still
	// outstanding work, so "processing" belongs in the count — otherwise the
	// gauge reads 0 while entries are in flight or wedged mid-retry.
	var pendingCount int64
	billingOutboxDB.Model(&entity.BillingOutbox{}).
		Where("status IN ?", []string{outboxStatusPending, outboxStatusProcessing}).
		Count(&pendingCount)
	metrics.BillingOutboxPending.Set(float64(pendingCount))

	entries, err := claimBillingOutbox(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("query outbox: %w", err)
	}

	for i := range entries {
		entry := &entries[i]
		processCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var err error

		switch entry.Action {
		case outboxActionSettle:
			_, err = common.SettlePreAuthGRPC(processCtx, entry.PreAuthID, entry.AmountLB)
		case outboxActionRelease:
			err = common.ReleasePreAuthGRPC(processCtx, entry.PreAuthID)
		default:
			err = fmt.Errorf("unknown action: %s", entry.Action)
		}
		cancel()

		if err == nil {
			// Atomic update: only mark done if we still hold the claim.
			billingOutboxDB.Model(&entity.BillingOutbox{}).
				Where("id = ? AND status = ?", entry.ID, outboxStatusProcessing).
				Updates(map[string]any{"status": outboxStatusDone, "error": ""})
			slog.Info("billing outbox processed", "id", entry.ID, "action", entry.Action, "preauth_id", entry.PreAuthID)
		} else {
			entry.RetryCount++
			entry.Error = err.Error()
			newStatus := outboxStatusPending
			if entry.RetryCount >= outboxMaxRetries {
				newStatus = outboxStatusFailed
				metrics.BillingOutboxFailedTotal.Inc()
				slog.Error("billing outbox permanently failed", "id", entry.ID, "action", entry.Action, "preauth_id", entry.PreAuthID, "err", err)
			} else {
				backoff := time.Duration(math.Pow(2, float64(entry.RetryCount))) * 5 * time.Second
				entry.NextRetry = time.Now().Add(backoff)
				slog.Warn("billing outbox retry scheduled", "id", entry.ID, "retry", entry.RetryCount, "next", entry.NextRetry, "err", err)
			}
			// Atomic update: only update if we still hold the claim
			billingOutboxDB.Model(&entity.BillingOutbox{}).
				Where("id = ? AND status = ?", entry.ID, outboxStatusProcessing).
				Updates(map[string]any{
					"retry_count": entry.RetryCount,
					"next_retry":  entry.NextRetry,
					"status":      newStatus,
					"error":       entry.Error,
				})
		}
	}

	return nil
}
