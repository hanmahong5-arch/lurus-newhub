package nats

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestPublishPoolThreshold_DedupReadErrorPropagates covers the switch `default:`
// branch: when the schema dedup read fails with an error that is NOT
// gorm.ErrRecordNotFound, the function must abort and propagate a wrapped
// error — and must NOT mark the slot or publish (fail-closed on an unknown DB
// state, so a transient read blip can't silently drop or double-fire alerts).
func TestPublishPoolThreshold_DedupReadErrorPropagates(t *testing.T) {
	rdb := newMockPoolRedis()
	pub := &mockPoolPublisher{}
	db := &mockPoolDB{readErr: errors.New("connection reset by peer")}

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-RE", 77, 10, 1000, 80,
		pub, rdb, db, time.Now().UTC(),
	)
	if err == nil {
		t.Fatal("expected error when the dedup read fails, got nil")
	}
	if !strings.Contains(err.Error(), "pool threshold dedup read") {
		t.Errorf("error should wrap the dedup read failure, got %q", err.Error())
	}
	if !errors.Is(err, db.readErr) {
		t.Errorf("wrapped error must preserve the underlying cause via %%w; got %v", err)
	}
	if pub.count() != 0 {
		t.Errorf("read failure must not publish, got %d", pub.count())
	}
	if db.markCount() != 0 {
		t.Errorf("read failure must not mark the slot, got %d marks", db.markCount())
	}
}

// TestPublishPoolThreshold_RecordNotFoundFallsThrough proves the sibling arm of
// the switch: a gorm.ErrRecordNotFound (pool row absent) is treated as
// "never fired" and publication proceeds normally.
func TestPublishPoolThreshold_RecordNotFoundFallsThrough(t *testing.T) {
	rdb := newMockPoolRedis()
	pub := &mockPoolPublisher{}
	db := &mockPoolDB{readErr: gorm.ErrRecordNotFound}

	_, _, err := publishPoolThreshold(
		context.Background(),
		"tenant-NF", 88, 10, 1000, 80,
		pub, rdb, db, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("record-not-found must be treated as never-fired, got error: %v", err)
	}
	if pub.count() != 1 {
		t.Errorf("expected publication to proceed on missing row, got %d", pub.count())
	}
}

// TestGormPoolDB_LastAlertFiredAt_ScanError exercises the error return of the
// real GORM query path: when the underlying table does not exist the Scan
// fails, and LastAlertFiredAt must surface that error (not swallow it as a
// nil timestamp — that would let a broken DB masquerade as "never fired").
func TestGormPoolDB_LastAlertFiredAt_ScanError(t *testing.T) {
	// Fresh in-memory DB with NO tenant_credit_pools table migrated.
	db, err := gorm.Open(sqlite.Open("file:natspoolthresholdscanerr?mode=memory&cache=private"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	g := &gormPoolDB{db: db}

	got, err := g.LastAlertFiredAt(context.Background(), 1)
	if err == nil {
		t.Fatal("expected a Scan error against a missing table, got nil")
	}
	if got != nil {
		t.Errorf("on error the timestamp must be nil, got %v", got)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		t.Errorf("error should describe the missing table, got %q", err.Error())
	}
}
