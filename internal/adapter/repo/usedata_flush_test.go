package repo

// usedata_flush_test.go — oracle for cycle 13 L11: UpdateQuotaDataWithContext
// must flush the in-memory quota_data cache to the DB when its context is
// cancelled (shutdown), not just log and drop it on the floor.

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestUpdateQuotaDataWithContext_FlushesOnCancel locks the ctx.Done() arm:
// a bucket logged before shutdown must actually land in quota_data once the
// context is cancelled, not stay stranded in the in-memory cache.
func TestUpdateQuotaDataWithContext_FlushesOnCancel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	prevEnabled := common.DataExportEnabled
	prevInterval := common.DataExportInterval
	common.DataExportEnabled = true
	common.DataExportInterval = 60 // minutes — long enough the ticker never fires during this test
	defer func() {
		common.DataExportEnabled = prevEnabled
		common.DataExportInterval = prevInterval
	}()

	// Start from a clean cache: a leftover bucket from another test running
	// earlier in this process would let this test pass even with the flush
	// deleted, since SaveQuotaDataCache (called elsewhere) might have
	// already written something with a matching key.
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	const userID = 8842
	now := common.GetTimestamp()
	LogQuotaData(userID, "drain-flush-user", "gpt-4", 321, now, 111)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		UpdateQuotaDataWithContext(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("UpdateQuotaDataWithContext did not return after ctx cancellation")
	}

	hourStart := (now / 3600) * 3600
	data, err := GetQuotaDataByUserId(userID, hourStart-3600, hourStart+7200)
	if err != nil {
		t.Fatalf("GetQuotaDataByUserId: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected the logged quota_data bucket to be flushed to the DB on ctx cancellation, found none")
	}
	if data[0].Quota != 321 {
		t.Errorf("flushed row Quota = %d, want 321", data[0].Quota)
	}

	// The in-memory cache must be empty after the flush — SaveQuotaDataCache
	// resets it, and a leftover entry would mean a *different* code path
	// wrote the row (or none was cleared), not the ctx.Done() arm.
	CacheQuotaDataLock.Lock()
	remaining := len(CacheQuotaData)
	CacheQuotaDataLock.Unlock()
	if remaining != 0 {
		t.Errorf("CacheQuotaData still has %d entr(ies) after flush, want 0", remaining)
	}
}

// TestUpdateQuotaDataWithContext_SkipsFlushWhenDataExportDisabled proves the
// ctx.Done() flush still respects DataExportEnabled, the same guard the
// ticker branch already uses — the fix must not turn this into an
// unconditional write.
func TestUpdateQuotaDataWithContext_SkipsFlushWhenDataExportDisabled(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	prevEnabled := common.DataExportEnabled
	prevInterval := common.DataExportInterval
	common.DataExportEnabled = false
	common.DataExportInterval = 60
	defer func() {
		common.DataExportEnabled = prevEnabled
		common.DataExportInterval = prevInterval
	}()

	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	const userID = 8843
	now := common.GetTimestamp()
	LogQuotaData(userID, "drain-flush-disabled-user", "gpt-4", 654, now, 222)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		UpdateQuotaDataWithContext(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("UpdateQuotaDataWithContext did not return after ctx cancellation")
	}

	hourStart := (now / 3600) * 3600
	data, err := GetQuotaDataByUserId(userID, hourStart-3600, hourStart+7200)
	if err != nil {
		t.Fatalf("GetQuotaDataByUserId: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected no flush while DataExportEnabled=false, found %d row(s)", len(data))
	}
}

// TestWriteQuotaDataSnapshot_DoesNotHoldCacheLock locks the drain-window
// contention fix (cycle 13 L11 repair, D-L11-4). The shutdown flush runs
// while the pod is still serving whatever was in flight when SIGTERM
// arrived, and every completing relay request calls LogQuotaData, which
// takes CacheQuotaDataLock. If the writer held that lock across its DB round
// trips, a slow PG would stall those requests for the whole flush and push
// the drain past terminationGracePeriodSeconds. So the cache is snapshotted
// and cleared under the lock, and the writes happen with the lock released —
// which this test proves by holding the lock itself while the writer runs.
func TestWriteQuotaDataSnapshot_DoesNotHoldCacheLock(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	const userID = 8844
	hourStart := (common.GetTimestamp() / 3600) * 3600
	snapshot := map[string]*QuotaData{
		"lock-probe": {
			UserID:    userID,
			Username:  "drain-lock-user",
			ModelName: "gpt-4",
			CreatedAt: hourStart,
			Count:     1,
			Quota:     42,
			TokenUsed: 7,
		},
	}

	CacheQuotaDataLock.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			CacheQuotaDataLock.Unlock()
		}
	}()

	done := make(chan struct{})
	go func() {
		writeQuotaDataSnapshot(context.Background(), snapshot)
		close(done)
	}()

	timedOut := false
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		timedOut = true
	}
	CacheQuotaDataLock.Unlock()
	unlocked = true
	if timedOut {
		// Release first, then wait: a writer still blocked on the lock has
		// to finish before the sqlite fixture is torn down under it.
		<-done
		t.Fatal("writeQuotaDataSnapshot did not finish while CacheQuotaDataLock was held by this test: the flush still writes under the lock, so a relay request completing during the drain would block on its DB round trips")
	}

	data, err := GetQuotaDataByUserId(userID, hourStart-3600, hourStart+7200)
	if err != nil {
		t.Fatalf("GetQuotaDataByUserId: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("expected the snapshot row to be written, found %d row(s)", len(data))
	}
	if data[0].Quota != 42 {
		t.Errorf("written row Quota = %d, want 42", data[0].Quota)
	}
}

// TestWriteQuotaDataSnapshot_HonorsContextCancellation proves the flush's
// bounded context actually reaches the DB calls: the shutdown path gives the
// writer its own 10s deadline (quotaDataShutdownFlushTimeout) precisely so a
// hung PG cannot hold the drain open past terminationGracePeriodSeconds, and
// that deadline is worth nothing if the queries do not carry it. An
// already-cancelled context is the observable form of "the deadline has
// passed": nothing may be written.
func TestWriteQuotaDataSnapshot_HonorsContextCancellation(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	const userID = 8845
	hourStart := (common.GetTimestamp() / 3600) * 3600
	snapshot := map[string]*QuotaData{
		"ctx-probe": {
			UserID:    userID,
			Username:  "drain-ctx-user",
			ModelName: "gpt-4",
			CreatedAt: hourStart,
			Count:     1,
			Quota:     99,
			TokenUsed: 3,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	writeQuotaDataSnapshot(ctx, snapshot)

	data, err := GetQuotaDataByUserId(userID, hourStart-3600, hourStart+7200)
	if err != nil {
		t.Fatalf("GetQuotaDataByUserId: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected no write under an expired flush deadline, found %d row(s)", len(data))
	}
}

// TestQuotaDataShutdownFlushTimeout_IsBounded pins the shutdown flush budget
// itself: the value has to leave room inside terminationGracePeriodSeconds
// (90s after this cycle's manifest change) after GRACEFUL_SHUTDOWN_TIMEOUT
// has already been spent, so it is deliberately small.
func TestQuotaDataShutdownFlushTimeout_IsBounded(t *testing.T) {
	if quotaDataShutdownFlushTimeout <= 0 {
		t.Fatalf("quotaDataShutdownFlushTimeout = %v, want a positive bound", quotaDataShutdownFlushTimeout)
	}
	if quotaDataShutdownFlushTimeout > 15*time.Second {
		t.Errorf("quotaDataShutdownFlushTimeout = %v, want <= 15s so the flush cannot eat the shutdown grace period", quotaDataShutdownFlushTimeout)
	}
}
