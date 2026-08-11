package repo

// project_test.go — HERMETIC (in-memory SQLite) coverage for repo/project.go.
//
// Deliberately in the hermetic tier, not the *_pg_test.go tier: the CI
// coverage gate runs `go test -short` with no TEST_POSTGRES_DSN, so anything
// behind SetupTestDB contributes ZERO statements to the
// internal/adapter/repo threshold. project.go is ~120 statements — parking its
// tests behind a skipped DSN would mathematically push the package under its
// baseline. The PostgreSQL-dialect behaviour that SQLite cannot prove (the
// partial unique index) is covered separately in project_pg_test.go and
// internal/pkg/migration/projects_pg_test.go.
//
// REDIS IS OFF here (setupSQLiteDB sets common.RedisEnabled = false) and that
// is load-bearing, not incidental: Token.Update is write-through to the Redis
// token cache and GetTokenByKey prefers the cache, so a test that
// re-attributes a token and immediately asserts the result would be reading
// the cache rather than the column it claims to test.

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func mustCreateProject(t *testing.T, tenantID, name string) *entity.Project {
	t.Helper()
	p, err := CreateProject(tenantID, name, "desc of "+name)
	if err != nil {
		t.Fatalf("CreateProject(%q, %q): %v", tenantID, name, err)
	}
	return p
}

func TestProject_CRUDLifecycle(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")
	if p.Id <= 0 {
		t.Fatalf("CreateProject returned id %d, want a positive generated id", p.Id)
	}
	if p.TenantId != "tenant-a" || p.Name != "Marketing" {
		t.Errorf("created row = %+v, want tenant-a/Marketing", p)
	}

	got, err := GetProjectByID("tenant-a", p.Id)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if got.Name != "Marketing" || got.Description != "desc of Marketing" {
		t.Errorf("fetched row = %+v", got)
	}

	// Name and description are both written unconditionally, so clearing the
	// description actually persists (the Updates-on-struct zero-value trap).
	updated, err := UpdateProject("tenant-a", p.Id, "  Growth  ", "")
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if updated.Name != "Growth" {
		t.Errorf("name = %q, want %q (leading/trailing space must be trimmed)", updated.Name, "Growth")
	}
	reread, err := GetProjectByID("tenant-a", p.Id)
	if err != nil {
		t.Fatalf("GetProjectByID after update: %v", err)
	}
	if reread.Name != "Growth" {
		t.Errorf("persisted name = %q, want Growth", reread.Name)
	}
	if reread.Description != "" {
		t.Errorf("persisted description = %q, want empty (clearing must persist)", reread.Description)
	}

	mustCreateProject(t, "tenant-a", "Research")
	list, err := ListProjectsByTenant("tenant-a", false)
	if err != nil {
		t.Fatalf("ListProjectsByTenant: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("listed %d projects, want 2", len(list))
	}
	if list[0].Name != "Research" {
		t.Errorf("first listed = %q, want Research (newest first)", list[0].Name)
	}

	if _, err := SoftDeleteProject("tenant-a", p.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}
	if _, err := GetProjectByID("tenant-a", p.Id); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("GetProjectByID after delete = %v, want ErrProjectNotFound", err)
	}
	list, err = ListProjectsByTenant("tenant-a", false)
	if err != nil {
		t.Fatalf("ListProjectsByTenant after delete: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("listed %d projects after delete, want 1", len(list))
	}
}

// TestIntegrationProject_CrossTenantIsolation: every accessor takes tenantID
// as a mandatory argument, so a valid id belonging to another tenant must be
// indistinguishable from a nonexistent one.
func TestProject_CrossTenantIsolation(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	a := mustCreateProject(t, "tenant-a", "Marketing")
	b := mustCreateProject(t, "tenant-b", "Finance")

	if _, err := GetProjectByID("tenant-b", a.Id); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("GetProjectByID(tenant-b, tenant-a's id) = %v, want ErrProjectNotFound", err)
	}
	if _, err := UpdateProject("tenant-b", a.Id, "Hijacked", ""); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("UpdateProject across tenants = %v, want ErrProjectNotFound", err)
	}
	if _, err := SoftDeleteProject("tenant-b", a.Id); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("SoftDeleteProject across tenants = %v, want ErrProjectNotFound", err)
	}
	// The victim row is untouched by all three attempts.
	if survivor, err := GetProjectByID("tenant-a", a.Id); err != nil || survivor.Name != "Marketing" {
		t.Errorf("tenant-a's project after cross-tenant attempts: %+v, err=%v", survivor, err)
	}

	list, err := ListProjectsByTenant("tenant-a", false)
	if err != nil {
		t.Fatalf("ListProjectsByTenant: %v", err)
	}
	for _, p := range list {
		if p.Id == b.Id {
			t.Errorf("tenant-a's listing contains tenant-b's project %+v", p)
		}
	}
}

// TestIntegrationProject_ResolveProjectNames_DoesNotLeakOtherTenantsNames is
// the name-leak test. Project names are customer-confidential and project_id
// has no foreign key, so an id from another tenant reaching this function must
// resolve to nothing rather than to that tenant's name.
func TestProject_ResolveProjectNames_DoesNotLeakOtherTenantsNames(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	mine := mustCreateProject(t, "tenant-a", "Marketing")
	theirs := mustCreateProject(t, "tenant-b", "Project Zeus")

	names, err := ResolveProjectNames("tenant-a", []int{mine.Id, theirs.Id})
	if err != nil {
		t.Fatalf("ResolveProjectNames: %v", err)
	}
	if names[mine.Id] != "Marketing" {
		t.Errorf("own project resolved to %q, want Marketing", names[mine.Id])
	}
	if n, ok := names[theirs.Id]; ok {
		t.Fatalf("LEAK: another tenant's project name %q resolved through tenant-a's report", n)
	}

	// Positive control: the same id DOES resolve for its owner, so the absence
	// above is the tenant filter working and not an unrelated lookup failure.
	ownerView, err := ResolveProjectNames("tenant-b", []int{theirs.Id})
	if err != nil {
		t.Fatalf("ResolveProjectNames(tenant-b): %v", err)
	}
	if ownerView[theirs.Id] != "Project Zeus" {
		t.Fatalf("negative control void: owner cannot resolve its own project either (%+v)", ownerView)
	}
}

// TestIntegrationProject_ResolveProjectNames_ReadsSoftDeleted keeps the
// invariant that per-project spend still renders a human-readable name after
// the project is retired — otherwise the report drops rows (under-reporting
// the tenant total) or prints a bare integer.
func TestProject_ResolveProjectNames_ReadsSoftDeleted(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")
	if _, err := SoftDeleteProject("tenant-a", p.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}

	names, err := ResolveProjectNames("tenant-a", []int{p.Id})
	if err != nil {
		t.Fatalf("ResolveProjectNames: %v", err)
	}
	if names[p.Id] != "Marketing" {
		t.Errorf("soft-deleted project resolved to %q, want Marketing (Unscoped read)", names[p.Id])
	}
}

func TestProject_ResolveProjectNames_SkipsUnassignedAndDedupes(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")

	// 0 is "unassigned", not a project; negatives are nonsense; duplicates
	// must not produce duplicate work or duplicate keys.
	names, err := ResolveProjectNames("tenant-a", []int{0, -1, p.Id, p.Id, 0})
	if err != nil {
		t.Fatalf("ResolveProjectNames: %v", err)
	}
	if len(names) != 1 || names[p.Id] != "Marketing" {
		t.Errorf("names = %+v, want exactly {%d: Marketing}", names, p.Id)
	}
	if _, ok := names[entity.ProjectUnassigned]; ok {
		t.Error("ResolveProjectNames must never emit a name for project 0 (unassigned)")
	}

	empty, err := ResolveProjectNames("tenant-a", nil)
	if err != nil {
		t.Fatalf("ResolveProjectNames(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ResolveProjectNames(nil) = %+v, want empty map", empty)
	}
}

func TestProject_DuplicateNames(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")

	if _, err := CreateProject("tenant-a", "Marketing", ""); !errors.Is(err, ErrProjectNameExists) {
		t.Errorf("duplicate create = %v, want ErrProjectNameExists", err)
	}
	// Trimming happens before the check, so " Marketing " is the same name.
	if _, err := CreateProject("tenant-a", "  Marketing  ", ""); !errors.Is(err, ErrProjectNameExists) {
		t.Errorf("duplicate create (untrimmed) = %v, want ErrProjectNameExists", err)
	}
	// A different tenant may use the same name.
	if _, err := CreateProject("tenant-b", "Marketing", ""); err != nil {
		t.Errorf("same name in another tenant = %v, want success", err)
	}

	other := mustCreateProject(t, "tenant-a", "Research")
	if _, err := UpdateProject("tenant-a", other.Id, "Marketing", ""); !errors.Is(err, ErrProjectNameExists) {
		t.Errorf("rename onto an existing name = %v, want ErrProjectNameExists", err)
	}
	// Renaming a project to its OWN current name is not a clash.
	if _, err := UpdateProject("tenant-a", other.Id, "Research", "still research"); err != nil {
		t.Errorf("no-op rename = %v, want success", err)
	}

	// A soft-deleted name is released: the partial unique index and the
	// explicit pre-check must agree on this.
	if _, err := SoftDeleteProject("tenant-a", p.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}
	if _, err := CreateProject("tenant-a", "Marketing", ""); err != nil {
		t.Errorf("recreating a soft-deleted name = %v, want success", err)
	}
}

func TestProject_InvalidInput(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if _, err := CreateProject("tenant-a", "   ", ""); err == nil {
		t.Error("CreateProject with a blank name must fail")
	}
	if _, err := GetProjectByID("tenant-a", 0); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("GetProjectByID(0) = %v, want ErrProjectNotFound", err)
	}
	if _, err := GetProjectByID("tenant-a", -5); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("GetProjectByID(-5) = %v, want ErrProjectNotFound", err)
	}
	if _, err := GetProjectByID("tenant-a", 999999); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("GetProjectByID(missing) = %v, want ErrProjectNotFound", err)
	}
	if _, err := UpdateProject("tenant-a", 999999, "X", ""); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("UpdateProject(missing) = %v, want ErrProjectNotFound", err)
	}
	p := mustCreateProject(t, "tenant-a", "Marketing")
	if _, err := UpdateProject("tenant-a", p.Id, "  ", ""); err == nil {
		t.Error("UpdateProject with a blank name must fail")
	}
	if _, err := SoftDeleteProject("tenant-a", 999999); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("SoftDeleteProject(missing) = %v, want ErrProjectNotFound", err)
	}
}

// TestIntegrationProject_SoftDeleteDetachesTokensAtomically: deleting a project
// must leave no token still tagging fresh spend onto it, and must not touch
// another tenant's token that happens to carry the same numeric project_id
// (there is no foreign key, so numeric collisions across tenants are normal).
func TestProject_SoftDeleteDetachesTokens(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")

	mine := &Token{UserId: 1, TenantId: "tenant-a", Key: "k-mine-0000000000000000000000000000000000000", Name: "mine", ProjectId: p.Id}
	if err := DB.Create(mine).Error; err != nil {
		t.Fatalf("insert own token: %v", err)
	}
	// Same numeric project_id, different tenant — must survive untouched.
	foreign := &Token{UserId: 2, TenantId: "tenant-b", Key: "k-foreign-000000000000000000000000000000000", Name: "foreign", ProjectId: p.Id}
	if err := DB.Create(foreign).Error; err != nil {
		t.Fatalf("insert foreign token: %v", err)
	}

	if _, err := SoftDeleteProject("tenant-a", p.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}

	var reread Token
	if err := DB.Where("id = ?", mine.Id).Take(&reread).Error; err != nil {
		t.Fatalf("reread own token: %v", err)
	}
	if reread.ProjectId != entity.ProjectUnassigned {
		t.Errorf("own token project_id = %d after project delete, want %d (unassigned)",
			reread.ProjectId, entity.ProjectUnassigned)
	}

	var untouched Token
	if err := DB.Where("id = ?", foreign.Id).Take(&untouched).Error; err != nil {
		t.Fatalf("reread foreign token: %v", err)
	}
	if untouched.ProjectId != p.Id {
		t.Errorf("another tenant's token was detached (project_id = %d, want %d) — "+
			"the UPDATE is missing its tenant_id clause", untouched.ProjectId, p.Id)
	}
}

// TestIntegrationProject_TokenUpdatePersistsProjectId is the regression test
// for the Token.Update column allow-list. Without "project_id" in that Select
// list the write is silently dropped while the Redis write-through still
// publishes the NEW value — the DB and the cache then disagree until the TTL
// expires. Redis is off here, so this asserts the column itself.
func TestProject_TokenUpdatePersistsProjectId(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if common.RedisEnabled {
		t.Fatal("this test must run with the token cache disabled, otherwise it asserts Redis, not the column")
	}

	p := mustCreateProject(t, "tenant-a", "Marketing")
	tok := &Token{UserId: 1, TenantId: "tenant-a", Key: "k-update-0000000000000000000000000000000000", Name: "t"}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("insert token: %v", err)
	}
	if tok.ProjectId != entity.ProjectUnassigned {
		t.Fatalf("fresh token project_id = %d, want 0", tok.ProjectId)
	}

	tok.ProjectId = p.Id
	if err := tok.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var afterAssign Token
	if err := DB.Where("id = ?", tok.Id).Take(&afterAssign).Error; err != nil {
		t.Fatalf("reread after assign: %v", err)
	}
	if afterAssign.ProjectId != p.Id {
		t.Fatalf("project_id did not persist: got %d, want %d — is \"project_id\" missing from "+
			"Token.Update's Select allow-list?", afterAssign.ProjectId, p.Id)
	}

	// Unassigning writes the ZERO value, which Updates-on-struct would drop
	// were it not explicitly selected.
	tok.ProjectId = entity.ProjectUnassigned
	if err := tok.Update(); err != nil {
		t.Fatalf("Update (unassign): %v", err)
	}
	var afterClear Token
	if err := DB.Where("id = ?", tok.Id).Take(&afterClear).Error; err != nil {
		t.Fatalf("reread after unassign: %v", err)
	}
	if afterClear.ProjectId != entity.ProjectUnassigned {
		t.Errorf("unassign did not persist: project_id = %d, want 0", afterClear.ProjectId)
	}
}

// ─── Reversibility and repeat-safety ─────────────────────────────────────────
//
// Every mutating project operation has to survive being clicked twice (a lost
// response, an impatient user, a retrying proxy) and the destructive one has to
// be undoable. These tests pin both.

// TestProject_DeleteIsIdempotent: a second DELETE of the caller's own project
// is the state they asked for, not an error. Another tenant's id stays 404.
func TestProject_DeleteIsIdempotent(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")

	first, err := SoftDeleteProject("tenant-a", p.Id)
	if err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if first == nil {
		t.Error("first delete returned nil ids; want an allocated (possibly empty) slice")
	}

	second, err := SoftDeleteProject("tenant-a", p.Id)
	if err != nil {
		t.Fatalf("second delete must succeed (the requested state already holds): %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second delete reported %v detached tokens; want none", second)
	}

	// Repeat-safety must NOT come at the cost of the tenant clause.
	if _, err := SoftDeleteProject("tenant-b", p.Id); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("cross-tenant delete = %v, want ErrProjectNotFound", err)
	}
}

// TestProject_DeleteReturnsUndoInformation: nothing else in the schema records
// which tokens pointed at a project, so the delete has to hand them back or the
// detach is a one-way door.
func TestProject_DeleteReturnsUndoInformation(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")
	mine := &Token{UserId: 1, TenantId: "tenant-a", Key: "u-mine-00000000000000000000000000000000000", Name: "mine", ProjectId: p.Id}
	other := &Token{UserId: 1, TenantId: "tenant-a", Key: "u-other-0000000000000000000000000000000000", Name: "other", ProjectId: p.Id}
	foreign := &Token{UserId: 2, TenantId: "tenant-b", Key: "u-foreign-00000000000000000000000000000000", Name: "foreign", ProjectId: p.Id}
	for _, tok := range []*Token{mine, other, foreign} {
		if err := DB.Create(tok).Error; err != nil {
			t.Fatalf("seed token %q: %v", tok.Name, err)
		}
	}

	detached, err := SoftDeleteProject("tenant-a", p.Id)
	if err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}
	got := map[int]bool{}
	for _, id := range detached {
		got[id] = true
	}
	if !got[mine.Id] || !got[other.Id] {
		t.Errorf("detached ids = %v, want both own tokens (%d, %d)", detached, mine.Id, other.Id)
	}
	if got[foreign.Id] {
		t.Errorf("detached ids %v include another tenant's token %d", detached, foreign.Id)
	}
}

// TestProject_RestoreUndoesDelete is the round trip: delete, restore with the
// ids the delete handed back, and the tenant is exactly where it started.
func TestProject_RestoreUndoesDelete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")
	tok := &Token{UserId: 1, TenantId: "tenant-a", Key: "u-round-0000000000000000000000000000000000", Name: "t", ProjectId: p.Id}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	detached, err := SoftDeleteProject("tenant-a", p.Id)
	if err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}

	restored, err := RestoreProject("tenant-a", p.Id, detached)
	if err != nil {
		t.Fatalf("RestoreProject: %v", err)
	}
	if restored.DeletedAt.Valid {
		t.Error("restored project is still marked deleted")
	}
	if _, err := GetProjectByID("tenant-a", p.Id); err != nil {
		t.Errorf("restored project not visible to the live lookup: %v", err)
	}

	var after Token
	if err := DB.Where("id = ?", tok.Id).Take(&after).Error; err != nil {
		t.Fatalf("reread token: %v", err)
	}
	if after.ProjectId != p.Id {
		t.Errorf("token project_id = %d after undo, want %d", after.ProjectId, p.Id)
	}
}

// TestProject_RestoreIsIdempotentAndTenantScoped: restore can be replayed, and
// it is not a way to reach into another tenant.
func TestProject_RestoreIsIdempotentAndTenantScoped(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")
	if _, err := SoftDeleteProject("tenant-a", p.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := RestoreProject("tenant-a", p.Id, nil); err != nil {
			t.Fatalf("restore #%d: %v", i+1, err)
		}
	}
	live, err := ListProjectsByTenant("tenant-a", false)
	if err != nil {
		t.Fatalf("ListProjectsByTenant: %v", err)
	}
	if len(live) != 1 {
		t.Errorf("live projects after 3 restores = %d, want 1 (restore must not duplicate)", len(live))
	}

	// Restoring a live project is a no-op, not an error.
	if _, err := RestoreProject("tenant-a", p.Id, nil); err != nil {
		t.Errorf("restoring an already-live project = %v, want success", err)
	}
	if _, err := RestoreProject("tenant-b", p.Id, nil); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("cross-tenant restore = %v, want ErrProjectNotFound", err)
	}
}

// TestProject_RestoreNeverOverwritesANewerDecision: an undo may only put back
// what it took. A token the user has since assigned somewhere else, or one that
// belongs to another tenant, must be left alone.
func TestProject_RestoreNeverOverwritesANewerDecision(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := mustCreateProject(t, "tenant-a", "Marketing")
	later := mustCreateProject(t, "tenant-a", "Research")

	moved := &Token{UserId: 1, TenantId: "tenant-a", Key: "u-moved-0000000000000000000000000000000000", Name: "moved", ProjectId: p.Id}
	foreign := &Token{UserId: 2, TenantId: "tenant-b", Key: "u-alien-0000000000000000000000000000000000", Name: "alien", ProjectId: 0}
	for _, tok := range []*Token{moved, foreign} {
		if err := DB.Create(tok).Error; err != nil {
			t.Fatalf("seed token %q: %v", tok.Name, err)
		}
	}

	detached, err := SoftDeleteProject("tenant-a", p.Id)
	if err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}

	// The user reassigns the token before pressing undo.
	moved.ProjectId = later.Id
	if err := moved.Update(); err != nil {
		t.Fatalf("reassign token: %v", err)
	}

	// Undo also tries to grab a token that was never ours.
	if _, err := RestoreProject("tenant-a", p.Id, append(detached, foreign.Id)); err != nil {
		t.Fatalf("RestoreProject: %v", err)
	}

	var afterMoved, afterForeign Token
	if err := DB.Where("id = ?", moved.Id).Take(&afterMoved).Error; err != nil {
		t.Fatalf("reread moved token: %v", err)
	}
	if afterMoved.ProjectId != later.Id {
		t.Errorf("undo clobbered a newer assignment: project_id = %d, want %d", afterMoved.ProjectId, later.Id)
	}
	if err := DB.Where("id = ?", foreign.Id).Take(&afterForeign).Error; err != nil {
		t.Fatalf("reread foreign token: %v", err)
	}
	if afterForeign.ProjectId != 0 {
		t.Errorf("undo reached into another tenant: project_id = %d, want 0", afterForeign.ProjectId)
	}
}

// TestProject_RestoreBlockedByNameCollision: the partial unique index is what
// let a new project take the retired one's name; restoring would violate it, so
// the caller gets an actionable ErrProjectNameExists instead of a raw
// constraint error, and neither row is damaged.
func TestProject_RestoreBlockedByNameCollision(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	old := mustCreateProject(t, "tenant-a", "Marketing")
	if _, err := SoftDeleteProject("tenant-a", old.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}
	fresh := mustCreateProject(t, "tenant-a", "Marketing")

	if _, err := RestoreProject("tenant-a", old.Id, nil); !errors.Is(err, ErrProjectNameExists) {
		t.Fatalf("restore into a taken name = %v, want ErrProjectNameExists", err)
	}
	// The retired row stays retired and the live one is untouched.
	if _, err := GetProjectByID("tenant-a", old.Id); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("failed restore left the old row live: %v", err)
	}
	if got, err := GetProjectByID("tenant-a", fresh.Id); err != nil || got.Name != "Marketing" {
		t.Errorf("failed restore damaged the live row: %+v err=%v", got, err)
	}
}

// TestProject_ListIncludeDeleted: the console cannot offer an undo for
// something it cannot see.
func TestProject_ListIncludeDeleted(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	live := mustCreateProject(t, "tenant-a", "Marketing")
	gone := mustCreateProject(t, "tenant-a", "Research")
	if _, err := SoftDeleteProject("tenant-a", gone.Id); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}
	mustCreateProject(t, "tenant-b", "Other tenant")

	liveOnly, err := ListProjectsByTenant("tenant-a", false)
	if err != nil {
		t.Fatalf("ListProjectsByTenant(live): %v", err)
	}
	if len(liveOnly) != 1 || liveOnly[0].Id != live.Id {
		t.Errorf("live listing = %+v, want only %d", liveOnly, live.Id)
	}

	withDeleted, err := ListProjectsByTenant("tenant-a", true)
	if err != nil {
		t.Fatalf("ListProjectsByTenant(include deleted): %v", err)
	}
	if len(withDeleted) != 2 {
		t.Fatalf("listing with deleted = %d rows, want 2", len(withDeleted))
	}
	// Still tenant-scoped — include_deleted is not a way out of the tenant.
	for _, r := range withDeleted {
		if r.TenantId != "tenant-a" {
			t.Errorf("include_deleted leaked tenant %q", r.TenantId)
		}
	}
	var sawDeleted bool
	for _, r := range withDeleted {
		if r.Id == gone.Id {
			sawDeleted = r.DeletedAt.Valid
		}
	}
	if !sawDeleted {
		t.Error("retired row is not flagged deleted; the console cannot tell it apart")
	}
}

// TestProject_DedupePositive guards the bound on the undo id list.
func TestProject_DedupePositive(t *testing.T) {
	got := dedupePositive([]int{3, 0, -1, 3, 5})
	if len(got) != 2 || got[0] != 3 || got[1] != 5 {
		t.Errorf("dedupePositive = %v, want [3 5]", got)
	}
	if len(dedupePositive(nil)) != 0 {
		t.Error("dedupePositive(nil) must be empty")
	}
	over := make([]int, maxReattachTokens+50)
	for i := range over {
		over[i] = i + 1
	}
	if n := len(dedupePositive(over)); n != maxReattachTokens {
		t.Errorf("dedupePositive capped at %d, want %d", n, maxReattachTokens)
	}
}
