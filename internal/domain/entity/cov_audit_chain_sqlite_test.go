package entity

// cov_audit_chain_sqlite_test.go — hermetic (no external DB required)
// business-acceptance tests for AuditEvent.BeforeCreate / chainLink, the
// per-tenant tamper-evidence hash-chain linking hook. Uses the glebarez
// pure-Go SQLite driver (same hermetic tier used elsewhere in this repo)
// so these run everywhere, and exercises:
//   - the happy path: first event in a tenant's chain has empty PrevHash;
//     the second event's PrevHash equals the first event's RowHash.
//   - two tenants' chains never interleave (separate PrevHash/RowHash
//     sequences keyed by tenant_id).
//   - the pre-hashed-row bypass (restore tooling / explicit-chain tests).
//   - the fail-open contract: if the chain-head table is unavailable, the
//     event still inserts (audit availability beats chain completeness),
//     with empty PrevHash/RowHash and an observable fallback counter+log.
//
// tx.Name() on this driver is "sqlite" (not "postgres"), so these tests
// exercise the branch that skips the FOR UPDATE row lock — see
// cov_audit_chain_pg_test.go for the postgres-locking branch and the
// concurrent-writer no-fork guarantee.

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openSQLiteAuditDB(t *testing.T, migrateChainHead bool) *gorm.DB {
	t.Helper()
	// Each test gets its own uniquely-named shared-cache in-memory database:
	// shared-cache is required so all pooled connections see the same schema
	// (a plain ":memory:" DSN gives every connection its own private DB), but
	// the name must be unique per test or sqlite's shared cache would let an
	// earlier test's AutoMigrate leak a table into a later test that relies
	// on it being absent (the fail-open test below).
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if db.Name() != "sqlite" {
		t.Fatalf("driver Name() = %q, want sqlite (test assumes the non-postgres branch)", db.Name())
	}
	if err := db.AutoMigrate(&AuditEvent{}); err != nil {
		t.Fatalf("automigrate AuditEvent: %v", err)
	}
	if migrateChainHead {
		if err := db.AutoMigrate(&AuditChainHead{}); err != nil {
			t.Fatalf("automigrate AuditChainHead: %v", err)
		}
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

func TestAuditEvent_BeforeCreate_FirstEventInChainHasEmptyPrevHash(t *testing.T) {
	db := openSQLiteAuditDB(t, true)

	e := &AuditEvent{TenantID: "t-first", ActorType: "user", Action: "token.created", Resource: "token"}
	if err := db.Create(e).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.PrevHash != "" {
		t.Fatalf("first event PrevHash = %q, want empty (no predecessor)", e.PrevHash)
	}
	if e.RowHash == "" || len(e.RowHash) != 64 {
		t.Fatalf("first event RowHash = %q, want 64-hex-char digest", e.RowHash)
	}
	wantHash := ComputeAuditRowHash(&AuditEvent{
		TenantID: e.TenantID, Timestamp: e.Timestamp, ActorType: e.ActorType, ActorID: e.ActorID,
		Action: e.Action, Resource: e.Resource, ResourceID: e.ResourceID, RequestID: e.RequestID,
		RetentionUntil: e.RetentionUntil, PrevHash: "",
	})
	if e.RowHash != wantHash {
		t.Fatalf("RowHash = %q, want %q (matching ComputeAuditRowHash on the persisted fields)", e.RowHash, wantHash)
	}

	var head AuditChainHead
	if err := db.Where("tenant_id = ?", "t-first").Take(&head).Error; err != nil {
		t.Fatalf("read chain head: %v", err)
	}
	if head.LastHash != e.RowHash {
		t.Fatalf("chain head LastHash = %q, want %q (advanced to the new row's hash)", head.LastHash, e.RowHash)
	}
}

func TestAuditEvent_BeforeCreate_SecondEventLinksToFirst(t *testing.T) {
	db := openSQLiteAuditDB(t, true)

	first := &AuditEvent{TenantID: "t-chain", ActorType: "user", Action: "token.created", Resource: "token"}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first: %v", err)
	}
	second := &AuditEvent{TenantID: "t-chain", ActorType: "user", Action: "token.deleted", Resource: "token"}
	if err := db.Create(second).Error; err != nil {
		t.Fatalf("create second: %v", err)
	}

	if second.PrevHash != first.RowHash {
		t.Fatalf("second.PrevHash = %q, want first.RowHash %q (chain must link)", second.PrevHash, first.RowHash)
	}
	if second.RowHash == first.RowHash {
		t.Fatal("second event produced the same RowHash as the first — chain did not advance")
	}

	var head AuditChainHead
	if err := db.Where("tenant_id = ?", "t-chain").Take(&head).Error; err != nil {
		t.Fatalf("read chain head: %v", err)
	}
	if head.LastHash != second.RowHash {
		t.Fatalf("chain head = %q, want tail of chain %q", head.LastHash, second.RowHash)
	}
}

func TestAuditEvent_BeforeCreate_DifferentTenantsDoNotInterleave(t *testing.T) {
	db := openSQLiteAuditDB(t, true)

	a1 := &AuditEvent{TenantID: "tenant-a", Action: "a.first"}
	b1 := &AuditEvent{TenantID: "tenant-b", Action: "b.first"}
	if err := db.Create(a1).Error; err != nil {
		t.Fatalf("create a1: %v", err)
	}
	if err := db.Create(b1).Error; err != nil {
		t.Fatalf("create b1: %v", err)
	}
	a2 := &AuditEvent{TenantID: "tenant-a", Action: "a.second"}
	if err := db.Create(a2).Error; err != nil {
		t.Fatalf("create a2: %v", err)
	}

	// tenant-a's second event must chain to tenant-a's first — never to
	// tenant-b's, even though b1 was inserted in between.
	if a2.PrevHash != a1.RowHash {
		t.Fatalf("a2.PrevHash = %q, want a1.RowHash %q (cross-tenant leak)", a2.PrevHash, a1.RowHash)
	}
	if a2.PrevHash == b1.RowHash {
		t.Fatal("a2 chained to tenant-b's event — tenant isolation violated")
	}
}

func TestAuditEvent_BeforeCreate_PreHashedRowIsTrustedNotRelinked(t *testing.T) {
	db := openSQLiteAuditDB(t, true)

	// Seed the chain so a "real" link exists to relink to, then verify the
	// pre-hashed insert deliberately does NOT link to it.
	seed := &AuditEvent{TenantID: "t-restore", Action: "seed"}
	if err := db.Create(seed).Error; err != nil {
		t.Fatalf("create seed: %v", err)
	}

	restored := &AuditEvent{
		TenantID: "t-restore", Action: "restored.row",
		PrevHash: "caller-supplied-prev",
		RowHash:  "caller-supplied-row-hash-should-be-trusted",
	}
	if err := db.Create(restored).Error; err != nil {
		t.Fatalf("create restored: %v", err)
	}
	if restored.PrevHash != "caller-supplied-prev" || restored.RowHash != "caller-supplied-row-hash-should-be-trusted" {
		t.Fatalf("pre-hashed row was mutated: PrevHash=%q RowHash=%q", restored.PrevHash, restored.RowHash)
	}

	// And the chain head must NOT have been advanced by the trusted insert
	// (BeforeCreate returns early before touching the head row).
	var head AuditChainHead
	if err := db.Where("tenant_id = ?", "t-restore").Take(&head).Error; err != nil {
		t.Fatalf("read chain head: %v", err)
	}
	if head.LastHash != seed.RowHash {
		t.Fatalf("chain head = %q, want unchanged at seed's hash %q (pre-hashed insert must not advance the head)", head.LastHash, seed.RowHash)
	}
}

func TestAuditEvent_BeforeCreate_FailOpenWhenChainHeadTableMissing(t *testing.T) {
	// migrateChainHead=false: audit_chain_heads does not exist, so
	// chainLink's ensure-head-row Create() must fail. BeforeCreate must
	// roll back to the savepoint and still let the event insert succeed,
	// unchained (fail-open: availability beats chain completeness).
	db := openSQLiteAuditDB(t, false)

	before := AuditChainFallbacks.Load()
	var loggedMsgs []string
	var mu sync.Mutex
	SetAuditChainLogger(func(msg string) {
		mu.Lock()
		loggedMsgs = append(loggedMsgs, msg)
		mu.Unlock()
	})
	t.Cleanup(func() { SetAuditChainLogger(func(string) {}) })

	e := &AuditEvent{TenantID: "t-failopen", Action: "token.created"}
	if err := db.Create(e).Error; err != nil {
		t.Fatalf("Create must succeed even when chaining fails (fail-open): %v", err)
	}
	if e.PrevHash != "" || e.RowHash != "" {
		t.Fatalf("fail-open row got PrevHash=%q RowHash=%q, want both empty (legacy/unchained marker)", e.PrevHash, e.RowHash)
	}

	after := AuditChainFallbacks.Load()
	if after != before+1 {
		t.Fatalf("AuditChainFallbacks = %d, want %d (exactly one fallback recorded)", after, before+1)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(loggedMsgs) != 1 {
		t.Fatalf("logger received %d messages, want exactly 1: %#v", len(loggedMsgs), loggedMsgs)
	}
	if !strings.Contains(loggedMsgs[0], "audit chain fallback") {
		t.Fatalf("logged message = %q, want it to mention the fallback", loggedMsgs[0])
	}

	// The row itself must still be durably persisted (this is the whole
	// point of fail-open: don't drop the audit event).
	var count int64
	if err := db.Model(&AuditEvent{}).Where("tenant_id = ?", "t-failopen").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit_events row count = %d, want 1 (fail-open must not drop the event)", count)
	}
}

func TestSetAuditChainLogger_NilIsNoop(t *testing.T) {
	// Install a real logger first, then verify passing nil does not clear it
	// (the implementation explicitly guards `if f != nil`).
	called := false
	SetAuditChainLogger(func(string) { called = true })
	SetAuditChainLogger(nil)
	auditChainFallback("probe")
	if !called {
		t.Fatal("SetAuditChainLogger(nil) cleared the previously installed logger; expected the guard to keep it")
	}
	SetAuditChainLogger(func(string) {}) // reset for other tests in this process
}
