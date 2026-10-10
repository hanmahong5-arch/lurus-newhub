package repo

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// A dropped quota_data bucket used to leave one log line; it must now move
// lurus_quota_data_write_failed_total, and a healthy flush must not.
func TestWriteQuotaDataSnapshot_FailureCounted(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := common.GetTimestamp()
	snap := map[string]*QuotaData{
		"k": {UserID: 9101, Username: "wf-user", ModelName: "model-a", CreatedAt: (now / 3600) * 3600, Count: 1, Quota: 5, TokenUsed: 2},
	}

	// Healthy flush: counter must stay put.
	before := testutil.ToFloat64(metrics.QuotaDataWriteFailedTotal)
	writeQuotaDataSnapshot(context.Background(), snap)
	if got := testutil.ToFloat64(metrics.QuotaDataWriteFailedTotal); got != before {
		t.Fatalf("counter moved on a successful flush: %v -> %v", before, got)
	}

	// Cancelled ctx: the existing-row update branch fails (row now exists
	// from the flush above), as would a create for a fresh bucket.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fresh := map[string]*QuotaData{
		"f": {UserID: 9102, Username: "wf-user2", ModelName: "model-b", CreatedAt: (now / 3600) * 3600, Count: 1, Quota: 5, TokenUsed: 2},
	}
	writeQuotaDataSnapshot(ctx, fresh)
	writeQuotaDataSnapshot(ctx, snap)
	if got := testutil.ToFloat64(metrics.QuotaDataWriteFailedTotal); got < before+1 {
		t.Fatalf("counter %v -> %v, want an increment for the failed bucket write", before, got)
	}
}
