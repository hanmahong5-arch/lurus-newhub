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
