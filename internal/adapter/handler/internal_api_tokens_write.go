package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// InternalUpdateToken updates mutable properties on an existing token.
func InternalUpdateToken(c *gin.Context) {
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

	var req struct {
		Name               *string `json:"name"`
		Status             *int    `json:"status"`
		ExpiredTime        *int64  `json:"expired_time"`
		RemainQuota        *int    `json:"remain_quota"`
		UnlimitedQuota     *bool   `json:"unlimited_quota"`
		ModelLimitsEnabled *bool   `json:"model_limits_enabled"`
		ModelLimits        *string `json:"model_limits"`
		AllowIps           *string `json:"allow_ips"`
		Group              *string `json:"group"`
		CrossGroupRetry    *bool   `json:"cross_group_retry"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	if req.Name != nil {
		if err := app.ValidateTokenName(*req.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    err.Error(),
				"error_code": "VALIDATION_FAILED",
			})
			return
		}
		token.Name = *req.Name
	}
	if req.Status != nil {
		token.Status = *req.Status
	}
	if req.ExpiredTime != nil {
		token.ExpiredTime = *req.ExpiredTime
	}
	if req.RemainQuota != nil {
		token.RemainQuota = *req.RemainQuota
	}
	if req.UnlimitedQuota != nil {
		token.UnlimitedQuota = *req.UnlimitedQuota
	}
	if req.ModelLimitsEnabled != nil {
		token.ModelLimitsEnabled = *req.ModelLimitsEnabled
	}
	if req.ModelLimits != nil {
		token.ModelLimits = *req.ModelLimits
	}
	if req.AllowIps != nil {
		token.AllowIps = req.AllowIps
	}
	if req.Group != nil {
		token.Group = *req.Group
	}
	if req.CrossGroupRetry != nil {
		token.CrossGroupRetry = *req.CrossGroupRetry
	}

	if err := token.Update(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update token: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog(fmt.Sprintf("Internal API updated token %d via key: %s", tokenId, keyName))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token updated successfully",
		"data": gin.H{
			"id":     token.Id,
			"name":   token.Name,
			"status": token.Status,
		},
	})
}

// InternalDeleteToken deletes (revokes) a token by ID.
func InternalDeleteToken(c *gin.Context) {
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

	if err := token.Delete(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete token: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog(fmt.Sprintf("Internal API deleted token %d (user=%d) via key: %s", tokenId, token.UserId, keyName))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token deleted successfully",
	})
}
