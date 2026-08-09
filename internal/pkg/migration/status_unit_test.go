package migration_test

// Hermetic coverage for PendingVersions' argument guards. The query paths need
// a real PostgreSQL and live in status_pg_test.go.

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
)

func TestPendingVersions_NilArgumentsAreRejected(t *testing.T) {
	fsys := fstest.MapFS{"021_x.sql": &fstest.MapFile{Data: []byte(`SELECT 1;`)}}

	if _, _, err := migration.PendingVersions(context.Background(), nil, fsys, ""); err == nil {
		t.Error("PendingVersions(nil db) = nil error, want a rejection")
	}
	if _, _, err := migration.PendingVersions(context.Background(), &sql.DB{}, nil, ""); err == nil {
		t.Error("PendingVersions(nil fs) = nil error, want a rejection")
	}
}
