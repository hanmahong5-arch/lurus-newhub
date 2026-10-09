package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Tenant-scoped membership for entity.ProjectMember (migration 046). Like
// project.go, every function takes tenantID as a mandatory positional argument
// and applies it as an explicit WHERE clause — there is no id-only variant.

// liveMembershipsQuery scopes project_members to rows whose project exists in
// the SAME tenant and is not soft-deleted. The membership row repeats tenant_id
// but project_id is a bare number, so without this join a stale, foreign-tenant
// or dangling project_id would count towards a dept_lead's scope.
func liveMembershipsQuery(tenantID string, userID int) *gorm.DB {
	return DB.Table("project_members AS pm").
		Joins("JOIN projects AS p ON p.id = pm.project_id AND p.tenant_id = pm.tenant_id AND p.deleted_at IS NULL").
		Where("pm.tenant_id = ? AND pm.user_id = ?", tenantID, userID)
}

// ListProjectIDsForUser returns the ids of the live projects userID is a member
// of inside tenantID, ascending. Empty (not nil-error) when the user has none.
func ListProjectIDsForUser(tenantID string, userID int) ([]int, error) {
	if tenantID == "" || userID <= 0 {
		return nil, nil
	}
	var ids []int64
	if err := liveMembershipsQuery(tenantID, userID).
		Order("pm.project_id ASC").Pluck("pm.project_id", &ids).Error; err != nil {
		return nil, err
	}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		out = append(out, int(id))
	}
	return out, nil
}

// ListProjectMembers returns the members of one project, oldest first.
func ListProjectMembers(tenantID string, projectID int) ([]entity.ProjectMember, error) {
	var rows []entity.ProjectMember
	err := DB.Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("id ASC").Find(&rows).Error
	return rows, err
}

// AddProjectMember adds userID to the project. Idempotent: an existing
// membership is left untouched (ON CONFLICT DO NOTHING on the unique key).
// The caller is responsible for checking that the project and the user belong
// to tenantID.
func AddProjectMember(tenantID string, projectID int, userID int) error {
	if tenantID == "" || projectID <= 0 || userID <= 0 {
		return errors.New("project member: tenant, project and user are required")
	}
	row := &entity.ProjectMember{
		TenantId:  tenantID,
		ProjectId: int64(projectID),
		UserId:    int64(userID),
		CreatedAt: time.Now().Unix(),
	}
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error
}

// RemoveProjectMember deletes one membership and reports how many live projects
// of this tenant the user still belongs to, so the caller can revoke the
// dept_lead role when the last one is gone.
func RemoveProjectMember(tenantID string, projectID int, userID int) (remaining int64, err error) {
	err = DB.Transaction(func(tx *gorm.DB) error {
		if e := tx.Where("tenant_id = ? AND project_id = ? AND user_id = ?", tenantID, projectID, userID).
			Delete(&entity.ProjectMember{}).Error; e != nil {
			return e
		}
		return tx.Table("project_members AS pm").
			Joins("JOIN projects AS p ON p.id = pm.project_id AND p.tenant_id = pm.tenant_id AND p.deleted_at IS NULL").
			Where("pm.tenant_id = ? AND pm.user_id = ?", tenantID, userID).
			Count(&remaining).Error
	})
	return remaining, err
}

// HardDeleteUserProjectMembers removes every membership of userID across all
// tenants (privacy erasure; executeErasure's tokens step). Tenant isolation is
// bypassed on purpose: the erasing subject spans tenants. A missing table (a
// deployment that has not run migration 046 yet) is a no-op, not an error, so
// the erasure cascade is never blocked by it.
func HardDeleteUserProjectMembers(ctx context.Context, userID int) (int64, error) {
	if !DB.Migrator().HasTable(&entity.ProjectMember{}) {
		return 0, nil
	}
	result := WithoutTenantIsolationCtx(ctx, DB).
		Where("user_id = ?", userID).
		Delete(&entity.ProjectMember{})
	if result.Error != nil {
		return 0, fmt.Errorf("hard delete user project members: %w", result.Error)
	}
	return result.RowsAffected, nil
}
