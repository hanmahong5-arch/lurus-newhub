package governance

// chain_verify_test.go — hermetic (SQLite) coverage of the audit
// tamper-evidence hash chain: canonical hash vector, sequential + concurrent
// chaining, tamper detection, legacy-row tolerance, PIPL-redaction
// compatibility, and the fail-open fallback. The DB-level append-only trigger
// is PostgreSQL-only and covered by the pg-integration tests in
// internal/pkg/migration/audit_tamper_pg_test.go.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

var chainDBCounter atomic.Int64

// openChainDB opens an isolated in-memory SQLite DB. MaxOpenConns(1)
// serializes connections so concurrent GORM transactions cannot hit
// SQLITE_BUSY (production concurrency control is the PG row lock, not SQLite).
func openChainDB(t *testing.T, withHeadTable bool) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:auditchain%d?mode=memory&cache=shared", chainDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	models := []interface{}{&entity.AuditEvent{}}
	if withHeadTable {
		models = append(models, &entity.AuditChainHead{})
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func mustCreateEvent(t *testing.T, db *gorm.DB, tenant, action string) *entity.AuditEvent {
	t.Helper()
	e := &entity.AuditEvent{
		TenantID: tenant, Timestamp: 1700000000, ActorType: "admin",
		ActorID: 42, Action: action, Resource: "token", ResourceID: 7,
		RequestID: "req-1",
	}
	if err := db.Create(e).Error; err != nil {
		t.Fatalf("create audit event: %v", err)
	}
	return e
}

// TestComputeAuditRowHash_KnownVector pins the canonical preimage format: the
// expected digest is derived from an explicitly hand-constructed byte string,
// NOT from the production helper, so any silent format drift fails here.
func TestComputeAuditRowHash_KnownVector(t *testing.T) {
	e := &entity.AuditEvent{
		TenantID: "default", Timestamp: 1700000000, ActorType: "admin",
		ActorID: 42, Action: "token.created", Resource: "token",
		ResourceID: 7, RequestID: "req-1", RetentionUntil: 0, PrevHash: "",
		// Excluded fields — must NOT influence the hash:
		IP: "10.0.0.1", Details: `{"pii":"yes"}`, ID: 999,
	}
	canonical := "lurus-audit-chain-v1\n" +
		"7:default\n" +
		"10:1700000000\n" +
		"5:admin\n" +
		"2:42\n" +
		"13:token.created\n" +
		"5:token\n" +
		"1:7\n" +
		"5:req-1\n" +
		"1:0\n" +
		"0:\n"
	sum := sha256.Sum256([]byte(canonical))
	want := hex.EncodeToString(sum[:])

	if got := entity.ComputeAuditRowHash(e); got != want {
		t.Fatalf("ComputeAuditRowHash = %s, want %s (canonical preimage drifted)", got, want)
	}

	// Second link: same content chained onto the first hash — the preimage
	// swaps only the prev_hash field ("64:<hash>\n").
	e2 := *e
	e2.PrevHash = want
	canonical2 := canonical[:len(canonical)-len("0:\n")] + "64:" + want + "\n"
	sum2 := sha256.Sum256([]byte(canonical2))
	if got := entity.ComputeAuditRowHash(&e2); got != hex.EncodeToString(sum2[:]) {
		t.Fatalf("chained hash = %s, want %s", got, hex.EncodeToString(sum2[:]))
	}

	// PII fields are excluded: scrubbing them must not change the hash.
	scrubbed := *e
	scrubbed.IP, scrubbed.Details = "", "ERASED"
	if got := entity.ComputeAuditRowHash(&scrubbed); got != want {
		t.Fatalf("hash covers ip/details — PIPL redaction would break the chain")
	}
}

// TestAuditChain_SequentialLinks proves BeforeCreate links rows and advances
// the per-tenant head.
func TestAuditChain_SequentialLinks(t *testing.T) {
	db := openChainDB(t, true)

	e1 := mustCreateEvent(t, db, "default", "a.one")
	e2 := mustCreateEvent(t, db, "default", "a.two")
	e3 := mustCreateEvent(t, db, "default", "a.three")

	if e1.PrevHash != "" || e1.RowHash == "" {
		t.Fatalf("genesis row: prev=%q row=%q, want prev empty + row set", e1.PrevHash, e1.RowHash)
	}
	if e2.PrevHash != e1.RowHash {
		t.Errorf("e2.PrevHash = %s, want e1.RowHash %s", e2.PrevHash, e1.RowHash)
	}
	if e3.PrevHash != e2.RowHash {
		t.Errorf("e3.PrevHash = %s, want e2.RowHash %s", e3.PrevHash, e2.RowHash)
	}

	var head entity.AuditChainHead
	if err := db.Where("tenant_id = ?", "default").Take(&head).Error; err != nil {
		t.Fatalf("read chain head: %v", err)
	}
	if head.LastHash != e3.RowHash {
		t.Errorf("head.LastHash = %s, want %s", head.LastHash, e3.RowHash)
	}

	res, err := VerifyAuditChain(db, ChainVerifyParams{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Checked != 3 || res.LegacyRows != 0 || res.FirstBreak != nil || res.LinkBreaks != 0 {
		t.Errorf("verify = %+v, want 3 checked / clean", res)
	}
}

// TestAuditChain_Concurrent20 fires 20 concurrent inserts across two tenants
// and asserts the chains neither fork nor break: every row is chained, all
// prev/row hashes are distinct, and verification is clean.
func TestAuditChain_Concurrent20(t *testing.T) {
	db := openChainDB(t, true)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tenant := "tenant-a"
			if i%2 == 1 {
				tenant = "tenant-b"
			}
			e := &entity.AuditEvent{
				TenantID: tenant, Timestamp: 1700000000 + int64(i),
				ActorType: "user", ActorID: i, Action: "concurrent.write",
				Resource: "token", ResourceID: i,
			}
			errs <- db.Create(e).Error
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent create: %v", err)
		}
	}

	var rows []*entity.AuditEvent
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 20 {
		t.Fatalf("rows = %d, want 20", len(rows))
	}
	seenPrev := map[string]int{}
	seenRow := map[string]int{}
	for _, e := range rows {
		if e.RowHash == "" {
			t.Fatalf("row %d fell back to unchained insert under concurrency", e.ID)
		}
		seenPrev[e.TenantID+"|"+e.PrevHash]++
		seenRow[e.RowHash]++
	}
	// A fork = two rows of one tenant claiming the same predecessor.
	for k, n := range seenPrev {
		if n != 1 {
			t.Errorf("chain fork: prev_hash key %q claimed by %d rows", k, n)
		}
	}
	for k, n := range seenRow {
		if n != 1 {
			t.Errorf("duplicate row_hash %q x%d", k, n)
		}
	}

	res, err := VerifyAuditChain(db, ChainVerifyParams{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Checked != 20 || res.LegacyRows != 0 || res.FirstBreak != nil ||
		res.LinkBreaks != 0 || res.FirstLinkBreak != nil {
		t.Errorf("verify = %+v, want 20 checked / clean", res)
	}
}

// TestVerifyAuditChain_TamperFlagged alters one row's content behind the
// chain's back (raw SQL — SQLite has no trigger) and expects first_break to
// point at exactly that row.
func TestVerifyAuditChain_TamperFlagged(t *testing.T) {
	db := openChainDB(t, true)
	mustCreateEvent(t, db, "default", "a.one")
	victim := mustCreateEvent(t, db, "default", "a.two")
	mustCreateEvent(t, db, "default", "a.three")

	if err := db.Exec("UPDATE audit_events SET action = 'a.forged' WHERE id = ?", victim.ID).Error; err != nil {
		t.Fatalf("tamper: %v", err)
	}

	res, err := VerifyAuditChain(db, ChainVerifyParams{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.FirstBreak == nil {
		t.Fatal("tampered row not detected: first_break is nil")
	}
	if res.FirstBreak.ID != victim.ID {
		t.Errorf("first_break.id = %d, want %d", res.FirstBreak.ID, victim.ID)
	}
	if res.FirstBreak.Actual != victim.RowHash {
		t.Errorf("first_break.actual = %s, want stored hash %s", res.FirstBreak.Actual, victim.RowHash)
	}
	if res.FirstBreak.Expected == res.FirstBreak.Actual {
		t.Error("expected == actual on a tampered row")
	}
	if res.HashBreaks != 1 {
		t.Errorf("hash_breaks = %d, want 1", res.HashBreaks)
	}
	// Link state is tracked from STORED hashes, so a content-only tamper
	// must not cascade into link breaks on subsequent rows.
	if res.LinkBreaks != 0 {
		t.Errorf("link_breaks = %d, want 0 for content-only tamper", res.LinkBreaks)
	}
}

// TestVerifyAuditChain_LegacyRowsReported: pre-chain rows (empty hashes) are
// counted as legacy, never as breaks, and do not disturb linkage of chained
// rows around them.
func TestVerifyAuditChain_LegacyRowsReported(t *testing.T) {
	db := openChainDB(t, true)

	for i := 0; i < 2; i++ {
		if err := db.Exec(`INSERT INTO audit_events
			(tenant_id, timestamp, actor_type, actor_id, action, resource, resource_id,
			 details, ip, request_id, retention_until, prev_hash, row_hash)
			VALUES ('default', 1690000000, 'user', 1, 'legacy.write', 'token', 0,
			 '', '', '', 0, '', '')`).Error; err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}
	mustCreateEvent(t, db, "default", "a.one")
	mustCreateEvent(t, db, "default", "a.two")

	res, err := VerifyAuditChain(db, ChainVerifyParams{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.LegacyRows != 2 {
		t.Errorf("legacy_rows = %d, want 2", res.LegacyRows)
	}
	if res.Checked != 2 {
		t.Errorf("checked = %d, want 2", res.Checked)
	}
	if res.FirstBreak != nil || res.LinkBreaks != 0 {
		t.Errorf("legacy rows misreported as breaks: %+v", res)
	}
}

// TestVerifyAuditChain_RedactionDoesNotBreak simulates the PIPL scrub
// (ip/details rewrite) on a chained row: excluded fields, chain stays green.
func TestVerifyAuditChain_RedactionDoesNotBreak(t *testing.T) {
	db := openChainDB(t, true)
	victim := mustCreateEvent(t, db, "default", "a.one")
	mustCreateEvent(t, db, "default", "a.two")

	if err := db.Exec("UPDATE audit_events SET ip = '', details = 'ERASED' WHERE id = ?", victim.ID).Error; err != nil {
		t.Fatalf("scrub: %v", err)
	}

	res, err := VerifyAuditChain(db, ChainVerifyParams{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.FirstBreak != nil || res.LinkBreaks != 0 || res.Checked != 2 {
		t.Errorf("redaction broke the chain: %+v", res)
	}
}

// TestAuditChain_FailOpen_NoHeadTable: with the chain table absent, the event
// must still persist (unchained) and the fallback counter must move — audit
// availability beats chain perfection.
func TestAuditChain_FailOpen_NoHeadTable(t *testing.T) {
	db := openChainDB(t, false) // no audit_chain_heads

	before := entity.AuditChainFallbacks.Load()
	e := mustCreateEvent(t, db, "default", "a.one")
	if e.RowHash != "" || e.PrevHash != "" {
		t.Errorf("fallback row carries hashes: prev=%q row=%q", e.PrevHash, e.RowHash)
	}
	if got := entity.AuditChainFallbacks.Load(); got <= before {
		t.Errorf("AuditChainFallbacks did not increment (%d -> %d)", before, got)
	}

	var count int64
	if err := db.Model(&entity.AuditEvent{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("event not persisted on fallback: count=%d err=%v", count, err)
	}
}

// TestVerifyAuditChain_CursorPagination pages a 5-row chain 2 rows at a time
// and confirms cross-page linkage via the prior-hash seeding.
func TestVerifyAuditChain_CursorPagination(t *testing.T) {
	db := openChainDB(t, true)
	for i := 0; i < 5; i++ {
		mustCreateEvent(t, db, "default", fmt.Sprintf("a.%d", i))
	}

	var cursor int64
	totalChecked := 0
	pages := 0
	for {
		res, err := VerifyAuditChain(db, ChainVerifyParams{AfterID: cursor, Limit: 2})
		if err != nil {
			t.Fatalf("verify page: %v", err)
		}
		if res.FirstBreak != nil || res.LinkBreaks != 0 {
			t.Fatalf("break across pages: %+v", res)
		}
		totalChecked += res.Checked
		pages++
		if res.NextCursor == 0 {
			break
		}
		cursor = res.NextCursor
	}
	if totalChecked != 5 {
		t.Errorf("total checked = %d, want 5", totalChecked)
	}
	if pages < 3 {
		t.Errorf("pages = %d, want >= 3 with limit 2", pages)
	}
}

// TestVerifyAuditChain_TenantFilterAndTimeWindow: tenant filter verifies one
// chain only; a time window disables link checks but still verifies content.
func TestVerifyAuditChain_TenantFilterAndTimeWindow(t *testing.T) {
	db := openChainDB(t, true)
	mustCreateEvent(t, db, "tenant-a", "a.one")
	mustCreateEvent(t, db, "tenant-b", "b.one")
	mustCreateEvent(t, db, "tenant-a", "a.two")

	res, err := VerifyAuditChain(db, ChainVerifyParams{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("verify tenant-a: %v", err)
	}
	if res.Checked != 2 || res.FirstBreak != nil || res.LinkBreaks != 0 {
		t.Errorf("tenant-a verify = %+v, want 2 checked / clean", res)
	}

	res, err = VerifyAuditChain(db, ChainVerifyParams{StartTime: 1})
	if err != nil {
		t.Fatalf("verify time window: %v", err)
	}
	if !res.LinkChecksSkipped {
		t.Error("time-filtered verify must mark link_checks_skipped")
	}
	if res.Checked != 3 || res.FirstBreak != nil {
		t.Errorf("time-window verify = %+v, want 3 checked / no content breaks", res)
	}
}
