package repo

import (
	"errors"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// tenant_member.go — writers for users.tenant_role and project membership
// (migration 046). Every function that writes tenant_role refuses the shared
// bootstrap tenant "default" (it hosts every personal user, a role there is
// never honoured) and invalidates the Redis user cache after the commit, so a
// revoked admin loses privilege on the next request rather than at TTL expiry.

var (
	// ErrTenantRoleInDefault: tenant_role must never be written for a user of
	// the "default" tenant.
	ErrTenantRoleInDefault = errors.New("tenant_role cannot be set in the default tenant")
	// ErrInvalidTenantRole: role is not one of "", "admin", "dept_lead".
	ErrInvalidTenantRole = errors.New("invalid tenant_role")
	// ErrMemberNotInTenant covers "no such user" and "user belongs to another
	// tenant" alike, so the endpoints cannot probe foreign user ids.
	ErrMemberNotInTenant = errors.New("user not found in this tenant")
)

// IsUniqueViolation reports a duplicate-key error (PostgreSQL 23505 or the
// SQLite wording used by the hermetic tier).
func IsUniqueViolation(err error) bool { return isUniqueViolation(err) }

// ValidTenantRole reports whether role is a storable users.tenant_role value.
func ValidTenantRole(role string) bool {
	switch role {
	case entity.TenantRoleNone, entity.TenantRoleAdmin, entity.TenantRoleDeptLead:
		return true
	}
	return false
}

// tenantMemberUser loads the user inside tx and requires it to belong to
// tenantID. Soft-deleted users do not resolve.
func tenantMemberUser(tx *gorm.DB, tenantID string, userID int) (*User, error) {
	var u User
	err := tx.Where("id = ? AND tenant_id = ?", userID, tenantID).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMemberNotInTenant
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func projectLiveInTenant(tx *gorm.DB, tenantID string, projectID int64) (bool, error) {
	var n int64
	err := tx.Model(&entity.Project{}).
		Where("id = ? AND tenant_id = ?", projectID, tenantID).Count(&n).Error
	return n > 0, err
}

func setTenantRoleTx(tx *gorm.DB, tenantID string, userID int, role string) error {
	return tx.Model(&User{}).Where("id = ? AND tenant_id = ?", userID, tenantID).
		Update("tenant_role", role).Error
}

func invalidateTenantRoleCache(userID int) {
	// Best effort: the DB write is the source of truth; a failed delete only
	// means the stale entry lives until its own TTL.
	_ = invalidateUserCache(userID)
}

// AddProjectMemberGranting adds userID to the project and, when the user has no
// tenant_role yet, promotes them to dept_lead — one transaction. The project
// must be live in tenantID (ErrProjectNotFound) and the user must belong to it
// (ErrMemberNotInTenant). Idempotent.
func AddProjectMemberGranting(tenantID string, projectID, userID int) error {
	if tenantID == "" || tenantID == "default" {
		return ErrTenantRoleInDefault
	}
	err := WithoutTenantIsolation(DB).Transaction(func(tx *gorm.DB) error {
		ok, err := projectLiveInTenant(tx, tenantID, int64(projectID))
		if err != nil {
			return err
		}
		if !ok {
			return ErrProjectNotFound
		}
		u, err := tenantMemberUser(tx, tenantID, userID)
		if err != nil {
			return err
		}
		return addMemberTx(tx, tenantID, int64(projectID), u)
	})
	if err != nil {
		return err
	}
	invalidateTenantRoleCache(userID)
	return nil
}

func addMemberTx(tx *gorm.DB, tenantID string, projectID int64, u *User) error {
	row := &entity.ProjectMember{
		TenantId: tenantID, ProjectId: projectID, UserId: int64(u.Id), CreatedAt: time.Now().Unix(),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
		return err
	}
	if u.TenantRole == entity.TenantRoleNone {
		return setTenantRoleTx(tx, tenantID, u.Id, entity.TenantRoleDeptLead)
	}
	return nil
}

// RemoveProjectMemberRevoking deletes one membership; when it was the user's
// last live project and the user is a dept_lead, the role is revoked in the same
// transaction (an admin keeps theirs). revoked reports whether that happened.
func RemoveProjectMemberRevoking(tenantID string, projectID, userID int) (revoked bool, err error) {
	err = WithoutTenantIsolation(DB).Transaction(func(tx *gorm.DB) error {
		u, err := tenantMemberUser(tx, tenantID, userID)
		if err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND project_id = ? AND user_id = ?", tenantID, projectID, userID).
			Delete(&entity.ProjectMember{}).Error; err != nil {
			return err
		}
		var remaining int64
		if err := tx.Table("project_members AS pm").
			Joins("JOIN projects AS p ON p.id = pm.project_id AND p.tenant_id = pm.tenant_id AND p.deleted_at IS NULL").
			Where("pm.tenant_id = ? AND pm.user_id = ?", tenantID, userID).
			Count(&remaining).Error; err != nil {
			return err
		}
		if remaining == 0 && u.TenantRole == entity.TenantRoleDeptLead {
			revoked = true
			return setTenantRoleTx(tx, tenantID, userID, entity.TenantRoleNone)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if revoked {
		invalidateTenantRoleCache(userID)
	}
	return revoked, nil
}

// ProjectMemberUser is the projection returned by the members listing.
type ProjectMemberUser struct {
	UserId      int    `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	TenantRole  string `json:"tenant_role"`
	AddedAt     int64  `json:"added_at"`
}

// ListProjectMemberUsers lists the members of a project with their user rows,
// oldest membership first. Users not (or no longer) in tenantID are omitted.
func ListProjectMemberUsers(tenantID string, projectID int) ([]ProjectMemberUser, error) {
	members, err := ListProjectMembers(tenantID, projectID)
	if err != nil || len(members) == 0 {
		return []ProjectMemberUser{}, err
	}
	ids := make([]int64, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserId)
	}
	var users []User
	if err := WithoutTenantIsolation(DB).Where("id IN ? AND tenant_id = ?", ids, tenantID).Find(&users).Error; err != nil {
		return nil, err
	}
	byID := make(map[int]User, len(users))
	for _, u := range users {
		byID[u.Id] = u
	}
	out := make([]ProjectMemberUser, 0, len(members))
	for _, m := range members {
		u, ok := byID[int(m.UserId)]
		if !ok {
			continue
		}
		out = append(out, ProjectMemberUser{
			UserId: u.Id, Username: u.Username, DisplayName: u.DisplayName,
			TenantRole: u.TenantRole, AddedAt: m.CreatedAt,
		})
	}
	return out, nil
}

// ApplyInviteGrant writes the role and project membership an invite carries for
// a freshly created user, in ONE transaction (role and membership land together
// or not at all). role "" with projectID > 0 means dept_lead. The project must
// be live in tenantID; the "default" tenant refuses any grant.
func ApplyInviteGrant(tenantID string, userID int, role string, projectID int64) error {
	if role == "" && projectID <= 0 {
		return nil
	}
	if tenantID == "" || tenantID == "default" {
		return ErrTenantRoleInDefault
	}
	if role == "" {
		role = entity.TenantRoleDeptLead
	}
	if !ValidTenantRole(role) {
		return ErrInvalidTenantRole
	}
	err := WithoutTenantIsolation(DB).Transaction(func(tx *gorm.DB) error {
		u, err := tenantMemberUser(tx, tenantID, userID)
		if err != nil {
			return err
		}
		if projectID > 0 {
			ok, err := projectLiveInTenant(tx, tenantID, projectID)
			if err != nil {
				return err
			}
			if !ok {
				return ErrProjectNotFound
			}
		}
		if err := setTenantRoleTx(tx, tenantID, userID, role); err != nil {
			return err
		}
		if projectID <= 0 {
			return nil
		}
		u.TenantRole = role
		return addMemberTx(tx, tenantID, projectID, u)
	})
	if err != nil {
		return err
	}
	invalidateTenantRoleCache(userID)
	return nil
}

// --- employee_ref / external_code uniqueness helpers -----------------------
// The partial unique indexes exist only in migration 045; these explicit checks
// give a clean 409 on every dialect (the hermetic tier has no such index).

// TokenEmployeeRefTaken reports whether a live token of tenantID already
// carries employeeRef, ignoring the token exceptID (0 = none).
func TokenEmployeeRefTaken(tenantID, employeeRef string, exceptID int) (bool, error) {
	if employeeRef == "" {
		return false, nil
	}
	var n int64
	err := DB.Model(&Token{}).
		Where("tenant_id = ? AND employee_ref = ? AND id <> ?", tenantID, employeeRef, exceptID).
		Count(&n).Error
	return n > 0, err
}

// ExistingEmployeeRefs maps employee_ref -> token for the live tokens of
// tenantID whose ref is in refs.
func ExistingEmployeeRefs(tenantID string, refs []string) (map[string]*Token, error) {
	out := map[string]*Token{}
	if len(refs) == 0 {
		return out, nil
	}
	var rows []*Token
	if err := DB.Where("tenant_id = ? AND employee_ref IN ?", tenantID, refs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, t := range rows {
		out[t.EmployeeRef] = t
	}
	return out, nil
}

// CreateTokensBatch inserts every token in ONE transaction: any failure rolls
// the whole batch back.
func CreateTokensBatch(tenantID string, tokens []*Token) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		for _, t := range tokens {
			if err := WithTenantID(tx, tenantID).Create(t).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ErrProjectExternalCodeExists: a live project of the tenant already uses the code.
var ErrProjectExternalCodeExists = errors.New("project external_code already exists in this tenant")

// ProjectExternalCodeTaken reports whether a live project of tenantID already
// uses code, ignoring project exceptID (0 = none).
func ProjectExternalCodeTaken(tenantID, code string, exceptID int) (bool, error) {
	if code == "" {
		return false, nil
	}
	var n int64
	err := DB.Model(&entity.Project{}).
		Where("tenant_id = ? AND external_code = ? AND id <> ?", tenantID, code, exceptID).
		Count(&n).Error
	return n > 0, err
}

// SetProjectExternalCode writes projects.external_code ("" clears it) and
// returns the fresh row. Uniqueness is checked here and by the partial index.
func SetProjectExternalCode(tenantID string, id int, code string) (*entity.Project, error) {
	code = strings.TrimSpace(code)
	if _, err := GetProjectByID(tenantID, id); err != nil {
		return nil, err
	}
	taken, err := ProjectExternalCodeTaken(tenantID, code, id)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrProjectExternalCodeExists
	}
	if err := DB.Model(&entity.Project{}).Where("id = ? AND tenant_id = ?", id, tenantID).
		Update("external_code", code).Error; err != nil {
		if isUniqueViolation(err) {
			return nil, ErrProjectExternalCodeExists
		}
		return nil, err
	}
	return GetProjectByID(tenantID, id)
}

// HardDeleteProject removes a just-created project row (rollback of a create
// whose follow-up write failed). Never used for retirement; see SoftDeleteProject.
func HardDeleteProject(tenantID string, id int) error {
	return DB.Unscoped().Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&entity.Project{}).Error
}

// RevokeDeptLeadsWithoutProjects revokes dept_lead from each of userIDs that no
// longer has any live project membership in tenantID (admins keep their role),
// and drops their user-cache entry. Called after a project is soft-deleted so a
// lead whose only project vanished does not keep a role that can no longer be
// removed through DELETE /members (a retired project resolves to 404). Returns
// the user ids that were revoked.
func RevokeDeptLeadsWithoutProjects(tenantID string, userIDs []int64) ([]int, error) {
	if tenantID == "" || tenantID == "default" || len(userIDs) == 0 {
		return nil, nil
	}
	var revoked []int
	err := WithoutTenantIsolation(DB).Transaction(func(tx *gorm.DB) error {
		for _, uid := range userIDs {
			var u User
			err := tx.Where("id = ? AND tenant_id = ? AND tenant_role = ?", uid, tenantID, entity.TenantRoleDeptLead).Take(&u).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			var remaining int64
			if err := tx.Table("project_members AS pm").
				Joins("JOIN projects AS p ON p.id = pm.project_id AND p.tenant_id = pm.tenant_id AND p.deleted_at IS NULL").
				Where("pm.tenant_id = ? AND pm.user_id = ?", tenantID, uid).
				Count(&remaining).Error; err != nil {
				return err
			}
			if remaining > 0 {
				continue
			}
			if err := setTenantRoleTx(tx, tenantID, u.Id, entity.TenantRoleNone); err != nil {
				return err
			}
			revoked = append(revoked, u.Id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, uid := range revoked {
		invalidateTenantRoleCache(uid)
	}
	return revoked, nil
}

// RetiredProjectExternalCodeTaken reports whether restoring the soft-deleted
// project id would collide with a live project on external_code (the partial
// unique index would otherwise surface as a misleading name conflict).
func RetiredProjectExternalCodeTaken(tenantID string, id int) (bool, error) {
	row, err := getProjectByIDUnscoped(tenantID, id)
	if err != nil {
		return false, err
	}
	if !row.DeletedAt.Valid {
		return false, nil
	}
	return ProjectExternalCodeTaken(tenantID, row.ExternalCode, id)
}
