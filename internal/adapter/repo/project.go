package repo

import (
	"errors"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

// Tenant-scoped CRUD for entity.Project (migration 029) — the cost-attribution
// dimension between a tenant and its tokens.
//
// ISOLATION CONTRACT: every exported function here takes tenantID as a
// MANDATORY POSITIONAL argument and applies it as an explicit WHERE clause.
// There is deliberately no id-only variant of any of them, most importantly
// not of ResolveProjectNames: project names are customer-confidential, there
// is no foreign key on project_id, and a stray id resolved by id alone would
// hand another tenant's project NAME back to the caller. This is the
// TenantScope philosophy (repo/log.go) applied where it actually buys
// something.
//
// There is NO ProjectScope type, and that is on purpose. TenantScope exists
// because forgetting the tenant decision leaks data ACROSS CUSTOMERS. The
// worst case for a forgotten project filter is a tenant admin seeing another
// project inside their own tenant — which they are already entitled to see
// (GET /logs/all shows every member's rows). A second fail-closed type there
// would be protection against no threat.
//
// Projects are LABELS, not permission boundaries — see entity/project.go.

var (
	// ErrProjectNotFound is returned when no live project with that id exists
	// IN THE GIVEN TENANT. Callers surface it as 404 — deliberately
	// indistinguishable from "exists but belongs to someone else", so the
	// endpoints cannot be used to probe another tenant's id space.
	ErrProjectNotFound = errors.New("project not found")

	// ErrProjectNameExists is returned when the tenant already has a LIVE
	// project with that name. Soft-deleted rows keep their name but do not
	// reserve it (uk_projects_tenant_name is partial on deleted_at IS NULL).
	ErrProjectNameExists = errors.New("project name already exists in this tenant")
)

// maxProjectsPerTenantListing bounds the unpaginated listing. Projects are a
// coarse, human-maintained dimension (departments, cost centres) — a tenant
// with more than this has a different problem than pagination.
const maxProjectsPerTenantListing = 500

// maxReattachTokens bounds the id list an undo may carry. It sits far above any
// real tenant's token count for a single project and exists only so a malformed
// request cannot expand into an unbounded IN (...) clause.
const maxReattachTokens = 1000

// Duplicate-key detection reuses isUniqueViolation from tenant_credit_pool.go
// (same package): SQLSTATE 23505 for PostgreSQL plus the SQLite wording used
// by the hermetic unit-test tier.

// ListProjectsByTenant returns the tenant's projects, newest first.
//
// includeDeleted adds SOFT-DELETED rows (Unscoped) so the console can offer an
// undo path for a retired project. Deleted rows are distinguishable by
// Project.DeletedAt.Valid; the caller decides how to render them.
func ListProjectsByTenant(tenantID string, includeDeleted bool) ([]entity.Project, error) {
	var rows []entity.Project
	q := DB.Where("tenant_id = ?", tenantID)
	if includeDeleted {
		q = DB.Unscoped().Where("tenant_id = ?", tenantID)
	}
	err := q.Order("id DESC").
		Limit(maxProjectsPerTenantListing).
		Find(&rows).Error
	return rows, err
}

// GetProjectByID returns one LIVE project belonging to tenantID.
// A project that exists under a different tenant returns ErrProjectNotFound —
// the tenant clause is part of the lookup, not a check applied afterwards.
func GetProjectByID(tenantID string, id int) (*entity.Project, error) {
	if id <= 0 {
		return nil, ErrProjectNotFound
	}
	var row entity.Project
	err := DB.Where("id = ? AND tenant_id = ?", id, tenantID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateProject adds a project to the tenant.
//
// The duplicate check is layered on purpose: the explicit pre-check gives a
// clean ErrProjectNameExists on every dialect (the hermetic SQLite tier has no
// partial unique index — that index is created only by migration 029), while
// the unique-violation mapping below is the race-safe backstop that catches
// two concurrent creates the pre-check both waved through.
func CreateProject(tenantID, name, description string) (*entity.Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("project name is required")
	}
	var existing int64
	if err := DB.Model(&entity.Project{}).
		Where("tenant_id = ? AND name = ?", tenantID, name).
		Count(&existing).Error; err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, ErrProjectNameExists
	}

	row := entity.Project{
		TenantId:    tenantID,
		Name:        name,
		Description: strings.TrimSpace(description),
	}
	if err := DB.Create(&row).Error; err != nil {
		if isUniqueViolation(err) {
			return nil, ErrProjectNameExists
		}
		return nil, err
	}
	return &row, nil
}

// UpdateProject renames / re-describes a live project of this tenant.
// Both columns are written unconditionally (Select, not Updates-on-struct) so
// clearing the description actually persists — the zero-value trap that made
// project_id silently unwritable in Token.Update before it was allow-listed.
func UpdateProject(tenantID string, id int, name, description string) (*entity.Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("project name is required")
	}
	row, err := GetProjectByID(tenantID, id)
	if err != nil {
		return nil, err
	}

	if name != row.Name {
		var clash int64
		if err := DB.Model(&entity.Project{}).
			Where("tenant_id = ? AND name = ? AND id <> ?", tenantID, name, id).
			Count(&clash).Error; err != nil {
			return nil, err
		}
		if clash > 0 {
			return nil, ErrProjectNameExists
		}
	}

	row.Name = name
	row.Description = strings.TrimSpace(description)
	if err := DB.Model(row).
		Where("tenant_id = ?", tenantID).
		Select("name", "description").
		Updates(row).Error; err != nil {
		if isUniqueViolation(err) {
			return nil, ErrProjectNameExists
		}
		return nil, err
	}
	return row, nil
}

// SoftDeleteProject retires a project and detaches its tokens IN ONE
// TRANSACTION: any token still pointing at it falls back to unassigned, so no
// token can keep tagging fresh spend onto a project the tenant just deleted.
//
// The row is SOFT-deleted. Historical log rows keep the numeric project_id
// forever, and ResolveProjectNames reads through Unscoped() so the report can
// still print "Marketing (deleted)" instead of dropping the rows (which would
// under-report the tenant's total) or rendering "project 17".
// REVERSIBILITY: the returned slice is the ids of the tokens this call
// detached — everything RestoreProject needs to put the tenant back exactly
// where it was. Without it the detach would be a silent one-way door: nothing
// else in the schema records which tokens used to point here.
//
// IDEMPOTENCE: deleting an ALREADY-deleted project of this tenant succeeds and
// returns no ids. A second click, or a retry whose first response was lost,
// must not surface as an error — the caller asked for a state that already
// holds. Another tenant's id still returns ErrProjectNotFound.
func SoftDeleteProject(tenantID string, id int) (detachedTokenIDs []int, err error) {
	row, err := getProjectByIDUnscoped(tenantID, id)
	if err != nil {
		return nil, err
	}
	if row.DeletedAt.Valid {
		return []int{}, nil // already retired — nothing left to do
	}

	detached := []int{}
	err = DB.Transaction(func(tx *gorm.DB) error {
		// Read the ids BEFORE clearing them: afterwards nothing records which
		// tokens were ours.
		var ids []int
		if err := tx.Model(&Token{}).
			Where("project_id = ? AND tenant_id = ?", id, tenantID).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if err := tx.Model(&Token{}).
			Where("project_id = ? AND tenant_id = ?", id, tenantID).
			Update("project_id", entity.ProjectUnassigned).Error; err != nil {
			return err
		}
		res := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&entity.Project{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// Lost a race with a concurrent delete. The requested state holds
			// either way, so report success — but with no ids, because the
			// winner of that race owns the undo information.
			detached = []int{}
			return nil
		}
		detached = ids
		return nil
	})
	if err != nil {
		return nil, err
	}
	return detached, nil
}

// getProjectByIDUnscoped finds a project of this tenant whether it is live or
// soft-deleted. Same mandatory-tenant contract as GetProjectByID: another
// tenant's id is ErrProjectNotFound, never a peek at its state.
func getProjectByIDUnscoped(tenantID string, id int) (*entity.Project, error) {
	if id <= 0 {
		return nil, ErrProjectNotFound
	}
	var row entity.Project
	err := DB.Unscoped().Where("id = ? AND tenant_id = ?", id, tenantID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// RestoreProject is the undo for SoftDeleteProject: it un-retires the row and,
// given the ids SoftDeleteProject returned, re-attaches those tokens.
//
// Properties that make it safe to click repeatedly:
//   - Restoring an ALREADY-LIVE project succeeds as a no-op, so a double-click
//     is not an error.
//   - Re-attachment only touches tokens that are STILL UNASSIGNED and belong to
//     this tenant. A token the user has since pointed somewhere else is left
//     alone — an undo must never overwrite a newer, deliberate decision — and
//     an id from another tenant matches nothing.
//   - It is one transaction, so a partial restore cannot survive.
//
// A live project may have taken the name in the meantime (the unique index is
// partial on deleted_at IS NULL, which is exactly what allowed that). Restoring
// would then violate it, so this returns ErrProjectNameExists and asks the
// caller to rename first — a 409 the user can act on, instead of a raw
// constraint error.
func RestoreProject(tenantID string, id int, reattachTokenIDs []int) (*entity.Project, error) {
	row, err := getProjectByIDUnscoped(tenantID, id)
	if err != nil {
		return nil, err
	}
	if !row.DeletedAt.Valid {
		return row, nil // already live — idempotent no-op
	}

	var clash int64
	if err := DB.Model(&entity.Project{}).
		Where("tenant_id = ? AND name = ? AND id <> ?", tenantID, row.Name, id).
		Count(&clash).Error; err != nil {
		return nil, err
	}
	if clash > 0 {
		return nil, ErrProjectNameExists
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Unscoped().Model(&entity.Project{}).
			Where("id = ? AND tenant_id = ?", id, tenantID).
			Update("deleted_at", nil)
		if res.Error != nil {
			if isUniqueViolation(res.Error) {
				return ErrProjectNameExists
			}
			return res.Error
		}
		ids := dedupePositive(reattachTokenIDs)
		if len(ids) == 0 {
			return nil
		}
		return tx.Model(&Token{}).
			Where("id IN ? AND tenant_id = ? AND project_id = ?", ids, tenantID, entity.ProjectUnassigned).
			Update("project_id", id).Error
	})
	if err != nil {
		return nil, err
	}
	return getProjectByIDUnscoped(tenantID, id)
}

// dedupePositive drops non-positive and duplicate ids and caps the result at
// maxReattachTokens.
func dedupePositive(ids []int) []int {
	out := make([]int, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= maxReattachTokens {
			break
		}
	}
	return out
}

// ResolveProjectNames maps project ids to names for report rendering,
// including SOFT-DELETED projects (Unscoped) so historical spend stays
// attributable to a human-readable name.
//
// tenantID is mandatory and filtered on: an id that belongs to another tenant
// is simply absent from the result map. Callers render a missing id as
// "unassigned"/unknown rather than as a leaked name.
//
// id 0 (entity.ProjectUnassigned) is never looked up — it is not a project.
func ResolveProjectNames(tenantID string, ids []int) (map[int]string, error) {
	out := make(map[int]string, len(ids))
	uniq := make([]int, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id <= entity.ProjectUnassigned || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return out, nil
	}

	var rows []struct {
		Id   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := DB.Unscoped().Model(&entity.Project{}).
		Select("id, name").
		Where("tenant_id = ? AND id IN ?", tenantID, uniq).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Id] = r.Name
	}
	return out, nil
}
