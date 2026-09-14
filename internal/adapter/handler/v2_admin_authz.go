package handler

// v2_admin_authz.go — L4 (auth-security-17/18, console-ux-36): delegated
// admin permission grant management. All four routes mount under
// adminRoute (RootJWTAuth + AuditWriteGuard, router/api-v2-router.go) —
// only root manages WHO holds a grant; middleware.RootOrGranted (the thing
// a grant unlocks) is a separate, narrower gate on a different route group
// (auditRoute).

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/authz"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// grantedBySubDetail returns a JSON object-fragment (leading comma, no
// trailing comma) naming the acting admin's OIDC subject when a write
// handler below ran on RootJWTAuth's Bearer-JWT branch: that branch never
// populates the "id" context key (cycle-7 §8 L2 finding; root_or_granted.go
// documents the same gap for RootOrGranted's own JWT branch), so actorID —
// and therefore GrantedBy / the audit ActorID — is recorded as 0 for a
// JWT-path write. This is a RULING (cycle-8 §8 L4 B-F11), not a bug fix:
// the write is still allowed with GrantedBy=0; the JWT subject is recorded
// alongside it purely as an audit-trail convenience. Empty when actorID is
// nonzero (the common, session-path case) or no admin_sub is set (OIDC-off
// JWT fallback, or a non-JWT caller).
func grantedBySubDetail(c *gin.Context, actorID int) string {
	if actorID != 0 {
		return ""
	}
	sub := c.GetString("admin_sub")
	if sub == "" {
		return ""
	}
	return fmt.Sprintf(`,"granted_by_sub":%q`, sub)
}

// ListGrantsV2 lists every delegated permission grant, active and revoked.
// Route: GET /api/v2/admin/authz/grants
func ListGrantsV2(c *gin.Context) {
	grants, err := repo.ListPermissionGrants()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list grants"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": grants})
}

type createGrantRequest struct {
	UserID   int     `json:"user_id"`
	Resource string  `json:"resource"`
	Action   string  `json:"action"`
	TenantID *string `json:"tenant_id"`
}

// CreateGrantV2 mints a delegated permission grant.
// Route: POST /api/v2/admin/authz/grants
// Body:  {"user_id":int,"resource":"audit","action":"read","tenant_id":null}
//
// Grants are GLOBAL this cycle (O5, cycle-8 plan §8 L4 amendment): a
// non-null tenant_id is rejected as GRANT_INVALID, same as any
// (resource, action) outside the static catalogue (internal/app/authz), or
// a user_id that does not resolve to an existing user holding at least
// common.RoleAdminUser (cycle-8 L4 repair round, B-F4 — a mistyped id used
// to mint a silently inert "active" row).
//
// GrantedBy is c.GetInt("id"), which is 0 for a Bearer-JWT root write
// (RootJWTAuth's JWT branch never sets "id" — see grantedBySubDetail); the
// write is still allowed (RULING B-F11), and the audit Details additionally
// carry granted_by_sub for that path.
func CreateGrantV2(c *gin.Context) {
	var req createGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request: " + err.Error()})
		return
	}
	if req.UserID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "user_id is required", "error_code": "GRANT_INVALID"})
		return
	}
	if req.TenantID != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tenant_id must be null: grants are global this cycle", "error_code": "GRANT_INVALID"})
		return
	}
	if !authz.IsValid(req.Resource, req.Action) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "unknown resource/action", "error_code": "GRANT_INVALID"})
		return
	}
	grantee, err := repo.GetUserById(req.UserID)
	if err != nil || grantee.Role < common.RoleAdminUser {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "user not found or not an admin", "error_code": "GRANT_INVALID"})
		return
	}

	actorID := c.GetInt("id")
	grant, createErr := repo.CreatePermissionGrant(req.UserID, req.Resource, req.Action, actorID)
	if createErr != nil {
		if errors.Is(createErr, repo.ErrGrantExists) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "an active grant for this user/resource/action already exists", "error_code": "GRANT_EXISTS"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create grant"})
		return
	}

	detail := fmt.Sprintf(`{"grantee_user_id":%d,"resource":%q,"action":%q,"tenant_id":null%s}`,
		req.UserID, req.Resource, req.Action, grantedBySubDetail(c, actorID))
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionPermissionGranted, governance.ResourceAuthz, grant.Id, detail))

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"id": grant.Id}})
}

// RevokeGrantV2 withdraws a permission grant.
// Route: DELETE /api/v2/admin/authz/grants/:id
//
// 200 on success (sets revoked_at); 404 if the id doesn't resolve to an
// ACTIVE grant (absent or already-revoked are indistinguishable — same
// not-found shape other admin revoke handlers in this package use, e.g.
// RevokeTenantInvite).
func RevokeGrantV2(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid grant id"})
		return
	}

	// Read the grant BEFORE revoking so the audit detail can still name who
	// held it — RevokePermissionGrant only flips revoked_at, it doesn't
	// return the row.
	grants, listErr := repo.ListPermissionGrants()
	var target *repo.AdminPermissionGrant
	if listErr == nil {
		for i := range grants {
			if grants[i].Id == id {
				target = &grants[i]
				break
			}
		}
	}

	if err := repo.RevokePermissionGrant(id); err != nil {
		if errors.Is(err, repo.ErrGrantNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Grant not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to revoke grant"})
		return
	}

	actorID := c.GetInt("id")
	detail := fmt.Sprintf(`{"grant_id":%d%s}`, id, grantedBySubDetail(c, actorID))
	if target != nil {
		detail = fmt.Sprintf(`{"grant_id":%d,"grantee_user_id":%d,"resource":%q,"action":%q,"tenant_id":null%s}`,
			id, target.UserId, target.Resource, target.Action, grantedBySubDetail(c, actorID))
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionPermissionRevoked, governance.ResourceAuthz, id, detail))

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Grant revoked"})
}

// GetAuthzCatalogV2 returns the static resource/action catalogue plus the
// two fixed roles — the read-only registry a grant's (resource, action)
// must come from.
// Route: GET /api/v2/admin/authz/catalog
func GetAuthzCatalogV2(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"resources": authz.Catalog(),
			"roles":     authz.Roles(),
		},
	})
}
