package app

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// The platform now answers a settle/release with 404 (no such hold) or 409
// (the hold is in a state this operation cannot reach: released, expired,
// settled for another amount). Neither can ever succeed on retry, so the
// outbox must file them as failed on the FIRST pass — and count a settle as
// money lost right then — instead of burning ten retries (~25 min) before
// saying so. A 400 stays retryable: the platform's HTTP fallback still maps
// a transient DB failure to 400 "Invalid request".

func setupOutboxWithSeams(t *testing.T) {
	t.Helper()
	db := setupServiceTestDB(t)
	if err := InitBillingOutbox(db); err != nil {
		t.Fatalf("init outbox: %v", err)
	}
	prev := billingOutboxDB
	t.Cleanup(func() { billingOutboxDB = prev })
	prevS, prevR := settleOutboxCall, releaseOutboxCall
	t.Cleanup(func() { settleOutboxCall, releaseOutboxCall = prevS, prevR })
}

func outboxRow(t *testing.T) entity.BillingOutbox {
	t.Helper()
	var rows []entity.BillingOutbox
	billingOutboxDB.Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("outbox rows = %d, want 1", len(rows))
	}
	return rows[0]
}

func TestOutbox_Settle409_IsTerminalOnFirstPassAndCountsMoneyLost(t *testing.T) {
	setupOutboxWithSeams(t)
	calls := 0
	settleOutboxCall = func(context.Context, int64, float64) (*common.SettlePreAuthResult, error) {
		calls++
		return nil, &common.PlatformRejectedError{Op: "settle", Status: http.StatusConflict, Reason: "pre-auth is already settled or released"}
	}
	if err := EnqueueSettle(57, 9001, 0.25); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	before := testutil.ToFloat64(metrics.BillingMoneyPathLostTotal.WithLabelValues("settle"))
	failedBefore := testutil.ToFloat64(metrics.BillingOutboxFailedTotal)

	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("ProcessBillingOutbox: %v", err)
	}
	row := outboxRow(t)
	if row.Status != outboxStatusFailed {
		t.Fatalf("status = %q after a 409, want %q on the first pass (retry can never succeed)", row.Status, outboxStatusFailed)
	}
	if row.RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", row.RetryCount)
	}
	if delta := testutil.ToFloat64(metrics.BillingMoneyPathLostTotal.WithLabelValues("settle")) - before; delta != 1 {
		t.Errorf("money_path_lost{settle} delta = %v, want 1", delta)
	}
	if delta := testutil.ToFloat64(metrics.BillingOutboxFailedTotal) - failedBefore; delta != 1 {
		t.Errorf("outbox_failed delta = %v, want 1", delta)
	}
	// A second sweep must not pick it up again.
	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("second ProcessBillingOutbox: %v", err)
	}
	if calls != 1 {
		t.Errorf("platform calls = %d, want 1 — a terminal entry was retried", calls)
	}
}

func TestOutbox_Release404_IsTerminalOnFirstPass(t *testing.T) {
	setupOutboxWithSeams(t)
	releaseOutboxCall = func(context.Context, int64) error {
		return &common.PlatformRejectedError{Op: "release", Status: http.StatusNotFound, Reason: "Pre-auth not found"}
	}
	if err := EnqueueRelease(57, 9002); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	before := testutil.ToFloat64(metrics.BillingMoneyPathLostTotal.WithLabelValues("settle"))

	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("ProcessBillingOutbox: %v", err)
	}
	row := outboxRow(t)
	if row.Status != outboxStatusFailed || row.RetryCount != 1 {
		t.Fatalf("row = status %q retry %d, want failed/1", row.Status, row.RetryCount)
	}
	if delta := testutil.ToFloat64(metrics.BillingMoneyPathLostTotal.WithLabelValues("settle")) - before; delta != 0 {
		t.Errorf("a refused RELEASE is not lost revenue; money_path_lost{settle} delta = %v, want 0", delta)
	}
}

func TestOutbox_Settle400_StaysRetryable(t *testing.T) {
	setupOutboxWithSeams(t)
	settleOutboxCall = func(context.Context, int64, float64) (*common.SettlePreAuthResult, error) {
		return nil, &common.PlatformRejectedError{Op: "settle", Status: http.StatusBadRequest, Reason: "Invalid request"}
	}
	if err := EnqueueSettle(57, 9003, 0.25); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("ProcessBillingOutbox: %v", err)
	}
	row := outboxRow(t)
	if row.Status != outboxStatusPending || row.RetryCount != 1 {
		t.Fatalf("row = status %q retry %d, want pending/1 (a 400 may be the platform's own DB hiccup)", row.Status, row.RetryCount)
	}
}

func TestOutbox_UntypedError_StaysRetryable(t *testing.T) {
	setupOutboxWithSeams(t)
	settleOutboxCall = func(context.Context, int64, float64) (*common.SettlePreAuthResult, error) {
		return nil, errors.New("settle: billing service unreachable")
	}
	if err := EnqueueSettle(57, 9004, 0.25); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("ProcessBillingOutbox: %v", err)
	}
	if row := outboxRow(t); row.Status != outboxStatusPending {
		t.Fatalf("status = %q, want pending", row.Status)
	}
}
