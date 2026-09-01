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
// tenant-scoped counterpart.

// IssueTenantInvite mints a one-time onboarding code for tenantID.
// Route: POST /api/v2/admin/tenants/:id/invites
// Body:  { ttl_hours int }  — omitted or <= 0 means the code never expires.
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

	var req struct {
		TTLHours int `json:"ttl_hours"`
	}
	// An empty body is valid (no-expiry invite) — only reject a malformed one.
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request: " + err.Error()})
			return
		}
	}
	var ttl time.Duration
	if req.TTLHours > 0 {
		ttl = time.Duration(req.TTLHours) * time.Hour
	}

	actorID := c.GetInt("id")
	invite, err := repo.CreateTenantInvite(tenantID, actorID, ttl)
	if err != nil {
		common.SysError("IssueTenantInvite: create failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create invite"})
		return
	}

	// Audit detail carries the invite id and a code PREFIX only — the full
	// code is a live onboarding credential and the audit log is readable by
	// every audit-scope holder, so recording it verbatim would leak pending
	// codes to anyone with audit export.
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantInviteIssued, governance.ResourceTenant, 0,
		tenantID+":"+strconv.Itoa(invite.Id)+":"+invite.Code[:8]+"…"))

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": invite})
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
