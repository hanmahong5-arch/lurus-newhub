package lifecycle

// Tests for the log/download_logs retention sweep (cycle-13 L6).
//
// Every case drives the real RunLogRetentionPass against a real gorm handle
// wired into repo.LOG_DB / repo.DB, so the rows really are deleted (or
// really are not) through the shipped repo.DeleteLogsBefore query — no
// hand-built shape stands in for it.

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var logRetentionDBCounter atomic.Int64

// openLogRetentionTestDB wires a fresh in-memory SQLite into BOTH repo.DB
// and repo.LOG_DB: `logs` is read through LOG_DB (it may live in a separate
// database behind LOG_SQL_DSN) while `download_logs` is read through DB.
func openLogRetentionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:logretention%d?mode=memory&cache=shared", logRetentionDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, model := range []interface{}{&entity.Log{}, &entity.DownloadLog{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", model, err)
		}
	}
	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	repo.DB, repo.LOG_DB = db, db
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func seedLogAt(t *testing.T, db *gorm.DB, logType int, createdAt int64) {
	t.Helper()
	if err := db.Create(&entity.Log{
		UserId:    3,
		TenantId:  "default",
		Type:      logType,
		Content:   "retention fixture",
		CreatedAt: createdAt,
	}).Error; err != nil {
		t.Fatalf("seed log(type=%d): %v", logType, err)
	}
}

func logRowsOfType(t *testing.T, db *gorm.DB, logType int) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&entity.Log{}).Where("type = ?", logType).Count(&n).Error; err != nil {
		t.Fatalf("count type=%d: %v", logType, err)
	}
	return n
}

// TestLogRetentionPass_DeletesAgedDiagnosticsAndSparesMoneyRows is the
// plan's headline oracle: with LOG_RETENTION_DAYS=30 a pass removes the
// 40-day-old error row, leaves the 40-day-old consume row (money rows have
// their own, much longer window, and it is off here), and leaves the
// 10-day-old error row.
func TestLogRetentionPass_DeletesAgedDiagnosticsAndSparesMoneyRows(t *testing.T) {
	db := openLogRetentionTestDB(t)

	now := time.Now()
	old := now.AddDate(0, 0, -40).Unix()
	recent := now.AddDate(0, 0, -10).Unix()
	seedLogAt(t, db, entity.LogTypeError, old)
	seedLogAt(t, db, entity.LogTypeConsume, old)
	seedLogAt(t, db, entity.LogTypeError, recent)

	t.Setenv("LOG_RETENTION_DAYS", "30")
	t.Setenv("DOWNLOAD_LOG_RETENTION_DAYS", "0")

	deleted, err := RunLogRetentionPass(context.Background(), LoadLogRetentionConfig(), now)
	if err != nil {
		t.Fatalf("RunLogRetentionPass: %v", err)
	}
	if deleted["logs"] != 1 {
		t.Errorf("deleted[logs] = %d, want 1", deleted["logs"])
	}
	if got := logRowsOfType(t, db, entity.LogTypeConsume); got != 1 {
		t.Errorf("consume rows left = %d, want 1 — a billing row must not age out on LOG_RETENTION_DAYS", got)
	}
	if got := logRowsOfType(t, db, entity.LogTypeError); got != 1 {
		t.Errorf("error rows left = %d, want 1 (the 10-day-old one)", got)
	}
}

// TestLogRetentionPass_RefusesAWindowUnderTheFloor: 3 days is below the
// 30-day diagnostics floor, so the pass refuses it and deletes NOTHING —
// clamping up to the floor would quietly keep more than the operator asked
// for, clamping down would delete evidence they still needed.
func TestLogRetentionPass_RefusesAWindowUnderTheFloor(t *testing.T) {
	db := openLogRetentionTestDB(t)

	now := time.Now()
	seedLogAt(t, db, entity.LogTypeError, now.AddDate(0, 0, -40).Unix())

	t.Setenv("LOG_RETENTION_DAYS", strconv.Itoa(LogRetentionMinDays-1))
	t.Setenv("DOWNLOAD_LOG_RETENTION_DAYS", "0")

	deleted, err := RunLogRetentionPass(context.Background(), LoadLogRetentionConfig(), now)
	if err != nil {
		t.Fatalf("RunLogRetentionPass: %v", err)
	}
	if deleted["logs"] != 0 {
		t.Errorf("deleted[logs] = %d, want 0 for a refused window", deleted["logs"])
	}
	if got := logRowsOfType(t, db, entity.LogTypeError); got != 1 {
		t.Errorf("error rows left = %d, want 1", got)
	}
}

// TestLogRetentionPass_UnsetWindowsDeleteNothing: the shipped default is
// off. A deploy of this task must not delete a single row until an operator
// sets a window (O-retention).
func TestLogRetentionPass_UnsetWindowsDeleteNothing(t *testing.T) {
	db := openLogRetentionTestDB(t)

	now := time.Now()
	veryOld := now.AddDate(0, 0, -3650).Unix()
	seedLogAt(t, db, entity.LogTypeError, veryOld)
	seedLogAt(t, db, entity.LogTypeConsume, veryOld)

	t.Setenv("LOG_RETENTION_DAYS", "")
	t.Setenv("LOG_RETENTION_MONEY_DAYS", "")
	t.Setenv("DOWNLOAD_LOG_RETENTION_DAYS", "0")

	cfg := LoadLogRetentionConfig()
	if cfg.DiagnosticDays != 0 || cfg.MoneyDays != 0 {
		t.Fatalf("unset windows resolved to %d/%d, want 0/0", cfg.DiagnosticDays, cfg.MoneyDays)
	}
	deleted, err := RunLogRetentionPass(context.Background(), cfg, now)
	if err != nil {
		t.Fatalf("RunLogRetentionPass: %v", err)
	}
	if deleted["logs"] != 0 {
		t.Errorf("deleted[logs] = %d, want 0 with retention off", deleted["logs"])
	}
	var left int64
	if err := db.Model(&entity.Log{}).Count(&left).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if left != 2 {
		t.Errorf("rows left = %d, want 2", left)
	}
}

// TestLogRetentionPass_MoneyWindowAgesBillingRowsOnItsOwnFloor covers the
// other half: the money leg deletes only past its own 400-day floor, and a
// value under that floor is refused the same way.
func TestLogRetentionPass_MoneyWindowAgesBillingRowsOnItsOwnFloor(t *testing.T) {
	db := openLogRetentionTestDB(t)

	now := time.Now()
	seedLogAt(t, db, entity.LogTypeConsume, now.AddDate(0, 0, -500).Unix())
	seedLogAt(t, db, entity.LogTypeTopup, now.AddDate(0, 0, -500).Unix())
	seedLogAt(t, db, entity.LogTypeConsume, now.AddDate(0, 0, -399).Unix())

	t.Setenv("DOWNLOAD_LOG_RETENTION_DAYS", "0")

	// Under the floor: nothing goes.
	t.Setenv("LOG_RETENTION_MONEY_DAYS", strconv.Itoa(LogRetentionMoneyMinDays-1))
	deleted, err := RunLogRetentionPass(context.Background(), LoadLogRetentionConfig(), now)
	if err != nil {
		t.Fatalf("RunLogRetentionPass (under floor): %v", err)
	}
	if deleted["logs"] != 0 {
		t.Fatalf("deleted[logs] = %d under the money floor, want 0", deleted["logs"])
	}

	// At the floor: the two 500-day-old rows go, the 399-day-old one stays.
	t.Setenv("LOG_RETENTION_MONEY_DAYS", strconv.Itoa(LogRetentionMoneyMinDays))
	deleted, err = RunLogRetentionPass(context.Background(), LoadLogRetentionConfig(), now)
	if err != nil {
		t.Fatalf("RunLogRetentionPass (at floor): %v", err)
	}
	if deleted["logs"] != 2 {
		t.Errorf("deleted[logs] = %d, want 2", deleted["logs"])
	}
	if got := logRowsOfType(t, db, entity.LogTypeConsume); got != 1 {
		t.Errorf("consume rows left = %d, want 1 (the 399-day-old row is inside a 400-day window)", got)
	}
	if got := logRowsOfType(t, db, entity.LogTypeTopup); got != 0 {
		t.Errorf("topup rows left = %d, want 0", got)
	}
}

// TestLogRetentionPass_DownloadLogsAgeOutOnTheirOwnWindow: download_logs
// rows have no user_id, so age is the only lever a privacy request has on
// them. This leg defaults to ON at 90 days.
func TestLogRetentionPass_DownloadLogsAgeOutOnTheirOwnWindow(t *testing.T) {
	db := openLogRetentionTestDB(t)

	now := time.Now()
	for _, at := range []time.Time{now.AddDate(0, 0, -120), now.AddDate(0, 0, -30)} {
		if err := db.Create(&entity.DownloadLog{
			ArtifactId:   4,
			IpAddress:    "203.0.113.0",
			UserAgent:    "fixture",
			DownloadedAt: at,
		}).Error; err != nil {
			t.Fatalf("seed download log: %v", err)
		}
	}

	cfg := LoadLogRetentionConfig()
	if cfg.DownloadDays != 90 {
		t.Fatalf("DOWNLOAD_LOG_RETENTION_DAYS default = %d, want 90", cfg.DownloadDays)
	}
	deleted, err := RunLogRetentionPass(context.Background(), cfg, now)
	if err != nil {
		t.Fatalf("RunLogRetentionPass: %v", err)
	}
	if deleted["download_logs"] != 1 {
		t.Errorf("deleted[download_logs] = %d, want 1", deleted["download_logs"])
	}
	var left int64
	if err := db.Model(&entity.DownloadLog{}).Count(&left).Error; err != nil {
		t.Fatalf("count download_logs: %v", err)
	}
	if left != 1 {
		t.Errorf("download_logs left = %d, want 1", left)
	}
}

// TestLogRetentionPass_HonoursTheBatchBudget: a backlog bigger than
// batch*maxBatches drains over passes rather than in one lock-holding loop.
func TestLogRetentionPass_HonoursTheBatchBudget(t *testing.T) {
	db := openLogRetentionTestDB(t)

	now := time.Now()
	for i := 0; i < 6; i++ {
		seedLogAt(t, db, entity.LogTypeSystem, now.AddDate(0, 0, -40).Unix()-int64(i))
	}

	t.Setenv("LOG_RETENTION_DAYS", "30")
	t.Setenv("LOG_RETENTION_BATCH", "2")
	t.Setenv("LOG_RETENTION_MAX_BATCHES_PER_PASS", "1")
	t.Setenv("DOWNLOAD_LOG_RETENTION_DAYS", "0")

	cfg := LoadLogRetentionConfig()
	if cfg.Batch != 2 || cfg.MaxBatches != 1 {
		t.Fatalf("batch/maxBatches resolved to %d/%d, want 2/1", cfg.Batch, cfg.MaxBatches)
	}
	deleted, err := RunLogRetentionPass(context.Background(), cfg, now)
	if err != nil {
		t.Fatalf("RunLogRetentionPass: %v", err)
	}
	if deleted["logs"] != 2 {
		t.Errorf("deleted[logs] = %d, want 2 (batch 2 x 1 pass)", deleted["logs"])
	}
	if got := logRowsOfType(t, db, entity.LogTypeSystem); got != 4 {
		t.Errorf("system rows left = %d, want 4", got)
	}
}

// TestLogRetentionInterval_ReadsTheEnv pins the tick period taskreg reports.
func TestLogRetentionInterval_ReadsTheEnv(t *testing.T) {
	if got := LogRetentionInterval(); got != 24*time.Hour {
		t.Errorf("default interval = %s, want 24h", got)
	}
	t.Setenv("LOG_RETENTION_INTERVAL_SECONDS", "600")
	if got := LogRetentionInterval(); got != 10*time.Minute {
		t.Errorf("interval with LOG_RETENTION_INTERVAL_SECONDS=600 = %s, want 10m", got)
	}
	t.Setenv("LOG_RETENTION_INTERVAL_SECONDS", "not-a-number")
	if got := LogRetentionInterval(); got != 24*time.Hour {
		t.Errorf("interval with an unparsable value = %s, want the 24h default", got)
	}
}

// TestLogRetention_StartRegistersHeartbeat: the task has to appear in
// GET /api/v2/admin/system/tasks as leader-only, or an operator cannot tell
// a retention sweep that never ran from one that has nothing to do.
func TestLogRetention_StartRegistersHeartbeat(t *testing.T) {
	before := len(taskreg.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartLogRetentionWithContext(ctx)

	snap := taskreg.Snapshot()
	if len(snap) <= before {
		t.Fatalf("taskreg.Snapshot() length did not grow: before=%d after=%d", before, len(snap))
	}
	found := false
	for _, task := range snap {
		if task.Name == "log-retention" {
			found = true
			if !task.LeaderOnly {
				t.Errorf("log-retention task.LeaderOnly = false, want true")
			}
			if task.Interval == nil {
				t.Error("log-retention task.Interval is nil")
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q after StartLogRetentionWithContext", "log-retention")
	}
}

// TestLogRetentionTypes_PartitionEveryDeclaredLogType parses
// internal/domain/entity/log.go and requires every LogType* constant it
// declares to appear in exactly one of the two retention buckets.
//
// WHY parse rather than list: adding a log type is a one-line change in
// entity, and a type in neither bucket is retained forever with no error
// anywhere — the silent failure this gate exists to convert into a red test.
func TestLogRetentionTypes_PartitionEveryDeclaredLogType(t *testing.T) {
	const entityLogFile = "../domain/entity/log.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, entityLogFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", entityLogFile, err)
	}

	declared := map[string]int{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
			return true
		}
		name := spec.Names[0].Name
		if !strings.HasPrefix(name, "LogType") {
			return true
		}
		lit, ok := spec.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return true
		}
		v, convErr := strconv.Atoi(lit.Value)
		if convErr != nil {
			return true
		}
		declared[name] = v
		return true
	})

	if len(declared) < 5 {
		t.Fatalf("found only %d LogType* constants in %s — the gate parsed nothing useful",
			len(declared), entityLogFile)
	}

	bucket := map[int][]string{}
	for _, v := range moneyLogTypes {
		bucket[v] = append(bucket[v], "money")
	}
	for _, v := range diagnosticLogTypes {
		bucket[v] = append(bucket[v], "diagnostic")
	}
	for name, value := range declared {
		switch len(bucket[value]) {
		case 0:
			t.Errorf("%s (=%d) is in neither moneyLogTypes nor diagnosticLogTypes — "+
				"rows of that type would be retained forever", name, value)
		case 1:
			// exactly one bucket, as required
		default:
			t.Errorf("%s (=%d) is in more than one bucket (%v)", name, value, bucket[value])
		}
	}
	for value, names := range bucket {
		found := false
		for _, v := range declared {
			if v == value {
				found = true
			}
		}
		if !found {
			t.Errorf("retention bucket %v classifies log type %d, which entity no longer declares", names, value)
		}
	}
}
