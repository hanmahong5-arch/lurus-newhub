package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Root-admin handlers for tenant invite codes (N2). Routes registered under
// /api/v2/admin/tenants/:id/invites* in api-v2-router.go, same RootJWTAuth
// gate as the sibling tenant/credit-pool admin groups in tenant.go /
// tenant_credit_pool.go — a tenant invite is a platform-wide onboarding
// credential, not a tenant-self-service resource, so there is no
// tenant-scoped counterpart for the root-issued codes. Tenant admins mint their
// own grant-bearing invites through IssueMyTenantInviteV2
// (POST /api/v2/:tenant_slug/invites).

// IssueTenantInvite mints a one-time onboarding code for tenantID.
// Route: POST /api/v2/admin/tenants/:id/invites
// Body:  { ttl_hours int }  — omitted means 72h; at most 720 (30 days).
//
// Returns 201 with the invite (including its Code — this is the one
// response the operator reads the code from; it is never listed back out
// in plaintext elsewhere).
func IssueTenantInvite(c *gin.Context) {
	tenantID := c.Param("id")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tenant id required"})
		return
	}
	if _, err := repo.GetTenantByID(tenantID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Tenant not found"})
		return
	}

	var req inviteIssueRequest
	// An empty body is valid (default 72h expiry) - only reject a malformed one.
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request: " + err.Error()})
			return
		}
	}
	issueInvite(c, tenantID, c.GetInt("id"), req)
}

// tenantInviteView is the admin list projection of a TenantInvite. It
// deliberately drops Code down to an 8-char CodePrefix — the list projection
// drops the code; IssueTenantInvite's 201 is the only producer that returns
// the full code today (TestListTenantInvites_NeverReturnsFullCode).
type tenantInviteView struct {
	Id                  int        `json:"id"`
	CodePrefix          string     `json:"code_prefix"`
	Status              int        `json:"status"`
	ExpiredTime         int64      `json:"expired_time"`
	ConsumedByAccountId *int64     `json:"consumed_by_account_id"`
	ConsumedAt          *time.Time `json:"consumed_at"`
	CreatedByUserId     int        `json:"created_by_user_id"`
	CreatedAt           time.Time  `json:"created_at"`
	// Grant and lifecycle fields (migration 046 / 048). ExpiresAt mirrors
	// ExpiredTime (0 = never); Used/Revoked/Expired are derived for the console.
	MemberRole string `json:"member_role"`
	ProjectId  int64  `json:"project_id"`
	ExpiresAt  int64  `json:"expires_at"`
	RevokedAt  int64  `json:"revoked_at"`
	Used       bool   `json:"used"`
	Revoked    bool   `json:"revoked"`
	Expired    bool   `json:"expired"`
}

func toTenantInviteView(inv repo.TenantInvite) tenantInviteView {
	prefix := inv.Code
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return tenantInviteView{
		Id:                  inv.Id,
		CodePrefix:          prefix,
		Status:              inv.Status,
		ExpiredTime:         inv.ExpiredTime,
		ConsumedByAccountId: inv.ConsumedByAccountId,
		ConsumedAt:          inv.ConsumedAt,
		CreatedByUserId:     inv.CreatedByUserId,
		CreatedAt:           inv.CreatedAt,
		MemberRole:          inv.MemberRole,
		ProjectId:           inv.ProjectId,
		ExpiresAt:           inv.ExpiredTime,
		RevokedAt:           inv.RevokedAt,
		Used:                inv.Status == repo.TenantInviteStatusConsumed,
		Revoked:             inv.Status == repo.TenantInviteStatusRevoked || inv.RevokedAt != 0,
		Expired:             inv.Status == repo.TenantInviteStatusPending && inv.ExpiredTime != 0 && inv.ExpiredTime < common.GetTimestamp(),
	}
}

// ListTenantInvites lists tenantID's invite codes, newest first
// (repo.ListTenantInvites orders created_at DESC;
// TestListTenantInvites_NewestFirst pins it), projected through
// tenantInviteView so the list response carries only the 8-char prefix
// (TestListTenantInvites_NeverReturnsFullCode).
// Route: GET /api/v2/admin/tenants/:id/invites
func ListTenantInvites(c *gin.Context) {
	tenantID := c.Param("id")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tenant id required"})
		return
	}
	if _, err := repo.GetTenantByID(tenantID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Tenant not found"})
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
	offset := (page - 1) * pageSize

	invites, total, err := repo.ListTenantInvites(tenantID, pageSize, offset)
	if err != nil {
		common.SysError("ListTenantInvites: list failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list invites"})
		return
	}

	views := make([]tenantInviteView, 0, len(invites))
	for _, inv := range invites {
		views = append(views, toTenantInviteView(inv))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"invites":   views,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// RevokeTenantInvite kills a pending code early.
// Route: DELETE /api/v2/admin/tenants/:id/invites/:invite_id
//
// 200 on success; 404 if the id doesn't resolve to a PENDING invite owned by
// this tenant (already consumed/revoked codes and codes belonging to a
// different tenant id are indistinguishable from "not found" — same
// not-found shape IDOR-safe handlers elsewhere in this package use).
func RevokeTenantInvite(c *gin.Context) {
	tenantID := c.Param("id")
	inviteID, err := strconv.Atoi(c.Param("invite_id"))
	if err != nil || inviteID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid invite id"})
		return
	}

	if err := repo.RevokeTenantInvite(inviteID, tenantID); err != nil {
		if errors.Is(err, repo.ErrInviteNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Invite not found"})
			return
		}
		common.SysError("RevokeTenantInvite: revoke failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to revoke invite"})
		return
	}

	actorID := c.GetInt("id")
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantInviteRevoked, governance.ResourceTenant, 0, tenantID+":"+strconv.Itoa(inviteID)))

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Invite revoked"})
}
