package repo

// Tests for the age-based retention deletes (cycle-13 L6). Hermetic SQLite
// tier: the behaviour under test is the WHERE shape and the batch bound, not
// a dialect feature, and the same id-subquery batching is what the PG-tier
// DeleteOldLog tests already cover on Postgres.

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// seedRetentionLog inserts one logs row at an explicit created_at.
func seedRetentionLog(t *testing.T, logType int, createdAt int64) {
	t.Helper()
	lg := &Log{
		UserId:    7,
		TenantId:  "default",
		Type:      logType,
		Content:   "retention fixture",
		CreatedAt: createdAt,
		Quota:     11,
	}
	if err := LOG_DB.Create(lg).Error; err != nil {
		t.Fatalf("seed log(type=%d, created_at=%d): %v", logType, createdAt, err)
	}
}

func countLogRows(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := LOG_DB.Model(&Log{}).Count(&n).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	return n
}

// TestDeleteLogsBefore_DeletesOnlyListedTypesPastTheCutoff is the core
// oracle: the money rows (consume) an invoice is still derived from must
// survive a pass that ages out diagnostics, and a row inside the window must
// survive regardless of type.
func TestDeleteLogsBefore_DeletesOnlyListedTypesPastTheCutoff(t *testing.T) {
	defer setupSQLiteDB(t)()

	now := time.Now().Unix()
	old := now - 40*86400
	recent := now - 10*86400
	cutoff := now - 30*86400

	seedRetentionLog(t, entity.LogTypeError, old)
	seedRetentionLog(t, entity.LogTypeConsume, old)
	seedRetentionLog(t, entity.LogTypeError, recent)

	deleted, err := DeleteLogsBefore(context.Background(), cutoff,
		[]int{entity.LogTypeUnknown, entity.LogTypeManage, entity.LogTypeSystem, entity.LogTypeError},
		500, 200)
	if err != nil {
		t.Fatalf("DeleteLogsBefore: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only the 40-day-old error row)", deleted)
	}
	if got := countLogRows(t); got != 2 {
		t.Errorf("rows left = %d, want 2", got)
	}

	var consumeLeft int64
	if err := LOG_DB.Model(&Log{}).Where("type = ?", entity.LogTypeConsume).Count(&consumeLeft).Error; err != nil {
		t.Fatalf("count consume rows: %v", err)
	}
	if consumeLeft != 1 {
		t.Errorf("consume rows left = %d, want 1 — a billing row must not age out on the diagnostics window", consumeLeft)
	}

	var recentLeft int64
	if err := LOG_DB.Model(&Log{}).Where("created_at = ?", recent).Count(&recentLeft).Error; err != nil {
		t.Fatalf("count recent rows: %v", err)
	}
	if recentLeft != 1 {
		t.Errorf("rows inside the window left = %d, want 1", recentLeft)
	}
}

// TestDeleteLogsBefore_EmptyTypeListIsAnError: "no types" must never be read
// as "every type" — that mistake would delete billing rows on the
// diagnostics window.
func TestDeleteLogsBefore_EmptyTypeListIsAnError(t *testing.T) {
	defer setupSQLiteDB(t)()

	now := time.Now().Unix()
	seedRetentionLog(t, entity.LogTypeConsume, now-40*86400)

	if _, err := DeleteLogsBefore(context.Background(), now, nil, 500, 200); err == nil {
		t.Fatal("DeleteLogsBefore(nil types) returned nil error; want a refusal")
	}
	if got := countLogRows(t); got != 1 {
		t.Errorf("rows left = %d, want 1 (a refused call must delete nothing)", got)
	}
}

// TestDeleteLogsBefore_StopsAtTheBatchBudget proves the pass is bounded: a
// backlog larger than batch*maxBatches drains over several passes instead of
// in one long loop.
func TestDeleteLogsBefore_StopsAtTheBatchBudget(t *testing.T) {
	defer setupSQLiteDB(t)()

	now := time.Now().Unix()
	for i := 0; i < 5; i++ {
		seedRetentionLog(t, entity.LogTypeError, now-40*86400-int64(i))
	}

	deleted, err := DeleteLogsBefore(context.Background(), now, []int{entity.LogTypeError}, 2, 1)
	if err != nil {
		t.Fatalf("DeleteLogsBefore: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (batch=2, maxBatches=1)", deleted)
	}
	if got := countLogRows(t); got != 3 {
		t.Errorf("rows left = %d, want 3", got)
	}

	// The remainder drains on the next pass.
	if _, err := DeleteLogsBefore(context.Background(), now, []int{entity.LogTypeError}, 2, 10); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := countLogRows(t); got != 0 {
		t.Errorf("rows left after an unbounded-enough pass = %d, want 0", got)
	}
}

func TestCountLogsBefore_ReportsTheBacklogWithoutDeleting(t *testing.T) {
	defer setupSQLiteDB(t)()

	now := time.Now().Unix()
	seedRetentionLog(t, entity.LogTypeError, now-40*86400)
	seedRetentionLog(t, entity.LogTypeError, now-40*86400)
	seedRetentionLog(t, entity.LogTypeConsume, now-40*86400)
	seedRetentionLog(t, entity.LogTypeError, now-1)

	pending, err := CountLogsBefore(context.Background(), now-30*86400, []int{entity.LogTypeError})
	if err != nil {
		t.Fatalf("CountLogsBefore: %v", err)
	}
	if pending != 2 {
		t.Errorf("pending = %d, want 2 (the two old error rows)", pending)
	}
	if got := countLogRows(t); got != 4 {
		t.Errorf("rows left = %d, want 4 — counting must not delete", got)
	}
	if _, err := CountLogsBefore(context.Background(), now, nil); err == nil {
		t.Error("CountLogsBefore(nil types) returned nil error; want a refusal")
	}
}

// TestDeleteDownloadLogsBefore_AgesOutAnonymousDownloadRows: download_logs
// rows carry ip/user-agent/referer for callers with no user_id, so age is
// the only lever that can remove them.
func TestDeleteDownloadLogsBefore_AgesOutAnonymousDownloadRows(t *testing.T) {
	defer setupSQLiteDB(t)()

	if err := DB.AutoMigrate(&entity.DownloadLog{}); err != nil {
		t.Fatalf("auto migrate download_logs: %v", err)
	}
	now := time.Now()
	for _, at := range []time.Time{now.AddDate(0, 0, -100), now.AddDate(0, 0, -95), now.AddDate(0, 0, -10)} {
		row := &entity.DownloadLog{
			ArtifactId:   1,
			IpAddress:    "203.0.113.0",
			UserAgent:    "fixture",
			DownloadedAt: at,
		}
		if err := DB.Create(row).Error; err != nil {
			t.Fatalf("seed download log: %v", err)
		}
	}

	deleted, err := DeleteDownloadLogsBefore(context.Background(), now.AddDate(0, 0, -90), 500, 200)
	if err != nil {
		t.Fatalf("DeleteDownloadLogsBefore: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	var left int64
	if err := DB.Model(&entity.DownloadLog{}).Count(&left).Error; err != nil {
		t.Fatalf("count download_logs: %v", err)
	}
	if left != 1 {
		t.Errorf("download_logs left = %d, want 1 (the 10-day-old row)", left)
	}

	pending, err := CountDownloadLogsBefore(context.Background(), now.AddDate(0, 0, -90))
	if err != nil {
		t.Fatalf("CountDownloadLogsBefore: %v", err)
	}
	if pending != 0 {
		t.Errorf("pending = %d, want 0 after the pass", pending)
	}
}
