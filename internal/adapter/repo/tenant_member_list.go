package repo

import (
	"strings"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// tenant_member_list.go — the tenant roster read behind GET /:tenant_slug/members.
// Every query carries tenant_id as an explicit WHERE clause.

// TenantMemberDept is one live department a member belongs to.
type TenantMemberDept struct {
	ProjectId int    `json:"project_id"`
	Name      string `json:"name"`
	IsLead    bool   `json:"is_lead"`
}

// TenantMemberRow is a roster entry. users has no creation timestamp column,
// so no join time is reported (the handler says so by omission).
type TenantMemberRow struct {
	User        User
	Departments []TenantMemberDept
}

// TenantMemberFilter narrows the roster. Role: "" = any, "admin", "dept_lead",
// or "member" (= no tenant_role).
type TenantMemberFilter struct {
	Role    string
	Keyword string
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ListTenantMembers returns one page of the tenant's live users (id ASC) with
// their live departments, plus the total matching count.
func ListTenantMembers(tenantID string, f TenantMemberFilter, offset, limit int) ([]TenantMemberRow, int64, error) {
	q := WithoutTenantIsolation(DB).Model(&User{}).Where("tenant_id = ?", tenantID)
	switch f.Role {
	case "":
	case "member":
		q = q.Where("tenant_role = ?", entity.TenantRoleNone)
	default:
		q = q.Where("tenant_role = ?", f.Role)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + escapeLike(kw) + "%"
		q = q.Where(`(username LIKE ? ESCAPE '\' OR display_name LIKE ? ESCAPE '\' OR email LIKE ? ESCAPE '\')`, like, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []User
	if err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]TenantMemberRow, 0, len(users))
	if len(users) == 0 {
		return rows, total, nil
	}
	ids := make([]int, 0, len(users))
	idx := make(map[int]int, len(users))
	for i, u := range users {
		ids = append(ids, u.Id)
		idx[u.Id] = i
		rows = append(rows, TenantMemberRow{User: u, Departments: []TenantMemberDept{}})
	}
	var deps []struct {
		UserId    int
		ProjectId int
		Name      string
	}
	if err := DB.Table("project_members AS pm").
		Select("pm.user_id AS user_id, p.id AS project_id, p.name AS name").
		Joins("JOIN projects AS p ON p.id = pm.project_id AND p.tenant_id = pm.tenant_id AND p.deleted_at IS NULL").
		Where("pm.tenant_id = ? AND pm.user_id IN ?", tenantID, ids).
		Order("p.id ASC").Scan(&deps).Error; err != nil {
		return nil, 0, err
	}
	for _, d := range deps {
		i := idx[d.UserId]
		// Membership in a project is what makes a dept_lead a lead of it; a
		// plain admin who was added is listed but is not "lead" by role.
		rows[i].Departments = append(rows[i].Departments, TenantMemberDept{
			ProjectId: d.ProjectId, Name: d.Name, IsLead: rows[i].User.TenantRole == entity.TenantRoleDeptLead,
		})
	}
	return rows, total, nil
}

// TenantTokenFilter narrows the tenant-wide token listing.
type TenantTokenFilter struct {
	UserId    int
	ProjectId int // 0 = any
	Keyword   string
	Status    int // 0 = any
}

// ListTokensByTenant returns one page of every token of tenantID (id DESC)
// with the total matching count.
func ListTokensByTenant(tenantID string, f TenantTokenFilter, offset, limit int) ([]*Token, int64, error) {
	q := DB.Model(&Token{}).Where("tenant_id = ?", tenantID)
	if f.UserId > 0 {
		q = q.Where("user_id = ?", f.UserId)
	}
	if f.ProjectId > 0 {
		q = q.Where("project_id = ?", f.ProjectId)
	}
	if f.Status > 0 {
		q = q.Where("status = ?", f.Status)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + escapeLike(kw) + "%"
		q = q.Where(`(name LIKE ? ESCAPE '\' OR employee_ref LIKE ? ESCAPE '\')`, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*Token
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&out).Error
	return out, total, err
}

// UserNamesByID maps id -> (display name or username) for users of tenantID.
func UserNamesByID(tenantID string, ids []int) (map[int]string, error) {
	out := map[int]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var us []User
	if err := WithoutTenantIsolation(DB).Select("id", "username", "display_name").
		Where("tenant_id = ? AND id IN ?", tenantID, ids).Find(&us).Error; err != nil {
		return nil, err
	}
	for _, u := range us {
		n := u.DisplayName
		if n == "" {
			n = u.Username
		}
		out[u.Id] = n
	}
	return out, nil
}
