package repo

// enterprise_052_pg_test.go - migration 052 against a real PostgreSQL (gated
// on TEST_POSTGRES_DSN like its siblings): the GORM tags on entity.LogBody /
// Tenant.SedimentationConsent must agree with the SQL file after a production
// style boot, and the archive round-trips through the real columns.

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestIntegration052_BootShapeAndRoundTrip(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()
	sqlDB := bootLikeProduction(t)

	for _, c := range []struct{ table, col, typ string }{
		{"tenants", "sedimentation_consent", "boolean"},
		{"log_bodies", "request_id", "character varying"},
		{"log_bodies", "tenant_id", "character varying"},
		{"log_bodies", "user_id", "bigint"},
		{"log_bodies", "token_id", "bigint"},
		{"log_bodies", "created_at", "bigint"},
		{"log_bodies", "expires_at", "bigint"},
		{"log_bodies", "request_body", "text"},
		{"log_bodies", "response_text", "text"},
		{"log_bodies", "response_captured", "boolean"},
		{"log_bodies", "truncated", "boolean"},
	} {
		var nullable, typ string
		err := sqlDB.QueryRow(`SELECT is_nullable, data_type FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name=$2`, c.table, c.col).Scan(&nullable, &typ)
		if err != nil {
			t.Errorf("%s.%s missing: %v", c.table, c.col, err)
			continue
		}
		if nullable != "NO" || typ != c.typ {
			t.Errorf("%s.%s: nullable=%s type=%s, want NO / %s", c.table, c.col, nullable, typ, c.typ)
		}
	}
	for _, name := range []string{"idx_log_bodies_tenant_created", "idx_log_bodies_request_id", "idx_log_bodies_expires_at"} {
		if ex, ok := pgIndexValid(t, sqlDB, name); !ex || !ok {
			t.Errorf("index %s: exists=%v valid=%v", name, ex, ok)
		}
	}

	if err := WithTenantID(DB, "t1").Create(&Tenant{Id: "t1", Slug: "t1-slug", Name: "t1", Status: 1}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if on, err := GetTenantSedimentationConsent("t1"); err != nil || on {
		t.Fatalf("new tenant consent = %v, %v; want false", on, err)
	}
	if err := SetTenantSedimentationConsent("t1", true); err != nil {
		t.Fatal(err)
	}
	now := common.GetTimestamp()
	if err := DB.Create(&LogBody{RequestId: "r1", TenantId: "t1", CreatedAt: now, ExpiresAt: now + 60, RequestBody: "a\u4e2db"}).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	if b, err := GetLogBody("t1", "r1"); err != nil || b.RequestBody != "a\u4e2db" {
		t.Fatalf("read back: %v %+v", err, b)
	}
	if _, err := GetLogBody("t2", "r1"); !errors.Is(err, ErrLogBodyNotFound) {
		t.Fatalf("foreign tenant read = %v", err)
	}
}
