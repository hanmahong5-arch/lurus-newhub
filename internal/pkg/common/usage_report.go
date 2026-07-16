package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// usageEventsPath is the platform's generic per-account usage ingest endpoint
// (scope usage:report). Dedup happens platform-side via the partial unique
// index on (product_id, idempotency_key) — migration 045 — so a retried or
// double-fired report is a safe no-op.
const usageEventsPath = "/internal/v1/usage/events"

// ReportUsageEvent mirrors one relay consumption into the platform's
// billing.usage_events table (metric=llm_relay). This is METERING, not money:
// the wallet settle/debit moves the funds; this row is the independent record
// the Track A drift reconciliation joins against wallet_transactions
// (usage > wallet ⇒ missed charge, wallet > usage ⇒ double charge).
//
// Fire-and-forget contract: callers run this inside AsyncGo with a bounded
// ctx; a failure only loses this reconciliation data point (counted by
// metrics.BillingUsageMirrorTotal at the call site), it never blocks or
// fails the relay/settlement.
func ReportUsageEvent(ctx context.Context, accountID int64, productID, metric string, quantity int64, idempotencyKey string, metadata map[string]any) error {
	if IdentityServiceURL == "" {
		return fmt.Errorf("billing service not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"account_id":      accountID,
		"product_id":      productID,
		"metric":          metric,
		"quantity":        quantity,
		"occurred_at":     time.Now().UTC(),
		"idempotency_key": idempotencyKey,
		"metadata":        metadata,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, IdentityServiceURL+usageEventsPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("usage report request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		return fmt.Errorf("usage report: billing service unreachable")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		reason := parseErrorResponse(resp.Body)
		SysLog(fmt.Sprintf("usage report failed: account=%d, status=%d, reason=%s", accountID, resp.StatusCode, reason))
		return fmt.Errorf("usage report failed: %s", reason)
	}
	return nil
}
