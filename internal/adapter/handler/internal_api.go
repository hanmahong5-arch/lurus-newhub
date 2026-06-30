package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// ===== User APIs =====

// InternalGetUser gets user info by ID
// GET /internal/user/:id
func InternalGetUser(c *gin.Context) {
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
		},
	})
}

// InternalGetUserByEmail gets user by email
// GET /internal/user/by-email/:email
func InternalGetUserByEmail(c *gin.Context) {
	email := c.Param("email")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Email is required",
		})
		return
	}

	user := &repo.User{Email: email}
	if err := user.FillUserByEmail(); err != nil || user.Id == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
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
		},
	})
}

// InternalGetUserByPhone returns 410 Gone — phone-based lookup is no longer supported.
// Phone auth is delegated to the OIDC provider.
// GET /internal/user/by-phone/:phone
func InternalGetUserByPhone(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"success":    false,
		"message":    "Phone-based user lookup is no longer supported",
		"error_code": "DEPRECATED",
	})
}

// InternalUpdateUser updates user information
// PUT /internal/user/:id
func InternalUpdateUser(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	var req struct {
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
		Status      *int    `json:"status"`
		Group       *string `json:"group"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	// Build updates map before DB lookup to fail fast on empty requests
	updates := make(map[string]interface{})
	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}
	if req.Email != nil {
		updates["email"] = *req.Email
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.Group != nil {
		updates["group"] = *req.Group
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "No fields to update",
		})
		return
	}

	// Check user exists
	_, err = repo.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	// Perform update
	err = repo.DB.Model(&repo.User{}).Where("id = ?", userId).Updates(updates).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update user: " + err.Error(),
		})
		return
	}

	// Log the operation
	keyName := c.GetString("internal_api_key_name")
	common.SysLog("Internal API updated user " + strconv.Itoa(userId) + " via key: " + keyName)

	// Return updated user info
	updatedUser, _ := repo.GetUserById(userId, false)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User updated successfully",
		"data": gin.H{
			"id":           updatedUser.Id,
			"username":     updatedUser.Username,
			"display_name": updatedUser.DisplayName,
			"email":        updatedUser.Email,
			"role":         updatedUser.Role,
			"status":       updatedUser.Status,
			"group":        updatedUser.Group,
		},
	})
}

// ===== Quota APIs =====

// InternalGetUserQuota gets user's quota information
// GET /internal/quota/user/:id
func InternalGetUserQuota(c *gin.Context) {
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":          user.Id,
			"quota":            user.Quota,
			"used_quota":       user.UsedQuota,
			"daily_quota":      user.DailyQuota,
			"daily_used":       user.DailyUsed,
			"last_daily_reset": user.LastDailyReset,
			"group":            user.Group,
			"base_group":       user.BaseGroup,
			"fallback_group":   user.FallbackGroup,
			// Lute currency overlay (1 LUT = 1 quota unit)
			"lute": gin.H{
				"balance":         user.Quota,
				"balance_display": currency.FormatLutCN(user.Quota),
				"used":            user.UsedQuota,
				"used_display":    currency.FormatLutCN(user.UsedQuota),
				"luc_equivalent":  currency.LutToLucDisplay(user.Quota),
			},
		},
	})
}

// InternalAdjustQuota adjusts user's quota
// POST /internal/quota/adjust
func InternalAdjustQuota(c *gin.Context) {
	var req struct {
		UserId int    `json:"user_id" binding:"required"`
		Amount int    `json:"amount" binding:"required"` // Positive = add, Negative = deduct
		Reason string `json:"reason" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	// Validate user ID is positive
	if req.UserId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	// Check user exists
	user, err := repo.GetUserById(req.UserId, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	// Adjust quota
	if req.Amount > 0 {
		err = repo.IncreaseUserQuota(req.UserId, req.Amount, true)
	} else if req.Amount < 0 {
		err = repo.DecreaseUserQuota(req.UserId, -req.Amount)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to adjust quota: " + err.Error(),
		})
		return
	}

	// Log the operation
	keyName := c.GetString("internal_api_key_name")
	repo.RecordLog(req.UserId, repo.LogTypeSystem,
		"Internal API adjusted quota by "+logger.LogQuota(req.Amount)+" via key: "+keyName+". Reason: "+req.Reason)

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorSystem, 0,
		governance.ActionUserQuotaAdjusted, governance.ResourceUser, user.Id,
		fmt.Sprintf(`{"adjustment":%d,"key_name":%q,"reason":%q}`, req.Amount, keyName, req.Reason)))

	// Get updated quota
	newQuota, _ := repo.GetUserQuota(req.UserId, true)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Quota adjusted successfully",
		"data": gin.H{
			"user_id":      user.Id,
			"old_quota":    user.Quota,
			"adjustment":   req.Amount,
			"new_quota":    newQuota,
		},
	})
}

// ===== Balance APIs =====

// InternalGetUserBalance gets user's balance
// GET /internal/balance/user/:id
func InternalGetUserBalance(c *gin.Context) {
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

	// Convert quota to LUC (1 LUC = QuotaPerUnit quota units)
	balanceLuc := currency.LutToLucDisplay(user.Quota)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":     user.Id,
			"balance":     user.Quota,                          // Balance in LUT (= quota units)
			"balance_luc": balanceLuc,                          // Balance in LUC
			"balance_rmb": balanceLuc,                          // LUC ~ CNY 1:1 (backward compat)
			"used_quota":  user.UsedQuota,
			"lute": gin.H{
				"balance":         user.Quota,
				"balance_display": currency.FormatLutCN(user.Quota),
				"luc_equivalent":  balanceLuc,
			},
		},
	})
}

// InternalTopupBalance tops up user's balance.
// Idempotent when order_id is provided — duplicate requests return 200 without double-charging.
// POST /internal/balance/topup
func InternalTopupBalance(c *gin.Context) {
	var req struct {
		UserId    int     `json:"user_id" binding:"required"`
		AmountRmb float64 `json:"amount_rmb" binding:"required"`
		OrderId   string  `json:"order_id"`
		Reason    string  `json:"reason" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	if req.UserId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	if req.AmountRmb <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Amount must be positive",
		})
		return
	}

	// Idempotency check: if order_id is provided, verify it hasn't been processed already
	if req.OrderId != "" {
		var count int64
		orderTag := "Order ID: " + req.OrderId
		repo.LOG_DB.Model(&repo.Log{}).
			Where("user_id = ? AND type = ? AND content LIKE ?", req.UserId, repo.LogTypeTopup, "%"+orderTag+"%").
			Count(&count)
		if count > 0 {
			currentQuota, _ := repo.GetUserQuota(req.UserId, true)
			c.JSON(http.StatusOK, gin.H{
				"success":    true,
				"message":    "Already processed (idempotent)",
				"idempotent": true,
				"data": gin.H{
					"user_id":     req.UserId,
					"order_id":    req.OrderId,
					"new_balance": currentQuota,
				},
			})
			return
		}
	}

	// Check user exists
	user, err := repo.GetUserById(req.UserId, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	// Convert RMB to tokens
	quotaAmount := int(req.AmountRmb * common.QuotaPerUnit)

	// Add quota
	err = repo.IncreaseUserQuota(req.UserId, quotaAmount, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to top up: " + err.Error(),
		})
		return
	}

	// Log the operation (order_id is always appended when present, used for idempotency check)
	keyName := c.GetString("internal_api_key_name")
	logMsg := "Internal API topped up " + logger.LogQuota(quotaAmount) + " via key: " + keyName + ". Reason: " + req.Reason
	if req.OrderId != "" {
		logMsg += ". Order ID: " + req.OrderId
	}
	repo.RecordLog(req.UserId, repo.LogTypeTopup, logMsg)

	// Get updated quota
	newQuota, _ := repo.GetUserQuota(req.UserId, true)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Balance topped up successfully",
		"data": gin.H{
			"user_id":     user.Id,
			"old_balance": user.Quota,
			"amount":      quotaAmount,
			"amount_rmb":  req.AmountRmb,
			"new_balance": newQuota,
		},
	})
}

// ===== API Key Management (Admin) =====

// AdminListApiKeys lists all internal API keys
// GET /api/admin/api-keys
func AdminListApiKeys(c *gin.Context) {
	keys, err := repo.GetAllInternalApiKeys()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get API keys: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    keys,
	})
}

// AdminGetApiKeyScopes returns available scopes
// GET /api/admin/api-keys/scopes
func AdminGetApiKeyScopes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    repo.GetAvailableScopes(),
	})
}

// AdminCreateApiKey creates a new internal API key
// POST /api/admin/api-keys
func AdminCreateApiKey(c *gin.Context) {
	var req struct {
		Name        string   `json:"name" binding:"required"`
		Scopes      []string `json:"scopes" binding:"required"`
		ExpiresAt   int64    `json:"expires_at"` // 0 = never
		Description string   `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	adminId := c.GetInt("id")

	// Only root can create keys with wildcard scope
	for _, scope := range req.Scopes {
		if scope == repo.ScopeAll {
			userRole := c.GetInt("role")
			if userRole != common.RoleRootUser {
				c.JSON(http.StatusForbidden, gin.H{
					"success": false,
					"message": "Only root user can create keys with full access",
				})
				return
			}
			break
		}
	}

	key, apiKey, err := repo.CreateInternalApiKey(req.Name, req.Scopes, adminId, req.ExpiresAt, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create API key: " + err.Error(),
		})
		return
	}

	// IMPORTANT: Only return the full key ONCE during creation
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "API key created successfully. Please save the key now - it won't be shown again!",
		"data": gin.H{
			"key":      key, // Full key - only shown once
			"key_info": apiKey,
		},
	})
}

// AdminDeleteApiKey deletes an API key
// DELETE /api/admin/api-keys/:id
func AdminDeleteApiKey(c *gin.Context) {
	keyId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid key ID",
		})
		return
	}

	err = repo.DeleteInternalApiKey(keyId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete API key: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "API key deleted successfully",
	})
}

// AdminToggleApiKey enables/disables an API key
// PUT /api/admin/api-keys/:id/toggle
func AdminToggleApiKey(c *gin.Context) {
	keyId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid key ID",
		})
		return
	}

	err = repo.ToggleInternalApiKey(keyId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to toggle API key: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "API key toggled successfully",
	})
}

// AdminUpdateApiKey updates an API key
// PUT /api/admin/api-keys/:id
func AdminUpdateApiKey(c *gin.Context) {
	keyId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid key ID",
		})
		return
	}

	var req struct {
		Name        string   `json:"name" binding:"required"`
		Scopes      []string `json:"scopes" binding:"required"`
		ExpiresAt   int64    `json:"expires_at"`
		Description string   `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	// Only root can update keys with wildcard scope
	for _, scope := range req.Scopes {
		if scope == repo.ScopeAll {
			userRole := c.GetInt("role")
			if userRole != common.RoleRootUser {
				c.JSON(http.StatusForbidden, gin.H{
					"success": false,
					"message": "Only root user can assign full access",
				})
				return
			}
			break
		}
	}

	err = repo.UpdateInternalApiKey(keyId, req.Name, req.Scopes, req.ExpiresAt, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update API key: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "API key updated successfully",
	})
}
