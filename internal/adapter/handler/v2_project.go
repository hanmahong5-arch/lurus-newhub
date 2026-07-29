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
//   - READS are open to any user in the tenant. The token page needs a project
//     picker, and an ordinary member creating their own key must be able to
//     use it. This leaks nothing new: a project is a LABEL, not a permission
//     boundary — this codebase has no tenant-level role table and no
//     per-project subject to gate on (see entity/project.go).
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
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	// Deleted marks a retired (soft-deleted) row, only ever present when the
	// caller asked for include_deleted. It is what lets the console show an
	// undo affordance instead of pretending the project never existed.
	Deleted bool `json:"deleted"`
}

func toProjectView(p *entity.Project) projectView {
	return projectView{
		Id:          p.Id,
		Name:        p.Name,
		Description: p.Description,
		CreatedAt:   p.CreatedAt.Unix(),
		UpdatedAt:   p.UpdatedAt.Unix(),
		Deleted:     p.DeletedAt.Valid,
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": toProjectViews(rows),
			"total": len(rows),
		},
	})
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

	row, err := repo.RestoreProject(tenantCtx.TenantID, id, req.ReattachTokenIDs)
	if err != nil {
		respondProjectRepoErr(c, err, "restore project")
		return
	}

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
// Readable by any user in the tenant, same as the project listing: the numbers
// are already visible through GET /logs/all, which shows every member's rows
// to any tenant admin and each member their own.
//
// The response ALWAYS includes the project_id = 0 "unassigned" bucket, so the
// rows sum to the tenant's total consume spend for the window.
func GetProjectSpendV2(c *gin.Context) {
	tenantCtx, ok := projectTenantCtx(c)
	if !ok {
		return
	}
	start, _ := strconv.ParseInt(c.Query("start"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end"), 10, 64)

	rows, err := repo.GetSpendByProject(repo.ForTenant(tenantCtx.TenantID), start, end)
	if err != nil {
		respondProjectRepoErr(c, err, "load project spend")
		return
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
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}
	if !validateProjectPayload(c, req.Name, req.Description) {
		return
	}

	row, err := repo.CreateProject(tenantCtx.TenantID, req.Name, req.Description)
	if err != nil {
		respondProjectRepoErr(c, err, "create project")
		return
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
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}
	if !validateProjectPayload(c, req.Name, req.Description) {
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

	// Record the rename explicitly: historical log rows keep only the numeric
	// id, so without this the audit trail is the only way to reconstruct what
	// a past report's project label meant.
	detailBytes, _ := json.Marshal(map[string]string{
		"old_name": prev.Name,
		"new_name": row.Name,
	})
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

	detached, err := repo.SoftDeleteProject(tenantCtx.TenantID, id)
	if err != nil {
		respondProjectRepoErr(c, err, "delete project")
		return
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
