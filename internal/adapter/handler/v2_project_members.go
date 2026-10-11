package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// v2_project_members.go — project membership (who a dept_lead leads), the
// project external_code validator, the dept_lead spend filter and the
// tenant-scoped role/project invite (docs/plans/enterprise-hub §2.4).

const (
	maxProjectExternalCodeLen = 64

	errCodeRoleForbiddenInDefault = "TENANT_ROLE_FORBIDDEN_IN_DEFAULT"

	// An invite never lives forever: omitted ttl_hours means 72h, the ceiling is 30 days.
	defaultInviteTTLHours   = 72
	maxInviteTTLHours       = 30 * 24
	inviteErrCodeTTLInvalid = "INVITE_TTL_INVALID"
)

// validateExternalCode checks a project external_code (empty = none): length,
// no control characters, and unused by another live project of the tenant
// (exceptID = the project being edited). Replies and returns false on failure.
func validateExternalCode(c *gin.Context, tenantID, raw string, exceptID int) bool {
	code := strings.TrimSpace(raw)
	if code == "" {
		return true
	}
	if len(code) > maxProjectExternalCodeLen {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "external_code must be at most 64 characters",
		})
		return false
	}
	for _, r := range code {
		if unicode.IsControl(r) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "external_code must not contain control characters",
			})
			return false
		}
	}
	taken, err := repo.ProjectExternalCodeTaken(tenantID, code, exceptID)
	if err != nil {
		respondProjectRepoErr(c, err, "check project external code")
		return false
	}
	if taken {
		respondProjectRepoErr(c, repo.ErrProjectExternalCodeExists, "check project external code")
		return false
	}
	return true
}

// filterSpendRowsToProjects keeps only rows of the given project ids; the
// unassigned bucket (project_id 0) is never part of a dept_lead's view.
func filterSpendRowsToProjects(rows []repo.ProjectSpendRow, ids []int) []repo.ProjectSpendRow {
	allowed := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	out := make([]repo.ProjectSpendRow, 0, len(rows))
	for _, r := range rows {
		if r.Unassigned || r.ProjectId == 0 {
			continue
		}
		if _, ok := allowed[r.ProjectId]; ok {
			out = append(out, r)
		}
	}
	return out
}

// respondMemberRepoErr maps the membership writers' errors.
func respondMemberRepoErr(c *gin.Context, err error, op string) {
	switch {
	case errors.Is(err, repo.ErrTenantRoleInDefault):
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "Roles and project membership are not available in the default tenant",
			"error_code": errCodeRoleForbiddenInDefault,
		})
	case errors.Is(err, repo.ErrProjectNotFound), errors.Is(err, repo.ErrMemberNotInTenant):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": err.Error()})
	default:
		common.SysError(op + ": " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to " + op})
	}
}

func auditMember(c *gin.Context, tc *middleware.TenantContext, action string, projectID, userID int) {
	detail, _ := json.Marshal(map[string]int{"project_id": projectID, "user_id": userID})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tc.UserID,
		action, governance.ResourceProject, projectID, string(detail)))
}

// ListProjectMembersV2 — GET /api/v2/:tenant_slug/projects/:id/members (admin).
func ListProjectMembersV2(c *gin.Context) {
	tc, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	id, ok := projectIDParam(c)
	if !ok {
		return
	}
	if _, err := repo.GetProjectByID(tc.TenantID, id); err != nil {
		respondMemberRepoErr(c, err, "list project members")
		return
	}
	items, err := repo.ListProjectMemberUsers(tc.TenantID, id)
	if err != nil {
		respondMemberRepoErr(c, err, "list project members")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items}})
}

// AddProjectMemberV2 — POST /api/v2/:tenant_slug/projects/:id/members {user_id}.
// A user with no tenant_role becomes dept_lead; idempotent.
func AddProjectMemberV2(c *gin.Context) {
	tc, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	id, ok := projectIDParam(c)
	if !ok {
		return
	}
	var req struct {
		UserId int `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "user_id is required"})
		return
	}
	if err := repo.AddProjectMemberGranting(tc.TenantID, id, req.UserId); err != nil {
		respondMemberRepoErr(c, err, "add project member")
		return
	}
	auditMember(c, tc, governance.ActionProjectMemberAdded, id, req.UserId)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RemoveProjectMemberV2 — DELETE /api/v2/:tenant_slug/projects/:id/members?user_id=N
// (or {user_id} in the body). A dept_lead left with no projects is demoted.
func RemoveProjectMemberV2(c *gin.Context) {
	tc, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	id, ok := projectIDParam(c)
	if !ok {
		return
	}
	userID, _ := strconv.Atoi(c.Query("user_id"))
	if userID <= 0 {
		var req struct {
			UserId int `json:"user_id"`
		}
		if c.Request.ContentLength > 0 {
			_ = c.ShouldBindJSON(&req)
		}
		userID = req.UserId
	}
	if userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "user_id is required"})
		return
	}
	if _, err := repo.GetProjectByID(tc.TenantID, id); err != nil {
		respondMemberRepoErr(c, err, "remove project member")
		return
	}
	if _, err := repo.RemoveProjectMemberRevoking(tc.TenantID, id, userID); err != nil {
		respondMemberRepoErr(c, err, "remove project member")
		return
	}
	auditMember(c, tc, governance.ActionProjectMemberRemoved, id, userID)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// validateInviteGrant checks an invite's member_role / project_id against the
// tenant and replies on failure. role "" with a project means dept_lead.
func validateInviteGrant(c *gin.Context, tenantID, role string, projectID int64) bool {
	if role == "" && projectID == 0 {
		return true
	}
	if tenantID == "" || tenantID == "default" {
		respondMemberRepoErr(c, repo.ErrTenantRoleInDefault, "issue invite")
		return false
	}
	if !repo.ValidTenantRole(role) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "member_role must be admin or dept_lead"})
		return false
	}
	if projectID < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "project_id must not be negative"})
		return false
	}
	if projectID > 0 {
		if _, err := repo.GetProjectByID(tenantID, int(projectID)); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "project not found in this tenant"})
			return false
		}
	}
	return true
}

type inviteIssueRequest struct {
	TTLHours   int    `json:"ttl_hours"`
	MemberRole string `json:"member_role"`
	ProjectId  int64  `json:"project_id"`
}

// issueInvite validates the grant, mints the code and audits it. Shared by the
// root route and the tenant-admin route.
func issueInvite(c *gin.Context, tenantID string, actorID int, req inviteIssueRequest) {
	if !validateInviteGrant(c, tenantID, req.MemberRole, req.ProjectId) {
		return
	}
	if req.TTLHours < 0 || req.TTLHours > maxInviteTTLHours {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "ttl_hours must be between 1 and " + strconv.Itoa(maxInviteTTLHours) + " (default " + strconv.Itoa(defaultInviteTTLHours) + ")",
			"error_code": inviteErrCodeTTLInvalid,
		})
		return
	}
	ttlHours := req.TTLHours
	if ttlHours == 0 {
		ttlHours = defaultInviteTTLHours
	}
	ttl := time.Duration(ttlHours) * time.Hour
	invite, err := repo.CreateTenantInviteWithGrant(tenantID, actorID, ttl, req.MemberRole, req.ProjectId)
	if err != nil {
		common.SysError("issueInvite: create failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create invite"})
		return
	}
	// Code PREFIX only: the full code is a live onboarding credential.
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantInviteIssued, governance.ResourceTenant, 0,
		tenantID+":"+strconv.Itoa(invite.Id)+":"+invite.Code[:8]+"..."+
			":role="+req.MemberRole+":project="+strconv.FormatInt(req.ProjectId, 10)))
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": invite})
}

// IssueMyTenantInviteV2 — POST /api/v2/:tenant_slug/invites (tenant admin).
// Body: {ttl_hours, member_role, project_id}. The redeemer lands in the
// caller's own tenant with the stated role / project membership.
func IssueMyTenantInviteV2(c *gin.Context) {
	tc, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	var req inviteIssueRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request: " + err.Error()})
			return
		}
	}
	issueInvite(c, tc.TenantID, tc.UserID, req)
}
