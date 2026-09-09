package repo

import (
	"context"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// TestDeleteExpiredAuditEvents_OnlyExpired asserts the Phase E3 invariant:
// rows with retention_until = 0 ("no expiry") stay; rows past their deadline
// are deleted; rows still within their window also stay. This is the
// boundary the audit_cleanup lifecycle task depends on.
func TestDeleteExpiredAuditEvents_OnlyExpired(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := int64(2_000_000_000)

	seed := []entity.AuditEvent{
		// Expired (retention_until in the past) — must be deleted.
		{Action: "expired-1", Timestamp: now - 100, RetentionUntil: now - 1, TenantID: "default"},
		{Action: "expired-2", Timestamp: now - 200, RetentionUntil: now - 50, TenantID: "default"},
		// Boundary: retention_until == now — must be deleted (predicate is <=).
		{Action: "boundary", Timestamp: now - 10, RetentionUntil: now, TenantID: "default"},
		// Future retention — must stay.
		{Action: "future", Timestamp: now - 5, RetentionUntil: now + 100, TenantID: "default"},
		// No-expiry (legacy backfill row) — must stay.
		{Action: "no-expiry", Timestamp: now - 5, RetentionUntil: 0, TenantID: "default"},
	}
	for i := range seed {
		if err := DB.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed event %s: %v", seed[i].Action, err)
		}
	}

	deleted, err := DeleteExpiredAuditEvents(context.Background(), now, 100)
	if err != nil {
		t.Fatalf("DeleteExpiredAuditEvents: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 deletions, got %d", deleted)
	}

	var remaining []entity.AuditEvent
	if err := DB.Find(&remaining).Error; err != nil {
		t.Fatalf("query remaining: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 surviving rows, got %d", len(remaining))
	}
	survivors := map[string]bool{}
	for _, r := range remaining {
		survivors[r.Action] = true
	}
	if !survivors["future"] || !survivors["no-expiry"] {
		t.Errorf("wrong survivors: %v", survivors)
	}
}

// TestDeleteExpiredAuditEvents_BatchesCorrectly asserts the cleanup loops
// across batches until none remain. With limit=2 and 5 expired rows the
// function should issue 3 DELETE statements (2 + 2 + 1) and return 5 total —
// not one unbounded DELETE, which is what `.Limit(n).Delete(...)` alone
// silently produces (gorm's DeleteClauses render no LIMIT).
func TestDeleteExpiredAuditEvents_BatchesCorrectly(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := int64(2_000_000_000)
	for i := 0; i < 5; i++ {
		if err := DB.Create(&entity.AuditEvent{
			Action:         "expired",
			Timestamp:      now - 100,
			RetentionUntil: now - 1,
			TenantID:       "default",
		}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	prevDB := DB
	scoped, counter := countDeletes(DB)
	DB = scoped
	defer func() { DB = prevDB }()

	deleted, err := DeleteExpiredAuditEvents(context.Background(), now, 2)
	if err != nil {
		t.Fatalf("DeleteExpiredAuditEvents: %v", err)
	}
	if deleted != 5 {
		t.Errorf("expected 5 deletions, got %d", deleted)
	}
	if counter.deletes != 3 {
		t.Errorf("DELETE statement count = %d, want 3 (2+2+1 bounded pages, not one unbounded DELETE)", counter.deletes)
	}
	for _, sql := range counter.deleteSQL {
		if !strings.Contains(sql, "ORDER BY") {
			t.Errorf("DELETE subquery has no ORDER BY (non-deterministic pages): %s", sql)
		}
	}
}

// TestDeleteOldAuditEvents_BatchesCorrectly locks L3 finding #2: the
// timestamp-based sibling of DeleteExpiredAuditEvents on the same table must
// use the same bounded id-subquery batching, not a raw `.Limit(n).Delete()`
// (silently unbounded on both dialects — see the comment on
// DeleteOldAuditEvents). limit=2 against 5 old rows must issue 3 DELETE
// statements (2 + 2 + 1), never one.
func TestDeleteOldAuditEvents_BatchesCorrectly(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	cutoff := int64(2_000_000_000)
	for i := 0; i < 5; i++ {
		if err := DB.Create(&entity.AuditEvent{
			Action:    "old",
			Timestamp: cutoff - 100,
			TenantID:  "default",
		}).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := DB.Create(&entity.AuditEvent{
		Action:    "new",
		Timestamp: cutoff + 100,
		TenantID:  "default",
	}).Error; err != nil {
		t.Fatalf("seed newer row: %v", err)
	}

	prevDB := DB
	scoped, counter := countDeletes(DB)
	DB = scoped
	defer func() { DB = prevDB }()

	deleted, err := DeleteOldAuditEvents(context.Background(), cutoff, 2)
	if err != nil {
		t.Fatalf("DeleteOldAuditEvents: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}
	if counter.deletes != 3 {
		t.Errorf("DELETE statement count = %d, want 3 (2+2+1 bounded pages, not one unbounded DELETE)", counter.deletes)
	}
	for _, sql := range counter.deleteSQL {
		if !strings.Contains(sql, "ORDER BY") {
			t.Errorf("DELETE subquery has no ORDER BY (non-deterministic pages): %s", sql)
		}
	}

	var remaining int64
	if err := prevDB.Model(&entity.AuditEvent{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Errorf("remaining rows = %d, want 1 (the newer row must survive)", remaining)
	}
}

// TestExportAuditEvents_CursorPagination asserts cursor-based traversal:
// returned events are ordered by id ASC, next_cursor points past the last
// row of the page, and a subsequent call with that cursor returns the rest.
func TestExportAuditEvents_CursorPagination(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		if err := DB.Create(&entity.AuditEvent{
			Action:    "token.created",
			Timestamp: int64(i + 1),
			TenantID:  "default",
		}).Error; err != nil {
			t.Fatalf("seed event %d: %v", i, err)
		}
	}

	page1, next, err := ExportAuditEvents(0, "", "", 0, 0, 0, 2)
	if err != nil {
		t.Fatalf("ExportAuditEvents page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1 size = %d, want 2", len(page1))
	}
	if next == 0 {
		t.Fatal("page 1 next_cursor = 0, expected positive")
	}
	if page1[0].ID >= page1[1].ID {
		t.Errorf("page 1 not sorted ASC: %v %v", page1[0].ID, page1[1].ID)
	}

	page2, next2, err := ExportAuditEvents(next, "", "", 0, 0, 0, 2)
	if err != nil {
		t.Fatalf("ExportAuditEvents page 2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page 2 size = %d, want 2", len(page2))
	}
	if next2 == 0 {
		t.Fatal("page 2 next_cursor = 0, expected positive (5 total, 4 read)")
	}

	page3, next3, err := ExportAuditEvents(next2, "", "", 0, 0, 0, 2)
	if err != nil {
		t.Fatalf("ExportAuditEvents page 3: %v", err)
	}
	if len(page3) != 1 {
		t.Errorf("page 3 size = %d, want 1 (final)", len(page3))
	}
	if next3 != 0 {
		t.Errorf("page 3 next_cursor = %d, want 0 (terminator)", next3)
	}
}

// TestExportAuditEvents_FilterByAction asserts the optional action filter
// narrows the scan and is composable with cursor pagination.
func TestExportAuditEvents_FilterByAction(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seeds := []entity.AuditEvent{
		{Action: "token.created", TenantID: "default"},
		{Action: "channel.deleted", TenantID: "default"},
		{Action: "token.created", TenantID: "default"},
		{Action: "auth.failed", TenantID: "default"},
	}
	for i := range seeds {
		if err := DB.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	events, _, err := ExportAuditEvents(0, "token.created", "", 0, 0, 0, 100)
	if err != nil {
		t.Fatalf("ExportAuditEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(events))
	}
	for _, e := range events {
		if e.Action != "token.created" {
			t.Errorf("unexpected action %q in filtered result", e.Action)
		}
	}
}
