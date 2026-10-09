package repo

// enterprise_045_049_pg_test.go — the enterprise-onboarding schema (migrations
// 045-049) proven against a real PostgreSQL, gated on TEST_POSTGRES_DSN (the CI
// pg-integration job provides one). Hermetic SQLite cannot express any of what
// is asserted here:
//
//   - the PARTIAL unique indexes (uk_tokens_tenant_employee_ref,
//     uk_projects_tenant_external_code, ux_account_key_bindings_live) — GORM
//     tags cannot spell `WHERE ... deleted_at IS NULL`, so a database built by
//     AutoMigrate alone has none of the first two;
//   - CREATE INDEX CONCURRENTLY (047) completing and leaving VALID indexes;
//   - real cross-connection races: a roster upload and an account-key create
//     racing each other must still produce exactly one key.
//
// The database is brought up the way a production boot does it: AutoMigrate
// (migrateDB) first, then the embedded SQL runner from the production
// baseline, then the runner a second time (idempotency).

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"
	"gorm.io/gorm"
)

// bootLikeProduction runs migrateDB + the embedded runner on the SetupTestDB
// database, twice, and returns the raw handle.
func bootLikeProduction(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	if err := migrateDB(); err != nil {
		t.Fatalf("migrateDB (AutoMigrate, first boot): %v", err)
	}
	run := func(label string) {
		r := &migration.Runner{DB: sqlDB, FS: migrations.FS, BaselineThrough: migrationBaselineThrough}
		if err := r.Run(context.Background()); err != nil {
			t.Fatalf("runner (%s): %v", label, err)
		}
	}
	run("first run")
	versions, err := migration.DiscoverVersions(migrations.FS)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var applied int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM public.schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if applied != len(versions) {
		t.Fatalf("schema_migrations has %d rows, want %d", applied, len(versions))
	}
	run("second run (idempotent)")
	var again int
	_ = sqlDB.QueryRow(`SELECT count(*) FROM public.schema_migrations`).Scan(&again)
	if again != applied {
		t.Fatalf("second run changed schema_migrations: %d -> %d", applied, again)
	}
	var head string
	if err := sqlDB.QueryRow(`SELECT max(version) FROM public.schema_migrations`).Scan(&head); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if head != "049_account_key_bindings" {
		t.Fatalf("head migration = %q, want 049_account_key_bindings", head)
	}
	return sqlDB
}

func pgIndexValid(t *testing.T, db *sql.DB, name string) (exists, valid bool) {
	t.Helper()
	err := db.QueryRow(`SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = $1`, name).Scan(&valid)
	if err == sql.ErrNoRows {
		return false, false
	}
	if err != nil {
		t.Fatalf("index %s: %v", name, err)
	}
	return true, valid
}

func newAcctToken(key string, account int64, product string) *Token {
	return &Token{
		UserId: 1, TenantId: "default", Name: product, Key: key, Status: 1, ExpiredTime: -1,
		CreatedTime: 1, AccessedTime: 1, IdentityAccountID: account, SourceProduct: product, Group: "default",
	}
}

// TestIntegration045to049_BootIndexesAndConstraints folds every schema
// assertion into ONE database (each SetupTestDB is a CREATE/DROP DATABASE and
// the repo suite already sits close to its CI budget).
func TestIntegration045to049_BootIndexesAndConstraints(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()
	sqlDB := bootLikeProduction(t)

	// 047: CREATE INDEX CONCURRENTLY completed and left VALID indexes.
	for _, name := range []string{"idx_logs_tenant_project_created", "idx_logs_tenant_employee_created"} {
		if ex, ok := pgIndexValid(t, sqlDB, name); !ex || !ok {
			t.Errorf("047 index %s: exists=%v valid=%v, want both true", name, ex, ok)
		}
	}
	// 045-049 columns exist with the NOT NULL + default shape.
	for _, c := range [][2]string{
		{"tokens", "trusted_identity_headers"}, {"tokens", "employee_ref"}, {"tokens", "source_product"},
		{"logs", "employee_ref"}, {"projects", "external_code"}, {"users", "tenant_role"},
		{"tenants", "wallet_authoritative"}, {"tenants", "payer_user_id"},
		{"tenant_invites", "member_role"}, {"tenant_invites", "project_id"}, {"tenant_invites", "revoked_at"},
	} {
		var nullable string
		err := sqlDB.QueryRow(`SELECT is_nullable FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name=$2`, c[0], c[1]).Scan(&nullable)
		if err != nil {
			t.Errorf("%s.%s missing: %v", c[0], c[1], err)
			continue
		}
		if nullable != "NO" {
			t.Errorf("%s.%s is nullable", c[0], c[1])
		}
	}
	for _, name := range []string{"uk_tokens_tenant_employee_ref", "uk_projects_tenant_external_code", "ux_account_key_bindings_live"} {
		if ex, ok := pgIndexValid(t, sqlDB, name); !ex || !ok {
			t.Errorf("partial unique index %s: exists=%v valid=%v", name, ex, ok)
		}
	}

	// 045 partial unique: tokens (tenant, employee_ref). The tenant plugin
	// refuses tenant-less creates, so go through WithTenantID like production.
	createTok := func(tk *Token) *gorm.DB { return WithTenantID(DB, tk.TenantId).Create(tk) }
	mk := func(key, tenant, ref string) *Token {
		tk := newAcctToken(key, 0, "")
		tk.TenantId, tk.EmployeeRef = tenant, ref
		return tk
	}
	if err := createTok(mk("k-emp-1", "t1", "E1")).Error; err != nil {
		t.Fatalf("first employee_ref: %v", err)
	}
	if err := createTok(mk("k-emp-2", "t1", "E1")).Error; !IsUniqueViolation(err) {
		t.Errorf("duplicate live (tenant, employee_ref) = %v, want unique violation", err)
	}
	if err := createTok(mk("k-emp-3", "t2", "E1")).Error; err != nil {
		t.Errorf("same employee_ref in another tenant must be allowed: %v", err)
	}
	for i := 0; i < 3; i++ { // empty ref is unconstrained
		if err := createTok(mk(fmt.Sprintf("k-empty-%d", i), "t1", "")).Error; err != nil {
			t.Errorf("empty employee_ref #%d: %v", i, err)
		}
	}
	if err := DB.Exec("UPDATE tokens SET deleted_at = now() WHERE key = ?", "k-emp-1").Error; err != nil {
		t.Fatal(err)
	}
	if err := createTok(mk("k-emp-4", "t1", "E1")).Error; err != nil {
		t.Errorf("employee_ref must be reusable after the holder is soft-deleted: %v", err)
	}

	// 045 partial unique: projects (tenant, external_code).
	p := func(name, code string) *entity.Project {
		return &entity.Project{TenantId: "t1", Name: name, ExternalCode: code}
	}
	if err := WithTenantID(DB, "t1").Create(p("a", "D-1")).Error; err != nil {
		t.Fatal(err)
	}
	if err := WithTenantID(DB, "t1").Create(p("b", "D-1")).Error; !IsUniqueViolation(err) {
		t.Errorf("duplicate live external_code = %v, want unique violation", err)
	}
	if err := WithTenantID(DB, "t1").Create(p("c", "")).Error; err != nil {
		t.Errorf("empty external_code: %v", err)
	}
	if err := WithTenantID(DB, "t1").Create(p("d", "")).Error; err != nil {
		t.Errorf("second empty external_code: %v", err)
	}

	// 049 partial unique: account_key_bindings (account, product) while live.
	b := func(tok int64) *AccountKeyBinding {
		return &AccountKeyBinding{IdentityAccountID: 77, Product: "kova", TokenId: tok, TenantId: "default", CreatedAt: 1}
	}
	first := b(1)
	if err := DB.Create(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(b(2)).Error; !IsUniqueViolation(err) {
		t.Errorf("second live binding = %v, want unique violation", err)
	}
	other := &AccountKeyBinding{IdentityAccountID: 77, Product: "lutu", TokenId: 3, TenantId: "default", CreatedAt: 1}
	if err := DB.Create(other).Error; err != nil {
		t.Errorf("another product for the same account must be allowed: %v", err)
	}
	if err := DB.Delete(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(b(4)).Error; err != nil {
		t.Errorf("binding must be re-creatable after the live one is soft-deleted: %v", err)
	}
}

// TestIntegration049_ConcurrentAccountKeyCreateYieldsOneKey replays the create
// transaction of handler.CreateAccountKey WITHOUT its in-process mutex — the
// cross-replica case the mutex cannot cover — and asserts the partial unique
// index leaves exactly one live binding and exactly one surviving token (every
// loser's transaction, token insert included, rolled back).
func TestIntegration049_ConcurrentAccountKeyCreateYieldsOneKey(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()
	bootLikeProduction(t)

	const racers = 12
	const account = int64(9001)
	var won, lost atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			tok := newAcctToken(fmt.Sprintf("race-key-%02d", i), account, "kova")
			binding := &AccountKeyBinding{IdentityAccountID: account, Product: "kova", TenantId: "default", CreatedAt: 1}
			err := DB.Transaction(func(tx *gorm.DB) error {
				if err := WithTenantID(tx, "default").Create(tok).Error; err != nil {
					return err
				}
				binding.TokenId = int64(tok.Id)
				return tx.Create(binding).Error
			})
			switch {
			case err == nil:
				won.Add(1)
			case IsUniqueViolation(err):
				lost.Add(1)
			default:
				t.Errorf("racer %d: unexpected error %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if won.Load() != 1 || lost.Load() != racers-1 {
		t.Fatalf("winners=%d losers=%d, want 1 and %d", won.Load(), lost.Load(), racers-1)
	}
	var bindings, tokens int64
	DB.Model(&AccountKeyBinding{}).Where("identity_account_id = ?", account).Count(&bindings)
	DB.Model(&Token{}).Where("identity_account_id = ? AND source_product = ?", account, "kova").Count(&tokens)
	if bindings != 1 || tokens != 1 {
		t.Fatalf("live bindings=%d tokens=%d, want exactly 1 each (a loser leaked a token)", bindings, tokens)
	}
}

// TestIntegration045_ConcurrentRosterUploadsCreateEachEmployeeOnce races
// CreateTokensBatch (the transaction under BatchCreateTokensV2) with the SAME
// roster: the partial unique index must admit exactly one uploader, and the
// all-or-nothing transaction must leave no half-applied roster behind.
func TestIntegration045_ConcurrentRosterUploadsCreateEachEmployeeOnce(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()
	bootLikeProduction(t)

	const racers = 10
	var won, lost atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var batch []*Token
			// A private ref first, so a loser that applied rows before hitting the
			// shared ref would leave a visible orphan.
			for _, ref := range []string{fmt.Sprintf("own-%02d", i), "E-A", "E-B"} {
				tk := newAcctToken(fmt.Sprintf("roster-%02d-%s", i, ref), 0, "")
				tk.TenantId, tk.EmployeeRef, tk.Name = "tenant-r", ref, ref
				batch = append(batch, tk)
			}
			err := CreateTokensBatch("tenant-r", batch)
			switch {
			case err == nil:
				won.Add(1)
			case IsUniqueViolation(err):
				lost.Add(1)
			default:
				t.Errorf("uploader %d: unexpected error %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if won.Load() != 1 || lost.Load() != racers-1 {
		t.Fatalf("winners=%d losers=%d, want 1 and %d", won.Load(), lost.Load(), racers-1)
	}
	var total, shared int64
	DB.Model(&Token{}).Where("tenant_id = ?", "tenant-r").Count(&total)
	DB.Model(&Token{}).Where("tenant_id = ? AND employee_ref IN ?", "tenant-r", []string{"E-A", "E-B"}).Count(&shared)
	if total != 3 || shared != 2 {
		t.Fatalf("tokens in tenant=%d (shared refs=%d), want 3 and 2 — a losing upload left rows behind", total, shared)
	}
}
