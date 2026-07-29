package entity

import (
	"time"

	"gorm.io/gorm"
)

// Project is the cost-attribution dimension between a tenant and its tokens
// (migration 029). Every relay log row carries the project_id of the token
// that produced it, so a tenant admin can answer "which department spent what
// this month" — the one question the existing dimensions (tenant / user /
// token / model / channel / group / source_product) cannot.
//
// A Project is a LABEL, NOT A PERMISSION BOUNDARY.
//
// The word "project" invites the assumption that it carries members and an
// owner. In this codebase it does not, and cannot yet: a user belongs to
// exactly one tenant, there is no tenant-level role table, and
// TenantContext.Roles is empty for every session user (middleware/oidc_auth.go).
// There is therefore no subject a per-project permission could be attached to.
// Projects are maintained by tenant admins (requireTenantAdmin on the write
// routes) and are readable by every user inside the tenant — exactly the
// visibility a tenant admin already has over that tenant's logs
// (GET /logs/all shows every member's rows). Do not add per-project access
// control here without first introducing the subject it would gate.
//
// Attribution cannot be backfilled: a log row written without a project_id is
// permanently unattributable, which is why the tagging path shipped before the
// console did.
//
// Deletion is SOFT (DeletedAt). A hard delete would strand historical log rows
// pointing at an id that no longer resolves, and the spend report would then
// either drop those rows (under-reporting the total) or render "project 17".
// Name resolution for reports therefore reads through Unscoped()
// (repo.ResolveProjectNames).
//
// Budget enforcement (chargeback) is deliberately NOT part of this model — it
// lives on the relay hot path and is a separate change. No unused budget
// column is pre-created here: a column that looks like a spending guard but
// enforces nothing is exactly the dead configuration the repo's structural
// tests exist to police.
type Project struct {
	Id int `json:"id" gorm:"primaryKey"`
	// TenantId is the owning tenant. There is no FK to tenants (neither has
	// one anywhere in this schema); cross-tenant safety comes from every repo
	// function taking tenantID as a mandatory argument — see
	// repo.GetProjectByID / repo.ResolveProjectNames.
	TenantId string `json:"tenant_id" gorm:"type:varchar(36);not null;default:'default';index"`
	// Name is unique per tenant among live rows. The partial unique index
	// (uk_projects_tenant_name ... WHERE deleted_at IS NULL) is created ONLY by
	// migration 029 — deliberately NOT by a GORM uniqueIndex tag, because GORM
	// cannot express the partial predicate, so the two creation paths would
	// produce different indexes under the same name and diverge (the failure
	// mode documented in the header of migration 026).
	Name        string         `json:"name" gorm:"type:varchar(128);not null"`
	Description string         `json:"description" gorm:"type:varchar(512);not null;default:''"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

func (Project) TableName() string {
	return "projects"
}

// ProjectUnassigned is the project_id stored on tokens and log rows that carry
// no project attribution. 0 (not NULL) is the repo-wide convention for a
// missing int reference — TokenId, IdentityAccountID, CreatorUserId and
// RateLimitRPM all use it — so aggregates need no COALESCE and readers need no
// nil check. Reports MUST surface it as a first-class "unassigned" row:
// otherwise the per-project figures do not sum to the tenant total and the
// finance user stops trusting the page on day one.
const ProjectUnassigned = 0
