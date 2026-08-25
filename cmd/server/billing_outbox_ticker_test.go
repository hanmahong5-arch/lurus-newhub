package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestRunBillingOutboxLoop_PicksUpRuntimeToggle covers defect (2): the
// unified-billing toggle is flipped from the platform console and reaches the pod
// through StartBillingConfigPoller, so the drain has to notice mid-run. The loop
// here is started ONCE, while the toggle is off, and is never restarted — the
// same loop must be idle before the flip and working after it.
func TestRunBillingOutboxLoop_PicksUpRuntimeToggle(t *testing.T) {
	db := newOutboxTestDB(t)
	if err := app.InitBillingOutbox(db); err != nil {
		t.Fatalf("InitBillingOutbox: %v", err)
	}

	// An unknown action fails locally, so the drain needs no platform backend:
	// "work happened" shows up as retry_count moving off 0.
	entry := entity.BillingOutbox{
		AccountID: 1, PreAuthID: 909, Action: "bogus",
		Status: "pending", NextRetry: time.Now().Add(-time.Minute),
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("seed outbox entry: %v", err)
	}

	prev := common.BillingUnifiedEnabled()
	t.Cleanup(func() { common.SetBillingUnifiedEnabled(prev) })
	common.SetBillingUnifiedEnabled(false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runBillingOutboxLoop(ctx, 5*time.Millisecond) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("runBillingOutboxLoop did not return after ctx cancel")
		}
	})

	// Toggle off: many ticks, no work.
	time.Sleep(100 * time.Millisecond)
	if got := outboxRetryCount(t, db, entry.ID); got != 0 {
		t.Fatalf("retry_count = %d while unified billing is off, want 0 (tick must be a no-op)", got)
	}

	// Flip it on with the loop already running — no restart.
	common.SetBillingUnifiedEnabled(true)
	if err := waitFor(2*time.Second, func() bool { return outboxRetryCount(t, db, entry.ID) == 1 }); err != nil {
		t.Fatalf("outbox never drained after the toggle flipped on: %v (retry_count=%d)",
			err, outboxRetryCount(t, db, entry.ID))
	}
}

func outboxRetryCount(t *testing.T, db *gorm.DB, id int64) int {
	t.Helper()
	var row entity.BillingOutbox
	if err := db.First(&row, id).Error; err != nil {
		t.Fatalf("reload outbox row %d: %v", id, err)
	}
	return row.RetryCount
}

func waitFor(timeout time.Duration, cond func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("condition still false after %s", timeout)
}

// newOutboxTestDB opens a named shared-cache in-memory SQLite database pinned to
// one connection — see internal/app/testutil_test.go for why a bare ":memory:"
// hands every connection its own empty database.
func newOutboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:outboxticker%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}
