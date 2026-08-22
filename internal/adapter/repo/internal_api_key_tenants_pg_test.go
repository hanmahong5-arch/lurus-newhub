package repo

// internal_api_key_tenants_pg_test.go — real-Postgres coverage for
// ListInternalKeyTenants / GrantInternalKeyTenant / RevokeInternalKeyTenant
// (internal_api_key.go), gated on TEST_POSTGRES_DSN.
//
// internal_api_key_tenants has NO GORM entity backing it (see the comment on
// InternalKeyTenantGrant in internal_api_key.go), so SetupTestDB's AutoMigrate
// list never creates it — every one of these three functions is hand-written
// SQL that until now had zero PG coverage. The only existing test
// (internal/adapter/handler/internal_api_key_admin_v2_test.go) runs against a
// hand-copied SQLite DDL whose created_at column is a bare TIMESTAMP, not
// production's TIMESTAMPTZ — precisely the "engine-parsed SQL needs a PG
// test" gap this repo has been burned by before. This file instead executes
// migration 013's REAL body via migrations.FS, so the schema under test is
// byte-for-byte what production runs, not a re-typed approximation.
import (
	"testing"

	"github.com/LurusTech/lurus-hub/migrations"
)

// setupInternalKeyTenantsPG wires an isolated PG database (SetupTestDB, which
// AutoMigrates entity.InternalApiKey among others) and then lays down
// internal_api_key_tenants by executing migration 013's actual SQL file —
// the same bytes the production Runner applies — so created_at is a real
// TIMESTAMPTZ and the PRIMARY KEY is the real composite (api_key_id, tenant_id).
func setupInternalKeyTenantsPG(t *testing.T) {
	t.Helper()
	SetupTestDB(t)

	body, err := migrations.FS.ReadFile("013_create_internal_api_key_tenants.sql")
	if err != nil {
		t.Fatalf("read migration 013 body: %v", err)
	}
	if err := DB.Exec(string(body)).Error; err != nil {
		t.Fatalf("apply migration 013 DDL: %v", err)
	}
}

// TestInternalKeyTenants_RealPG_GrantListRevokeLifecycle walks the full
// whitelist lifecycle against real Postgres: Grant, List (exactly one row),
// a replayed Grant (idempotent — swallowed by the REAL primary-key unique
// violation on (api_key_id, tenant_id), not a SQLite stand-in for one),
// Revoke, List (zero rows), and a replayed Revoke (idempotent no-op).
func TestInternalKeyTenants_RealPG_GrantListRevokeLifecycle(t *testing.T) {
	setupInternalKeyTenantsPG(t)

	_, apiKey, err := CreateInternalApiKey(
		"pg-lifecycle-key", []string{ScopeProvisioning}, 1, 0, "PG lifecycle test key")
	if err != nil {
		t.Fatalf("CreateInternalApiKey: %v", err)
	}
	const tenantID = "tenant-pg-lifecycle"

	// Grant.
	if err := GrantInternalKeyTenant(apiKey.Id, tenantID); err != nil {
		t.Fatalf("GrantInternalKeyTenant: %v", err)
	}

	// List — exactly one row.
	grants, err := ListInternalKeyTenants(apiKey.Id)
	if err != nil {
		t.Fatalf("ListInternalKeyTenants after grant: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("grants after 1 Grant = %d, want 1: %+v", len(grants), grants)
	}
	if grants[0].TenantID != tenantID {
		t.Fatalf("grants[0].TenantID = %q, want %q", grants[0].TenantID, tenantID)
	}
	if grants[0].CreatedAt.IsZero() {
		t.Error("grants[0].CreatedAt is zero — TIMESTAMPTZ DEFAULT NOW() did not populate")
	}

	// Grant replay — idempotent: the real PG PRIMARY KEY (api_key_id,
	// tenant_id) violation must be swallowed by isUniqueViolation, not
	// surfaced as an error, and must not insert a second row.
	if err := GrantInternalKeyTenant(apiKey.Id, tenantID); err != nil {
		t.Fatalf("GrantInternalKeyTenant replay must be idempotent, got error: %v", err)
	}
	grants, err = ListInternalKeyTenants(apiKey.Id)
	if err != nil {
		t.Fatalf("ListInternalKeyTenants after replay: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("grants after replayed Grant = %d, want 1 (no duplicate row)", len(grants))
	}

	// Revoke.
	if err := RevokeInternalKeyTenant(apiKey.Id, tenantID); err != nil {
		t.Fatalf("RevokeInternalKeyTenant: %v", err)
	}
	grants, err = ListInternalKeyTenants(apiKey.Id)
	if err != nil {
		t.Fatalf("ListInternalKeyTenants after revoke: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("grants after Revoke = %d, want 0: %+v", len(grants), grants)
	}

	// Revoke replay — idempotent no-op on an already-absent grant.
	if err := RevokeInternalKeyTenant(apiKey.Id, tenantID); err != nil {
		t.Fatalf("RevokeInternalKeyTenant replay must be idempotent, got error: %v", err)
	}
}
