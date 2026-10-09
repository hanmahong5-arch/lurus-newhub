package repo

import (
	"context"
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// seedProject creates a live project and returns its id.
func seedProject(t *testing.T, tenant, name string) int {
	t.Helper()
	p := &entity.Project{TenantId: tenant, Name: name}
	if err := DB.Create(p).Error; err != nil {
		t.Fatalf("seed project %s/%s: %v", tenant, name, err)
	}
	return p.Id
}

func TestProjectMember_AddListRemove(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&entity.ProjectMember{}, &entity.Project{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	a, b := seedProject(t, "t1", "a"), seedProject(t, "t1", "b")
	other := seedProject(t, "t2", "other")
	for _, m := range []struct {
		tenant string
		pid    int
		uid    int
	}{{"t1", b, 42}, {"t1", a, 42}, {"t2", other, 42}, {"t1", a, 43}} {
		if err := AddProjectMember(m.tenant, m.pid, m.uid); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	// idempotent re-add
	if err := AddProjectMember("t1", b, 42); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	got, err := ListProjectIDsForUser("t1", 42)
	if err != nil || !reflect.DeepEqual(got, []int{a, b}) {
		t.Fatalf("ListProjectIDsForUser = %v, %v; want [%d %d] (tenant-scoped, deduped)", got, err, a, b)
	}
	if rows, _ := ListProjectMembers("t1", a); len(rows) != 2 {
		t.Fatalf("members of project a = %d, want 2", len(rows))
	}
	remaining, err := RemoveProjectMember("t1", b, 42)
	if err != nil || remaining != 1 {
		t.Fatalf("RemoveProjectMember remaining = %d, %v; want 1", remaining, err)
	}
	remaining, _ = RemoveProjectMember("t1", a, 42)
	if remaining != 0 {
		t.Fatalf("after last removal remaining = %d, want 0 (t2 membership must not count)", remaining)
	}
	if err := AddProjectMember("", 1, 1); err == nil {
		t.Fatal("empty tenant must be rejected")
	}
}

// A membership row whose project_id points at ANOTHER tenant's project (or at a
// project that never existed) must not widen the dept_lead's scope.
func TestListProjectIDsForUser_OnlyCountsProjectsOfThisTenant(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&entity.ProjectMember{}, &entity.Project{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mine := seedProject(t, "t1", "mine")
	foreign := seedProject(t, "t2", "foreign")
	for _, pid := range []int{mine, foreign, 9999} {
		if err := AddProjectMember("t1", pid, 42); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	got, err := ListProjectIDsForUser("t1", 42)
	if err != nil || !reflect.DeepEqual(got, []int{mine}) {
		t.Fatalf("ListProjectIDsForUser = %v, %v; want only [%d]", got, err, mine)
	}
}

// RemoveProjectMember's remaining count decides whether the dept_lead role is
// revoked, so memberships of deleted / foreign projects must not keep it alive.
func TestRemoveProjectMember_RemainingIgnoresDeadProjects(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&entity.ProjectMember{}, &entity.Project{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	keep := seedProject(t, "t1", "keep")
	gone := seedProject(t, "t1", "gone")
	foreign := seedProject(t, "t2", "foreign")
	for _, pid := range []int{keep, gone, foreign} {
		if err := AddProjectMember("t1", pid, 42); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if err := DB.Delete(&entity.Project{}, gone).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	remaining, err := RemoveProjectMember("t1", keep, 42)
	if err != nil || remaining != 0 {
		t.Fatalf("remaining = %d, %v; want 0 (deleted + foreign projects do not count)", remaining, err)
	}
}

// Privacy erasure: the user's membership rows go, other users' rows stay.
func TestHardDeleteUserProjectMembers(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&entity.ProjectMember{}, &entity.Project{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	p1, p2 := seedProject(t, "t1", "p1"), seedProject(t, "t2", "p2")
	for _, m := range []struct {
		tenant string
		pid    int
		uid    int
	}{{"t1", p1, 42}, {"t2", p2, 42}, {"t1", p1, 43}} {
		if err := AddProjectMember(m.tenant, m.pid, m.uid); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	n, err := HardDeleteUserProjectMembers(context.Background(), 42)
	if err != nil || n != 2 {
		t.Fatalf("HardDeleteUserProjectMembers = %d, %v; want 2 rows across both tenants", n, err)
	}
	var left int64
	DB.Model(&entity.ProjectMember{}).Count(&left)
	if left != 1 {
		t.Fatalf("rows left = %d, want 1 (user 43 untouched)", left)
	}
}

// A soft-deleted project must drop out of a dept_lead's data scope.
func TestListProjectIDsForUser_ExcludesSoftDeletedProjects(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&entity.ProjectMember{}, &entity.Project{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	live := &entity.Project{TenantId: "t1", Name: "live"}
	gone := &entity.Project{TenantId: "t1", Name: "gone"}
	if err := DB.Create(live).Error; err != nil {
		t.Fatalf("seed live: %v", err)
	}
	if err := DB.Create(gone).Error; err != nil {
		t.Fatalf("seed gone: %v", err)
	}
	for _, id := range []int{live.Id, gone.Id} {
		if err := AddProjectMember("t1", id, 42); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if err := DB.Delete(gone).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	got, err := ListProjectIDsForUser("t1", 42)
	if err != nil || !reflect.DeepEqual(got, []int{live.Id}) {
		t.Fatalf("ListProjectIDsForUser = %v, %v; want only live project %d", got, err, live.Id)
	}
}
