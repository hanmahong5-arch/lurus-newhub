package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// V2 Platform Admin — User Management
// Registered under adminRoute (RootJWTAuth, session-fallback). Platform-scoped:
// these list/update/delete users across all tenants.
// ============================================================================

// adminUserView is the field-whitelisted projection returned by the admin user
// endpoints. It deliberately excludes the access_token (a bearer secret),
// setting blob, remark, tenant linkage and soft-delete columns — only the
// curated, safe-to-surface fields appear. Mirrors tokenView/channelView/logView.
// (Note: the User entity has no created-time column — auth/timestamps live in
// the OIDC provider — so none is projected here rather than fabricating one.)
type adminUserView struct {
	Id           int    `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	Role         int    `json:"role"`
	Status       int    `json:"status"`
	Group        string `json:"group"`
	Quota        int    `json:"quota"`
	UsedQuota    int    `json:"used_quota"`
	RequestCount int    `json:"request_count"`
}

func toAdminUserViews(users []*repo.User) []adminUserView {
	items := make([]adminUserView, 0, len(users))
	for _, u := range users {
		items = append(items, adminUserView{
			Id:           u.Id,
			Username:     u.Username,
			DisplayName:  u.DisplayName,
			Email:        u.Email,
			Role:         u.Role,
			Status:       u.Status,
			Group:        u.Group,
			Quota:        u.Quota,
			UsedQuota:    u.UsedQuota,
			RequestCount: u.RequestCount,
		})
	}
	return items
}

// requireRoot is the shared platform-admin gate. Returns the caller's role and
// whether the request may proceed; on rejection it has already written the 403.
func requireRoot(c *gin.Context) (int, bool) {
	role := c.GetInt("role")
	if role < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Platform admin role required",
		})
		return role, false
	}
	return role, true
}

// ListAdminUsersV2 lists users with keyword/group/status filters + pagination.
// Route: GET /api/v2/admin/users
func ListAdminUsersV2(c *gin.Context) {
	if _, ok := requireRoot(c); !ok {
		return
	}

	keyword := c.Query("keyword")
	group := c.Query("group")
	status := 0
	if s := c.Query("status"); s != "" {
		status, _ = strconv.Atoi(s)
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

	users, total, err := repo.AdminListUsers(keyword, group, status, offset, pageSize)
	if err != nil {
		common.SysError("Failed to list users: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve users",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"users":     toAdminUserViews(users),
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// UpdateAdminUserV2 updates a user's role / status / quota / group only.
// Route: PUT /api/v2/admin/users/:id
func UpdateAdminUserV2(c *gin.Context) {
	myRole, ok := requireRoot(c)
	if !ok {
		return
	}

	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid user ID"})
		return
	}

	origin, err := repo.GetUserById(userID, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "User not found"})
		return
	}

	// nil = leave unchanged. Only these four fields are mutable here.
	var req struct {
		Role   *int    `json:"role"`
		Status *int    `json:"status"`
		Quota  *int    `json:"quota"`
		Group  *string `json:"group"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request parameters", "error": err.Error()})
		return
	}

	// Cannot operate on a same-or-higher-privilege user (root is exempt).
	if err := app.CheckPermission(myRole, origin.Role); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
		return
	}

	newRole := origin.Role
	if req.Role != nil {
		if !common.IsValidateRole(*req.Role) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid role"})
			return
		}
		// Cannot promote a user to a role at or above the caller's own (root exempt).
		if err := app.CheckRolePromotion(myRole, *req.Role); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
			return
		}
		// Self-demotion locks the actor out of admin (root is NOT exempt).
		// Only checked when a role change is actually requested (req.Role != nil).
		if err := app.CheckSelfDemotion(c.GetInt("id"), userID, myRole, *req.Role); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
			return
		}
		newRole = *req.Role
	}

	newStatus := origin.Status
	if req.Status != nil {
		if *req.Status != common.UserStatusEnabled && *req.Status != common.UserStatusDisabled {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid status"})
			return
		}
		newStatus = *req.Status
	}

	newQuota := origin.Quota
	if req.Quota != nil {
		if *req.Quota < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Quota value cannot be negative"})
			return
		}
		newQuota = *req.Quota
	}

	newGroup := origin.Group
	if req.Group != nil && *req.Group != "" {
		newGroup = *req.Group
	}

	updated, err := repo.AdminUpdateUser(userID, newRole, newStatus, newQuota, newGroup)
	if err != nil {
		common.SysError("Failed to update user: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to update user"})
		return
	}

	// Audit: role changes and quota adjustments are separately attributable.
	if origin.Role != updated.Role {
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
			governance.ActionUserRoleChanged, governance.ResourceUser, userID,
			fmt.Sprintf(`{"old_role":%d,"new_role":%d}`, origin.Role, updated.Role)))
	}
	if origin.Quota != updated.Quota {
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
			governance.ActionUserQuotaAdjusted, governance.ResourceUser, userID,
			fmt.Sprintf(`{"old_quota":%d,"new_quota":%d}`, origin.Quota, updated.Quota)))
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionUserUpdated, governance.ResourceUser, userID,
		fmt.Sprintf(`{"username":%q}`, updated.Username)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User updated successfully",
		"data":    toAdminUserViews([]*repo.User{updated})[0],
	})
}

// DeleteAdminUserV2 deletes a user. Guards: cannot delete self, cannot remove
// the last root account.
// Route: DELETE /api/v2/admin/users/:id
func DeleteAdminUserV2(c *gin.Context) {
	if _, ok := requireRoot(c); !ok {
		return
	}

	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid user ID"})
		return
	}

	if userID == c.GetInt("id") {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "You cannot delete your own account"})
		return
	}

	target, err := repo.GetUserById(userID, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "User not found"})
		return
	}

	// Refuse to remove the last root — would lock everyone out of admin.
	if target.Role == common.RoleRootUser {
		rootCount, cErr := repo.CountUsersByRole(common.RoleRootUser)
		if cErr != nil {
			common.SysError("Failed to count root users: " + cErr.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to delete user"})
			return
		}
		if rootCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Cannot delete the last root user"})
			return
		}
	}

	if err := repo.AdminDeleteUser(userID); err != nil {
		common.SysError("Failed to delete user: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to delete user"})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionUserDeleted, governance.ResourceUser, userID,
		fmt.Sprintf(`{"username":%q}`, target.Username)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User deleted successfully",
	})
}
