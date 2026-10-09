package repo

// enterprise_050_051_pg_test.go - migrations 050 (relay data control) and 051
// (logs.channel_key_idx) proven against a real PostgreSQL, gated on
// TEST_POSTGRES_DSN. Hermetic SQLite cannot show what matters here: the column
// shapes the SQL migration and the GORM tags must agree on (NOT NULL, BIGINT,
// defaults), the unique template-name index, and the per-key GROUP BY on a
// real planner. The database is brought up like a production boot
// (bootLikeProduction: AutoMigrate, the embedded runner, the runner again).

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestIntegration050to051_BootShapeAndBehaviour(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()
	sqlDB := bootLikeProduction(t)

	// Column shape: NOT NULL, the type and the default the migration promises.
	for _, c := range []struct{ table, col, typ, def string }{
		{"tenants", "content_retention", "character varying", "''::character varying"},
		{"tokens", "content_retention", "character varying", "''::character varying"},
		{"logs", "channel_key_idx", "bigint", "'-1'::integer"},
		{"content_rules", "ordinal", "bigint", "0"},
		{"content_rules", "enabled", "boolean", "true"},
		{"channel_override_templates", "version", "bigint", "1"},
		{"channel_template_applications", "template_version", "bigint", "0"},
	} {
		var nullable, typ, def string
		err := sqlDB.QueryRow(`SELECT is_nullable, data_type, COALESCE(column_default, '')
			FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name=$2`, c.table, c.col).Scan(&nullable, &typ, &def)
		if err != nil {
			t.Errorf("%s.%s missing: %v", c.table, c.col, err)
			continue
		}
		if nullable != "NO" || typ != c.typ {
			t.Errorf("%s.%s: nullable=%s type=%s, want NO / %s", c.table, c.col, nullable, typ, c.typ)
		}
		if def == "" {
			t.Errorf("%s.%s has no column default", c.table, c.col)
		}
	}
	for _, name := range []string{"idx_content_rules_scope_tenant", "ux_channel_override_templates_name", "idx_channel_template_applications_channel"} {
		if ex, ok := pgIndexValid(t, sqlDB, name); !ex || !ok {
			t.Errorf("index %s: exists=%v valid=%v", name, ex, ok)
		}
	}

	// Template names are unique at the database, not only in the handler.
	if _, err := sqlDB.Exec(`INSERT INTO channel_override_templates (name) VALUES ('dup')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO channel_override_templates (name) VALUES ('dup')`); !IsUniqueViolation(err) {
		t.Errorf("duplicate template name = %v, want unique violation", err)
	}

	// Content rules: validation, per-scope limit and tenant confinement on PG.
	bad := builtinRule("tenant", "t1", 0, "mask", "enforce", "nope")
	if err := CreateContentRule(bad); !errors.Is(err, contentpolicy.ErrInvalidRule) {
		t.Fatalf("bad builtin accepted: %v", err)
	}
	r := builtinRule("tenant", "t1", 0, "mask", "observe", "email")
	if err := CreateContentRule(r); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if _, err := GetContentRule(r.Id, "tenant", "t2"); err == nil {
		t.Error("a tenant rule was readable under another tenant")
	}
	if _, err := GetContentRule(r.Id, "platform", ""); err == nil {
		t.Error("a tenant rule was readable under the platform scope")
	}

	// Retention round trip through the real columns.
	if err := WithTenantID(DB, "t1").Create(&Tenant{Id: "t1", Slug: "t1-slug", Name: "t1", Status: 1}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := SetTenantContentRetention("t1", contentpolicy.RetentionMetadataOnly); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	if got, err := GetTenantContentRetention("t1"); err != nil || got != contentpolicy.RetentionMetadataOnly {
		t.Errorf("tenant retention = %q, %v", got, err)
	}

	// 051: an unstamped log row takes the -1 default; per-key aggregation
	// groups on the real column.
	now := common.GetTimestamp()
	seedUsageLog(t, 1, 0, LogTypeConsume, now-10, 100, 50, 10, 300)
	seedUsageLog(t, 1, 0, LogTypeError, now-20, 0, 0, 0, 0)
	seedUsageLog(t, 1, 1, LogTypeConsume, now-30, 7, 3, 2, 60)
	if err := LOG_DB.Create(&Log{UserId: 1, Type: LogTypeConsume, CreatedAt: now - 5, ChannelId: 1, Quota: 1}).Error; err != nil {
		t.Fatal(err)
	}
	var unstamped int64
	if err := sqlDB.QueryRow(`SELECT channel_key_idx FROM logs WHERE channel_id = 1 AND quota = 1`).Scan(&unstamped); err != nil || unstamped != -1 {
		t.Errorf("unstamped log channel_key_idx = %d (%v), want -1", unstamped, err)
	}
	rows, err := AggregateChannelUsage([]int{1}, now-3600, true)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	byKey := map[int64]ChannelUsageRow{}
	for _, row := range rows {
		byKey[row.KeyIdx] = row
	}
	if k := byKey[0]; k.Requests != 2 || k.Errors != 1 || k.CostCNY4 != 300 {
		t.Errorf("key 0 = %+v", k)
	}
	if k := byKey[1]; k.Requests != 1 || k.CostCNY4 != 60 {
		t.Errorf("key 1 = %+v", k)
	}
	if k, ok := byKey[NoChannelKeyIdx]; !ok || k.Requests != 1 {
		t.Errorf("unstamped bucket = %+v (present=%v)", k, ok)
	}
}
