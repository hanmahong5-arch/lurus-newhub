package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

var natsSQLiteCounter atomic.Int64

// tcpRow is a minimal stand-in for the tenant_credit_pools table with just the
// columns gormPoolDB reads/writes. Defining it locally keeps the gormPoolDB SQL
// builder honest (real table name + real alert_fired_at column) without pulling
// the full repo entity into this package's test surface.
type tcpRow struct {
	ID           int64      `gorm:"column:id;primaryKey"`
	AlertFiredAt *time.Time `gorm:"column:alert_fired_at"`
}

func (tcpRow) TableName() string { return "tenant_credit_pools" }

// newPoolSQLite opens a unique in-memory SQLite DB with the tenant_credit_pools
// table migrated, so gormPoolDB's real GORM queries execute against a real
// schema.
func newPoolSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:natspoolthreshold%d?mode=memory&cache=shared", natsSQLiteCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&tcpRow{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// --- gormPoolDB.LastAlertFiredAt ---

func TestGormPoolDB_LastAlertFiredAt_NullColumn(t *testing.T) {
	db := newPoolSQLite(t)
	if err := db.Create(&tcpRow{ID: 1, AlertFiredAt: nil}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	g := &gormPoolDB{db: db}

	got, err := g.LastAlertFiredAt(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("NULL alert_fired_at should read as nil, got %v", got)
	}
}

func TestGormPoolDB_LastAlertFiredAt_ReturnsTimestamp(t *testing.T) {
	db := newPoolSQLite(t)
	fired := time.Date(2026, 5, 18, 9, 30, 0, 0, time.UTC)
	if err := db.Create(&tcpRow{ID: 2, AlertFiredAt: &fired}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	g := &gormPoolDB{db: db}

	got, err := g.LastAlertFiredAt(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a timestamp, got nil")
	}
	if !got.Equal(fired) {
		t.Errorf("alert_fired_at = %v, want %v", got, fired)
	}
}

func TestGormPoolDB_LastAlertFiredAt_MissingRow(t *testing.T) {
	db := newPoolSQLite(t)
	g := &gormPoolDB{db: db}

	// Scan (not First) over a missing row yields no error and a zero row →
	// nil timestamp, which the caller treats as "never fired".
	got, err := g.LastAlertFiredAt(context.Background(), 999)
	if err != nil {
		t.Fatalf("missing row must not error under Scan, got %v", err)
	}
	if got != nil {
		t.Errorf("missing row should read as nil, got %v", got)
	}
}

// --- gormPoolDB.MarkAlertFired ---

func TestGormPoolDB_MarkAlertFired_PersistsTimestamp(t *testing.T) {
	db := newPoolSQLite(t)
	if err := db.Create(&tcpRow{ID: 3, AlertFiredAt: nil}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	g := &gormPoolDB{db: db}

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := g.MarkAlertFired(context.Background(), 3, now); err != nil {
		t.Fatalf("MarkAlertFired: %v", err)
	}

	got, err := g.LastAlertFiredAt(context.Background(), 3)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got == nil || !got.Equal(now) {
		t.Errorf("persisted alert_fired_at = %v, want %v", got, now)
	}
}

// --- redisCommonDeduper.SetNXBool (no-Redis branch) ---

func TestRedisCommonDeduper_NoRedisAlwaysAcquires(t *testing.T) {
	prevEnabled := common.RedisEnabled
	prevRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = prevEnabled
		common.RDB = prevRDB
	})

	acquired, err := redisCommonDeduper{}.SetNXBool(context.Background(), "pool:threshold:x:1", time.Hour)
	if err != nil {
		t.Fatalf("no-Redis SetNXBool must not error, got %v", err)
	}
	if !acquired {
		t.Error("no-Redis mode must let publication proceed (acquired=true)")
	}
}

// --- PublishPoolThreshold wrapper: guard branches ---

// TestPublishPoolThreshold_DisabledStillMarksRecordedOnly locks the lane's
// core behaviour change: LLM_QUOTA_NATS_ENABLED=false no longer short-
// circuits before dedup/mark. The crossing must still be marked, audited
// (by the caller), and counted with delivery="recorded_only" — only the
// wire leg (fj.mu.calls) is skipped. See pool_threshold.go's PublishPoolThreshold
// doc comment; NoOpWhenDisabled/NoOpWhenPublisherNil (the pre-lane names)
// described the OLD "return nil immediately" behaviour this replaces.
func TestPublishPoolThreshold_DisabledStillMarksRecordedOnly(t *testing.T) {
	t.Setenv("LLM_QUOTA_NATS_ENABLED", "false")
	// Even with a publisher installed, disabled must not reach the wire.
	fj := &fakeJS{}
	withGlobal(t, fj)

	db := newPoolSQLite(t)
	if err := db.Create(&tcpRow{ID: 61, AlertFiredAt: nil}).Error; err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	prevDB := repo.DB
	repo.DB = db
	t.Cleanup(func() { repo.DB = prevDB })

	beforeCounter := testutil.ToFloat64(metrics.CreditPoolAlertTotal.WithLabelValues("t-disabled", "recorded_only"))

	fired, delivery, err := PublishPoolThreshold(context.Background(), "t-disabled", 61, 10, 1000, 80)
	if err != nil {
		t.Fatalf("disabled must not error, got %v", err)
	}
	if !fired {
		t.Error("disabled must still report fired=true (dedup let it through, mark landed)")
	}
	if delivery != "recorded_only" {
		t.Errorf("delivery = %q, want recorded_only", delivery)
	}
	if fj.mu.calls != 0 {
		t.Errorf("disabled must not publish, got %d calls", fj.mu.calls)
	}

	g := &gormPoolDB{db: db}
	last, lerr := g.LastAlertFiredAt(context.Background(), 61)
	if lerr != nil {
		t.Fatalf("read alert_fired_at: %v", lerr)
	}
	if last == nil {
		t.Error("alert_fired_at must be marked even when NATS is disabled")
	}
	if delta := testutil.ToFloat64(metrics.CreditPoolAlertTotal.WithLabelValues("t-disabled", "recorded_only")) - beforeCounter; delta != 1 {
		t.Errorf("CreditPoolAlertTotal{recorded_only} delta = %v, want 1", delta)
	}
}

// TestPublishPoolThreshold_NilGlobalPublisherStillMarksRecordedOnly is the
// sibling case: LLM_QUOTA_NATS_ENABLED=true but the global Publisher was
// never wired (global == nil). Must behave identically to the disabled case
// — recorded_only, no panic from a typed-nil *Publisher reaching Publish().
func TestPublishPoolThreshold_NilGlobalPublisherStillMarksRecordedOnly(t *testing.T) {
	t.Setenv("LLM_QUOTA_NATS_ENABLED", "true")
	prev := global
	global = nil
	t.Cleanup(func() { global = prev })

	db := newPoolSQLite(t)
	if err := db.Create(&tcpRow{ID: 62, AlertFiredAt: nil}).Error; err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	prevDB := repo.DB
	repo.DB = db
	t.Cleanup(func() { repo.DB = prevDB })

	fired, delivery, err := PublishPoolThreshold(context.Background(), "t-nilpub", 62, 10, 1000, 80)
	if err != nil {
		t.Fatalf("nil publisher must not error, got %v", err)
	}
	if !fired || delivery != "recorded_only" {
		t.Errorf("fired=%v delivery=%q, want fired=true delivery=recorded_only", fired, delivery)
	}
}

func TestPublishPoolThreshold_NoOpOnBadArgs(t *testing.T) {
	t.Setenv("LLM_QUOTA_NATS_ENABLED", "true")
	fj := &fakeJS{}
	withGlobal(t, fj)

	cases := []struct {
		name     string
		tenantID string
		poolID   int64
	}{
		{"empty tenant", "", 1},
		{"zero pool", "t", 0},
		{"negative pool", "t", -3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := PublishPoolThreshold(context.Background(), tc.tenantID, tc.poolID, 10, 1000, 80); err != nil {
				t.Fatalf("bad args must return nil, got %v", err)
			}
		})
	}
	if fj.mu.calls != 0 {
		t.Errorf("guarded args must never publish, got %d calls", fj.mu.calls)
	}
}

// TestPublishPoolThreshold_EndToEnd wires the real production dependencies the
// wrapper reaches for (repo.DB via gormPoolDB, redisCommonDeduper with Redis
// disabled, the global Publisher) and proves the low-balance alert reaches the
// wire exactly once with the correct tenant/pool payload — and marks the schema
// dedup slot durably.
func TestPublishPoolThreshold_EndToEnd(t *testing.T) {
	t.Setenv("LLM_QUOTA_NATS_ENABLED", "true")

	db := newPoolSQLite(t)
	if err := db.Create(&tcpRow{ID: 55, AlertFiredAt: nil}).Error; err != nil {
		t.Fatalf("seed pool: %v", err)
	}

	prevDB := repo.DB
	repo.DB = db
	t.Cleanup(func() { repo.DB = prevDB })

	prevEnabled := common.RedisEnabled
	prevRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = prevEnabled
		common.RDB = prevRDB
	})

	fj := &fakeJS{}
	withGlobal(t, fj)

	beforeCounter := testutil.ToFloat64(metrics.CreditPoolAlertTotal.WithLabelValues("tenant-Z", "nats"))

	fired, delivery, err := PublishPoolThreshold(context.Background(), "tenant-Z", 55, 120, 1000, 80)
	if err != nil {
		t.Fatalf("PublishPoolThreshold: %v", err)
	}
	if !fired || delivery != "nats" {
		t.Errorf("fired=%v delivery=%q, want fired=true delivery=nats", fired, delivery)
	}
	if delta := testutil.ToFloat64(metrics.CreditPoolAlertTotal.WithLabelValues("tenant-Z", "nats")) - beforeCounter; delta != 1 {
		t.Errorf("CreditPoolAlertTotal{nats} delta = %v, want 1", delta)
	}

	if fj.mu.calls != 1 {
		t.Fatalf("expected exactly 1 publish, got %d", fj.mu.calls)
	}
	if fj.mu.subject != SubjectPoolThreshold {
		t.Errorf("subject = %q, want %q", fj.mu.subject, SubjectPoolThreshold)
	}

	var payload PoolThresholdPayload
	if err := json.Unmarshal(fj.mu.data, &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.TenantID != "tenant-Z" || payload.PoolID != 55 ||
		payload.CurrentBalance != 120 || payload.MaxBalance != 1000 || payload.ThresholdPct != 80 {
		t.Errorf("payload mismatch: %+v", payload)
	}

	// Schema dedup slot must be committed after a successful fire.
	g := &gormPoolDB{db: db}
	last, err := g.LastAlertFiredAt(context.Background(), 55)
	if err != nil {
		t.Fatalf("read alert_fired_at: %v", err)
	}
	if last == nil {
		t.Error("alert_fired_at must be marked after a successful publish")
	}

	// Second call within the dedup window must suppress (no second publish).
	fired2, delivery2, err := PublishPoolThreshold(context.Background(), "tenant-Z", 55, 120, 1000, 80)
	if err != nil {
		t.Fatalf("second PublishPoolThreshold: %v", err)
	}
	if fired2 || delivery2 != "" {
		t.Errorf("suppressed call = fired=%v delivery=%q, want fired=false delivery=\"\"", fired2, delivery2)
	}
	if fj.mu.calls != 1 {
		t.Errorf("schema dedup must suppress the repeat fire; got %d total calls", fj.mu.calls)
	}
}

// TestPublishPoolThreshold_PublishFailureNotCountedAsNats is the metrics
// honesty lock (findings L4 item 1): a crossing whose schema mark succeeds
// but whose NATS publish fails must NOT increment
// credit_pool_alert_total{delivery="nats"} — that label means the payload
// actually reached the wire. It must still report fired=true (mark+audit
// landed) with delivery="recorded_only", and the caller sees a non-nil
// error to drive CreditPoolAlertHookErrorTotal instead. Revert the ordering
// in publishPoolThreshold (increment "nats" before checking pub.Publish's
// error) and this goes red.
func TestPublishPoolThreshold_PublishFailureNotCountedAsNats(t *testing.T) {
	t.Setenv("LLM_QUOTA_NATS_ENABLED", "true")

	db := newPoolSQLite(t)
	if err := db.Create(&tcpRow{ID: 56, AlertFiredAt: nil}).Error; err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	prevDB := repo.DB
	repo.DB = db
	t.Cleanup(func() { repo.DB = prevDB })

	prevEnabled := common.RedisEnabled
	prevRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = prevEnabled
		common.RDB = prevRDB
	})

	fj := &fakeJS{failErr: fmt.Errorf("simulated NATS down")}
	withGlobal(t, fj)

	beforeNats := testutil.ToFloat64(metrics.CreditPoolAlertTotal.WithLabelValues("tenant-fail", "nats"))
	beforeRecordedOnly := testutil.ToFloat64(metrics.CreditPoolAlertTotal.WithLabelValues("tenant-fail", "recorded_only"))

	fired, delivery, err := PublishPoolThreshold(context.Background(), "tenant-fail", 56, 120, 1000, 80)
	if err == nil {
		t.Fatal("expected error from failed publish, got nil")
	}
	if !fired {
		t.Error("expected fired=true — schema mark landed even though the wire leg failed")
	}
	if delivery != "recorded_only" {
		t.Errorf("delivery = %q, want recorded_only (publish never reached the wire)", delivery)
	}
	if delta := testutil.ToFloat64(metrics.CreditPoolAlertTotal.WithLabelValues("tenant-fail", "nats")) - beforeNats; delta != 0 {
		t.Errorf("CreditPoolAlertTotal{nats} delta = %v, want 0 — a failed publish must never be counted as delivered", delta)
	}
	if delta := testutil.ToFloat64(metrics.CreditPoolAlertTotal.WithLabelValues("tenant-fail", "recorded_only")) - beforeRecordedOnly; delta != 0 {
		t.Errorf("CreditPoolAlertTotal{recorded_only} delta = %v, want 0 — a failed nats attempt is not the same as a nil-publisher recorded_only fire", delta)
	}

	g := &gormPoolDB{db: db}
	last, lerr := g.LastAlertFiredAt(context.Background(), 56)
	if lerr != nil {
		t.Fatalf("read alert_fired_at: %v", lerr)
	}
	if last == nil {
		t.Error("alert_fired_at must still be marked when the publish fails (fail-closed dedup)")
	}
}
