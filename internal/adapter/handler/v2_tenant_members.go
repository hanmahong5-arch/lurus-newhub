package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// v2_tenant_members.go — tenant onboarding loop (migration 048): who is the
// tenant's admin / payer, the tenant-side invite list / revoke / redeem.

const (
	errCodeLastTenantAdmin   = "LAST_TENANT_ADMIN"
	errCodeInvalidTenantRole = "TENANT_ROLE_INVALID"
	errCodePayerNotSet       = "payer_not_set"

	inviteErrNotFound  = "INVITE_NOT_FOUND"
	inviteErrExpired   = "INVITE_EXPIRED"
	inviteErrRevoked   = "INVITE_REVOKED"
	inviteErrConsumed  = "INVITE_ALREADY_CONSUMED"
	inviteErrCodeBlank = "INVITE_CODE_REQUIRED"
)

type memberRoleRequest struct {
	TenantRole *string `json:"tenant_role"`
	Payer      *bool   `json:"payer"`
}

func bindMemberRole(c *gin.Context) (*memberRoleRequest, int, bool) {
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid user id"})
		return nil, 0, false
	}
	var req memberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TenantRole == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tenant_role is required (admin, dept_lead or empty)"})
		return nil, 0, false
	}
	return &req, userID, true
}

func respondRoleErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrLastTenantAdmin):
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error(), "error_code": errCodeLastTenantAdmin})
	case errors.Is(err, repo.ErrInvalidTenantRole):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tenant_role must be admin, dept_lead or empty", "error_code": errCodeInvalidTenantRole})
	default:
		respondMemberRepoErr(c, err, "set member role")
	}
}

func auditRoleSet(c *gin.Context, actor string, actorID int, tenantID string, userID int, role string, payer *bool) {
	detail := map[string]any{"tenant_id": tenantID, "user_id": userID, "tenant_role": role}
	if payer != nil {
		detail["payer"] = *payer
	}
	b, _ := json.Marshal(detail)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, actor, actorID,
		governance.ActionTenantMemberRoleSet, governance.ResourceUser, userID, string(b)))
}

// SetTenantMemberRoleAdmin — PUT /api/v2/admin/tenants/:id/members/:user_id/role
// Body: {tenant_role, payer?}. Root only (route group). Bootstraps a tenant's
// first admin / payer; the user must belong to the tenant (404 otherwise).
func SetTenantMemberRoleAdmin(c *gin.Context) {
	tenantID := c.Param("id")
	req, userID, ok := bindMemberRole(c)
	if !ok {
		return
	}
	if _, err := repo.GetTenantByID(tenantID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Tenant not found"})
		return
	}
	if err := repo.SetTenantMemberRole(tenantID, userID, *req.TenantRole, req.Payer, false); err != nil {
		respondRoleErr(c, err)
		return
	}
	auditRoleSet(c, governance.ActorAdmin, c.GetInt("id"), tenantID, userID, *req.TenantRole, req.Payer)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SetTenantMemberRoleV2 — PUT /api/v2/:tenant_slug/members/:user_id/role
// Body: {tenant_role}. Tenant admin only, own tenant only; refuses to leave the
// tenant without an admin. The payer flag is root-only and ignored here.
func SetTenantMemberRoleV2(c *gin.Context) {
	tc, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	req, userID, ok := bindMemberRole(c)
	if !ok {
		return
	}
	if err := repo.SetTenantMemberRole(tc.TenantID, userID, *req.TenantRole, nil, true); err != nil {
		respondRoleErr(c, err)
		return
	}
	auditRoleSet(c, governance.ActorUser, tc.UserID, tc.TenantID, userID, *req.TenantRole, nil)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListMyTenantInvitesV2 — GET /api/v2/:tenant_slug/invites (tenant admin).
func ListMyTenantInvitesV2(c *gin.Context) {
	tc, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	invites, total, err := repo.ListTenantInvites(tc.TenantID, pageSize, (page-1)*pageSize)
	if err != nil {
		common.SysError("ListMyTenantInvitesV2: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list invites"})
		return
	}
	views := make([]tenantInviteView, 0, len(invites))
	for _, inv := range invites {
		views = append(views, toTenantInviteView(inv))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"invites": views, "total": total, "page": page, "page_size": pageSize,
	}})
}

// RevokeMyTenantInviteV2 — DELETE /api/v2/:tenant_slug/invites/:id (tenant
// admin). Another tenant's invite id and a spent/revoked one are both 404.
func RevokeMyTenantInviteV2(c *gin.Context) {
	tc, ok := projectAdminCtx(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid invite id"})
		return
	}
	if err := repo.RevokeTenantInvite(id, tc.TenantID); err != nil {
		if errors.Is(err, repo.ErrInviteNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Invite not found", "error_code": inviteErrNotFound})
			return
		}
		common.SysError("RevokeMyTenantInviteV2: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to revoke invite"})
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tc.UserID,
		governance.ActionTenantInviteRevoked, governance.ResourceTenant, 0, tc.TenantID+":"+strconv.Itoa(id)))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Invite revoked"})
}

// respondInviteRedeemErr maps redemption failures onto distinct error codes.
func respondInviteRedeemErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrInviteNotFound), errors.Is(err, repo.ErrInviteWrongTenant):
		// A foreign tenant's code is indistinguishable from an unknown one.
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Invite not found", "error_code": inviteErrNotFound})
	case errors.Is(err, repo.ErrInviteExpired):
		c.JSON(http.StatusGone, gin.H{"success": false, "message": err.Error(), "error_code": inviteErrExpired})
	case errors.Is(err, repo.ErrInviteRevoked):
		c.JSON(http.StatusGone, gin.H{"success": false, "message": err.Error(), "error_code": inviteErrRevoked})
	case errors.Is(err, repo.ErrInviteAlreadyConsumed):
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error(), "error_code": inviteErrConsumed})
	default:
		respondMemberRepoErr(c, err, "redeem invite")
	}
}

// RedeemMyTenantInviteV2 — POST /api/v2/:tenant_slug/invites/redeem {code}.
// For an already logged-in user: applies the invite's role / project grant to
// them. Only a code of the caller's own tenant redeems.
func RedeemMyTenantInviteV2(c *gin.Context) {
	tc, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "code is required", "error_code": inviteErrCodeBlank})
		return
	}
	grant, err := repo.RedeemInviteForExistingUser(req.Code, tc.UserID)
	if err != nil {
		respondInviteRedeemErr(c, err)
		return
	}
	detail, _ := json.Marshal(map[string]any{"tenant_id": tc.TenantID, "user_id": tc.UserID, "existing_user": true})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tc.UserID,
		governance.ActionTenantInviteConsumed, governance.ResourceTenant, 0, string(detail)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"tenant_id": tc.TenantID, "member_role": grant.MemberRole, "project_id": grant.ProjectID,
	}})
}

// tenantRoleView is the (tenant_role, is_payer) pair the console reads to decide
// which menus to show. The role is reported only where it is honoured (never in
// the shared "default" tenant), matching tenantRoleOf.
func tenantRoleView(user *repo.User) (role string, isPayer bool) {
	if user == nil || user.TenantId == "" || user.TenantId == "default" {
		return "", false
	}
	return user.TenantRole, repo.IsTenantPayer(user.TenantId, user.Id)
}
