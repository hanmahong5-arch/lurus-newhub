package repo

import (
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// newDryRunPG opens a GORM handle bound to the PRODUCTION Postgres dialector in
// DryRun mode: it builds real SQL strings but never contacts a server (sql.Open
// is lazy and DryRun skips execution). This lets us assert, hermetically and
// with no live DB, which locking idiom actually emits SELECT ... FOR UPDATE on
// the production driver — the trap being that the GORM v1 idiom
// tx.Set("gorm:query_option", "FOR UPDATE") is a SILENT NO-OP under v2.
func newDryRunPG(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{
		DriverName: "pgx",
		DSN:        "postgres://u:p@127.0.0.1:1/db",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open dry-run PG: %v", err)
	}
	return db
}

// renderSQL builds the SQL for one chained query on a fresh session (a fresh
// session avoids statement pollution across builds) and returns it.
func renderSQL(db *gorm.DB, build func(tx *gorm.DB) *gorm.DB) string {
	return build(db.Session(&gorm.Session{})).Statement.SQL.String()
}

// TestGetChannelForUpdate_EmitsForUpdate exercises the REAL repo function under
// the production dialector and asserts it takes a row lock. GetChannelForUpdate
// discards its chained *gorm.DB, so the built SQL is captured via a callback
// registered after gorm:query (which still runs under DryRun).
func TestGetChannelForUpdate_EmitsForUpdate(t *testing.T) {
	db := newDryRunPG(t)
	var captured string
	if err := db.Callback().Query().After("gorm:query").
		Register("test:capture_sql", func(tx *gorm.DB) {
			captured = tx.Statement.SQL.String()
		}); err != nil {
		t.Fatalf("register capture callback: %v", err)
	}

	if _, err := GetChannelForUpdate(db.Session(&gorm.Session{}), 1); err != nil {
		t.Fatalf("GetChannelForUpdate (dry-run): %v", err)
	}
	if !strings.Contains(captured, "FOR UPDATE") {
		t.Fatalf("GetChannelForUpdate must emit SELECT ... FOR UPDATE, got: %s", captured)
	}
}

// TestRedeemLockedRead_EmitsForUpdate guards the row lock inside repo.Redeem.
// Without FOR UPDATE, two concurrent redeems of the same code both read
// Status==Enabled and both credit quota (a double-spend on the money path). The
// clause built here is byte-identical to the locked read at the Redeem() call
// site (redemption.go); if that call site regresses to the legacy no-op idiom
// this test still passes, so it is paired with the guard below.
func TestRedeemLockedRead_EmitsForUpdate(t *testing.T) {
	db := newDryRunPG(t)
	sql := renderSQL(db, func(tx *gorm.DB) *gorm.DB {
		return tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(`"key" = ?`, "abc").First(&Redemption{})
	})
	if !strings.Contains(sql, "FOR UPDATE") {
		t.Fatalf("redemption locked read must emit FOR UPDATE, got: %s", sql)
	}
}

// TestLegacyQueryOptionIdiom_IsANoOp is the anti-regression guard: it pins the
// exact reason the two call sites were migrated. If a future change reintroduces
// tx.Set("gorm:query_option", "FOR UPDATE") believing it locks, this documents
// (and asserts) that it does NOT emit FOR UPDATE on the production dialector.
func TestLegacyQueryOptionIdiom_IsANoOp(t *testing.T) {
	db := newDryRunPG(t)
	sql := renderSQL(db, func(tx *gorm.DB) *gorm.DB {
		return tx.Set("gorm:query_option", "FOR UPDATE").
			Where("id = ?", 1).First(&Channel{})
	})
	if strings.Contains(sql, "FOR UPDATE") {
		t.Fatalf("legacy gorm:query_option idiom unexpectedly emitted FOR UPDATE; "+
			"if GORM changed, the production call sites can use it again. got: %s", sql)
	}
}
