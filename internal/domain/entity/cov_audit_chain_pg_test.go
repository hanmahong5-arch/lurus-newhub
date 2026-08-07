package entity

// cov_audit_chain_pg_test.go — real-PostgreSQL business-acceptance tests for
// the audit hash chain's concurrency guarantee: chainLink locks the
// per-tenant AuditChainHead row with SELECT ... FOR UPDATE only when
// tx.Name() == "postgres" (see chainLink). This file proves that branch
// actually engages and actually serializes concurrent writers so the chain
// cannot fork — the property the whole tamper-evidence design depends on.
//
// Skips (does not fail) when TEST_POSTGRES_DSN is unset, matching the
// existing pg-integration convention used across this repo (see e.g.
// internal/adapter/repo/pg_harness_honesty_test.go). To avoid any schema/
// search_path complexity across a real multi-connection pool, this uses the
// database's default schema directly with the *real* table names (the repo
// convention for pg-integration tests against a disposable Postgres) and
// scopes every assertion to a random tenant_id so runs never collide with
// each other or with any other suite.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openPGAuditDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping real-Postgres audit chain concurrency test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if db.Name() != "postgres" {
		t.Fatalf("driver Name() = %q, want postgres (test targets the FOR UPDATE branch)", db.Name())
	}
	if err := db.AutoMigrate(&AuditEvent{}, &AuditChainHead{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// testTenantID returns a probabilistically-unique tenant id so concurrent
// test runs (or a leftover row from a prior failed run) never interact with
// this run's assertions. Must fit the tenant_id column's varchar(36) limit.
func testTenantID(t *testing.T) string {
	t.Helper()
	id := fmt.Sprintf("cov%d", time.Now().UnixNano())
	if len(id) > 36 {
		t.Fatalf("generated tenant id %q exceeds varchar(36)", id)
	}
	return id
}

func TestAuditEvent_BeforeCreate_Postgres_LocksAndLinksChain(t *testing.T) {
	db := openPGAuditDB(t)
	tenant := testTenantID(t)
	t.Cleanup(func() {
		db.Where("tenant_id = ?", tenant).Delete(&AuditEvent{})
		db.Where("tenant_id = ?", tenant).Delete(&AuditChainHead{})
	})

	first := &AuditEvent{TenantID: tenant, Action: "token.created"}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first: %v", err)
	}
	if first.PrevHash != "" {
		t.Fatalf("first event PrevHash = %q, want empty", first.PrevHash)
	}
	second := &AuditEvent{TenantID: tenant, Action: "token.deleted"}
	if err := db.Create(second).Error; err != nil {
		t.Fatalf("create second: %v", err)
	}
	if second.PrevHash != first.RowHash {
		t.Fatalf("second.PrevHash = %q, want %q", second.PrevHash, first.RowHash)
	}
}

// TestAuditEvent_BeforeCreate_Postgres_ConcurrentWritersDoNotForkChain fires
// N concurrent inserts for the SAME tenant, from N independent pooled
// connections, and verifies the resulting chain is a single unforked linked
// list: exactly one row has PrevHash=="" (the root), every other row's
// PrevHash matches exactly one other row's RowHash, and the chain head ends
// up pointing at the true tail. A fork (two rows both claiming the same
// PrevHash, or a row whose PrevHash matches no existing RowHash) would mean
// the FOR UPDATE lock failed to serialize writers — exactly the bug class
// this design exists to prevent.
func TestAuditEvent_BeforeCreate_Postgres_ConcurrentWritersDoNotForkChain(t *testing.T) {
	db := openPGAuditDB(t)
	tenant := testTenantID(t)
	t.Cleanup(func() {
		db.Where("tenant_id = ?", tenant).Delete(&AuditEvent{})
		db.Where("tenant_id = ?", tenant).Delete(&AuditChainHead{})
	})

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}
	sqlDB.SetMaxOpenConns(8)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := &AuditEvent{TenantID: tenant, Action: fmt.Sprintf("event.%d", i)}
			errs[i] = db.Create(e).Error
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create %d failed: %v", i, err)
		}
	}

	var events []AuditEvent
	if err := db.Where("tenant_id = ?", tenant).Order("id asc").Find(&events).Error; err != nil {
		t.Fatalf("read back events: %v", err)
	}
	if len(events) != n {
		t.Fatalf("got %d events, want %d", len(events), n)
	}

	rowHashes := make(map[string]int, n)
	roots := 0
	for _, e := range events {
		if e.RowHash == "" {
			t.Fatalf("event id=%d has empty RowHash — chain link failed silently under concurrency", e.ID)
		}
		rowHashes[e.RowHash]++
		if e.PrevHash == "" {
			roots++
		}
	}
	if roots != 1 {
		t.Fatalf("found %d root events (PrevHash==\"\"), want exactly 1 — a fork or a gap occurred", roots)
	}
	for hash, count := range rowHashes {
		if count != 1 {
			t.Fatalf("RowHash %q appears %d times, want 1 (duplicate/forked hash under concurrency)", hash, count)
		}
	}
	// Every non-root PrevHash must resolve to exactly one existing RowHash —
	// i.e. the chain is a single linked list, not a tree with a fork.
	prevCount := map[string]int{}
	for _, e := range events {
		if e.PrevHash == "" {
			continue
		}
		if rowHashes[e.PrevHash] != 1 {
			t.Fatalf("event id=%d PrevHash=%q does not match any existing RowHash — broken link", e.ID, e.PrevHash)
		}
		prevCount[e.PrevHash]++
	}
	for prev, count := range prevCount {
		if count > 1 {
			t.Fatalf("PrevHash %q is claimed by %d different events — the chain forked under concurrency", prev, count)
		}
	}

	var head AuditChainHead
	if err := db.Where("tenant_id = ?", tenant).Take(&head).Error; err != nil {
		t.Fatalf("read chain head: %v", err)
	}
	// The tail of the chain is the one event whose RowHash nobody claims as
	// PrevHash.
	tail := ""
	for hash := range rowHashes {
		if prevCount[hash] == 0 {
			if tail != "" {
				t.Fatalf("found more than one un-succeeded row hash (multiple tails => fork): %q and %q", tail, hash)
			}
			tail = hash
		}
	}
	if head.LastHash != tail {
		t.Fatalf("chain head LastHash = %q, want true chain tail %q", head.LastHash, tail)
	}
}

// TestAuditEvent_BeforeCreate_Postgres_ChainLinkFailureRollsBackAndFallsOpen
// aborts the real Postgres transaction with a genuine SQL error (division by
// zero) immediately before Create() runs. Empirically, PostgreSQL still
// accepts the SAVEPOINT statement issued by BeforeCreate even against an
// already-aborted transaction, but the very next statement inside chainLink
// (ensuring the chain-head row exists) is rejected with "current transaction
// is aborted" — driving BeforeCreate down its chainLink-failure path, where
// it must RollbackTo the savepoint (which must itself succeed here, since the
// savepoint really was established) and fall open with cleared hashes. This
// is a real, unmocked exercise of that recovery path against a live backend,
// distinct from the sqlite "missing table" fail-open test above.
func TestAuditEvent_BeforeCreate_Postgres_ChainLinkFailureRollsBackAndFallsOpen(t *testing.T) {
	db := openPGAuditDB(t)
	tenant := testTenantID(t)

	before := AuditChainFallbacks.Load()
	var loggedMsgs []string
	var mu sync.Mutex
	SetAuditChainLogger(func(msg string) {
		mu.Lock()
		loggedMsgs = append(loggedMsgs, msg)
		mu.Unlock()
	})
	t.Cleanup(func() { SetAuditChainLogger(func(string) {}) })

	e := &AuditEvent{TenantID: tenant, Action: "x"}
	txErr := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT 1/0").Error; err == nil {
			t.Fatal("expected SELECT 1/0 to fail and abort the transaction")
		}
		return tx.Create(e).Error
	})
	if txErr == nil {
		t.Fatal("expected the outer transaction to fail overall — it was aborted by the division-by-zero error before Create's INSERT could run")
	}

	if e.PrevHash != "" || e.RowHash != "" {
		t.Fatalf("chainLink-failure fallback must clear hashes before returning: PrevHash=%q RowHash=%q", e.PrevHash, e.RowHash)
	}
	after := AuditChainFallbacks.Load()
	if after != before+1 {
		t.Fatalf("AuditChainFallbacks = %d, want %d (exactly one fallback recorded)", after, before+1)
	}

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, m := range loggedMsgs {
		if strings.Contains(m, "audit chain fallback") && strings.Contains(m, "aborted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a fallback log mentioning the aborted-transaction error, got %#v", loggedMsgs)
	}
}
