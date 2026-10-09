package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// v2_project.go — CRUD for cost-attribution projects (migration 029).
//
// AUTHORIZATION SHAPE, and why it is asymmetric:
//   - WRITES are tenant-admin only (requireTenantAdmin, matching every other
//     tenant-configuration surface). Renaming or deleting a project re-shapes
//     every spend report the tenant reads.
//   - READS of the project LIST are open to any user in the tenant. The token
//     page needs a project picker, and an ordinary member creating their own
//     key must be able to use it. This leaks nothing new: a project is a
//     LABEL, not a permission boundary — this codebase has no tenant-level
//     role table and no per-project subject to gate on (see entity/project.go).
//   - The SPEND report (GET /projects/spend) is the exception since cycle 13
//     L9: it rolls up every member's usage, so it is tenant-admin only
//     (projectAdminCtx), and the console renders a restricted panel for a
//     member instead of an empty report.
//
// Tenant isolation comes from the repo layer: every repo/project.go function
// takes tenantID as a mandatory positional argument, so a project id belonging
// to another tenant resolves to ErrProjectNotFound -> 404 here, deliberately
// indistinguishable from a nonexistent id.

const maxProjectNameLen = 128
const maxProjectDescriptionLen = 512

// projectView is the field-whitelisted projection. tenant_id is omitted
// (implicit from the route) and deleted_at never leaves the server.
type projectView struct {
	Id          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// MonthlyBudgetQuota is the enforced monthly cap in quota units, 0 = none
	// (migration 043; app.enforceProjectBudget).
	MonthlyBudgetQuota int64 `json:"monthly_budget_quota"`
	// ExternalCode is the department code a customer gateway sends in
	// X-Lurus-Dept (migration 045); "" = none.
	ExternalCode string `json:"external_code"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	// Deleted marks a retired (soft-deleted) row, only ever present when the
	// caller asked for include_deleted. It is what lets the console show an
	// undo affordance instead of pretending the project never existed.
	Deleted bool `json:"deleted"`
}

func toProjectView(p *entity.Project) projectView {
	return projectView{
		Id:                 p.Id,
		Name:               p.Name,
		Description:        p.Description,
		MonthlyBudgetQuota: p.MonthlyBudgetQuota,
		ExternalCode:       p.ExternalCode,
		CreatedAt:          p.CreatedAt.Unix(),
		UpdatedAt:          p.UpdatedAt.Unix(),
		Deleted:            p.DeletedAt.Valid,
	}
}

func toProjectViews(rows []entity.Project) []projectView {
	items := make([]projectView, 0, len(rows))
	for i := range rows {
		items = append(items, toProjectView(&rows[i]))
	}
	return items
}

// projectTenantCtx resolves the tenant context, replying 401 when absent.
func projectTenantCtx(c *gin.Context) (*middleware.TenantContext, bool) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return nil, false
	}
	return tenantCtx, true
}

// projectAdminCtx additionally enforces tenant-admin for the write routes.
func projectAdminCtx(c *gin.Context) (*middleware.TenantContext, bool) {
	tenantCtx, ok := projectTenantCtx(c)
	if !ok {
		return nil, false
	}
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return nil, false
	}
	return tenantCtx, true
}

// respondProjectRepoErr maps repo sentinel errors onto status codes.
// ErrProjectNotFound covers both "no such id" and "belongs to another tenant"
// on purpose, so the endpoint cannot be used to probe another tenant's ids.
func respondProjectRepoErr(c *gin.Context, err error, what string) {
	switch {
	case errors.Is(err, repo.ErrProjectNotFound):
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Project not found",
		})
	case errors.Is(err, repo.ErrProjectExternalCodeExists):
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"message":    "A project with this external_code already exists",
			"error_code": "PROJECT_EXTERNAL_CODE_CONFLICT",
		})
	case errors.Is(err, repo.ErrProjectNameExists):
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": "A project with this name already exists",
		})
	default:
		common.SysError(what + ": " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to " + what,
		})
	}
}

// validateProjectPayload rejects client input the repo would also refuse, but
// with a 400 instead of a 500. The blank-name check has to live here as well as
// in repo.CreateProject: `binding:"required"` only rejects a MISSING field, so
// "   " arrives as a present-but-empty name, and matching on the repo's error
// STRING to classify it would be a status code hanging off a message literal.
func validateProjectPayload(c *gin.Context, name, description string) bool {
	if strings.TrimSpace(name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Project name is required",
		})
		return false
	}
	if len([]rune(name)) > maxProjectNameLen {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Project name is too long",
		})
		return false
	}
	if len([]rune(description)) > maxProjectDescriptionLen {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Project description is too long",
		})
		return false
	}
	return true
}

// validateProjectBudget rejects a negative cap. nil (field omitted) is fine:
// create treats it as "no cap", update as "leave the cap alone".
func validateProjectBudget(c *gin.Context, budget *int64) bool {
	if budget != nil && *budget < 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Project monthly budget must be zero or positive",
		})
		return false
	}
	return true
}

// validateTokenProject checks a project id supplied on the token create/update
// path. 0 (unassigned) is always allowed; anything else must resolve INSIDE the
// caller's own tenant. Replies 400 and returns false when it does not.
//
// This is the only validation standing between a client-supplied integer and
// the project_id column: there is no foreign key, so an unvalidated value would
// be stored happily and would show up in another tenant's spend report as rows
// it cannot explain. The v1 token handlers deliberately do NOT accept this
// field at all (see app.BuildCleanToken / app.ApplyTokenUpdate) because they
// bind the whole request body with no tenant validation.
func validateTokenProject(c *gin.Context, tenantID string, projectID int) bool {
	if projectID == entity.ProjectUnassigned {
		return true
	}
	if _, err := repo.GetProjectByID(tenantID, projectID); err != nil {
		if errors.Is(err, repo.ErrProjectNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "Project not found in this tenant",
			})
			return false
		}
		common.SysError("validate token project: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to validate project",
		})
		return false
	}
	return true
}

// projectIDParam parses the :id path segment, replying 400 when it is not a
// number. Shared so every project route rejects garbage identically.
func projectIDParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid project ID",
		})
		return 0, false
	}
	return id, true
}

// ListProjectsV2 lists the tenant's projects.
// Route: GET /api/v2/:tenant_slug/projects
func ListProjectsV2(c *gin.Context) {
	tenantCtx, ok := projectTenantCtx(c)
	if !ok {
		return
	}

	// include_deleted is opt-in: the default listing stays live-only so no
	// existing client suddenly starts rendering retired projects.
	includeDeleted := c.Query("include_deleted") == "1" || c.Query("include_deleted") == "true"

	rows, err := repo.ListProjectsByTenant(tenantCtx.TenantID, includeDeleted)
	if err != nil {
		respondProjectRepoErr(c, err, "list projects")
		return
	}

	// A dept_lead sees only the departments they belong to (zero-usage ones
	// included: this is membership, not spend). Admins, platform staff and
	// plain members keep the full list (the token page's project picker).
	if all, ids := allowedProjectIDs(c, tenantCtx); !all && tenantRoleOf(tenantCtx) == entity.TenantRoleDeptLead {
		rows = filterProjectsToIDs(rows, ids)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": toProjectViews(rows),
			"total": len(rows),
		},
	})
}

// filterProjectsToIDs keeps only the projects whose id is in ids.
func filterProjectsToIDs(rows []entity.Project, ids []int) []entity.Project {
	keep := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		keep[id] = struct{}{}
	}
	out := make([]entity.Project, 0, len(rows))
	for _, r := range rows {
		if _, ok := keep[r.Id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// RestoreProjectV2 is the undo for DeleteProjectV2.
// Route: POST /api/v2/:tenant_slug/projects/:id/restore
//
// Body (all optional):
//
//	reattach_token_ids  []int  the ids DELETE returned, so the tokens that were
//	                           detached go back where they were
//
// Safe to click repeatedly: restoring a project that is already live succeeds
// as a no-op, and re-attachment skips any token the user has since assigned
// elsewhere (an undo must never overwrite a newer, deliberate decision).
//
// 409 when a LIVE project has taken the name in the meantime — the partial
// unique index is what allowed that, and the user has to rename one of them.
func RestoreProjectV2(c *gin.Context) {
	tenantCtx, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	id, ok := projectIDParam(c)
	if !ok {
		return
	}

	var req struct {
		ReattachTokenIDs []int `json:"reattach_token_ids"`
	}
	// A body is optional: restoring the project alone is a valid request, so a
	// missing/!malformed payload must not fail the undo.
	_ = c.ShouldBindJSON(&req)

	if taken, terr := repo.RetiredProjectExternalCodeTaken(tenantCtx.TenantID, id); terr == nil && taken {
		respondProjectRepoErr(c, repo.ErrProjectExternalCodeExists, "restore project")
		return
	}
	row, err := repo.RestoreProject(tenantCtx.TenantID, id, req.ReattachTokenIDs)
	if err != nil {
		respondProjectRepoErr(c, err, "restore project")
		return
	}
	repo.ResetProjectDeptCache()

	detailBytes, _ := json.Marshal(map[string]interface{}{
		"name":               row.Name,
		"reattach_token_ids": req.ReattachTokenIDs,
	})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionProjectRestored, governance.ResourceProject, id, string(detailBytes)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Project restored successfully",
		"data":    toProjectView(row),
	})
}

// GetProjectSpendV2 reports per-project consume spend for the caller's tenant.
// Route: GET /api/v2/:tenant_slug/projects/spend?start=&end=
//
// Tenant-admin only (cycle-13 L9). The numbers here are the whole tenant's
// consume totals — every member's spend, rolled up by project — which is the
// same blast radius as GET /logs/all and GET /logs/stat/all, both of which
// carry the admin gate. The comment this replaced justified the open read by
// claiming the figures were "already visible through GET /logs/all"; that
// route refuses a plain member outright (requireTenantAdmin in
// GetAllLogsV2), and the member-scoped GET /logs returns only the caller's
// own rows, so the spend report was in fact the one place a plain member
// could read the tenant's total. The project LIST stays readable by any
// member — the token page's project picker needs it and it carries no
// amounts.
//
// For a tenant admin the response includes the project_id = 0 "unassigned"// bucket, so the rows sum to the tenant's total consume spend for the window. A// dept_lead sees only the rows of their own projects (never the unassigned// bucket), and total_quota sums just those rows.
func GetProjectSpendV2(c *gin.Context) {
	tenantCtx, ok := projectTenantCtx(c)
	if !ok {
		return
	}
	// Tenant admins see everything; a dept_lead sees only their own projects;
	// anyone else is refused as before.
	all, leadIDs := allowedProjectIDs(c, tenantCtx)
	if !all && tenantRoleOf(tenantCtx) != entity.TenantRoleDeptLead {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}
	start, _ := strconv.ParseInt(c.Query("start"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end"), 10, 64)

	rows, err := repo.GetSpendByProject(repo.ForTenant(tenantCtx.TenantID), start, end)
	if err != nil {
		respondProjectRepoErr(c, err, "load project spend")
		return
	}

	if !all {
		rows = filterSpendRowsToProjects(rows, leadIDs)
	}

	var total int64
	for _, r := range rows {
		total += r.TotalQuota
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": rows,
			// total_quota is the sum of the rows above, unassigned included —
			// clients can assert the invariant instead of trusting it.
			"total_quota": total,
			"start":       start,
			"end":         end,
		},
	})
}

// GetProjectV2 fetches one project of the caller's tenant.
// Route: GET /api/v2/:tenant_slug/projects/:id
func GetProjectV2(c *gin.Context) {
	tenantCtx, ok := projectTenantCtx(c)
	if !ok {
		return
	}
	id, ok := projectIDParam(c)
	if !ok {
		return
	}

	row, err := repo.GetProjectByID(tenantCtx.TenantID, id)
	if err != nil {
		respondProjectRepoErr(c, err, "get project")
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": toProjectView(row)})
}

// CreateProjectV2 adds a project to the caller's own tenant.
// Route: POST /api/v2/:tenant_slug/projects
func CreateProjectV2(c *gin.Context) {
	tenantCtx, ok := projectAdminCtx(c)
	if !ok {
		return
	}

	var req struct {
		Name               string `json:"name" binding:"required"`
		Description        string `json:"description"`
		MonthlyBudgetQuota *int64 `json:"monthly_budget_quota"`
		ExternalCode       string `json:"external_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}
	if !validateProjectPayload(c, req.Name, req.Description) || !validateProjectBudget(c, req.MonthlyBudgetQuota) ||
		!validateExternalCode(c, tenantCtx.TenantID, req.ExternalCode, 0) {
		return
	}

	row, err := repo.CreateProject(tenantCtx.TenantID, req.Name, req.Description)
	if err != nil {
		respondProjectRepoErr(c, err, "create project")
		return
	}
	if code := strings.TrimSpace(req.ExternalCode); code != "" {
		coded, cerr := repo.SetProjectExternalCode(tenantCtx.TenantID, row.Id, code)
		if cerr != nil {
			// Lost a race on the code: do not leave a code-less project behind.
			_ = repo.HardDeleteProject(tenantCtx.TenantID, row.Id)
			respondProjectRepoErr(c, cerr, "set project external code")
			return
		}
		row = coded
	}
	repo.ResetProjectDeptCache()
	if req.MonthlyBudgetQuota != nil && *req.MonthlyBudgetQuota > 0 {
		if row, err = repo.SetProjectMonthlyBudget(tenantCtx.TenantID, row.Id, *req.MonthlyBudgetQuota); err != nil {
			respondProjectRepoErr(c, err, "set project budget")
			return
		}
	}

	detailBytes, _ := json.Marshal(map[string]string{"name": row.Name})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionProjectCreated, governance.ResourceProject, row.Id, string(detailBytes)))

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Project created successfully",
		"data":    toProjectView(row),
	})
}

// UpdateProjectV2 renames / re-describes a project of the caller's tenant.
// Route: PUT /api/v2/:tenant_slug/projects/:id
func UpdateProjectV2(c *gin.Context) {
	tenantCtx, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	id, ok := projectIDParam(c)
	if !ok {
		return
	}

	var req struct {
		Name               string `json:"name" binding:"required"`
		Description        string `json:"description"`
		MonthlyBudgetQuota *int64 `json:"monthly_budget_quota"`
		// ExternalCode: nil = leave alone, "" clears.
		ExternalCode *string `json:"external_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}
	if !validateProjectPayload(c, req.Name, req.Description) || !validateProjectBudget(c, req.MonthlyBudgetQuota) {
		return
	}
	if req.ExternalCode != nil && !validateExternalCode(c, tenantCtx.TenantID, *req.ExternalCode, id) {
		return
	}

	prev, err := repo.GetProjectByID(tenantCtx.TenantID, id)
	if err != nil {
		respondProjectRepoErr(c, err, "get project")
		return
	}

	row, err := repo.UpdateProject(tenantCtx.TenantID, id, req.Name, req.Description)
	if err != nil {
		respondProjectRepoErr(c, err, "update project")
		return
	}
	if req.ExternalCode != nil {
		if row, err = repo.SetProjectExternalCode(tenantCtx.TenantID, id, *req.ExternalCode); err != nil {
			respondProjectRepoErr(c, err, "set project external code")
			return
		}
	}
	// The dept-header cache keys on name AND external_code; drop it so a rename
	// or re-code attributes correctly now rather than after the TTL.
	repo.ResetProjectDeptCache()
	// Omitted budget = leave the cap alone (a client built before 043 must
	// not clear caps by renaming); present = set it, 0 clears.
	if req.MonthlyBudgetQuota != nil {
		if row, err = repo.SetProjectMonthlyBudget(tenantCtx.TenantID, id, *req.MonthlyBudgetQuota); err != nil {
			respondProjectRepoErr(c, err, "set project budget")
			return
		}
	}

	// Record the rename explicitly: historical log rows keep only the numeric
	// id, so without this the audit trail is the only way to reconstruct what
	// a past report's project label meant. The budget goes in for the same
	// reason: a 402 project_budget_exceeded next month is explained by this.
	detail := map[string]string{
		"old_name": prev.Name,
		"new_name": row.Name,
	}
	if req.MonthlyBudgetQuota != nil {
		detail["old_monthly_budget_quota"] = strconv.FormatInt(prev.MonthlyBudgetQuota, 10)
		detail["new_monthly_budget_quota"] = strconv.FormatInt(row.MonthlyBudgetQuota, 10)
	}
	detailBytes, _ := json.Marshal(detail)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionProjectUpdated, governance.ResourceProject, row.Id, string(detailBytes)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Project updated successfully",
		"data":    toProjectView(row),
	})
}

// DeleteProjectV2 soft-deletes a project and detaches its tokens.
// Route: DELETE /api/v2/:tenant_slug/projects/:id
//
// Historical spend keeps the numeric project_id forever and still resolves to
// the old name (repo.ResolveProjectNames reads Unscoped), so reports stay
// complete — per-project figures must always sum to the tenant total.
func DeleteProjectV2(c *gin.Context) {
	tenantCtx, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	id, ok := projectIDParam(c)
	if !ok {
		return
	}

	// Leads of this project may lose their last live project with it; a retired
	// project 404s on DELETE /members, so revoke their role here (revoked leads
	// are re-promoted by POST /members if the project is restored).
	var leadIDs []int64
	if members, merr := repo.ListProjectMembers(tenantCtx.TenantID, id); merr == nil {
		for _, m := range members {
			leadIDs = append(leadIDs, m.UserId)
		}
	}

	detached, err := repo.SoftDeleteProject(tenantCtx.TenantID, id)
	if err != nil {
		respondProjectRepoErr(c, err, "delete project")
		return
	}
	repo.ResetProjectDeptCache()
	if _, rerr := repo.RevokeDeptLeadsWithoutProjects(tenantCtx.TenantID, leadIDs); rerr != nil {
		common.SysError("revoke dept_lead after project delete: " + rerr.Error())
	}

	detailBytes, _ := json.Marshal(map[string]interface{}{
		"detached_token_ids": detached,
	})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionProjectDeleted, governance.ResourceProject, id, string(detailBytes)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Project deleted successfully",
		"data": gin.H{
			// Hand the undo information back to the caller. Nothing else in
			// the schema records which tokens used to point at this project,
			// so without this the detach would be a one-way door.
			"detached_token_ids": detached,
			"project_id":         id,
		},
	})
}
