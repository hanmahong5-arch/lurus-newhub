package handler

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

var usernameRegexp = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// InternalLogin rejects deprecated password-based login in favor of OIDC.
func InternalLogin(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"success":    false,
		"message":    "Password-based login is no longer supported. Use OIDC.",
		"error_code": "DEPRECATED",
	})
}

// InternalCreateUser creates a user row inside the default tenant via the internal API.
func InternalCreateUser(c *gin.Context) {
	var req struct {
		Username    string `json:"username" binding:"required"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Group       string `json:"group"`
		Quota       int    `json:"quota"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	username := strings.TrimSpace(req.Username)
	if len(username) < 3 || len(username) > 20 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Username must be 3-20 characters",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}
	if !usernameRegexp.MatchString(username) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Username contains invalid characters",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}

	if req.Email != "" && !strings.Contains(req.Email, "@") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Invalid email format",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}

	defaultTenantID := repo.ResolveDefaultTenantID()
	db := repo.GetTenantDBWithID(defaultTenantID)

	if c.GetHeader("X-Idempotency-Key") != "" {
		existing := &repo.User{Username: username}
		if err := db.Where("username = ?", username).First(existing).Error; err == nil && existing.Id > 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"id":           existing.Id,
					"username":     existing.Username,
					"display_name": existing.DisplayName,
					"email":        existing.Email,
					"group":        existing.Group,
					"quota":        existing.Quota,
					"is_duplicate": true,
				},
			})
			return
		}
	}

	var existingCount int64
	db.Model(&repo.User{}).Where("username = ?", username).Count(&existingCount)
	if existingCount > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"message":    "Username already exists",
			"error_code": "USER_EXISTS",
		})
		return
	}

	if req.Email != "" {
		var emailCount int64
		db.Model(&repo.User{}).Where("email = ?", req.Email).Count(&emailCount)
		if emailCount > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"success":    false,
				"message":    "Email already exists",
				"error_code": "USER_EXISTS",
			})
			return
		}
	}

	group := req.Group
	if group == "" {
		group = "default"
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = username
	}

	user := &repo.User{
		Username:    username,
		TenantId:    defaultTenantID,
		Email:       req.Email,
		DisplayName: displayName,
		Group:       group,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       req.Quota,
	}

	if err := db.Create(user).Error; err != nil {
		if isUsernameUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{
				"success":    false,
				"message":    "Username already exists",
				"error_code": "USER_EXISTS",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create user: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"group":        user.Group,
			"quota":        user.Quota,
		},
	})
}

func isUsernameUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "duplicate key value") ||
		strings.Contains(s, "UNIQUE constraint failed") ||
		strings.Contains(s, "23505")
}

// InternalDeleteUser deletes a non-root user by ID.
func InternalDeleteUser(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	user, err := repo.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	if user.Role >= common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "Cannot delete admin/root user",
			"error_code": "FORBIDDEN",
		})
		return
	}

	if err = repo.DeleteUserById(userId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete user: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog("Internal API deleted user " + strconv.Itoa(userId) + " via key: " + keyName)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User deleted successfully",
	})
}

// InternalGetUserByZitadelSub resolves a user and identity mapping by OIDC subject ID.
func InternalGetUserByZitadelSub(c *gin.Context) {
	sub := c.Param("sub")
	if sub == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "OIDC subject ID is required",
		})
		return
	}

	tenantId := c.DefaultQuery("tenant_id", "default")

	user, mapping, err := repo.GetUserByIDPSubject(sub, tenantId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found for OIDC subject: " + sub,
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"role":         user.Role,
			"status":       user.Status,
			"group":        user.Group,
			"tenant_id":    user.TenantId,
			"mapping": gin.H{
				"id":                 mapping.Id,
				"idp_subject":        mapping.IDPSubject,
				"zitadel_user_id":    mapping.IDPSubject,
				"tenant_id":          mapping.TenantID,
				"preferred_username": mapping.PreferredUsername,
			},
		},
	})
}
