package search

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// A log document that cannot be indexed must move the counter on both the
// pooled path and the pool-not-initialised synchronous fallback; the index
// failure was previously only visible under Debug.
func TestSyncLogAsync_FailureCounted(t *testing.T) {
	srv, _ := withFakeClient(t)
	SyncEnabled = true
	RetryCount = 1
	asyncPool = nil
	srv.Close() // every request now fails

	before := testutil.ToFloat64(metrics.LogSearchSyncFailedTotal)
	SyncLogAsync(sampleLog())
	SyncLogsBatchAsync([]*Log{sampleLog()})
	after := testutil.ToFloat64(metrics.LogSearchSyncFailedTotal)
	if after != before+2 {
		t.Fatalf("counter %v -> %v, want +2 (single + batch)", before, after)
	}
}

func TestSyncLogAsync_SuccessNotCounted(t *testing.T) {
	withFakeClient(t)
	SyncEnabled = true
	RetryCount = 1
	asyncPool = nil

	before := testutil.ToFloat64(metrics.LogSearchSyncFailedTotal)
	SyncLogAsync(sampleLog())
	if after := testutil.ToFloat64(metrics.LogSearchSyncFailedTotal); after != before {
		t.Fatalf("counter moved on success: %v -> %v", before, after)
	}
}
