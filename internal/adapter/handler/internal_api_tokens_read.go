package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// InternalGetUserTokens returns paginated tokens for a user.
func InternalGetUserTokens(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	if _, err = repo.GetUserById(userId, false); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize
	tokens, err := repo.GetAllUserTokens(userId, offset, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get tokens: " + err.Error(),
		})
		return
	}

	total, _ := repo.CountUserTokens(userId)

	for _, t := range tokens {
		t.Clean()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tokens":    tokens,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// InternalCreateToken creates a new API token for a user.
func InternalCreateToken(c *gin.Context) {
	var req struct {
		UserId         int    `json:"user_id" binding:"required"`
		Name           string `json:"name" binding:"required"`
		UnlimitedQuota bool   `json:"unlimited_quota"`
		RemainQuota    int    `json:"remain_quota"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	user, err := repo.GetUserById(req.UserId, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	if user.Status == common.UserStatusDisabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "User is disabled",
			"error_code": "USER_DISABLED",
		})
		return
	}

	if c.GetHeader("X-Idempotency-Key") != "" {
		var existing repo.Token
		if err := repo.DB.Where("user_id = ? AND name = ?", req.UserId, req.Name).First(&existing).Error; err == nil && existing.Id > 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"id":           existing.Id,
					"name":         existing.Name,
					"is_duplicate": true,
				},
			})
			return
		}
	}

	tokenKey, err := app.GenerateTokenKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate token key: " + err.Error(),
		})
		return
	}

	token := &repo.Token{
		UserId:         req.UserId,
		TenantId:       user.TenantId,
		Name:           req.Name,
		Key:            tokenKey,
		UnlimitedQuota: req.UnlimitedQuota,
		RemainQuota:    req.RemainQuota,
		CreatedTime:    time.Now().Unix(),
		AccessedTime:   time.Now().Unix(),
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		Group:          user.Group,
	}

	if err = token.Insert(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create token: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog("Internal API created token for user " + strconv.Itoa(req.UserId) + " via key: " + keyName)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"id":      token.Id,
			"key":     tokenKey,
			"name":    token.Name,
			"warning": "Please save this key - it will not be shown again.",
		},
	})
}

// InternalGetToken returns a single token by ID with its raw key redacted.
func InternalGetToken(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid token ID",
		})
		return
	}

	token, err := repo.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Token not found",
			"error_code": "TOKEN_NOT_FOUND",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":                   token.Id,
			"user_id":              token.UserId,
			"tenant_id":            token.TenantId,
			"name":                 token.Name,
			"status":               token.Status,
			"created_time":         token.CreatedTime,
			"accessed_time":        token.AccessedTime,
			"expired_time":         token.ExpiredTime,
			"remain_quota":         token.RemainQuota,
			"used_quota":           token.UsedQuota,
			"unlimited_quota":      token.UnlimitedQuota,
			"model_limits_enabled": token.ModelLimitsEnabled,
			"model_limits":         token.GetModelLimits(),
			"allow_ips":            token.AllowIps,
			"group":                token.Group,
			"cross_group_retry":    token.CrossGroupRetry,
		},
	})
}

// InternalGetTokenUsage returns usage statistics for a token.
func InternalGetTokenUsage(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid token ID",
		})
		return
	}

	token, err := repo.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Token not found",
			"error_code": "TOKEN_NOT_FOUND",
		})
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"token_id":        token.Id,
			"name":            token.Name,
			"status":          token.Status,
			"total_granted":   token.RemainQuota + token.UsedQuota,
			"total_used":      token.UsedQuota,
			"total_available": token.RemainQuota,
			"unlimited_quota": token.UnlimitedQuota,
			"expires_at":      expiredAt,
			"last_accessed":   token.AccessedTime,
		},
	})
}
