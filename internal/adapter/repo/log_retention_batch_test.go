package repo

// log_retention_batch_test.go pins DeleteOldLog's bounded-batch shape: each
// pass must issue one DELETE per `limit`-sized page, never a single
// unbounded DELETE. gorm's DeleteClauses (both the postgres and the sqlite
// drivers) don't render a LIMIT on DELETE, so `.Limit(n).Delete(...)` alone
// is silently ignored and ships one DELETE that removes every matching row —
// exactly the multi-second table lock this test exists to prevent.

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// stmtCounter is a gorm logger.Interface that counts DELETE statements it
// observes via Trace, so a test can assert "how many round trips" rather
// than only "how many rows ended up deleted" — the row count alone cannot
// distinguish one unbounded DELETE from N bounded ones.
type stmtCounter struct {
	logger.Interface
	deletes   int
	deleteSQL []string
}

func (c *stmtCounter) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	if len(sql) >= 6 && (sql[:6] == "DELETE" || sql[:6] == "delete") {
		c.deletes++
		c.deleteSQL = append(c.deleteSQL, sql)
	}
}

// countDeletes points scoped at a fresh session using counter as its logger,
// so statements issued through the returned *gorm.DB are counted without
// disturbing the shared package-level DB/LOG_DB globals used elsewhere.
func countDeletes(scoped *gorm.DB) (*gorm.DB, *stmtCounter) {
	counter := &stmtCounter{Interface: logger.Discard}
	return scoped.Session(&gorm.Session{Logger: counter}), counter
}

// TestDeleteOldLog_Batches is the hermetic (SQLite) half of the plan shape:
// 5 rows older than the cutoff, 2 newer, limit=2 must yield exactly 3 DELETE
// statements (2 + 2 + 1) and return 5 while leaving the 2 newer rows intact.
func TestDeleteOldLog_Batches(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	cutoff := int64(2_000_000_000)
	for i := 0; i < 5; i++ {
		if err := DB.Create(&Log{CreatedAt: cutoff - 100, TenantId: "default"}).Error; err != nil {
			t.Fatalf("seed old row %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := DB.Create(&Log{CreatedAt: cutoff + 100, TenantId: "default"}).Error; err != nil {
			t.Fatalf("seed new row %d: %v", i, err)
		}
	}

	prevLogDB := LOG_DB
	scoped, counter := countDeletes(LOG_DB)
	LOG_DB = scoped
	defer func() { LOG_DB = prevLogDB }()

	deleted, err := DeleteOldLog(context.Background(), AllTenantsForAdmin(), cutoff, 2)
	if err != nil {
		t.Fatalf("DeleteOldLog: %v", err)
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
	if err := DB.Model(&Log{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 2 {
		t.Errorf("remaining rows = %d, want 2 (the newer rows must survive)", remaining)
	}
}

// TestDeleteOldLog_Batches_PG is the same assertion against real PostgreSQL —
// the only proof the LIMIT-via-subquery shape actually renders on the
// production dialect, not just glebarez's SQLite port. Skips without
// TEST_POSTGRES_DSN.
func TestDeleteOldLog_Batches_PG(t *testing.T) {
	SetupTestDB(t)

	cutoff := int64(2_000_000_000)
	for i := 0; i < 5; i++ {
		if err := DB.Create(&Log{CreatedAt: cutoff - 100, TenantId: "default"}).Error; err != nil {
			t.Fatalf("seed old row %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := DB.Create(&Log{CreatedAt: cutoff + 100, TenantId: "default"}).Error; err != nil {
			t.Fatalf("seed new row %d: %v", i, err)
		}
	}

	prevLogDB := LOG_DB
	scoped, counter := countDeletes(LOG_DB)
	LOG_DB = scoped
	defer func() { LOG_DB = prevLogDB }()

	deleted, err := DeleteOldLog(context.Background(), AllTenantsForAdmin(), cutoff, 2)
	if err != nil {
		t.Fatalf("DeleteOldLog: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}
	if counter.deletes != 3 {
		t.Errorf("DELETE statement count = %d, want 3 (2+2+1 bounded pages)", counter.deletes)
	}

	var remaining int64
	if err := DB.Model(&Log{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 2 {
		t.Errorf("remaining rows = %d, want 2", remaining)
	}
}
