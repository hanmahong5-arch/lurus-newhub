package repo

// project_pg_test.go — the subset of repo/project.go behaviour that must be
// re-proven on the PRODUCTION dialect, gated on TEST_POSTGRES_DSN.
//
// The bulk of the coverage lives in the hermetic project_test.go (SQLite) so
// it actually counts toward the CI coverage gate, which runs `go test -short`
// with no DSN. What is repeated HERE is only what a dialect difference could
// plausibly break, and what would be a security incident if it did: the
// tenant clauses that keep one customer's project ids and names away from
// another, and the transactional token detach on delete.
//
// NOT covered here: the partial unique index. SetupTestDB builds its schema
// with AutoMigrate, which cannot express `WHERE deleted_at IS NULL`, so that
// index does not exist in this harness at all — it is proven against the real
// DDL in internal/pkg/migration/projects_pg_test.go.

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// TestIntegrationProject_RealPG_TenantClausesAndDetach folds every real-PG
// assertion into ONE SetupTestDB. Each call to that helper CREATEs and DROPs a
// throwaway database; on the pg-integration runner the repo suite already sits
// close to its 15m budget, so a second harness spin-up buys nothing that this
// single one does not already prove.
func TestIntegrationProject_RealPG_TenantClausesAndDetach(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	mine, err := CreateProject("tenant-a", "Marketing", "")
	if err != nil {
		t.Fatalf("CreateProject(tenant-a): %v", err)
	}
	theirs, err := CreateProject("tenant-b", "Project Zeus", "")
	if err != nil {
		t.Fatalf("CreateProject(tenant-b): %v", err)
	}

	// ── tenant clause on the by-id read ──────────────────────────────────────
	if _, err := GetProjectByID("tenant-b", mine.Id); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("GetProjectByID across tenants = %v, want ErrProjectNotFound", err)
	}

	// ── tenant clause on name resolution (customer-confidential strings) ─────
	names, err := ResolveProjectNames("tenant-a", []int{mine.Id, theirs.Id})
	if err != nil {
		t.Fatalf("ResolveProjectNames: %v", err)
	}
	if names[mine.Id] != "Marketing" {
		t.Errorf("own project resolved to %q, want Marketing", names[mine.Id])
	}
	if leaked, ok := names[theirs.Id]; ok {
		t.Fatalf("LEAK on PostgreSQL: another tenant's project name %q resolved", leaked)
	}

	// ── transactional token detach on delete ─────────────────────────────────
	own := &Token{UserId: 1, TenantId: "tenant-a", Key: "pg-mine-000000000000000000000000000000000000", Name: "mine", ProjectId: mine.Id}
	// Same numeric project_id under a different tenant: there is no foreign
	// key, so this collision is normal and must survive untouched.
	foreign := &Token{UserId: 2, TenantId: "tenant-b", Key: "pg-foreign-000000000000000000000000000000000", Name: "foreign", ProjectId: mine.Id}
	for _, tok := range []*Token{own, foreign} {
		if err := DB.Create(tok).Error; err != nil {
			t.Fatalf("seed token %q: %v", tok.Name, err)
		}
	}

	if _, err := SoftDeleteProject("tenant-a", mine.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}

	var reread, untouched Token
	if err := DB.Where("id = ?", own.Id).Take(&reread).Error; err != nil {
		t.Fatalf("reread own token: %v", err)
	}
	if reread.ProjectId != entity.ProjectUnassigned {
		t.Errorf("own token project_id = %d, want 0 — delete must detach in the same transaction", reread.ProjectId)
	}
	if err := DB.Where("id = ?", foreign.Id).Take(&untouched).Error; err != nil {
		t.Fatalf("reread foreign token: %v", err)
	}
	if untouched.ProjectId != mine.Id {
		t.Errorf("another tenant's token was detached (project_id = %d, want %d)", untouched.ProjectId, mine.Id)
	}

	// ── soft-deleted rows still resolve, so reports stay complete ────────────
	afterDelete, err := ResolveProjectNames("tenant-a", []int{mine.Id})
	if err != nil {
		t.Fatalf("ResolveProjectNames after delete: %v", err)
	}
	if afterDelete[mine.Id] != "Marketing" {
		t.Errorf("soft-deleted project name = %q, want Marketing", afterDelete[mine.Id])
	}
}
