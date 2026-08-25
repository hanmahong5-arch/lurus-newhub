package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// The outbox claim has to survive the statement that made it: three replicas
// tick the drain every 5s, so a claim carried only by row locks (a bare
// SELECT ... FOR UPDATE SKIP LOCKED in autocommit) is gone before the entry is
// handed to the platform, and every replica settles the same pre-auth.
//
// What the hermetic (SQLite) tier below proves: the claim is durable — a second
// claimer that runs AFTER the first statement completed sees nothing, the
// status transitions are keyed off "processing", and an expired lease is
// re-claimed. What it CANNOT prove: that two claimers running AT THE SAME
// INSTANT stay disjoint. SQLite serialises writers with a single database-wide
// write lock, so it cannot express SKIP LOCKED contention at all — and GORM
// silently drops a FOR UPDATE clause on that dialect, which is exactly how the
// original defect stayed green here. That half is covered by
// TestClaimBillingOutbox_ConcurrentReplicasStayDisjoint, which needs a real
// PostgreSQL (TEST_POSTGRES_DSN) and skips without it.

// TestBillingOutboxTableName_MatchesClaimSQL guards the table name that the
// claim statement hardcodes (raw SQL cannot go through the GORM naming layer).
func TestBillingOutboxTableName_MatchesClaimSQL(t *testing.T) {
	if got := (entity.BillingOutbox{}).TableName(); got != "billing_outbox" {
		t.Fatalf("TableName() = %q, but the claim SQL targets billing_outbox", got)
	}
}

// TestClaimBillingOutbox_SecondClaimerGetsNothing is the core of defect (1):
// once an entry is claimed, a later claim must not hand it out again. Under the
// old lock-only claim the row stayed "pending" after the SELECT returned, so
// every subsequent tick — on this pod or any of the other two replicas — picked
// it up and called SettlePreAuthGRPC for it again.
func TestClaimBillingOutbox_SecondClaimerGetsNothing(t *testing.T) {
	db := setupServiceTestDB(t)
	restore := setupOutbox(t, db)
	defer restore()

	if err := EnqueueSettle(11, 4242, 2.5); err != nil {
		t.Fatalf("EnqueueSettle: %v", err)
	}

	now := time.Now()
	first, err := claimBillingOutbox(context.Background(), now)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first claim returned %d entries, want 1", len(first))
	}
	if first[0].ID == 0 || first[0].PreAuthID != 4242 || first[0].Action != outboxActionSettle {
		t.Fatalf("claimed entry = %+v, want the seeded settle row with its id", first[0])
	}

	var row entity.BillingOutbox
	if err := db.First(&row, first[0].ID).Error; err != nil {
		t.Fatalf("reload claimed row: %v", err)
	}
	if row.Status != outboxStatusProcessing {
		t.Errorf("persisted status = %q, want %q — the claim must outlive the statement",
			row.Status, outboxStatusProcessing)
	}

	second, err := claimBillingOutbox(context.Background(), now)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second claim returned %d entries, want 0 — entry %d would be settled twice",
			len(second), second[0].ID)
	}
}

// TestClaimBillingOutbox_ReclaimsExpiredLeaseOnly proves a pod killed mid-flight
// cannot wedge an entry forever, while an entry a live processor is still
// working on is left alone.
func TestClaimBillingOutbox_ReclaimsExpiredLeaseOnly(t *testing.T) {
	db := setupServiceTestDB(t)
	restore := setupOutbox(t, db)
	defer restore()

	now := time.Now()
	stuck := seedClaimedEntry(t, db, 5001, now.Add(-outboxClaimLease-time.Minute))
	live := seedClaimedEntry(t, db, 5002, now.Add(-time.Second))

	claimed, err := claimBillingOutbox(context.Background(), now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d entries, want 1 (only the expired lease)", len(claimed))
	}
	if claimed[0].ID != stuck {
		t.Fatalf("claimed entry id = %d, want %d (the stuck row); id %d is still owned",
			claimed[0].ID, stuck, live)
	}

	var refreshed entity.BillingOutbox
	if err := db.First(&refreshed, stuck).Error; err != nil {
		t.Fatalf("reload reclaimed row: %v", err)
	}
	if !refreshed.UpdatedAt.After(now.Add(-outboxClaimLease)) {
		t.Errorf("reclaimed row updated_at = %s, want the lease refreshed to ~%s",
			refreshed.UpdatedAt, now)
	}
}

// TestProcessBillingOutbox_PendingGaugeCountsClaimedEntries keeps the queue-depth
// metric honest: entries that are mid-flight are still outstanding work, so
// moving them to a new status must not make them vanish from the gauge.
func TestProcessBillingOutbox_PendingGaugeCountsClaimedEntries(t *testing.T) {
	db := setupServiceTestDB(t)
	restore := setupOutbox(t, db)
	defer restore()

	// One entry claimed by a peer replica moments ago (lease still valid, so
	// this tick will not re-claim it) plus one that is not due yet.
	seedClaimedEntry(t, db, 6001, time.Now().Add(-time.Second))
	notDueYet := entity.BillingOutbox{
		AccountID: 1, PreAuthID: 6002, Action: outboxActionSettle,
		Status: outboxStatusPending, NextRetry: time.Now().Add(time.Hour),
	}
	if err := db.Create(&notDueYet).Error; err != nil {
		t.Fatalf("seed pending row: %v", err)
	}

	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("ProcessBillingOutbox: %v", err)
	}

	if got := testutil.ToFloat64(metrics.BillingOutboxPending); got != 2 {
		t.Errorf("outbox_pending gauge = %v, want 2 (1 in flight + 1 waiting)", got)
	}
}

// TestClaimBillingOutbox_ConcurrentReplicasStayDisjoint is the half the hermetic
// tier cannot express: real replicas claiming at the same instant. Needs
// PostgreSQL — SKIP LOCKED has no SQLite equivalent.
func TestClaimBillingOutbox_ConcurrentReplicasStayDisjoint(t *testing.T) {
	db := setupBillingOutboxPG(t)

	const seeded = 20
	for i := 0; i < seeded; i++ {
		if err := EnqueueSettle(1, int64(7000+i), 1.0); err != nil {
			t.Fatalf("EnqueueSettle: %v", err)
		}
	}

	const replicas = 3
	now := time.Now()
	var (
		mu      sync.Mutex
		claimed = map[int64]int{}
		wg      sync.WaitGroup
		failed  atomic.Value
	)
	start := make(chan struct{})
	for r := 0; r < replicas; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			entries, err := claimBillingOutbox(context.Background(), now)
			if err != nil {
				failed.Store(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, e := range entries {
				claimed[e.ID]++
			}
		}()
	}
	close(start)
	wg.Wait()

	if err, ok := failed.Load().(error); ok && err != nil {
		t.Fatalf("concurrent claim failed: %v", err)
	}

	total := 0
	for id, n := range claimed {
		if n > 1 {
			t.Errorf("entry %d claimed by %d replicas — it would be settled %d times", id, n, n)
		}
		total += n
	}
	if total != seeded {
		t.Errorf("claimed %d entries in total, want %d (every due entry claimed exactly once)", total, seeded)
	}

	var stillPending int64
	db.Model(&entity.BillingOutbox{}).Where("status = ?", outboxStatusPending).Count(&stillPending)
	if stillPending != 0 {
		t.Errorf("%d entries still pending after the claim — the claim was not persisted", stillPending)
	}
}

// seedClaimedEntry inserts an entry already in "processing" whose claim lease
// was last refreshed at leaseAt. updated_at is written with UpdateColumn so
// GORM's autoUpdateTime cannot stamp it with time.Now() instead.
func seedClaimedEntry(t *testing.T, db *gorm.DB, preAuthID int64, leaseAt time.Time) int64 {
	t.Helper()
	entry := entity.BillingOutbox{
		AccountID: 1, PreAuthID: preAuthID, Action: outboxActionSettle,
		Status: outboxStatusProcessing, NextRetry: time.Now().Add(-time.Hour),
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("seed claimed entry: %v", err)
	}
	if err := db.Model(&entity.BillingOutbox{}).Where("id = ?", entry.ID).
		UpdateColumn("updated_at", leaseAt).Error; err != nil {
		t.Fatalf("backdate claim lease: %v", err)
	}
	return entry.ID
}

var billingOutboxPGCounter atomic.Int64

// setupBillingOutboxPG wires the outbox to an isolated PostgreSQL database,
// mirroring the pattern in internal/app/openrouter_pool/pgsetup_test.go (the
// repo helper is package-private). Skips when TEST_POSTGRES_DSN is unset, as the
// rest of the PG tier does.
func setupBillingOutboxPG(t *testing.T) *gorm.DB {
	t.Helper()

	baseDSN := os.Getenv("TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping PostgreSQL claim-contention test")
	}

	dbName := fmt.Sprintf("test_outbox_%d_%d", time.Now().UnixNano(), billingOutboxPGCounter.Add(1))
	adminDB, err := gorm.Open(postgres.Open(baseDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := adminDB.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)).Error; err != nil {
		t.Fatalf("create test database %q: %v", dbName, err)
	}
	if sqlDB, err := adminDB.DB(); err == nil {
		_ = sqlDB.Close()
	}

	dsn := baseDSN
	if u, perr := url.Parse(baseDSN); perr == nil && strings.HasPrefix(baseDSN, "postgres") {
		u.Path = "/" + dbName
		dsn = u.String()
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database %q: %v", dbName, err)
	}

	prev := billingOutboxDB
	if err := InitBillingOutbox(db); err != nil {
		t.Fatalf("InitBillingOutbox: %v", err)
	}

	t.Cleanup(func() {
		billingOutboxDB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		if dropDB, err := gorm.Open(postgres.Open(baseDSN), &gorm.Config{}); err == nil {
			_ = dropDB.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)).Error
			if sqlDB, err := dropDB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
	})

	return db
}
