package migration_test

// INTEGRATION coverage for migration 024_audit_events_tamper_evidence against
// a real PostgreSQL, gated on TEST_POSTGRES_DSN (the CI pg-integration job
// provides a disposable Postgres; helpers setupPG/tableExists/scalarInt live
// in runner_pg_test.go / baseline_gaps_pg_test.go, same package).
//
// Proves the DB-level guarantees SQLite cannot: the append-only trigger
// (UPDATE blocked except the GUC-gated PIPL redaction of ip/details, DELETE
// blocked except retention-expired rows, TRUNCATE always blocked), the
// FOR UPDATE-serialized hash chain on the production engine, and the real
// repo.ScrubAuditEventsBatch redaction path passing through the trigger
// without breaking chain verification.

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
)

// setup024 mirrors the production boot order: AutoMigrate creates
// audit_events (incl. the new hash columns from the entity tags), then the
// Runner executes 024 (001–023 baselined) which adds the chain-heads table
// and the append-only triggers. Returns a postgres-dialect gorm handle.
func setup024(t *testing.T) (*sql.DB, *gorm.DB) {
	t.Helper()
	db := setupPG(t)

	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	if err := gdb.AutoMigrate(&entity.AuditEvent{}); err != nil {
		t.Fatalf("AutoMigrate audit_events: %v", err)
	}

	r := &migration.Runner{
		DB:              db,
		FS:              migrations.FS,
		BaselineThrough: "023_add_rate_limit_columns",
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run 024: %v", err)
	}
	return db, gdb
}

func createChained(t *testing.T, gdb *gorm.DB, tenant, action string, retentionUntil int64) *entity.AuditEvent {
	t.Helper()
	e := &entity.AuditEvent{
		TenantID: tenant, Timestamp: time.Now().Unix(), ActorType: "user",
		ActorID: 7, Action: action, Resource: "token", ResourceID: 1,
		IP: "10.9.9.9", Details: `{"pii":"x"}`, RetentionUntil: retentionUntil,
	}
	if err := gdb.Create(e).Error; err != nil {
		t.Fatalf("create chained event: %v", err)
	}
	if e.RowHash == "" {
		t.Fatalf("event %q not chained on PG (row_hash empty) — chain must be real on the production engine, not fail-open", action)
	}
	return e
}

func wantBlocked(t *testing.T, err error, op string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded, want append-only trigger rejection", op)
	}
	if !strings.Contains(err.Error(), "append-only") && !strings.Contains(err.Error(), "ip/details") {
		t.Fatalf("%s error = %v, want append-only guard message", op, err)
	}
}

func TestIntegration024_SchemaObjects(t *testing.T) {
	db, _ := setup024(t)

	if !tableExists(t, db, "audit_chain_heads") {
		t.Error("audit_chain_heads not created by 024")
	}
	for _, col := range []string{"prev_hash", "row_hash"} {
		if scalarInt(t, db, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name='audit_events' AND column_name=$1`, col) != 1 {
			t.Errorf("audit_events.%s missing", col)
		}
	}
	if scalarInt(t, db, `SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		WHERE c.relname='audit_events' AND t.tgname IN ('audit_events_tamper_guard','audit_events_block_truncate')`) != 2 {
		t.Error("append-only triggers missing on audit_events")
	}

	// Idempotency: replaying the full 024 body must be a no-op, not an error.
	body, err := migrations.FS.ReadFile("024_audit_events_tamper_evidence.sql")
	if err != nil {
		t.Fatalf("read 024 body: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
		t.Fatalf("024 re-run not idempotent: %v", err)
	}
}

// TestIntegration024_EmptyDBGuard: on a runner-only database (no AutoMigrate,
// so audit_events is absent) 024 must no-op its column/trigger section via the
// to_regclass guard instead of erroring, while still creating the standalone
// chain-heads table.
func TestIntegration024_EmptyDBGuard(t *testing.T) {
	db := setupPG(t)
	r := &migration.Runner{
		DB:              db,
		FS:              migrations.FS,
		BaselineThrough: "023_add_rate_limit_columns",
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run 024 on empty DB: %v", err)
	}
	if !tableExists(t, db, "audit_chain_heads") {
		t.Error("audit_chain_heads not created on empty DB")
	}
	if tableExists(t, db, "audit_events") {
		t.Error("024 must not create audit_events (AutoMigrate owns it)")
	}
}

func TestIntegration024_TriggerBlocksTamper(t *testing.T) {
	db, gdb := setup024(t)
	ctx := context.Background()

	e := createChained(t, gdb, "t-upd", "trigger.probe", 0)

	// UPDATE of a hash-covered column without the GUC: blocked.
	_, err := db.ExecContext(ctx, `UPDATE audit_events SET action='forged' WHERE id=$1`, e.ID)
	wantBlocked(t, err, "UPDATE action (no GUC)")

	// UPDATE of PII columns without the GUC: still blocked.
	_, err = db.ExecContext(ctx, `UPDATE audit_events SET ip='' WHERE id=$1`, e.ID)
	wantBlocked(t, err, "UPDATE ip (no GUC)")

	// DELETE of a non-expired row (retention_until=0 = keep forever): blocked.
	_, err = db.ExecContext(ctx, `DELETE FROM audit_events WHERE id=$1`, e.ID)
	wantBlocked(t, err, "DELETE non-expired")

	// TRUNCATE: unconditionally blocked.
	_, err = db.ExecContext(ctx, `TRUNCATE audit_events`)
	wantBlocked(t, err, "TRUNCATE")

	// Under the GUC, hash-covered columns are STILL immutable.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.audit_redaction','on', true)`); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE audit_events SET action='forged' WHERE id=$1`, e.ID)
	wantBlocked(t, err, "UPDATE action (GUC on)")
	_ = tx.Rollback()

	// Retention carve-out: an expired row (deadline in the past) is prunable
	// exactly as repo.DeleteExpiredAuditEvents does it.
	expired := createChained(t, gdb, "t-del", "trigger.expired", 1)
	res, err := db.ExecContext(ctx,
		`DELETE FROM audit_events WHERE retention_until > 0 AND retention_until <= $1 AND id = $2`,
		time.Now().Unix(), expired.ID)
	if err != nil {
		t.Fatalf("retention prune blocked: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Errorf("retention prune affected %d rows, want 1", n)
	}
}

func TestIntegration024_RedactionGUCAndChainSurvive(t *testing.T) {
	_, gdb := setup024(t)

	victim := createChained(t, gdb, "t-scrub", "scrub.me", 0)
	createChained(t, gdb, "t-scrub", "scrub.after", 0)

	// The REAL production redaction path: repo.ScrubAuditEventsBatch must set
	// the tx-local GUC itself and pass through the trigger.
	prevDB, prevPG := repo.DB, common.UsingPostgreSQL
	repo.DB, common.UsingPostgreSQL = gdb, true
	defer func() { repo.DB, common.UsingPostgreSQL = prevDB, prevPG }()

	n, err := repo.ScrubAuditEventsBatch(context.Background(), victim.ActorID, 100)
	if err != nil {
		t.Fatalf("ScrubAuditEventsBatch through append-only trigger: %v", err)
	}
	if n != 2 {
		t.Fatalf("scrubbed %d rows, want 2", n)
	}

	var got entity.AuditEvent
	if err := gdb.Where("id = ?", victim.ID).Take(&got).Error; err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if got.IP != "" || got.Details != repo.ErasedMarker {
		t.Errorf("scrub did not land: ip=%q details=%q", got.IP, got.Details)
	}

	// Redaction must NOT read as tampering: the hash chain excludes ip/details.
	res, err := governance.VerifyAuditChain(gdb, governance.ChainVerifyParams{TenantID: "t-scrub"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Checked != 2 || res.FirstBreak != nil || res.LinkBreaks != 0 {
		t.Errorf("chain broken by lawful redaction: %+v", res)
	}
}

func TestIntegration024_ConcurrentChainSerializes(t *testing.T) {
	_, gdb := setup024(t)

	// 10 concurrent writers on one tenant: the FOR UPDATE head lock must
	// serialize them into a single unbroken chain.
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- gdb.Create(&entity.AuditEvent{
				TenantID: "t-conc", Timestamp: time.Now().Unix(),
				ActorType: "user", ActorID: i, Action: "conc.write",
				Resource: "token",
			}).Error
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent create: %v", err)
		}
	}

	res, err := governance.VerifyAuditChain(gdb, governance.ChainVerifyParams{TenantID: "t-conc"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Checked != 10 || res.LegacyRows != 0 || res.FirstBreak != nil ||
		res.LinkBreaks != 0 || res.FirstLinkBreak != nil {
		t.Errorf("concurrent PG chain not clean: %+v", res)
	}
}
