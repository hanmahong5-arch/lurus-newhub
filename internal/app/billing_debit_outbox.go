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
)

// EnqueueDebit parks a direct wallet debit whose first attempt failed, for the
// outbox worker to retry under the same idempotency reference.
//
// Before this existed the legacy branch logged the failure and moved on, so a
// platform outage turned every request it admitted (high-balance accounts that
// skip pre-auth, and requests admitted on a cached balance while the billing
// breaker is open) into usage nobody was ever charged for.
func EnqueueDebit(accountID int64, amountLB float64, txType, description, productID, refID string) error {
	if billingOutboxDB == nil {
		return fmt.Errorf("billing outbox not initialized")
	}
	entry := entity.BillingDebitOutbox{
		AccountID:   accountID,
		RefID:       refID,
		AmountLB:    amountLB,
		TxType:      txType,
		Description: description,
		ProductID:   productID,
		Status:      outboxStatusPending,
		NextRetry:   time.Now(),
	}
	if err := billingOutboxDB.Create(&entry).Error; err != nil {
		return fmt.Errorf("enqueue debit: %w", err)
	}
	return nil
}

// enqueueFailedLegacyDebit is the legacy branch's failure arm: park the debit,
// and only if that fails too say loudly that money was lost.
func enqueueFailedLegacyDebit(accountID int64, amountLB float64, description, productID, refID string, debitErr error) {
	common.SysLog(fmt.Sprintf("legacy wallet debit failed, enqueuing: accountID=%d, amount=%.4f LB, ref=%s, err=%s",
		accountID, amountLB, refID, debitErr.Error()))
	if enqErr := EnqueueDebit(accountID, amountLB, "llm_usage", description, productID, refID); enqErr != nil {
		common.SysError(fmt.Sprintf("CRITICAL: wallet debit %s of %.4f LB for account %d failed (%s) and could not be enqueued (%s); this usage is unbilled",
			refID, amountLB, accountID, debitErr.Error(), enqErr.Error()))
	}
}

// processDebitOutbox retries parked debits. Same claim, lease and backoff as
// the settle/release outbox; the platform deduplicates on RefID, so a retry of
// a debit that did land the first time is a no-op there.
func processDebitOutbox(ctx context.Context) {
	var entries []entity.BillingDebitOutbox
	if err := claimOutboxRows(ctx, entity.BillingDebitOutbox{}.TableName(), time.Now(), &entries); err != nil {
		slog.Error("billing debit outbox claim failed", "err", err)
		return
	}
	for i := range entries {
		e := &entries[i]
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := debitWalletGRPC(callCtx, e.AccountID, e.AmountLB, e.TxType, e.Description, e.ProductID, e.RefID)
		cancel()

		updates := map[string]any{"status": outboxStatusDone, "error": ""}
		if err == nil {
			// DebitWalletGRPC records billing_debit_amount_cny itself.
			common.InvalidateCachedWalletBalance(e.AccountID) //nolint:contextcheck // takes no ctx; a cache delete, same as the settle path
		} else {
			e.RetryCount++
			updates = map[string]any{"retry_count": e.RetryCount, "status": outboxStatusPending, "error": err.Error()}
			if e.RetryCount >= outboxMaxRetries {
				updates["status"] = outboxStatusFailed
				metrics.BillingOutboxFailedTotal.Inc()
				slog.Error("billing debit outbox permanently failed", "id", e.ID, "ref", e.RefID, "amount_lb", e.AmountLB, "err", err)
			} else {
				updates["next_retry"] = time.Now().Add(time.Duration(math.Pow(2, float64(e.RetryCount))) * 5 * time.Second)
			}
		}
		// Only if the claim is still ours.
		billingOutboxDB.Model(&entity.BillingDebitOutbox{}).
			Where("id = ? AND status = ?", e.ID, outboxStatusProcessing).
			Updates(updates)
	}
}
