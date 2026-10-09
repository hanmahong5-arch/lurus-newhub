package entity

// ProjectMember binds a user to a project inside one tenant (migration 046).
// It is the subject the department-lead role (users.tenant_role = "dept_lead")
// is scoped by: a lead sees the logs/spend of exactly the projects they are a
// member of. TenantId is repeated here (rather than derived through projects)
// so every query can be tenant-scoped without a join.
//
// The unique key and the (tenant_id, user_id) index are declared both here and in
// migration 046 under the same names, so either creation path yields one schema.
type ProjectMember struct {
	Id        int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantId  string `json:"tenant_id" gorm:"type:varchar(36);not null;uniqueIndex:uk_project_members_tenant_project_user,priority:1;index:idx_project_members_tenant_user,priority:1"`
	ProjectId int64  `json:"project_id" gorm:"type:bigint;not null;uniqueIndex:uk_project_members_tenant_project_user,priority:2"`
	UserId    int64  `json:"user_id" gorm:"type:bigint;not null;uniqueIndex:uk_project_members_tenant_project_user,priority:3;index:idx_project_members_tenant_user,priority:2"`
	CreatedAt int64  `json:"created_at" gorm:"type:bigint;not null"`
}

func (ProjectMember) TableName() string {
	return "project_members"
}
